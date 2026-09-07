// Package tray 系统托盘常驻:打开工作台 / 退出。
package tray

import (
	_ "embed"
	"os/exec"

	"github.com/energye/systray"
)

//go:embed icon.ico
var iconData []byte

// Deps 托盘行为依赖。
type Deps struct {
	WorkbenchURL func() string // 每次点击时取最新 URL(端口可能自动偏移)
	OnQuit       func()
}

// Run 启动托盘(阻塞)。
func Run(d Deps) {
	systray.SetIcon(iconData)
	systray.SetTitle("ManJuX")
	systray.SetTooltip("ManJuX 短视频创作工作台")
	mOpen := systray.AddMenuItem("打开工作台", "在浏览器中打开 ManJuX 工作台")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "退出 ManJuX(进行中的管线会安全中止)")

	mOpen.Click(func() {
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", d.WorkbenchURL()).Start()
	})
	mQuit.Click(func() {
		systray.Quit()
	})
	systray.Run(func() {}, func() {
		if d.OnQuit != nil {
			d.OnQuit()
		}
	})
}

// Quit 退出托盘(从外部触发)。
func Quit() {
	systray.Quit()
}
