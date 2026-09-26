package comfyengine

import "testing"

// TestESRGANScaleFromModelName 从权重名推放大倍数。
//
// 命名约定在社区权重里高度统一(倍数总与字母 x 相邻):4x-UltraSharp / RealESRGAN_x4plus /
// RealESRGAN_x2plus / 2x_ModernSpanimationV2 / HAT_SRx4。认不出来必须返回 0,
// 而不是猜一个 —— 猜错会让产物尺寸静默不对(见 upscaleDownScale)。
func TestESRGANScaleFromModelName(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"4x-UltraSharp.safetensors", 4},
		{"RealESRGAN_x4plus.pth", 4},
		{"RealESRGAN_x4plus_anime_6B.pth", 4},
		{"RealESRGAN_x2plus.pth", 2},
		{"2x_ModernSpanimationV2.pth", 2},
		{"HAT_SRx4.pth", 4},
		{"8x_NMKD-Superscale.pth", 8},
		{"sub/dir/4X-UltraSharp.safetensors", 4}, // 大小写与子目录都要认
		// 名字里没有倍数:必须返回 0(BSRGAN/remacri/NMKD-Superscale 这类真实存在)
		{"BSRGAN.pth", 0},
		{"remacri.pth", 0},
		{"NMKD-Superscale-SP_178000_G.pth", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := esrganScale(c.name); got != c.want {
			t.Errorf("esrganScale(%q) = %d,期望 %d", c.name, got, c.want)
		}
	}
}

// TestUpscaleGraphConvergesToExactly2x 2× 的承诺必须与权重倍数一致。
//
// 回归(2026-09-26 渲染管线审查,medium):ESRGAN 分支把 scale_by 写死 0.5,而 0.5
// **只对 4× 权重成立** —— 换成 2× 权重时 2× → ×0.5 = 原分辨率,用户拿到一个和源片
// 一样大的文件,界面却报「2× 放大完成」。
func TestUpscaleGraphConvergesToExactly2x(t *testing.T) {
	cases := []struct {
		model    string
		wantNode bool
		wantBy   float64
	}{
		{"4x-UltraSharp.safetensors", true, 0.5}, // 默认权重:4× → ×0.5 = 2×
		{"RealESRGAN_x2plus.pth", false, 0},      // 2× 权重:本来就对,再缩就白干
		{"RealESRGAN_x4plus.pth", true, 0.5},     // 4×
		{"8x_NMKD-Superscale.pth", true, 0.25},   // 8× → ×0.25 = 2×
		{"BSRGAN.pth", true, 0.5},                // 认不出:按历史行为当 4×(并写日志)
	}
	for _, c := range cases {
		g := buildUpscaleGraph("shot.mp4", UpscaleOptions{Type: "esrgan", ModelName: c.model}, 124, 24, "shot_x2")
		assertGraphContract(t, g)

		id, inputs := findNode(t, g, "ImageScaleBy")
		if !c.wantNode {
			if id != "" {
				t.Errorf("%s:2× 权重不该再挂 ImageScaleBy(挂了就是 2×→×0.5=原分辨率,界面却报放大完成)", c.model)
			}
			continue
		}
		if id == "" {
			t.Fatalf("%s:缺少 ImageScaleBy —— 超分倍数不是 2× 时必须收敛", c.model)
		}
		if got, _ := inputs["scale_by"].(float64); got != c.wantBy {
			t.Errorf("%s:scale_by = %v,期望 %v(目标是精确 2×)", c.model, inputs["scale_by"], c.wantBy)
		}
	}

	// 音轨仍必须来自源视频(收敛节点增减不得改动这条)
	g := buildUpscaleGraph("shot.mp4", UpscaleOptions{Type: "esrgan", ModelName: "RealESRGAN_x2plus.pth"}, 124, 24, "shot_x2")
	_, cv := findNode(t, g, "CreateVideo")
	audio, _ := cv["audio"].([]any)
	if len(audio) != 2 || audio[0] != "11" || audio[1] != 1 {
		t.Errorf("音轨应仍取源视频(节点 11 槽 1),实际 %v", cv["audio"])
	}
	// 2× 权重路径:图片链直连超分输出
	_, sc := findNode(t, g, "ImageUpscaleWithModel")
	if sc == nil {
		t.Fatal("缺少 ImageUpscaleWithModel")
	}
	images, _ := cv["images"].([]any)
	upID, _ := findNode(t, g, "ImageUpscaleWithModel")
	if len(images) != 2 || images[0] != upID {
		t.Errorf("2× 权重下画面应直接来自超分输出(%s),实际 %v", upID, cv["images"])
	}
}

// TestUpscaleDownScaleKnownFlag 认不出倍数时第二个返回值必须为 false —— 调用方靠它
// 写日志(否则"没放大但报成功"事后无从判断)。
func TestUpscaleDownScaleKnownFlag(t *testing.T) {
	if by, known := upscaleDownScale("4x-UltraSharp.safetensors"); !known || by != 0.5 {
		t.Errorf("4× 应认得出来且缩放 0.5,实际 by=%v known=%v", by, known)
	}
	if by, known := upscaleDownScale("BSRGAN.pth"); known || by != 0.5 {
		t.Errorf("认不出时应按 4× 兜底并如实返回 known=false,实际 by=%v known=%v", by, known)
	}
}
