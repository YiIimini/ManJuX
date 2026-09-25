package comfyengine

import "strings"

// imagefamily.go 图像生成(定妆照/场景/Q版/抽卡)的**模型家族**表。
//
// 【为什么要有这一层】
// 2026-09-25 用户报「角色渲染漂移」并定调「Krea-2 出图这么多问题,那就不用它生图」。
// 换成任何一个新模型都不是"改个文件名"那么简单:每个家族连的是**不同的文本编码器类型、
// 不同的采样档、有的还要额外的位移节点**。把这套差异散在 graph 拼接里,下一次换模型
// 又是一次"改一处漏一处"。所以收成一张表:一族一行,装配只读表。
//
// 【选型依据(2026-09-25 全网核对,不是拍脑袋)】
//
//	候选          文本遵循/中文   生成+编辑    显存        结论
//	Krea-2 Turbo  编码器会改写   否(仅风格参考) 12B fp8    退役:卡片要求的身份记号
//	              指令(社区已知                              (眉尾疤/铜扣刻字)出不来,
//	              的对齐问题)                                且会"美颜"抹平文字描述
//	Z-Image Turbo 文字可读,偏    否            6B          保留为本地兜底(权重已在机器上)
//	              "审美平滑"、简化背景
//	Qwen-Image-2.1 生成+编辑同一   是            7B int8     首选:同一权重既出定妆照
//	              个权重,中文文字                            也做编辑,资产链只对一个家族
//	              精度最高
//
// 出处:
//
//	ComfyUI 官方博客「Qwen-Image-2.1 in ComfyUI」(7B / 原生 2K / RGBA / 最多 10 张参考图 /
//	  生成与编辑同一权重)  https://blog.comfy.org/p/qwen-image-21-in-comfyui-open-weight
//	Qwen-Image-2512 vs Z-Image Turbo 五提示词基准(指令遵循:Qwen 更"字面";文字精度:
//	  Qwen 能拼对指定字符串与标点,Z-Image 更偏审美平滑)
//	  https://qwen-image-2512.com/blog/qwen-image-2512-vs-turbo-benchmark
//	Krea-2 变体/设置/编码器说明(Qwen3-VL 对齐版会"改写提示词",社区改用 abliterated 编码器;
//	  Turbo 官方倾向 er_sde/simple) 社区参考
//	  https://github.com/charliecpeterson/comfyui_mcp/blob/main/skills/prompt-krea2/references/model-variants.md
//	官方工作流模板(本机 ComfyUI 自带,是各家连线的**权威**):
//	  comfyui_workflow_templates_json/templates/image_qwen_image_2_1_t2i.json
//	  comfyui_workflow_templates_json/templates/image_z_image_turbo.json
//	  comfyui_workflow_templates_json/templates/image_krea2_turbo_t2i.json

// ImageFamily 一个图像模型家族的装配参数。
type ImageFamily struct {
	ID    string // 配置里的 image_family 取值
	Label string // 日志/界面用的中文名

	// Source 该家族生成的资产写进 project.Asset.Source / 资产库时的**来源 key**。
	// 资产库与前端按它显示这张图是谁生成的,所以它必须随家族走、不能写死 ——
	// 2026-09-25 换模型时,写入端那四处字面量 "krea2" 一夜之间变成假话,就是漏了这一层。
	// 取值同时登记在 project/source_label.go 的词汇表里(有闸门 TestFamilySourcesHaveLabels)。
	Source string

	// CLIPType 是 CLIPLoader 的 type 参数。**错一个就报特征维度不符**
	// (Krea-2 误用 qwen_image 会报 "Krea2 expects conditioning with 12x2560=30720 features")。
	CLIPType string

	Steps     int
	CFG       float64
	Sampler   string
	Scheduler string

	// AuraShift > 0 时插入 ModelSamplingAuraFlow(官方模板给 Z-Image 用 shift=3)。
	AuraShift float64

	// Encode21 为真时用 TextEncodeQwenImage21 单节点产出正/负条件
	// (输出 0=positive 1=negative 2=latent);否则用两个 CLIPTextEncode。
	Encode21 bool
}

