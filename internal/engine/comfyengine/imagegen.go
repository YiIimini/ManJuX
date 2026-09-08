package comfyengine

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// GenerateImage 用 Krea-2 Turbo 生成定妆照/场景图(8 步蒸馏,CFG 1.0)。
// 关键(NiliX 实证):CLIPLoader type 必须为 "krea2"(12 层 Qwen3-VL stack,
// 误用 qwen_image 会报 "Krea2 expects conditioning with 12x2560=30720 features")。
// 返回下载到本地的图片绝对路径。
func (e *Engine) GenerateImage(ctx context.Context, prompt string, neg string, seed int64, w, h int, outPath string) (string, error) {
	touchBusy()
	c := e.cfg()
	if err := e.Available(ctx); err != nil {
		return "", err
	}
	if c.ImageUNet == "" || c.ImageCLIP == "" || c.ImageVAE == "" {
		return "", fmt.Errorf("定妆照模型未配置:请在设置中填写 ComfyUI 的图像模型(UNet/CLIP/VAE)")
	}
	// 画幅对齐 32(Krea 潜空间要求)
	w, h = snap32(w), snap32(h)

	g := map[string]any{
		"1": nd("UNETLoader", map[string]any{"unet_name": c.ImageUNet, "weight_dtype": "default"}),
		"2": nd("CLIPLoader", map[string]any{"clip_name": c.ImageCLIP, "type": "krea2"}),
		"3": nd("VAELoader", map[string]any{"vae_name": c.ImageVAE}),
		"4": nd("CLIPTextEncode", map[string]any{"clip": link("2", 0), "text": prompt}),
		"5": nd("CLIPTextEncode", map[string]any{"clip": link("2", 0), "text": neg}),
		"6": nd("EmptySD3LatentImage", map[string]any{"width": w, "height": h, "batch_size": 1}),
		"7": nd("KSampler", map[string]any{
			"model": link("1", 0), "positive": link("4", 0), "negative": link("5", 0),
			"latent_image": link("6", 0), "seed": seed,
			"steps": 8, "cfg": 1.0, "sampler_name": "euler", "scheduler": "normal", "denoise": 1.0,
		}),
		"8":  nd("VAEDecode", map[string]any{"samples": link("7", 0), "vae": link("3", 0)}),
		"9":  nd("SaveImage", map[string]any{"images": link("8", 0), "filename_prefix": "manjux/asset_" + baseName(outPath)}),
	}

	hist, err := e.submitAndWait(ctx, g, nil)
	if err != nil {
		return "", err
	}
	files := hist.ExtractFiles()
	for i := range files {
		if files[i].Animated {
			continue
		}
		dlCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		dest := withExt(outPath, filepath.Ext(files[i].Filename))
		dlErr := e.cli.Download(dlCtx, files[i], dest)
		cancel()
		if dlErr == nil {
			return dest, nil
		}
	}
	return "", fmt.Errorf("图像生成完成但下载失败")
}

func snap32(v int) int {
	if v < 32 {
		return 32
	}
	return v / 32 * 32
}

func baseName(p string) string {
	b := filepath.Base(p)
	for j := len(b) - 1; j >= 0; j-- {
		if b[j] == '.' {
			return b[:j]
		}
	}
	return b
}

func withExt(p, ext string) string {
	if ext == "" {
		return p + ".png"
	}
	return p[:len(p)-len(filepath.Ext(p))] + ext
}
