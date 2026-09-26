package comfyengine

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// UpscaleOptions 放大参数 —— 定义在 engine 包(见 engine.UpscaleOptions 的说明:
// 类型留在具体引擎里会让 server 层不得不 import comfyengine)。
type UpscaleOptions = engine.UpscaleOptions

// upscaleFrames 时间轴对齐:LTX 视频 VAE 要求 8n+1 帧,不足则复制尾帧补齐。
func upscalePad(frames int) int {
	if frames <= 1 {
		return 0
	}
	return (8 - ((frames - 1) % 8)) % 8
}

// esrganScale 从超分权重文件名推它的**原生放大倍数**(0 = 认不出)。
//
// 命名约定在社区权重里高度统一:`4x-UltraSharp` / `RealESRGAN_x4plus` /
// `RealESRGAN_x2plus` / `2x_ModernSpanimationVista` / `HAT_SRx4` —— 倍数总是与
// 字母 x 相邻(`4x` 或 `x4`)。**认不出来就返回 0**(调用方按历史行为当 4× 处理并
// 写日志),不猜、也不拒绝服务:BSRGAN / remacri / NMKD-Superscale 这类名字里
// 确实不含倍数。
func esrganScale(modelName string) int {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(modelName)))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < '0' || c > '9' {
			continue
		}
		if i+1 < len(name) && name[i+1] == 'x' {
			return int(c - '0') // 4x / 2x
		}
		if i > 0 && name[i-1] == 'x' {
			return int(c - '0') // x4 / x2
		}
	}
	return 0
}

// defaultESRGANScale 认不出权重倍数时按 4× 处理 —— 与 engine.DefaultESRGANUpscaleModel
// (4x-UltraSharp)一致,也就是本功能一直以来的实际行为。
const defaultESRGANScale = 4

// upscaleDownScale ESRGAN 原生输出 → 目标 2× 需要的缩放比;
// 返回 0 表示**不需要**缩放节点(权重本身就是 2×),第二个返回值表示倍数是否认得出来。
//
// 为什么不能写死 0.5(2026-09-26 渲染管线审查):0.5 只对 4× 权重成立。换成 2× 权重
// (RealESRGAN_x2plus 之类)时 2× → ×0.5 = 原分辨率,用户拿到一个和源片一样大的文件,
// 而界面报「2× 放大完成」—— 静默假成功。倍数由权重自己决定,所以只能按名字推。
func upscaleDownScale(modelName string) (float64, bool) {
	s := esrganScale(modelName)
	if s <= 0 {
		return 2.0 / float64(defaultESRGANScale), false
	}
	if s == 2 {
		return 0, true
	}
	return 2.0 / float64(s), true
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
		// 逐帧超分**不保证**正好 2×(ESRGAN 多为 4×)→ 用 ImageScaleBy 收敛到精确 2×。
		// 但缩放比必须按**权重自己的倍数**推:写死 0.5 只对 4× 权重成立,换 2× 权重时
		// 2×→×0.5 等于没放大却报成功(见 upscaleDownScale)。
		images = link("81", 0)
		if by, _ := upscaleDownScale(opt.ModelName); by > 0 {
			g["82"] = nd("ImageScaleBy", map[string]any{
				"image": link("81", 0), "upscale_method": "lanczos", "scale_by": by,
			})
			images = link("82", 0)
		}
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
	// 权重名里认不出倍数时按 4× 处理(= 历史行为),但要留痕:这种权重配错时
	// 产物会"看起来成功了但没放大",没有日志事后无从判断。
	if opt.Type != "ltx" {
		if _, known := upscaleDownScale(opt.ModelName); !known {
			log.Printf("[comfy] ⚠ 无法从超分权重名「%s」判定放大倍数,按 %d× 处理;若产物尺寸不是源片的 2×,请改用带 2x/4x 的权重名",
				opt.ModelName, defaultESRGANScale)
		}
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
