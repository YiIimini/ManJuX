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

// TestWaitForDoneUsesConfiguredTimeout 回归 2026-09-11 + 2026-09-12:
// ①配置的单镜超时必须真正生效,且超时错误要能被 errors.Is(err, engine.ErrRenderTimeout)
// 识别 —— 管线据此决定是否释放显存重试;
// ②但超时**只对"已开始执行"的时间**生效:排队等待不吃执行预算。
// 旧实现从提交那刻起算,定妆照排在长视频任务后面排 10 分钟队就必被判"渲染超时",
// 实测整批角色与场景定妆照被误杀、镜头缺参考图降级 t2va(2026-09-12 用户报障)。
func TestWaitForDoneUsesConfiguredTimeout(t *testing.T) {
	newSrv := func(queueBody string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasPrefix(r.URL.Path, "/history/"):
				_, _ = w.Write([]byte(`{}`))
			case r.URL.Path == "/queue":
				_, _ = w.Write([]byte(queueBody))
			default:
				_, _ = w.Write([]byte(`{}`))
			}
		}))
	}

	// ① 任务还在排队:1 纳秒的执行上限也不该判超时(排队不计时),
	//    只受调用方 ctx 约束 —— 这里用 8 秒 ctx 收尾,验证它一直等到 ctx 到期。
	pendSrv := newSrv(`{"queue_running":[],"queue_pending":[[1,"pid-1"]]}`)
	defer pendSrv.Close()
	pc := NewClient(pendSrv.URL)
	pctx, pcancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer pcancel()
	if _, err := pc.WaitForDone(pctx, "pid-1", time.Nanosecond, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("排队中的任务不应被判执行超时(排队不吃执行预算),实际 %v", err)
	}

	// ② 任务已开始执行:1 纳秒上限应在下一次心跳被判超时,且包装 ErrRenderTimeout
	runSrv := newSrv(`{"queue_running":[[1,"pid-1"]],"queue_pending":[]}`)
	defer runSrv.Close()
	c := NewClient(runSrv.URL)
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
