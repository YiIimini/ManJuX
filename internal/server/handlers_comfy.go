package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/browser"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/sysexec"
	"github.com/YiIimini/ManJuX/internal/sysmon"
	"github.com/YiIimini/ManJuX/internal/textutil"
)

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	id, ch, replay := s.bus.Subscribe()
	defer s.bus.Unsubscribe(id)

	send := func(ev string, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, data)
		fl.Flush()
	}
	// 回放历史
	for _, ev := range replay {
		raw, _ := json.Marshal(ev)
		send("log", string(raw))
	}
	send("hello", `{"ok":true}`)

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			raw, _ := json.Marshal(ev)
			send("log", string(raw))
		case <-heartbeat.C:
			send("ping", `{"t":`+strconv.FormatInt(time.Now().Unix(), 10)+`}`)
		}
	}
}

// ---------- 打开目录(白名单)----------

// openExternalURL 系统默认浏览器打开外链(仅 https,防任意协议)。
func (s *Server) openExternalURL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	// 【2026-09-29 审计 #10】原来是 `_ = readBody(r, &in)` —— 请求体坏了（截断/非 JSON）
	// 时 `in.URL` 保持零值，于是报出来的错是"仅支持 https 链接"，把一个**格式问题**
	// 说成了**内容问题**，用户去改链接怎么改都不对。
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体格式错误:%w", err))
		return
	}
	if !strings.HasPrefix(in.URL, "https://") {
		writeErr(w, 400, fmt.Errorf("仅支持 https 链接"))
		return
	}
	if err := browser.OpenDefault(in.URL); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) openInExplorer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string `json:"path"`
		Project string `json:"project"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	var target, allow string
	if in.Project != "" {
		p, ok := s.store.Get(in.Project)
		if !ok {
			writeErr(w, 404, fmt.Errorf("项目不存在"))
			return
		}
		allow = s.store.Dir(p.ID)
		// 相对路径 → 项目目录内解析(拒绝穿越)
		rel := path.Clean(in.Path)
		if strings.Contains(rel, "..") || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
			writeErr(w, 403, fmt.Errorf("非法路径"))
			return
		}
		target = filepath.Join(allow, filepath.FromSlash(rel))
	} else {
		target = filepath.Clean(in.Path)
		// 允许 DataDir 或 ComfyUI 根目录(引擎管理页的目录说明需要打开模型/输出等子目录)
		allow = s.cfg.Get().DataDir
	}
	if allow == "" {
		writeErr(w, 403, fmt.Errorf("路径超出允许范围"))
		return
	}
	if !strings.HasPrefix(target, filepath.Clean(allow)) {
		// 不在 DataDir 下 → 检查是否在 ComfyUI 根目录内
		comfyRoot := filepath.Clean(s.cfg.Get().Comfy.RootDir)
		if comfyRoot != "" && comfyRoot != "." && strings.HasPrefix(target, comfyRoot) {
			// 在 ComfyUI 根下,放行
		} else {
			writeErr(w, 403, fmt.Errorf("路径超出允许范围"))
			return
		}
	}
	if _, err := os.Stat(target); err != nil {
		writeErr(w, 404, err)
		return
	}
	// explorer 怪癖:/select,<目录> 在已有实例时常静默不弹窗(2026-09-09 实测
	// 窗口数不变);目录改为直接打开,文件保持 /select 定位选中。
	var cmd *exec.Cmd
	if fi, err := os.Stat(target); err == nil && fi.IsDir() {
		cmd = sysexec.Hide(exec.Command("explorer.exe", target))
	} else {
		// explorer 的 /select 必须与路径连成一个参数,拆开会打开默认文档目录
		cmd = sysexec.Hide(exec.Command("explorer.exe", "/select,"+target))
	}
	_ = cmd.Start()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// testConn 连通性测试:body {target: "llm"|"comfy"|"minimax"}。
func (s *Server) testConn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Target string `json:"target"`
	}
	// 同 #10：body 坏了要说"body 坏了"，别让它走到下面报成"未知的测试目标"。
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体格式错误:%w", err))
		return
	}
	g := s.cfg.Get()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	switch in.Target {
	case "llm":
		client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, 60)
		out, err := client.Chat(ctx, []llm.Message{{Role: "user", Content: "回复两个字:连通"}}, false)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "message": "LLM 应答:" + textutil.Truncate(out, 40)})
	case "comfy":
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			strings.TrimRight(g.Comfy.BaseURL, "/")+"/system_stats", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			writeErr(w, 400, fmt.Errorf("ComfyUI 不可达(%s):%v", g.Comfy.BaseURL, err))
			return
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			writeErr(w, 400, fmt.Errorf("ComfyUI HTTP %d", resp.StatusCode))
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "message": "ComfyUI 在线"})
	case "minimax":
		if g.MiniMax.APIKey == "" {
			writeErr(w, 400, fmt.Errorf("MiniMax API Key 未填写"))
			return
		}
		// 探测策略:查询一个不存在的任务 id —— 鉴权失败返回 401(login fail);
		// 通过鉴权则返回业务错误(record not found),即可证明 Key 有效
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			strings.TrimRight(g.MiniMax.BaseURL, "/")+"/v2/query/video_generation/manjux-probe-000", nil)
		req.Header.Set("Authorization", "Bearer "+g.MiniMax.APIKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			writeErr(w, 400, fmt.Errorf("MiniMax API 不可达:%v", err))
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		body := string(raw)
		if resp.StatusCode == http.StatusUnauthorized || strings.Contains(body, "authorized_error") || strings.Contains(body, "login fail") {
			writeErr(w, 400, fmt.Errorf("API Key 无效(401 鉴权失败)"))
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "message": "MiniMax API Key 有效"})
	case "vlm":
		if g.VLM.BaseURL == "" || g.VLM.APIKey == "" || g.VLM.Model == "" {
			writeErr(w, 400, fmt.Errorf("视觉模型未配置"))
			return
		}
		client := llm.New(g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model, 60)
		// 1×1 红色 PNG 的 data URI,验证多模态通路
		red1x1 := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg=="
		out, err := client.ChatVision(ctx, "You reply in Chinese.", []llm.VisionContent{
			{Role: "user", Text: "What color is this image? Just one Chinese word.", Images: []string{red1x1}},
		}, false)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "message": "Vision model response: " + textutil.Truncate(out, 30)})
	default:
		writeErr(w, 400, fmt.Errorf("unknown target: %s", in.Target))
	}
}

// comfyAdmin ComfyUI 管理状态。
func (s *Server) comfyAdmin(w http.ResponseWriter, r *http.Request) {
	eng, err := s.reg.Get("comfy")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		writeErr(w, 500, fmt.Errorf("引擎类型错误"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	st, err := ce.Admin(ctx)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

// comfySageStatus Sage 加速状态(装了没/会不会启用/本机 torch 版本)。
func (s *Server) comfySageStatus(w http.ResponseWriter, r *http.Request) {
	ce, err := s.comfyEngine()
	if err != nil {
		writeErr(w, 503, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	st := ce.SageStatus(ctx)
	writeJSON(w, 200, st)
}

// comfySageInstall 一键安装/更新与本机 torch+CUDA 匹配的 Sage 轮子。
//
// 为什么由应用来做:该轮子与 torch 版本严格绑定,升级 ComfyUI/torch 后旧轮子失效、
// 表现为"突然又变慢了"而界面看不出原因。这里按本机版本自动挑官方推荐发布源里的
// 匹配轮子安装(pip --no-deps,绝不动 torch),装完提示重启引擎生效。
func (s *Server) comfySageInstall(w http.ResponseWriter, r *http.Request) {
	ce, err := s.comfyEngine()
	if err != nil {
		writeErr(w, 503, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	s.opLog("⬇ 开始安装 Sage 加速(按本机 torch/CUDA 自动匹配轮子)…")
	ver, err := ce.InstallSage(ctx, func(msg string) { s.opLog("%s", "   "+msg) })
	if err != nil {
		s.opLog("✕ Sage 安装失败:%v", err)
		writeErr(w, 500, err)
		return
	}
	s.opLog("✔ Sage 加速已就绪:%s(重启引擎后生效)", ver)
	writeJSON(w, 200, map[string]any{"ok": true, "version": ver,
		"note": "已安装,重启 ComfyUI 后生效(管理页「停止引擎」再点「启动」,或等下次渲染自动重启)"})
}

// comfyAction ComfyUI 管理操作:start/stop/queue-clear/interrupt/free。
func (s *Server) comfyAction(w http.ResponseWriter, r *http.Request) {
	eng, _ := s.reg.Get("comfy")
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		writeErr(w, 500, fmt.Errorf("引擎类型错误"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()
	var err error
	switch r.PathValue("action") {
	case "start":
		err = ce.StartAdmin(ctx)
		if err == nil {
			s.markComfyLive(true) // 立即置在线,不等探测缓存
		}
	case "stop":
		err = ce.StopAdmin()
		if err == nil {
			s.markComfyLive(false) // 立即置离线,不等 8s 缓存/失败翻转
		}
	case "queue-clear":
		ce.QueueClear(ctx)
		s.opLog("🧹 已清空 ComfyUI 等待队列(运行中任务不受影响)")
	case "interrupt":
		ce.Interrupt(ctx)
		s.opLog("⏹ 已中断 ComfyUI 当前执行任务(可重试)")
	case "free":
		before, hasB := sysmon.VRAMUsedMB()
		if err = ce.FreeVRAM(ctx); err != nil {
			// 引擎未运行/请求失败必须如实上报,不再假装成功
			s.opLog("⚠ 释放显存未生效:%v(引擎已停止时显存随进程释放,无需操作)", err)
			break // 走底部 writeErr,前端弹真实错误
		}
		time.Sleep(2500 * time.Millisecond) // 给卸载模型+empty_cache 生效时间
		after, hasA := sysmon.VRAMUsedMB()
		if hasB && hasA {
			note := fmt.Sprintf("显存 %.1f GB → %.1f GB", before/1024, after/1024)
			s.opLog("🧽 已卸载模型释放显存:%s(任务执行中卸载会延后到当前任务结束)", note)
			writeJSON(w, 200, map[string]any{"ok": true, "note": note})
			return
		}
		s.opLog("🧽 已请求 ComfyUI 卸载模型释放显存(/free)")
	default:
		writeErr(w, 400, fmt.Errorf("未知操作"))
		return
	}
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// putShot 单镜微调:更新分镜提示词/时长/模式(指纹变化将触发该镜重渲)。

// startupSageCheck 启动时自检 Sage 加速并按需自愈。
//
// 用户把环境升级交给本应用负责(2026-09-12):升级 ComfyUI/torch 后,Sage 轮子会失配,
// 表现只是"渲染突然又慢一倍"而界面上看不出原因。这里在应用启动时(后台、不阻塞启动)
// 比对轮子与本机 torch/CUDA,失配就按本机版本自动装回;过程写进运行日志,失败也不
// 影响渲染(只是没有加速),并在管理页状态里如实说明。
func (s *Server) startupSageCheck() {
	// 只在本机确实装了 ComfyUI 环境时检查(否则不该联网装东西)
	g := s.cfg.Get()
	if g.Comfy.RootDir == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(g.Comfy.RootDir, "ComfyUI", "main.py")); err != nil {
		return
	}
	eng, err := s.reg.Get("comfy")
	if err != nil {
		return
	}
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		return
	}
	want := g.Comfy.SageAttention == nil || *g.Comfy.SageAttention
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	done, msg := ce.SageAutoRepair(ctx, want, func(m string) { s.opLog("%s", "   "+m) })
	if !done {
		s.opLog("⚠ %s", msg)
		return
	}
	if strings.Contains(msg, "重装完成") {
		s.opLog("✔ %s(下次启动引擎即生效)", msg)
	}
}
