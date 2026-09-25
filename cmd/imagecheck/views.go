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
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/pipeline"
)

// views 多视角自检:拿一张定妆照,走**生产同一条路**(pipeline.GenerateViewsEdit →
// engine.EditImage)出多视角图并按角度命名落盘,打印每个角度的路径与耗时。
//
// 为什么要这条命令:多视角的图形态与生成不是一套(生成=文生图,多视角=图像编辑),
// 换图像模型时两处都要验。2026-09-25 用户定调「都使用 2.1」,这里就是那条链路的验收口。
func runViews(args []string) int {
	fs := flag.NewFlagSet("views", flag.ContinueOnError)
	settings := fs.String("settings", filepath.Join("data", "settings.json"), "settings.json 路径")
	exeDir := fs.String("exedir", ".", "exe 目录")
	master := fs.String("master", "", "定妆照绝对路径(必填)")
	name := fs.String("name", "测试角色", "角色名")
	prompt := fs.String("prompt", "", "定妆照提示词(取身份描述用)")
	promptFile := fs.String("prompt-file", "", "从 UTF-8 文本文件读定妆照提示词")
	kind := fs.String("kind", "human", "实体类型:human/chibi/object/spirit_pet/beast/incorporeal")
	outDir := fs.String("dir", "", "产物目录;空 = 定妆照所在目录")
	seed := fs.Int64("seed", 20260925, "种子")
	steps := fs.Int("steps", 0, "采样步数;0 = 按家族自动")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *master == "" {
		fmt.Fprintln(os.Stderr, "-master 必填(定妆照路径)")
		return 2
	}
	text := *prompt
	if text == "" && *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读提示词文件失败: %v\n", err)
			return 1
		}
		text = string(b)
	}

	mgr, err := config.NewManager(*settings, *exeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载设置失败: %v\n", err)
		return 1
	}
	c := mgr.Get().Comfy
	fam := comfyengine.ResolveImageFamily("", c.EditUNet, c.EditCLIP)
	fmt.Printf("编辑家族(按 edit_unet 判定) = %s\n", fam.Label)
	fmt.Printf("模型 edit_unet=%s\n     edit_clip=%s (type=%s)\n     edit_vae=%s\n",
		c.EditUNet, c.EditCLIP, fam.CLIPType, c.EditVAE)
	if c.EditTurboLoRA != "" || c.EditAnglesLoRA != "" {
		fmt.Printf("LoRA turbo=%s angles=%s(仅 2511 档生效)\n", c.EditTurboLoRA, c.EditAnglesLoRA)
	}

	eng := comfyengine.New(func() config.ComfySettings { return c })
	// ffmpeg 只用于把各角度横向拼成转台图;路径取默认(本机 PATH 里就有)。
	ffw, err := ff.New("ffmpeg", "ffprobe")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ffmpeg 不可用(拼转台图需要): %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	started := time.Now()
	turn, err := pipeline.GenerateViewsEdit(ctx, eng, ffw, pipeline.TurnaroundOptions{
		MasterPath: *master,
		Dir:        *outDir,
		Name:       *name,
		Prompt:     text,
		Kind:       pipeline.EntityKind(*kind),
		Source:     pipeline.ViewSourceEdit,
		Seed:       *seed,
		Steps:      *steps,
		Emit:       func(format string, a ...any) { fmt.Printf("  · "+format+"\n", a...) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "多视角失败(%v): %v\n", time.Since(started).Round(time.Second), err)
		return 1
	}
	dir := *outDir
	if dir == "" {
		dir = filepath.Dir(*master)
	}
	fmt.Printf("完成 %s\n转台图 %s\n", time.Since(started).Round(time.Second), turn)
	for _, tag := range []string{"threequarter", "side", "back"} {
		p := filepath.Join(dir, pipeline.RelatedSuffixName(filepath.Base(*master), "view_"+tag))
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("角度图 %s\n", p)
		}
	}
	return 0
}
