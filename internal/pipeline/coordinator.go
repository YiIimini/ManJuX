package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
	"github.com/YiIimini/ManJuX/internal/sysmon"
)

// Phase 阶段标识。
const (
	PhaseScript   = "script"
	PhaseAssets   = "assets"
	PhaseRender   = "render"
	PhaseQC       = "qc"
	PhaseAssemble = "assemble"
	PhaseAll      = "all"
)

// PhaseNames 阶段中文名(顺序即执行顺序)。
var PhaseNames = []struct{ ID, Name, Icon string }{
	{PhaseScript, "剧本分镜", "script"},
	{PhaseAssets, "资产定妆", "assets"},
	{PhaseRender, "渲染出片", "render"},
	{PhaseQC, "质检", "qc"},
	{PhaseAssemble, "合成成片", "assemble"},
}

// Coordinator 管线协调器:每项目最多一个运行实例,阶段可单跑。
type Coordinator struct {
	mu     sync.Mutex
	runs   map[string]*Run
	result map[string]string // 最近一次运行结束原因:completed/stopped/failed(批量排队判断用)
	store  *project.Store
	cfg    *config.Manager
	reg    *engine.Registry
	ff     *ff.FF
	bus    *Bus
	lib    *library.Library // 跨项目资产库(可为 nil=禁用)

	// 清单落盘失败的**聚合**（2026-09-28 审计 A12）。
	//
	// `setShotStatusLen` 每一镜都要落一次清单，失败时不能逐镜播报（刷屏），
	// 更不能静默（用户只看到"渲染完成"，重开应用才发现几十镜的状态全没记上）。
	// 所以在这里记"首次错误 + 次数"，阶段末统一报一次。
	manifestErrMu    sync.Mutex
	manifestErrCount int
	manifestErrFirst error

	// musicGen 配乐生成器的**注入点**（nil = 走真实引擎）。
	//
	// 为什么放在实例上而不是包级变量（2026-09-28 并发审计）：包级可变状态是"测试写、产线读"，
	// 一旦有测试并行跑就会互相踩（而且这正是 Go 竞态检测会报的那一类）。放实例上，每个
	// Coordinator 各用各的，测试之间天然隔离。
	// 需要它的原因：配乐生成依赖 13.68 GB 的本地模型，测试机不一定有；而"生成→入库→复用"
	// 这条链**本身**的逻辑（入库名字、来源标记、失败措辞）与此无关、必须能测。
	musicGen func(ctx context.Context, tags string, seconds float64) (string, error)

	// vlm 视觉调用的闸门（限流熔断 + 调用预算，见 vlm_guard.go）。
	// 进程级：限流是同一份 key 的全局状态，换项目重来照样会被拒。
	vlm vlmGuard
}

// Run 一次运行。
type Run struct {
	Cancel         context.CancelFunc
	Phase          string
	StartedAt      time.Time
	EngineOverride string // 本次运行的引擎覆盖(auto/comfy/api,空=用项目配置)
	SeedOverride   int64
}

// RunResult 运行结束原因枚举。
const (
	ResultCompleted = "completed" // 全部阶段自然跑完
	ResultStopped   = "stopped"   // 被用户/外部取消
	ResultFailed    = "failed"    // 阶段报错中止
)

// New 创建协调器。
func New(store *project.Store, cfg *config.Manager, reg *engine.Registry, f *ff.FF, bus *Bus, lib *library.Library) *Coordinator {
	return &Coordinator{
		runs:   map[string]*Run{},
		result: map[string]string{},
		store:  store, cfg: cfg, reg: reg, ff: f, bus: bus, lib: lib,
	}
}

// SetResult 记录本次运行结束原因;Result 读取后即清除(只保留最近一次)。
func (c *Coordinator) SetResult(projectID, res string) {
	c.mu.Lock()
	c.result[projectID] = res
	c.mu.Unlock()
}

// Result 读取最近一次运行结束原因(completed/stopped/failed;无记录返回空)。
func (c *Coordinator) Result(projectID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, ok := c.result[projectID]
	if !ok {
		return ""
	}
	delete(c.result, projectID)
	return res
}

// Running 返回项目当前运行信息(未运行返回 nil)。
func (c *Coordinator) Running(projectID string) *Run {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.runs[projectID]; ok {
		cp := *r
		return &cp
	}
	return nil
}

// HasRunning 是否有任一项目在运行(关窗保护用)。
func (c *Coordinator) HasRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.runs) > 0
}

