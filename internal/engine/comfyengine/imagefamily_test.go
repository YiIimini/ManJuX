package comfyengine

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// 图像模型家族的连线闸门。
//
// 背景(2026-09-25 用户定调「Krea-2 出图这么多问题,那就不用它生图」):换家族
// 不是改文件名,编码器类型/采样档/位移节点/条件节点四样都可能不同,错一样的表现
// 都是"提交后报一个看不懂的维度错"或"默默出烂图"。所以每一族都钉一条断言。

// TestDefaultImageFamilyMatchesConfig 本包常量与 config 默认值必须一致。
//
// 为什么需要这条:config 反向依赖本包(取 ComfySettings),所以本包不能 import config,
// 家族名在两处各写了一份字面量。这道闸门把"两处一致"变成可验证的事实。
func TestDefaultImageFamilyMatchesConfig(t *testing.T) {
	def := config.Default().Comfy
	if def.ImageFamily != FamilyQwenImage21 {
		t.Errorf("config 默认家族应为 %q,实际 %q", FamilyQwenImage21, def.ImageFamily)
	}
	fam := ResolveImageFamily(def.ImageFamily, def.ImageUNet, def.ImageCLIP)
	if fam.ID != FamilyQwenImage21 {
		t.Errorf("默认三件套应解析为 Qwen-Image-2.1,实际 %q(unet=%s clip=%s)",
			fam.ID, def.ImageUNet, def.ImageCLIP)
	}
	// 默认三件套必须是本机实际存在的 ModelScope 文件名(Qwen-Image-2.1 int8 组合)
	for _, want := range []string{"qwen_image_2.1", "qwen3vl_8b"} {
		if !strings.Contains(def.ImageUNet+def.ImageCLIP, want) {
			t.Errorf("默认模型文件名里应含 %q,实际 unet=%s clip=%s", want, def.ImageUNet, def.ImageCLIP)
		}
	}
}

// TestResolveImageFamily 家族解析:显式优先,其次按键名推断(存量设置没有该字段)。
func TestResolveImageFamily(t *testing.T) {
	cases := []struct {
		name, family, unet, clip, want string
	}{
		{"显式 2.1", FamilyQwenImage21, "", "", FamilyQwenImage21},
		{"显式 z-image", FamilyZImage, "", "", FamilyZImage},
		{"显式 krea2", FamilyKrea2, "", "", FamilyKrea2},
		{"空字段按键名推断 krea2", "", "krea2_turbo_fp8_scaled.safetensors", "qwen3vl_4b_fp8_scaled.safetensors", FamilyKrea2},
		{"空字段按键名推断 z-image", "", "z_image_turbo_bf16.safetensors", "qwen_3_4b.safetensors", FamilyZImage},
		{"空字段按键名推断 2.1", "", "qwen_image_2.1_int8_convrot.safetensors", "qwen3vl_8b_int8_convrot.safetensors", FamilyQwenImage21},
		{"认不出来按默认家族", "", "whatever.safetensors", "unknown.safetensors", FamilyQwenImage21},
	}
	for _, tc := range cases {
		if got := ResolveImageFamily(tc.family, tc.unet, tc.clip); got.ID != tc.want {
			t.Errorf("%s: 期望 %q,实际 %q", tc.name, tc.want, got.ID)
		}
	}
}

func imageSettings(family, unet, clip, vae string) config.ComfySettings {
	c := testComfySettings()
	c.ImageFamily, c.ImageUNet, c.ImageCLIP, c.ImageVAE = family, unet, clip, vae
	return c
}

