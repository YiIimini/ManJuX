package h3api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// 云端引擎的两条**契约**(跨分区消费):
//
//	乙6 实际时长回传 —— engine.Result 增加 ActualFrames / DurationAdjusted,
//	    云端把已算出的"实际提交秒数"换算成帧数填进去(2K/清单/界面据此把"计划"与
//	    "实际"分开记账);
//	乙8 失败不自动花钱重提 —— 云端任何失败都不再包装 engine.ErrRenderTimeout
//	    (那个哨兵会让管线"释放显存后重渲一次",在云端等于再提交一个计费任务),
//	    并把"未自动重试"写进进度事件流。

// cloudOKService 起一套"云端一切正常"的假服务:提交成功、查询成功、成片可下载。
func cloudOKService(t *testing.T) (*Engine, *fakeAPI) {
	t.Helper()
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cloud-bytes"))
	}))
	t.Cleanup(files.Close)
	f := &fakeAPI{queryBody: fmt.Sprintf(`{"task":{"status":"succeed","content":{"url":%q}}}`, files.URL+"/v.mp4")}
	return newTestEngine(t, f, time.Millisecond), f
}

// TestRenderReportsActualFrames 云端必须回传**实际**时长口径(乙6 选项 C)。
//
// 云端只吃整数秒且区间比计划侧更窄:计划 4 秒(96 帧)会被抬成 5 秒。此前这次改写只在
// 进度里告警一句,清单里什么也没留下 —— 用户看到成片比计划长,无从判断是引擎改了还是
// 剪辑错了。ActualFrames 用与本地引擎同一把尺子(24fps)给出实际帧数。
func TestRenderReportsActualFrames(t *testing.T) {
	cases := []struct {
		name         string
		length       int
		wantFrames   int
		wantAdjusted bool
		why          string
	}{
		{"4 秒镜被云端下限抬到 5 秒", 96, 5 * 24, true, "96 帧 = 4 秒 < 云端下限 5 秒"},
		{"5 秒镜原样", 120, 5 * 24, false, "正好在下限上"},
		{"10 秒镜原样", 240, 10 * 24, false, ""},
		{"20 秒镜被压回上限 15 秒", 480, 15 * 24, true, "超过云端上限被 clamp"},
		{"上游没给帧数时按最短 5 秒", 0, 5 * 24, true, "Length=0 也在进度里播报过"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, f := cloudOKService(t)
			job := testJob(t)
			job.Length = c.length
			job.Timeout = 5 * time.Second

			res, err := e.Render(context.Background(), job)
			if err != nil {
				t.Fatalf("成功路径不该报错:%v", err)
			}
			if res.ActualFrames != c.wantFrames {
				t.Errorf("ActualFrames = %d,期望 %d(%s)", res.ActualFrames, c.wantFrames, c.why)
			}
			if res.DurationAdjusted != c.wantAdjusted {
				t.Errorf("DurationAdjusted = %v,期望 %v(计划 %d 帧 → 实际 %d 帧)",
					res.DurationAdjusted, c.wantAdjusted, c.length, res.ActualFrames)
			}
			// 云端只出整数秒:帧数必须是 24 的整数倍(否则说明有人把计划帧数原样填了进来)
			if res.ActualFrames%24 != 0 {
				t.Errorf("ActualFrames = %d 不是整秒:%d 帧不是 24 的整数倍", res.ActualFrames, res.ActualFrames)
			}
			if n := atomic.LoadInt32(&f.creates); n != 1 {
				t.Errorf("成功路径应只提交一次,实际 %d 次", n)
			}
		})
	}
}