// Start 启动阶段执行。phase=all 为全流程;only 为镜头过滤(render 阶段);
// engineOverride 非空时本次运行覆盖项目引擎(镜头级分派:auto/comfy/api)。
//
// **全局单管线铁律(2026-09-12 用户定调「管线生产需要合理化」)**:同一时刻只允许
// 一条管线在跑。多集并发共用**一张显卡**,不是"更快"而是互相伤害:出图任务排在
// 长视频任务后面排队(旧实现排队时间还计入超时预算 → 定妆照整批被判"渲染超时"),
// 单镜从 3-5 分钟涨到 20+ 分钟(显存被反复换入换出)。实测三条管线并发时,角色与
// 场景定妆照接连失败、镜头因缺参考图降级纯文生。要并行请换一张卡,不要靠并发本集。
func (c *Coordinator) Start(projectID, phase string, only []int, engineOverride string, seedOverride int64) error {
	if phase == "" {
		phase = PhaseAll
	}
	c.mu.Lock()
	if _, dup := c.runs[projectID]; dup {
		c.mu.Unlock()
		return fmt.Errorf("项目正在运行中,请先停止")
	}
	// **按资源分类的并发规则**（用户 2026-09-28：「LLM 继续解析分镜脚本，他们互不影响的，可以分开跑的，
	// 这种情况以后需要考虑到如何合理分配任务」）：
	//   · 抢显卡的阶段（资产出图 / 渲染）**互相排斥** —— 实测并发会让定妆照超时、单镜从 3-5 分钟涨到 20+ 分钟；
	//   · 不碰显卡的阶段（剧本分镜＝纯 LLM 调用、合成＝CPU/ffmpeg）**可以与显卡阶段并行**。
	//
	// 注意这条规则只负责**放行**并行，不主动替谁抢跑：批量产线本身是逐集串行的，一集的
	// LLM 解析只属于那一集（见 server/novel_split.go 的逐集串行铁律）。这里放行的是用户
	// 自己发起的并行（例如甲集在渲染时，他又去点乙集的「剧本分镜」）。
	for id, r := range c.runs {
		if phaseNeedsGPU(phase) && phaseNeedsGPU(r.Phase) {
			c.mu.Unlock()
			return fmt.Errorf("另一条管线正在运行(《%s》，阶段 %s):显卡只能同时喂一条管线"+
				"(实测并发会让定妆照生成超时、单镜从 3-5 分钟涨到 20+ 分钟)。"+
				"不碰显卡的阶段(如剧本分镜)可以与它并行，请等它跑完或先停止本集", id, r.Phase)
		}
	}
	proj, ok := c.store.Get(projectID)
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &Run{Cancel: cancel, Phase: phase, StartedAt: time.Now(), EngineOverride: engineOverride, SeedOverride: seedOverride}
	c.runs[projectID] = run
	c.mu.Unlock()

	go c.execute(ctx, proj, phase, only, nil, engineOverride, seedOverride, run)
	return nil
}

