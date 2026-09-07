// Package winctl 无边框窗口控制(Frameless 后系统按钮自绘,前端经 HTTP 桥调用)。
package winctl

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type Controller struct {
	mu    sync.Mutex
	ctx   context.Context
	max   bool  // 最大化状态(前端切换按钮样式用)
	force int32 // 用户确认退出标志(OnBeforeClose 放行,防反复拦截)
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

// Hide 隐藏窗口(后台运行;托盘图标与单实例二次启动均可唤回)。
func (c *Controller) Hide() {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.WindowHide(ctx)
	}
}

// Show 显示并前置窗口(单实例二次启动/托盘点击唤回)。
func (c *Controller) Show() {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.WindowShow(ctx)
		runtime.WindowUnminimise(ctx)
	}
}

// RequestQuit 请求退出:管线运行中走前端确认(Alt+F4 / 系统菜单场景)。
func (c *Controller) RequestQuit() {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.EventsEmit(ctx, "confirm-quit")
	}
}

// ForceQuit 用户已在界面明确选择退出:置放行标志并真正退出。
// OnBeforeClose 看到该标志直接放行 —— 否则运行中的管线会反复拦截,
// 界面表现即「点了退出没反应」(2026-09-08 修复)。
func (c *Controller) ForceQuit() {
	atomic.StoreInt32(&c.force, 1)
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx != nil {
		runtime.Quit(ctx)
	}
}

// ForceMode 是否处于用户确认后的强制退出(OnBeforeClose 放行)。
func (c *Controller) ForceMode() bool { return atomic.LoadInt32(&c.force) == 1 }
