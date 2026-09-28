// syswin.go 顶栏系统监控与无边框窗口控制路由。
package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/sysmon"
	"github.com/YiIimini/ManJuX/internal/winctl"
)

// handleSysmon 顶栏 CPU/RAM/GPU 快照。
//
// ## 轮询间隔是**自适应**的（2026-09-29 性能审计 #2）
//
// 前端原来固定 2 秒拉一次，而每次采样会让后端 spawn 一个 `nvidia-smi`
// （实测空载 40~50 ms，满载更久）—— 6 小时渲染 ≈ **1.08 万次进程创建**，
// 常驻 2~2.5% 单核。而渲染期间这些数字每 2 秒变一次和每 10 秒变一次**没有任何区别**。
//
// 所以这里把"有没有管线在跑"一并交给前端（`busy`）：它据此把间隔在
// **跑时 10 秒 / 空闲 3 秒**之间切。用户在看仪表盘时仍然跟手，渲染时把 CPU 让出去。
func (s *Server) handleSysmon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snap := sysmon.Default.Sample()
	writeJSON(w, http.StatusOK, map[string]any{
		"cpu": snap.CPU, "cores": snap.Cores, "ramPct": snap.RAMPct,
		"ramUsed": snap.RAMUsed, "ramTotal": snap.RAMTotal, "ramAvail": snap.RAMAvail,
		"gpu": snap.GPU, "coreN": snap.CoreN,
		// busy 让前端决定下次多久后回来（见上面的说明）
		"busy": s.pipe != nil && s.pipe.HasRunning(),
	})
}

// handleWindowAction 无边框窗口控制:POST /api/window/{action}
// action = minimise | maximise | close | force-close
func (s *Server) handleWindowAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "minimise":
		winctl.Default.Minimise()
	case "maximise":
		max := winctl.Default.ToggleMaximise()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "maximised": max})
		return
	case "close":
		// 走确认链:弹前端确认框(管线运行中必须人工确认)
		winctl.Default.RequestQuit()
	case "hide":
		// 最小化后台运行(再点应用图标自动唤回)
		winctl.Default.Hide()
	case "show":
		winctl.Default.Show()
	case "force-close":
		winctl.Default.ForceQuit()
	default:
		writeErr(w, http.StatusBadRequest, errors.New("未知窗口操作: "+action))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLogs 轮询增量日志:GET /api/logs?after=N(桌面 webview 的 SSE 兜底)。
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	events, next := s.bus.Recent(after)
	if events == nil {
		events = []pipeline.Event{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "next": next})
}
