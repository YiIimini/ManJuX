package comfyengine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
)

// 多视角图像编辑的**进度回调**(甲6)。
//
// 回归背景(2026-09-26 渲染管线审查):EditImage 把进度回调写死传 nil,而多视角补齐是
// "每个角色 3 张、逐角度串行"的链路 —— 用户从点下到出结果既无日志也无百分比,只能靠
// "停止"判断是不是卡死。这里用假 ComfyUI 把回调真的跑一遍:它必须被调用、百分比单调、
// note 原样透传(而不是只在源码里"看起来接上了")。

// editFake 假 ComfyUI:只实现 EditImage 这条路会问到的端点。
type editFake struct {
	creates      int32
	historyCalls int32
	// progressPcts 每次 /history 轮询返回的采样进度(百分比);轮完之后返回 completed。
	// 故意让最后一个值**低于**前一个,用来钉住"交付给界面的百分比只增不减"。
	progressPcts []int
}

func (f *editFake) server(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/system_stats":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"system":{"comfyui_version":"test"}}`))
		case r.URL.Path == "/upload/image":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"up_master.png","subfolder":"","type":"input"}`))
		case r.URL.Path == "/prompt" && r.Method == http.MethodPost:
			atomic.AddInt32(&f.creates, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"prompt_id":"pid-1"}`))
		case strings.HasPrefix(r.URL.Path, "/history/"):
			n := int(atomic.AddInt32(&f.historyCalls, 1))
			w.Header().Set("Content-Type", "application/json")
			if n <= len(f.progressPcts) {
				// 执行中:带一条采样进度消息(progressFromMessages 从它读百分比)
				_, _ = fmt.Fprintf(w,
					`{"pid-1":{"status":{"completed":false,"status_str":"running","messages":[["progress",{"value":%d,"max":100}]]},"outputs":{}}}`,
					f.progressPcts[n-1])
				return
			}
			_, _ = w.Write([]byte(`{"pid-1":{"status":{"completed":true,"status_str":"success"},` +
				`"outputs":{"33":{"images":[{"filename":"edit_00001_.png","subfolder":"manjux/edit","type":"output"}]}}}}`))
		case r.URL.Path == "/view":
			_, _ = w.Write([]byte("PNG-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newEditEngine(t *testing.T, f *editFake) *Engine {
	t.Helper()
	base := f.server(t).URL
	return New(func() config.ComfySettings {
		return config.ComfySettings{
			BaseURL: base,
			// 编辑三件套:非 2.1 文件名 → 走旧的 EditPlus 链路,单张参考图即可
			EditUNet: "qwen_image_edit_2511_fp8mixed.safetensors",
			EditCLIP: "qwen_2.5_vl_7b_fp8_scaled.safetensors",
			EditVAE:  "qwen_image_vae.safetensors",
		}
	})
}

// fastWaitTick 把轮询心跳压到毫秒级(生产是 3 秒),否则这条用例要白等好几个心跳。
func fastWaitTick(t *testing.T) {
	t.Helper()
	old := waitTick
	waitTick = time.Millisecond
	t.Cleanup(func() { waitTick = old })
}

func editRefImage(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "master.png")
	if err := os.WriteFile(p, []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestEditImageForwardsProgress 进度回调必须真的被调用,且交付给界面的百分比单调不减。
func TestEditImageForwardsProgress(t *testing.T) {
	fastWaitTick(t)
	f := &editFake{progressPcts: []int{20, 60, 60, 5}}
	e := newEditEngine(t, f)

	out := filepath.Join(t.TempDir(), "view_side.png")
	type tick struct {
		pct  int
		note string
	}
	var got []tick
	path, err := e.EditImage(context.Background(), ImageEditOptions{
		RefImages: []string{editRefImage(t)},
		Prompt:    "show the character from a side profile",
		OnProgress: func(pct int, note string) {
			got = append(got, tick{pct, note})
		},
	}, out)
	if err != nil {
		t.Fatalf("多视角编辑不该失败:%v", err)
	}
	if path != out {
		t.Fatalf("应返回产物路径 %s,实际 %s", out, path)
	}
	if raw, rerr := os.ReadFile(out); rerr != nil || string(raw) != "PNG-bytes" {
		t.Fatalf("产物没落盘:%v %q", rerr, raw)
	}
	if atomic.LoadInt32(&f.creates) != 1 {
		t.Errorf("应只提交一次任务,实际 %d 次", f.creates)
	}

	if len(got) < 2 {
		t.Fatalf("回调必须被调用(「长时间无日志无百分比」就是这么来的),实际 %d 次:%v", len(got), got)
	}
	for i, g := range got {
		if i > 0 && g.pct < got[i-1].pct {
			t.Errorf("交付给界面的百分比回退了:%v(进度条会倒着走)", got)
			break
		}
		if !strings.HasPrefix(g.note, "渲染中") {
			t.Errorf("note 应原样透传引擎文案(带百分比),实际第 %d 条:%q", i, g.note)
		}
	}
	// 首末百分比要覆盖到假 ComfyUI 给的进度值(证明不是"随便调一下")
	if got[0].pct != 20 {
		t.Errorf("首条回调应为引擎给的 20%%,实际 %v", got)
	}
	if last := got[len(got)-1]; last.pct < 60 {
		t.Errorf("回调应覆盖到 60%%,实际最后一条 = %+v(全部:%v)", last, got)
	}
	// note 原样透传:第 2 条是引擎给的 "渲染中 60%"
	if !strings.Contains(got[1].note, "60%") {
		t.Errorf("note 应原样透传引擎文案,实际第 2 条:%q", got[1].note)
	}
	// 末条是引擎报的 "渲染中 5%"(进度事件乱序):读数被单调化成 60,但**文案不被改写** ——
	// 用户仍然看得见引擎最后报的是 5%,信息没被吞掉。
	if last := got[len(got)-1]; last.note != "渲染中 5%" || last.pct != 60 {
		t.Errorf("末条应为 {60, 渲染中 5%%}(读数单调化 + 文案原样),实际 %+v", last)
	}
}

// TestEditImageWithoutProgressCallback 不接回调时行为完全不变(nil 安全,且不制造空回调)。
func TestEditImageWithoutProgressCallback(t *testing.T) {
	fastWaitTick(t)
	f := &editFake{progressPcts: []int{30, 90}}
	e := newEditEngine(t, f)

	out := filepath.Join(t.TempDir(), "no_cb.png")
	path, err := e.EditImage(context.Background(), ImageEditOptions{
		RefImages: []string{editRefImage(t)},
		Prompt:    "x",
	}, out)
	if err != nil {
		t.Fatalf("不接回调不该影响出图:%v", err)
	}
	if path != out {
		t.Fatalf("应返回产物路径,实际 %s", path)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("产物没落盘:%v", err)
	}
}

// TestEditProgressClampsMonotonic 单调化本身(纯函数):只抬高、不压低,nil 进 nil 出。
func TestEditProgressClampsMonotonic(t *testing.T) {
	if editProgress(nil) != nil {
		t.Error("nil 回调应原样返回 nil,而不是造一个空回调")
	}
	var seen [][2]any
	cb := editProgress(func(pct int, note string) { seen = append(seen, [2]any{pct, note}) })
	cb(2, "排队中")
	cb(20, "执行中")
	cb(5, "渲染中 5%")
	cb(60, "渲染中 60%")
	cb(10, "渲染中 10%")

	if len(seen) != 5 {
		t.Fatalf("5 次上报都该被转发,实际 %d 次", len(seen))
	}
	want := []int{2, 20, 20, 60, 60}
	for i, w := range want {
		if got, _ := seen[i][0].(int); got != w {
			t.Errorf("第 %d 次读数 = %d,期望 %d(只增不减):%v", i, got, w, seen)
		}
	}
	// note 不能被改写:界面上"现在在干什么"全靠它
	if note, _ := seen[2][1].(string); note != "渲染中 5%" {
		t.Errorf("note 应原样透传,实际 %q", note)
	}
}
