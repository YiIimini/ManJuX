package comfyengine

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
)

// imageedit.go 多视角图像编辑。
//
// 【为什么需要这条路】角色多视角原先靠 H3 转身视频抽帧,而视频模型会**顺手重新设计**
// 角色:实测赛璐璐动漫少年被画成写实真人、插画铜雀被画成 3D 机械铜鸟 —— 逐帧身份校验
// (qc.IdentityJudge)因此正确地拒绝,多视角永远拿不到(见 docs/AUDIT-2026-09-24.md)。
// 编辑式模型是"改你给的那张图":身份/配色/画风按像素保住,只把视角转过去。
//
// 【两条图形态,按编辑底模的文件名自动选】(2026-09-25 用户定调「都使用 2.1」)
//
//	Qwen-Image-2.1(默认) —— 与官方模板 image_qwen_image_2_1_image_edit.json 同构:
//	  UNETLoader(qwen_image_2.1) → CLIPLoader(qwen3vl_8b, type=qwen_image) → VAELoader
//	  LoadImage(image_1..N) ┐
//	  TextEncodeQwenImage21(clip, prompt, negative_prompt, vae, images.image_N)
//	                       ├→ positive / negative → KSampler
//	                       └→ latent(按第一张参考图的画幅)→ KSampler
//	  KSampler(25 步 / cfg 1 / euler / simple)→ VAEDecode → SaveImage
//	  参考图直接进文本编码器(节点内部按 resolution 缩放),**不需要** AuraFlow 位移,
//	  也**不需要**任何 LoRA —— 一个权重同时管生成与编辑。
//
//	Qwen-Image-Edit-2511(存量设置仍可选中) —— 与官方模板 image_qwen_image_edit_2511.json 同构:
//	  UNETLoader(qwen_image_edit_2511) → ModelSamplingAuraFlow(shift 3.1)
//	    → [LoraLoaderModelOnly: Lightning 4 步] → [LoraLoaderModelOnly: Multiple-Angles]
//	  LoadImage(参考图) → FluxKontextImageScale ┐
//	  TextEncodeQwenImageEditPlus(pos/neg, image1..image3)┘
//	                └→ VAEEncode → KSampler → VAEDecode → SaveImage
//
// 官方 2511 模板里那两个 FluxKontextMultiReferenceLatentMethod 节点**本机不需要**
// (模板 Note 原文:"not needed if you use Comfy files" —— 我们用的正是 Comfy-Org 打包的
// fp8mixed),所以不加;若日后换成第三方 repack 权重且出现结构崩坏,再补那一步。

// ImageEditOptions 一次"按角度重绘"的参数。
type ImageEditOptions struct {
	// RefImages 参考图**本地路径**(1-3 张)。多角度一致性靠它:第一张必须是定妆照,
	// 可再附上已生成的角度图(2.1 最多吃 16 张,2511 官方支持 image1..image3)。
	RefImages []string
	Prompt    string // 编辑指令(英文,见 pipeline.viewEditPrompt)
	Negative  string // 负面提示(CFG=1 的档位会被忽略)
	Steps     int    // 0 = 按家族/加速档自动
	CFG       float64
	Seed      int64
	UseTurbo  bool          // 2511:Lightning 4 步 LoRA;2.1 无对应 LoRA,此标志被忽略
	UseAngles bool          // 2511:Multiple-Angles LoRA;2.1 不需要(它自带参考图机制)
	Timeout   time.Duration // <=0 时用**图像级**兜底 defaultImageTimeout(10 分钟),不是单镜的 45 分钟

	// OnProgress 生成进度回调(percent 0-100 + 一句中文说明)。
	//
	// 补这个字段的原因(甲6,2026-09-26 渲染管线审查):多视角补齐是"每个角色 3 张、
	// 逐角度串行"的链路,而 EditImage 此前把进度回调写死传 nil —— 用户从点下到出结果
	// 既无日志也无百分比,只能靠"停止"判断是不是卡死。
	//
	// 百分比**只增不减**(见 EditImage 里的单调化):它会被界面当成进度条读数,
	// 而 WaitForDone 的档位文案在"排队(2)→ 执行中(20)→ 渲染中 N%"之间切换,
	// N 来自 ComfyUI 的 progress 事件,可能低于前一个档位值。回调的 note 里始终是
	// 引擎的原始文案,信息不丢。
	OnProgress func(percent int, note string)
}

