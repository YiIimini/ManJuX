package comfyengine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// sam3Checkpoint SAM3 跟踪模型文件名(ComfyUI 标准 checkpoints 目录)。
// 缺它时局部重绘无法定位目标。它**不在 Preflight 的节点体检里**(那套只校验
// H3 根节点),所以另给了 MaskAssets() 做这条链路的权重+节点体检 ——
// 缺模型要在**点下去之前**就告诉用户,而不是等 ComfyUI 执行到那一节点抛原始报文。
const sam3Checkpoint = "sam3.1_multiplex_fp16.safetensors"

// MaskOptions 局部重绘参数。
type MaskOptions struct {
	SourceVideo string // 源镜头在 ComfyUI 侧的**文件名**(调用方先上传)
	Prompt      string // 完整 H3 提示词(与整镜渲染同构的六段式)
	// Target 要跟踪/重绘的目标,**英文名词短语**(如 the bronze sparrow / the young man /
	// the lantern / the hands)。SAM3 是"开放词表"跟踪器:必须告诉它找什么,否则
	// 节点会 `raise ValueError("Either initial_mask or conditioning must be provided")`。
	//
	// 2026-09-24 审计发现的死功能:这张图从建立起就**两个必需输入一个都没传**,
	// 而 UI 上按钮、弹窗、API 一应俱全 —— 用户点下去只会在 ComfyUI 侧抛原始报文。
	Target      string
	ObjectIndex string // 跟踪到多个目标时取哪一个(""=全部;"0"=第一个)
	Feather     int    // 回贴边缘羽化像素(0 用默认 12)
}

// MaskTargetHint 缺目标时的统一提示语(引擎、API、前端共用一句话,免得三处口径不一)。
const MaskTargetHint = "局部重绘需要指定要跟踪的目标(英文名词短语,如 the bronze sparrow / the young man / the hands):" +
	"SAM3 是开放词表跟踪器,不给它目标就无法判断该重绘哪一块"

// validate 参数自检。**必须在提交任务之前做**:这类缺参数的错误一旦漏到 ComfyUI 侧,
// 用户拿到的是节点原始报文(ValueError: Either initial_mask or conditioning...),
// 既不知道是自己少填了东西,也不知道该填什么。
func (o MaskOptions) validate() error {
	if strings.TrimSpace(o.Target) == "" {
		return fmt.Errorf("%s", MaskTargetHint)
	}
	return nil
}

