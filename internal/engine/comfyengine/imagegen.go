package comfyengine

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
)

// GenerateImage 用配置里的图像模型家族生成定妆照/场景图/抽卡图。
//
// 家族差异(编码器类型、采样档、是否要额外的位移节点)全部由 imagefamily.go 的表决定,
// 这里只负责拼图 —— 换模型是改表,不是改这段代码。
//
// 关键(NiliX 实证,保留):Krea-2 的 CLIPLoader type 必须为 "krea2"(12 层 Qwen3-VL stack),
// 误用 qwen_image 会报 "Krea2 expects conditioning with 12x2560=30720 features"。
// Qwen-Image-2.1 用 type="qwen_image" + TextEncodeQwenImage21(单节点出正/负条件)。
// 返回下载到本地的图片绝对路径。
func (e *Engine) GenerateImage(ctx context.Context, prompt string, neg string, seed int64, w, h int, outPath string) (string, error) {
	return e.generateImage(ctx, prompt, neg, seed, w, h, "", 0, outPath)
}

// GenerateImageFrom 以参考图为起点生成图像(img2img)。
//
// 用途:产出同一角色的多视角图。**必须走 img2img,而不是"同一段描述换个角度词重新
// 文生图"** —— 后者同 seed 换 prompt 仍会换脸,多视图就失去意义。denoise 建议
// 0.6-0.75:太低角度几乎不变,太高身份开始漂移(0.7 已实测:视角成功转为三分之四
// 侧视,而雀斑/发型/服装等身份特征完整保留)。
//
// 注意:目前**没有调用方**(多视角已改用 Qwen-Image-Edit 编辑路线,见 imageedit.go),
// 保留是给"同一角色换姿势/换背景"这类后续需求留的入口。它只支持老式
// CLIPTextEncode 家族(Krea-2/Z-Image);Qwen-Image-2.1 的参考图应当走编辑路径。
//
// refImagePath 传**本地绝对路径**:内部会自动上传到 ComfyUI 的 input 目录,
// 调用方不必关心服务器端的文件名。
func (e *Engine) GenerateImageFrom(ctx context.Context, refImagePath, prompt, neg string, seed int64, w, h int, denoise float64, outPath string) (string, error) {
	name, err := e.client().UploadImage(ctx, refImagePath)
	if err != nil {
		return "", fmt.Errorf("上传参考图失败(img2img 需要它作为起点): %w", err)
	}
	return e.generateImage(ctx, prompt, neg, seed, w, h, name, denoise, outPath)
}

func (e *Engine) generateImage(ctx context.Context, prompt, neg string, seed int64, w, h int, refImage string, denoise float64, outPath string) (string, error) {
	touchBusy()
	c := e.cfg()
	if err := e.Available(ctx); err != nil {
		return "", err
	}
	if c.ImageUNet == "" || c.ImageCLIP == "" || c.ImageVAE == "" {
		return "", fmt.Errorf("定妆照模型未配置:请在设置中填写 ComfyUI 的图像模型(UNet/CLIP/VAE)")
	}
	fam := ResolveImageFamily(c.ImageFamily, c.ImageUNet, c.ImageCLIP)
	// 画幅对齐 32(潜空间要求)
	w, h = snap32(w), snap32(h)

	g := buildImageGraph(c, fam, prompt, neg, seed, w, h, refImage, denoise, "manjux/"+baseName(outPath))

	// 定妆图生成无单镜作业可依,给一个比整镜渲染短得多的兜底上限(10 分钟)。
	// Qwen-Image-2.1 是 25 步 7B(比 Krea-2 Turbo 的 8 步慢),本机实测仍在 1 分钟内,
	// 10 分钟足够发现卡死又不至于白等。
	hist, err := e.submitAndWait(ctx, g, defaultImageTimeout, nil)
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
		dlErr := e.client().Download(dlCtx, files[i], dest)
		cancel()
		if dlErr == nil {
			return dest, nil
		}
	}
	return "", fmt.Errorf("图像生成完成但下载失败")
}

// buildImageGraph 是**纯函数**:图怎么拼只取决于参数,不碰网络 ——
// 这样"每个家族的连线对不对"可以在单元测试里直接断言(见 imagefamily_test.go),
// 而不是等真机提交时才被 ComfyUI 的 node_errors 打回来。
//
// 家族差异(编码器类型 / 采样档 / 位移节点 / 条件节点)全部来自 imagefamily.go 的表。
// refImage 非空时走 img2img(LoadImage → VAEEncode)。
func buildImageGraph(c config.ComfySettings, fam ImageFamily, prompt, neg string, seed int64, w, h int, refImage string, denoise float64, prefix string) map[string]any {
	g := map[string]any{
		"1": nd("UNETLoader", map[string]any{"unet_name": c.ImageUNet, "weight_dtype": "default"}),
		"2": nd("CLIPLoader", map[string]any{"clip_name": c.ImageCLIP, "type": fam.CLIPType, "device": "default"}),
		"3": nd("VAELoader", map[string]any{"vae_name": c.ImageVAE}),
	}

	// 条件:2.1 用单节点(同时给出 positive/negative),其余家族用两个 CLIPTextEncode。
	posLink, negLink := link("4", 0), link("5", 0)
	if fam.Encode21 {
		g["4"] = nd("TextEncodeQwenImage21", map[string]any{
			"clip": link("2", 0), "prompt": prompt, "negative_prompt": neg,
			// resolution 只影响参考图缩放;本路径不喂参考图,画幅由下面的 EmptyLatentImage 决定。
			"resolution": 1024,
		})
		posLink, negLink = link("4", 0), link("4", 1)
	} else {
		g["4"] = nd("CLIPTextEncode", map[string]any{"clip": link("2", 0), "text": prompt})
		g["5"] = nd("CLIPTextEncode", map[string]any{"clip": link("2", 0), "text": neg})
	}

	// 模型入口:有的家族要先过位移节点(Z-Image 官方模板 shift=3)
	modelLink := link("1", 0)
	if fam.AuraShift > 0 {
		g["1m"] = nd("ModelSamplingAuraFlow", map[string]any{"model": link("1", 0), "shift": fam.AuraShift})
		modelLink = link("1m", 0)
	}

	// 潜空间来源。注意**不使用** TextEncodeQwenImage21 自带的 latent 输出:它只按
	// resolution 出正方形,而定妆照是 832x1248 竖幅。官方 2.1 模板同样用
	// EmptyLatentImage(4 通道 /8)喂采样器,由采样器按模型自身的 latent 规格接管。
	latentFrom := "6"
	if refImage != "" {
		g["6"] = nd("LoadImage", map[string]any{"image": refImage})
		g["6e"] = nd("VAEEncode", map[string]any{"pixels": link("6", 0), "vae": link("3", 0)})
		latentFrom = "6e"
		if denoise <= 0 {
			denoise = 0.7
		}
	} else {
		g["6"] = nd("EmptyLatentImage", map[string]any{"width": w, "height": h, "batch_size": 1})
		denoise = 1.0
	}
	g["7"] = nd("KSampler", map[string]any{
		"model": modelLink, "positive": posLink, "negative": negLink,
		"latent_image": link(latentFrom, 0), "seed": seed,
		"steps": fam.Steps, "cfg": fam.CFG, "sampler_name": fam.Sampler,
		"scheduler": fam.Scheduler, "denoise": denoise,
	})
	g["8"] = nd("VAEDecode", map[string]any{"samples": link("7", 0), "vae": link("3", 0)})
	g["9"] = nd("SaveImage", map[string]any{"images": link("8", 0), "filename_prefix": prefix})
	return g
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
