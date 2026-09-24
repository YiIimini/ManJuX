package comfyengine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// QueueKnown 契约测试(审计 P0-5)。
//
// 显存护栏要回答的问题是"现在能不能释放",而它唯一安全的依据是"队列确实查到了、
// 且确实为空"。旧实现里 /queue 查询失败时 Running/Pending 保持 0,与真正的空闲
// 在返回值上完全同构 —— 护栏于是会在引擎**忙到答不上探测**时把权重卸掉。
// 这里把"查到/查不到"这件事在引擎层钉死。
func adminFake(t *testing.T, queueStatus int) (*Engine, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/system_stats":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"system":{"comfyui_version":"test"},"devices":[{"name":"fake-gpu"}]}`))
		case r.URL.Path == "/queue":
			if queueStatus != http.StatusOK {
				w.WriteHeader(queueStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"queue_running":[],"queue_pending":[]}`))
		case r.URL.Path == "/object_info" || len(r.URL.Path) > len("/object_info/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	base := ts.URL
	return New(func() config.ComfySettings { return config.ComfySettings{BaseURL: base} }), ts
}

func TestAdminQueueKnownFalseWhenQueueQueryFails(t *testing.T) {
	e, _ := adminFake(t, http.StatusInternalServerError)
	st, err := e.Admin(context.Background())
	if err != nil {
		t.Fatalf("Admin 不应因 /queue 失败而报错(它要给管理页一个可渲染的状态):%v", err)
	}
	if st == nil || !st.Alive {
		t.Fatal("引擎本身是活的,Alive 应为 true")
	}
	if st.QueueKnown {
		t.Fatal("/queue 返回 500 时 QueueKnown 必须为 false —— 否则护栏会把'答不上来'当'空闲'")
	}
	if st.Running+st.Pending != 0 {
		t.Fatalf("查不到队列时数字应保持 0(这正是需要 QueueKnown 的原因),实际 %d+%d", st.Running, st.Pending)
	}
}

func TestAdminQueueKnownTrueWhenQueueQuerySucceeds(t *testing.T) {
	e, _ := adminFake(t, http.StatusOK)
	st, err := e.Admin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.QueueKnown {
		t.Fatal("/queue 正常返回时 QueueKnown 必须为 true,否则护栏永远不会释放显存")
	}
	if st.Running+st.Pending != 0 {
		t.Fatalf("空队列应为 0+0,实际 %d+%d", st.Running, st.Pending)
	}
}
