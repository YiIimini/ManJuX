package comfyengine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// TestWaitForDoneFailsFastWhenEngineGone 引擎死亡/不可达时必须快速判死。
//
// 回归(2026-09-26 渲染管线审查):ComfyUI 被 OOM 杀掉或崩溃后,History 与 Queue 会
// **一路**失败,而旧实现只 `continue` 容忍网络抖动 —— 于是界面持续显示「排队中 /
// 执行中 · 已 N 分」,最长空转到执行上限 45 分钟(还没轮到执行则排队上限 90 分钟)
// 才抛超时。一次崩溃代价接近一小时机时,报障信息还把"引擎已死"误导成"在排队"。
//
// 这里把连续失败阈值压到 1,免得用例白等 18 秒。
func TestWaitForDoneFailsFastWhenEngineGone(t *testing.T) {
	old := maxProbeFails
	maxProbeFails = 1
	defer func() { maxProbeFails = old }()

	// 假"死掉的引擎":/history 与 /queue 都返回非 JSON 的 500(真机上就是连接被拒
	// 或 ComfyUI 已退出),只有 /system_stats 也一并失败。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "engine gone", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	var mu sync.Mutex
	var notes []string
	start := time.Now()
	_, err := c.WaitForDone(context.Background(), "pid-1", time.Minute, func(_ int, note string) {
		mu.Lock()
		notes = append(notes, note)
		mu.Unlock()
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("引擎不可达时必须判失败,而不是一路空转")
	}
	if !errors.Is(err, engine.ErrRenderTimeout) {
		t.Fatalf("判死错误必须包装 engine.ErrRenderTimeout(管线据此释放显存并重试),实际 %v", err)
	}
	if !strings.Contains(err.Error(), "无响应") {
		t.Errorf("错误信息要说清是「引擎没回应」,实际:%v", err)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("应在连续失败后立刻判死,实际等了 %v(默认上限 45 分钟)", elapsed)
	}
	// 文案不得谎报"排队中":引擎都连不上了,说自己排在队里只会把人引向错误方向
	mu.Lock()
	defer mu.Unlock()
	for _, n := range notes {
		if strings.Contains(n, "排队中") {
			t.Errorf("引擎不可达时不能报「排队中」,实际进度文案:%v", notes)
			break
		}
	}
}

// TestWaitForDoneToleratesSingleProbeFailure 单次探测失败仍是"网络抖动",不得判死。
//
// 判据是**连续**失败:偶发丢包/一次 GC 停顿不该把正常渲染判成引擎死亡并触发重试。
func TestWaitForDoneToleratesSingleProbeFailure(t *testing.T) {
	old := maxProbeFails
	maxProbeFails = 2
	defer func() { maxProbeFails = old }()

	var mu sync.Mutex
	fails := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/history/"):
			mu.Lock()
			fails++
			first := fails == 1
			mu.Unlock()
			if first {
				http.Error(w, "blip", http.StatusInternalServerError) // 只抖一次
				return
			}
			_, _ = w.Write([]byte(`{}`)) // 之后一直"还在执行"
		case r.URL.Path == "/queue":
			_, _ = w.Write([]byte(`{"queue_running":[[1,"pid-1"]],"queue_pending":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c := NewClient(srv.URL)
	if _, err := c.WaitForDone(ctx, "pid-1", time.Minute, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("只抖一次不该判死(应继续等到 ctx 到期),实际 %v", err)
	}
}