// TestCloudFailureNeverSignalsRetry 乙8:云端失败**从不**给出"可重试"信号。
//
// 这是"不自动花钱重提"的落点。管线的重试判据只有一条 ——
// `errors.Is(err, engine.ErrRenderTimeout)`(pipeline/stages_render.go 的"释放显存
// 后原地重试一次"),而那次重试对云端就是**再提交一个计费任务**:
// CapsOf("api") 是零值(caps.VRAM=false),重试前什么都没释放,纯属重复花钱与重复排队。
// 所以云端每一条失败路径都必须满足两件事:
//
//	① errors.Is(err, engine.ErrRenderTimeout) == false  → 管线失败即停;
//	② 只 create 过一次                                        → 引擎自己也没偷偷重提;
//	③ 进度事件流里出现"不会自动重提"                        → 用户知道没有第二次计费。
//
// (本地引擎的重试策略不变:那是本地算力,重试不花钱,见 comfyengine 的超时哨兵用例。)
func TestCloudFailureNeverSignalsRetry(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) (*Engine, *fakeAPI, *engine.Job)
	}{
		{
			name: "提交被拒(401)",
			build: func(t *testing.T) (*Engine, *fakeAPI, *engine.Job) {
				f := &fakeAPI{createStatus: http.StatusUnauthorized}
				e := newTestEngine(t, f, time.Millisecond)
				job := testJob(t)
				job.Timeout = time.Second
				return e, f, &job
			},
		},
		{
			name: "查询鉴权失败(401)",
			build: func(t *testing.T) (*Engine, *fakeAPI, *engine.Job) {
				f := &fakeAPI{queryStatus: http.StatusUnauthorized}
				e := newTestEngine(t, f, time.Millisecond)
				job := testJob(t)
				job.Timeout = time.Second
				return e, f, &job
			},
		},
		{
			name: "超时(远端仍在跑)",
			build: func(t *testing.T) (*Engine, *fakeAPI, *engine.Job) {
				f := &fakeAPI{} // 永远 running
				e := newTestEngine(t, f, time.Millisecond)
				job := testJob(t)
				job.Timeout = 50 * time.Millisecond
				return e, f, &job
			},
		},
		{
			name: "连续查询失败(5xx)",
			build: func(t *testing.T) (*Engine, *fakeAPI, *engine.Job) {
				f := &fakeAPI{queryStatus: http.StatusInternalServerError}
				e := newTestEngine(t, f, 2*time.Millisecond)
				job := testJob(t)
				job.Timeout = time.Second
				return e, f, &job
			},
		},
		{
			name: "下载成片失败",
			build: func(t *testing.T) (*Engine, *fakeAPI, *engine.Job) {
				broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "gone", http.StatusNotFound)
				}))
				t.Cleanup(broken.Close)
				f := &fakeAPI{queryBody: fmt.Sprintf(`{"task":{"status":"succeed","content":{"url":%q}}}`, broken.URL+"/v.mp4")}
				e := newTestEngine(t, f, time.Millisecond)
				job := testJob(t)
				job.Timeout = time.Second
				return e, f, &job
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, f, job := c.build(t)
			var notes []string
			job.OnProgress = func(_ int, note string) { notes = append(notes, note) }

			_, err := e.Render(context.Background(), *job)
			if err == nil {
				t.Fatal("失败路径必须返回错误(失败即停,而不是静默继续)")
			}
			if errors.Is(err, engine.ErrRenderTimeout) {
				t.Errorf("云端失败不能带 ErrRenderTimeout:管线会据此再提交一次(重提 = 再花一次额度),实际 %v", err)
			}
			if n := atomic.LoadInt32(&f.creates); n != 1 {
				t.Errorf("失败路径只能提交一次,实际 %d 次(第二次提交就是第二次计费)", n)
			}
			joined := strings.Join(notes, "|")
			if !strings.Contains(joined, "不会自动重提") {
				t.Errorf("「未自动重试」必须写进进度事件流(用户会怀疑它偷偷又花了钱),实际:%q", joined)
			}
		})
	}
}

// TestTimeoutErrorTextTellsNoResubmit 超时文案三件事都要说清:超时、已取消远端、不会自动重提。
//
// 这条链路里错误文本就是**用户唯一能看到的东西**(管线把它写进镜头状态与运行日志),
// 少了"已请求取消"用户会担心远端继续跑着计费,少了"不会自动重提"用户不知道要不要自己再点一次。
func TestTimeoutErrorTextTellsNoResubmit(t *testing.T) {
	e := newTestEngine(t, &fakeAPI{}, time.Millisecond)
	job := testJob(t)
	job.Timeout = 50 * time.Millisecond

	_, err := e.Render(context.Background(), job)
	if err == nil {
		t.Fatal("应超时")
	}
	msg := err.Error()
	for _, want := range []string{"超时", "取消", "不会自动重提", "task-1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("超时错误应提到 %q,实际:%s", want, msg)
		}
	}
}
