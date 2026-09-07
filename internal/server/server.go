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

	"github.com/YiIimini/ManJuX/internal/sysexec"
	"github.com/YiIimini/ManJuX/internal/browser"
	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/llm"
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
	lib   *library.Library
	mux   *http.ServeMux
	quit  func() // 桌面模式注入的优雅退出
}

// SetQuitFunc 注入桌面模式的退出函数(Wails runtime.Quit)。
func (s *Server) SetQuitFunc(f func()) {
	s.quit = f
}

// New 创建服务。
func New(cfg *config.Manager, store *project.Store, reg *engine.Registry,
	pipe *pipeline.Coordinator, f *ff.FF, bus *pipeline.Bus, lib *library.Library) *Server {
	s := &Server{cfg: cfg, store: store, reg: reg, pipe: pipe, ff: f, bus: bus, lib: lib, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Start 启动独立 HTTP 服务(浏览器/App 窗口模式,端口占用自动 +1)。
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

// APIHandler 返回纯 API 路由(Wails 桌面模式的 AssetServer Handler)。
func (s *Server) APIHandler() http.Handler {
	m := http.NewServeMux()
	s.registerAPI(m)
	return m
}

// WebAssets 返回前端静态资源(Wails 桌面模式)。
func WebAssets() fs.FS {
	sub, _ := fs.Sub(webFS, "webdist")
	return sub
}

func (s *Server) routes() {
	s.registerAPI(s.mux)
	// 前端静态(SPA fallback → index.html)
	s.mux.HandleFunc("/", s.serveWeb)
}

func (s *Server) registerAPI(m *http.ServeMux) {
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
	m.HandleFunc("POST /api/projects/{id}/upgrade2k", s.upgrade2K)
	m.HandleFunc("POST /api/projects/{id}/assets", s.uploadAsset)
	m.HandleFunc("DELETE /api/projects/{id}/assets/{assetID}", s.deleteAsset)
	m.HandleFunc("GET /api/projects/{id}/files/{path...}", s.serveProjectFile)
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("POST /api/open", s.openInExplorer)
	m.HandleFunc("POST /api/quit", s.quitApp)
	m.HandleFunc("POST /api/test", s.testConn)
	m.HandleFunc("GET /api/library", s.listLibrary)
	m.HandleFunc("GET /api/library/{kind}/{name}/image", s.libraryImage)
	m.HandleFunc("DELETE /api/library/{kind}/{name}", s.deleteLibrary)
	m.HandleFunc("POST /api/projects/{id}/export-pack", s.exportClipPack)
	m.HandleFunc("POST /api/projects/{id}/assets/regen", s.regenAsset)
	m.HandleFunc("GET /api/comfy/admin", s.comfyAdmin)
	m.HandleFunc("POST /api/comfy/{action}", s.comfyAction)
	m.HandleFunc("GET /api/outputs", s.outputsList)
	m.HandleFunc("DELETE /api/projects/{id}/output", s.projectOutputDelete)
	m.HandleFunc("GET /api/sysmon", s.handleSysmon)
	m.HandleFunc("POST /api/library/gacha", s.handleLibraryGacha)
	m.HandleFunc("POST /api/library/characters", s.handleLibraryCreateCharacter)
	m.HandleFunc("GET /api/comfy/versions", s.comfyVersions)
	m.HandleFunc("PUT /api/projects/{id}/shot", s.putShot)
	m.HandleFunc("POST /api/comfy/open-web", s.comfyOpenWeb)
	m.HandleFunc("POST /api/projects/{id}/import-plan", s.importPlan)
	m.HandleFunc("POST /api/window/{action}", s.handleWindowAction)
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
	// 模型文件名类字段传空 = 未改动,保留现值(防部分提交把模型名清空导致 400)
	keepStr(&in.Comfy.UNetRef2VA, cur.Comfy.UNetRef2VA)
	keepStr(&in.Comfy.UNetFL2VA, cur.Comfy.UNetFL2VA)
	keepStr(&in.Comfy.TurboLoRARef2VA, cur.Comfy.TurboLoRARef2VA)
	keepStr(&in.Comfy.TurboLoRAFL2VA, cur.Comfy.TurboLoRAFL2VA)
	keepStr(&in.Comfy.VideoVAE, cur.Comfy.VideoVAE)
	keepStr(&in.Comfy.AudioVAE, cur.Comfy.AudioVAE)
	keepStr(&in.Comfy.CLIP, cur.Comfy.CLIP)
	keepStr(&in.Comfy.ImageUNet, cur.Comfy.ImageUNet)
	keepStr(&in.Comfy.ImageCLIP, cur.Comfy.ImageCLIP)
	keepStr(&in.Comfy.ImageVAE, cur.Comfy.ImageVAE)
	keepStr(&in.Comfy.BaseURL, cur.Comfy.BaseURL)
	keepStr(&in.MiniMax.Model, cur.MiniMax.Model)
	keepStr(&in.LLM.Model, cur.LLM.Model)
	keepStr(&in.LLM.BaseURL, cur.LLM.BaseURL)
	if err := s.cfg.Update(in); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// keepStr 空-string 字段回填现值。
func keepStr(dst *string, cur string) {
	if strings.TrimSpace(*dst) == "" {
		*dst = cur
	}
}

// ---------- 项目 ----------

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	type item struct {
		*project.Project
		HasPlan bool   `json:"has_plan"`
		Done    int    `json:"shots_done"`
		Total   int    `json:"shots_total"`
		Cover   string `json:"cover"` // 项目封面(角色定妆照/场景图)
	}
	out := []item{}
	for _, p := range s.store.List() {
		it := item{Project: p, Cover: s.projectCover(p.ID)}
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
	// 全局设置的默认引擎优先(auto=本地优先自动降级;api=纯云端;comfy=纯本地)
	if e := s.cfg.Get().Render.DefaultEngine; e == "auto" || e == "comfy" || e == "api" {
		cfg.Params.Render.Engine = e
	}
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
	// 成片状态(含 2K 定稿)
	outReady := map[string]any{"ready": false, "final": "output/final.mp4"}
	if st, err := os.Stat(filepath.Join(s.store.Dir(id), "output", "final.mp4")); err == nil && st.Size() > 0 {
		outReady["ready"] = true
		outReady["size"] = st.Size()
	}
	outReady["final_2k"] = "output/final_2k.mp4"
	if st, err := os.Stat(filepath.Join(s.store.Dir(id), "output", "final_2k.mp4")); err == nil && st.Size() > 0 {
		outReady["ready_2k"] = true
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
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.ContainsRune(in.Text, '\uFFFD') {
		writeErr(w, 400, fmt.Errorf("文本含乱码字符(U+FFFD):请通过工作台界面提交,确保 UTF-8 编码"))
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
	// 拒绝含 U+FFFD 的输入(GBK 直传等编码事故),防脏数据入库
	if strings.ContainsRune(fmt.Sprint(in), '\uFFFD') {
		writeErr(w, 400, fmt.Errorf("参数含乱码字符(U+FFFD):请通过工作台界面提交,确保 UTF-8 编码"))
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
		Phase  string `json:"phase"`
		Only   []int  `json:"only"`
		Engine string `json:"engine"` // 单次引擎覆盖(auto/comfy/api,空=项目配置)
		Seed   int64  `json:"seed"`   // 单镜微调种子(>0 生效:seed+N*101)
	}
	_ = readBody(r, &in)
	if in.Phase == "" {
		in.Phase = pipeline.PhaseAll
	}
	if err := s.pipe.Start(id, in.Phase, in.Only, in.Engine, in.Seed); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "phase": in.Phase})
}

// upgrade2K 云端 2K 定稿:body {shot: N}(0=成片)。
func (s *Server) upgrade2K(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	var in struct {
		Shot int `json:"shot"`
	}
	_ = readBody(r, &in)
	if err := s.pipe.StartUpgrade2K(id, in.Shot); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
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
	// 上传即入库:后续项目同名角色/场景自动复用
	if s.lib != nil {
		desc := r.FormValue("description")
		prompt := r.FormValue("image_prompt")
		plan, _ := s.store.LoadPlan(id)
		if plan != nil {
			if kind == "characters" {
				for _, c := range plan.Characters {
					if "char:"+c.ID == assetID {
						desc, prompt = c.Description, c.ImagePrompt
					}
				}
			} else if kind == "scenes" {
				for _, sc := range plan.Scenes {
					if "scene:"+sc.ID == assetID {
						desc, prompt = sc.Description, sc.ImagePrompt
					}
				}
			}
		}
		if _, err := s.lib.Put(kind, name, desc, prompt, dest, id, "upload"); err == nil {
			// 入库成功仅记录日志,不阻塞响应
		}
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
	cmd := sysexec.Hide(exec.Command("explorer.exe", "/select,", target))
	_ = cmd.Start()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// testConn 连通性测试:body {target: "llm"|"comfy"|"minimax"}。
func (s *Server) testConn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Target string `json:"target"`
	}
	_ = readBody(r, &in)
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
		writeJSON(w, 200, map[string]any{"ok": true, "message": "LLM 应答:" + truncateStr(out, 40)})
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
		writeJSON(w, 200, map[string]any{"ok": true, "message": "Vision model response: " + truncateStr(out, 30)})
	default:
		writeErr(w, 400, fmt.Errorf("unknown target: %s", in.Target))
	}
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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
	case "stop":
		err = ce.StopAdmin()
	case "queue-clear":
		ce.QueueClear(ctx)
	case "interrupt":
		ce.Interrupt(ctx)
	case "free":
		ce.FreeVRAM(ctx)
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
func (s *Server) putShot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	var in struct {
		ShotID   int    `json:"shot_id"`
		H3Prompt string `json:"h3_prompt"`
		Duration int    `json:"duration"`
		Mode     string `json:"mode"`
	}
	if err := readBody(r, &in); err != nil || in.ShotID <= 0 {
		writeErr(w, 400, fmt.Errorf("参数错误(shot_id 必填)"))
		return
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚未生成分镜方案"))
		return
	}
	for i := range plan.Shots {
		if plan.Shots[i].ID != in.ShotID {
			continue
		}
		if strings.TrimSpace(in.H3Prompt) != "" {
			plan.Shots[i].H3Prompt = in.H3Prompt
		}
		if in.Duration >= 4 && in.Duration <= 10 {
			plan.Shots[i].Duration = in.Duration
		}
		if in.Mode == "ref2va" || in.Mode == "t2va" {
			plan.Shots[i].Mode = in.Mode
		}
		if err := s.store.SavePlan(id, plan); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	writeErr(w, 404, fmt.Errorf("镜头不存在: %d", in.ShotID))
}

// importPlan 导入既有分镜方案(JSON,新建项目时上传)。
func (s *Server) importPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中"))
		return
	}
	var in struct {
		Plan *project.Plan `json:"plan"`
	}
	if err := readBody(r, &in); err != nil || in.Plan == nil || len(in.Plan.Shots) == 0 {
		writeErr(w, 400, fmt.Errorf("无效的分镜方案(需含 shots 数组)"))
		return
	}
	if err := s.store.SavePlan(id, in.Plan); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "shots": len(in.Plan.Shots)})
}

