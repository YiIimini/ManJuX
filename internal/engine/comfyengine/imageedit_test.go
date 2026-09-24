package comfyengine

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// 图像编辑(Qwen-Image-Edit-2511)多视角链路的工作流契约测试。
//
// 这一层测的不是"模型好不好看",而是**图本身对不对**:官方模板里每一处接线失误
// 都以"渲染时报 node_errors / 出一个看起来正常但语义错误的图"收场,而后者更危险 ——
// 编辑路径的产物会作为角色的第二张参考图喂进渲染,接错了没人会立刻发现。
//
// 对齐 ComfyUI 官方模板 image_qwen_image_edit_2511.json:
// ModelSamplingAuraFlow shift=3.1;Lightning 4 步档 = steps 4 / cfg 1.0 / euler+simple;
// 编辑的 latent 来自**参考图**的 VAEEncode(在它的 latent 上重绘)。

func editTestSettings() config.ComfySettings {
	return config.ComfySettings{
		EditUNet:       "qwen_image_edit_2511_fp8mixed.safetensors",
		EditCLIP:       "qwen_2.5_vl_7b_fp8_scaled.safetensors",
		EditVAE:        "qwen_image_vae.safetensors",
		EditTurboLoRA:  "Qwen-Image-Edit-2511-Lightning-4steps-V1.0-bf16.safetensors",
		EditAnglesLoRA: "qwen-image-edit-2511-multiple-angles-lora.safetensors",
	}
}

// TestEditGraphContract 单参考图 + 双 LoRA(生产默认组合)的图必须自洽且接线正确。
func TestEditGraphContract(t *testing.T) {
	c := editTestSettings()
	g := buildEditGraph(c, ImageEditOptions{
		RefImages: []string{"master.png"},
		Prompt:    "show the character from a three-quarter view",
		Negative:  "another character",
		Seed:      7,
		UseTurbo:  true,
		UseAngles: true,
	}, []string{"up_master.png"})
	assertGraphContract(t, g)

	for _, cls := range []string{
		"CLIPLoader", "VAELoader", "UNETLoader", "ModelSamplingAuraFlow",
		"LoraLoaderModelOnly", "LoadImage", "FluxKontextImageScale",
		"TextEncodeQwenImageEditPlus", "VAEEncode", "KSampler", "VAEDecode", "SaveImage",
	} {
		if id, _ := findNode(t, g, cls); id == "" {
			t.Errorf("编辑链路缺少节点 %s", cls)
		}
	}

	// ① CLIPLoader 的类型必须是 qwen_image:填错(flux/sd3)会以"文本编码器与底模不匹配"
	//    在执行时炸掉,而这一步纯粹是配置问题,应当在这里就锁住。
	_, clip := findNode(t, g, "CLIPLoader")
	if clip["type"] != "qwen_image" {
		t.Errorf("CLIPLoader.type 应为 qwen_image,实际 %v", clip["type"])
	}
	if clip["clip_name"] != c.EditCLIP {
		t.Errorf("CLIPLoader.clip_name 应为 %q,实际 %v", c.EditCLIP, clip["clip_name"])
	}

	// ② shift 3.1 是官方模板固定值,漂了会改变模型的时间步分布(画面发灰/过曝)。
	_, aura := findNode(t, g, "ModelSamplingAuraFlow")
	if shift, _ := aura["shift"].(float64); shift != 3.1 {
		t.Errorf("ModelSamplingAuraFlow.shift 应为 3.1,实际 %v", aura["shift"])
	}

	// ③ 两条 LoRA 必须都挂在**同一条模型链**上,且顺序为 加速 → 转角度:
	//    Multiple-Angles 是给"转视角"训的,叠在加速之后(官方示例的叠法)。
	var loras []map[string]any
	for _, raw := range g {
		node, ok := raw.(map[string]any)
		if !ok || node["class_type"] != "LoraLoaderModelOnly" {
			continue
		}
		inputs, _ := node["inputs"].(map[string]any)
		loras = append(loras, inputs)
	}
	if len(loras) != 2 {
		t.Fatalf("同时开加速与转角度时应有 2 个 LoraLoaderModelOnly,实际 %d", len(loras))
	}
	names := map[any]map[string]any{}
	for _, l := range loras {
		names[l["lora_name"]] = l
	}
	if names[c.EditTurboLoRA] == nil || names[c.EditAnglesLoRA] == nil {
		t.Errorf("两条 LoRA 的文件名应与配置一致,实际 %v", names)
	}
	// 转角度那条应吃加速那条的输出(链式),而不是各自从 UNETLoader 取模型。
	angles := names[c.EditAnglesLoRA]
	if src, ok := angles["model"].([]any); !ok || src[0] == "3" {
		t.Errorf("Multiple-Angles LoRA 应接在加速 LoRA 之后(链式),实际 model=%v", angles["model"])
	}

	// ④ 采样档:Lightning 4 步 = steps 4 / cfg 1.0,采样器与调度器固定 euler+simple。
	_, ks := findNode(t, g, "KSampler")
	if steps, _ := ks["steps"].(int); steps != 4 {
		t.Errorf("启用 Lightning 时 steps 应为 4,实际 %v", ks["steps"])
	}
	if cfg, _ := ks["cfg"].(float64); cfg != 1.0 {
		t.Errorf("4 步蒸馏必须 CFG 1.0,实际 %v", ks["cfg"])
	}
	if ks["sampler_name"] != "euler" || ks["scheduler"] != "simple" {
		t.Errorf("采样器/调度器应为 euler+simple,实际 %v + %v", ks["sampler_name"], ks["scheduler"])
	}

	// ⑤ 编辑的 latent 必须来自**参考图**的 VAEEncode(编辑 = 在它的 latent 上重绘)。
	//    若误接到空 latent,就退化成"纯文生另一张图",身份全靠提示词 —— 这条路就白走了。
	_, venc := findNode(t, g, "VAEEncode")
	scaleID, _ := findNode(t, g, "FluxKontextImageScale")
	if src, ok := venc["pixels"].([]any); !ok || src[0] != scaleID {
		t.Errorf("VAEEncode 应吃 FluxKontextImageScale 的输出(节点 %s),实际 %v", scaleID, venc["pixels"])
	}

	// ⑥ 正负两组条件都必须带 image1(编辑模型靠它"看见"要改的图)。
	condCount := 0
	for _, raw := range g {
		node, ok := raw.(map[string]any)
		if !ok || node["class_type"] != "TextEncodeQwenImageEditPlus" {
			continue
		}
		inputs, _ := node["inputs"].(map[string]any)
		if _, ok := inputs["image1"]; !ok {
			t.Errorf("TextEncodeQwenImageEditPlus 缺 image1:%v", inputs)
		}
		condCount++
	}
	if condCount != 2 {
		t.Errorf("应有正负两组 TextEncodeQwenImageEditPlus,实际 %d", condCount)
	}

	// ⑦ 产物必须落盘到 SaveImage(引擎按它取结果图)。
	_, save := findNode(t, g, "SaveImage")
	if _, ok := save["filename_prefix"].(string); !ok {
		t.Error("SaveImage 缺少 filename_prefix")
	}
}

