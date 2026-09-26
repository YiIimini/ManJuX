package comfyengine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/sysexec"
	"golang.org/x/sys/windows"
)

// Engine 本地 ComfyUI 渲染引擎(H3-Base 768p,音视频一体直出)。
type Engine struct {
	cfg func() config.ComfySettings // 设置快照 provider(热更新)

	// cliRef 当前 ComfyUI 客户端。地址变更时整体替换。读多写少(渲染路径每次
	// 调用都要取),用原子指针避免给它加锁形成热点。
	cliRef atomic.Pointer[Client]

	// procMu 保护 procRef。它是三方并发触碰的共享状态:常驻的空闲巡检
	// goroutine(60s 一轮,可能停进程)、管理页操作、渲染路径的探测 ——
	// 此前无任何同步,构成真实数据竞争(go test -race 可复现)。
	procMu  sync.Mutex
	procRef *os.Process // 代管启动的 ComfyUI 进程

	// torch 版本探测缓存(sage 状态展示用):管理页每 8 秒轮询,不能每次都起 python。
	torchMu      sync.Mutex
	torchVer     string
	torchCudaVer string
	torchAt      time.Time
}

// client 取当前客户端快照。
func (e *Engine) client() *Client { return e.cliRef.Load() }

// setClient 替换客户端(设置中的地址变化时重建)。
func (e *Engine) setClient(c *Client) { e.cliRef.Store(c) }

// ownedProc 取本应用代管的 ComfyUI 进程(外部启动的实例返回 nil)。
func (e *Engine) ownedProc() *os.Process {
	e.procMu.Lock()
	defer e.procMu.Unlock()
	return e.procRef
}

// setOwnedProc 记录或清除代管进程。
func (e *Engine) setOwnedProc(p *os.Process) {
	e.procMu.Lock()
	e.procRef = p
	e.procMu.Unlock()
}

// lifecycleNotice 引擎生命周期通知:启动成功 / 停止完成即回调
// (装配期接事件总线 → 前端底栏灯/首页/管理页即时同步,不依赖轮询窗口)。
type lifecycleNotice struct {
	sync.Mutex
	cb func(alive bool, why string)
}

var lifecycle = &lifecycleNotice{}

// SetLifecycleNotice 注册生命周期回调(单引擎,装配期调用一次)。
func SetLifecycleNotice(cb func(alive bool, why string)) {
	lifecycle.Lock()
	lifecycle.cb = cb
	lifecycle.Unlock()
}

func notifyLifecycle(alive bool, why string) {
	lifecycle.Lock()
	cb := lifecycle.cb
	lifecycle.Unlock()
	if cb != nil {
		cb(alive, why)
	}
}

// New 创建引擎;cfg 返回最新设置快照。
func New(cfg func() config.ComfySettings) *Engine {
	e := &Engine{cfg: cfg}
	e.setClient(NewClient(cfg().BaseURL))
	return e
}

// Name 引擎名。
func (e *Engine) Name() string { return "comfy" }

// Cfg 返回 ComfyUI 设置快照(WEB 界面地址等对外暴露用)。
func (e *Engine) Cfg() config.ComfySettings { return e.cfg() }

// StartOnBoot 应用启动时**预热** ComfyUI(按设置里的 AutoStart 决定要不要代管启动)。
//
// 为什么不能让 Available 兼职:Available 是"用之前确保可用"(带请求 ctx、会阻塞调用方),
// 而这里是**开机预热** —— 失败只记日志,不阻塞窗口,也不该被某次请求的超时牵连。
//
// 为什么需要它(2026-09-26 用户要求「APP启动后 ComfyUI 自动启动」):
// 原先只有懒启动 —— 第一次渲染才去拉 ComfyUI,用户要白等 1-2 分钟加载模型;
// 更糟的是预检(preflight)按设计**不触发启动**(避免误启动),于是"没启动"会被读成
// "不可用",整条管线在渲染阶段直接失败(实测:批量产线第 1 集就断在「无可用引擎」)。
func (e *Engine) StartOnBoot(ctx context.Context) error {
	c := e.cfg()
	if !c.AutoStart {
		return nil
	}
	e.setClient(NewClient(c.BaseURL))
	if e.client().Alive(ctx) {
		return nil // 已经在跑(用户自己开的):不重复拉起,也不接管
	}
	if err := e.launch(ctx, c); err != nil {
		return err
	}
	return nil
}

func (e *Engine) Available(ctx context.Context) error {
	e.setClient(NewClient(e.cfg().BaseURL))
	if e.client().Alive(ctx) {
		return nil
	}
	c := e.cfg()
	if !c.AutoStart {
		return fmt.Errorf("ComfyUI 未运行(%s):请在设置中开启自动启动,或手动启动 ComfyUI", c.BaseURL)
	}
	return e.launch(ctx, c)
}

