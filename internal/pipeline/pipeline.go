package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/llm"
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
func (c *Coordinator) Start(projectID, phase string, only []int, engineOverride string, seedOverride int64) error {
	if phase == "" {
		phase = PhaseAll
	}
	c.mu.Lock()
	if _, dup := c.runs[projectID]; dup {
		c.mu.Unlock()
		return fmt.Errorf("项目正在运行中,请先停止")
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
				kind, name, prompt, desc = a.Kind, a.Name, a.ImagePrompt, ""
			}
		}
	}
	if kind == "" {
		return fmt.Errorf("资产不存在于方案: %s", assetID)
	}
	// Q 版(chibi)方形画幅,其余竖版半身
	isChibi := strings.HasSuffix(assetID, "_chibi")
	eng, err := c.reg.Get("comfy")
	if err != nil {
		return err
	}
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		return fmt.Errorf("本地引擎不可用")
	}
	ctx := context.Background()
	if err := ce.Available(ctx); err != nil {
		return fmt.Errorf("单资产重生成需要本地 ComfyUI: %w", err)
	}
	c.emit(projectID, PhaseAssets, 0, "info", 0, "重新生成:%s「%s」…", kindLabel(kind), name)
	w, h := 832, 1248
	if isChibi {
		w, h = 1024, 1024
	} else if kind == "scenes" && proj.Config.Params.Render.Width >= proj.Config.Params.Render.Height {
		w, h = 1248, 832
	}
	out := filepath.Join(c.store.Dir(projectID), "assets", kind, director.SanitizeForCache(kind+"_"+strings.TrimPrefix(assetID, kind+":"))+".png")
	seed := time.Now().UnixNano() & 0x7fffffff
	path, err := ce.GenerateImage(ctx, prompt, "", seed, w, h, out)
	if err != nil {
		return err
	}
	assets, _ := c.store.LoadAssets(projectID)
	rel, _ := filepath.Rel(c.store.Dir(projectID), path)
	assets[assetID] = project.Asset{
		ID: assetID, Kind: kind, Name: name,
		File: filepath.ToSlash(rel), ImagePrompt: prompt,
		Source: "krea2", Fingerprint: project.FingerprintFile(path),
	}
	if err := c.store.SaveAssets(projectID, assets); err != nil {
		return err
	}
	if c.lib != nil && !isChibi {
		// Q 版是项目内派生素材,不沉淀进跨项目资产库
		_, _ = c.lib.Put(kind, name, desc, prompt, path, projectID, "krea2")
	}
	c.emit(projectID, PhaseAssets, 0, "success", 100, "✔ 已重生成:%s「%s」", kindLabel(kind), name)
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
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ce.Interrupt(ctx)
	ce.ClearQueue(ctx)
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

func (c *Coordinator) runScript(ctx context.Context, proj *project.Project) error {
	c.emit(proj.ID, PhaseScript, 0, "info", 5, "读取源文本…")
	text, err := c.store.LoadSource(proj.ID)
	if err != nil {
		return fmt.Errorf("读取源文本失败: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("源文本为空:请先在「剧本」中输入故事/创意文本")
	}
	g := c.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	p := proj.Config.Params.Script
	c.emit(proj.ID, PhaseScript, 0, "info", 15, "LLM 直出分镜(%s,%d 镜约 %d 秒)…", g.LLM.Model, p.MaxShots, p.TotalSeconds)
	c.emit(proj.ID, PhaseScript, 0, "info", 15, "深度推理模型单次可思考数分钟属正常,期间每 30 秒报一次心跳,请勿提前停止")

	// 心跳:LLM 推理期间持续可见进度,避免「看起来卡死」被误停
	hbCtx, hbStop := context.WithCancel(ctx)
	defer hbStop()
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		start := time.Now()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-tick.C:
				c.emit(proj.ID, PhaseScript, 0, "info", 15, "⏳ LLM 仍在推理…已等待 %d 秒(推理模型深度思考,通常 2-6 分钟)", int(time.Since(start).Seconds()))
			}
		}
	}()

	// 书级角色锚定:系列前几集已确立的角色/场景名注入提示,强制沿用同名
	// ——配合资产库按名复用,杜绝同角色跨集换脸(漂移)。实证:貔貅第01集
	// 「貅满堂」到第02集被 LLM 写成「貅满仓」,一字之差即匹配失效重生成。
	hint := composeStyleHint(p.Styles, p.StyleHint)
	if strings.Contains(text, "【本集原文】") {
		if hint != "" {
			hint += "\n"
		}
		// 原文在场:分镜必须沿原文节拍逐段改编,覆盖全部关键情节——
		// 跳段/断章取义的根治指令(2026-09-10 用户定调)
		hint += "ADAPTATION SOURCE PRIORITY: 本集附有小说原文(【本集原文】)。分镜必须沿原文叙事节拍逐段改编:每个关键情节转折都必须有对应镜头,不得跳段、不得省略因果链、不得凭空压缩时空;原文对白优先原句使用。"
		c.emit(proj.ID, PhaseScript, 0, "info", 10, "📖 本集附小说原文,分镜沿原文节拍逐段改编(不跳段)")
	}
	if roster, nChars := c.bookRoster(proj.ID); roster != "" {
		if hint != "" {
			hint += "\n"
		}
		hint += roster
		c.emit(proj.ID, PhaseScript, 0, "info", 10, "🔁 系列角色锚定:沿用本书已确立角色名 %d 个(跨集不换脸)", nChars)
	}
	plan, err := director.Generate(ctx, client, text, director.Options{
		MaxShots: p.MaxShots, TotalSeconds: p.TotalSeconds,
		ExtraHint: hint, Chibi: p.Chibi,
		OnBatch: func(batch, total, fromShot, toShot int) {
			c.emit(proj.ID, PhaseScript, 0, "info", 15, "▸ 分批分镜 %d/%d:镜 %d-%d 生成中…", batch, total, fromShot, toShot)
		},
	})
	hbStop()
	<-hbDone
	if err != nil {
		return err
	}
	if err := c.store.SavePlan(proj.ID, plan); err != nil {
		return err
	}
	c.emit(proj.ID, PhaseScript, 0, "success", 100, "✔ 方案就绪:《%s》%d 角色 / %d 场景 / %d 镜", plan.Title, len(plan.Characters), len(plan.Scenes), len(plan.Shots))
	return nil
}

