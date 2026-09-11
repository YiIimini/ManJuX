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

// handleSysmon 顶栏 CPU/RAM/GPU 快照(2s 轮询)。
func (s *Server) handleSysmon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, sysmon.Default.Sample())
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