// imageEditTimeout 编辑链路的实际超时。**唯一实现**,便于单测钉住"用的是图像级档位"。
//
// 回归(2026-09-26 渲染管线审查):旧实现把 0 原样透传下去,而 WaitForDone 在 timeout<=0
// 时回退的是 **defaultShotTimeout(45 分钟)** —— 一张多视角图卡住要白等 45 分钟,而这条
// 链路处在"渲染前资产补齐"里,每个角色 3 张、失败还要重试,最坏把整条管线堵上小时级。
// 项目里早就为此另立了 defaultImageTimeout(handlers_enhance.go 的注释写着定妆图那条链
// 就是因为"共用一个 45 分钟"才分开的),只是这条路没用上。
func imageEditTimeout(opt ImageEditOptions) time.Duration {
	if opt.Timeout > 0 {
		return opt.Timeout
	}
	return defaultImageTimeout
}

// editProgress 把调用方的进度回调包成 submitAndWait 需要的形式,并做两件事:
//
//	① nil 安全:不接回调的调用方原样得到 nil,不制造一个空回调;
//	② 百分比单调化:界面会把它当进度条读数,而 WaitForDone 的百分比在**换档**时可能回退
//	   (排队 2 → 执行中 20 → 渲染中 N%,N 来自 ComfyUI 的 progress 事件)。这里只抬高、
//	   不压低;note 始终是引擎原文,所以"现在到底在干什么"一点没丢。
//
// 回调由 WaitForDone 的轮询循环串行调用,last 不需要加锁。
func editProgress(cb func(percent int, note string)) func(int, string) {
	if cb == nil {
		return nil
	}
	last := -1
	return func(pct int, note string) {
		if pct < last {
			pct = last
		}
		last = pct
		cb(pct, note)
	}
}

// EditImage 用 Qwen-Image-Edit 生成一张"同主体、不同视角"的图,返回本地路径。
func (e *Engine) EditImage(ctx context.Context, opt ImageEditOptions, outPath string) (string, error) {
	if err := e.Available(ctx); err != nil {
		return "", err
	}
	c := e.cfg()
	if strings.TrimSpace(c.EditUNet) == "" || strings.TrimSpace(c.EditCLIP) == "" || strings.TrimSpace(c.EditVAE) == "" {
		return "", fmt.Errorf("图像编辑模型未配置:请在设置里填写编辑用的 UNet / CLIP / VAE 文件名(默认与生成同一个 Qwen-Image-2.1)")
	}
	if len(opt.RefImages) == 0 {
		return "", fmt.Errorf("多视角编辑至少需要 1 张参考图")
	}
	// 上传参考图(ComfyUI 侧只认 input 目录里的文件名)
	var upNames []string
	for _, p := range opt.RefImages {
		n, err := e.client().UploadImage(ctx, p)
		if err != nil {
			return "", fmt.Errorf("上传参考图失败(%s): %w", filepath.Base(p), err)
		}
		upNames = append(upNames, n)
	}
	graph := buildEditGraph(c, opt, upNames)
	hist, err := e.submitAndWait(ctx, graph, imageEditTimeout(opt), editProgress(opt.OnProgress))
	if err != nil {
		return "", err
	}
	// 取最后一张静态图产物(编辑结果的节点在 SaveImage 上)
	files := hist.ExtractFiles()
	for i := len(files) - 1; i >= 0; i-- {
		if files[i].Animated {
			continue
		}
		if err := e.client().Download(ctx, files[i], outPath); err == nil {
			return outPath, nil
		}
	}
	return "", fmt.Errorf("多视角编辑完成但未找到图片产物")
}