// ---------- 阶段二:assets ----------

// krea2Negative Krea-2 蒸馏(CFG 1.0)下负面词无引导作用,留空。
const krea2Negative = ""

func (c *Coordinator) runAssets(ctx context.Context, proj *project.Project, onlyAssets []string) error {
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		return fmt.Errorf("尚未生成分镜方案:请先运行「剧本分镜」阶段")
	}
	assets, err := c.store.LoadAssets(proj.ID)
	if err != nil {
		return err
	}
	p := proj.Config.Params.Assets

	// Krea-2 自动生成缺失参考图(角色竖幅半身 832×1248;场景按项目画幅方向)
	var comfyGen *comfyengine.Engine
	if p.Source == "krea2" {
		eng, err := c.reg.Get("comfy")
		if err != nil {
			return err
		}
		ce, ok := eng.(*comfyengine.Engine)
		if !ok {
			return fmt.Errorf("本地引擎不支持图像生成")
		}
		if err := ce.Available(ctx); err != nil {
			return fmt.Errorf("Krea-2 定妆照需要本地 ComfyUI: %w", err)
		}
		comfyGen = ce
	}

	genAsset := func(kind, id, name, imagePrompt string) (string, error) {
		return c.genAssetImage(ctx, proj, comfyGen, kind, id, name, imagePrompt)
	}

	missing := 0
	total := 0
	reused := 0
	onlySet := map[string]bool{}
	for _, id := range onlyAssets {
		onlySet[id] = true
	}
	check := func(kind, id, name, description, imagePrompt string) {
		if len(onlySet) > 0 && !onlySet[id] {
			return
		}
		total++
		if a, ok := assets[id]; ok && a.File != "" {
			full := filepath.Join(c.store.Dir(proj.ID), a.File)
			if _, err := os.Stat(full); err == nil {
				if fp := project.FingerprintFile(full); fp != a.Fingerprint {
					a.Fingerprint = fp
					assets[id] = a
				}
				// 存量回填:项目已有资产若库中无同名条目,自动沉淀入库
				if c.lib != nil {
					if _, _, ok := c.lib.Match(kind, name); !ok {
						if _, err := c.lib.Put(kind, name, description, imagePrompt, full, proj.ID, a.Source); err == nil {
							c.emit(proj.ID, PhaseAssets, 0, "info", 0, "已入库:%s「%s」(存量回填)", kindLabel(kind), name)
						}
					}
				}
				return
			}
		}
		// ① 资产库复用(名称精确匹配):跨项目同一角色/场景直接复用,不重复生成
		if c.lib != nil {
			if _, img, ok := c.lib.Match(kind, name); ok {
				destDir := filepath.Join(c.store.Dir(proj.ID), "assets", kind)
				_ = os.MkdirAll(destDir, 0o755)
				dest := filepath.Join(destDir, director.SanitizeForCache(kind+"_"+id)+filepath.Ext(img))
				if raw, err := os.ReadFile(img); err == nil {
					_ = os.WriteFile(dest, raw, 0o644)
					rel, _ := filepath.Rel(c.store.Dir(proj.ID), dest)
					assets[id] = project.Asset{
						ID: id, Kind: kind, Name: name,
						File: filepath.ToSlash(rel), ImagePrompt: imagePrompt,
						Source: "library", Fingerprint: project.FingerprintFile(dest),
					}
					c.lib.BumpUsed(kind, name)
					reused++
					c.emit(proj.ID, PhaseAssets, 0, "success", 0, "♻ 资产库复用:%s「%s」(跨项目共享,免生成)", kindLabel(kind), name)
					return
				}
			}
		}
		// ② Krea-2 生成缺失参考图;成功后自动入库供后续项目复用
		if comfyGen != nil && strings.TrimSpace(imagePrompt) != "" {
			if path, err := genAsset(kind, id, name, imagePrompt); err == nil {
				rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
				assets[id] = project.Asset{
					ID: id, Kind: kind, Name: name,
					File: filepath.ToSlash(rel), ImagePrompt: imagePrompt,
					Source: "krea2", Fingerprint: project.FingerprintFile(path),
				}
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ 已生成:%s「%s」", kindLabel(kind), name)
				if c.lib != nil {
					if _, err := c.lib.Put(kind, name, description, imagePrompt, path, proj.ID, "krea2"); err == nil {
						c.emit(proj.ID, PhaseAssets, 0, "info", 0, "已入库:%s「%s」(后续项目自动复用)", kindLabel(kind), name)
					}
				}
				return
			} else {
				c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "生成失败:%s「%s」:%v", kindLabel(kind), name, err)
			}
		}
		missing++
		if p.Source == "upload" {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "缺少参考图:%s「%s」(可上传,缺失镜头将降级纯文生)", kindLabel(kind), name)
		}
	}
	for _, ch := range plan.Characters {
		check("characters", "char:"+ch.ID, ch.Name, ch.Description, ch.ImagePrompt)
	}
	for _, sc := range plan.Scenes {
		check("scenes", "scene:"+sc.ID, sc.Name, sc.Description, sc.ImagePrompt)
	}

	// Q 版形象(chibi):主定妆照就绪的角色同步生成 Q 版,内心活动镜头的表达素材。
	// 独立资产 ID(char:<id>_chibi),渲染 ref 不携带,仅作画面内容/花絮素材。
	if proj.Config.Params.Script.Chibi && comfyGen != nil {
		for _, ch := range plan.Characters {
			chibiID := "char:" + ch.ID + "_chibi"
			if a, ok := assets[chibiID]; ok && a.File != "" {
				if _, err := os.Stat(filepath.Join(c.store.Dir(proj.ID), a.File)); err == nil {
					continue
				}
			}
			// 复用同 seed 机制,保证同角色 Q 版稳定
			safe := director.SanitizeForCache("characters_char_" + ch.ID + "_chibi")
			out := filepath.Join(c.store.Dir(proj.ID), "assets", "characters", safe+".png")
			chibiPrompt := "cute chibi SD figure of the same character, big head, tiny body, full body, simple pastel background, " +
				"expressive face, figure product photography, " + ch.ImagePrompt
			seed := int64(len(ch.ID)*1000 + len(ch.Name)*7 + 13)
			c.emit(proj.ID, PhaseAssets, 0, "info", 0, "Krea-2 生成:「%s」Q 版形象…", ch.Name)
			if path, err := comfyGen.GenerateImage(ctx, chibiPrompt, krea2Negative, seed, 1024, 1024, out); err == nil {
				rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
				assets[chibiID] = project.Asset{
					ID: chibiID, Kind: "characters", Name: ch.Name + " · Q版",
					File: filepath.ToSlash(rel), ImagePrompt: chibiPrompt,
					Source: "krea2-chibi", Fingerprint: project.FingerprintFile(path),
				}
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ Q 版就绪:「%s」(内心活动素材)", ch.Name)
			} else {
				c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "Q 版生成失败(不影响主流程):%v", err)
			}
		}
	}

	if err := c.store.SaveAssets(proj.ID, assets); err != nil {
		return err
	}
	pct := 100
	if total > 0 {
		pct = (total - missing) * 100 / total
	}
	if p.Source == "skip" {
		c.emit(proj.ID, PhaseAssets, 0, "success", 100, "✔ 资产策略=跳过(全部镜头纯文生 t2va)")
		return nil
	}
	c.emit(proj.ID, PhaseAssets, 0, "success", pct, "✔ 资产就绪:%d/%d(库复用 %d)", total-missing, total, reused)
	if missing > 0 {
		c.emit(proj.ID, PhaseAssets, 0, "warn", pct, "提示:缺 %d 张参考图,对应镜头将以 t2va 纯文生渲染", missing)
	}
	return nil
}

