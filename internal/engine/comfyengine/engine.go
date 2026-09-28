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

	// launchMu 让"启动 ComfyUI"单飞（2026-09-29 运行时审计 P2-5）。
	// 见 launch 的注释：并发探活失败会拉起两个实例，各吃 20GB 权重 → OOM。
	launchMu sync.Mutex

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
//
// ## 单飞（2026-09-29 运行时审计 P2-5）
//
// `Available()` 是"先探活、失败就启动"，而它被十几个入口并发调用（启动时的
// `StartOnBoot`、管理页的 `StartAdmin`、各种一次性操作）。两处同时探活失败时，
// 两个 `cmd.Start()` 都会执行 —— 端口只有一个能绑，另一个报错退出或反复重试；
// 而 `setOwnedProc` / `writePidFile` 是**后者覆盖前者**，于是代管句柄指向错的 pid，
// 下次 `adoptFromPidFile` 认领到错进程。
//
// 最坏形态正是文档里反复记的那个：两个实例各吃 20GB 权重 → OOM / 换页 →
// "定妆照整批失败、单镜 20+ 分钟"。
//
// 所以整个启动过程加一把锁：**并发者排队等**，等到的第一个启动成功后，
// 后面的会在 `Available()` 的探活处直接命中（见那里的二次探活）。
func (e *Engine) launch(ctx context.Context, c config.ComfySettings) error {
	e.launchMu.Lock()
	defer e.launchMu.Unlock()
	// 排队期间别人可能已经把它拉起来了 —— 拿锁后**再探一次**，避免重复启动。
	if e.client() != nil && e.client().Alive(ctx) {
		return nil
	}
	return e.launchLocked(ctx, c)
}

