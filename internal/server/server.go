// Package server 提供 HTTP API(REST + SSE)与前端静态资源。
// 仅绑定 127.0.0.1,文件服务限制在数据目录内(路径穿越防护)。
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/version"
)

//go:embed all:webdist
var webFS embed.FS

// Server HTTP 服务。
type Server struct {
	cfg   *config.Manager
	store *project.Store
	reg   *engine.Registry
	pipe  *pipeline.Coordinator
	ff    *ff.FF
	bus   *pipeline.Bus
	mux   *http.ServeMux
}

// New 创建服务。
func New(cfg *config.Manager, store *project.Store, reg *engine.Registry,
	pipe *pipeline.Coordinator, f *ff.FF, bus *pipeline.Bus) *Server {
	s := &Server{cfg: cfg, store: store, reg: reg, pipe: pipe, ff: f, bus: bus, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Start 启动(端口占用自动 +1 重试)。
func (s *Server) Start(basePort int) (int, error) {
	port := basePort
	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			go http.Serve(ln, s.mux)
			return port, nil
		}
		port++
	}
	return 0, fmt.Errorf("端口 %d~%d 均被占用", basePort, port-1)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/health", s.health)
	m.HandleFunc("GET /api/status", s.status)
	m.HandleFunc("GET /api/settings", s.getSettings)
	m.HandleFunc("PUT /api/settings", s.putSettings)
	m.HandleFunc("GET /api/projects", s.listProjects)
	m.HandleFunc("POST /api/projects", s.createProject)
	m.HandleFunc("GET /api/projects/{id}", s.getProject)
	m.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)
	m.HandleFunc("GET /api/projects/{id}/source", s.getSource)
	m.HandleFunc("PUT /api/projects/{id}/source", s.putSource)
	m.HandleFunc("GET /api/projects/{id}/plan", s.getPlan)
	m.HandleFunc("PUT /api/projects/{id}/params", s.putParams)
	m.HandleFunc("POST /api/projects/{id}/run", s.runProject)
	m.HandleFunc("POST /api/projects/{id}/stop", s.stopProject)
	m.HandleFunc("POST /api/projects/{id}/assets", s.uploadAsset)
	m.HandleFunc("DELETE /api/projects/{id}/assets/{assetID}", s.deleteAsset)
	m.HandleFunc("GET /api/projects/{id}/files/{path...}", s.serveProjectFile)
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("POST /api/open", s.openInExplorer)
	// 前端静态(SPA fallback → index.html)
	m.HandleFunc("/", s.serveWeb)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

func readBody[T any](r *http.Request, v *T) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// ---------- 基础 ----------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "app": version.AppName, "version": version.Version})
}

type statusOut struct {
	Version   string `json:"version"`
	BuildInfo string `json:"build_info"`
	Port      int    `json:"port"`
	Env       envOut `json:"env"`
	Running   map[string]pipeline.Run `json:"running"`
}

