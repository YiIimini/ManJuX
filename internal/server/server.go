// Package server 提供 HTTP API(REST + SSE)与前端静态资源。
// 仅绑定 127.0.0.1,文件服务限制在数据目录内(路径穿越防护)。
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
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
	// 拆集任务状态落盘:此前纯内存,重启即丢 —— 首页占位卡会卡在「AI 拆集中」
	// 直到 20 分钟孤儿清理兜底。落盘后重启可看到上次拆到哪、并明确标注"被中断"。
	SetJobsPath(filepath.Join(cfg.Get().DataDir, "novel_jobs.json"))
	s.routes()
	return s
}

// Start 启动独立 HTTP 服务(浏览器/App 窗口模式,端口占用自动 +1)。
// 同时起一个后台 Sage 自检:升级 ComfyUI/torch 后轮子失配要能自己装回来
// (用户 2026-09-12 把环境升级交给本应用负责:每次处理到位,而不是靠人记得)。
func (s *Server) Start(basePort int) (int, error) {
	go s.startupSageCheck()
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
	m.HandleFunc("POST /api/projects/{id}/assets/{assetID}/frame-preview", s.handleAssetFramePreview)
	m.HandleFunc("POST /api/projects/{id}/assets/{assetID}/frame-apply", s.handleAssetFrameApply)
	m.HandleFunc("DELETE /api/projects/{id}/assets/{assetID}", s.deleteAsset)
	m.HandleFunc("GET /api/projects/{id}/files/{path...}", s.serveProjectFile)
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("POST /api/open", s.openInExplorer)
	m.HandleFunc("POST /api/cleanup", s.handleCleanup)
	m.HandleFunc("GET /api/browse", s.handleBrowse)
	m.HandleFunc("POST /api/open-url", s.openExternalURL)
	m.HandleFunc("POST /api/quit", s.quitApp)
	m.HandleFunc("POST /api/test", s.testConn)
	m.HandleFunc("GET /api/library", s.listLibrary)
	m.HandleFunc("GET /api/thumb", s.serveThumb)
	m.HandleFunc("GET /api/library/{kind}/{name}", s.libraryDetail)
	m.HandleFunc("GET /api/library/{kind}/{name}/image", s.libraryImage)
	m.HandleFunc("DELETE /api/library/{kind}/{name}", s.deleteLibrary)
	m.HandleFunc("POST /api/projects/{id}/export-pack", s.exportClipPack)
	// 镜头增强:局部重绘(只改目标区域)与音频驱动(口型跟随配音)
	m.HandleFunc("POST /api/projects/{id}/shot/{shotID}/mask-fix", s.handleShotMaskFix)
	m.HandleFunc("POST /api/projects/{id}/shot/{shotID}/audio-drive", s.handleShotAudioDrive)
	m.HandleFunc("POST /api/projects/{id}/shot/{shotID}/upscale", s.handleShotUpscale)
	m.HandleFunc("POST /api/projects/{id}/assets/regen", s.regenAsset)
	m.HandleFunc("GET /api/comfy/admin", s.comfyAdmin)
	m.HandleFunc("POST /api/comfy/{action}", s.comfyAction)
	m.HandleFunc("GET /api/comfy/sage", s.comfySageStatus)
	m.HandleFunc("POST /api/comfy/sage-install", s.comfySageInstall)
	m.HandleFunc("GET /api/outputs", s.outputsList)
	m.HandleFunc("DELETE /api/projects/{id}/output", s.projectOutputDelete)
	m.HandleFunc("GET /api/meta", s.meta)
	m.HandleFunc("GET /api/sysmon", s.handleSysmon)
	m.HandleFunc("GET /api/logs", s.handleLogs)
	m.HandleFunc("POST /api/library/{kind}/{name}/regen", s.libraryRegen)
	// 库资产运营:补全多视角转台图 / AI 生成描述与提示词 / 手动编辑描述与提示词
	m.HandleFunc("POST /api/library/{kind}/{name}/views", s.libraryViews)
	m.HandleFunc("POST /api/library/{kind}/{name}/describe", s.libraryDescribe)
	m.HandleFunc("PUT /api/library/{kind}/{name}", s.librarySetMeta)
	m.HandleFunc("POST /api/library/gacha", s.handleLibraryGacha)
	m.HandleFunc("POST /api/library/characters", s.handleLibraryCreateCharacter)
	m.HandleFunc("GET /api/comfy/versions", s.comfyVersions)
	m.HandleFunc("PUT /api/projects/{id}/shot", s.putShot)
	m.HandleFunc("DELETE /api/projects/{id}/shot/{shotID}", s.deleteShot)
	m.HandleFunc("POST /api/projects/{id}/cover", s.uploadCover)
	m.HandleFunc("POST /api/projects/{id}/compose-story", s.handleComposeStory)
	m.HandleFunc("POST /api/novel-split", s.handleNovelSplit)
	m.HandleFunc("GET /api/novel-batch", s.handleNovelBatch)
	m.HandleFunc("GET /api/novels", s.handleNovels)
	m.HandleFunc("DELETE /api/novels", s.handleNovelDelete)
	m.HandleFunc("GET /api/novel-jobs", s.handleNovelJobs)
	m.HandleFunc("DELETE /api/novel-jobs", s.handleNovelJobsDelete)
	m.HandleFunc("GET /api/novels/cover", s.handleNovelCover)
	m.HandleFunc("GET /api/novels/detail", s.handleNovelDetail)
	m.HandleFunc("GET /api/novels/read", s.handleNovelRead)
	m.HandleFunc("POST /api/novels/produce", s.handleNovelProduce)
	m.HandleFunc("POST /api/novel-batch", s.handleNovelBatch)
	m.HandleFunc("DELETE /api/novel-batch", s.handleNovelBatch)
	m.HandleFunc("POST /api/comfy/open-web", s.comfyOpenWeb)
	m.HandleFunc("POST /api/comfy/open-appwin", s.comfyOpenAppWin)
	m.HandleFunc("POST /api/projects/{id}/import-plan", s.importPlan)
	m.HandleFunc("POST /api/window/{action}", s.handleWindowAction)
}

