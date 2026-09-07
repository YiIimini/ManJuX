// ManJuX 短视频创作工作台 —— 桌面主程序(Wails v2)。
//
// 启动链:单实例看门狗 → 日志 → 设置 → 组件装配 →
// Wails 原生窗口(前端嵌入 + API 直通)→ 托盘常驻。
// 同时保留独立 HTTP 服务(127.0.0.1,浏览器访问/排障用)。
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/engine/h3api"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/server"
	"github.com/YiIimini/ManJuX/internal/version"
	"github.com/YiIimini/ManJuX/internal/winctl"
)

func main() {
	log.SetFlags(log.LstdFlags)

	exeDir := mustExeDir()

	// 1) 单实例(Wails 内建锁:第二实例自动唤起已有窗口后退出)
	// 2) 日志(文件 + 控制台);超 10MB 归档轮转
	logDir := filepath.Join(exeDir, "data", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "manjux.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 10<<20 {
		_ = os.Rename(logPath, logPath+".old")
	}
	logFile, err := os.OpenFile(logPath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		log.SetOutput(newTee(logFile, os.Stdout))
		defer logFile.Close()
	}
	log.Printf("[manjux] %s 启动(桌面模式)", version.BuildInfo())

	// 3) 设置与组件装配
	cfgMgr, err := config.NewManager(filepath.Join(exeDir, "data", "settings.json"), exeDir)
	fatalIf(err, "加载设置失败")
	g := cfgMgr.Get()
	_ = os.MkdirAll(g.DataDir, 0o755)

	store, err := project.NewStore(filepath.Join(g.DataDir, "projects"))
	fatalIf(err, "初始化项目仓库失败")

	f, err := ff.New(g.FF.FFmpegPath, g.FF.FFprobePath)
	if err != nil {
		log.Printf("[manjux] ⚠ %v(合成/质检将不可用,请安装 ffmpeg)", err)
	}

	reg := engine.NewRegistry()
	reg.Register(comfyengine.New(func() config.ComfySettings { return cfgMgr.Get().Comfy }))
	reg.Register(h3api.New(func() config.MiniMaxSettings { return cfgMgr.Get().MiniMax }))

	bus := pipeline.NewBus()
	// 事件桥:管线事件同步落日志文件(持久化运行历史)
	go func() {
		_, ch, _ := bus.Subscribe()
		for ev := range ch {
			log.Printf("[事件][%s][%s][%s] %s", ev.ProjectID, ev.Phase, ev.Level, ev.Message)
		}
	}()
	// 跨项目资产库(角色/场景一次定妆全局复用)
	lib, err := library.New(filepath.Join(g.DataDir, "assets_library"))
	fatalIf(err, "初始化资产库失败")
	coord := pipeline.New(store, cfgMgr, reg, f, bus, lib)
	srv := server.New(cfgMgr, store, reg, coord, f, bus, lib)

	// 4) 独立 HTTP 服务(浏览器访问/排障;桌面窗口不依赖它)
	port, err := srv.Start(g.ServerPort)
	if err != nil {
		log.Printf("[manjux] ⚠ 独立 HTTP 服务启动失败:%v(桌面窗口不受影响)", err)
	} else {
		log.Printf("[manjux] 浏览器入口(可选): http://127.0.0.1:%d/", port)
	}

	// 5) Wails 原生桌面窗口(无边框,标题栏由前端自绘:拖动区+窗口控制按钮)
	err = wails.Run(&options.App{
		Title:            "ManJuX · 短视频创作工作台",
		Width:            1480,
		Height:           920,
		MinWidth:         1160,
		MinHeight:        720,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 11, G: 14, B: 20, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  server.WebAssets(),
			Handler: srv.APIHandler(),
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "ManJuX_SingleInstance_6f1c2a",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				log.Printf("[manjux] 检测到第二实例启动,已忽略")
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
		OnStartup: func(ctx context.Context) {
			winctl.Default.Bind(ctx)
			srv.SetQuitFunc(func() {
				log.Printf("[manjux] 收到退出请求(界面确认)")
				go runtime.Quit(ctx)
			})
			log.Printf("[manjux] 桌面窗口就绪")
		},
		OnBeforeClose: func(ctx context.Context) bool {
			// 管线运行中拦截关窗,交前端二次确认(防误关中断渲染)
			if coord.HasRunning() {
				log.Printf("[manjux] 拦截关窗:管线运行中,等待用户确认")
				runtime.EventsEmit(ctx, "confirm-quit")
				return true
			}
			return false
		},
		OnShutdown: func(ctx context.Context) {
			log.Printf("[manjux] 退出")
			bus.Close()
		},
		Bind: []interface{}{},
	})
	fatalIf(err, "桌面窗口启动失败")
}

func mustExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		fatalIf(err, "定位可执行文件失败")
	}
	// 开发模式(wails dev / go run)用工作目录
	dir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		if wd, werr := os.Getwd(); werr == nil {
			if _, gerr := os.Stat(filepath.Join(wd, "go.mod")); gerr == nil {
				return wd
			}
		}
	}
	return dir
}

func fatalIf(err error, msg string) {
	if err != nil {
		log.Fatalf("[manjux] ✕ %s: %v", msg, err)
	}
}
