package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/project"
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
	for id := range c.runs {
		c.mu.Unlock()
		return fmt.Errorf("另一条管线正在运行(《%s》):为避免两条管线抢同一张显卡一起变慢/超时(实测并发会让定妆照生成超时、单镜从 3-5 分钟涨到 20+ 分钟),请先停止它再启动本集", id)
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

	go c.execute(ctx, proj, phase, only, nil, engineOverride, seedOverride)
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
			kind, name, desc, prompt = "scenes", sc.Name, sc.Description, sc.ImagePrompt
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
	// 同剧对比+风格具象化注入:重生成与产线生成同规则;已含的块不重复追加
	// (只对角色做 —— 场景不该被塞角色身份/画风块;此前这里判的是资产类别,别混淆)
	if kind == "characters" && !isChibi {
		if !strings.Contains(prompt, "RENDER STYLE") {
			prompt = normalizeUniformWording(prompt) // 名词短语层面的去警用化
			// 取景按类型重写:器物若沿用 LLM 写的"半身正面",重新生成又会长成一个人
			prof := EntityProfileOf(ek)
			if prof.RewriteSheetFraming {
				prompt = rewriteSheetFraming(prompt, prof.SheetFraming)
			}
			prompt += styleExpand(plan.Style, proj.Config.Params.Script.FacesOn(), ek)
			prompt += uniformCivilianClarify(desc)
		}
		if !strings.Contains(prompt, "CAST DISTINCTNESS") {
			prompt += castContrastBlock(plan, name, ek)
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
	c.emit(projectID, PhaseAssets, 0, "info", 0, "重新生成:%s「%s」(%s)…", kindLabel(kind), name, EntityKindLabel(ek))
	w, h := 832, 1248
	if isChibi {
		w, h = 1024, 1024
	} else if kind == "scenes" && proj.Config.Params.Render.Width >= proj.Config.Params.Render.Height {
		w, h = 1248, 832
	}
	out := filepath.Join(c.store.Dir(projectID), "assets", kind, director.SanitizeForCache(kind+"_"+strings.TrimPrefix(assetID, kind+":"))+".png")
	seed := time.Now().UnixNano() & 0x7fffffff
	path, err := eng.GenerateImage(ctx, prompt, "", seed, w, h, out)
	if err != nil {
		return err
	}
	var rel string
	if r2, e2 := filepath.Rel(c.store.Dir(projectID), path); e2 == nil {
		rel = filepath.ToSlash(r2)
	}
	assets, err := c.store.LoadAssets(projectID)
	if err != nil {
		return err
	}
	assets[assetID] = project.Asset{
		ID: assetID, Kind: kind, Name: name,
		File: rel, ImagePrompt: prompt,
		Source: "krea2", Fingerprint: project.FingerprintFile(path),
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
			if _, err := c.lib.Put(kind, name, desc, prompt, path, projectID, "krea2"); err != nil {
				c.emit(projectID, PhaseAssets, 0, "warn", 0, "⚠ 写回资产库失败(不影响本项目):%v", err)
			}
		}
	}
	c.emit(projectID, PhaseAssets, 0, "success", 0, "✔ 已重新生成:%s「%s」(旧角度图已作废,渲染前会自动补多视角)", kindLabel(kind), name)
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
func (c *Coordinator) Stop(projectID string) bool {
	c.mu.Lock()
	r, ok := c.runs[projectID]
	if !ok {
		c.mu.Unlock()
		return false
	}
	r.Cancel()
	c.mu.Unlock()
	c.interruptComfy() // 服务端同步中断+清队,防重开渲染堵在旧任务后面
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

func (c *Coordinator) finish(projectID string) {
	c.mu.Lock()
	delete(c.runs, projectID)
	c.mu.Unlock()
}

func (c *Coordinator) emit(projectID, phase string, shotID int, level string, progress int, format string, args ...any) {
	c.bus.Publish(Event{
		ProjectID: projectID, Phase: phase, ShotID: shotID,
		Level: level, Progress: progress,
		Message: fmt.Sprintf(format, args...),
	})
}

func (c *Coordinator) execute(ctx context.Context, proj *project.Project, phase string, only []int, onlyAssets []string, engineOverride string, seedOverride int64) {
	// 记录结束原因(completed/stopped/failed)供批量排队判断:中止或失败不得被当作"完成"而自动跳集
	ended := ResultCompleted
	defer func() {
		c.SetResult(proj.ID, ended)
		// 管线收尾:把 GPU 还给桌面(2026-09-12 用户定调「该释放的就进行释放,
		// 专职某一项,避免资源抢占」)。渲染结束后 H3 常驻显存对用户没有任何价值 ——
		// 桌面/浏览器/其它工具都在等这块显存,而下一集渲染前重新加载只要几十秒。
		c.releaseVRAM(proj.ID, "管线结束")
		c.finish(proj.ID)
	}()
	stages := []string{phase}
	if phase == PhaseAll {
		stages = []string{PhaseScript, PhaseAssets, PhaseRender, PhaseQC, PhaseAssemble}
		// 断点续跑:已有有效方案则跳过「剧本分镜」重新生成 ——
		// 重新生成会改变每镜提示词 → 指纹全变 → 已渲镜头全部作废(从头开跑的根因)。
		// 想重新创作方案,请在剧本页点「生成分镜方案」(显式意图)。
		if plan, err := c.store.LoadPlan(proj.ID); err == nil && len(plan.Shots) > 0 {
			stages = stages[1:]
			c.emit(proj.ID, "pipeline", 0, "info", 0,
				"⏩ 检测到已有分镜方案(%d 镜),跳过重新生成直接续跑(重新创作请到剧本页)", len(plan.Shots))
		}
	}
	started := time.Now()
	c.emit(proj.ID, "pipeline", 0, "info", 0, "▶ 管线启动:%s", strings.Join(stageLabels(stages), " → "))
	var err error
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
			// 全流程中质检不通过不中止:带瑕疵继续合成,用户按报告单镜重渲
			if err != nil && phase == PhaseAll && ctx.Err() == nil {
				c.emit(proj.ID, PhaseQC, 0, "warn", 100, "⚠ %v(全流程继续,请稍后按报告单镜重渲)", err)
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
		c.emit(proj.ID, "pipeline", 0, "success", 100, "✔ 全流程完成,耗时 %s", time.Since(started).Round(time.Second))
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
// 只在**没有正在跑的管线**时由一次性操作调用(callers 自己判断);管线内部的调用
// 由本函数放行(projID 就是当前管线自己)。
func (c *Coordinator) releaseVRAM(projID, why string) {
	eng, err := c.reg.Get("comfy")
	if err != nil {
		return
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
// 可靠性护栏:ComfyUI 队列里还有活时**绝不**释放 —— /free 会卸载模型,若那条
// 任务正在执行,它的权重会被踢出显存(接下来每步重新流式加载,等同白跑甚至超时)。
// 队列为空才是真正的空闲,这时释放才安全。
func (c *Coordinator) ReleaseVRAMIfIdle(why string) {
	if c.HasRunning() {
		return
	}
	eng, err := c.reg.Get("comfy")
	if err != nil {
		return
	}
	if ce, ok := eng.(*comfyengine.Engine); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		st, aerr := ce.Admin(ctx)
		cancel()
		if aerr == nil && st != nil && (st.Running+st.Pending) > 0 {
			return // 队列非空:别人(或刚投的任务)正在用,不许抢
		}
	}
	c.releaseVRAM("", why)
}