// 家族常量(config.image_family 的合法取值)。
const (
	FamilyQwenImage21 = "qwen_image_2_1"
	FamilyZImage      = "z_image"
	FamilyKrea2       = "krea2"
)

// 各家族参数 —— 数值全部取自本机 ComfyUI 自带的官方模板,不要凭印象改。
var (
	// Qwen-Image-2.1:UNETLoader + CLIPLoader(type=qwen_image) + VAELoader +
	// TextEncodeQwenImage21 + KSampler(25 步 / cfg 1 / euler / simple)。
	famQwenImage21 = ImageFamily{
		ID: FamilyQwenImage21, Label: "Qwen-Image-2.1", Source: "qwen-image-2.1",
		CLIPType: "qwen_image",
		Steps:    25, CFG: 1.0, Sampler: "euler", Scheduler: "simple",
		Encode21: true,
	}
	// Z-Image Turbo:CLIPLoader(type=lumina2) + ModelSamplingAuraFlow(shift=3) +
	// KSampler(8 步 / cfg 1 / res_multistep / simple)。本地权重已就位,做兜底。
	famZImage = ImageFamily{
		ID: FamilyZImage, Label: "Z-Image Turbo", Source: "z-image",
		CLIPType: "lumina2",
		Steps:    8, CFG: 1.0, Sampler: "res_multistep", Scheduler: "simple",
		AuraShift: 3,
	}
	// Krea-2 Turbo:CLIPLoader(type=krea2) + KSampler(8 步 / cfg 1)。
	// 采样器按社区记录的"官方倾向"用 er_sde/simple(此前是 euler/normal,属自选)。
	famKrea2 = ImageFamily{
		ID: FamilyKrea2, Label: "Krea-2 Turbo(已退役,仅为兼容存量设置保留)", Source: "krea2",
		CLIPType: "krea2",
		Steps:    8, CFG: 1.0, Sampler: "er_sde", Scheduler: "simple",
	}
)

// ResolveImageFamily 由配置解析出家族。
//
// family 为空时**按键名推断**:存量 settings.json 里没有 image_family 字段,但
// image_unet 的文件名能说明它是什么家族 —— 这样老配置不会因为新字段缺失而走错连线。
func ResolveImageFamily(family, unet, clip string) ImageFamily {
	switch strings.ToLower(strings.TrimSpace(family)) {
	case FamilyQwenImage21, "qwen_image_21", "qwen_image_2.1":
		return famQwenImage21
	case FamilyZImage, "z-image", "zimage":
		return famZImage
	case FamilyKrea2:
		return famKrea2
	}
	name := strings.ToLower(unet + " " + clip)
	switch {
	case strings.Contains(name, "z_image") || strings.Contains(name, "z-image"):
		return famZImage
	case strings.Contains(name, "qwen_image_2.1") || strings.Contains(name, "qwen_image_2_1"):
		return famQwenImage21
	case strings.Contains(name, "krea"):
		return famKrea2
	case strings.Contains(name, "qwen_image"):
		// 旧版 Qwen-Image / Qwen-Image-Edit 权重:文本编码器类型同为 qwen_image,
		// 采样档按 2.x 之外的通用档(20 步)走 —— 宁可慢一点也不猜采样器。
		return ImageFamily{
			ID: "qwen_image", Label: "Qwen-Image(旧权重)", Source: "qwen-image",
			CLIPType: "qwen_image",
			Steps:    20, CFG: 2.5, Sampler: "euler", Scheduler: "simple",
		}
	}
	// 完全认不出来:按 Qwen-Image-2.1 的连线走(它是当前默认家族),并在日志里可见。
	return famQwenImage21
}

// KnownImageFamilies 供设置界面列出可选项。
func KnownImageFamilies() []ImageFamily {
	return []ImageFamily{famQwenImage21, famZImage, famKrea2}
}
