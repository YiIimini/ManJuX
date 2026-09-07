// Package tray 系统托盘常驻。
// 桌面(Wails)模式:显示主窗口 / 退出;后台运行(窗口隐藏)时托盘是唯一入口。
// 独立服务/浏览器模式:打开工作台浏览器窗口 / 退出。
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
	WorkbenchURL func() string // 浏览器模式:每次点击时取最新 URL
	OnShow       func()        // 桌面模式:唤回主窗口
	OnQuit       func()        // 退出应用
}

// RunDesktop 桌面模式托盘:显示主窗口 / 退出(阻塞,需在独立 goroutine 运行)。
func RunDesktop(d Deps) {
	systray.Run(
		func() {
			systray.SetIcon(iconData)
			systray.SetTitle("ManJuX")
			systray.SetTooltip("ManJuX 短视频创作工作台 · 后台运行中")
			mShow := systray.AddMenuItem("显示主窗口", "唤回 ManJuX 主窗口")
			systray.AddSeparator()
			mQuit := systray.AddMenuItem("退出", "退出 ManJuX(停止管线并同步退出 ComfyUI)")
			mShow.Click(func() {
				if d.OnShow != nil {
					d.OnShow()
				}
			})
			mQuit.Click(func() {
				if d.OnQuit != nil {
					d.OnQuit()
				}
			})
		},
		func() { /* onQuit:应用退出由 OnQuit 驱动,这里不额外动作 */ },
	)
}

// Run 浏览器模式托盘(打开工作台 / 退出)。
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
			mQuit.Click(func() {
				if d.OnQuit != nil {
					d.OnQuit()
				} else {
					systray.Quit()
				}
			})
		},
		func() { // onQuit
			if d.OnQuit != nil {
				d.OnQuit()
			}
		},
	)
}

// Quit 退出托盘(应用关闭时清掉图标)。
func Quit() {
	systray.Quit()
}