type envOut struct {
	FFmpeg    string `json:"ffmpeg"`
	LLMReady  bool   `json:"llm_ready"`
	ComfyURL  string `json:"comfy_url"`
	ComfyLive bool   `json:"comfy_live"`
	APIReady  bool   `json:"minimax_ready"`
	Engines   []string `json:"engines"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	g := s.cfg.Get()
	env := envOut{
		FFmpeg:   s.ff.Ffmpeg,
		LLMReady: g.LLM.BaseURL != "" && g.LLM.APIKey != "" && g.LLM.Model != "",
		ComfyURL: g.Comfy.BaseURL,
		APIReady: g.MiniMax.APIKey != "",
		Engines:  s.reg.Names(),
	}
	// ComfyUI 存活探测(只 ping,不触发代管启动)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(g.Comfy.BaseURL, "/")+"/system_stats", nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		env.ComfyLive = resp.StatusCode == http.StatusOK
	}
	running := map[string]pipeline.Run{}
	for _, p := range s.store.List() {
		if run := s.pipe.Running(p.ID); run != nil {
			running[p.ID] = *run
		}
	}
	writeJSON(w, 200, statusOut{
		Version: version.Version, BuildInfo: version.BuildInfo(),
		Port: g.ServerPort, Env: env, Running: running,
	})
}

// ---------- 设置 ----------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	g := s.cfg.Get()
	// key 脱敏回显(只给尾 4 位)
	if len(g.LLM.APIKey) > 8 {
		g.LLM.APIKey = "****" + g.LLM.APIKey[len(g.LLM.APIKey)-4:]
	}
	if len(g.MiniMax.APIKey) > 8 {
		g.MiniMax.APIKey = "****" + g.MiniMax.APIKey[len(g.MiniMax.APIKey)-4:]
	}
	writeJSON(w, 200, g)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in config.Settings
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	cur := s.cfg.Get()
	// 脱敏占位不覆盖真实 key
	if strings.HasPrefix(in.LLM.APIKey, "****") {
		in.LLM.APIKey = cur.LLM.APIKey
	}
	if strings.HasPrefix(in.MiniMax.APIKey, "****") {
		in.MiniMax.APIKey = cur.MiniMax.APIKey
	}
	if err := s.cfg.Update(in); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 项目 ----------

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	type item struct {
		*project.Project
		HasPlan bool `json:"has_plan"`
		Done    int  `json:"shots_done"`
		Total   int  `json:"shots_total"`
	}
	out := []item{}
	for _, p := range s.store.List() {
		it := item{Project: p}
		if plan, err := s.store.LoadPlan(p.ID); err == nil {
			it.HasPlan = true
			it.Total = len(plan.Shots)
			if m, err := s.store.LoadManifest(p.ID); err == nil {
				for _, c := range m {
					if c.Status == project.ShotDone {
						it.Done++
					}
				}
			}
		}
		if run := s.pipe.Running(p.ID); run != nil {
			it.Running = true
			it.Phase = run.Phase
		}
		out = append(out, it)
	}
	writeJSON(w, 200, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	cfg := project.ProjectConfig{Params: project.DefaultStageParams()}
	p, err := s.store.Create(in.Name, cfg)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if run := s.pipe.Running(id); run != nil {
		p.Running = true
		p.Phase = run.Phase
	}
	out := map[string]any{"project": p}
	if plan, err := s.store.LoadPlan(id); err == nil {
		out["plan"] = plan
	}
	if assets, err := s.store.LoadAssets(id); err == nil {
		out["assets"] = assets
	}
	if m, err := s.store.LoadManifest(id); err == nil {
		out["manifest"] = m
	}
	if source, err := s.store.LoadSource(id); err == nil {
		out["source"] = source
	}
	// qc 报告
	qcRaw, err := os.ReadFile(filepath.Join(s.store.Dir(id), "output", "qc_report.json"))
	if err == nil {
		var qcAny any
		if json.Unmarshal(qcRaw, &qcAny) == nil {
			out["qc"] = qcAny
		}
	}
	// 成片状态
	outReady := map[string]any{"ready": false, "final": "output/final.mp4"}
	if st, err := os.Stat(filepath.Join(s.store.Dir(id), "output", "final.mp4")); err == nil && st.Size() > 0 {
		outReady["ready"] = true
		outReady["size"] = st.Size()
	}
	out["output"] = outReady
	writeJSON(w, 200, out)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	if err := s.store.Delete(id); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	text, err := s.store.LoadSource(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]string{"text": text})
}

func (s *Server) putSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Text string `json:"text"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.store.SaveSource(id, in.Text); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚未生成分镜方案"))
		return
	}
	writeJSON(w, 200, plan)
}

// putParams 阶段参数调优(整体替换,服务端 Sanitize 钳制)。
func (s *Server) putParams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,不能调参"))
		return
	}
	p, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in project.StageParams
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	in.Sanitize()
	p.Config.Params = in
	if err := s.store.SaveMeta(p); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, p.Config.Params)
}