func kindLabel(kind string) string {
	switch kind {
	case "characters":
		return "角色"
	case "scenes":
		return "场景"
	case "props":
		return "道具"
	}
	return kind
}

// ---------- 阶段三:render ----------

func (c *Coordinator) runRender(ctx context.Context, proj *project.Project, only []int, engineOverride string, seedOverride int64) error {
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		return fmt.Errorf("尚未生成分镜方案:请先运行「剧本分镜」阶段")
	}
	assets, err := c.store.LoadAssets(proj.ID)
	if err != nil {
		return err
	}
	manifest, err := c.store.LoadManifest(proj.ID)
	if err != nil {
		return err
	}
	g := c.cfg.Get()
	rp := proj.Config.Params.Render
	if rp.Turbo {
		// turbo LoRA 生效时步数钳到 4-8
		if rp.Steps > 8 {
			rp.Steps = 8
		}
	}

	// 引擎解析:本次覆盖 > 项目配置(引擎=api 时云端 API Key 必须就绪)
	engName := rp.Engine
	if engineOverride == "auto" || engineOverride == "comfy" || engineOverride == "api" {
		engName = engineOverride
	}
	if engName == "auto" {
		comfyEng, _ := c.reg.Get("comfy")
		if comfyEng != nil && comfyEng.Available(ctx) == nil {
			engName = "comfy"
		} else {
			apiEng, _ := c.reg.Get("api")
			if apiEng != nil && apiEng.Available(ctx) == nil {
				engName = "api"
			}
		}
	}
	if engName == "" {
		return fmt.Errorf("无可 用引擎:本地 ComfyUI 与云端 API 均不可用,请检查设置")
	}
	eng, err := c.reg.Get(engName)
	if err != nil {
		return err
	}
	c.emit(proj.ID, PhaseRender, 0, "info", 0, "引擎=%s(%s)", engName, engineLabel(engName))
	if rp.Seam && engName == "comfy" {
		c.emit(proj.ID, PhaseRender, 0, "info", 0, "镜间连续接缝已开启:每镜尾部 22 帧钉入下一镜(仅本地引擎;重渲某镜后建议后续镜头一并重渲保持链一致)")
	}

	shots := plan.Shots
	if len(only) > 0 {
		want := map[int]bool{}
		for _, id := range only {
			want[id] = true
		}
		var filtered []project.Shot
		for _, s := range shots {
			if want[s.ID] {
				filtered = append(filtered, s)
			}
		}
		shots = filtered
	}
	if len(shots) == 0 {
		return fmt.Errorf("没有可渲染的镜头")
	}

	clipsDir := filepath.Join(c.store.Dir(proj.ID), "clips")
	_ = os.MkdirAll(clipsDir, 0o755)
	// 渲染前补齐缺失参考图(不问资产来源模式,除 skip;缺图先生成再渲染)
	c.backfillAssets(ctx, proj, plan, assets)
	// 阶段起点清一次遗留队列(仅本地引擎;绝不在逐镜循环内清,防误杀排队任务)
	// 并释放残留显存:上一阶段(Krea-2 定妆/抽卡等)模型常驻会把 H3 主模型
	// (~20GB)挤出显存触发 dynamic VRAM loading——每步在 CPU↔GPU 搬权重,
	// 实测 150s/步(正常 ~2s),极端时整镜 45 分钟超时(2026-09-10 血案)。
	if ce, ok := eng.(*comfyengine.Engine); ok && engName == "comfy" {
		ce.ClearQueue(ctx)
		b, hasB := sysmon.VRAMUsedMB()
		ce.FreeVRAM(ctx)
		if hasB {
			if a, hasA := sysmon.VRAMUsedMB(); hasA {
				c.emit(proj.ID, PhaseRender, 0, "info", 0, "🧽 渲染前释放显存:%.1f GB → %.1f GB(腾出主模型常驻空间)", b/1024, a/1024)
			}
		}
	}
	done, skipped, failed := 0, 0, 0
	var failures []string
	for i, shot := range shots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		base := (i * 100) / len(shots)

		// 参考图组装(ref2va):角色定妆照 + 场景图
		var refs []engine.RefImage
		mode := shot.Mode
		if proj.Config.Params.Assets.Source != "skip" && mode == "ref2va" {
			for _, cid := range shot.Characters {
				if a, ok := assets["char:"+cid]; ok && a.File != "" {
					refs = append(refs, engine.RefImage{Kind: "character", Path: filepath.Join(c.store.Dir(proj.ID), a.File)})
				}
			}
			if a, ok := assets["scene:"+shot.Scene]; ok && a.File != "" {
				refs = append(refs, engine.RefImage{Kind: "scene", Path: filepath.Join(c.store.Dir(proj.ID), a.File)})
			}
			if len(refs) == 0 {
				mode = "t2va" // 资产缺失降级纯文生
				c.emit(proj.ID, PhaseRender, shot.ID, "warn", base, "镜头 %d 无参考图,降级 t2va", shot.ID)
			}
		} else if mode == "ref2va" && proj.Config.Params.Assets.Source == "skip" {
			mode = "t2va"
		}

		seed := rp.Seed
		if seedOverride > 0 {
			seed = seedOverride + int64(shot.ID)*101 // 单镜微调种子:同值同镜稳定复现
		} else if seed == 0 {
			// 角色锚定种子:默认种子曾用时间随机,同角色同定妆照跨集仍换脸
			// (2026-09-09 用户实证「貅兜兜飘逸」)。改为按镜头角色组合派生,
			// 同角色组合跨集得到同基种子,配合同一张库定妆照,一致性最强;
			// shot.ID 项保住集内镜头间的多样性。
			seed = nameSeed("shot", strings.Join(shot.Characters, "+")) + int64(shot.ID)*101
		}
		// 角色一致性锚定:出场角色的固定外貌描述强制前置,消除 LLM 逐镜漂移
		prompt := anchorPrompt(plan, &shot)
		// 指纹记录「配置 seed」而非运行时随机值 —— 随机模式(=0)下每次时间戳不同,
		// 指纹必然失配,断点续跑的已渲镜头全部误判失效(从头重跑的第二根因)。
		// 渲染仍用上面的随机 seed 保多样;产物有效性以 提示词/参考图/分辨率/步数/档位 为准。
		inputFP := renderFingerprint(prompt, refs, rp, mode, rp.Seed)
		outName := fmt.Sprintf("shot_%03d", shot.ID)

		// 断点续跑:同输入指纹且文件存在 → 跳过
		if m, ok := manifest[strconv.Itoa(shot.ID)]; ok && m.Status == project.ShotDone && m.InputFP == inputFP {
			full := filepath.Join(c.store.Dir(proj.ID), m.File)
			if st, err := os.Stat(full); err == nil && st.Size() > 0 {
				skipped++
				c.emit(proj.ID, PhaseRender, shot.ID, "info", base+100/len(shots), "镜头 %d 产物有效,跳过(断点续跑)", shot.ID)
				continue
			}
		}

		c.emit(proj.ID, PhaseRender, shot.ID, "info", base, "镜头 %d/%d 渲染开始(%s,%ds,seed=%d)", shot.ID, len(shots), mode, shot.Duration, seed)
		c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotRendering, engName, "", inputFP, "")

		var res *engine.Result
		var err error
		for attempt := 1; attempt <= 2; attempt++ {
			res, err = eng.Render(ctx, engine.Job{
				ProjectID: proj.ID, ShotID: shot.ID,
				Prompt: prompt, Mode: mode, RefImages: refs,
				Width: rp.Width, Height: rp.Height,
				Length: shot.Duration * 24, Seed: seed, Steps: rp.Steps,
				Accel:     rp.Accel,
				OutputDir: clipsDir, OutputName: outName,
				Seam:     rp.Seam && engName == "comfy",
				PrevShot: shot.ID - 1,
				OnProgress: func(p int, note string) {
					total := base + p/len(shots)
					if total > 100 {
						total = 100
					}
					c.emit(proj.ID, PhaseRender, shot.ID, "info", total, "镜头 %d:%s", shot.ID, note)
				},
			})
			if err == nil || ctx.Err() != nil {
				break
			}
			// 超时类失败多为显存挤压导致的动态换页极慢(150s/步)甚至假死:
			// 先释放显存再原地重试一次,多数可恢复正常速度(2026-09-10)。
			if !strings.Contains(err.Error(), "超时") || attempt == 2 {
				break
			}
			c.emit(proj.ID, PhaseRender, shot.ID, "warn", base, "⚠ 镜头 %d 超时,释放显存后重试一次:%v", shot.ID, err)
			if ce, ok := eng.(*comfyengine.Engine); ok && engName == "comfy" {
				ce.FreeVRAM(ctx)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// 单镜失败不中止:记录后继续后续镜头,阶段末汇总
			failed++
			failures = append(failures, fmt.Sprintf("镜%d:%v", shot.ID, err))
			c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotFailed, engName, "", inputFP, err.Error())
			c.emit(proj.ID, PhaseRender, shot.ID, "error", base, "✕ 镜头 %d 渲染失败(继续下一镜):%v", shot.ID, err)
			continue
		}
		rel, _ := filepath.Rel(c.store.Dir(proj.ID), res.VideoPath)
		c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotDone, engName, rel, inputFP, "")
		if res.TaskID != "" {
			manifest[strconv.Itoa(shot.ID)] = mergeTask(manifest[strconv.Itoa(shot.ID)], res.TaskID)
		}
		done++
		c.emit(proj.ID, PhaseRender, shot.ID, "success", base+100/len(shots), "✔ 镜头 %d 完成(%s)", shot.ID, rel)
	}
	if err := c.store.SaveManifest(proj.ID, manifest); err != nil {
		return err
	}
	_ = g
	if failed > 0 {
		// 有失败:报告但不抛错(qc/assemble 自行处理缺失);全失败才视为阶段失败
		if done+skipped == 0 {
			return fmt.Errorf("全部 %d 镜渲染失败:%s", failed, strings.Join(failures, ";"))
		}
		c.emit(proj.ID, PhaseRender, 0, "warn", 100, "⚠ 渲染结束:成功 %d,跳过 %d,失败 %d(%s)", done, skipped, failed, strings.Join(failures, ";"))
		return nil
	}
	c.emit(proj.ID, PhaseRender, 0, "success", 100, "✔ 渲染完成:新出 %d 镜,跳过 %d 镜", done, skipped)
	return nil
}