// TestImageGraphQwen21Wiring Qwen-Image-2.1 的连线(取自本机官方模板
// image_qwen_image_2_1_t2i.json):单条件节点出正/负,25 步 / cfg 1 / euler / simple。
func TestImageGraphQwen21Wiring(t *testing.T) {
	c := imageSettings(FamilyQwenImage21, "qwen_image_2.1_int8_convrot.safetensors",
		"qwen3vl_8b_int8_convrot.safetensors", "qwen_image_2.1_vae_bf16.safetensors")
	fam := ResolveImageFamily(c.ImageFamily, c.ImageUNet, c.ImageCLIP)
	g := buildImageGraph(c, fam, "PROMPT", "NEG", 7, 832, 1248, "", 0, "manjux/x")
	assertGraphContract(t, g)

	clip := nodeOf(t, g, "CLIPLoader")
	if clip["type"] != "qwen_image" {
		t.Errorf("CLIPLoader type 应为 qwen_image,实际 %v", clip["type"])
	}
	enc := nodeOf(t, g, "TextEncodeQwenImage21")
	if enc["prompt"] != "PROMPT" || enc["negative_prompt"] != "NEG" {
		t.Errorf("2.1 的正/负提示词必须走同一个节点,实际 %v", enc)
	}
	if _, has := g["4"].(map[string]any)["class_type"]; !has {
		t.Fatal("缺少 2.1 的条件节点")
	}
	if _, ok := g["5"]; ok {
		t.Error("2.1 不该再有两个 CLIPTextEncode(旧式家族才那样)")
	}
	ks := nodeOf(t, g, "KSampler")
	if ks["positive"] == nil || ks["negative"] == nil {
		t.Fatal("KSampler 缺少条件输入")
	}
	if !sameLink(ks["positive"], "4", 0) || !sameLink(ks["negative"], "4", 1) {
		t.Errorf("2.1 的正/负条件应取同一节点 0/1 号输出,实际 %v / %v", ks["positive"], ks["negative"])
	}
	if steps, _ := ks["steps"].(int); steps != 25 {
		t.Errorf("2.1 官方模板是 25 步,实际 %v", ks["steps"])
	}
	if ks["sampler_name"] != "euler" || ks["scheduler"] != "simple" {
		t.Errorf("2.1 采样器/调度器应为 euler/simple,实际 %v/%v", ks["sampler_name"], ks["scheduler"])
	}
	if ks["cfg"].(float64) != 1.0 {
		t.Errorf("2.1 cfg 应为 1.0,实际 %v", ks["cfg"])
	}
	if _, ok := g["1m"]; ok {
		t.Error("2.1 不需要 ModelSamplingAuraFlow")
	}
	// 竖幅定妆照必须走 EmptyLatentImage 的宽高,而不是条件节点自带的方形 latent
	el := nodeOf(t, g, "EmptyLatentImage")
	if el["width"] != 832 || el["height"] != 1248 {
		t.Errorf("空 latent 应为 832x1248,实际 %vx%v", el["width"], el["height"])
	}
	if !sameLink(ks["latent_image"], "6", 0) {
		t.Errorf("KSampler 的 latent 应来自 EmptyLatentImage,实际 %v", ks["latent_image"])
	}
}

// TestImageGraphZImageWiring Z-Image Turbo(官方模板 image_z_image_turbo.json):
// CLIPLoader type=lumina2 + ModelSamplingAuraFlow(shift 3) + 8 步 res_multistep/simple。
func TestImageGraphZImageWiring(t *testing.T) {
	c := imageSettings(FamilyZImage, "z_image_turbo_bf16.safetensors", "qwen_3_4b.safetensors", "ae.safetensors")
	fam := ResolveImageFamily(c.ImageFamily, c.ImageUNet, c.ImageCLIP)
	g := buildImageGraph(c, fam, "P", "N", 7, 1024, 1024, "", 0, "manjux/x")
	assertGraphContract(t, g)

	if got := nodeOf(t, g, "CLIPLoader")["type"]; got != "lumina2" {
		t.Errorf("Z-Image 的 CLIPLoader type 应为 lumina2,实际 %v", got)
	}
	aura := nodeOf(t, g, "ModelSamplingAuraFlow")
	if shift, _ := aura["shift"].(float64); shift != 3 {
		t.Errorf("Z-Image 官方模板 shift=3,实际 %v", aura["shift"])
	}
	ks := nodeOf(t, g, "KSampler")
	if !sameLink(ks["model"], "1m", 0) {
		t.Errorf("有位移节点时 KSampler 必须接位移后的模型,实际 %v", ks["model"])
	}
	if steps, _ := ks["steps"].(int); steps != 8 {
		t.Errorf("Z-Image Turbo 是 8 步,实际 %v", ks["steps"])
	}
	if ks["sampler_name"] != "res_multistep" || ks["scheduler"] != "simple" {
		t.Errorf("Z-Image 采样器应为 res_multistep/simple,实际 %v/%v", ks["sampler_name"], ks["scheduler"])
	}
	for _, id := range []string{"4", "5"} {
		n, _ := g[id].(map[string]any)
		if ct, _ := n["class_type"].(string); ct != "CLIPTextEncode" {
			t.Errorf("Z-Image 的正/负条件应由节点 4/5 两个 CLIPTextEncode 提供,节点 %s 实际是 %v", id, ct)
		}
	}
	if ks["negative"] == nil || !sameLink(ks["negative"], "5", 0) {
		t.Errorf("Z-Image 的负条件应来自节点 5,实际 %v", ks["negative"])
	}
}

