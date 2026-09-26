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
//
// **每个引擎调用都必须"进也打点、出也打点"**:long 作业(单镜 20+ 分钟)只在入口
// 打点的话,lastBusy 在整个执行期都是旧的,而巡检只看这一个时间戳 —— 一旦落在
// 镜与镜之间的空档(队列恰好空、管线正在下载/后处理),引擎就被当成空闲杀掉
// (2026-09-26 渲染管线审查)。收尾打点把镜间空档压回秒级。
func touchBusy() {
	idle.mu.Lock()
	idle.lastBusy = time.Now()
	idle.mu.Unlock()
}

// lastBusyAt 读最近一次使用时刻(测试与诊断用)。
func lastBusyAt() time.Time {
	idle.mu.Lock()
	defer idle.mu.Unlock()
	return idle.lastBusy
}

// shouldIdleStop 是否该停掉代管引擎。抽成纯函数便于单测 —— 这里的判据直接决定
// "镜间空档会不会把正在产线的引擎杀掉"。
//
// 四个条件全满足才动手:代管启动(外部实例不动)、距最近一次**引擎调用**(进/出各一次)
// 超过 idle 分钟、ComfyUI 队列空(不打断用户在 WEB 里的排队)、探测仍在线。
//
// 为什么不能拿"管线在不在跑"当判据:comfyengine 是被 pipeline 依赖的下层包,
// 反向 import 会成环。因此这里靠"进出都打点"把镜间空档压到秒级(见 touchBusy),
// 再叠加队列判据 —— 长作业期间该镜始终在 ComfyUI 队列里,队列非空即不杀。
func shouldIdleStop(lastBusy, now time.Time, idleMin int, owned, queueBusy, alive bool) bool {
	if idleMin <= 0 || !owned {
		return false
	}
	if now.Sub(lastBusy) < time.Duration(idleMin)*time.Minute {
		return false
	}
	if queueBusy || !alive {
		return false
	}
	return true
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
			if c.IdleStopMin <= 0 || e.ownedProc() == nil {
				continue
			}
			// 先做最便宜的一步:没到空闲阈值就不必去问队列
			last := lastBusyAt()
			if time.Since(last) < time.Duration(c.IdleStopMin)*time.Minute {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			run, pend, err := e.client().Queue(ctx)
			cancel()
			queueBusy := err != nil || len(run) > 0 || len(pend) > 0
			if !shouldIdleStop(last, time.Now(), c.IdleStopMin, true, queueBusy, e.client().Alive(ctx2(3*time.Second))) {
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