func mergeTask(m project.ClipManifest, taskID string) project.ClipManifest {
	m.TaskID = taskID
	return m
}

func engineLabel(name string) string {
	switch name {
	case "comfy":
		return "本地 ComfyUI · H3-Base"
	case "api":
		return "MiniMax 云端 API"
	}
	return name
}

func renderFingerprint(prompt string, refs []engine.RefImage, rp project.RenderParams, mode string, seed int64) string {
	var files []string
	for _, r := range refs {
		files = append(files, r.Path+":"+project.FingerprintFile(r.Path))
	}
	// 知识库工程教训:指纹必须覆盖采样质量配置(VAE/LoRA 档位),否则换档后旧缓存静默命中。
	// 兼容:默认档(turbo/空)不附加标记 —— 与旧版指纹串完全一致,存量项目续跑不触发全量重渲;
	// 显式 quality/express 档才追加(换档=有意重渲)。
	accelTag := ""
	if rp.Accel == "quality" || rp.Accel == "express" {
		accelTag = "accel" + rp.Accel
	}
	// qualityTag:采样环境版本(v1=2026-09-10 撤下 fp16_accumulation/ck-attention
	// 后的画质基线;画质敏感项再变更时递增,旧镜指纹失配自动重渲)。
	return project.FingerprintText(prompt, mode, fmt.Sprint(rp.Width, "x", rp.Height, "s", rp.Steps, "seed", seed, "seam", rp.Seam, accelTag, "q1"), strings.Join(files, ","))
}