// launch 代管启动 ComfyUI 进程并等待就绪(日志落 data/logs/comfyui.log)。
func (e *Engine) launch(ctx context.Context, c config.ComfySettings) error {
	py := filepath.Join(c.RootDir, "standalone-env", "python.exe")
	main := filepath.Join(c.RootDir, "ComfyUI", "main.py")
	if _, err := os.Stat(py); err != nil {
		return fmt.Errorf("未找到 %s:请在设置中确认 ComfyUI 根目录", py)
	}
	if _, err := os.Stat(main); err != nil {
		return fmt.Errorf("未找到 %s:请在设置中确认 ComfyUI 根目录", main)
	}
	// 端口:优先从设置解析;设置本身异常时回退到配置层的默认地址 ——
	// 不再各写一份 "8190" 字面量,避免改端口时漏改此处导致启动的引擎与
	// 客户端指向不同端口。
	port, perr := parsePort(c.BaseURL)
	if perr != nil {
		port, _ = parsePort(config.DefaultComfyBaseURL)
	}
	logPath := filepath.Join(sidecarRoot(c), "logs", "comfyui.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logF = nil
	}
	// 画质回退(2026-09-10 用户实证「同内容昨天好看今晚变差」):--fast
	// fp16_accumulation(fp16 累加,官方明示可能画质劣化)与 --use-ck-attention
	// (注意力后端数值特性)均已撤下——速度收益主体是 torch cu130 的 kitchen
	// CUDA 内核(保留)+渲染前释放显存,画质优先于极限速度。
	//
	// 内存/显存治理(2026-09-12 用户报「渲染时 GPU 和内存都快爆了」,实测数值:
	// 显存 23529/24463 MiB = 96%、ComfyUI 的 python 进程占 40.3GB、系统内存仅剩
	// 8GB)。根因是**同时常驻的东西太多**:H3 主模型 20GB + 视频 VAE 5GB 超出一张
	// 24GB 卡的容量,ComfyUI 的 smart memory 又把 TE(15GB)、VAE、主模型尽量留在
	// 显存/内存里,于是每步都在换页(代码里记过的"150 秒/步"就是这种挤压)。
	// 这里三个参数都是**纯内存策略、不动数值与画质**:
	//   --disable-smart-memory:用不到就卸载(TE 用完卸、采样时只留主模型、解码前
	//     再卸主模型),把峰值从"全都挤在显存里"改成"按阶段一样一样来";
	//   --cache-none:不缓存节点产物(190 帧解码后的图像张量一次就 1-2GB),
	//     代价是同图内节点按需重算,而我们的图每镜只跑一次,基本无感;
	//   --reserve-vram:给系统/浏览器留出显存(实测渲染前别的进程已占 1-2GB),
	//     没有预留就等于和桌面抢最后几 GB —— 数值可在设置页调,0=交由 ComfyUI 自定。
	cmd := sysexec.Hide(exec.Command(py, comfyLaunchArgs(main, port, c.ReserveVRAMGB, e.sageEnabled())...))
	cmd.Dir = filepath.Dir(main)
	// 无控制台(CREATE_NO_WINDOW)下 Python 管道 IO 默认按系统 locale(GBK)编码,
	// ComfyUI 启动横幅的 emoji(🎉)会触发 UnicodeEncodeError 直接崩溃 —— 强制 UTF-8。
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8", "PYTHONLEGACYWINDOWSSTDIO=0")
	if logF != nil {
		cmd.Stdout = logF
		cmd.Stderr = logF
	}
	if err := cmd.Start(); err != nil {
		if logF != nil {
			logF.Close()
		}
		return fmt.Errorf("启动 ComfyUI 失败: %w", err)
	}
	e.setOwnedProc(cmd.Process)
	writePidFile(c, cmd.Process.Pid) // 代管状态持久化:重启后可接管
	// 渲染引擎提为高优先级:分镜渲染是最重的生产任务,不应被桌面/其它进程
	// 抢 CPU(GPU 等待时自然让出,不会饿死系统;2026-09-10 用户定调)。
	if h, herr := windows.OpenProcess(windows.PROCESS_SET_INFORMATION, false, uint32(cmd.Process.Pid)); herr == nil {
		_ = windows.SetPriorityClass(h, windows.HIGH_PRIORITY_CLASS)
		_ = windows.CloseHandle(h)
	}
	touchBusy()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(4 * time.Second):
		}
		if e.client().Alive(ctx) {
			notifyLifecycle(true, "启动") // 就绪即通知:各状态点即时点亮
			return nil
		}
	}
	return fmt.Errorf("ComfyUI 启动超时(5 分钟),详见 %s", logPath)
}

func parsePort(baseURL string) (string, error) {
	s := baseURL
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			p := s[i+1:]
			if _, err := strconv.Atoi(p); err == nil {
				return p, nil
			}
			break
		}
		if s[i] == '/' {
			break
		}
	}
	return "", fmt.Errorf("no port")
}

