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
	"github.com/YiIimini/ManJuX/internal/tray"
	"github.com/YiIimini/ManJuX/internal/version"
	"github.com/YiIimini/ManJuX/internal/winctl"
)

func main() {
	log.SetFlags(log.LstdFlags)

	exeDir := mustExeDir()

	// 1) 单实例(Wails 内建锁:第二实例自动唤起已有窗口后退出)
	// 2) 日志(文件 + 控制台):写入器按 10MB 在**运行期**自动归档,保留最近 5 份。
	//    旧实现只在启动时判一次大小并固定归档成 .old(仅一份且被下次启动覆盖),
	//    长会话跑到中途文件仍会无限增长。
	logDir := filepath.Join(exeDir, "data", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "manjux.log")
	if lw, lerr := newTee(logPath, 10<<20, os.Stdout); lerr == nil {
		log.SetOutput(lw)
		defer func() { _ = lw.Close() }()
	} else {
		log.Printf("[manjux] 日志文件不可用(%v),仅输出到控制台", lerr)
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
	comfyEng := comfyengine.New(func() config.ComfySettings { return cfgMgr.Get().Comfy })
	reg.Register(comfyEng)
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
	// ComfyUI 生命周期通知(启动成功/停止完成)→ 全局事件流:
	// 前端底栏灯/首页 chip/管理页即时同步,不再等 15s 轮询或探测翻转
	comfyengine.SetLifecycleNotice(func(alive bool, why string) {
		msg := "ComfyUI 已停止(手动)"
		if alive {
			msg = "ComfyUI 已启动,渲染与抽卡可用"
		} else if why == "空闲自动退出" {
			msg = "ComfyUI 已停止(空闲自动退出,下次渲染自动重启)"
		}
		bus.Publish(pipeline.Event{Phase: "comfy", Level: "info", Message: msg})
	})
	comfyEng.RunIdleWatcher()
	// 开机预热 ComfyUI(2026-09-26 用户要求「APP启动后 ComfyUI 自动启动」):
	// 后台起,不阻塞窗口;失败只记日志 —— 真正的可用性判定仍留给出像素的路径。
	// 这一步同时消掉一个真实故障:预检按设计不触发启动,于是"没启动"会被读成"不可用",
	// 批量产线第 1 集就断在「无可用引擎」(实测)。
	go func() {
		if err := comfyEng.StartOnBoot(context.Background()); err != nil {
			log.Printf("[manjux] ⚠ ComfyUI 预热启动失败:%v(渲染时仍会再试一次)", err)
			return
		}
		log.Printf("[manjux] ComfyUI 已就绪:%s", cfgMgr.Get().Comfy.BaseURL)
	}()
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
				log.Printf("[manjux] 检测到第二实例启动:唤回已有窗口")
				winctl.Default.Show()
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
			// WebView2 稳定性:渲染采样期 GPU 满载曾挤崩 webview 的 GPU 进程,
			// 而 go-webview2 对失效 webview 的兜底是 os.Exit(1) 整进程退出
			// (2026-09-09 崩溃链实证)。界面为 DOM 型,软件合成足够;禁用
			// webview GPU 加速根治渲染期闪退。(注:WEBVIEW2_ADDITIONAL_
			// BROWSER_ARGUMENTS 环境变量在 WebView2 152 实测不生效,须走本开关)
			WebviewGpuIsDisabled: true,
		},
		// 原生文件拖放:把拖入文件的绝对路径发给前端(上传区用
		// --wails-drop-target 标记);同时关掉 webview 自带的拖放,
		// 杜绝拖文件误当网页打开。
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		OnStartup: func(ctx context.Context) {
			winctl.Default.Bind(ctx)
			// 系统托盘:后台运行(窗口隐藏)时的常驻入口
			go tray.RunDesktop(tray.Deps{
				OnShow: func() { winctl.Default.Show() },
				OnQuit: func() { winctl.Default.ForceQuit() },
			})
			srv.SetQuitFunc(func() {
				log.Printf("[manjux] 收到退出请求(界面确认)")
				go runtime.Quit(ctx)
			})
			log.Printf("[manjux] 桌面窗口就绪")
		},
		OnBeforeClose: func(ctx context.Context) bool {
			// 用户已在界面确认「直接退出」:放行(否则反复拦截表现为退出无反应)
			if winctl.Default.ForceMode() {
				return false
			}
			// 管线运行中拦截关窗,交前端二次确认(防误关中断渲染)
			if coord.HasRunning() {
				log.Printf("[manjux] 拦截关窗:管线运行中,等待用户确认")
				runtime.EventsEmit(ctx, "confirm-quit")
				return true
			}
			return false
		},
		OnShutdown: func(ctx context.Context) {
			tray.Quit()
			log.Printf("[manjux] 退出:停止全部管线并结束 ComfyUI 全部进程")
			coord.StopAll()
			if eng, err := reg.Get("comfy"); err == nil {
				if ce, ok := eng.(*comfyengine.Engine); ok {
					// 直接退出承诺:ComfyUI 进程清干净(代管树 + rootDir 内 python)
					_ = ce.StopAdmin()
				}
			}
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
