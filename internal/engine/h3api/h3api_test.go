package h3api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// h3api 的首个测试文件(审计 P1-3:此前 0 测试,而这条链路**直接花钱**)。
//
// 三个必须钉住的点(审计 P0-6):
//  1. 鉴权失败必须**立刻失败**,不能被"网络抖动容忍"吞掉 —— 否则无效 Key 白等 20 分钟,
//     报的还是"超时",管线据此再跑一轮 → 一次鉴权失败 = 两个计费任务;
//  2. 超时必须**取消远端任务**,否则云端继续跑并计费(与文档宣称相反);
//  3. 失败路径只能 create 一次(不能悄悄重提)。

// fakeAPI 假云端:按脚本决定每个端点的行为。
type fakeAPI struct {
	createStatus int
	queryStatus  int
	queryBody    string
	creates      int32
	deletes      int32
	queries      int32
}

func (f *fakeAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/video_generation":
			atomic.AddInt32(&f.creates, 1)
			if f.createStatus != 0 && f.createStatus != http.StatusOK {
				w.WriteHeader(f.createStatus)
				_, _ = w.Write([]byte(`{"error":"denied"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"task-1"}`))
		case r.Method == http.MethodDelete:
			atomic.AddInt32(&f.deletes, 1)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			atomic.AddInt32(&f.queries, 1)
			if f.queryStatus != 0 && f.queryStatus != http.StatusOK {
				w.WriteHeader(f.queryStatus)
				_, _ = w.Write([]byte(`{"error":"denied"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			body := f.queryBody
			if body == "" {
				body = `{"task":{"status":"running"}}`
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newTestEngine(t *testing.T, f *fakeAPI, poll time.Duration) *Engine {
	t.Helper()
	ts := f.server(t)
	base := ts.URL
	e := New(func() config.MiniMaxSettings {
		return config.MiniMaxSettings{BaseURL: base, APIKey: "k", Model: "MiniMax-H3", Resolution: "768P"}
	})
	e.pollEvery = poll
	return e
}

func testJob(t *testing.T) engine.Job {
	t.Helper()
	return engine.Job{
		ProjectID: "p", ShotID: 1, Prompt: "x", Mode: "t2va",
		Width: 768, Height: 1344, Length: 120,
		OutputDir: t.TempDir(), OutputName: "shot_001",
	}
}

// TestRenderFailsFastOnAuthError 401 必须立刻失败:查询次数极少、错误**不是**
// ErrRenderTimeout(否则管线会当成显存挤压再跑一轮 = 第二次计费)。
func TestRenderFailsFastOnAuthError(t *testing.T) {
	f := &fakeAPI{queryStatus: http.StatusUnauthorized}
	e := newTestEngine(t, f, 5*time.Millisecond)

	start := time.Now()
	_, err := e.Render(context.Background(), testJob(t))
	if err == nil {
		t.Fatal("401 应让 Render 失败")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("鉴权失败必须立刻返回,实际耗时 %v", time.Since(start))
	}
	if errors.Is(err, engine.ErrRenderTimeout) {
		t.Fatal("鉴权失败绝不能包装成 ErrRenderTimeout —— 管线会据此再跑一轮,重新计费")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("错误里应带上 HTTP 状态码,便于用户判断,实际 %v", err)
	}
	if n := atomic.LoadInt32(&f.queries); n > 2 {
		t.Errorf("鉴权失败不应反复查询,实际查了 %d 次", n)
	}
	if n := atomic.LoadInt32(&f.creates); n != 1 {
		t.Errorf("失败路径只能创建一次任务,实际 %d 次", n)
	}
	// 失败后必须取消远端任务(尽力而为,给 goroutine 一点时间)
	waitFor(t, &f.deletes, 1)
}

// TestRenderTimeoutCancelsRemote 超时必须取消远端任务,否则云端继续跑并计费。
func TestRenderTimeoutCancelsRemote(t *testing.T) {
	f := &fakeAPI{} // 永远 running
	e := newTestEngine(t, f, 5*time.Millisecond)

	job := testJob(t)
	job.Timeout = 60 * time.Millisecond
	_, err := e.Render(context.Background(), job)
	if err == nil || !errors.Is(err, engine.ErrRenderTimeout) {
		t.Fatalf("应返回超时错误,实际 %v", err)
	}
	waitFor(t, &f.deletes, 1)
	if n := atomic.LoadInt32(&f.creates); n != 1 {
		t.Errorf("超时不应重新提交任务,实际 %d 次", n)
	}
}

// TestRenderSucceedsAndDoesNotCancel 成功路径:下载成片、不取消远端(任务已结束)。
func TestRenderSucceedsAndDoesNotCancel(t *testing.T) {
	var filesSrv *httptest.Server
	f := &fakeAPI{}
	ts := f.server(t)
	filesSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("video-bytes"))
	}))
	t.Cleanup(filesSrv.Close)
	f.queryBody = fmt.Sprintf(`{"task":{"status":"succeed","content":{"url":%q}}}`, filesSrv.URL+"/v.mp4")

	base := ts.URL
	e := New(func() config.MiniMaxSettings {
		return config.MiniMaxSettings{BaseURL: base, APIKey: "k", Model: "MiniMax-H3"}
	})
	e.pollEvery = 5 * time.Millisecond

	job := testJob(t)
	job.Timeout = 5 * time.Second
	res, err := e.Render(context.Background(), job)
	if err != nil {
		t.Fatalf("成功路径不该报错:%v", err)
	}
	if res == nil || res.VideoPath == "" {
		t.Fatal("应返回产物路径")
	}
	raw, rerr := os.ReadFile(res.VideoPath)
	if rerr != nil || string(raw) != "video-bytes" {
		t.Fatalf("成片内容不对:%v %q", rerr, raw)
	}
	if res.TaskID != "task-1" {
		t.Errorf("应回传任务 id,实际 %q", res.TaskID)
	}
	// 成功之后不该再取消(任务已 succeed)
	time.Sleep(80 * time.Millisecond)
	if n := atomic.LoadInt32(&f.deletes); n != 0 {
		t.Errorf("成功路径不应调用取消,实际 %d 次", n)
	}
}

// TestConsecutiveQueryFailuresGiveUp 连续失败要有上限:5xx 一直返也必须在 3 次内放弃,
// 不能无限轮询到超时(用户等的每一秒都是"看起来在跑")。
func TestConsecutiveQueryFailuresGiveUp(t *testing.T) {
	f := &fakeAPI{queryStatus: http.StatusInternalServerError}
	e := newTestEngine(t, f, 2*time.Millisecond)

	job := testJob(t)
	job.Timeout = 3 * time.Second
	_, err := e.Render(context.Background(), job)
	if err == nil {
		t.Fatal("连续 5xx 应失败")
	}
	if n := atomic.LoadInt32(&f.queries); n != 3 {
		t.Errorf("应在连续 3 次失败后放弃,实际查了 %d 次", n)
	}
}

// TestRegenerate2KSharesPollingPolicy 2K 再生成与主渲染共用同一套等待逻辑:
// 鉴权失败同样立刻失败、同样取消远端(两处各写一份正是审计 P0-6 里"修一处漏一处"的根源)。
func TestRegenerate2KSharesPollingPolicy(t *testing.T) {
	f := &fakeAPI{queryStatus: http.StatusForbidden}
	e := newTestEngine(t, f, 2*time.Millisecond)

	out := filepath.Join(t.TempDir(), "out.mp4")
	src := filepath.Join(t.TempDir(), "base.mp4")
	if err := os.WriteFile(src, []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := e.Regenerate2K(context.Background(), src, "p", nil, out, nil)
	if err == nil {
		t.Fatal("403 应让 2K 再生成失败")
	}
	if n := atomic.LoadInt32(&f.queries); n > 2 {
		t.Errorf("鉴权失败不该反复查询,实际 %d 次", n)
	}
	waitFor(t, &f.deletes, 1)
}

// TestCreateAuthErrorIsActionable 提交阶段被拒时也要给出可执行的说明,
// 而不是一句裸的 HTTP 码。
func TestCreateAuthErrorIsActionable(t *testing.T) {
	f := &fakeAPI{createStatus: http.StatusUnauthorized}
	e := newTestEngine(t, f, 2*time.Millisecond)

	_, err := e.Render(context.Background(), testJob(t))
	if err == nil {
		t.Fatal("提交被拒应失败")
	}
	var ae *apiError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized {
		t.Fatalf("应返回可判别的 apiError,实际 %T %v", err, err)
	}
}

// TestRatioAndDurationClamps 参数换算:ratio 必须显式给出(ref2va 缺省会落 16:9),
// 时长落在 5-15 秒(云端硬约束)。
func TestRatioAndDurationClamps(t *testing.T) {
	cases := []struct {
		w, h int
		want string
	}{
		{768, 1344, "9:16"},
		{1344, 768, "16:9"},
		{1024, 1024, "1:1"},
		{960, 1280, "3:4"},
	}
	for _, c := range cases {
		if got := ratioFor(c.w, c.h); got != c.want {
			t.Errorf("ratioFor(%d,%d) = %q,期望 %q", c.w, c.h, got, c.want)
		}
	}
	for in, want := range map[int]int{1: 5, 4: 5, 6: 6, 15: 15, 30: 15} {
		if got := clampDuration(in); got != want {
			t.Errorf("clampDuration(%d) = %d,期望 %d", in, got, want)
		}
	}
}

// TestRequestCarriesBearerToken 鉴权头必须真的带上(这条链路的凭据一旦漏发,
// 表现是"云端返回 401"而用户以为 Key 没配)。
func TestRequestCarriesBearerToken(t *testing.T) {
	got := make(chan string, 2)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"t"}`))
	}))
	t.Cleanup(ts.Close)
	base := ts.URL
	e := New(func() config.MiniMaxSettings {
		return config.MiniMaxSettings{BaseURL: base, APIKey: "secret-key", Model: "MiniMax-H3"}
	})
	e.pollEvery = time.Hour

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _ = e.Render(ctx, testJob(t))
	select {
	case h := <-got:
		if h != "Bearer secret-key" {
			t.Errorf("Authorization 头应为 Bearer secret-key,实际 %q", h)
		}
	case <-time.After(time.Second):
		t.Fatal("没有收到任何请求")
	}
}

