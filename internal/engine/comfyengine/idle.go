// idle.go ComfyUI 空闲自动退出:管线结束后 N 分钟无任务(渲染/生成/队列),
// 停掉本应用代管的引擎释放显存与内存;外部启动的实例(e.proc==nil)永不接管。
// 停止只动代管进程树(stopManagedOnly);状态变化经 notifyLifecycle 通知,
// 由装配回调发全局事件,前端状态灯/管理页即时同步。
package comfyengine

import (
	"context"
	"log"
	"sync"
	"time"
)

// idleWatcher 空闲看门狗。
type idleWatcher struct {
	mu       sync.Mutex
	lastBusy time.Time
}

var idle = &idleWatcher{}

// touchBusy 标记「正在使用」(渲染/生成/管理操作时调用)。
func touchBusy() {
	idle.mu.Lock()
	idle.lastBusy = time.Now()
	idle.mu.Unlock()
}

// RunIdleWatcher 启动看门狗(每 60s 检查一次;应用生命周期内常驻)。
// 停止条件全部满足才动手:
//   - 代管启动(e.proc != nil,外部实例不动)
//   - 距最近一次使用超过 idle 分钟(设置 0 = 禁用)
//   - ComfyUI 队列空(running+pending 都空,不打断用户在 WEB 里的排队)
//   - 探测仍在线(已死就不用动作)
func (e *Engine) RunIdleWatcher() {
	go func() {
		tick := time.NewTicker(60 * time.Second)
		defer tick.Stop()
		for range tick.C {
			c := e.cfg()
			if c.IdleStopMin <= 0 || e.proc == nil {
				continue
			}
			idle.mu.Lock()
			busy := time.Since(idle.lastBusy)
			idle.mu.Unlock()
			if busy < time.Duration(c.IdleStopMin)*time.Minute {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			run, pend, err := e.cli.Queue(ctx)
			cancel()
			if err != nil || len(run) > 0 || len(pend) > 0 {
				continue // 队列有活/探测失败:不动
			}
			if !e.cli.Alive(ctx2(3 * time.Second)) {
				continue
			}
			mins := c.IdleStopMin
			if err := e.stopManagedOnly(); err != nil {
				log.Printf("[comfy] ⚠ 空闲自动退出失败: %v", err)
				continue
			}
			log.Printf("[comfy] 空闲 %d 分钟无任务,已自动退出 ComfyUI 释放资源", mins)
		}
	}()
}

func ctx2(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	// 短命探测,泄漏一个 cancel 可忽略(每分钟最多一次)
	_ = cancel
	return ctx
}