// buildEditGraph 构造编辑图。家族按**编辑底模的文件名**判定(见 imagefamily.go):
// Qwen-Image-2.1 走单条件节点 + 自带的 latent;其余(含 2511)走旧的 EditPlus 链路。
func buildEditGraph(c config.ComfySettings, opt ImageEditOptions, refs []string) map[string]any {
	fam := ResolveImageFamily("", c.EditUNet, c.EditCLIP)
	if fam.Encode21 {
		return buildEditGraphQwen21(c, opt, refs)
	}
	return buildEditGraphLegacy(c, opt, refs)
}

// buildEditGraphQwen21 Qwen-Image-2.1 的编辑图(官方模板 image_qwen_image_2_1_image_edit.json)。
//
// 与旧链路的三个实质差别:
//  1. 参考图直接进 TextEncodeQwenImage21 的 images.image_N,由该节点统一按 resolution 缩放
//     (文本塔看到的与 VAE 编码的是同一张缩放图)—— 不再需要 FluxKontextImageScale。
//  2. latent 取该节点的**第 3 个输出**(按第一张参考图的画幅),而不是自己 VAEEncode:
//     官方模板用 ComfySwitchNode 在"有参考图 → 节点 latent / 无参考图 → EmptyLatent"之间切,
//     我们这条路必然有参考图,所以直接接节点 latent。
//  3. 没有 AuraFlow 位移、没有 LoRA(2.1 的 Lightning/多角度 LoRA 目前不存在,也不该硬套 2511 的)。
func buildEditGraphQwen21(c config.ComfySettings, opt ImageEditOptions, refs []string) map[string]any {
	g := map[string]any{
		"1": nd("CLIPLoader", map[string]any{"clip_name": c.EditCLIP, "type": "qwen_image", "device": "default"}),
		"2": nd("VAELoader", map[string]any{"vae_name": c.EditVAE}),
		"3": nd("UNETLoader", map[string]any{"unet_name": c.EditUNet, "weight_dtype": "default"}),
	}

	// 参考图:第 1 张固定 image_1(它是画幅与身份的主参考),其余依次 image_2..image_16。
	enc := map[string]any{
		"clip": link("1", 0), "prompt": opt.Prompt, "negative_prompt": opt.Negative,
		"vae": link("2", 0), "resolution": 1024,
	}
	for i, n := range refs {
		id := "1" + strconv.Itoa(i+1) // 110 / 111 / 112 …
		g[id] = nd("LoadImage", map[string]any{"image": n})
		enc["images.image_"+strconv.Itoa(i+1)] = link(id, 0)
	}
	g["20"] = nd("TextEncodeQwenImage21", enc)

	steps, cfg := opt.Steps, opt.CFG
	if steps <= 0 {
		steps = 25 // 官方 2.1 模板档
	}
	if cfg <= 0 {
		cfg = 1.0
	}
	g["31"] = nd("KSampler", map[string]any{
		"model": link("3", 0), "positive": link("20", 0), "negative": link("20", 1),
		"latent_image": link("20", 2),
		"seed":         opt.Seed, "steps": steps, "cfg": cfg,
		"sampler_name": "euler", "scheduler": "simple", "denoise": 1.0,
	})
	g["32"] = nd("VAEDecode", map[string]any{"samples": link("31", 0), "vae": link("2", 0)})
	g["33"] = nd("SaveImage", map[string]any{"images": link("32", 0), "filename_prefix": "manjux/edit"})
	return g
}

