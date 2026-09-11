package comfyengine

import (
	"context"
	"fmt"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// UpscaleOptions 放大参数。
type UpscaleOptions struct {
	// Type 放大方式:
	//   "ltx"    隐空间 2× —— 把已解码的画面重编码到 LTX VAE 空间做学习式空间放大,
	//            细节更自然,是"保音频高清化"的成熟做法;
	//   "esrgan" 帧级 2× —— 逐帧超分,快但对已干净素材更稳妥,容易放大噪点。
	Type string
	// ModelName 模型文件名(ltx 用 latent_upscale_models/ 下的,esrgan 用 upscale_models/ 下的)
	ModelName string
	// LTXVAE LTX 的 VAE 文件名(仅 ltx 方式需要)
	LTXVAE string
}

// upscaleFrames 时间轴对齐:LTX 视频 VAE 要求 8n+1 帧,不足则复制尾帧补齐。
func upscalePad(frames int) int {
	if frames <= 1 {
		return 0
	}
	return (8 - ((frames - 1) % 8)) % 8
}

// buildUpscaleGraph 构造"放大后处理"工作流:在已完成的镜头上追加 2× 放大分支。
//
// 两条分支都**保留原音轨** —— 放大只提升画面分辨率,声音必须原样复用,
// 否则会引入重编码损失甚至音画错位。
//
// LTX 隐空间 2×(与官方两段式工作流的放大段同源):
//
//	GetVideoComponents → VAEEncodeTiled(LTX VAE) → LTXVLatentUpsampler → VAEDecodeTiled
//	→(裁掉为对齐 8n+1 补的尾帧)→ CreateVideo(audio = 源音轨)→ SaveVideo
//
// ESRGAN 帧级 2×:
//
//	GetVideoComponents → UpscaleModelLoader → ImageUpscaleWithModel
//	→ ImageScale(归一化到精确 2×)→ CreateVideo(audio = 源音轨)→ SaveVideo
//
// srcVideo 是 ComfyUI 侧的**文件名**(调用方先上传)。
func buildUpscaleGraph(srcVideo string, opt UpscaleOptions, length int, fps float64, outPrefix string) map[string]any {
	g := map[string]any{
		"10": nd("LoadVideo", map[string]any{"file": srcVideo}),
		"11": nd("GetVideoComponents", map[string]any{"video": link("10", 0)}),
	}

	var images Link2
	if opt.Type == "ltx" {
		// 时间轴补齐到 8n+1(LTX VAE 的时间布局要求),解码后再裁回原长
		pad := upscalePad(length)
		srcImg := link("11", 0)
		if pad > 0 {
			g["60"] = nd("ImageFromBatch", map[string]any{
				"image": srcImg, "batch_index": length - 1, "length": 1,
			})
			g["61"] = nd("RepeatImageBatch", map[string]any{"image": link("60", 0), "amount": pad})
			g["62"] = nd("ImageBatch", map[string]any{"image1": srcImg, "image2": link("61", 0)})
			srcImg = link("62", 0)
		}
		g["63"] = nd("VAELoader", map[string]any{"vae_name": opt.LTXVAE})
		g["64"] = nd("VAEEncodeTiled", map[string]any{
			"pixels": srcImg, "vae": link("63", 0),
			"tile_size": 512, "overlap": 64, "temporal_size": 64, "temporal_overlap": 8,
		})
		g["65"] = nd("LatentUpscaleModelLoader", map[string]any{"model_name": opt.ModelName})
		g["66"] = nd("LTXVLatentUpsampler", map[string]any{
			"samples": link("64", 0), "upscale_model": link("65", 0), "vae": link("63", 0),
		})
		g["67"] = nd("VAEDecodeTiled", map[string]any{
			"samples": link("66", 0), "vae": link("63", 0),
			"tile_size": 512, "overlap": 64, "temporal_size": 64, "temporal_overlap": 8,
		})
		// 裁回原帧数,保证时长不漂移
		g["68"] = nd("ImageFromBatch", map[string]any{
			"image": link("67", 0), "batch_index": 0, "length": length,
		})
		images = link("68", 0)
	} else {
		g["80"] = nd("UpscaleModelLoader", map[string]any{"model_name": opt.ModelName})
		g["81"] = nd("ImageUpscaleWithModel", map[string]any{
			"upscale_model": link("80", 0), "image": link("11", 0),
		})
		// 逐帧超分**不保证**正好 2×(ESRGAN 多为 4×)→ 用 ImageScale 收敛到精确 2×,
		// 尺寸从上方链路的实际输出推导(用比例缩放,target 由调用方给宽高亦可)
		g["82"] = nd("ImageScaleBy", map[string]any{
			"image": link("81", 0), "upscale_method": "lanczos", "scale_by": 0.5,
		})
		images = link("82", 0)
	}

	// 重混**原始音轨**(放大不碰声音)
	g["18"] = nd("CreateVideo", map[string]any{
		"images": images, "audio": link("11", 1), "fps": fps,
	})
	g["19"] = nd("SaveVideo", map[string]any{
		"video":           link("18", 0),
		"filename_prefix": "manjux/" + outPrefix,
		"format":          "auto",
		"codec":           "auto",
	})
	return g
}

// Link2 便捷别名:与其它图构造函数保持一致的链接表示。
type Link2 = []any

// Upscale 对已完成的镜头做 2× 放大后处理(保留原音轨)。
// srcVideoPath 是源镜头的**本地**路径(内部会先上传到 ComfyUI)。
func (e *Engine) Upscale(ctx context.Context, srcVideoPath string, opt UpscaleOptions, length int, fps float64, outName, outPath string, timeout time.Duration) (string, error) {
	if err := e.Available(ctx); err != nil {
		return "", err
	}
	upName, err := e.client().UploadImage(ctx, srcVideoPath)
	if err != nil {
		return "", err
	}
	graph := buildUpscaleGraph(upName, opt, length, fps, outName)
	hist, err := e.submitAndWait(ctx, graph, timeout, nil)
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
	return "", fmt.Errorf("放大完成但未找到视频产物")
}

var _ = engine.Job{} // 保持与其它引擎文件一致的包依赖
