// Command clipfix 把**已有片段**里的黑闪剪掉（存量项目用）。
//
// 为什么单开一个入口：黑闪切除挂在渲染阶段的"片段落盘点"上（见 pipeline/stages_render.go），
// 所以它只对**新渲出来的**片段生效；而真机那一集的 13 条片段早在 09:40 那一版就渲完了 ——
// 重渲整集要 30 分钟以上 GPU，而"把已有的几十毫秒全黑剪掉"是纯 CPU、几十秒的事。
//
// 用法：
//
//	go run ./cmd/clipfix -project "<项目 id>"            # 只干跑，列出会剪什么
//	go run ./cmd/clipfix -project "<项目 id>" -apply    # 真剪（**已处理过的也重剪**，存量修复）
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/compose"
	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
)

func main() {
	settings := flag.String("settings", filepath.Join("data", "settings.json"), "settings.json 路径")
	exeDir := flag.String("exedir", ".", "exe 目录")
	projID := flag.String("project", "", "项目 id；空 = projects 目录下最近修改的一个")
	apply := flag.Bool("apply", false, "真的剪（不带这个参数只列出会剪什么）")
	flag.Parse()

	mgr, err := config.NewManager(*settings, *exeDir)
	if err != nil {
		fatal("加载设置失败: %v", err)
	}
	g := mgr.Get()
	store, err := project.NewStore(filepath.Join(g.DataDir, "projects"))
	if err != nil {
		fatal("打开项目仓库失败: %v", err)
	}
	id := *projID
	if id == "" {
		entries, _ := os.ReadDir(filepath.Join(g.DataDir, "projects"))
		var best string
		var bestT time.Time
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || !info.IsDir() {
				continue
			}
			if _, ok := store.Get(e.Name()); !ok {
				continue
			}
			if info.ModTime().After(bestT) {
				best, bestT = e.Name(), info.ModTime()
			}
		}
		id = best
	}
	if id == "" {
		fatal("没有可选的项目")
	}
	if _, ok := store.Get(id); !ok {
		fatal("项目不存在: %s", id)
	}
	ffm, err := ff.New(g.FF.FFmpegPath, g.FF.FFprobePath)
	if err != nil {
		fatal("ffmpeg 不可用: %v", err)
	}

	clipsDir := filepath.Join(store.Dir(id), "clips")
	entries, err := os.ReadDir(clipsDir)
	if err != nil {
		fatal("读片段目录失败: %v", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mp4") {
			continue
		}
		if strings.HasSuffix(e.Name(), "_placeholder.mp4") {
			continue // 失败镜的占位片段不是产物，别动
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	fmt.Printf("项目 %s\n片段 %d 条（%s）\napply=%v\n", id, len(files), clipsDir, *apply)

	ctx := context.Background()
	totalShots, totalSec := 0, 0.0
	for _, name := range files {
		p := filepath.Join(clipsDir, name)
		runs, err := compose.DetectBlackRuns(ctx, ffm, p)
		if err != nil {
			fmt.Printf("  %-18s ⚠ 探测失败: %v\n", name, err)
			continue
		}
		if len(runs) == 0 {
			continue
		}
		var parts []string
		for _, r := range runs {
			parts = append(parts, fmt.Sprintf("%.3f~%.3fs", r.Start, r.End))
		}
		fmt.Printf("  %-18s 黑段 %d 处: %s\n", name, len(runs), strings.Join(parts, ", "))
		if !*apply {
			continue
		}
		shot, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "shot_"), ".mp4"))
		// 走 Force：存量片段带着"已处理过"标记（旧判据下只剪掉了一部分），普通入口会静默跳过。
		removed, n, longRuns, err := compose.ForceStripBlackFlash(ctx, ffm, p, 0)
		if err != nil {
			fmt.Printf("      ✕ 镜头 %d 剪失败: %v\n", shot, err)
			continue
		}
		if n > 0 {
			totalShots++
			totalSec += removed
			fmt.Printf("      ✔ 镜头 %d 剪掉 %d 段（%.3fs）\n", shot, n, removed)
		}
		for _, r := range longRuns {
			fmt.Printf("      ⚠ 镜头 %d 有 %.2fs 连续黑场（%.2fs 起）—— 太长，没剪\n", shot, r.Dur(), r.Start)
		}
	}
	if *apply {
		fmt.Printf("\n共 %d 条片段剪掉黑闪（合计 %.3fs）\n", totalShots, totalSec)
	} else {
		fmt.Printf("\n（干跑：加 -apply 才真剪）\n")
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
