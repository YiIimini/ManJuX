package pipeline

import (
	"context"
	"fmt"
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
	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
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
	mu    sync.Mutex
	runs  map[string]*Run
	store *project.Store
	cfg   *config.Manager
	reg   *engine.Registry
	ff    *ff.FF
	bus   *Bus
}

// Run 一次运行。
type Run struct {
	Cancel        context.CancelFunc
	Phase         string
	StartedAt     time.Time
	EngineOverride string // 本次运行的引擎覆盖(auto/comfy/api,空=用项目配置)
}

// New 创建协调器。
func New(store *project.Store, cfg *config.Manager, reg *engine.Registry, f *ff.FF, bus *Bus) *Coordinator {
	return &Coordinator{
		runs:  map[string]*Run{},
		store: store, cfg: cfg, reg: reg, ff: f, bus: bus,
	}
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
func (c *Coordinator) Start(projectID, phase string, only []int, engineOverride string) error {
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
	run := &Run{Cancel: cancel, Phase: phase, StartedAt: time.Now(), EngineOverride: engineOverride}
	c.runs[projectID] = run
	c.mu.Unlock()

	go c.execute(ctx, proj, phase, only, engineOverride)
	return nil
}

// Stop 停止项目运行。
func (c *Coordinator) Stop(projectID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.runs[projectID]
	if !ok {
		return false
	}
	r.Cancel()
	return true
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

func (c *Coordinator) execute(ctx context.Context, proj *project.Project, phase string, only []int, engineOverride string) {
	defer c.finish(proj.ID)
	stages := []string{phase}
	if phase == PhaseAll {
		stages = []string{PhaseScript, PhaseAssets, PhaseRender, PhaseQC, PhaseAssemble}
	}
	started := time.Now()
	c.emit(proj.ID, "pipeline", 0, "info", 0, "▶ 管线启动:%s", strings.Join(stageLabels(stages), " → "))
	var err error
	for _, st := range stages {
		if ctx.Err() != nil {
			c.emit(proj.ID, "pipeline", 0, "warn", 0, "⏹ 已停止")
			return
		}
		switch st {
		case PhaseScript:
			err = c.runScript(ctx, proj)
		case PhaseAssets:
			err = c.runAssets(ctx, proj)
		case PhaseRender:
			err = c.runRender(ctx, proj, only, engineOverride)
		case PhaseQC:
			err = c.runQC(ctx, proj)
		case PhaseAssemble:
			err = c.runAssemble(ctx, proj)
		}
		if err != nil {
			if ctx.Err() != nil {
				c.emit(proj.ID, "pipeline", 0, "warn", 0, "⏹ 已停止")
			} else {
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
	plan, err := director.Generate(ctx, client, text, director.Options{
		MaxShots: p.MaxShots, TotalSeconds: p.TotalSeconds, ExtraHint: p.StyleHint,
	})
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

func (c *Coordinator) runAssets(ctx context.Context, proj *project.Project) error {
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
		w, h := 832, 1248 // 角色半身竖幅
		if kind == "scenes" {
			if proj.Config.Params.Render.Width >= proj.Config.Params.Render.Height {
				w, h = 1248, 832
			}
		}
		safe := director.SanitizeForCache(kind + "_" + id)
		out := filepath.Join(c.store.Dir(proj.ID), "assets", kind, safe+".png")
		seed := int64(len(id)*1000 + len(name)*7)
		c.emit(proj.ID, PhaseAssets, 0, "info", 0, "Krea-2 生成:%s「%s」(%d×%d)…", kindLabel(kind), name, w, h)
		path, err := comfyGen.GenerateImage(ctx, imagePrompt, krea2Negative, seed, w, h, out)
		if err != nil {
			return "", err
		}
		return path, nil
	}

	missing := 0
	total := 0
	check := func(kind, id, name, imagePrompt string) {
		total++
		if a, ok := assets[id]; ok && a.File != "" {
			full := filepath.Join(c.store.Dir(proj.ID), a.File)
			if _, err := os.Stat(full); err == nil {
				if fp := project.FingerprintFile(full); fp != a.Fingerprint {
					a.Fingerprint = fp
					assets[id] = a
				}
				return
			}
		}
		// 缺失:krea2 生成 / 提示上传
		if comfyGen != nil && strings.TrimSpace(imagePrompt) != "" {
			if path, err := genAsset(kind, id, name, imagePrompt); err == nil {
				rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
				assets[id] = project.Asset{
					ID: id, Kind: kind, Name: name,
					File: filepath.ToSlash(rel), ImagePrompt: imagePrompt,
					Source: "krea2", Fingerprint: project.FingerprintFile(path),
				}
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ 已生成:%s「%s」", kindLabel(kind), name)
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
		check("characters", "char:"+ch.ID, ch.Name, ch.ImagePrompt)
	}
	for _, sc := range plan.Scenes {
		check("scenes", "scene:"+sc.ID, sc.Name, sc.ImagePrompt)
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
	c.emit(proj.ID, PhaseAssets, 0, "success", pct, "✔ 资产就绪:%d/%d", total-missing, total)
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

func (c *Coordinator) runRender(ctx context.Context, proj *project.Project, only []int, engineOverride string) error {
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
	done, skipped := 0, 0
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
		if seed == 0 {
			seed = time.Now().UnixNano()&0x7fffffff + int64(shot.ID)*101
		}
		inputFP := renderFingerprint(shot.H3Prompt, refs, rp, mode, seed)
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

		res, err := eng.Render(ctx, engine.Job{
			ProjectID: proj.ID, ShotID: shot.ID,
			Prompt: shot.H3Prompt, Mode: mode, RefImages: refs,
			Width: rp.Width, Height: rp.Height,
			Length: shot.Duration * 24, Seed: seed, Steps: rp.Steps,
			OutputDir: clipsDir, OutputName: outName,
			OnProgress: func(p int, note string) {
				total := base + p/len(shots)
				if total > 100 {
					total = 100
				}
				c.emit(proj.ID, PhaseRender, shot.ID, "info", total, "镜头 %d:%s", shot.ID, note)
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotFailed, engName, "", inputFP, err.Error())
			c.emit(proj.ID, PhaseRender, shot.ID, "error", base, "✕ 镜头 %d 渲染失败:%v", shot.ID, err)
			// 保存清单后返回错误(单镜失败即中止,便于定点重跑)
			c.store.SaveManifest(proj.ID, manifest)
			return fmt.Errorf("镜头 %d 渲染失败: %w", shot.ID, err)
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
	return project.FingerprintText(prompt, mode, fmt.Sprint(rp.Width, "x", rp.Height, "s", rp.Steps, "seed", seed), strings.Join(files, ","))
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