// TestRegenerate2KRejectsBadModel Max 模型不支持 2K:必须在提交前拦住(省一次计费)。
func TestRegenerate2KRejectsBadModel(t *testing.T) {
	f := &fakeAPI{}
	ts := f.server(t)
	base := ts.URL
	e := New(func() config.MiniMaxSettings {
		return config.MiniMaxSettings{BaseURL: base, APIKey: "k", Model: "MiniMax-H3-Max"}
	})
	dir := t.TempDir()
	src := filepath.Join(dir, "base.mp4")
	if err := os.WriteFile(src, []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := e.Regenerate2K(context.Background(), src, "p", nil, filepath.Join(dir, "o.mp4"), nil)
	if err == nil {
		t.Fatal("Max 模型应被拒绝")
	}
	if n := atomic.LoadInt32(&f.creates); n != 0 {
		t.Errorf("应在提交前拦住,实际创建了 %d 个任务", n)
	}
}

// TestSubmitDurationReportsRaise 纯函数面:被云端区间改写时必须给出 adjusted=true。
//
// 96 帧 = 4 秒,在计划侧(PlanMinShotSeconds=4)完全合法,云端的下限却是 5 秒 ——
// 这就是"静默抬升"的入口:同一条 plan 走云端每条 4 秒镜都变长,走本地却是
// AlignLength 后的 4.46 秒,两条路产物时长不一致。
func TestSubmitDurationReportsRaise(t *testing.T) {
	cases := []struct {
		name     string
		frames   int
		sec      int
		adjusted bool
	}{
		{"4 秒镜被抬到云端下限", 96, 5, true},
		{"正好 5 秒不动", 120, 5, false},
		{"10 秒不动", 240, 10, false},
		{"上游没给帧数", 0, 5, true},
		{"15 秒不动", 360, 15, false},
		{"超过云端上限被压回", 480, 15, true},
	}
	for _, c := range cases {
		sec, adjusted := submitDuration(c.frames)
		if sec != c.sec || adjusted != c.adjusted {
			t.Errorf("%s: submitDuration(%d) = (%d, %v),期望 (%d, %v)",
				c.name, c.frames, sec, adjusted, c.sec, c.adjusted)
		}
	}
}

// TestRenderWarnsWhenCloudDurationIsRaised 静默抬升必须变成**可见**(审计:云端时长静默抬升)。
//
// 断言两件事:①真正提交给云端的 duration 是被改写后的值(5,而不是计划的 4);
// ②OnProgress 里出现"计划 N 秒 → 云端 M 秒"的播报 —— 没有这句,用户从日志与界面
// 都看不出这一镜为什么比计划长,只有换回本地引擎渲染时才会暴露。
func TestRenderWarnsWhenCloudDurationIsRaised(t *testing.T) {
	var sent atomic.Value // 提交出去的请求体(字符串)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			sent.Store(string(raw))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"t"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden) // 查询一律拒绝:本条不关心后续
	}))
	t.Cleanup(ts.Close)
	base := ts.URL
	e := New(func() config.MiniMaxSettings {
		return config.MiniMaxSettings{BaseURL: base, APIKey: "k", Model: "MiniMax-H3"}
	})
	e.pollEvery = time.Millisecond

	job := testJob(t)
	job.Length = 96 // 4 秒镜
	job.Timeout = 300 * time.Millisecond
	var notes []string
	job.OnProgress = func(_ int, note string) { notes = append(notes, note) }
	_, _ = e.Render(context.Background(), job) // 后续 403 失败与本节无关

	raw, _ := sent.Load().(string)
	if raw == "" {
		t.Fatal("没有捕获到提交请求")
	}
	var body struct {
		Duration int `json:"duration"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("解析提交体失败: %v\n%s", err, raw)
	}
	if body.Duration != 5 {
		t.Errorf("云端下限为 5 秒,提交的 duration 应为 5,实际 %d", body.Duration)
	}
	joined := strings.Join(notes, "|")
	if !strings.Contains(joined, "计划 4 秒") || !strings.Contains(joined, "云端按 5 秒") {
		t.Errorf("时长被改写必须如实播报「计划 N 秒 → 云端按 M 秒」,实际进度文案:%q", joined)
	}
}

// TestRenderWarnsWhenLengthMissing 上游没给帧数(Length=0)时同样要播报 ——
// 这类调用曾是静默的:引擎按 AlignLength(0)=5 帧建图编码,接口却返回 ok。
func TestRenderWarnsWhenLengthMissing(t *testing.T) {
	var sent atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			sent.Store(string(raw))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"t"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(ts.Close)
	base := ts.URL
	e := New(func() config.MiniMaxSettings {
		return config.MiniMaxSettings{BaseURL: base, APIKey: "k", Model: "MiniMax-H3"}
	})
	e.pollEvery = time.Millisecond

	job := testJob(t)
	job.Length = 0
	job.Timeout = 300 * time.Millisecond
	var notes []string
	job.OnProgress = func(_ int, note string) { notes = append(notes, note) }
	_, _ = e.Render(context.Background(), job)

	if raw, _ := sent.Load().(string); raw == "" {
		t.Fatal("没有捕获到提交请求")
	}
	if joined := strings.Join(notes, "|"); !strings.Contains(joined, "未给出渲染帧数") {
		t.Errorf("未指定帧数必须播报(否则成片时长不受镜头计划约束而无人知晓),实际:%q", joined)
	}
}

// TestRenderWarnsWhenSeedRequested M32:云端不支持 seed(H3 请求体里没有该字段,
// 产物不可复现),而"固定种子/单镜 seed"在界面上是真能设的 —— 静默失效必须变成可见。
func TestRenderWarnsWhenSeedRequested(t *testing.T) {
	var sent atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			sent.Store(string(raw))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"t"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(ts.Close)
	base := ts.URL
	e := New(func() config.MiniMaxSettings {
		return config.MiniMaxSettings{BaseURL: base, APIKey: "k", Model: "MiniMax-H3"}
	})
	e.pollEvery = time.Millisecond

	job := testJob(t)
	job.Seed = 424242
	job.Timeout = 300 * time.Millisecond
	var notes []string
	job.OnProgress = func(_ int, note string) { notes = append(notes, note) }
	_, _ = e.Render(context.Background(), job)

	if joined := strings.Join(notes, "|"); !strings.Contains(joined, "不支持 seed") {
		t.Errorf("设了 seed 就必须播报云端不支持,实际进度文案:%q", joined)
	}
	// 请求体里本来就不该有 seed(有的话说明另一处改了契约,这条断言会提醒)
	if raw, _ := sent.Load().(string); strings.Contains(raw, "seed") {
		t.Errorf("云端请求体不含 seed,实际:%s", raw)
	}

	// 没设 seed 时保持安静(不制造噪声)
	job2 := testJob(t)
	job2.Timeout = 300 * time.Millisecond
	var notes2 []string
	job2.OnProgress = func(_ int, note string) { notes2 = append(notes2, note) }
	_, _ = e.Render(context.Background(), job2)
	if joined := strings.Join(notes2, "|"); strings.Contains(joined, "不支持 seed") {
		t.Errorf("未设 seed 时不该播报 seed,实际:%q", joined)
	}
}

// waitFor 等一个原子计数达到目标(失败路径的取消是异步发出的)。
func waitFor(t *testing.T, counter *int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(counter) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待计数达到 %d 超时(实际 %d)", want, atomic.LoadInt32(counter))
}

var _ = json.Marshal // 保底:文件内使用 json(便于日后扩展断言时不改 import 块)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