// RegenAsset 单资产重生成(krea2 换 seed 重出图,更新项目与库)。
func (c *Coordinator) RegenAsset(projectID, assetID string) error {
	proj, ok := c.store.Get(projectID)
	if !ok {
		return fmt.Errorf("项目不存在: %s", projectID)
	}
	plan, err := c.store.LoadPlan(projectID)
	if err != nil {
		return fmt.Errorf("尚未生成分镜方案")
	}
	kind, name, desc, prompt := "", "", "", ""
	for _, ch := range plan.Characters {
		if "char:"+ch.ID == assetID {
			kind, name, desc, prompt = "characters", ch.Name, ch.Description, ch.ImagePrompt
		}
	}
	for _, sc := range plan.Scenes {
		if "scene:"+sc.ID == assetID {
			// 场景的 image_prompt **不在这里取原值**:它必须过场景装配唯一实现点
			// (见下面的 SceneImagePrompt 分支)—— 原样取用正是乙1 要修的那个洞。
			kind, name, desc = "scenes", sc.Name, sc.Description
		}
	}
	// 道具(2026-09-27 补):资产阶段开始出道具图,抽屉里也就有了「重新生成」这颗按钮 ——
	// 少了这一支,点它只会得到"资产不存在于方案"(而资产明明在)。提示词同样**不在循环里取原值**,
	// 走下面的 PropImagePrompt 装配点。
	for _, pr := range plan.Props {
		if "prop:"+pr.ID == assetID {
			kind, name, desc = "props", pr.Name, pr.Description
		}
	}
	if kind == "" {
		// Q 版等派生资产:直接用资产表里存的提示词重生成(不入库)
		if assets, err := c.store.LoadAssets(projectID); err == nil {
			if a, ok := assets[assetID]; ok && strings.TrimSpace(a.ImagePrompt) != "" {
				kind, name, prompt, _ = a.Kind, a.Name, a.ImagePrompt, ""
			}
		}
	}
	if kind == "" {
		return fmt.Errorf("资产不存在于方案: %s", assetID)
	}
	// Q 版(chibi)方形画幅,其余竖版半身/整件
	isChibi := strings.HasSuffix(assetID, "_chibi")
	// 实体类型(按类型重写取景与画风;Q 版资产强制按 Q 版那一套渲染)
	ek := ResolveEntityKind(project.Character{Name: name, Description: desc, ImagePrompt: prompt})
	if isChibi {
		ek = KindChibi
	}
	// 同剧对比+风格具象化注入:走**唯一装配点**(PortraitPrompt),与产线生成、
	// 渲染前补齐、资产库三条路完全同一套约束。此前这里自己写了一段,幂等判据还是
	// 另外两个字符串("RENDER STYLE"/"CAST DISTINCTNESS")—— 于是"重新生成的那张
	// 定妆照"与产线那张可能不是同一套约束,而它们是同一个角色的脸(审计 P1-2)。
	if kind == "characters" {
		// Q 版资产强制按 Q 版那一套渲染;其余按实体类型(器物不能套人像取景)
		prompt = PortraitPrompt(PortraitSubject{
			Kind: ek, Name: name, Description: desc, ImagePrompt: prompt,
			FacesOn: proj.Config.Params.Script.FacesOn(),
		}, plan)
	} else if kind == "scenes" {
		// 场景同理走**场景装配唯一实现点**(SceneImagePrompt,乙1):这条"单个资产重新生成"
		// 是第五条会出像素的路径,漏掉它 = 用户点一次「重新生成」就把场景图打回裸提示词,
		// 又变回"角色一套画风、场景另一套"。提示词按 sc 重新装配,不用上面循环里那份原文。
		for _, sc := range plan.Scenes {
			if "scene:"+sc.ID == assetID {
				prompt = SceneImagePrompt(proj, plan, sc)
				break
			}
		}
	} else if kind == "props" {
		// 道具同理走**道具装配唯一实现点**(PropImagePrompt,2026-09-27):白底单件那套画面纪律
		// 只能有一个出口,漏掉它 = 用户点一次「重新生成」就把道具图打回裸提示词,
		// 于是"人审用的白底单件"变成一张带场景的图,跨镜比对就无从谈起。
		for _, pr := range plan.Props {
			if "prop:"+pr.ID == assetID {
				prompt = PropImagePrompt(proj, plan, pr)
				break
			}
		}
	}
	eng, err := c.reg.Get("comfy")
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := eng.Available(ctx); err != nil {
		return fmt.Errorf("单资产重生成需要本地 ComfyUI: %w", err)
	}
	c.emit(projectID, PhaseAssets, 0, "info", 0, "重新生成:%s「%s」%s…", kindLabel(kind), name, entityKindSuffix(kind, ek))
	// 尺寸口径与产线一致:资产图 = 项目的成片画幅(见 stages_assets.go 的 assetImageSize)。
	// 旧实现在这里也是写死的 832×1248 + 场景按方向翻转 —— "重新生成的那张"与产线那张
	// 比例不同,同一个角色在参考图里就已经是两种构图。
	w, h := assetImageSize(proj, kind)
	if isChibi {
		w, h = 1024, 1024 // Q 版是方幅(与产线 Q 版同一口径)
	}
	// 出图交给**唯一出口**(genSheetImage):造型变体卡在这里也必须"从主卡派生"——
	// 用户点「重新生成」的意图是"这一档重出一张",不是"把这个人的脸换一个";而各自
	// 文生图正是同一角色几张脸会漂的来源(见 sheetderive.go)。chibi 资产不是造型档,
	// 由 sheetVariantTrigger 挡在外面,走的仍是原本那一次文生图。
	assets, err := c.store.LoadAssets(projectID)
	if err != nil {
		return err
	}
	path, err := c.genSheetImage(ctx, eng, sheetJob{
		proj: proj, assets: assets, kind: kind, id: strings.TrimPrefix(assetID, kind+":"),
		name: name, prompt: prompt, ek: ek, w: w, h: h,
		seed: RandomSeed(), // 重新生成 = 就是要换一张(随机种子只允许出现在这类显式动作里)
	})
	if err != nil {
		return err
	}
	var rel string
	if r2, e2 := filepath.Rel(c.store.Dir(projectID), path); e2 == nil {
		rel = filepath.ToSlash(r2)
	}
	assets[assetID] = project.Asset{
		ID: assetID, Kind: kind, Name: name,
		File: rel, ImagePrompt: prompt,
		Source: c.imageSourceKey(), Fingerprint: project.FingerprintFile(path),
	}
	if err := c.store.SaveAssets(projectID, assets); err != nil {
		return err
	}
	// 换脸后旧的角度图必须一并作废,并把新图写回资产库(2026-09-12 修):
	//   ①项目侧:同名 <base>_view_*.png / <base>_turnaround.png 对应的是上一张脸,
	//     留着会让"定妆照是新脸、多位图是旧脸"混用;
	//   ②库侧:此前重生成只更新项目、不更新库 —— 库是跨集复用的真源,不写回就等于
	//     后续剧集仍在复用旧脸(实测:同一角色在库里还是修复前的圆脸大眼版本)。
	// 清掉派生图后,多位图会在渲染前自动补齐(或用户点「补全多视角」重建)。
	if !strings.HasSuffix(assetID, "_chibi") {
		ClearDerivedViews(path)
		if c.lib != nil {
			c.lib.ClearRelated(kind, name)
			// 写回按**卡片 ID**：重生成的是同一张卡，名字可能被重抽改过，ID 不会 ——
			// 按名字写回会另起一条条目，库里的旧脸仍旧留着被后续剧集复用。
			if _, err := c.lib.PutCard(kind, strings.TrimPrefix(assetID, "char:"), name, desc, prompt, path, projectID, c.imageSourceKey()); err != nil {
				c.emit(projectID, PhaseAssets, 0, "warn", 0, "⚠ 写回资产库失败(不影响本项目):%v", err)
			}
		}
	}
	c.emit(projectID, PhaseAssets, 0, "success", 0, "✔ 已重新生成:%s「%s」(旧角度图已作废,渲染前会自动补多视角)", kindLabel(kind), name)
	// 重出后**按同一套判据再量一次**(技能侧清单 §5.1):偏离告警里那句"建议重出该卡"
	// 只有配上"重出后再量一遍"才闭环 —— 否则用户按建议重出了,却没有任何反馈说好没好。
	// 这里每次都提醒(未配置视觉模型时),因为这是一次用户动作,不是一次批量循环。
	if kind == "characters" {
		rep, outcome := c.checkPortraitProportion(ctx, proj, ek, prompt, name, path)
		if outcome == proportionNoCapability {
			c.emit(projectID, PhaseAssets, 0, "warn", 0, "⚠ %s", qc.ProportionSkipNoCapability)
		}
		// 朝向未过时**不再自动重出**了(资产阶段那条路会自动重出,这里不):
		// 「重新生成」本来就是用户手动要换一张,而它用的是随机种子 —— 每次点都是新样本。
		// 自动连抽两三次会把一次点击变成"不知道在等什么"的静默等待;如实告诉他
		// "这张不是正面,再点一次通常就好"更省事,也更符合"钱/时间的事让用户拍板"(乙8 同一取向)。
		if qc.FacingNeedsRegen(rep) {
			c.emit(projectID, PhaseAssets, 0, "warn", 0,
				"⚠ 定妆照「%s」不是正面(四分之三侧):定妆照是全片身份参考图,建议再点一次「重新生成」换一颗种子(实测约四次里有三次是正面)", name)
		}
	}
	return nil
}

