// Package winctl 无边框窗口控制(Frameless 后系统按钮自绘,前端经 HTTP 桥调用)。
package winctl

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type Controller struct {
	mu  sync.Mutex
	ctx context.Context
	max bool // 最大化状态(前端切换按钮样式用)
}

var Default = &Controller{}

// Bind Wails OnStartup 时注入窗口上下文。
func (c *Controller) Bind(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
}

func (c *Controller) Minimise() {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.WindowMinimise(ctx)
	}
}

// ToggleMaximise 最大化/还原切换,返回当前是否最大化。
func (c *Controller) ToggleMaximise() bool {
	c.mu.Lock()
	ctx := c.ctx
	c.max = !c.max
	now := c.max
	c.mu.Unlock()
	if ctx != nil {
		runtime.WindowToggleMaximise(ctx)
	}
	return now
}

// Quit 退出。不直接 runtime.Quit(绕过 OnBeforeClose 确认),
// 走 wails 事件让前端弹确认,前端确认后再调 ForceQuit。
func (c *Controller) RequestQuit() {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.EventsEmit(ctx, "confirm-quit")
	}
}

// Hide 隐藏窗口(后台运行;再点应用图标经单实例锁自动唤回)。
func (c *Controller) Hide() {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.WindowHide(ctx)
	}
}

// Show 显示并前置窗口(单实例二次启动时唤回后台实例)。
func (c *Controller) Show() {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.WindowShow(ctx)
		runtime.WindowUnminimise(ctx)
	}
}

// ForceQuit 真正退出(前端确认后调用)。
func (c *Controller) ForceQuit() {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.Quit(ctx)
	}
}