// launchLocked 真正的启动过程（调用方须持 launchMu）。
func (e *Engine) launchLocked(ctx context.Context, c config.ComfySettings) error {
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
	// **子进程已拿到自己的句柄副本，父进程这份要立刻关**（2026-09-29 运行时审计 P3-12）。
	//
	// 原来只在 `Start()` **失败**时 Close，成功路径不关 —— 于是父进程在整个生命周期里
	// 额外持有一份日志写句柄（靠 `os.File` 的 finalizer 在 GC 时才回收）。
	// 更实际的影响不是句柄耗尽（每次 1–2 个，GC 会收），而是：**一键清理想截断引擎日志时
	// 会因句柄被占而失败**（Windows 上尤其明显）。
	//
	// Windows 下 Go 的 exec 会为子进程显式复制句柄，关掉父进程这份不影响子进程继续写。
	if logF != nil {
		logF.Close()
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
//
// **成片时长仍会与普通渲染差 ±若干帧**(2026-09-26 渲染管线审查更正了原先
// "保证成片时长不缩水"的承诺,它不成立):
//
//	普通:AlignLength(L)              = 17k+5,  例如 L=96 → 107 帧
//	接缝:AlignLength(L+22) − 22      = 17k,     例如 L=96 → 124−22 = 102 帧(短 5 帧)
//	                                              L=120 → 158−22 = 136 帧(长 12 帧)
//
// 这是网格本身决定的,不能靠"对齐后再补 22"绕开:提交的长度必须落在 17k+5 上,
// 否则 H3 侧的视频 VAE 会按**较低的网格点**编码,而那一档位覆盖的是输入的前
// 17m+5 帧而不是最后 17m+5 帧(ComfyUI-H3-Motion-Context/nodes.py:79-90 写明
// 了这条公式与后果)—— 于是模型实际只按 AlignLength(L) 帧出图,再裁掉 22 帧头,
// 成片反而更短(上例会变成 85 帧)。所以这里保持"提交值落在网格上"这一硬约束,
// 并把差异如实记在此处,不再宣称时长不缩水;真正要对齐时长应在成片侧处理。
func seamRenderLength(jobLength int, seamActive bool) int {
	if seamActive {
		return engine.AlignLength(jobLength + seamHeadFrames)
	}
	return engine.AlignLength(jobLength)
}

// seamHeadFrames 接缝钉入的帧数(与 buildRenderGraph 里 MotionContext 的
// context_length 一致;改一处必须改另一处,由测试钉住)。
const seamHeadFrames = 22

// producedFrames 本次提交**实际会产出**的帧数(成片时长口径)。
//
// 它不等于调用方给的 job.Length,有两个来源的差异,两者此前都只活在代码注释里:
//
//	① 网格量化:提交长度必须落在 17k+5 上,AlignLength 会把计划帧数改写(计划 96 → 107);
//	② 接缝裁剪:接缝生效时提交长度里含 seamHeadFrames 帧的 pinned 头,Trim 节点会把它裁掉。
//
// 上层(镜头清单、时长对账、界面)此前只能拿计划帧数当实际值,于是"云端把 4 秒抬成 5 秒"
// 这类改写对比不出来(乙6)。这里把它做成**唯一实现**,由 Render 回填 engine.Result。
func producedFrames(jobLength int, seamActive bool) int {
	n := seamRenderLength(jobLength, seamActive)
	if seamActive {
		n -= seamHeadFrames
	}
	return n
}

// localRenderResult 组装本地引擎的渲染结果。
//
// 抽成函数是为了让"实际帧数 / 时长是否被改写"有**运行期**可断言的面(Render 本身要一台
// 真的 ComfyUI 才跑得起来;这条口径不该只靠源码断言看着)。
func localRenderResult(dest, name string, jobLength int, seamActive bool) *engine.Result {
	actual := producedFrames(jobLength, seamActive)
	return &engine.Result{
		VideoPath: dest, Engine: name,
		ActualFrames: actual,
		// 计划帧数与实际产出一致时不算"被改写"(网格量化与接缝裁剪都会让它为 true)
		DurationAdjusted: actual != jobLength,
	}
}

func (e *Engine) Render(ctx context.Context, job engine.Job) (*engine.Result, error) {
	touchBusy()
	// 收尾也打一次点(2026-09-26 渲染管线审查):空闲看门狗只认「最近一次引擎调用」,
	// 而管线在镜与镜之间有一段不碰引擎的间隙(下载产物、写清单、后处理)。旧实现只在
	// 入口打点,于是单镜耗时一旦超过 IdleStopMin(默认 10 分钟;refine/quality 档与长镜
	// 都属于此列),巡检正好落在镜间那 1~3 秒空档就把引擎判成空闲并 taskkill ——
	// 下一镜白付一次冷启动,更糟的是顺手打断刚提交的任务。
	defer touchBusy()
	c := e.cfg()
	// 帧数缺失必须当场报错(2026-09-26 渲染管线审查):Job.Length 的注释写着
	// "17k+5 网格,engine 自行对齐",而 AlignLength(0) 会把它兜成 5 帧(0.21 秒)——
	// 漏传 Length 的调用方(音频驱动接口)会拿到一支 5 帧的片子,而接口照样返回
	// 成功、日志照写「✔ 音频驱动完成」。0/负数不是"未填"的合法写法。
	if job.Length <= 0 {
		return nil, fmt.Errorf("渲染帧数未指定(job.Length=%d):调用方必须传计划帧数(=时长×24),对齐到 H3 的 17k+5 网格由引擎负责", job.Length)
	}
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
	// 否则成片会比计划短 22 帧;**降级时必须不加**(见 seamRenderLength 的说明:
	// 网格量化后成片与普通渲染仍差 −5/+12 帧,那不是漏补,是 17k+5 网格决定的)。
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
	if condCacheEnabled(job, c) && e.client().HasNode(ctx, classCondSave) {
		// 键必须覆盖**全部**参与 encode 图的输入(2026-09-26 渲染管线审查)。
		// 旧实现只放参考图的内容指纹,于是:
		//   - 音频不在键里 —— 音频是条件的一等输入(addConditionNode 的 ref_audios),
		//     同一镜换一版配音重跑「音频驱动」时 prompt/refs/w/h/length 全同 → 命中
		//     上一次的条件 → 采样吃的是**旧配音**编码出的隐空间,口型仍跟着旧音频,
		//     而接口返回 ok:true、日志写「✔ 音频驱动完成」(该功能最核心的承诺被静默破坏);
		//   - 模式/加速档/CLIP/VAE 也不在键里 —— 它们决定用哪个条件节点、哪套编码器与
		//     哪张 VAE(express 档换 int8 VAE,见 loaders),变了同样必须重编码。
		keyParts := condKeyParts(job, c)
		// 键格式换代:旧格式的记账/数据一次性清掉(否则它们既命中不了也删不掉)
		purgeLegacyCondCache(c, job.ProjectID)
		cacheName := condCacheName(job.ProjectID, job.ShotID, job.Prompt, keyParts, w, h, length)
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
	// 这里**不再**逐镜 /free(2026-09-26 渲染管线审查):unload_models 会把 UNet
	// (~20GB)+CLIP+VAE 从显存卸掉,下一镜得重新从磁盘加载。管线侧的口径是
	// "渲染期间模型常驻、收尾才交还"(coordinator 的 releaseVRAM /
	// ReleaseVRAMIfIdle),逐镜释放等于一集 N 镜多付 N 次加载,还把两阶段条件缓存
	// 省下的时间吃掉;而它是本模块里唯一一处没有理由注释的显存动作。
	return localRenderResult(dest, e.Name(), job.Length, seamActive), nil
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

// condPtDirs H3 条件缓存的数据目录:插件把 CONDITIONING 序列化为 .pt 存于此。
// 与 sidecar 记账目录是**两个不同的位置** —— 数据在 ComfyUI 侧,记账在本应用侧。
//
// 目录解析与清理/管理页共用 comfydirs.go 的实现(2026-09-26 渲染管线审查):
// 旧实现只拼 `<root>/ComfyUI/models/conditioning`,而本机模型在
// `<root>/ComfyUI/shared/models/`(extra_model_paths 的 base_path)—— 于是删除项目时
// 一个 .pt 都没删,日志却照样打「已清理」,目录只增不减(历史堆积 4.9GB / 17.4GB)。
func condPtDirs(c config.ComfySettings) []string {
	return ModelDirs(c.RootDir, "conditioning")
}

// removeCondData 删除某条缓存对应的 .pt 数据文件,返回是否真的删掉了一个。
func removeCondData(dirs []string, cacheName string) bool {
	removed := false
	for _, dir := range dirs {
		p := filepath.Join(dir, cacheName+".pt")
		if err := os.Remove(p); err == nil {
			removed = true
		} else if !os.IsNotExist(err) {
			log.Printf("[comfy] ⚠ 条件缓存数据删除失败:%s: %v", p, err)
		}
	}
	return removed
}

// condKeyVersion 条件缓存键的格式版本。**改动键的组成时必须一起改它**:它保证
// 旧格式的 .ok/.pt 一律不再被命中,并由 purgeLegacyCondCache 做一次性清理 ——
// 否则那些文件既命中不了、也不会被删(原先只在删项目时才清),只增不减。
// 变量而非常量:测试要把它顶到下一版来钉住"版本参与哈希"。
var condKeyVersion = "ck2"

// condPurgeMu 串行化"换键清理"与缓存读写:RENDER 可并发(批量产线),两个镜头同时
// 进来时,若都判定"还没迁移"就会互相删掉对方刚写好的 .ok。标记文件写在临界区内,
// 因此后到者一定看到标记而跳过。
var condPurgeMu sync.Mutex

// purgeLegacyCondCache 键格式换代时,清掉本项目**旧格式**的条件缓存(记账 + 数据)。
//
// 只在项目目录里没有当前版本的标记文件时执行一次;清完写标记。清掉的只是缓存,
// 下一次渲染会重新预编码(一次性成本),换来的是"旧键不再可能被命中"这件事可验证。
func purgeLegacyCondCache(c config.ComfySettings, projectID string) {
	if strings.TrimSpace(projectID) == "" {
		return
	}
	condPurgeMu.Lock()
	defer condPurgeMu.Unlock()

	dir := condProjectDir(c, projectID)
	marker := filepath.Join(dir, ".key"+condKeyVersion)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	dirs := condPtDirs(c)
	removedDocs, removedData := 0, 0
	if entries, err := os.ReadDir(dir); err == nil {
		for _, ent := range entries {
			name := ent.Name()
			if ent.IsDir() || !strings.HasSuffix(name, ".ok") {
				continue
			}
			cache := strings.TrimSuffix(name, ".ok")
			if removeCondData(dirs, cache) {
				removedData++
			}
			if os.Remove(filepath.Join(dir, name)) == nil {
				removedDocs++
			}
		}
	}
	if removedDocs > 0 || removedData > 0 {
		log.Printf("[comfy] 条件缓存键格式化(%s):已清理旧缓存 %d 条记账 / %d 个 .pt(下次渲染会重新预编码)", condKeyVersion, removedDocs, removedData)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return // 写不了标记就下次再试,不影响渲染
	}
	_ = os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0o644)
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
// 2026-09-26 又修了另一半:数据目录只认标准布局,shared 布局下一个都找不到,
// 却照样报「已清理」—— 现在按 ModelDirs 遍历全部布局,并对"没删干净"如实告警。
func (e *Engine) CleanupProjectCache(projectID string) {
	c := e.cfg()
	sidecar := condProjectDir(c, projectID)

	// .ok 的文件名就是 cacheName(不含扩展名),据此定位同名 .pt
	var caches []string
	if entries, err := os.ReadDir(sidecar); err == nil {
		for _, ent := range entries {
			name := ent.Name()
			if ent.IsDir() || !strings.HasSuffix(name, ".ok") {
				continue
			}
			caches = append(caches, strings.TrimSuffix(name, ".ok"))
		}
	}

	dirs := condPtDirs(c)
	removed := 0
	for _, cache := range caches {
		if removeCondData(dirs, cache) {
			removed++
		}
	}

	if err := os.RemoveAll(sidecar); err != nil {
		return
	}
	switch {
	case len(caches) == 0:
		log.Printf("[comfy] 已清理项目条件缓存:%s(记账里没有 .pt 需要删)", sidecar)
	case removed < len(caches):
		// 报成功却没删干净是这条路径的老毛病,必须让人看见
		log.Printf("[comfy] ⚠ 项目条件缓存只清掉 %d/%d 个 .pt:%s(已查找目录:%s —— 其余可能在别处或已被手动删除)",
			removed, len(caches), sidecar, strings.Join(dirs, "; "))
	default:
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

// condKeyParts 条件缓存键的输入清单:**凡是会改变 encode 图产出的东西都必须在这里**。
//
// 单独立成函数是为了能被测试逐个钉住(每个输入变一下,键必须变)——
// "缓存键漏项"的症状是"改了配置/换了素材却静默复用旧条件",没有别的观测面。
// 覆盖范围:
//
//	参考图  路径 + 内容指纹(同名覆盖定妆照也要失效)
//	参考音频 路径 + 内容指纹(**换配音重跑音频驱动必须失效**,审计 2026-09-26 高危)
//	mode    决定用 Ref2V 还是 I2V 条件节点
//	accel   决定挂哪条加速 LoRA(只影响采样),但 express 会换 int8 视频 VAE
//	CLIP / 视频 VAE / 音频 VAE 文件名  决定编码器本身
func condKeyParts(job engine.Job, c config.ComfySettings) []string {
	parts := make([]string, 0, len(job.RefImages)+len(job.Audios)+5)
	for _, ref := range job.RefImages {
		parts = append(parts, "img:"+ref.Path+":"+fileFingerprint(ref.Path))
	}
	for _, a := range job.Audios {
		parts = append(parts, "audio:"+a+":"+fileFingerprint(a))
	}
	return append(parts,
		"mode:"+job.Mode,
		"accel:"+job.Accel,
		"clip:"+c.CLIP,
		"vae:"+videoVAEFor(c, job.Accel),
		"audio_vae:"+c.AudioVAE,
	)
}

// condCacheName 条件缓存键名(含项目子目录前缀)。
// keyParts 是**全部**参与 encode 图的输入的可辨认摘要(参考图/音频内容指纹、模式、
// 加速档、CLIP/VAE 文件名…):凡是会改变 CONDITIONING 的输入都必须在里面,否则就是
// 「改了设置/换了素材却跳过重编码」的静默错(2026-09-26 渲染管线审查)。
// 文件落盘路径:cond_cache/{projectID}/s{shotID}_{hash}.ok
func condCacheName(projectID string, shotID int, prompt string, keyParts []string, w, h, length int) string {
	sum := md5.Sum([]byte(fmt.Sprint(condKeyVersion, projectID, shotID, prompt, keyParts, w, h, length)))
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
		// autogrow 输入的 API 提交键是「聚合键.模板名」(如 ref_images.ref_image_0)——
		// 写成聚合数组 ref_images=[节点引用…] 会被 ComfyUI 静默丢成空 dict。见 autogrow.go。
		for i, name := range uploaded {
			inputs[autogrowKey("ref_images", "ref_image_", i)] = link(loadImageNode(g, i, name), 0)
		}
		// 音频参考(音频驱动 Audio Drive):H3 Ref2V 原生支持音频条件 —— 喂入定稿
		// 配音后,人物的口型与动作节奏会跟随音频。这是"精确台词"路线的入口:
		// 先把配音做成确定资产,再用它驱动画面,而不是让模型自由发挥台词。
		if len(audios) > 0 {
			for i, name := range audios {
				inputs[autogrowKey("ref_audios", "ref_audio_", i)] = link(loadAudioNode(g, i, name), 0)
			}
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

// videoVAEFor 本次作业实际用的视频 VAE 文件名。**唯一实现**。
//
// 抽出来是因为它有两个消费者:loaders 拿它建图,条件缓存键拿它做键的一部分。
// 各写一份的后果是"express 档换了 int8 VAE 却复用旧 VAE 算出的条件隐空间"。
func videoVAEFor(c config.ComfySettings, accel string) string {
	if accel != "express" {
		return c.VideoVAE
	}
	// 极速档:int8 量化 VAE 解码提速 30-50%(有损,预览/抽卡用;文件缺失回退 fp16)
	const int8VAE = "minimax_h3_video_vae_int8_convrot.safetensors"
	if modelFileExists(c, int8VAE, []string{"vae"}) {
		return int8VAE
	}
	return c.VideoVAE
}

func loaders(c config.ComfySettings, accel string) map[string]any {
	return map[string]any{
		"1": nd("CLIPLoader", map[string]any{"clip_name": c.CLIP, "type": "minimax"}),
		"2": nd("VAELoader", map[string]any{"vae_name": videoVAEFor(c, accel)}),
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
// 极速预览档的两套配方（expressRecipe 决定用哪套）。
//
// 2026-09-27（ComfyUI 侧交付单 `docs/ComfyUI侧交付-加速路线与显存回退-2026-09-27.md`）：
// 盘上新增社区转换的 **TaoMate 三步 LoRA**（1.16 GiB，挂现有 int8 底模、不需换运行时），
// 交付单在 124 帧上实测 **310.1s → 156.4s（1.98×）**、音轨完好。
// 平台侧据此做的**唯一改动**是让"极速预览"这一档吃到它 —— 不新增档位、不动交付默认：
//
//   - `express` 的用途本来就是**抽卡/筛方向**（"极速预览"），而交付单把这套配方的适用面
//     明确收窄到同一件事（3 步下参考图对画面的影响变大，建议先筛方向再上正式档）；
//   - 交付默认（`quality` / `turbo` / `refine`）**一个字都没动** —— 3 步路线的身份与细节
//     保真度没有被交付级验证过，不该上交付。
//
// **LoRA 与步数必须成对决定**：turbo 是 4 步蒸馏、TaoMate 是 3 步蒸馏，混着配（3 步 + 4 步
// LoRA）会偏离训练锚点。所以这里只有一个决定点，`accelLoraFor` 与 `accelSteps` 都问它 ——
// 本文件已经吃过一次"LoRA 那一半收口了、步数那一半没跟上"的亏（见 accelSteps 注释）。
const (
	expressTaoMateLoRA  = "TaoMate-H3-3step-ComfyUI.safetensors"
	expressTaoMateSteps = 3
	expressTurboSteps   = 4
)

// expressRecipe 极速预览档的配方：返回 (LoRA 文件名, 步数)。
//
// 用文件存在性做开关（而不是加一个设置项）：交付单给的是"文件在盘、配方已验证"，
// 谁不想要这套就等于谁不想要这个文件 —— 改名/删除即回到原来的 turbo@4，
// 不需要再改一次代码与设置页。
func expressRecipe(job engine.Job, c config.ComfySettings) (string, int) {
	if modelFileExists(c, expressTaoMateLoRA, []string{"loras"}) {
		return expressTaoMateLoRA, expressTaoMateSteps
	}
	return turboLoRAFor(job, c), expressTurboSteps
}

// turboLoRAFor 按模式选官方 Turbo 蒸馏 LoRA（ref2va / fl2va 各一份）。
func turboLoRAFor(job engine.Job, c config.ComfySettings) string {
	if job.Mode != "ref2va" {
		return c.TurboLoRAFL2VA
	}
	return c.TurboLoRARef2VA
}

// accelQualityPlan quality 档**打算**挂哪条加速 LoRA，以及"挂不上"的原因（空 = 能挂）。
//
// 抽出来是为了让**选路**（accelLoraFor）与**播报**（AccelTierNote）共用同一条判据 ——
// 两处各写一遍必然走散（本仓记过多次：同一个语义两处实现，改一处漏一处）。
//
// 判据本身**没有改动**（量化底模与量化底模不兼容 Acc-8Step，是有意设计且有测试的：
// accel_lora_test.go 的 TestAccelQualityLoraGuard/…NoAccFile）；这里只把结论说出来。
func accelQualityPlan(job engine.Job, c config.ComfySettings) (string, string) {
	acc := "MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy.safetensors"
	if job.Mode != "ref2va" {
		acc = "MiniMax-H3-FL2VA-Acc-8Step_pruned_comfy.safetensors"
	}
	if !modelFileExists(c, acc, []string{"loras", "diffusion_models"}) {
		return "", fmt.Sprintf("盘上没有 %s", acc)
	}
	if base := unetFor(job, c); isQuantizedBase(base) {
		return "", fmt.Sprintf("底模 %s 是**量化版**，Acc-8Step 与量化底模不兼容（tensor 32 vs 1024）", base)
	}
	return acc, ""
}

// AccelTierNote 档位与实际挂上的 LoRA **不一致**时给一行可检索的说明（一致时返回空串）。
//
// 由来（ComfyUI 侧整改单 2026-09-27 · P0-2）：本机 4 个 H3 底模全是 int8/w4a8，
// 而 quality 档要的非剪枝底模是 40 GB 级 —— 所以用户选 `quality` **必然**回退 turbo，
// 而回退是**静默**的：界面上写着"画质档"，跑出来的却是交付默认那套，
// 事后只能靠翻代码才知道"我选的档没生效"。
//
// 只加可见性，**不动选路**（门禁与既有测试原样保留）。
func AccelTierNote(accel, mode string, c config.ComfySettings) string {
	if accel != "quality" {
		return ""
	}
	if _, why := accelQualityPlan(engine.Job{Accel: accel, Mode: mode}, c); why != "" {
		return fmt.Sprintf("所选「画质档(quality)」**没有生效**，实际按 turbo 档渲染：%s。"+
			"画质档需要**非剪枝 H3 底座**（40 GB 级，本机没有）；要真正的画质档请先补那个底座，"+
			"否则这一步与「默认档」出的图完全一样", why)
	}
	return ""
}

// accelLoraFor 量化底模自动回退 Turbo,配合 fp16 VAE + ref_image_size=max。
func accelLoraFor(job engine.Job, c config.ComfySettings) string {
	if job.Accel == "refine" {
		return "" // 精修档:什么加速都不挂,让底模按自己的步数好好出图
	}
	if job.Accel == "express" {
		lora, _ := expressRecipe(job, c)
		return lora
	}
	turbo := turboLoRAFor(job, c)
	if job.Accel != "quality" {
		return turbo
	}
	if acc, why := accelQualityPlan(job, c); why == "" {
		return acc
	}
	return turbo
}

// accelSteps 档位 → 实际采样步数。**唯一实现**:凡自带采样链的图都必须走它。
//
// 单独立出来的原因(2026-09-26 渲染管线审查抓到):步数钳制此前只写在 buildRenderGraph 里,
// 而 maskgraph(局部重绘)是照抄的模型链 —— 加速 LoRA 那一半早就收口(两边都调 accelLoraFor),
// 步数这一半没跟上:refine 档在局部重绘里会变成「8 步 + 不挂任何蒸馏 LoRA」= 白关蒸馏,
// 修补过的那块与周围画面画质断层,而日志与界面都不提。这正是本项目记过一次的
// "同一个语义两处实现"事故,当时的护栏只比了 LoRA 名、没比步数。
//
// 2026-09-27:参数里带上 job/c —— 极速预览档的步数**必须跟着它实际挂的那条 LoRA 走**
// (turbo 是 4 步蒸馏、TaoMate 是 3 步蒸馏),问同一个 expressRecipe 才不会配出跨锚点的组合。
func accelSteps(job engine.Job, c config.ComfySettings, steps int) int {
	switch job.Accel {
	case "refine":
		// 精修档:不挂蒸馏 LoRA,步数按"完整采样"给足 —— 4 步蒸馏的锚定值在这里无意义。
		if steps < 20 {
			return 20
		}
	case "quality":
		if steps < 8 {
			return 8 // Acc-8Step 蒸馏锚定 8 步
		}
	case "express":
		if _, recipeSteps := expressRecipe(job, c); steps > recipeSteps {
			return recipeSteps // 极速预览:步数与所挂 LoRA 的训练锚点一致
		}
	}
	if steps <= 0 {
		return 8
	}
	return steps
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

	// 步数:档位表只有一份实现(accelSteps),局部重绘等其它采样链也调它
	steps := accelSteps(job, c, job.Steps)

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