// setRunPhase 运行中随阶段推进更新投影(/api/status 反映真实当前阶段,
// 而非启动时的 all——前端产线卡状态色依赖它)。
func (c *Coordinator) setRunPhase(projectID, phase string) {
	c.mu.Lock()
	if r, ok := c.runs[projectID]; ok {
		r.Phase = phase
	}
	c.mu.Unlock()
}

// Stop 停止项目运行。
//
// ## 为什么不能无条件打断 ComfyUI（2026-09-29 运行时审计 P1-1）
//
// `Start()` 明确放行"非显卡阶段与显卡阶段并行"（见 :163 的判据）。于是存在这种局面：
// 甲集在渲染（一镜要跑 20 分钟 GPU），用户对**乙集**点「停止」—— 乙集只是 script 阶段，
// 跟 ComfyUI 一点关系都没有。而原来这里无条件调 `interruptComfy()`，
// 后果是**甲集那一镜被掐**：`execution_interrupted` → 该镜判失败 → 写黑场占位进成片，
// 或整阶段 failed → 批量产线卡住不跳集。用户花 20+ 分钟机时换一个黑镜，
// 顺带把他在 ComfyUI WEB 里排的队也清了。
//
// 反证它本来就不该这样：`interruptComfy` 自己的注释写着"**仅在确有 comfy 运行管线时发**"
// —— 代码里一直没有这个判断；而 `stages_render.go` 渲染起点做同一件事时**是有护栏的**。
//
// 现在只在两个条件同时成立时才发：① 被停的这次 run **确实要用显卡**；
// ② 没有**别的** GPU run 在跑（别人在渲染时，中断等于误伤）。
func (c *Coordinator) Stop(projectID string) bool {
	c.mu.Lock()
	r, ok := c.runs[projectID]
	if !ok {
		c.mu.Unlock()
		return false
	}
	r.Cancel()
	phase := r.Phase
	// 还有谁在用显卡（排除自己）
	othersGPU := false
	for id, o := range c.runs {
		if id == projectID {
			continue
		}
		if phaseNeedsGPU(o.Phase) {
			othersGPU = true
			break
		}
	}
	c.mu.Unlock()
	if phaseNeedsGPU(phase) && !othersGPU {
		c.interruptComfy() // 服务端同步中断+清队,防重开渲染堵在旧任务后面
	}
	return true
}