// Render 执行单镜渲染:encode(条件缓存,可选)→ 采样 → 下载产物。
// ClearQueue 清空待执行队列(不影响运行中任务)。渲染阶段开始时调用一次,
// 清掉上次异常退出遗留的任务;不放在每镜 Render 里 —— fail-continue 链会误清
// 前镜仍在排队的任务(2026-09-08 事故根因之一)。
func (e *Engine) ClearQueue(ctx context.Context) {
	e.client().ClearPending(ctx)
}

// seamRenderLength 本次提交给引擎的帧数。
//
// 接缝模式会把上一镜的尾部 22 帧钉在本镜头部,再由 MiniMaxH3MotionContextTrim
// 裁掉 —— 所以渲染长度要**加回**这 22 帧,否则成片比计划短。
//
// 关键在"只在接缝真正生效时加"(审计 P2):旧实现在探测到"上一镜没有接缝存档"
// 而降级普通渲染时,只清了 PrevShot、**没有回滚 +22** —— 于是那一镜既不裁头
// (降级后图里没有 Trim 节点),又多渲 22 帧(约 0.9 秒),逐镜累积到成片。
// 现在降级与长度补偿是同一处决定,想漏也漏不掉。
func seamRenderLength(jobLength int, seamActive bool) int {
	if seamActive {
		return engine.AlignLength(jobLength + seamHeadFrames)
	}
	return engine.AlignLength(jobLength)
}

// seamHeadFrames 接缝钉入的帧数(与 buildRenderGraph 里 MotionContext 的
// context_length 一致;改一处必须改另一处,由测试钉住)。
const seamHeadFrames = 22

func (e *Engine) Render(ctx context.Context, job engine.Job) (*engine.Result, error) {
	touchBusy()
	c := e.cfg()
	if err := e.Available(ctx); err != nil {
		return nil, err
	}
	// 接缝前置探测必须**在条件缓存之前**(审计 P2):降级会改变渲染帧数,
	// 而缓存键含 length —— 先算缓存再降级,就会拿"接缝长度"的条件去跑"普通长度"的
	// 图(缓存与图不一致),而且要等渲完才发现时长多了 22 帧。
	// 上一镜 AV latent 存档不存在(云端产物/旧渲染)时降级普通渲染。
	if job.Seam && job.PrevShot > 0 {
		if !e.seamPrevExists(ctx, job.ProjectID, job.PrevShot) {
			if job.OnProgress != nil {
				job.OnProgress(15, "上一镜无接缝存档(云端产物或旧渲染),本镜普通渲染")
			}
			job.PrevShot = 0
		}
	}
	// 帧数(含接缝补偿):pinned 头(22 帧)会被 Trim 裁掉,接缝生效时须加回,
	// 保证成片时长不缩水;**降级时必须不加**(见 seamRenderLength 的说明)。
	seamActive := job.Seam && job.PrevShot > 0
	length := seamRenderLength(job.Length, seamActive)
	w, h := engine.SnapCanvas(job.Width, job.Height)

	// 参考图上传(ComfyUI input 目录)
	uploaded := make([]string, 0, len(job.RefImages))
	for _, ref := range job.RefImages {
		name, err := e.client().UploadImage(ctx, ref.Path)
		if err != nil {
			return nil, fmt.Errorf("参考图上传失败(%s): %w", filepath.Base(ref.Path), err)
		}
		uploaded = append(uploaded, name)
	}

	condRef := "" // ""=单图直跑内联编码;"cache:<name>"=两阶段加载缓存

	// 音频参考上传(音频驱动):H3 的 ref_audios 收服务器端文件名,先传再拼图。
	var uploadedAudios []string
	for _, a := range job.Audios {
		an, aerr := e.client().UploadImage(ctx, a)
		if aerr != nil {
			return nil, fmt.Errorf("上传音频参考失败: %w", aerr)
		}
		uploadedAudios = append(uploadedAudios, an)
	}
	if c.CondCache && e.client().HasNode(ctx, classCondSave) {
		// 缓存键含参考图内容指纹:换定妆照(同名覆盖)后自动失效,防旧条件隐性复用
		var refFPs []string
		for _, ref := range job.RefImages {
			refFPs = append(refFPs, ref.Path+":"+fileFingerprint(ref.Path))
		}
		cacheName := condCacheName(job.ProjectID, job.ShotID, job.Prompt, refFPs, w, h, length)
		if e.condExists(c, job.ProjectID, cacheName) {
			condRef = "cache:" + cacheName
		} else {
			if job.OnProgress != nil {
				job.OnProgress(2, "预编码(Qwen3-VL 条件)")
			}
			encGraph := buildEncodeGraph(job, c, uploaded, uploadedAudios, w, h, length, cacheName)
			if _, err := e.submitAndWait(ctx, encGraph, job.Timeout, nil); err != nil {
				return nil, fmt.Errorf("预编码失败: %w", err)
			}
			markCondReady(c, job.ProjectID, cacheName)
			condRef = "cache:" + cacheName
		}
	}

	if job.OnProgress != nil {
		job.OnProgress(16, "采样渲染")
	}
	graph := buildRenderGraph(job, c, condRef, uploaded, uploadedAudios, w, h, length)
	hist, err := e.submitAndWait(ctx, graph, job.Timeout, func(p int, note string) {
		if job.OnProgress != nil {
			job.OnProgress(16+p*84/100, note)
		}
	})
	if err != nil {
		return nil, err
	}

	// 产物回收(SaveVideo → animated 文件)
	files := hist.ExtractFiles()
	var video *OutputFile
	for i := range files {
		if files[i].Animated {
			video = &files[i]
			break
		}
	}
	if video == nil {
		return nil, fmt.Errorf("渲染完成但未找到视频产物(outputs=%d 项)", len(files))
	}
	ext := filepath.Ext(video.Filename)
	if ext == "" {
		ext = ".mp4"
	}
	dest := filepath.Join(job.OutputDir, job.OutputName+ext)
	dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := e.client().Download(dlCtx, *video, dest); err != nil {
		return nil, fmt.Errorf("产物下载失败: %w", err)
	}
	e.client().Free(context.Background())
	return &engine.Result{VideoPath: dest, Engine: e.Name()}, nil
}

