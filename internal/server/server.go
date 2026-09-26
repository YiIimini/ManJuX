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
	"net/url"
	"path/filepath"
	"strings"

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
	mux   http.Handler // 已套上来源守卫(见 guardAPI)
	quit  func()       // 桌面模式注入的优雅退出

	// startProdFn 是"拉起批量产线"的执行体,生产装配下为 nil(走真实 startBatch)。
	//
	// 为什么留这个接缝:导入路径(项目包导入 / 脚本创作)现在会**自动开跑**,而单元测试
	// 一旦真的拉起产线,就会去读项目、跑 ComfyUI、最后走到 quitApp(os.Exit)——那是进程级
	// 副作用,会把整个测试进程掀掉(2026-09-26 实测:TestReapStaleBatchFreesSingleton 因为
	// 被别的用例遗留的产线 goroutine 抢了全局单例而变红)。
	//
	// 测试里换成记录器,断言的是**"这条入口有没有请求开跑、开的是哪本书"**——恰恰就是
	// 当初漏掉的那件事(用户点「项目包导入」导了 40 集,一行 ▶ 都没有)。真实产线的启动
	// 由真机端到端跑覆盖,不靠单元测试假装。
	startProdFn func(prefix string, eps []string) bool
}

// SetQuitFunc 注入桌面模式的退出函数(Wails runtime.Quit)。
func (s *Server) SetQuitFunc(f func()) {
	s.quit = f
}

// New 创建服务。
func New(cfg *config.Manager, store *project.Store, reg *engine.Registry,
	pipe *pipeline.Coordinator, f *ff.FF, bus *pipeline.Bus, lib *library.Library) *Server {
	s := &Server{cfg: cfg, store: store, reg: reg, pipe: pipe, ff: f, bus: bus, lib: lib}
	// routes() 里装配 mux 并套上来源守卫(见 guardAPI)
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
	return guardAPI(m)
}

// WebAssets 返回前端静态资源(Wails 桌面模式)。
func WebAssets() fs.FS {
	sub, _ := fs.Sub(webFS, "webdist")
	return sub
}

func (s *Server) routes() {
	m := http.NewServeMux()
	s.registerAPI(m)
	// 前端静态(SPA fallback → index.html)
	m.HandleFunc("/", s.serveWeb)
	// 守卫包住**整个** mux:两条装配路径(独立 HTTP 服务 routes()、Wails 桌面
	// AssetServer APIHandler())用的是同一道门 —— 分别加中间件正是"改一处漏一处"。
	s.mux = guardAPI(m)
}