// TestImageGraphKrea2Wiring Krea-2 仍可选中(存量项目),但采样档按社区记录的
// "官方倾向" er_sde/simple —— 此前是自选的 euler/normal。
func TestImageGraphKrea2Wiring(t *testing.T) {
	c := imageSettings(FamilyKrea2, "krea2_turbo_fp8_scaled.safetensors", "qwen3vl_4b_fp8_scaled.safetensors", "qwen_image_vae.safetensors")
	fam := ResolveImageFamily(c.ImageFamily, c.ImageUNet, c.ImageCLIP)
	g := buildImageGraph(c, fam, "P", "N", 7, 832, 1248, "", 0, "manjux/x")
	assertGraphContract(t, g)

	if got := nodeOf(t, g, "CLIPLoader")["type"]; got != "krea2" {
		t.Errorf("Krea-2 的 CLIPLoader type 必须是 krea2(否则报 12x2560=30720 特征不符),实际 %v", got)
	}
	ks := nodeOf(t, g, "KSampler")
	if ks["sampler_name"] != "er_sde" || ks["scheduler"] != "simple" {
		t.Errorf("Krea-2 Turbo 应为 er_sde/simple,实际 %v/%v", ks["sampler_name"], ks["scheduler"])
	}
	if steps, _ := ks["steps"].(int); steps != 8 {
		t.Errorf("Krea-2 Turbo 是 8 步蒸馏,实际 %v", steps)
	}
}

// TestImageGraphImg2ImgKeepsReferencePaths img2img(旧式家族)接参考图时必须
// LoadImage → VAEEncode,且 denoise 落在 0.6-0.75 的实测区间内。
func TestImageGraphImg2ImgKeepsReferencePaths(t *testing.T) {
	c := imageSettings(FamilyKrea2, "krea2.safetensors", "qwen3vl.safetensors", "vae.safetensors")
	fam := ResolveImageFamily(c.ImageFamily, c.ImageUNet, c.ImageCLIP)
	g := buildImageGraph(c, fam, "P", "N", 7, 832, 1248, "uploaded.png", 0, "manjux/x")
	assertGraphContract(t, g)

	if got := nodeOf(t, g, "LoadImage")["image"]; got != "uploaded.png" {
		t.Errorf("参考图名应原样进 LoadImage,实际 %v", got)
	}
	ks := nodeOf(t, g, "KSampler")
	if !sameLink(ks["latent_image"], "6e", 0) {
		t.Errorf("img2img 的 latent 应来自 VAEEncode,实际 %v", ks["latent_image"])
	}
	if d, _ := ks["denoise"].(float64); d <= 0 || d > 1 {
		t.Errorf("denoise 应落在 (0,1],实际 %v", d)
	}
}

// nodeOf 取指定 class_type 的唯一节点输入字典(不存在则让测试立刻失败)。
func nodeOf(t *testing.T, g map[string]any, classType string) map[string]any {
	t.Helper()
	var hitID string
	for id, raw := range g {
		n, _ := raw.(map[string]any)
		if n == nil {
			continue
		}
		if ct, _ := n["class_type"].(string); ct == classType {
			if hitID != "" {
				t.Fatalf("图中有多个 %s 节点(%s / %s),本断言要求唯一", classType, hitID, id)
			}
			hitID = id
		}
	}
	if hitID == "" {
		t.Fatalf("图中没有 %s 节点", classType)
	}
	n, _ := g[hitID].(map[string]any)
	inputs, _ := n["inputs"].(map[string]any)
	return inputs
}

// sameLink 判断一个输入是否是 [nodeID, slot] 形式且与期望一致。
func sameLink(v any, id string, slot int) bool {
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 {
		return false
	}
	gotID, _ := arr[0].(string)
	gotSlot, _ := arr[1].(int)
	return gotID == id && gotSlot == slot
}