func (e *Engine) submitAndWait(ctx context.Context, graph map[string]any, timeout time.Duration, onProgress func(int, string)) (*HistoryEntry, error) {
	pid, err := e.client().Submit(ctx, graph)
	if err != nil {
		return nil, err
	}
	return e.client().WaitForDone(ctx, pid, timeout, onProgress)
}

// ---------- 条件缓存记账 ----------

func sidecarRoot(c config.ComfySettings) string {
	return filepath.Join(filepath.Dir(c.RootDir), "data")
}

func condSidecarDir(c config.ComfySettings) string {
	return filepath.Join(sidecarRoot(c), "cond_cache")
}

// condProjectDir 某项目的条件缓存子目录(cond_cache/{projectID}/)。
// 按项目分目录存放,删项目时整目录移除即可(2026-09-11 用户定调)。
func condProjectDir(c config.ComfySettings, projectID string) string {
	return filepath.Join(condSidecarDir(c), "manjux_"+projectID)
}

// condPtDir H3 条件缓存的数据目录:插件把 CONDITIONING 序列化为 .pt 存于此。
// 与 sidecar 记账目录是**两个不同的位置** —— 数据在 ComfyUI 侧,记账在本应用侧。
func condPtDir(c config.ComfySettings) string {
	if strings.TrimSpace(c.RootDir) == "" {
		return ""
	}
	return filepath.Join(c.RootDir, "ComfyUI", "models", "conditioning")
}

// CleanupProjectCache 删除某项目的全部条件缓存(数据 + 记账)。
//
// 两个目录必须一起清:
//   - 数据:models/conditioning/*.pt —— 文件名只有 s{shot}_{hash}(projectID 只参与
//     hash 计算,不体现在文件名里),因此**必须靠 sidecar 的 .ok 文件名反查**,
//     无法按项目名匹配;
//   - 记账:data/cond_cache/manjux_<projectID>/。
//
// 旧实现只清后者,导致 .pt 永久滞留 —— 实测该目录已堆积 4.9GB 且只增不减。
func (e *Engine) CleanupProjectCache(projectID string) {
	c := e.cfg()
	sidecar := condProjectDir(c, projectID)

	removed := 0
	if ptDir := condPtDir(c); ptDir != "" {
		if entries, err := os.ReadDir(sidecar); err == nil {
			for _, ent := range entries {
				name := ent.Name()
				if ent.IsDir() || !strings.HasSuffix(name, ".ok") {
					continue
				}
				// .ok 的文件名就是 cacheName(不含扩展名),据此定位同名 .pt
				if os.Remove(filepath.Join(ptDir, strings.TrimSuffix(name, ".ok")+".pt")) == nil {
					removed++
				}
			}
		}
	}
	if err := os.RemoveAll(sidecar); err == nil {
		log.Printf("[comfy] 已清理项目条件缓存:%s(连带 %d 个 .pt 数据文件)", sidecar, removed)
	}
}

func (e *Engine) condExists(c config.ComfySettings, projectID, cacheName string) bool {
	_, err := os.Stat(filepath.Join(condProjectDir(c, projectID), cacheName+".ok"))
	return err == nil
}

func markCondReady(c config.ComfySettings, projectID, cacheName string) {
	dir := condProjectDir(c, projectID)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, cacheName+".ok"),
		[]byte(time.Now().Format(time.RFC3339)), 0o644)
}