// RunOut Run 的可序列化投影(不含 CancelFunc;/api/status 用)。
type RunOut struct {
	Phase     string    `json:"phase"`
	StartedAt time.Time `json:"started_at"`
	Engine    string    `json:"engine,omitempty"`
	Seed      int64     `json:"seed,omitempty"`
}

// StopAll 停止全部运行中的管线(应用退出时调用;不等待收尾,管线 goroutine 自行感知取消)。
func (c *Coordinator) StopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.runs {
		r.Cancel()
	}
	c.interruptComfy()
}

// interruptComfy 停止管线后向服务端补发中断+清队:ctx 取消只停了本地等待,
// 服务端仍在执行/排队的提示词会占住工作线程——用户停止后重开渲染就会
// 「一直在排队」,直到重启引擎(2026-09-10 实证)。仅在确有 comfy 运行管线时发。
func (c *Coordinator) interruptComfy() {
	eng, err := c.reg.Get("comfy")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	eng.Interrupt(ctx)
	eng.ClearQueue(ctx)
}

// finish 收尾时把这次 run 从登记表里摘掉。
//
// ## 为什么要校验身份（2026-09-29 运行时审计 P1-2）
//
// `Stop()` 只 `Cancel()` 就返回，**不等 goroutine 收尾**；而收尾路径里还要走
// `releaseVRAM()`（若干次带超时的引擎调用，实测窗口 0.1–2.5s，最坏 ~27s）。
// 用户在这个窗口里点「开始」（"看着像卡住了就停掉重来"是最常见的动作）时：
// 新 run 已经写进 `c.runs`，旧 run 的 defer 才跑 —— 按 projectID 无条件删，
// **删掉的是新那一次的登记**。后果三连：
//
//	① 界面上项目显示"未运行"，其实在跑；
//	② `HasRunning()` 假 false → `ReleaseVRAMIfIdle` / 关窗保护会在渲染中卸显存
//	   （正是 `releaseVRAM` 注释里那个"150s/步、45 分钟超时"的坑）；
//	③ `Start()` 的"一卡一管线"判据看不到这次 run → 还能起第三条，
//	   或批量产线判"这集跑完了"直接开下一集 → **两集并发抢一张卡**。
//
// 所以只删**自己那一次**：指针相等才算我的。
func (c *Coordinator) finish(projectID string, r *Run) {
	c.mu.Lock()
	if cur, ok := c.runs[projectID]; ok && cur == r {
		delete(c.runs, projectID)
	}
	c.mu.Unlock()
}

func (c *Coordinator) emit(projectID, phase string, shotID int, level string, progress int, format string, args ...any) {
	// bus 为 nil 时**什么都不发**，而不是 panic。
	//
	// 为什么值得这一行（2026-09-28 被测试撞出来的）：Coordinator 在很多地方是可以"裸造"的
	// （只填它需要的那几个字段 —— 测试、单资产重生成、离线工具都是这么用的）。没有这道判断，
	// 任何一条新代码路径只要在裸 Coordinator 上走到 emit 就整进程崩，而崩的地方离原因很远
	// （症状是某段逻辑跑不通，实际是"没接消息总线"）。事件是**通知**，不是业务状态 ——
	// 发不出去不该让业务挂掉。
	if c.bus == nil {
		return
	}
	c.bus.Publish(Event{
		ProjectID: projectID, Phase: phase, ShotID: shotID,
		Level: level, Progress: progress,
		Message: fmt.Sprintf(format, args...),
	})
}