// comfyVersions ComfyUI 版本管理器报告(online=1 联网对比,较慢)。
func (s *Server) comfyVersions(w http.ResponseWriter, r *http.Request) {
	eng, _ := s.reg.Get("comfy")
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		writeErr(w, 500, fmt.Errorf("引擎类型错误"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	rep, err := ce.Versions(ctx, r.URL.Query().Get("online") == "1")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, rep)
}

// comfyOpenWeb 用系统默认浏览器打开 ComfyUI WEB 界面。
func (s *Server) comfyOpenWeb(w http.ResponseWriter, r *http.Request) {
	eng, _ := s.reg.Get("comfy")
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		writeErr(w, 500, fmt.Errorf("引擎类型错误"))
		return
	}
	url := ce.Cfg().BaseURL + "/"
	if err := browser.OpenDefault(url); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "url": url})
}

// regenAsset 单资产重生成(Krea-2 换 seed)。
func (s *Server) regenAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	var in struct {
		AssetID string `json:"asset_id"`
	}
	if err := readBody(r, &in); err != nil || in.AssetID == "" {
		writeErr(w, 400, fmt.Errorf("asset_id 不能为空"))
		return
	}
	if err := s.pipe.RegenAsset(id, in.AssetID); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 资产库 ----------

func (s *Server) listLibrary(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	writeJSON(w, 200, s.lib.List())
}

func (s *Server) libraryImage(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		http.Error(w, "library disabled", 500)
		return
	}
	img, err := s.lib.ImagePath(r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, img)
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	if err := s.lib.Delete(r.PathValue("kind"), r.PathValue("name")); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// quitApp 界面确认后的强制退出(停管线 → 优雅关窗)。
func (s *Server) quitApp(w http.ResponseWriter, r *http.Request) {
	for _, p := range s.store.List() {
		s.pipe.Stop(p.ID)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	if s.quit != nil {
		go func() {
			<-r.Context().Done() // 等响应送达
			s.quit()
		}()
		return
	}
	go func() {
		<-r.Context().Done()
		os.Exit(0)
	}()
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