// buildEditGraphLegacy Qwen-Image-Edit-2511 的编辑图(单图编辑 + 可选 LoRA)。
func buildEditGraphLegacy(c config.ComfySettings, opt ImageEditOptions, refs []string) map[string]any {
	g := map[string]any{}

	// ① 模型三件套
	g["1"] = nd("CLIPLoader", map[string]any{"clip_name": c.EditCLIP, "type": "qwen_image"})
	g["2"] = nd("VAELoader", map[string]any{"vae_name": c.EditVAE})
	g["3"] = nd("UNETLoader", map[string]any{"unet_name": c.EditUNet, "weight_dtype": "default"})
	// ② 官方 shift 3.1(AuraFlow 采样设置;模板里就是这一个固定值)
	g["4"] = nd("ModelSamplingAuraFlow", map[string]any{"model": link("3", 0), "shift": 3.1})
	model := link("4", 0)

	// ③ 可选 LoRA:顺序固定为 加速 → 转角度(Multiple-Angles 是给"转视角"训的,
	//    放在加速之后,官方示例也是这么叠的)
	next := 5
	if opt.UseTurbo && strings.TrimSpace(c.EditTurboLoRA) != "" {
		id := strconv.Itoa(next)
		next++
		g[id] = nd("LoraLoaderModelOnly", map[string]any{
			"lora_name": c.EditTurboLoRA, "strength_model": 1.0, "model": model,
		})
		model = link(id, 0)
	}
	if opt.UseAngles && strings.TrimSpace(c.EditAnglesLoRA) != "" {
		id := strconv.Itoa(next)
		next++
		g[id] = nd("LoraLoaderModelOnly", map[string]any{
			"lora_name": c.EditAnglesLoRA, "strength_model": 1.0, "model": model,
		})
		model = link(id, 0)
	}

	// ④ 参考图:第一张缩放到 Kontext 支持的分辨率,其余原样进条件
	g["10"] = nd("LoadImage", map[string]any{"image": refs[0]})
	g["11"] = nd("FluxKontextImageScale", map[string]any{"image": link("10", 0)})
	// 图像条件(image1..image3)由正负**两组**条件共用:官方模板里正负两条
	// TextEncodeQwenImageEditPlus 吃同一批参考图。只给正条件配 image2/3、负条件只留
	// image1 是半个接线 —— CFG>1 时负条件会带着与正条件不同的图像隐空间去比,
	// 而这类错误在 turbo 档(CFG 1.0,负条件不生效)下根本看不出来。
	imgCond := map[string]any{"image1": link("11", 0)}
	for i, n := range refs {
		if i == 0 || i > 2 {
			continue // image1 已用缩放后的;最多 3 张
		}
		id := "12" + strconv.Itoa(i) // "121" / "122"
		g[id] = nd("LoadImage", map[string]any{"image": n})
		imgCond["image"+strconv.Itoa(i+1)] = link(id, 0)
	}

	// ⑤ 正负条件(编辑指令 + 负面)
	pos := map[string]any{"clip": link("1", 0), "vae": link("2", 0), "prompt": opt.Prompt}
	neg := map[string]any{"clip": link("1", 0), "vae": link("2", 0), "prompt": opt.Negative}
	for k, v := range imgCond {
		pos[k] = v
		neg[k] = v
	}
	g["20"] = nd("TextEncodeQwenImageEditPlus", pos)
	g["21"] = nd("TextEncodeQwenImageEditPlus", neg)

	// ⑥ 采样:latent 来自**被缩放后的参考图**(编辑任务 = 在它的 latent 上重绘)
	g["30"] = nd("VAEEncode", map[string]any{"pixels": link("11", 0), "vae": link("2", 0)})

	steps := opt.Steps
	cfg := opt.CFG
	if steps <= 0 {
		if opt.UseTurbo {
			steps = 4
		} else {
			steps = 20 // Comfy 官方模板给的 20(作者档 40)
		}
	}
	if cfg <= 0 {
		if opt.UseTurbo {
			cfg = 1.0 // 4 步蒸馏必须 CFG 1.0
		} else {
			cfg = 4.0 // 官方模板值
		}
	}
	g["31"] = nd("KSampler", map[string]any{
		"model": model, "positive": link("20", 0), "negative": link("21", 0),
		"latent_image": link("30", 0),
		"seed":         opt.Seed, "steps": steps, "cfg": cfg,
		"sampler_name": "euler", "scheduler": "simple", "denoise": 1.0,
	})
	g["32"] = nd("VAEDecode", map[string]any{"samples": link("31", 0), "vae": link("2", 0)})
	g["33"] = nd("SaveImage", map[string]any{
		"images":          link("32", 0),
		"filename_prefix": "manjux/edit",
	})
	return g
}

