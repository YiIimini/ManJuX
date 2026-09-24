package comfyengine

import (
	"context"
	"fmt"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// sam3Checkpoint SAM3 跟踪模型文件名(ComfyUI 标准 checkpoints 目录)。
// 缺它时局部重绘无法定位目标。注意 Preflight **不检查这个 checkpoint** ——
// 它只校验 Ref2V / I2V / SigmaShift 三个 H3 根节点(见 preflight.go 的注释),
// 缺模型会在提交后由 ComfyUI 在 CheckpointLoaderSimple 处报错(表现为渲染
// 跑到一半失败、报文是 node_errors),排查时先确认该文件在 checkpoints/ 下。
const sam3Checkpoint = "sam3.1_multiplex_fp16.safetensors"

// MaskOptions 局部重绘参数。
type MaskOptions struct {
	SourceVideo string // 源镜头在 ComfyUI 侧的**文件名**(调用方先上传)
	Prompt      string // 完整 H3 提示词(与整镜渲染同构的六段式)
	ObjectIndex string // 要重绘的目标序号(""=全部跟踪到的目标;"0"=第一个)
	Feather     int    // 回贴边缘羽化像素(0 用默认 12)
}

// MaskFix 执行局部重绘:对源镜头中跟踪到的目标区域重新生成,其余画面逐帧保留。
//
// srcVideoPath 是源镜头的**本地**路径(内部会先上传到 ComfyUI);
// opt.Prompt 通常直接复用该镜的原提示词 —— 只把崩坏部分的描述改对即可,
// 其余描述保持不变,重绘出来的局部才会与原画面接得上。
func (e *Engine) MaskFix(ctx context.Context, srcVideoPath string, opt MaskOptions, job engine.Job, w, h, length int, outPath string) (string, error) {
	if err := e.Available(ctx); err != nil {
		return "", err
	}
	// LoadVideo 收的是 ComfyUI 侧文件名,先把本地源视频传上去
	upName, err := e.client().UploadImage(ctx, srcVideoPath)
	if err != nil {
		return "", fmt.Errorf("上传源视频失败(局部重绘需要它作为底片): %w", err)
	}
	opt.SourceVideo = upName

	graph := buildMaskGraph(job, e.cfg(), opt, w, h, length)
	hist, err := e.submitAndWait(ctx, graph, job.Timeout, job.OnProgress)
	if err != nil {
		return "", err
	}
	files := hist.ExtractFiles()
	for i := range files {
		if !files[i].Animated {
			continue
		}
		dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		dlErr := e.client().Download(dlCtx, files[i], outPath)
		cancel()
		if dlErr == nil {
			return outPath, nil
		}
	}
	return "", fmt.Errorf("局部重绘完成但未找到视频产物")
}

// buildMaskGraph 构造「局部重绘」工作流:跟踪目标区域 → 裁出 → H3 重绘 → 贴回原画面。
//
// 用途:某镜只有局部崩坏(手部/道具/人脸)时,**只重生成那一块**,其余画面逐帧原样保留
// —— 相比整镜重渲省一个数量级的成本,而且不会把本来正确的部分一起改掉。
//
// 链路(全部是 ComfyUI 核心节点 + MaskVidExperiments,不需要额外插件包):
//
//	LoadVideo → GetVideoComponents            取源帧
//	CheckpointLoaderSimple(sam3.1)            SAM3 跟踪模型
//	→ SAM3_VideoTrack                         跨帧跟踪目标
//	→ SAM3_TrackToMask                        跟踪数据转遮罩
//	→ MVEx_SubjectCrop                        按遮罩裁出主体
//	→ MiniMaxH3ReferenceToVideo               以裁剪画面为参考重绘
//	→ MVEx_SubjectUncrop                      把重绘结果贴回原帧
//
// 与 ALLinONE 的 Mask 模式同源(它用的是同一批节点);区别是这里不引入
// VideoHelperSuite 等额外依赖 —— 帧的取用直接走 ComfyUI 核心的 GetVideoComponents。
func buildMaskGraph(job engine.Job, c config.ComfySettings, opt MaskOptions, w, h, length int) map[string]any {
	g := loaders(c, job.Accel)

	// ① 源视频取帧
	g["10"] = nd("LoadVideo", map[string]any{"file": opt.SourceVideo})
	g["11"] = nd("GetVideoComponents", map[string]any{"video": link("10", 0)})

	// ② SAM3 跨帧跟踪(首帧无初始遮罩 → 走文本检测,由 conditioning 驱动)
	g["12"] = nd("CheckpointLoaderSimple", map[string]any{"ckpt_name": sam3Checkpoint})
	g["13"] = nd("SAM3_VideoTrack", map[string]any{
		"images":              link("11", 0),
		"model":               link("12", 0),
		"detection_threshold": 0.5,
		"max_objects":         1,
		"detect_interval":     1,
	})
	g["14"] = nd("SAM3_TrackToMask", map[string]any{
		"track_data":     link("13", 0),
		"object_indices": opt.ObjectIndex,
	})

	// ③ 按遮罩裁出主体:H3 只看到"要重绘的那一块",尺寸落在 32 网格上
	g["15"] = nd("MVEx_SubjectCrop", map[string]any{
		"original_images": link("11", 0),
		"masks":           link("14", 0),
		"crop_scale":      1.5,
		"divisible_by":    32,
	})

	// ④ 模型链:与整镜渲染共用同一套加载/加速档,保证画质基线一致
	unet := unetFor(job, c)
	turbo := c.TurboLoRARef2VA
	if job.Mode != "ref2va" {
		turbo = c.TurboLoRAFL2VA
	}
	g["30"] = nd("UNETLoader", map[string]any{"unet_name": unet, "weight_dtype": "default"})
	modelLink := link("30", 0)
	if turbo != "" {
		g["31"] = nd("LoraLoaderModelOnly", map[string]any{
			"lora_name": turbo, "strength_model": 1.0, "model": modelLink,
		})
		modelLink = link("31", 0)
	}
	g["32"] = nd("MiniMaxH3SigmaShift", map[string]any{
		"model": modelLink, "shift_video": 12.0, "shift_audio": 3.0,
	})
	model := link("32", 0)

	// ⑤ H3 Ref2V:参考图给的是**裁剪出的主体**(锁外观)。当前**没有**接参考视频 ——
	//    节点本身支持 ref_videos(autogrow,max 3,24fps 的 2-15s 帧序列),但这里
	//    一个都没传,所以运动一致性不是靠参考视频约束的,而是靠 ⑦ 的「贴回原画面」:
	//    重绘结果只替换遮罩区域,其余帧像素逐帧原样保留。
	g["20"] = nd("MiniMaxH3ReferenceToVideo", map[string]any{
		"clip":           link("1", 0),
		"vae":            link("2", 0),
		"audio_vae":      link("3", 0),
		"prompt":         opt.Prompt,
		"width":          w,
		"height":         h,
		"length":         length,
		"ref_image_size": "max",
		"ref_images":     []any{link("15", 0)},
	})

	// ⑥ 采样(与整镜渲染同一套参数:官方 res_multistep + simple)
	steps := job.Steps
	if steps <= 0 {
		steps = 8
	}
	g["40"] = nd("RandomNoise", map[string]any{"noise_seed": job.Seed})
	g["41"] = nd("KSamplerSelect", map[string]any{"sampler_name": "res_multistep"})
	g["42"] = nd("BasicScheduler", map[string]any{
		"model": model, "scheduler": "simple", "steps": steps, "denoise": 1.0,
	})
	g["43"] = nd("BasicGuider", map[string]any{"model": model, "conditioning": link("20", 0)})
	g["44"] = nd("SamplerCustomAdvanced", map[string]any{
		"noise":        link("40", 0),
		"guider":       link("43", 0),
		"sampler":      link("41", 0),
		"sigmas":       link("42", 0),
		"latent_image": link("20", 1),
	})
	g["50"] = nd("VAEDecode", map[string]any{"samples": link("44", 0), "vae": link("2", 0)})

	// ⑦ 贴回原画面:只把遮罩区域换掉,其余帧像素原样保留
	feather := opt.Feather
	if feather <= 0 {
		feather = 12
	}
	g["16"] = nd("MVEx_SubjectUncrop", map[string]any{
		"cropped_images":  link("50", 0),
		"original_images": link("11", 0),
		"cropped_masks":   link("14", 0),
		"feather":         feather,
	})

	// ⑧ 输出:音轨沿用源视频(局部重绘不改声音)
	g["18"] = nd("CreateVideo", map[string]any{
		"images": link("16", 0),
		"audio":  link("11", 1),
		"fps":    24.0,
	})
	g["19"] = nd("SaveVideo", map[string]any{
		"video":           link("18", 0),
		"filename_prefix": "manjux/" + baseName(job.OutputName) + "_maskfix",
		"format":          "auto",
		"codec":           "auto",
	})
	return g
}
