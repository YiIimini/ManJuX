package comfyengine

import "testing"

// TestUpscaleGraphContract 放大后处理契约:两条链路的关键节点齐备、整图自洽,
// 且**音轨必须重混源视频** —— 放大只提升画面分辨率,声音原样复用,
// 一旦音轨接错就会引入重编码损失甚至音画错位。
func TestUpscaleGraphContract(t *testing.T) {
	// ① LTX 隐空间 2×
	g := buildUpscaleGraph("shot.mp4", UpscaleOptions{
		Type:      "ltx",
		ModelName: "ltx-2-spatial-upscaler-x2-1.0.safetensors",
		LTXVAE:    "ltx_vae.safetensors",
	}, 124, 24, "shot_upscaled")
	assertGraphContract(t, g)

	for _, cls := range []string{
		"LoadVideo", "GetVideoComponents", "VAELoader", "VAEEncodeTiled",
		"LatentUpscaleModelLoader", "LTXVLatentUpsampler", "VAEDecodeTiled",
		"CreateVideo", "SaveVideo",
	} {
		if id, _ := findNode(t, g, cls); id == "" {
			t.Errorf("LTX 放大链路缺少节点 %s", cls)
		}
	}

	// ② ESRGAN 帧级 2×
	g2 := buildUpscaleGraph("shot.mp4", UpscaleOptions{
		Type: "esrgan", ModelName: "4x-UltraSharp.safetensors",
	}, 124, 24, "shot_x2")
	assertGraphContract(t, g2)

	for _, cls := range []string{
		"UpscaleModelLoader", "ImageUpscaleWithModel", "ImageScaleBy",
		"CreateVideo", "SaveVideo",
	} {
		if id, _ := findNode(t, g2, cls); id == "" {
			t.Errorf("ESRGAN 链路缺少节点 %s", cls)
		}
	}

	// ③ 两条链路的音轨都必须来自源视频(GetVideoComponents 的输出槽 1)
	for name, graph := range map[string]map[string]any{"ltx": g, "esrgan": g2} {
		_, inputs := findNode(t, graph, "CreateVideo")
		if inputs == nil {
			t.Fatalf("%s: 缺少 CreateVideo", name)
		}
		audio, ok := inputs["audio"].([]any)
		if !ok || len(audio) != 2 || audio[0] != "11" || audio[1] != 1 {
			t.Errorf("%s: 音轨应重混源视频(节点 11 的输出槽 1),实际 %v", name, inputs["audio"])
		}
	}
}

// TestUpscalePad 时间轴对齐:补齐后的帧数必须满足 LTX 的 8n+1 布局,
// 否则放大在时间维上会错位(项目里 H3 侧是 17k+5,两套网格不同,不能混用)。
func TestUpscalePad(t *testing.T) {
	for _, f := range []int{5, 22, 39, 124, 141, 294} {
		padded := f + upscalePad(f)
		if (padded-1)%8 != 0 {
			t.Errorf("%d 帧补齐后应满足 8n+1,实际 %d", f, padded)
		}
		if padded < f {
			t.Errorf("%d 帧补齐后不应变少,实际 %d", f, padded)
		}
	}
	// 已经落在 8n+1 上时不应再补
	if pad := upscalePad(129); pad != 0 {
		t.Errorf("129 已在 8n+1 上,不应补帧,实际补 %d", pad)
	}
}

// TestUpscalePadLTXSeparateFromH3 H3 的 17k+5 与 LTX 的 8n+1 是两套网格,
// 这里明确记录一处易错点:两者不能互相套用。
func TestUpscalePadLTXSeparateFromH3(t *testing.T) {
	frames := 124 // H3 侧 5 秒对应的合法帧数(17*7+5)
	if (frames-5)%17 != 0 {
		t.Fatalf("H3 帧数应满足 17k+5,实际 %d", frames)
	}
	if (frames-1)%8 == 0 {
		return // 恰好同时满足两套网格,无需补帧
	}
	if pad := upscalePad(frames); pad == 0 {
		t.Errorf("H3 帧数 %d 未落在 8n+1 上,upscalePad 应给出非零补帧量", frames)
	}
}
