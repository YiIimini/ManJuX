// Package tray 系统托盘常驻:打开工作台 / 退出。
// 桌面(Wails)模式下默认不启用;独立服务/浏览器模式下可选启用。
package tray

import (
	_ "embed"

	"github.com/energye/systray"

	"github.com/YiIimini/ManJuX/internal/browser"
)

//go:embed icon.ico
var iconData []byte

// Deps 托盘行为依赖。
type Deps struct {
	WorkbenchURL func() string // 每次点击时取最新 URL(端口可能自动偏移)
	OnQuit       func()
}

// Run 启动托盘(阻塞)。全部 UI 操作在 onReady 内执行才生效。
func Run(d Deps) {
	systray.Run(
		func() { // onReady
			systray.SetIcon(iconData)
			systray.SetTitle("ManJuX")
			systray.SetTooltip("ManJuX 短视频创作工作台")
			mOpen := systray.AddMenuItem("打开工作台", "在独立窗口中打开 ManJuX 工作台")
			systray.AddSeparator()
			mQuit := systray.AddMenuItem("退出", "退出 ManJuX(进行中的管线会安全中止)")
			mOpen.Click(func() {
				_ = browser.OpenAppWindow(d.WorkbenchURL(), 1480, 920)
			})
			mQuit.Click(func() { systray.Quit() })
		},
		func() { // onQuit
			if d.OnQuit != nil {
				d.OnQuit()
			}
		},
	)
}

// Quit 退出托盘(从外部触发)。
func Quit() {
	systray.Quit()
}