// pipelineStages 决定一次「全流程」要跑哪些阶段。
//
// 抽成纯函数是为了**让它可被测试钉住**(2026-09-26):这里藏着双侧共组的核心承诺 ——
// 技能端(ManJuNovel)已经把 `plan/plan.json` 交到盘上时,平台**不该再跑一次「LLM 直出分镜」**:
//
//	· 慢且贵:单集一次深度推理 2–6 分钟 + token;
//	· 更糟的是**会毁活**:重新生成会改写每镜提示词 → 镜头指纹全变 → 已渲的镜头全部作废,
//	  相当于"技能端出好的脚本被平台悄悄盖掉,然后从零重渲"。
//
// 所以只有**显式**指定 script 阶段(界面「生成分镜方案」)才重新创作;all 走断点续跑语义。
func pipelineStages(phase string, hasPlan bool) []string {
	if phase != PhaseAll {
		return []string{phase} // 显式指定某一阶段 = 显式意图,不替他省
	}
	if hasPlan {
		return []string{PhaseAssets, PhaseRender, PhaseQC, PhaseAssemble}
	}
	return []string{PhaseScript, PhaseAssets, PhaseRender, PhaseQC, PhaseAssemble}
}

// hasUsablePlan 盘上是否已有**可用**方案(有 shots 才算:空方案等于没有)。
func hasUsablePlan(st *project.Store, projectID string) bool {
	plan, err := st.LoadPlan(projectID)
	return err == nil && plan != nil && len(plan.Shots) > 0
}

