package engine

import (
	"strings"
	"testing"
)

// TestUpscaleOptionsFor 放大默认参数必须**按方式**成套。
//
// 这条锁的是一个实测踩到的真 bug:界面选「隐空间」时只换了 Type,ModelName 仍是
// 帧级超分的 4x-UltraSharp —— 而 ComfyUI 会拿这个名字去 latent_upscale_models/
// 里找,必然失败。默认值必须让"方式 + 权重 + VAE"三者同时成立。
func TestUpscaleOptionsFor(t *testing.T) {
	for _, name := range []string{
		DefaultESRGANUpscaleModel, DefaultLTXUpscaleModel, DefaultLTXUpscaleVAE,
	} {
		if strings.TrimSpace(name) == "" {
			t.Fatalf("默认权重/VAE 文件名不能为空(空值等于把文件名重新推给用户手打)")
		}
	}

	// 两种方式的默认权重必须不同:同一个名字不可能既在 upscale_models/ 又在
	// latent_upscale_models/ 里,共用就说明有一条路注定找不到文件。
	if DefaultESRGANUpscaleModel == DefaultLTXUpscaleModel {
		t.Error("帧级与隐空间的默认权重名不应相同 —— 它们分属两个不同的模型目录")
	}

	cases := []struct {
		kind      string
		wantType  string
		wantModel string
		wantVAE   string
	}{
		{kind: "esrgan", wantType: "esrgan", wantModel: DefaultESRGANUpscaleModel, wantVAE: ""},
		{kind: "ltx", wantType: "ltx", wantModel: DefaultLTXUpscaleModel, wantVAE: DefaultLTXUpscaleVAE},
		// 未知/空方式保守回落到默认方式,而不是产出一份半成品参数
		{kind: "", wantType: "esrgan", wantModel: DefaultESRGANUpscaleModel, wantVAE: ""},
		{kind: "bicubic", wantType: "esrgan", wantModel: DefaultESRGANUpscaleModel, wantVAE: ""},
	}

	for _, c := range cases {
		got := UpscaleOptionsFor(c.kind)
		if got.Type != c.wantType {
			t.Errorf("UpscaleOptionsFor(%q).Type want %q got %q", c.kind, c.wantType, got.Type)
		}
		if got.ModelName != c.wantModel {
			t.Errorf("UpscaleOptionsFor(%q).ModelName want %q got %q", c.kind, c.wantModel, got.ModelName)
		}
		if got.LTXVAE != c.wantVAE {
			t.Errorf("UpscaleOptionsFor(%q).LTXVAE want %q got %q", c.kind, c.wantVAE, got.LTXVAE)
		}
	}
}

// TestDefaultUpscaleOptions 默认走帧级 esrgan。
//
// 理由:esrgan 只依赖一个 upscale_models 权重,不必额外备齐 LTX 那套 2.3GB 视频
// VAE,速度也更快,对已定稿的成片更保险。改成 ltx 会让新装机器一上手就要求
// 一个可能没装的大文件。
func TestDefaultUpscaleOptions(t *testing.T) {
	d := DefaultUpscaleOptions()
	if d.Type != "esrgan" {
		t.Errorf("默认放大方式应为 esrgan(只依赖一个权重,不额外要 LTX VAE),实际 %q", d.Type)
	}
	if d.ModelName != DefaultESRGANUpscaleModel {
		t.Errorf("默认模型名应为 %q,实际 %q", DefaultESRGANUpscaleModel, d.ModelName)
	}
}