func (c *Coordinator) setShotStatus(projectID string, manifest map[string]project.ClipManifest, shotID int, st project.ShotStatus, eng, file, fp, errMsg string) {
	key := strconv.Itoa(shotID)
	m := manifest[key]
	m.ShotID = shotID
	m.Status = st
	m.Engine = eng
	m.File = file
	m.InputFP = fp
	m.Error = errMsg
	m.UpdatedAt = time.Now()
	manifest[key] = m
	c.store.SaveManifest(projectID, manifest) // 每次落盘,崩溃可恢复
}

// anchorPrompt 角色一致性锚定:把出场角色/场景的固定外貌描述和全局风格
// 强制前置到镜头提示词。H3 是 VLM 输入,中文锚定段与六段式正文并存无碍;
// 跨镜同角色描述逐字一致,配合定妆照 ref 双通道压住 LLM 逐镜漂移。
func anchorPrompt(plan *project.Plan, shot *project.Shot) string {
	var anchors []string
	for _, cid := range shot.Characters {
		for _, ch := range plan.Characters {
			if ch.ID == cid && strings.TrimSpace(ch.Description) != "" {
				anchors = append(anchors, fmt.Sprintf("%s(全程同一个人,外貌严格一致):%s", ch.Name, strings.TrimSpace(ch.Description)))
			}
		}
	}
	for _, sc := range plan.Scenes {
		if sc.ID == shot.Scene && strings.TrimSpace(sc.Description) != "" {
			anchors = append(anchors, fmt.Sprintf("场景「%s」(同一地点):%s", sc.Name, strings.TrimSpace(sc.Description)))
		}
	}
	if len(anchors) == 0 {
		return shot.H3Prompt
	}
	head := "【角色/场景一致性锚定(全片统一,以下外貌描述优先级最高,任何画面细节与之冲突时以此为准)】\n" +
		strings.Join(anchors, "\n")
	if strings.TrimSpace(plan.Style) != "" {
		head += "\n【全片视觉风格】" + strings.TrimSpace(plan.Style)
	}
	return head + "\n\n" + shot.H3Prompt
}

