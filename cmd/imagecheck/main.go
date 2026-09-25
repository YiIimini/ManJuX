// Command imagecheck 生图自检:用**当前设置里的图像模型家族**出一张图,打印家族、
// 实际模型文件名、耗时与落地路径。
//
// 为什么单独做成一条命令:换图像模型(见 comfyengine/imagefamily.go)之后,"到底能不能出图、
// 出的是不是那个模型"必须能一眼验证,而不能靠翻日志猜。它走的是**应用同一条路径** ——
// 读同一份 settings.json → 同一个 ResolveImageFamily → 同一个 buildImageGraph → 同一个 ComfyUI,
// 所以自检通过就等于应用那条路通。ComfyUI 没在跑时会按设置代管启动(隐藏窗口)。
//
// 用法:
//
//	go run ./cmd/imagecheck -prompt-file %TEMP%\p.txt -out %TEMP%\x.png -w 832 -h 1248
//	go run ./cmd/imagecheck            # 不带参数时用内置短提示词,832x1248
//	go run ./cmd/imagecheck views -master <定妆照> -prompt-file %TEMP%\p.txt   # 多视角自检
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
)

const defaultPrompt = "Young-adult woman in her mid-twenties, narrow oval face with a sharp pointed chin, " +
	"wheat-tan sun-weathered skin, short black ponytail tied with a worn grey rubber band, " +
	"a faint small scar at the tail of her right eyebrow; faded blue-grey courier jacket over a " +
	"washed-out grey tee, in CHINESE 2D ANIME STYLE, CLEAN LINEWORK, VIBRANT CEL SHADING, " +
	"plain background, upper-body framing, front-facing"

func main() {
	// 子命令:views = 多视角自检(走生产同一条 GenerateViewsEdit)
	if len(os.Args) > 1 && os.Args[1] == "views" {
		os.Exit(runViews(os.Args[2:]))
	}
	settings := flag.String("settings", filepath.Join("data", "settings.json"), "settings.json 路径")
	exeDir := flag.String("exedir", ".", "exe 目录(用于推导默认 DataDir/ComfyUI 根目录)")
	prompt := flag.String("prompt", "", "提示词(不给则用 -prompt-file 或内置短提示词)")
	promptFile := flag.String("prompt-file", "", "从 UTF-8 文本文件读提示词")
	w := flag.Int("w", 832, "宽")
	h := flag.Int("h", 1248, "高")
	seed := flag.Int64("seed", 20260925, "种子")
	out := flag.String("out", filepath.Join(os.TempDir(), "manjux-imagecheck.png"), "输出图片路径")
	noStart := flag.Bool("no-start", false, "不代管启动 ComfyUI(只测已经跑着的引擎)")
	flag.Parse()

	text := *prompt
	if text == "" && *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			fatal("读提示词文件失败: %v", err)
		}
		text = string(b)
	}
	if text == "" {
		text = defaultPrompt
	}

	mgr, err := config.NewManager(*settings, *exeDir)
	if err != nil {
		fatal("加载设置失败: %v", err)
	}
	c := mgr.Get().Comfy
	if *noStart {
		c.AutoStart = false
	}
	fam := comfyengine.ResolveImageFamily(c.ImageFamily, c.ImageUNet, c.ImageCLIP)
	fmt.Printf("家族 image_family=%q -> %s\n", c.ImageFamily, fam.Label)
	fmt.Printf("模型   unet=%s\n       clip=%s (type=%s)\n       vae=%s\n",
		c.ImageUNet, c.ImageCLIP, fam.CLIPType, c.ImageVAE)
	fmt.Printf("采样   steps=%d cfg=%v sampler=%s scheduler=%s auraShift=%v encode21=%v\n",
		fam.Steps, fam.CFG, fam.Sampler, fam.Scheduler, fam.AuraShift, fam.Encode21)
	fmt.Printf("画幅   %dx%d  seed=%d\n", *w, *h, *seed)
	if c.RootDir != "" {
		fmt.Printf("根目录 %s\n", c.RootDir)
	}

	eng := comfyengine.New(func() config.ComfySettings { return c })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	started := time.Now()
	path, err := eng.GenerateImage(ctx, text, "", *seed, *w, *h, *out)
	if err != nil {
		fatal("出图失败(%v): %v", time.Since(started).Round(time.Second), err)
	}
	st, _ := os.Stat(path)
	fmt.Printf("完成   %s  %s  %.1f KB\n", time.Since(started).Round(time.Second), path, float64(st.Size())/1024)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
