package comfyengine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
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
	cmd := exec.Command(py, main, "--port", port, "--listen", "127.0.0.1")
	cmd.Dir = filepath.Dir(main)
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
func (e *Engine) Render(ctx context.Context, job engine.Job) (*engine.Result, error) {
	c := e.cfg()
	if err := e.Available(ctx); err != nil {
		return nil, err
	}
	length := engine.AlignLength(job.Length)
	w, h := engine.SnapCanvas(job.Width, job.Height)

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
		cacheName := condCacheName(job.ProjectID, job.ShotID, job.Prompt, uploaded, w, h, length)
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

func condCacheName(projectID string, shotID int, prompt string, refs []string, w, h, length int) string {
	sum := md5.Sum([]byte(fmt.Sprint(projectID, shotID, prompt, refs, w, h, length)))
	return "manjux_" + projectID + "_s" + strconv.Itoa(shotID) + "_" + hex.EncodeToString(sum[:])[:8]
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
		"clip":    link("1", 0),
		"vae":     link("2", 0),
		"prompt":  job.Prompt,
		"width":   w,
		"height":  h,
		"length":  length,
	}
	if job.Mode == "ref2va" && len(uploaded) > 0 {
		inputs := copyMap(base)
		inputs["audio_vae"] = link("3", 0)
		inputs["ref_image_size"] = "match"
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
	g := loaders(c)
	cond, _ := addConditionNode(g, job, uploaded, w, h, length)
	g["60"] = nd("MiniMaxH3CondSave", map[string]any{
		"conditioning": link(cond, 0),
		"cache_name":   cacheName,
	})
	return g
}

func loaders(c config.ComfySettings) map[string]any {
	return map[string]any{
		"1": nd("CLIPLoader", map[string]any{"clip_name": c.CLIP, "type": "minimax"}),
		"2": nd("VAELoader", map[string]any{"vae_name": c.VideoVAE}),
		"3": nd("VAELoader", map[string]any{"vae_name": c.AudioVAE}),
	}
}

// buildRenderGraph 采样图:条件(内联或缓存)+ latent → 采样链 → 解码 → SaveVideo。
func buildRenderGraph(job engine.Job, c config.ComfySettings, condRef string, uploaded []string, w, h, length int) map[string]any {
	g := loaders(c)

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

	// 模型链:UNET → (LoRA) → SigmaShift(官方默认 12/3)
	turbo := c.TurboLoRARef2VA
	if job.Mode != "ref2va" {
		turbo = c.TurboLoRAFL2VA
	}
	modelLink := link("30", 0)
	g["30"] = nd("UNETLoader", map[string]any{
		"unet_name":  unetFor(job, c),
		"weight_dtype": "default",
	})
	if turbo != "" {
		g["31"] = nd("LoraLoaderModelOnly", map[string]any{
			"lora_name":     turbo,
			"strength_model": 1.0,
			"model":         modelLink,
		})
		modelLink = link("31", 0)
	}
	g["32"] = nd("MiniMaxH3SigmaShift", map[string]any{
		"model":        modelLink,
		"shift_video":  12.0,
		"shift_audio":  3.0,
	})
	model := link("32", 0)

	steps := job.Steps
	if steps <= 0 {
		steps = 8
	}
	g["40"] = nd("RandomNoise", map[string]any{"noise_seed": job.Seed})
	g["41"] = nd("KSamplerSelect", map[string]any{"sampler_name": "res_multistep"})
	g["42"] = nd("BasicScheduler", map[string]any{
		"model":    model,
		"scheduler": "simple",
		"steps":    steps,
		"denoise":  1.0,
	})
	g["43"] = nd("BasicGuider", map[string]any{
		"model":        model,
		"conditioning": link(condNode, 0),
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
	g["52"] = nd("CreateVideo", map[string]any{
		"images": link("50", 0),
		"fps":    24.0,
		"audio":  link("51", 0),
	})
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