func (s *Server) runProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Phase string `json:"phase"`
		Only  []int  `json:"only"`
	}
	_ = readBody(r, &in)
	if in.Phase == "" {
		in.Phase = pipeline.PhaseAll
	}
	if err := s.pipe.Start(id, in.Phase, in.Only); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "phase": in.Phase})
}

func (s *Server) stopProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.pipe.Stop(id) {
		writeErr(w, 404, fmt.Errorf("项目未在运行"))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 资产上传 ----------

func (s *Server) uploadAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, 400, err)
		return
	}
	kind := r.FormValue("kind")     // characters / scenes / props
	assetID := r.FormValue("asset_id") // 形如 char:xxx / scene:xxx
	name := r.FormValue("name")
	switch kind {
	case "characters", "scenes", "props":
	default:
		writeErr(w, 400, fmt.Errorf("kind 必须是 characters/scenes/props"))
		return
	}
	if assetID == "" {
		writeErr(w, 400, fmt.Errorf("asset_id 不能为空"))
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(hdr.Filename))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		writeErr(w, 400, fmt.Errorf("仅支持 png/jpg/webp"))
		return
	}
	// 文件名安全化
	safeName := sanitizeFilename(name + "_" + assetID)
	if safeName == "" {
		safeName = "asset"
	}
	destDir := filepath.Join(s.store.Dir(id), "assets", kind)
	_ = os.MkdirAll(destDir, 0o755)
	dest := filepath.Join(destDir, safeName+ext)
	out, err := os.Create(dest)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		writeErr(w, 500, err)
		return
	}
	out.Close()

	assets, _ := s.store.LoadAssets(id)
	rel, _ := filepath.Rel(s.store.Dir(id), dest)
	assets[assetID] = project.Asset{
		ID: assetID, Kind: kind, Name: name,
		File: filepath.ToSlash(rel), Source: "upload",
		Fingerprint: project.FingerprintFile(dest),
	}
	if err := s.store.SaveAssets(id, assets); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, assets[assetID])
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	assetID := r.PathValue("assetID")
	assets, err := s.store.LoadAssets(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	a, ok := assets[assetID]
	if !ok {
		writeErr(w, 404, fmt.Errorf("资产不存在"))
		return
	}
	if a.File != "" {
		_ = os.Remove(filepath.Join(s.store.Dir(id), a.File))
	}
	delete(assets, assetID)
	_ = s.store.SaveAssets(id, assets)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 项目文件服务(播放/预览,防路径穿越)----------

func (s *Server) serveProjectFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := path.Clean(r.PathValue("path"))
	if strings.Contains(rel, "..") || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if _, ok := s.store.Get(id); !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	full := filepath.Join(s.store.Dir(id), filepath.FromSlash(rel))
	if _, err := os.Stat(full); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// 视频加 Range 支持(拖动播放)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, full)
}

// ---------- SSE ----------

var sseMu sync.Mutex

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
		allow = s.cfg.Get().DataDir
	}
	if allow == "" || !strings.HasPrefix(target, filepath.Clean(allow)) {
		writeErr(w, 403, fmt.Errorf("路径超出允许范围"))
		return
	}
	if _, err := os.Stat(target); err != nil {
		writeErr(w, 404, err)
		return
	}
	cmd := exec.Command("explorer.exe", "/select,", target)
	_ = cmd.Start()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 静态前端 ----------

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	sub, _ := fs.Sub(webFS, "webdist")
	upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if upath == "" {
		upath = "index.html"
	}
	f, err := sub.Open(upath)
	if err != nil {
		// SPA fallback
		upath = "index.html"
		f, err = sub.Open(upath)
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
	}
	defer f.Close()
	if strings.HasSuffix(upath, ".html") {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	http.ServeFileFS(w, r, sub, upath)
}

func sanitizeFilename(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r >= 0x2e80: // CJK 及全角
			sb.WriteRune(r)
		case r == '_', r == '-', r == '.':
			sb.WriteRune(r)
		}
	}
	return strings.Trim(sb.String(), ".")
}
