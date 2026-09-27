// idle.go ComfyUI 空闲治理:管线结束后 N 分钟无任务(渲染/生成/队列)则**卸载模型释放显存**,
// **进程常驻**。
//
// ## 为什么不是"空闲退出引擎"(2026-09-26 用户报障修正)
//
// 原实现是"空闲 N 分钟停掉代管进程树(stopManagedOnly)"。用户点停止项目后过一会儿发现
// ComfyUI 整没了,原话:「平台中停止项目,ComfyUI 只需要释放显存,为什么要关闭它?
// 只有退出平台进程才关闭 ComfyUI」。
//
// 这条批评是对的,而且不只是观感:关进程与卸模型**都能把显存让出去**,而关进程多付一次
// 冷启动(下次渲染要把 20GB 权重重新加载),还顺手把"用户自己开着的那个 ComfyUI 网页"
// 一起端掉(他可能正在 WEB 里试提示词)。
//
// 引擎进程的关闭时机**只有一个** —— 退出平台(main.go 的 OnShutdown 走 StopAdmin,
// 代管树 + rootDir 内 python 全清,不留孤儿)。管理页那颗「停止引擎」按钮是用户主动要求,
// 与"自动"无关。
//
// 因此看门狗的动作收敛为 `FreeVRAM`(/free = unload_models + empty_cache):
// 显存交还出去,引擎随时候命。状态变化经 notifyLifecycle 通知的路径只剩手动启停。
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
// 镜与镜之间的空档(队列恰好空、管线正在下载/后处理),引擎就被当成空闲处理
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

// shouldIdleFree 是否该卸模型释放显存。抽成纯函数便于单测 —— 这里的判据直接决定
// "镜间空档会不会把正在产线的引擎的权重卸掉"。
//
// 四个条件全满足才动手:代管启动(外部实例不动)、距最近一次**引擎调用**(进/出各一次)
// 超过 idle 分钟、ComfyUI 队列空(不打断用户在 WEB 里的排队)、探测仍在线。
//
// 为什么不能拿"管线在不在跑"当判据:comfyengine 是被 pipeline 依赖的下层包,
// 反向 import 会成环。因此这里靠"进出都打点"把镜间空档压到秒级(见 touchBusy),
// 再叠加队列判据 —— 长作业期间该镜始终在 ComfyUI 队列里,队列非空即不卸。
func shouldIdleFree(lastBusy, now time.Time, idleMin int, owned, queueBusy, alive bool) bool {
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
// 动作条件全部满足才卸模型:
//   - 代管启动(e.proc != nil / pidfile 认领得到,外部实例不动)
//   - 距最近一次使用超过 idle 分钟(设置 0 = 不卸)
//   - ComfyUI 队列空(running+pending 都空,不打断用户在 WEB 里的排队)
//   - 探测仍在线(已死就不用动作)
func (e *Engine) RunIdleWatcher() {
	go func() {
		tick := time.NewTicker(60 * time.Second)
		defer tick.Stop()
		for range tick.C {
			c := e.cfg()
			if c.IdleFreeMin <= 0 {
				continue
			}
			// 应用重启过的话,代管句柄只在 pidfile 里 —— 先认领一次再判"是不是代管",
			// 否则重启之后空闲治理整条失效(审计 L7:看门狗从不调 adoptFromPidFile)。
			// 认领只读 pid + 验映像是 python,认不到就跳过,**绝不去动别人的进程**。
			if e.ownedProc() == nil && !e.adoptFromPidFile() {
				continue
			}
			// 先做最便宜的一步:没到空闲阈值就不必去问队列
			last := lastBusyAt()
			if time.Since(last) < time.Duration(c.IdleFreeMin)*time.Minute {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			run, pend, err := e.client().Queue(ctx)
			cancel()
			queueBusy := err != nil || len(run) > 0 || len(pend) > 0
			if !shouldIdleFree(last, time.Now(), c.IdleFreeMin, true, queueBusy, e.client().Alive(ctx2(3*time.Second))) {
				continue
			}
			// 卸载 20GB 权重可能要几十秒,给足时间;失败只记日志 —— 这是个"省显存"的
			// 优化动作,不该因为一次失败就动进程或中断什么。
			ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
			err = e.FreeVRAM(ctx)
			cancel()
			if err != nil {
				log.Printf("[comfy] ⚠ 空闲释放显存失败(引擎不受影响,下一轮再试): %v", err)
				continue
			}
			// 打点:把下一次评估推到 idle 分钟之后,否则每分钟都会重复卸载一遍
			touchBusy()
			log.Printf("[comfy] 空闲 %d 分钟无任务,已卸载模型释放显存(进程常驻,下次渲染自动重新加载)", c.IdleFreeMin)
		}
	}()
}

func ctx2(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	// 短命探测,泄漏一个 cancel 可忽略(每分钟最多一次)
	_ = cancel
	return ctx
}
