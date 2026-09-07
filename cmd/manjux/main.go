// ManJuX 短视频创作工作台 —— 主程序。
//
// 启动链:单实例看门狗 → 日志 → 设置 → 组件装配 → HTTP 服务 →
// 托盘常驻(阻塞)。关闭 = 托盘退出(进行中的管线安全中止)。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/YiIimini/ManJuX/internal/browser"
	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/engine/h3api"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/server"
	"github.com/YiIimini/ManJuX/internal/tray"
	"github.com/YiIimini/ManJuX/internal/version"
	"github.com/YiIimini/ManJuX/internal/watchdog"
)

func main() {
	log.SetFlags(log.LstdFlags)

	// 1) 单实例
	release, err := watchdog.Acquire()
	if err != nil {
		log.Printf("[manjux] %v,退出", err)
		watchdog.Halt(err.Error())
		return
	}
	defer release()

	exeDir := mustExeDir()

	// 2) 日志(文件 + 控制台)
	logDir := filepath.Join(exeDir, "data", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logFile, err := os.OpenFile(filepath.Join(logDir, "manjux.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		log.SetOutput(newTee(logFile, os.Stdout))
		defer logFile.Close()
	}
	log.Printf("[manjux] %s 启动", version.BuildInfo())

	// 3) 设置
	cfgMgr, err := config.NewManager(filepath.Join(exeDir, "data", "settings.json"), exeDir)
	fatalIf(err, "加载设置失败")
	g := cfgMgr.Get()
	_ = os.MkdirAll(g.DataDir, 0o755)

	// 4) 组件装配
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
	coord := pipeline.New(store, cfgMgr, reg, f, bus)

	srv := server.New(cfgMgr, store, reg, coord, f, bus)
	port, err := srv.Start(g.ServerPort)
	fatalIf(err, "启动 HTTP 服务失败")
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	log.Printf("[manjux] 工作台已就绪: %s", url)

	if g.AutoOpenUI {
		go func() {
			time.Sleep(600 * time.Millisecond)
			_ = browser.Open(url)
		}()
	}

	// 5) 托盘常驻(阻塞)
	tray.Run(tray.Deps{
		WorkbenchURL: func() string { return url },
		OnQuit: func() {
			log.Printf("[manjux] 退出")
			bus.Close()
		},
	})
}

func mustExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		fatalIf(err, "定位可执行文件失败")
	}
	// 开发模式(go run)落在临时目录 → 用工作目录
	dir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
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