// MaskFix 执行局部重绘:对源镜头中跟踪到的目标区域重新生成,其余画面逐帧保留。
//
// srcVideoPath 是源镜头的**本地**路径(内部会先上传到 ComfyUI);
// opt.Prompt 通常直接复用该镜的原提示词 —— 只把崩坏部分的描述改对即可,
// 其余描述保持不变,重绘出来的局部才会与原画面接得上。
func (e *Engine) MaskFix(ctx context.Context, srcVideoPath string, opt MaskOptions, job engine.Job, w, h, length int, outPath string) (string, error) {
	if err := opt.validate(); err != nil {
		return "", err
	}
	// 与 Render 同理:进出都打点,免得空闲看门狗在长作业期间把引擎判成空闲
	// (这条链路要上传源视频、跑十几分钟 —— 见 idle.go 的 shouldIdleStop)。
	touchBusy()
	defer touchBusy()
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
// 用途:某镜只有局部崩坏(手部/道具/人脸)时,把目标区域**作为参考图**喂给 H3 重绘,
// 再用遮罩只把那块贴回原画面 —— 相比整镜重渲,省下的是"正确部分被模型一起改掉"的风险。
//
// **代价要说清(2026-09-26 渲染管线审查更正)**:H3 拿到的是**整幅机位画布**
// (width/height 用的是 w/h,不是裁剪尺寸),采样成本与整镜重渲同级;"省一个数量级"
// 只体现在"只有一块被替换"的语义上。曾经写成"H3 只看到要重绘的那一块、省一个数量级",
// 与图不符 —— 裁剪结果确实只作为 ref_images 参考图进入条件,并没有缩小采样画布。
// (真按裁剪尺寸采样需要 MVEx_SubjectCrop 的**运行时**尺寸喂给 H3 的 width/height,
// 静态图构建拿不到,属于改链路的设计决定,不在本次缺陷修复范围内。)
//
// 链路(全部是 ComfyUI 核心节点 + MaskVidExperiments,不需要额外插件包):
//
//	LoadVideo → GetVideoComponents            取源帧
//	CheckpointLoaderSimple(sam3.1)            SAM3 跟踪模型
//	→ SAM3_VideoTrack                         跨帧跟踪目标
//	→ SAM3_TrackToMask                        跟踪数据转遮罩
//	→ MVEx_SubjectCrop                        按遮罩裁出主体(作为 H3 的参考图)
//	→ MiniMaxH3ReferenceToVideo               按整幅画布重绘(参考图锁外观)
//	→ MVEx_SubjectUncrop                      只把遮罩区域贴回原帧
//
// 与 ALLinONE 的 Mask 模式同源(它用的是同一批节点);区别是这里不引入
// VideoHelperSuite 等额外依赖 —— 帧的取用直接走 ComfyUI 核心的 GetVideoComponents。
func buildMaskGraph(job engine.Job, c config.ComfySettings, opt MaskOptions, w, h, length int) map[string]any {
	g := loaders(c, job.Accel)

	// ① 源视频取帧
	g["10"] = nd("LoadVideo", map[string]any{"file": opt.SourceVideo})
	g["11"] = nd("GetVideoComponents", map[string]any{"video": link("10", 0)})

	// ② SAM3 跨帧跟踪
	//
	// 首帧没有初始遮罩(本应用没有框选/涂抹 UI),所以**必须**走文本检测:
	// CheckpointLoaderSimple 的 CLIP 槽(输出 1)就是 SAM3 自己的文本编码器,
	// 用它把目标名词短语编码成 conditioning 喂给跟踪节点。
	// 漏掉这一步的后果是硬失败 —— 节点里写着:
	//   if conditioning ... elif initial_mask is None: raise ValueError("Either initial_mask or conditioning must be provided")
	g["12"] = nd("CheckpointLoaderSimple", map[string]any{"ckpt_name": sam3Checkpoint})
	g["17"] = nd("CLIPTextEncode", map[string]any{
		"clip": link("12", 1),
		"text": opt.Target,
	})
	g["13"] = nd("SAM3_VideoTrack", map[string]any{
		"images":              link("11", 0),
		"model":               link("12", 0),
		"conditioning":        link("17", 0),
		"detection_threshold": 0.5,
		"max_objects":         1,
		"detect_interval":     1,
	})
	g["14"] = nd("SAM3_TrackToMask", map[string]any{
		"track_data":     link("13", 0),
		"object_indices": opt.ObjectIndex,
	})

	// ③ 按遮罩裁出主体:裁剪结果作为 H3 的**参考图**(锁住要重绘那一块的外观,
	//    而不是整幅画面的构图)。注意 H3 的采样画布仍是整幅 w×h(见函数头说明):
	//    裁剪尺寸落在 32 网格上是给节点算 bbox 用的。
	g["15"] = nd("MVEx_SubjectCrop", map[string]any{
		"original_images": link("11", 0),
		"masks":           link("14", 0),
		"crop_scale":      1.5,
		"divisible_by":    32,
	})

	// ④ 模型链:与整镜渲染共用同一套加载/加速档,保证画质基线一致。
	//    **加速 LoRA 必须走 accelLoraFor**(审计 P2):这段原先自己按"mode 取
	//    Turbo LoRA",漏了 quality 档的 Acc-8Step 与量化底模的回退守卫 ——
	//    于是"quality 档项目里修补过的那一镜"会用 Turbo 渲染,画质与周围镜头不同。
	unet := unetFor(job, c)
	turbo := accelLoraFor(job, c)
	g["30"] = nd("UNETLoader", map[string]any{"unet_name": unet, "weight_dtype": "default"})
	modelLink := link("30", 0)
	if turbo != "" {
		g["31"] = nd("LoraLoaderModelOnly", map[string]any{
			"lora_name": turbo, "strength_model": 1.0, "model": modelLink,
		})
		modelLink = link("31", 0)
	}
	g["32"] = nd(classSigmaShift, map[string]any{
		"model": modelLink, "shift_video": 12.0, "shift_audio": 3.0,
	})
	model := link("32", 0)

	// ⑤ H3 Ref2V:参考图给的是**裁剪出的主体**(锁外观)。当前**没有**接参考视频 ——
	//    节点本身支持 ref_videos(autogrow,max 3,24fps 的 2-15s 帧序列),但这里
	//    一个都没传,所以运动一致性不是靠参考视频约束的,而是靠 ⑦ 的「贴回原画面」:
	//    重绘结果只替换遮罩区域,其余帧像素逐帧原样保留。
	g["20"] = nd(classRef2V, map[string]any{
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

	// ⑥ 采样(与整镜渲染同一套参数:官方 res_multistep + simple,步数也走同一张档位表 ——
	// 局部重绘不许自成一套:refine 档在这里少跑 12 步,修补过的那块就成了画质断层)
	steps := accelSteps(job.Accel, job.Steps)
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