// condCacheName 条件缓存键名(含项目子目录前缀)。
// 文件落盘路径:cond_cache/{projectID}/s{shotID}_{hash}.ok
func condCacheName(projectID string, shotID int, prompt string, refFPs []string, w, h, length int) string {
	sum := md5.Sum([]byte(fmt.Sprint(projectID, shotID, prompt, refFPs, w, h, length)))
	return "s" + strconv.Itoa(shotID) + "_" + hex.EncodeToString(sum[:])[:8]
}

// fileFingerprint 文件内容指纹(md5 前 16 位);读失败退化为 mtime,保证键变化。
func fileFingerprint(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		if st, serr := os.Stat(path); serr == nil {
			return "t" + st.ModTime().Format("150405.000000000")
		}
		return "missing"
	}
	sum := md5.Sum(raw)
	return hex.EncodeToString(sum[:])[:16]
}

// ---------- 工作流组装(API prompt 格式) ----------

func nd(classType string, inputs map[string]any) map[string]any {
	return map[string]any{"class_type": classType, "inputs": inputs}
}

func link(node string, slot int) []any { return []any{node, slot} }

// addConditionNode 向 g 加入条件节点(R2V 或 I2V/T2VA)。
// 返回 (条件节点id, latent节点id)。
// loadAudioNode 加入音频加载节点(ref 音频),返回节点 id。
// id 从 95x 段取并在冲突时自动退避,避免与模型链/采样链的固定 id 撞车。
func loadAudioNode(g map[string]any, i int, name string) string {
	id := fmt.Sprintf("95%d", i)
	for {
		if _, exists := g[id]; !exists {
			break
		}
		id += "x"
	}
	g[id] = nd("LoadAudio", map[string]any{"audio": name})
	return id
}

func addConditionNode(g map[string]any, job engine.Job, uploaded []string, audios []string, w, h, length int) (string, string) {
	base := map[string]any{
		"clip":   link("1", 0),
		"vae":    link("2", 0),
		"prompt": job.Prompt,
		"width":  w,
		"height": h,
		"length": length,
	}
	// 有图或**有音频**即走 Ref2V:纯音频驱动也是合法用法(音频驱动口型/节奏)
	if job.Mode == "ref2va" && (len(uploaded) > 0 || len(audios) > 0) {
		inputs := copyMap(base)
		inputs["audio_vae"] = link("3", 0)
		// 官方指南:max 保留最高 2048 短边编码,identity fidelity 更强
		// (match 会把定妆照缩到生成分辨率再编码,人脸细节折损;2026-09-08 返正)
		inputs["ref_image_size"] = "max"
		// autogrow 输入的 API 提交格式:聚合键 ref_images = [节点引用…](不是 ref_image_N)
		refs := make([]any, 0, len(uploaded))
		for i, name := range uploaded {
			refs = append(refs, link(loadImageNode(g, i, name), 0))
		}
		inputs["ref_images"] = refs
		// 音频参考(音频驱动 Audio Drive):H3 Ref2V 原生支持音频条件 —— 喂入定稿
		// 配音后,人物的口型与动作节奏会跟随音频。这是"精确台词"路线的入口:
		// 先把配音做成确定资产,再用它驱动画面,而不是让模型自由发挥台词。
		if len(audios) > 0 {
			auds := make([]any, 0, len(audios))
			for i, name := range audios {
				auds = append(auds, link(loadAudioNode(g, i, name), 0))
			}
			inputs["ref_audios"] = auds
		}
		g["20"] = nd(classRef2V, inputs)
		return "20", "20"
	}
	// t2va / fl2va:MiniMaxH3ImageToVideo(不带 first/last frame 即纯文生)
	g["20"] = nd(classI2V, base)
	return "20", "20"
}