func (c *Coordinator) execute(ctx context.Context, proj *project.Project, phase string, only []int, onlyAssets []string, engineOverride string, seedOverride int64, self *Run) {
	// 记录结束原因(completed/stopped/failed)供批量排队判断:中止或失败不得被当作"完成"而自动跳集
	ended := ResultCompleted
	defer func() {
		c.SetResult(proj.ID, ended)
		// 管线收尾:把 GPU 还给桌面(2026-09-12 用户定调「该释放的就进行释放,
		// 专职某一项,避免资源抢占」)。渲染结束后 H3 常驻显存对用户没有任何价值 ——
		// 桌面/浏览器/其它工具都在等这块显存,而下一集渲染前重新加载只要几十秒。
		c.releaseVRAM(proj.ID, "管线结束")
		// **带上自己那次的登记**：`Stop()` 只 Cancel 不等收尾，而这段收尾要花
		// 0.1–2.5s（最坏 ~27s）—— 用户在窗口里点「开始」的话，这里按 id 无条件删
		// 会把**新那一次**的登记删掉（见 finish 的注释）。
		c.finish(proj.ID, self)
	}()
	plan, planErr := c.store.LoadPlan(proj.ID)
	hasPlan := planErr == nil && plan != nil && len(plan.Shots) > 0
	stages := pipelineStages(phase, hasPlan)
	// 上游把"写给生成器的话"混进 h3_prompt 时**如实报一次**（见 project/h3_clean.go）：
	// 读取边界已经把这些行剔掉，但用户有权知道"这次渲染用的提示词与盘上文件不完全一样"
	// —— 静默改数据是这类兜底最容易犯的错。
	if plan != nil && plan.StrippedDirectives > 0 {
		c.emit(proj.ID, "pipeline", 0, "warn", 0,
			"⚠ 分镜提示词里混进了 %d 行生成器指令（REFERENCE ORDER / PICTURE MANIFEST 之类），"+
				"已在渲染时剔除（这些是写给拼提示词的人的规则，不是给模型的内容）；要根治请让脚本侧重新导出",
			plan.StrippedDirectives)
	}
	if phase == PhaseAll && hasPlan {
		c.emit(proj.ID, "pipeline", 0, "info", 0,
			"⏩ 检测到已有分镜方案(%d 镜),跳过重新生成直接续跑(重新创作请到剧本页)", len(plan.Shots))
	}
	started := time.Now()
	c.emit(proj.ID, "pipeline", 0, "info", 0, "▶ 管线启动:%s", strings.Join(stageLabels(stages), " → "))
	var err error
	// qcFailed 全流程里质检不通过时的**留痕**:合成照旧继续(用户定调"带瑕疵继续合成,
	// 按报告单镜重渲"),但这次运行不许再以"完成"收尾 —— 坏镜已经拼进成片,批产线若
	// 把它当成完成就会自动跳下一集,而用户只看到过一条 warn(审计:质检在最主要的
	// 入口「一键全流程」上拦不住任何坏镜,结尾却报「全流程完成 + 成片就绪」)。
	var qcFailed error
	for _, st := range stages {
		if ctx.Err() != nil {
			ended = ResultStopped
			c.emit(proj.ID, "pipeline", 0, "warn", 0, "⏹ 已停止")
			return
		}
		c.setRunPhase(proj.ID, st)
		switch st {
		case PhaseScript:
			err = c.runScript(ctx, proj)
		case PhaseAssets:
			err = c.runAssets(ctx, proj, onlyAssets)
		case PhaseRender:
			err = c.runRender(ctx, proj, only, engineOverride, seedOverride)
		case PhaseQC:
			err = c.runQC(ctx, proj)
			// 全流程中质检不通过不中止:带瑕疵继续合成,用户按报告单镜重渲。
			// 但结论必须留痕(qcFailed),见上面的说明 —— 不许把 error 清成 nil 之后
			// 又在结尾打「✔ 全流程完成」。
			if err != nil && phase == PhaseAll && ctx.Err() == nil {
				c.emit(proj.ID, PhaseQC, 0, "warn", 100, "⚠ %v(全流程继续,请稍后按报告单镜重渲)", err)
				qcFailed = err
				err = nil
			}
		case PhaseAssemble:
			err = c.runAssemble(ctx, proj)
		}
		if err != nil {
			if ctx.Err() != nil {
				ended = ResultStopped
				c.emit(proj.ID, "pipeline", 0, "warn", 0, "⏹ 已停止")
			} else {
				ended = ResultFailed
				c.emit(proj.ID, st, 0, "error", 0, "✕ 阶段失败:%v", err)
				c.emit(proj.ID, "pipeline", 0, "error", 0, "✕ 管线中止于「%s」", stageLabel(st))
			}
			return
		}
	}
	if phase == PhaseAll {
		// **交付判据只看成片在不在**（2026-09-28 全面审计补上的一道）：各阶段都没返回 error，
		// 不等于盘上有片子 —— 修之前这里会直接报「✔ 全流程完成」，批量产线据此推进下一集，
		// 用户最后看到的是一串"完成"、output/ 里却没有成片（用户原话：「交付的逻辑搞的跟个啥一样」）。
		_, delivered := project.DeliveredFinal(c.store.Dir(proj.ID))
		switch {
		case qcFailed != nil:
			// 带瑕疵收尾:成片产出了,但这次运行**不算完成**(批产线据此停住不自动跳集,
			// 状态与结尾文案也如实说明成片里含不合格镜头)。
			ended = ResultFailed
			c.emit(proj.ID, "pipeline", 0, "warn", 100,
				"⚠ 全流程跑完但**质检未通过**:%v —— 成片已按现有镜头合成(带瑕疵),请按报告单镜重渲后重新合成;本次不计为完成",
				qcFailed)
		case !delivered:
			ended = ResultFailed
			c.emit(proj.ID, "pipeline", 0, "error", 100,
				"✕ 各阶段都跑完了,但项目 output/ 里**没有成片**（final.mp4 / final_2k.mp4 都不在）—— "+
					"本次**不计为完成**,批量产线不会推进下一集;请到合成阶段看报告并按提示重跑")
		default:
			c.emit(proj.ID, "pipeline", 0, "success", 100, "✔ 全流程完成,耗时 %s", time.Since(started).Round(time.Second))
		}
	}
}

func stageLabels(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, stageLabel(id))
	}
	return out
}