// genAssetImage Krea-2 生成单张资产图(角色竖幅半身/场景按画幅方向)。
// bookRoster 收集系列兄弟集(同父目录)已确立的角色名+形象设定与场景名,
// 返回注入分镜提示的锚定文案与角色数。跨集角色一致性的根因修复:同集内
// 镜头间角色稳定(定妆照锚定有效),但每集分镜由 LLM 独立撰写,同一角色的
// 形象描述措辞漂移(穿着/毛色/年龄细节不一致),文字把画面拉离定妆照
// (2026-09-09 用户实证「貅兜兜跨集飘逸」)。单集项目返回空。
func (c *Coordinator) bookRoster(projectID string) (string, int) {
	epDir := c.store.Dir(projectID)
	series := filepath.Dir(epDir)
	if filepath.Base(series) == "projects" {
		return "", 0
	}
	type charInfo struct{ name, desc string }
	chars := []charInfo{}
	scenes := []string{}
	seenC, seenS := map[string]bool{}, map[string]bool{}
	eps, err := os.ReadDir(series)
	if err != nil {
		return "", 0
	}
	for _, e := range eps {
		if !e.IsDir() || filepath.Join(series, e.Name()) == epDir {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(series, e.Name(), "plan", "plan.json"))
		if err != nil {
			continue
		}
		var plan struct {
			Characters []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"characters"`
			Scenes []struct {
				Name string `json:"name"`
			} `json:"scenes"`
		}
		if json.Unmarshal(raw, &plan) != nil {
			continue
		}
		for _, ch := range plan.Characters {
			n := strings.TrimSpace(ch.Name)
			if n == "" || seenC[n] {
				continue
			}
			seenC[n] = true
			if len(chars) < 8 {
				d := strings.TrimSpace(ch.Description)
				if r := []rune(d); len(r) > 110 {
					d = string(r[:110]) + "…"
				}
				chars = append(chars, charInfo{n, d})
			}
		}
		for _, sc := range plan.Scenes {
			n := strings.TrimSpace(sc.Name)
			if n != "" && !seenS[n] {
				seenS[n] = true
				if len(scenes) < 10 {
					scenes = append(scenes, n)
				}
			}
		}
	}
	if len(chars) == 0 && len(scenes) == 0 {
		return "", 0
	}
	var sb strings.Builder
	sb.WriteString("【系列角色一致性·必须遵守】以下角色已在前几集登场并定妆,本集若出现这些角色:①必须严格沿用原名(不得增减字/换同音字/另起别名);②角色表描述与每镜 H3 提示词中的形象必须与下面给出的设定逐项一致(毛色/服饰/年龄/特征细节不得改动):\n")
	for i, ch := range chars {
		fmt.Fprintf(&sb, "%d. %s:%s\n", i+1, ch.name, ch.desc)
	}
	if len(scenes) > 0 {
		sb.WriteString("已确立的场景名(再次出现时沿用原名):")
		for _, n := range scenes {
			sb.WriteString("「" + n + "」")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("本集新登场的人物/场景属正常剧情推进,可另起新名与新设定,但不得与上述既有角色混淆或顶替。")
	return sb.String(), len(chars)
}

func (c *Coordinator) genAssetImage(ctx context.Context, proj *project.Project, ce *comfyengine.Engine, kind, id, name, imagePrompt string) (string, error) {
	w, h := 832, 1248 // 角色半身竖幅
	if kind == "scenes" {
		if proj.Config.Params.Render.Width >= proj.Config.Params.Render.Height {
			w, h = 1248, 832
		}
	}
	safe := director.SanitizeForCache(kind + "_" + id)
	out := filepath.Join(c.store.Dir(proj.ID), "assets", kind, safe+".png")
	// 名字锚定种子(fnv 全串哈希;曾用长度哈希易碰撞):同名资产重跑/跨集
	// 补齐得到同一张图。换图走「重新生成」(随机 seed)或上传替换。
	seed := nameSeed(kind, name)
	c.emit(proj.ID, PhaseAssets, 0, "info", 0, "Krea-2 生成:%s「%s」(%d×%d)…", kindLabel(kind), name, w, h)
	return ce.GenerateImage(ctx, imagePrompt, krea2Negative, seed, w, h, out)
}

// nameSeed 资产名→稳定种子:同名必同图,是跨集一致性的第二道保险
// (第一道是资产库按名复用)。
func nameSeed(kind, name string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(kind + "\x00" + name))
	return int64(h.Sum32())
}

// backfillAssets 渲染前自动补齐缺失的参考图:资产阶段未跑/未生成全时,
// 就地用本地 Krea-2 生成,绝不静默降级纯文生(2026-09-08 逻辑重构:
// 「定妆照没生成就渲染」被用户点名的反直觉行为根治)。
// 资产生成只依赖本地 ComfyUI,与本次渲染引擎选择无关(云端渲染也本地补图)。
func (c *Coordinator) backfillAssets(ctx context.Context, proj *project.Project, plan *project.Plan, assets map[string]project.Asset) bool {
	if proj.Config.Params.Assets.Source == "skip" {
		return true
	}
	type miss struct{ kind, id, name, desc, prompt string }
	var list []miss
	check := func(kind, id, name, desc, prompt string) {
		if strings.TrimSpace(prompt) == "" {
			return
		}
		if a, ok := assets[id]; ok && a.File != "" {
			if _, err := os.Stat(filepath.Join(c.store.Dir(proj.ID), a.File)); err == nil {
				return
			}
		}
		list = append(list, miss{kind, id, name, desc, prompt})
	}
	for _, ch := range plan.Characters {
		check("characters", "char:"+ch.ID, ch.Name, ch.Description, ch.ImagePrompt)
	}
	for _, sc := range plan.Scenes {
		check("scenes", "scene:"+sc.ID, sc.Name, sc.Description, sc.ImagePrompt)
	}
	if len(list) == 0 {
		return true
	}
	eng, err := c.reg.Get("comfy")
	if err != nil {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ 缺 %d 张参考图且本地引擎不可用,对应镜头将降级纯文生(建议先启动 ComfyUI)", len(list))
		return false
	}
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		return false
	}
	if err := ce.Available(ctx); err != nil {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ 缺 %d 张参考图且本地 ComfyUI 未就绪(%v),对应镜头将降级纯文生", len(list), err)
		return false
	}
	c.emit(proj.ID, PhaseRender, 0, "info", 0, "检测到 %d 张参考图缺失,渲染前自动补齐(Krea-2)…", len(list))
	for _, m := range list {
		path, err := c.genAssetImage(ctx, proj, ce, m.kind, m.id, m.name, m.prompt)
		if err != nil {
			c.emit(proj.ID, PhaseRender, 0, "warn", 0, "补齐失败:%s「%s」:%v(该镜降级 t2va)", kindLabel(m.kind), m.name, err)
			continue
		}
		rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
		assets[m.id] = project.Asset{
			ID: m.id, Kind: m.kind, Name: m.name,
			File: filepath.ToSlash(rel), ImagePrompt: m.prompt,
			Source: "krea2", Fingerprint: project.FingerprintFile(path),
		}
		c.emit(proj.ID, PhaseRender, 0, "success", 0, "✔ 已补齐:%s「%s」", kindLabel(m.kind), m.name)
		if c.lib != nil {
			_, _ = c.lib.Put(m.kind, m.name, m.desc, m.prompt, path, proj.ID, "krea2")
		}
	}
	_ = c.store.SaveAssets(proj.ID, assets)
	return true
}

// StylePresets 渲染风格预设(可多选组合):key → (显示名, 英文风格提示)。
// 提示词进 LLM 全局风格段,决定全片视觉基调。
var StylePresets = map[string]struct{ Name, Prompt string }{
	"urban_xianxia": {"都市仙侠霓虹", "CHINESE URBAN-XIANXIA, NEON-NOIR WET-STREET LIGHTING, CINEMATIC ANIMATION, GOD RAYS THROUGH SKYSCRAPERS"},
	"guoman_2d":     {"国漫二维动画", "CHINESE 2D ANIME STYLE, CLEAN LINEWORK, VIBRANT CEL SHADING, DONGHUA QUALITY"},
	"live_action":   {"真人实拍质感", "PHOTOREALISTIC LIVE-ACTION CINEMATOGRAPHY, 35MM FILM, SHALLOW DEPTH OF FIELD, NATURAL SKIN TEXTURE"},
	"pixar_3d":      {"3D 皮克斯", "PIXAR-STYLE 3D RENDER, SOFT GLOBAL ILLUMINATION, EXPRESSIVE STYLIZED CHARACTERS, SUBSURFACE SCATTERING"},
	"ink_wash":      {"水墨国风", "CHINESE INK WASH PAINTING STYLE, BRUSH TEXTURE, NEGATIVE SPACE, MISTY MOUNTAINS, TRADITIONAL AESTHETIC"},
	"cyberpunk":     {"赛博朋克", "CYBERPUNK, RAIN-SOAKED STREETS, HOLOGRAPHIC ADS, CHROME AND NEON, BLADE RUNNER MOOD"},
	"ghibli":        {"吉卜力手绘", "GHIBLI-INSPIRED HAND-PAINTED, WARM WATERCOLOR BACKGROUNDS, SOFT WIND AND LIGHT, WHIMSICAL SERENITY"},
	"film_noir":     {"黑色电影", "FILM NOIR, HIGH CONTRAST BLACK AND WHITE WITH ONE ACCENT COLOR, VENETIAN BLIND SHADOWS, SMOKEY"},
	"hongkong":      {"港片胶片", "HONG KONG CINEMA 1990s, SATURATED NEON, TEAL-ORANGE GRADE, ANAMORPHIC FLARES"},
	"claymation":    {"黏土定格", "CLAYMATION STOP-MOTION STYLE, FINGERPRINT TEXTURE, TACTILE HANDMADE CHARM"},
}

// composeStyleHint 风格预设(可组合)拼接 + 用户自由文本。
func composeStyleHint(styles []string, freeHint string) string {
	var parts []string
	for _, k := range styles {
		if p, ok := StylePresets[k]; ok {
			parts = append(parts, p.Prompt)
		}
	}
	out := strings.Join(parts, " + ")
	if strings.TrimSpace(freeHint) != "" {
		if out != "" {
			out += "; "
		}
		out += strings.TrimSpace(freeHint)
	}
	return out
}
