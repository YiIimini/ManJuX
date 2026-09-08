package comfyengine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// Engine 本地 ComfyUI 渲染引擎(H3-Base 768p,音视频一体直出)。
type Engine struct {
	cfg  func() config.ComfySettings // 设置快照 provider(热更新)
	cli  *Client
	proc *os.Process // 代管启动的 ComfyUI 进程
}

// New 创建引擎;cfg 返回最新设置快照。
func New(cfg func() config.ComfySettings) *Engine {
	return &Engine{cfg: cfg, cli: NewClient(cfg().BaseURL)}
}

// Name 引擎名。
func (e *Engine) Name() string { return "comfy" }

// Cfg 返回 ComfyUI 设置快照(WEB 界面地址等对外暴露用)。
func (e *Engine) Cfg() config.ComfySettings { return e.cfg() }

// Available 探测:不可达时按设置尝试代管启动。
func (e *Engine) Available(ctx context.Context) error {
	e.cli = NewClient(e.cfg().BaseURL)
	if e.cli.Alive(ctx) {
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
	port := "8190"
	if p, err := parsePort(c.BaseURL); err == nil {
		port = p
	}
	logPath := filepath.Join(sidecarRoot(c), "logs", "comfyui.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logF = nil
	}
	cmd := sysexec.Hide(exec.Command(py, main, "--port", port, "--listen", "127.0.0.1"))
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
	e.proc = cmd.Process
	writePidFile(c, cmd.Process.Pid) // 代管状态持久化:重启后可接管
	touchBusy()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(4 * time.Second):
		}
		if e.cli.Alive(ctx) {
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
	e.cli.ClearPending(ctx)
}

func (e *Engine) Render(ctx context.Context, job engine.Job) (*engine.Result, error) {
	touchBusy()
	c := e.cfg()
	if err := e.Available(ctx); err != nil {
		return nil, err
	}
	length := engine.AlignLength(job.Length)
	w, h := engine.SnapCanvas(job.Width, job.Height)
	// 接缝模式补偿:pinned 头(22 帧)会被 Trim 裁掉,渲染长度须加回,保证成片时长不缩水
	if job.Seam && job.PrevShot > 0 {
		length = engine.AlignLength(job.Length + 22)
	}

	// 参考图上传(ComfyUI input 目录)
	uploaded := make([]string, 0, len(job.RefImages))
	for _, ref := range job.RefImages {
		name, err := e.cli.UploadImage(ctx, ref.Path)
		if err != nil {
			return nil, fmt.Errorf("参考图上传失败(%s): %w", filepath.Base(ref.Path), err)
		}
		uploaded = append(uploaded, name)
	}

	condRef := "" // ""=单图直跑内联编码;"cache:<name>"=两阶段加载缓存

	if c.CondCache && e.cli.HasNode(ctx, "MiniMaxH3CondSave") {
		// 缓存键含参考图内容指纹:换定妆照(同名覆盖)后自动失效,防旧条件隐性复用
		var refFPs []string
		for _, ref := range job.RefImages {
			refFPs = append(refFPs, ref.Path+":"+fileFingerprint(ref.Path))
		}
		cacheName := condCacheName(job.ProjectID, job.ShotID, job.Prompt, refFPs, w, h, length)
		if e.condExists(c, cacheName) {
			condRef = "cache:" + cacheName
		} else {
			if job.OnProgress != nil {
				job.OnProgress(2, "预编码(Qwen3-VL 条件)")
			}
			encGraph := buildEncodeGraph(job, c, uploaded, w, h, length, cacheName)
			if _, err := e.submitAndWait(ctx, encGraph, nil); err != nil {
				return nil, fmt.Errorf("预编码失败: %w", err)
			}
			markCondReady(c, cacheName)
			condRef = "cache:" + cacheName
		}
	}

	if job.OnProgress != nil {
		job.OnProgress(16, "采样渲染")
	}
	// 接缝前置探测:上一镜 AV latent 存档不存在(云端产物/旧渲染)则降级普通渲染
	if job.Seam && job.PrevShot > 0 {
		if !e.seamPrevExists(ctx, job.ProjectID, job.PrevShot) {
			if job.OnProgress != nil {
				job.OnProgress(15, "上一镜无接缝存档(云端产物或旧渲染),本镜普通渲染")
			}
			job.PrevShot = 0
		}
	}
	graph := buildRenderGraph(job, c, condRef, uploaded, w, h, length)
	hist, err := e.submitAndWait(ctx, graph, func(p int, note string) {
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
	if err := e.cli.Download(dlCtx, *video, dest); err != nil {
		return nil, fmt.Errorf("产物下载失败: %w", err)
	}
	e.cli.Free(context.Background())
	return &engine.Result{VideoPath: dest, Engine: e.Name()}, nil
}

func (e *Engine) submitAndWait(ctx context.Context, graph map[string]any, onProgress func(int, string)) (*HistoryEntry, error) {
	pid, err := e.cli.Submit(ctx, graph)
	if err != nil {
		return nil, err
	}
	return e.cli.WaitForDone(ctx, pid, onProgress)
}

// ---------- 条件缓存记账 ----------

func sidecarRoot(c config.ComfySettings) string {
	return filepath.Join(filepath.Dir(c.RootDir), "data")
}

func condSidecarDir(c config.ComfySettings) string {
	return filepath.Join(sidecarRoot(c), "cond_cache")
}

func (e *Engine) condExists(c config.ComfySettings, cacheName string) bool {
	_, err := os.Stat(filepath.Join(condSidecarDir(c), cacheName+".ok"))
	return err == nil
}

func markCondReady(c config.ComfySettings, cacheName string) {
	_ = os.MkdirAll(condSidecarDir(c), 0o755)
	_ = os.WriteFile(filepath.Join(condSidecarDir(c), cacheName+".ok"),
		[]byte(time.Now().Format(time.RFC3339)), 0o644)
}

func condCacheName(projectID string, shotID int, prompt string, refFPs []string, w, h, length int) string {
	sum := md5.Sum([]byte(fmt.Sprint(projectID, shotID, prompt, refFPs, w, h, length)))
	return "manjux_" + projectID + "_s" + strconv.Itoa(shotID) + "_" + hex.EncodeToString(sum[:])[:8]
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
func addConditionNode(g map[string]any, job engine.Job, uploaded []string, w, h, length int) (string, string) {
	base := map[string]any{
		"clip":   link("1", 0),
		"vae":    link("2", 0),
		"prompt": job.Prompt,
		"width":  w,
		"height": h,
		"length": length,
	}
	if job.Mode == "ref2va" && len(uploaded) > 0 {
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
		g["20"] = nd("MiniMaxH3ReferenceToVideo", inputs)
		return "20", "20"
	}
	// t2va / fl2va:MiniMaxH3ImageToVideo(不带 first/last frame 即纯文生)
	g["20"] = nd("MiniMaxH3ImageToVideo", base)
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
func buildEncodeGraph(job engine.Job, c config.ComfySettings, uploaded []string, w, h, length int, cacheName string) map[string]any {
	g := loaders(c, job.Accel)
	cond, _ := addConditionNode(g, job, uploaded, w, h, length)
	g["60"] = nd("MiniMaxH3CondSave", map[string]any{
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

// modelFileExists 在模型子目录探测文件存在(质量档/极速档的文件守卫)。
// 同时覆盖 ComfyUI/models 与 shared/models(extra_model_paths 映射的两个根)。
func modelFileExists(c config.ComfySettings, name string, subs []string) bool {
	roots := []string{
		filepath.Join(c.RootDir, "ComfyUI", "models"),
		filepath.Join(c.RootDir, "shared", "models"),
	}
	for _, root := range roots {
		for _, sub := range subs {
			if _, err := os.Stat(filepath.Join(root, sub, name)); err == nil {
				return true
			}
		}
	}
	return false
}

// buildRenderGraph 采样图:条件(内联或缓存)+ latent → 采样链 → 解码 → SaveVideo。
func buildRenderGraph(job engine.Job, c config.ComfySettings, condRef string, uploaded []string, w, h, length int) map[string]any {
	g := loaders(c, job.Accel)

	var condNode, latentNode string
	latentSlot := 1 // R2V/I2V 双输出:cond=0 / latent=1
	if condRef != "" {
		g["24"] = nd("MiniMaxH3CondLoad", map[string]any{
			"cache_name": condRef[len("cache:"):],
		})
		g["25"] = nd("EmptyMiniMaxH3LatentAV", map[string]any{"width": w, "height": h, "length": length})
		condNode, latentNode = "24", "25"
		latentSlot = 0 // EmptyLatentAV 单输出
	} else {
		condNode, latentNode = addConditionNode(g, job, uploaded, w, h, length)
	}

	// 模型链:UNET → (加速 LoRA) → SigmaShift(官方默认 12/3)
	// 加速档(知识库 2026-09 结论):
	//   quality = Kijai Acc-8Step(并行解码蒸馏,人物质感/清晰度/表情显著优于 Turbo,慢约 20%)
	//   turbo   = 官方 Turbo 4step(最快,人物偏糊)
	//   express = 官方 Turbo + int8 VAE(极速抽卡预览)
	turbo := c.TurboLoRARef2VA
	if job.Mode != "ref2va" {
		turbo = c.TurboLoRAFL2VA
	}
	if job.Accel == "quality" {
		// Acc-8Step(并行解码蒸馏)目前对 int8 量化底模不兼容
		// (tensor 32 vs 1024,官方修复未合并主线——知识库 2026-09 实测结论)。
		// int8 底模自动回退「官方 Turbo LoRA @ 8 步」:benchmark 实测 8 步质量
		// 随步数单调上升、显著优于 4 步,配合 fp16 VAE + ref_image_size=max。
		unet := unetFor(job, c)
		acc := "MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy.safetensors"
		if job.Mode != "ref2va" {
			acc = "MiniMax-H3-FL2VA-Acc-8Step_pruned_comfy.safetensors"
		}
		if !strings.Contains(unet, "int8") && modelFileExists(c, acc, []string{"loras", "diffusion_models"}) {
			turbo = acc
		}
	}
	modelLink := link("30", 0)
	g["30"] = nd("UNETLoader", map[string]any{
		"unet_name":    unetFor(job, c),
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
	g["32"] = nd("MiniMaxH3SigmaShift", map[string]any{
		"model":       modelLink,
		"shift_video": 12.0,
		"shift_audio": 3.0,
	})
	model := link("32", 0)

	steps := job.Steps
	switch job.Accel {
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
		g["70"] = nd("MiniMaxH3MotionContextLoadLatent", map[string]any{
			"latent_path": seamDir, // 目录 + clip_index 取固定槽,项目间隔离
			"clip_index":  job.PrevShot,
		})
		g["71"] = nd("MiniMaxH3MotionContext", map[string]any{
			"conditioning":         guiderCond,
			"vae":                  link("2", 0),
			"latent":               link(latentNode, latentSlot),
			"context_length":       "22",
			"audio_context_length": 24,
			"context_latent":       link("70", 0),
		})
		guiderCond = link("71", 0)
	}
	// 本镜采样输出落盘为下一镜的接缝上下文(固定槽,重渲自动覆盖)
	if job.Seam {
		g["72"] = nd("MiniMaxH3MotionContextSaveLatent", map[string]any{
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
		g["73"] = nd("MiniMaxH3MotionContextTrim", map[string]any{
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.cli.BaseURL+q, nil)
	if err != nil {
		return false
	}
	resp, err := e.cli.HTTP().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}