func stageLabel(id string) string {
	for _, p := range PhaseNames {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}

// ---------- 阶段一:script ----------

// releaseVRAM 释放本地引擎占用的显存并把结果如实播报(引擎未运行/云端引擎时静默)。
//
// 「专职某一项,避免资源抢占」的落地点:重活做完就把显存交还,别攥着不放。
//
// **必须过同一道准入闸**(审计):管线收尾那一刻,同一张卡上完全可能还有别的东西在跑
// —— 用户在 ComfyUI WEB 里排的队、或经独立 HTTP 端口发起的一次性操作(局部重绘/
// 音频驱动/重生成定妆照,它们不受管线互斥约束)。/free 会把**正在执行**的任务权重
// 卸掉:接下来每步重新流式加载(实测 150s/步,整镜 45 分钟超时)。旧实现只在
// ReleaseVRAMIfIdle 里判队列,管线收尾这条路径**无条件**卸权重,与 idle 看门狗、
// ReleaseVRAMIfIdle 三处口径互相矛盾。
func (c *Coordinator) releaseVRAM(projID, why string) {
	eng, err := c.reg.Get("comfy")
	if err != nil {
		return
	}
	if ce, ok := eng.(*comfyengine.Engine); ok {
		actx, acancel := context.WithTimeout(context.Background(), 5*time.Second)
		st, aerr := ce.Admin(actx)
		acancel()
		if !vramReleaseAllowed(st, aerr) {
			// 探测不到队列状态时也不释放(宁可多占一会儿显存,不可毁掉正在跑的任务)。
			if st != nil && st.QueueKnown && st.Running+st.Pending > 0 {
				c.emit(projID, "system", 0, "info", 0,
					"🧽 %s:引擎上还有任务(运行 %d / 排队 %d),暂不释放显存 —— 不打断正在跑的任务",
					why, st.Running, st.Pending)
			}
			return
		}
	}
	before, hasBefore := sysmon.VRAMUsedMB()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := eng.FreeVRAM(ctx); err != nil {
		return // 引擎没起来/不可达:没什么可释放的,不刷屏
	}
	if !hasBefore {
		return
	}
	// 卸载 + empty_cache 是异步落下去的,轮询取最小值,别立刻读成"没释放"
	after := before
	for i := 0; i < 6; i++ {
		time.Sleep(400 * time.Millisecond)
		if a, ok := sysmon.VRAMUsedMB(); ok && a < after {
			after = a
		}
	}
	freed := (before - after) / 1024
	if freed >= 0.3 {
		c.emit(projID, "system", 0, "info", 0, "🧽 %s:释放显存 %.1f GB(现占用 %.1f GB,交还桌面)", why, freed, after/1024)
	} else {
		c.emit(projID, "system", 0, "info", 0, "🧽 %s:显存已无占用(当前 %.1f GB)", why, after/1024)
	}
}

// ReleaseVRAMIfIdle 供**一次性 GPU 操作**(抽卡/自定义新建/补全多视角/局部重绘/
// 音频驱动/单资产重生成/2K 定稿)收尾调用:没有管线在跑就把显存交还桌面;
// 有管线在跑时不动 —— 那块 GPU 正被管线使用,释放会把它的模型从显存里踢出去。
// 队列护栏在 releaseVRAM 内部(队列非空则跳过,见那里的说明)。
func (c *Coordinator) ReleaseVRAMIfIdle(why string) {
	if c.HasRunning() {
		return
	}
	c.releaseVRAM("", why)
}

// vramReleaseAllowed 是释放显存的**准入判据**,抽成纯函数以便穷举验证。
//
// 2026-09-24 修(审计 P0-5):旧判据是 `aerr == nil && (Running+Pending) == 0`,
// 而 Admin() 恒返回 err=nil、/queue 查询失败时 Running/Pending 保持 0 ——
// 于是"引擎忙到答不上探测"与"引擎彻底空闲"完全同构,护栏在最该生效的时候失效:
// /free 把正在跑的权重卸掉,接下来每步重新流式加载(实测 150s/步,整镜 45 分钟超时)。
// 现在要求 QueueKnown:探测不到就**不释放**(宁可多占一会儿显存,不可毁掉正在跑的任务)。
func vramReleaseAllowed(st *comfyengine.AdminStatus, adminErr error) bool {
	return adminErr == nil && st != nil && st.QueueKnown && st.Running+st.Pending == 0
}

// phaseNeedsGPU 这个阶段是否要用显卡（ComfyUI）。
//
// 依据是实测：资产（出图/转台/三视图）与渲染（H3 视频）是显卡活，并发会互相拖死；
// 剧本分镜是纯 LLM API 调用、合成是 CPU+ffmpeg —— 它们与显卡阶段**可以并行**。
// 这条判据的唯一用途是"放行不争用的并行"（见 Start 里的并发规则）。
func phaseNeedsGPU(phase string) bool {
	switch phase {
	case PhaseAssets, PhaseRender:
		return true
	case PhaseAll:
		return true // 全流程必然包含显卡阶段
	}
	return false
}