func loadImageNode(g map[string]any, idx int, serverName string) string {
	id := "10" + strconv.Itoa(idx)
	g[id] = nd("LoadImage", map[string]any{"image": serverName})
	return id
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// buildEncodeGraph encode 子图:条件节点 → CondSave(输出节点)。
func buildEncodeGraph(job engine.Job, c config.ComfySettings, uploaded, audios []string, w, h, length int, cacheName string) map[string]any {
	g := loaders(c, job.Accel)
	cond, _ := addConditionNode(g, job, uploaded, audios, w, h, length)
	g["60"] = nd(classCondSave, map[string]any{
		"conditioning": link(cond, 0),
		"cache_name":   cacheName,
	})
	return g
}

func loaders(c config.ComfySettings, accel string) map[string]any {
	videoVAE := c.VideoVAE
	if accel == "express" {
		// 极速档:int8 量化 VAE 解码提速 30-50%(有损,预览/抽卡用;文件缺失回退 fp16)
		const int8VAE = "minimax_h3_video_vae_int8_convrot.safetensors"
		if modelFileExists(c, int8VAE, []string{"vae"}) {
			videoVAE = int8VAE
		}
	}
	return map[string]any{
		"1": nd("CLIPLoader", map[string]any{"clip_name": c.CLIP, "type": "minimax"}),
		"2": nd("VAELoader", map[string]any{"vae_name": videoVAE}),
		"3": nd("VAELoader", map[string]any{"vae_name": c.AudioVAE}),
	}
}

// accelLoraFor 本次作业该挂的**加速 LoRA 文件名**(空 = 不挂)。唯一实现点。
//
// 为什么必须收口(审计 P2):整镜渲染有"quality 档换 Acc-8Step、量化底模不兼容就
// 回退 Turbo"这套逻辑,而局部重绘/音频驱动的图**复制了模型链却漏了这段** ——
// 同一个项目里,主渲染用 Acc-8Step、局部重绘用 Turbo,修补过的那一镜画质基线就与
// 周围镜头不同(人物偏糊),而两边的代码看起来都对。
//
// 档位语义(知识库 2026-09 结论):
//
//	quality = Kijai Acc-8Step(并行解码蒸馏,人物质感/清晰度显著优于 Turbo,慢约 20%)
//	turbo   = 官方 Turbo 4step(最快,人物偏糊)
//	express = 官方 Turbo + int8 VAE(极速抽卡预览)
//	refine  = **不挂任何蒸馏 LoRA**,按完整步数采样(用户 2026-09-26「现在渲染的真的垃圾」)。
//
// refine 这一档为什么必须单独存在:Turbo/Acc 那类 LoRA 是**蒸馏**权重,只配 4-8 步用;
// 而项目参数里那个 Turbo 开关此前只放开了步数钳制(见 stages_render 的 `if rp.Turbo`),
// LoRA 照样挂 —— 于是"关掉 Turbo、跑 20 步"实际是"挂着 4 步蒸馏 LoRA 跑 20 步",
// 画质比 8 步还差,而从界面与日志上都看不出原因。
//
// Acc-8Step 对 int8 量化底模**不兼容**(tensor 32 vs 1024,官方修复未合并主线):
// 量化底模自动回退 Turbo,配合 fp16 VAE + ref_image_size=max。
func accelLoraFor(job engine.Job, c config.ComfySettings) string {
	if job.Accel == "refine" {
		return "" // 精修档:什么加速都不挂,让底模按自己的步数好好出图
	}
	turbo := c.TurboLoRARef2VA
	if job.Mode != "ref2va" {
		turbo = c.TurboLoRAFL2VA
	}
	if job.Accel != "quality" {
		return turbo
	}
	acc := "MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy.safetensors"
	if job.Mode != "ref2va" {
		acc = "MiniMax-H3-FL2VA-Acc-8Step_pruned_comfy.safetensors"
	}
	if !isQuantizedBase(unetFor(job, c)) && modelFileExists(c, acc, []string{"loras", "diffusion_models"}) {
		return acc
	}
	return turbo
}

// isQuantizedBase 判断底模文件名是否属于低位量化权重 —— Acc-8Step LoRA 挂不上这类底模
// (int8 实测 tensor 32 vs 1024,官方修复未合并主线)。w4a8/nvfp4/mxfp8 与 int8 同属
// 低位量化,一并排除;`fp8_scaled` 是官方原厂发布的底模格式,保持原有行为不动。
//
// 大小写不敏感:模型文件名在社区发行里有 `INT8` / `Int8` 等写法,判据不该因大小写漏判
// (漏判的后果是给量化底模挂上蒸馏 LoRA,出图直接损坏,而不是报错)。
func isQuantizedBase(name string) bool {
	low := strings.ToLower(name)
	for _, m := range []string{"int8", "w4a8", "nvfp4", "mxfp8"} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// modelFileExists 在模型子目录探测文件存在(质量档/极速档的文件守卫)。
// 同时覆盖 ComfyUI/models 与 shared/models(extra_model_paths 映射的两个根)。
// modelFileExists 权重文件是否存在(仅作**提交前**的尽力判断,权威答案在
// ComfyUI 自己的 combo 列表里 —— 见 UpscaleAssets/MaskAssets/EditAssets)。
//
// 2026-09-24 修(审计 P1-2 权重判据):这里原先只猜 `<root>/ComfyUI/models` 与
// `<root>/shared/models`,漏了本机实际在用的 `<root>/ComfyUI/shared/models`
// (extra_model_paths.yaml 的 base_path)—— 于是 express 档的 int8 VAE 与 quality 档的
// Acc-8Step 在这台机器上被判"不存在",**静默降级**:用户以为开了极速/高画质,
// 实际跑的是默认档,而且没有任何提示。现在目录解析与清理/管理页共用同一份实现。
func modelFileExists(c config.ComfySettings, name string, subs []string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, sub := range subs {
		for _, dir := range ModelDirs(c.RootDir, sub) {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
	}
	return false
}

// buildRenderGraph 采样图:条件(内联或缓存)+ latent → 采样链 → 解码 → SaveVideo。
func buildRenderGraph(job engine.Job, c config.ComfySettings, condRef string, uploaded, audios []string, w, h, length int) map[string]any {
	g := loaders(c, job.Accel)

	var condNode, latentNode string
	latentSlot := 1 // R2V/I2V 双输出:cond=0 / latent=1
	if condRef != "" {
		g["24"] = nd(classCondLoad, map[string]any{
			"cache_name": condRef[len("cache:"):],
		})
		g["25"] = nd(classEmptyLatAV, map[string]any{"width": w, "height": h, "length": length})
		condNode, latentNode = "24", "25"
		latentSlot = 0 // EmptyLatentAV 单输出
	} else {
		condNode, latentNode = addConditionNode(g, job, uploaded, audios, w, h, length)
	}

	// 模型链:UNET → (加速 LoRA) → SigmaShift(官方默认 12/3)
	// 加速档(知识库 2026-09 结论):
	//   quality = Kijai Acc-8Step(并行解码蒸馏,人物质感/清晰度/表情显著优于 Turbo,慢约 20%)
	//   turbo   = 官方 Turbo 4step(最快,人物偏糊)
	//   express = 官方 Turbo + int8 VAE(极速抽卡预览)
	// 选档收口在 accelLoraFor —— 局部重绘/音频驱动那条链也必须用同一个(LED 见该函数)
	turbo := accelLoraFor(job, c)
	unet := unetFor(job, c)
	modelLink := link("30", 0)
	g["30"] = nd("UNETLoader", map[string]any{
		"unet_name":    unet,
		"weight_dtype": "default",
	})
	if turbo != "" {
		g["31"] = nd("LoraLoaderModelOnly", map[string]any{
			"lora_name":      turbo,
			"strength_model": 1.0,
			"model":          modelLink,
		})
		modelLink = link("31", 0)
	}
	g["32"] = nd(classSigmaShift, map[string]any{
		"model":       modelLink,
		"shift_video": 12.0,
		"shift_audio": 3.0,
	})
	model := link("32", 0)

	steps := job.Steps
	switch job.Accel {
	case "refine":
		// 精修档:不挂蒸馏 LoRA,步数按"完整采样"给足 —— 4 步蒸馏的锚定值在这里无意义。
		// 20 是保底(低于它等于白关蒸馏),用户可以把项目参数 steps 调到更高。
		if steps < 20 {
			steps = 20
		}
	case "quality":
		if steps < 8 {
			steps = 8 // Acc-8Step 蒸馏锚定 8 步
		}
	case "express":
		if steps > 4 {
			steps = 4 // 极速预览
		}
	}
	if steps <= 0 {
		steps = 8
	}

	// 镜间连续(MotionContext 接缝):加载上一镜 AV latent 钉入条件;
	// 产物由 Trim 裁掉 pinned 头(音画同裁,match_tail 对齐时长)。
	useSeam := job.Seam && job.PrevShot > 0
	guiderCond := link(condNode, 0)
	if useSeam {
		seamDir := "manjux/" + sanitizePrefix(job.ProjectID) + "_seam"
		g["70"] = nd(classMotionLoad, map[string]any{
			"latent_path": seamDir, // 目录 + clip_index 取固定槽,项目间隔离
			"clip_index":  job.PrevShot,
		})
		g["71"] = nd(classMotionCtx, map[string]any{
			"conditioning":         guiderCond,
			"vae":                  link("2", 0),
			"latent":               link(latentNode, latentSlot),
			"context_length":       strconv.Itoa(seamHeadFrames),
			"audio_context_length": 24,
			"context_latent":       link("70", 0),
		})
		guiderCond = link("71", 0)
	}
	// 本镜采样输出落盘为下一镜的接缝上下文(固定槽,重渲自动覆盖)
	if job.Seam {
		g["72"] = nd(classMotionSave, map[string]any{
			"latent":          link("44", 0),
			"filename_prefix": "manjux/" + sanitizePrefix(job.ProjectID) + "_seam/clip",
			"clip_index":      job.ShotID,
		})
	}

	g["40"] = nd("RandomNoise", map[string]any{"noise_seed": job.Seed})
	g["41"] = nd("KSamplerSelect", map[string]any{"sampler_name": "res_multistep"})
	g["42"] = nd("BasicScheduler", map[string]any{
		"model":     model,
		"scheduler": "simple",
		"steps":     steps,
		"denoise":   1.0,
	})
	g["43"] = nd("BasicGuider", map[string]any{
		"model":        model,
		"conditioning": guiderCond,
	})
	g["44"] = nd("SamplerCustomAdvanced", map[string]any{
		"noise":        link("40", 0),
		"guider":       link("43", 0),
		"sampler":      link("41", 0),
		"sigmas":       link("42", 0),
		"latent_image": link(latentNode, latentSlot),
	})
	g["50"] = nd("VAEDecode", map[string]any{"samples": link("44", 0), "vae": link("2", 0)})
	g["51"] = nd("VAEDecodeAudio", map[string]any{"samples": link("44", 0), "vae": link("3", 0)})
	if useSeam {
		// 裁掉 pinned 头:images+audio 同裁,match_tail 保证音画等长
		g["73"] = nd(classMotionTrim, map[string]any{
			"images":      link("50", 0),
			"trim_frames": link("71", 1),
			"audio":       link("51", 0),
			"match_tail":  true,
		})
		g["52"] = nd("CreateVideo", map[string]any{
			"images": link("73", 0),
			"fps":    24.0,
			"audio":  link("73", 1),
		})
	} else {
		g["52"] = nd("CreateVideo", map[string]any{
			"images": link("50", 0),
			"fps":    24.0,
			"audio":  link("51", 0),
		})
	}
	g["53"] = nd("SaveVideo", map[string]any{
		"video":           link("52", 0),
		"filename_prefix": "manjux/shot_" + strconv.Itoa(job.ShotID),
		"format":          "mp4",
	})
	return g
}

func unetFor(job engine.Job, c config.ComfySettings) string {
	if job.Mode == "ref2va" && c.UNetRef2VA != "" {
		return c.UNetRef2VA
	}
	return c.UNetFL2VA
}

// sanitizePrefix 清洗接缝 latent 前缀(项目名可能含中文,保安全字符)。
func sanitizePrefix(s string) string {
	var b []rune
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r >= 0x4e00 && r <= 0x9fff, r == '_', r == '-':
			b = append(b, r)
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}

// seamPrevExists 探测上一镜接缝 latent 是否已存盘(SaveLatent 固定槽)。
func (e *Engine) seamPrevExists(ctx context.Context, projectID string, prevShot int) bool {
	name := fmt.Sprintf("clip_%05d.safetensors", prevShot)
	sub := fmt.Sprintf("manjux/%s_seam", sanitizePrefix(projectID))
	q := fmt.Sprintf("/view?filename=%s&subfolder=%s&type=output", url.QueryEscape(name), url.QueryEscape(sub))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.client().BaseURL+q, nil)
	if err != nil {
		return false
	}
	resp, err := e.client().HTTP().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// comfyLaunchArgs 组装代管启动 ComfyUI 的参数。
//
// 抽成纯函数是为了能单测:这几个开关直接决定渲染时的显存/内存峰值(实测旧参数下
// 显存 96%、ComfyUI 进程 40GB),不能再以字面量散落在启动代码里;同时它们是
// **纯内存策略**,不触碰数值精度与画质(2026-09-10 用户明确要求画质优先于极限速度,
// 当时因此撤下了 --fast 与 --use-ck-attention)。
// sageEnabled 是否启用 Sage Attention(官方推荐:H3 生成速度约提升一倍,画质损失很小)。
//
// 三个条件:①standalone-env 里装了 sageattention;②设置没明确关掉(nil=未表态→开);
// ③**与本机 torch/CUDA 匹配** —— 升级 torch 后旧轮子会失配,加了参数轻则 import
// 失败、重则内核与显存布局不匹配导致出图异常,宁可不加速(读不到 torch 版本时才
// 交给 ComfyUI 自身的回退兜底)。
func (e *Engine) sageEnabled() bool {
	c := e.cfg()
	if !SageInstalled(c) {
		return false
	}
	if c.SageAttention != nil && !*c.SageAttention {
		return false
	}
	tv, cv := e.torchInfo()
	ver := readSageVersion(filepath.Join(c.RootDir, "standalone-env", "Lib", "site-packages"), tv, cv)
	if tv == "" {
		return true // 读不到 torch 版本时按"装了就用",由 ComfyUI 自身回退兜底
	}
	ok, _ := SageCompat(ver, tv, cv)
	if !ok {
		log.Printf("[comfy] Sage 轮子与本机 torch 不匹配,本次不加 --use-sage-attention(管理页可一键重装)")
	}
	return ok
}

func comfyLaunchArgs(main, port string, reserveVRAMGB int, sage bool) []string {
	args := []string{
		main,
		"--port", port,
		"--listen", "127.0.0.1",
		// 用不到就卸载:TE(15GB)编完即卸、采样期只留主模型、解码前卸主模型换 VAE,
		// 避免 H3(20GB)+视频VAE(5GB) 同时压在 24GB 卡上反复换页。
		"--disable-smart-memory",
		// 不缓存节点产物:视频解码后的图像张量一次就 1-2GB,缓存只对同图内重复
		// 执行有意义,而本应用每镜都是新图。
		"--cache-none",
	}
	if reserveVRAMGB > 0 {
		// 官方口径:该值让 DynamicVRAM 把这些显存**完全留空**(连别的程序占的也算),
		// 避免 H3 吃满 24GB 之后与桌面互相挤压。
		args = append(args, "--vram-headroom", strconv.Itoa(reserveVRAMGB))
	}
	if sage {
		// 官方 H3 教程里唯一推荐的提速手段(实测 91.6 → 44.8 秒/步,≈2.04 倍)
		args = append(args, "--use-sage-attention")
	}
	return args
}