// ---------- 本地服务的来源守卫(审计 P0-10) ----------
//
// 背景:这个 HTTP 服务监听本机端口,而 92 条路由**没有任何来源校验**。于是任何网页
// 只要诱导浏览器发一个请求,就能打到副作用端点:
//
//	POST /api/quit        退出应用
//	POST /api/comfy/stop  停掉渲染引擎
//	POST /api/cleanup     删中间产物与条件缓存
//	POST /api/open-url    用系统浏览器打开任意 URL
//
// 关键点在于"text/plain + JSON body"这种请求**不触发 CORS 预检**(简单请求),
// 所以它一定送得到服务端 —— 不能指望浏览器替我们拦。反过来说,真正能拦住的判据是:
//
//	① Host 必须是回环地址(挡 DNS rebinding:攻击者把自己的域名解析到 127.0.0.1);
//	② Origin/Referer 若存在,必须是回环来源(挡普通跨站请求);
//	③ 写操作必须声明 Content-Type: application/json —— 简单请求只能带
//	   text/plain / form-urlencoded / multipart 三种,于是"跨站写"在第一步就被挡下,
//	   而应用自己的 fetch 一定带 JSON 头。
//
// 例外:multipart 上传(浏览器模式的封面/资产上传)是合法写操作,单独放行 ——
// 它同样受 ①② 约束。
func guardAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(hostOnly(r.Host)) {
			writeErr(w, 403, fmt.Errorf("拒绝非本机来源的请求(Host=%s)", r.Host))
			return
		}
		if o := strings.TrimSpace(r.Header.Get("Origin")); o != "" && !originIsLoopback(o) {
			writeErr(w, 403, fmt.Errorf("拒绝跨站来源的请求(Origin=%s)", o))
			return
		} else if o == "" {
			if ref := strings.TrimSpace(r.Header.Get("Referer")); ref != "" && !originIsLoopback(ref) {
				writeErr(w, 403, fmt.Errorf("拒绝跨站来源的请求(Referer=%s)", ref))
				return
			}
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !writableContentType(r.Header.Get("Content-Type")) {
				writeErr(w, 415, fmt.Errorf("写操作必须使用 application/json(或 multipart/form-data):"+
					"当前 Content-Type=%q。这条校验用来挡住不触发预检的跨站简单请求", r.Header.Get("Content-Type")))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// writableContentType 允许的写请求 Content-Type。
func writableContentType(ct string) bool {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch base {
	case "application/json", "multipart/form-data", "":
		// 空 Content-Type 放行:部分原生/桌面调用不带这个头,而它们不是浏览器发起的
		// 跨站请求(跨站请求一定由浏览器补上头)。真正拦跨站的判据是 ①②。
		return true
	}
	return false
}

// hostOnly 去掉端口(IPv6 亦适用)。
func hostOnly(h string) string {
	h = strings.TrimSpace(h)
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i > 0 {
			return h[1:i]
		}
	}
	if i := strings.LastIndex(h, ":"); i > 0 {
		return h[:i]
	}
	return h
}

// isLoopbackHost 回环主机判据。
//
// 除了 127.0.0.1/localhost/::1,还要接受 `*.localhost`:Wails 桌面模式的页面来源是
// http://wails.localhost/**,而它由 Wails 自己的 scheme 处理器映射到本机 —— 把应用
// 自己挡在门外比不设防还糟(所有请求 403)。判据因此是"回环 + 保留域"而不是"字面等于 127.0.0.1"。
func isLoopbackHost(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" {
		return false
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// originIsLoopback Origin/Referer 是否是本机来源(它们带 scheme 与端口)。
func originIsLoopback(o string) bool {
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return isLoopbackHost(u.Hostname())
}

func (s *Server) registerAPI(m *http.ServeMux) {
	m.HandleFunc("GET /api/health", s.health)
	m.HandleFunc("GET /api/status", s.status)
	m.HandleFunc("GET /api/settings", s.getSettings)
	m.HandleFunc("PUT /api/settings", s.putSettings)
	m.HandleFunc("GET /api/projects", s.listProjects)
	m.HandleFunc("POST /api/projects", s.createProject)
	m.HandleFunc("POST /api/projects/rescan", s.rescanProjects)
	m.HandleFunc("POST /api/projects/import-package", s.importPackages)
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
	m.HandleFunc("PUT /api/projects/{id}/character", s.putCharacter)
	m.HandleFunc("DELETE /api/projects/{id}/shot/{shotID}", s.deleteShot)
	m.HandleFunc("POST /api/projects/{id}/cover", s.uploadCover)
	m.HandleFunc("POST /api/projects/{id}/compose-story", s.handleComposeStory)
	m.HandleFunc("POST /api/novel-split", s.handleNovelSplit)
	m.HandleFunc("GET /api/novel-batch", s.handleNovelBatch)
	m.HandleFunc("GET /api/novels", s.handleNovels)
	m.HandleFunc("DELETE /api/novels", s.handleNovelDelete)
	m.HandleFunc("POST /api/novels/hidden", s.handleNovelHidden)
	m.HandleFunc("POST /api/novels/produce-script", s.handleNovelProduceScript)
	m.HandleFunc("GET /api/novel-jobs", s.handleNovelJobs)
	m.HandleFunc("DELETE /api/novel-jobs", s.handleNovelJobsDelete)
	m.HandleFunc("GET /api/novels/cover", s.handleNovelCover)
	m.HandleFunc("POST /api/novels/cover-redo", s.handleNovelCoverRedo)
	m.HandleFunc("GET /api/novels/detail", s.handleNovelDetail)
	m.HandleFunc("GET /api/novels/script", s.handleNovelScript)
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