// opLog 用户操作审计日志:删除/创建/保存/导出等关键操作全部进运行日志面板
// (phase=system,前端 phaseLabel 显示「系统」)。
func (s *Server) opLog(format string, args ...any) {
	s.bus.Publish(pipeline.Event{Phase: "system", Level: "info",
		Message: fmt.Sprintf(format, args...)})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	// 序列化失败绝不静默空 body(曾致 /api/status 空响应,前端状态灯全灭)
	if err := enc.Encode(v); err != nil {
		log.Printf("[server] ⚠ JSON 序列化失败: %v", err)
		_, _ = w.Write([]byte(`{"error":"` + err.Error() + `"}`))
	}
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
	Version   string                     `json:"version"`
	BuildInfo string                     `json:"build_info"`
	Port      int                        `json:"port"`
	Env       envOut                     `json:"env"`
	Running   map[string]pipeline.RunOut `json:"running"`
}

type envOut struct {
	FFmpeg    string   `json:"ffmpeg"`
	LLMReady  bool     `json:"llm_ready"`
	ComfyURL  string   `json:"comfy_url"`
	ComfyLive bool     `json:"comfy_live"`
	APIReady  bool     `json:"minimax_ready"`
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
	env.ComfyLive = s.probeComfy(g.Comfy.BaseURL)
	running := map[string]pipeline.RunOut{}
	for _, p := range s.store.List() {
		if run := s.pipe.Running(p.ID); run != nil {
			running[p.ID] = pipeline.RunOut{
				Phase: run.Phase, StartedAt: run.StartedAt,
				Engine: run.EngineOverride, Seed: run.SeedOverride,
			}
		}
	}
	writeJSON(w, 200, statusOut{
		Version: version.Version, BuildInfo: version.BuildInfo(),
		Port: g.ServerPort, Env: env, Running: running,
	})
}

// ---------- 元信息 ----------

// meta 阶段与风格清单的单一真源。
//
// 此前前端 store.js 的 PHASES 与 ParamsDrawer.vue 的 STYLE_PRESETS 各持一份
// 手工副本:后端新增一个风格,前端不会出现(用户选不到);前端自行添加一个,
// 后端 composeStyleHint 会静默忽略(用户选了但不生效)。现在由后端统一提供
// **key 与显示名**;颜色、描述这类纯展示信息仍留在前端 —— 后端不该决定配色。