// TestEditGraphNoTurboFallsBackToTemplateSteps 不挂加速 LoRA 时必须回到官方模板档
// (20 步 / CFG 4.0)。用 4 步档跑非蒸馏模型会糊成一团,反之用 20 步跑蒸馏模型会过曝 ——
// 两档不能互相串。
func TestEditGraphNoTurboFallsBackToTemplateSteps(t *testing.T) {
	c := editTestSettings()
	g := buildEditGraph(c, ImageEditOptions{
		RefImages: []string{"m.png"},
		Prompt:    "x",
	}, []string{"up_m.png"})
	assertGraphContract(t, g)

	if _, l := findNode(t, g, "LoraLoaderModelOnly"); l != nil {
		t.Error("未启用加速/转角度时不应挂任何 LoRA 节点")
	}
	_, ks := findNode(t, g, "KSampler")
	if steps, _ := ks["steps"].(int); steps != 20 {
		t.Errorf("非蒸馏档 steps 应为 20(官方模板值),实际 %v", ks["steps"])
	}
	if cfg, _ := ks["cfg"].(float64); cfg != 4.0 {
		t.Errorf("非蒸馏档 cfg 应为 4.0(官方模板值),实际 %v", ks["cfg"])
	}
	// 模型链应直接来自 ModelSamplingAuraFlow。
	auraID, _ := findNode(t, g, "ModelSamplingAuraFlow")
	if src, ok := ks["model"].([]any); !ok || src[0] != auraID {
		t.Errorf("无 LoRA 时 KSampler.model 应直连 ModelSamplingAuraFlow(%s),实际 %v", auraID, ks["model"])
	}
}

// TestEditGraphUpToThreeRefs 2511 支持 image1..image3 三张参考图:第 2、3 张必须
// 各自带上 LoadImage(不缩放 —— 缩放只对第一张做,它是 Kontext 尺寸对齐的那张)。
func TestEditGraphUpToThreeRefs(t *testing.T) {
	c := editTestSettings()
	g := buildEditGraph(c, ImageEditOptions{
		RefImages: []string{"a.png", "b.png", "c.png"},
		Prompt:    "x",
	}, []string{"up_a.png", "up_b.png", "up_c.png"})
	assertGraphContract(t, g)

	n := 0
	for _, raw := range g {
		node, _ := raw.(map[string]any)
		if node != nil && node["class_type"] == "LoadImage" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("3 张参考图应有 3 个 LoadImage,实际 %d", n)
	}
	// 第一张走缩放,第 2/3 张原样进条件。
	for _, raw := range g {
		node, ok := raw.(map[string]any)
		if !ok || node["class_type"] != "TextEncodeQwenImageEditPlus" {
			continue
		}
		inputs, _ := node["inputs"].(map[string]any)
		for _, k := range []string{"image1", "image2", "image3"} {
			if _, ok := inputs[k]; !ok {
				t.Errorf("三图条件下缺少 %s:%v", k, inputs)
			}
		}
	}
}

// TestEditGraphSingleRefKeepsCondShape 单参考图时条件里**不应**出现 image2/image3 ——
// 给一个不存在的槽位会让工作流校验直接失败(ComfyUI 对未连接的 required 输入报错)。
func TestEditGraphSingleRefKeepsCondShape(t *testing.T) {
	g := buildEditGraph(editTestSettings(), ImageEditOptions{
		RefImages: []string{"a.png"},
		Prompt:    "x",
	}, []string{"up_a.png"})
	for _, raw := range g {
		node, ok := raw.(map[string]any)
		if !ok || node["class_type"] != "TextEncodeQwenImageEditPlus" {
			continue
		}
		inputs, _ := node["inputs"].(map[string]any)
		for _, k := range []string{"image2", "image3"} {
			if _, ok := inputs[k]; ok {
				t.Errorf("单参考图不应出现 %s:%v", k, inputs)
			}
		}
	}
}
