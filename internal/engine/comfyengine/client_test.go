package comfyengine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// TestWaitForDoneUsesConfiguredTimeout 回归 2026-09-11:单镜超时此前被写死
// 45 分钟,设置页的「单镜渲染超时」形同虚设。现在必须真正生效,且超时错误要能
// 被 errors.Is(err, engine.ErrRenderTimeout) 识别 —— 管线据此决定是否释放显存重试。
func TestWaitForDoneUsesConfiguredTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/history/"):
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/queue":
			_, _ = w.Write([]byte(`{"queue_running":[],"queue_pending":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	started := time.Now()
	// 轮询心跳固定 3s,超短超时会在第一个心跳后被判定,而不是等默认 45 分钟
	_, err := c.WaitForDone(context.Background(), "pid-1", time.Nanosecond, nil)
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if !errors.Is(err, engine.ErrRenderTimeout) {
		t.Fatalf("超时错误必须包装 engine.ErrRenderTimeout,实际 %v", err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("短超时不应等待默认上限,实际等待 %v", elapsed)
	}
}

// TestWaitForDoneZeroTimeoutFallsBack 未指定超时(<=0)时回退默认值,
// 不得退化成「立即超时」;定妆图超时应短于整镜超时。
func TestWaitForDoneZeroTimeoutFallsBack(t *testing.T) {
	if defaultShotTimeout <= 0 {
		t.Fatal("默认单镜超时必须为正")
	}
	if defaultImageTimeout <= 0 || defaultImageTimeout >= defaultShotTimeout {
		t.Fatalf("定妆图超时应为正且短于整镜超时(shot=%v image=%v)", defaultShotTimeout, defaultImageTimeout)
	}
}