// EditReady 分流探针,语义与 EditAssets 相同,只多一条"引擎不可达"。
//
// 为什么必须区分:EditAssets 在引擎不可达时返回空(= 看起来"就绪"),那是给
// "点按钮前预检"用的语义(不可达会在提交时报出来)。而 pipeline 的分流要在**动手
// 之前**选路径,把不可达读成就绪会让它选出编辑路径、再以一个含糊的执行错误收场。
// 不可达时返回一条明确说明,分流照旧退回视频路径 —— 那条路会把"ComfyUI 不可达"
// 如实报出来。
func (e *Engine) EditReady(ctx context.Context) []string {
	if !e.client().Alive(ctx) {
		return []string{"本地 ComfyUI 未就绪"}
	}
	return e.EditAssets(ctx)
}

// EditAssets 多视角编辑链路的**权重体检**(与 MaskAssets / UpscaleAssets 同一原则):
// 缺件在点下去之前就说清,而不是等 ComfyUI 执行到那一节点抛原始报文。
// 引擎不可达时返回空。
func (e *Engine) EditAssets(ctx context.Context) []string {
	if !e.client().Alive(ctx) {
		return nil
	}
	c := e.cfg()
	// 家族按编辑底模判定:2.1 一个权重管生成与编辑,标签也跟着说清是哪一族。
	editFam := ResolveImageFamily("", c.EditUNet, c.EditCLIP)
	need := []struct {
		class, field, file, label, where string
	}{
		{"UNETLoader", "unet_name", c.EditUNet, "图像编辑底模(" + editFam.Label + ")", "diffusion_models"},
		{"CLIPLoader", "clip_name", c.EditCLIP, "图像编辑文本编码器(type=" + editFam.CLIPType + ")", "text_encoders"},
		{"VAELoader", "vae_name", c.EditVAE, "图像编辑 VAE", "vae"},
	}
	if strings.TrimSpace(c.EditTurboLoRA) != "" {
		need = append(need, struct{ class, field, file, label, where string }{
			"LoraLoaderModelOnly", "lora_name", c.EditTurboLoRA, "Lightning 4 步加速 LoRA", "loras"})
	}
	if strings.TrimSpace(c.EditAnglesLoRA) != "" {
		need = append(need, struct{ class, field, file, label, where string }{
			"LoraLoaderModelOnly", "lora_name", c.EditAnglesLoRA, "Multiple-Angles 转角度 LoRA", "loras"})
	}
	var missing []string
	for _, a := range need {
		if strings.TrimSpace(a.file) == "" {
			missing = append(missing, a.label+":未指定文件名")
			continue
		}
		list, ok := e.client().ComboList(ctx, a.class, a.field)
		if !ok {
			missing = append(missing, fmt.Sprintf("%s:读不到 %s 的可选值(节点缺失或输入已改名)", a.label, a.class))
			continue
		}
		found := false
		for _, v := range list {
			if v == a.file {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, fmt.Sprintf(
				"%s「%s」不在 ComfyUI 的模型目录里:请把该文件放进 models/%s/(放好后不必重启)",
				a.label, a.file, a.where))
		}
	}
	return missing
}
