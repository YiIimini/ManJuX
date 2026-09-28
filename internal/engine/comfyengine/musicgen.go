package comfyengine

import (
	"context"
	"fmt"
	"github.com/YiIimini/ManJuX/internal/engine"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// musicgen.go 配乐生成（ComfyUI 原生 ACE-Step 1.5）。
//
// 【为什么走原生节点而不是第三方插件】本机 ComfyUI 自带 `comfy_extras/nodes_ace.py`
// 与官方蓝本 `blueprints/Text to Audio (ACE-Step 1.5).json` —— 接入只需要模型文件，
// 不必装任何自定义节点（少一层"插件升级把管线带崩"的风险）。
//
// 【全部来自本地权威源，没有一处是猜的】节点名与输入取自 ComfyUI 源码
// （`comfy_extras/nodes_ace.py`、`comfy_extras/nodes_audio.py`），连线与默认值取自官方蓝本。
// 四个模型文件（合计 13.68 GB）与落点见 docs/ACE-Step接入记录.md。
//
// 【为什么 BGM 只要器乐】平台已经按官方 qc-checklist 做"成片一条连续 BGM + 人声下闪避"
// （compose/bgm.go），而 H3 片段自带同期声与台词 ⇒ 配乐必须是**纯器乐**，否则会跟台词打架。
// 所以这里固定空歌词（lyrics 传空串）、并在 tags 里强调 instrumental。

// MusicModelFiles ACE-Step 1.5 需要的四个模型文件（与 docs/ACE-Step接入记录.md 一致）。
type MusicModelFiles struct {
	Diffusion    string // models/diffusion_models/
	TextEncoder1 string // models/text_encoders/（0.6B）
	TextEncoder2 string // models/text_encoders/（4B）
	VAE          string // models/vae/
}

// DefaultMusicModels 本机实测的默认文件名（用户可用引擎配置覆盖）。
func DefaultMusicModels() MusicModelFiles {
	return MusicModelFiles{
		Diffusion:    "acestep_v1.5_turbo.safetensors",
		TextEncoder1: "qwen_0.6b_ace15.safetensors",
		TextEncoder2: "qwen_4b_ace15.safetensors",
		VAE:          "ace_1.5_vae.safetensors",
	}
}

// buildMusicGraph 构造 ACE-Step 1.5 的图。
//
// 结构（与官方蓝本一致，只去掉 UI 辅助节点、末尾补一个落盘节点）：
//
//	UNETLoader ─┐
//	DualCLIPLoader(ace) ─ TextEncodeAceStepAudio1.5 ─┐
//	ConditioningZeroOut ────────────────────────────┼─ KSampler ─ VAEDecodeAudio ─ SaveAudioMP3
//	EmptyAceStep1.5LatentAudio ─────────────────────┘
//	VAELoader ───────────────────────────────────────┘（给 KSampler 与解码用）
func buildMusicGraph(m MusicModelFiles, job engine.MusicJob, seed int64, prefix string) map[string]any {
	if job.BPM <= 0 {
		job.BPM = 120
	}
	if job.Seconds <= 0 {
		job.Seconds = 120
	}
	return map[string]any{
		"1": nd("UNETLoader", map[string]any{"unet_name": m.Diffusion, "weight_dtype": "default"}),
		// ACE 1.5 用 **DualCLIPLoader**（两个编码器缺一不可），type 必须是 "ace"
		"2": nd("DualCLIPLoader", map[string]any{
			"clip_name1": m.TextEncoder1, "clip_name2": m.TextEncoder2,
			"type": "ace", "device": "default",
		}),
		"3": nd("VAELoader", map[string]any{"vae_name": m.VAE}),
		// 采样器要的 model 输入：官方蓝本在 UNETLoader 之后挂了 ModelSamplingAuraFlow
		"4": nd("ModelSamplingAuraFlow", map[string]any{"model": link("1", 0), "shift": 1.0}),
		"5": nd("TextEncodeAceStepAudio1.5", map[string]any{
			"clip": link("2", 0),
			"tags": job.Tags,
			// **纯器乐**：歌词留空（配乐要与台词共存，带唱词会打架）
			"lyrics":               "",
			"seed":                 seed,
			"bpm":                  job.BPM,
			"duration":             job.Seconds,
			"timesignature":        "4",
			"language":             "en",
			"keyscale":             "C major",
			"generate_audio_codes": true,
			"cfg_scale":            2.0,
			"temperature":          0.85,
			"top_p":                0.9,
			"top_k":                0,
			"min_p":                0.0,
		}),
		"6": nd("ConditioningZeroOut", map[string]any{"conditioning": link("5", 0)}),
		"7": nd("EmptyAceStep1.5LatentAudio", map[string]any{"seconds": job.Seconds, "batch_size": 1}),
		"8": nd("KSampler", map[string]any{
			"model": link("4", 0), "positive": link("5", 0), "negative": link("6", 0),
			"latent_image": link("7", 0),
			"seed":         seed, "steps": 50, "cfg": 5.0,
			"sampler_name": "euler", "scheduler": "simple", "denoise": 1.0,
		}),
		"9":  nd("VAEDecodeAudio", map[string]any{"samples": link("8", 0), "vae": link("3", 0)}),
		"10": nd("SaveAudioMP3", map[string]any{"audio": link("9", 0), "filename_prefix": prefix, "quality": "V0"}),
	}
}

// GenerateMusic 生成一段配乐并返回落盘路径。
//
// 时长给足：首次运行要加载 4B 文本编码器（比出图链路慢得多），超时默认 20 分钟。
func (e *Engine) GenerateMusic(ctx context.Context, job engine.MusicJob) (string, error) {
	if strings.TrimSpace(job.Tags) == "" {
		return "", fmt.Errorf("配乐提示词为空（tags 必须写风格/情绪/乐器）")
	}
	if job.OutputDir == "" || job.OutputName == "" {
		return "", fmt.Errorf("配乐落点未指定")
	}
	models := DefaultMusicModels()
	if c := e.Cfg(); c.MusicDiffusion != "" {
		models = MusicModelFiles{
			Diffusion: c.MusicDiffusion, TextEncoder1: c.MusicTextEncoder1,
			TextEncoder2: c.MusicTextEncoder2, VAE: c.MusicVAE,
		}
	}
	seed := job.Seed
	if seed == 0 {
		seed = time.Now().UnixNano() & 0x7fffffff
	}
	prefix := sanitizePrefix(job.OutputName)
	graph := buildMusicGraph(models, job, seed, prefix)
	timeout := job.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	hist, err := e.submitAndWait(ctx, graph, timeout, nil)
	if err != nil {
		return "", err
	}
	files := hist.ExtractFiles()
	// 取本次真正产出的音频文件（排除 input 类型，见 client.ExtractFiles 的说明）
	var want *OutputFile
	for i := range files {
		f := files[i]
		if f.Type == "input" || strings.TrimSpace(f.Filename) == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Filename))
		if ext == ".mp3" || ext == ".flac" || ext == ".wav" || ext == ".ogg" {
			want = &files[i]
			break
		}
	}
	if want == nil {
		return "", fmt.Errorf("ComfyUI 没有产出音频文件（共 %d 个产物）", len(files))
	}
	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(job.OutputDir, job.OutputName+filepath.Ext(want.Filename))
	dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := e.client().Download(dlCtx, *want, dst); err != nil {
		return "", fmt.Errorf("配乐下载失败: %w", err)
	}
	if st, err := os.Stat(dst); err != nil || st.Size() == 0 {
		return "", fmt.Errorf("配乐落盘失败或为空:%s", dst)
	}
	return dst, nil
}

// ProbeAudioSeconds 读音频时长（给"配乐时长是否够铺满成片"这类判断用）。
func ProbeAudioSeconds(ctx context.Context, f *ff.FF, path string) (float64, error) {
	if f == nil {
		return 0, ff.Missing("ffmpeg")
	}
	info, err := f.Probe(path)
	if err != nil {
		return 0, err
	}
	return info.Duration, nil
}
