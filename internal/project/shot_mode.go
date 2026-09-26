package project

import "strings"

// shot_mode.go 平台侧对 **mode 空值**的推断(契约承诺的那一半)。
//
// 【为什么必须有它】契约(plan_contract.go)对 mode 的规则是:
//
//	ref2va = 有参考图(角色/场景定妆),t2va = 纯文生;
//	空值 —— "空值平台会自行推断"。
//
// 而平台**从来没有推断过**:`stages_render.go` 只在 `mode == "ref2va"` 时才去装配参考图,
// 于是技能端产出的方案(shot 里根本没有 mode 字段)会**整片跳过参考图**,
// 每一镜都当纯文生渲染。2026-09-26 真机实测(求你们骂我·第01集):
//
//	13:53:30 ✔ 资产就绪:6/6(库复用 0)          ← 刚花 6 分钟生成的角色/场景定妆照
//	13:53:36 镜头 1/64 渲染开始(,4s,seed=…,参考图 0 张:角色 0)   ← 一张都没用上
//
// 后果不是"少一点像",而是**跨镜换脸**:资产阶段白跑,用户在成片里看到的正是他反复
// 报过的「主角的脸/头发漂移」。同一份 mode 还被另外三处当门用:
// `asset_scope.go`(该生成哪些资产)、`stages_assets.go`(要不要排多视角)、
// `final2k.go`(要不要升 2K)—— 全都因为空值被跳过。
//
// 【推断规则】空值 → `ref2va`,把"到底有没有参考图"交给渲染阶段既有的降级判据处理:
// 装配出 0 张就当场降级 t2va 并播报(stages_render.go 已实现,用户看得见)。
// 反过来推 `t2va` 才是错的:那等于替用户决定"这一镜不需要角色一致性",而它只是没写。
//
// 【为什么在 Plan 上做】三处门 + 渲染阶段看的是**同一份 plan**。在读取/导入的边界
// 统一推断一次,四处同时正确;散着改 `!= "ref2va"` 的判断,漏一处就回到今天这个 bug。

// ShotModeRef2VA / ShotModeT2VA 是契约里仅有的两个合法取值(空值由平台推断)。
const (
	ShotModeRef2VA = "ref2va"
	ShotModeT2VA   = "t2va"
	// ShotModeInferred 是"推断出来的"标记值,用于播报/测试断言,不落盘。
	ShotModeInferred = ShotModeRef2VA
)

// NormalizeShotModes 就地把 mode 空值推断成具体模式,返回被推断的镜头数。
//
// 已写死的值一个都不动 —— 技能端/LLM 明确说"这一镜纯文生"(t2va)时必须尊重,
// 契约已经把决定权给了写入方,平台只在**沉默**时才接手。
func (p *Plan) NormalizeShotModes() int {
	if p == nil {
		return 0
	}
	n := 0
	for i := range p.Shots {
		if strings.TrimSpace(p.Shots[i].Mode) != "" {
			continue
		}
		p.Shots[i].Mode = ShotModeRef2VA
		n++
	}
	return n
}

// EffectiveShotMode 单镜的**生效模式**(空值按 ref2va 解释)。
//
// 给那些手里只有一个 Shot、拿不到整个 Plan 的调用点用(例如渲染阶段的 mode 分支)。
// 它和 NormalizeShotModes 必须给出同一个答案 —— 闸门见 shot_mode_test.go。
func EffectiveShotMode(sh Shot) string {
	if m := strings.TrimSpace(sh.Mode); m != "" {
		return m
	}
	return ShotModeRef2VA
}
