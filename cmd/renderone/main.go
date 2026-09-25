// Command renderone 单镜重渲:走**生产同一条路径**(Coordinator.Start + only 过滤)
// 只重渲指定镜头,用于"提示词改了,拿一两镜做前后对比",不必整集重跑。
//
// 为什么需要它:提示词/参考图一改,指纹变化会让整集的有效产物全部失效;而验证一次
// 提示词改动是否真的解决问题,只需要一比一镜。整集重跑是 30 分钟,单镜是 7 分钟。
//
// 用法:
//
//	go run ./cmd/renderone -project "<项目 id>" -shots 20,22
//	-project 省略时用 -settings 里 DataDir/projects 下最近修改的那个项目。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

func main() {
	settings := flag.String("settings", filepath.Join("data", "settings.json"), "settings.json 路径")
	exeDir := flag.String("exedir", ".", "exe 目录")
	projID := flag.String("project", "", "项目 id;空 = projects 目录下最近修改的一个")
	shots := flag.String("shots", "", "要重渲的镜头号,逗号分隔(必填)")
	engineOverride := flag.String("engine", "comfy", "引擎:comfy / auto")
	timeout := flag.Duration("timeout", 60*time.Minute, "整体超时")
	flag.Parse()
	if strings.TrimSpace(*shots) == "" {
		fmt.Fprintln(os.Stderr, "-shots 必填,例如 -shots 20,22")
		os.Exit(2)
	}
	var only []int
	for _, p := range strings.Split(*shots, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			fmt.Fprintf(os.Stderr, "镜头号不合法: %q\n", p)
			os.Exit(2)
		}
		only = append(only, n)
	}

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
		id = newestProject(store)
		if id == "" {
			fatal("projects 目录里没有项目")
		}
	}
	if _, ok := store.Get(id); !ok {
		fatal("项目不存在: %s", id)
	}
	fmt.Printf("项目 %s\n重渲镜头 %v\n", id, only)

	f, err := ff.New(g.FF.FFmpegPath, g.FF.FFprobePath)
	if err != nil {
		fmt.Printf("⚠ %v(合成/质检不可用)\n", err)
	}
	reg := engine.NewRegistry()
	comfyEng := comfyengine.New(func() config.ComfySettings { return mgr.Get().Comfy })
	reg.Register(comfyEng)
	bus := pipeline.NewBus()
	go func() {
		_, ch, _ := bus.Subscribe()
		for ev := range ch {
			if ev.Level == "info" && strings.Contains(ev.Message, "执行中") {
				fmt.Printf("\r[%s] %s", ev.Phase, ev.Message)
				continue
			}
			fmt.Printf("[%s][%s] %s\n", ev.Phase, ev.Level, ev.Message)
		}
	}()
	lib, _ := library.New(filepath.Join(g.DataDir, "assets_library"))
	coord := pipeline.New(store, mgr, reg, f, bus, lib)

	started := time.Now()
	if err := coord.Start(id, pipeline.PhaseRender, only, *engineOverride, 0); err != nil {
		fatal("启动渲染失败: %v", err)
	}
	deadline := time.Now().Add(*timeout)
	for coord.Running(id) != nil {
		if time.Now().After(deadline) {
			coord.Stop(id)
			fatal("超时,已请求停止")
		}
		time.Sleep(3 * time.Second)
	}
	fmt.Printf("\n结果 %s  用时 %s\n", coord.Result(id), time.Since(started).Round(time.Second))
	rel := filepath.Join(store.Dir(id), "clips")
	for _, n := range only {
		p := filepath.Join(rel, fmt.Sprintf("shot_%03d.mp4", n))
		if st, err := os.Stat(p); err == nil {
			fmt.Printf("产物 %s  %.1f MB  %s\n", p, float64(st.Size())/(1<<20), st.ModTime().Format("15:04:05"))
		}
	}
}

func newestProject(store *project.Store) string {
	entries, err := os.ReadDir(filepath.Join("data", "projects"))
	if err != nil {
		return ""
	}
	best, bestT := "", time.Time{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := store.Get(e.Name()); !ok {
			continue // 不是有效项目(缺 project.json)
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestT) {
			best, bestT = e.Name(), info.ModTime()
		}
	}
	return best
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
