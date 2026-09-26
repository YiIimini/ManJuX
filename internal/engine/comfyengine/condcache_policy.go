package comfyengine

import (
	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// condCacheEnabled 本次渲染是否启用两阶段条件缓存。
//
// 判据顺序:**任务显式表态 > 全局设置**。
//
// 为什么不能让全局设置一个人说了算:项目参数抽屉里一直有 render.cond_cache(默认开),
// 而此前唯一的判据是全局 comfy.cond_cache —— 用户在项目里关掉它,只要全局开着就照样写
// .pt(代码里记过该目录堆到 4.9GB);反过来项目开着而全局关着,除了"用了云端引擎"那一
// 种场合外没人告知。开关能点、能存、无日志、无效果,属于审计点名的"假开关"。
//
// 现在管线把项目值经 engine.Job.CondCache 传下来(未表态时为 nil),这里据此裁决;
// 其它调用方(音频驱动/局部重绘等一次性操作)不传该字段,行为与以前完全一致。
func condCacheEnabled(job engine.Job, c config.ComfySettings) bool {
	if job.CondCache != nil {
		return *job.CondCache
	}
	return c.CondCache
}
