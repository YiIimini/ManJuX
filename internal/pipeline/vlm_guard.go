package pipeline

import (
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// vlm_guard.go 视觉模型的**限流熔断 + 调用预算**。
//
// ## 为什么要有（2026-09-27 用户报障）
//
// 用户在资产阶段连着重跑，看到：
//
//	⚠ 定妆照「周廷」比例自检失败(未判定,不当作通过):VLM HTTP 429: …"该模型当前访问量过大"
//
// 三件事叠在一起变成"越重跑越糟"：
//
//	① 资产阶段每个角色都要跑**比例自检**（每张一次视觉调用）；
//	② 那天新加的**撞脸自检**每个角色再比 1~3 次（30 个角色最多 90 次）；
//	③ 用户为了修一张脸会反复重跑同一段 —— 上一轮的判定完全没记账，每次从头再问一遍。
//
// 于是接口被压到限流，而当时的处置是"每张卡各报一条告警、继续往下打"。
// 这里的处置分三层：
//
//	① **认限流**（llm.IsRateLimited）→ 进入**静默期**（进程级，默认 3 分钟）：
//	   期间不再发起任何视觉调用，只播报一次（说清"暂停多久、不影响出图与渲染、到点自动恢复"）；
//	② **调用预算**（每次资产阶段开始重置）：一轮最多 maxVLMCallsPerRun 次，超了就不再问
//	   —— 增强项不该把主流程拖成"接口打满"；
//	③ 判定**按图片指纹记账**（见 cast_distinct.go 的缓存）→ 重跑时同一对脸不再重复问。
//
// 静默期是**进程级**而不是"本次运行"级别：限流是同一个 key 的全局状态，
// 换个项目重来一遍照样会被拒。
//
// vlmCooldown 是**变量**而非 const：测试要把它压到毫秒级（否则测一次要等 3 分钟）。
var vlmCooldown = 3 * time.Minute

const maxVLMCallsPerRun = 12

// vlmGuard 视觉调用的闸门（进程级）。
type vlmGuard struct {
	mu       sync.Mutex
	until    time.Time // 静默期截止
	notified bool      // 这一轮静默期是否已经播报过
	budget   int       // 本次资产阶段的剩余预算
	// budgetNotified 本轮的"预算用完"是否已播报（与限流同理：一条信息只说一次）。
	budgetNotified bool
}

// vlmAllowed 现在能不能调视觉模型。被限流后的静默期内为 false。
func (c *Coordinator) vlmAllowed() bool {
	g := &c.vlm
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().After(g.until)
}

// vlmNote 记录一次视觉调用结果：限流 → 进静默期 + 只播报一次；成功 → 不动静默期。
//
// 为什么只播报一次：限流期间每张卡都报一条，用户看到的是刷屏，而信息量只有一条
// （"接口被限流了，等一会儿"）。这与比例自检"一次运行只提醒一次"是同一条政策。
func (c *Coordinator) vlmNote(proj *project.Project, err error) {
	if err == nil {
		return
	}
	if !llm.IsRateLimited(err) {
		return // 其它错误（解析/网络/图太小）由调用方各自如实告警，不进静默期
	}
	g := &c.vlm
	g.mu.Lock()
	g.until = time.Now().Add(vlmCooldown)
	first := !g.notified
	g.notified = true
	g.mu.Unlock()
	if first && proj != nil {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
			"⚠ 视觉模型被限流（HTTP 429/额度）：**本机所有看图自检暂停 %d 分钟**（比例/撞脸/多视角校验），"+
				"到点自动恢复；出图与渲染**不受影响**，只是这几张卡少一次自动核对",
			int(vlmCooldown.Minutes()))
	}
}

// VLMResetBudget 进入资产阶段前重置调用预算，并清掉上一轮的"已播报"标记。
func (c *Coordinator) VLMResetBudget() {
	g := &c.vlm
	g.mu.Lock()
	g.budget = maxVLMCallsPerRun
	g.notified = false
	g.budgetNotified = false
	g.mu.Unlock()
}

// vlmBudgetTake 取一次视觉调用预算（用尽返回 false）。
func (c *Coordinator) vlmBudgetTake() bool {
	g := &c.vlm
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.budget <= 0 {
		return false
	}
	g.budget--
	return true
}

// vlmSkipReason 现在能不能调；不能时给一句**给人看**的原因（空 = 可以调）。
//
// 播报归属：限流那条在**认出限流的那一刻**播报（见 vlmNote）；预算用完这条在这里播报一次
// —— 两条都遵守"同一个原因只说一次"，否则 30 个角色就是 30 条一样的告警。
func (c *Coordinator) vlmSkipReason(proj *project.Project) string {
	if !c.vlmAllowed() {
		return "视觉模型处于限流静默期"
	}
	if !c.vlmBudgetTake() {
		g := &c.vlm
		g.mu.Lock()
		first := !g.budgetNotified
		g.budgetNotified = true
		g.mu.Unlock()
		if first && proj != nil {
			c.emit(proj.ID, PhaseAssets, 0, "info", 0,
				"本次资产阶段的看图自检已达上限（%d 次），后面的卡跳过自动核对 —— "+
					"不影响出图与渲染；要重验可在资产详情里对单张点「重新生成」", maxVLMCallsPerRun)
		}
		return "本次运行的视觉调用预算已用完"
	}
	return ""
}
