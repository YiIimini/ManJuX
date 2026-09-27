package project

import "strings"

// shot_table.go 官方「标准镜头信息表」的受控词表与自检阈值
// （依据 MiniMax-AI/MiniMax-H3 `skills/3d-animation-short-generator/references/shot-table-spec.md`
// STEP 5 六列镜头表 + STEP 5.5 强制自检）。
//
// ## 为什么平台要照着官方镜头表来
//
// 平台的 `plan.json` 就是这张镜头表：官方六列 ↔ 我们的字段是一一对应的 ——
//
//	Shot ID & Duration            → id / duration
//	Continuity Handoff            → end_state（尾帧状态）
//	Reference Anchors             → scene(地标/光态) + characters(身份) + refs
//	Hook Type                     → hook_type（本文件）
//	Shot Description (per-second) → action + h3_prompt
//	Audio & Dialogue Track        → dialogue / narration / h3_prompt 的 <d>
//
// 官方 STEP 5.5 是**强制**自检（不通过不许进下一步），其中三条机器判得出来，落在契约里：
// ①钩子密度 ②单镜主要角色数 ≤3 ③场景卡必须写"连续性地标 + 光态"。另外三条（时长上限、
// 每秒钟指令覆盖、跨镜连贯链）分别由 duration 的硬区间、end_state 判据、以及装配点的
// 提示词规则承担 —— 详见 docs/渲染脚本契约.md 的说明。

// PlanHookTypes 钩子类型**受控词表**（官方 STEP 5：one short label from a controlled vocabulary）。
// 逐字照抄官方示例：visual-joke / reversal / suspense / tender / chase / reveal / callback / expression-beat。
var PlanHookTypes = []string{
	"visual-joke", "reversal", "suspense", "tender", "chase", "reveal", "callback", "expression-beat",
}

// PlanHookDensityTypes 密度自检认的三种"强钩子"（官方 STEP 5.5 检查 1：每连续三镜至少一个 reveal/reversal/callback）。
var PlanHookDensityTypes = []string{"reveal", "reversal", "callback"}

// PlanHookStrongTypes 开篇镜与收尾镜必须带的强钩子（官方 STEP 5.5 检查 1 后半句）。
var PlanHookStrongTypes = []string{"visual-joke", "reversal", "reveal", "suspense", "tender"}

// PlanHookDensityWindow 钩子密度的滑动窗口（官方：at least one of every three consecutive shots）。
const PlanHookDensityWindow = 3

// PlanMaxImportantCharacters 单镜主要角色上限（官方 STEP 5.5 检查 3：no shot contains more than
// three important characters — 有画面动作或台词的角色）。超了模型会把注意力摊薄，
// 每个人都画不好，也更容易互相换脸。
const PlanMaxImportantCharacters = 3

// IsKnownHookType 钩子类型是否在受控词表里（空值不算，由调用方单独报"缺"）。
func IsKnownHookType(s string) bool {
	if s == "" {
		return false
	}
	for _, h := range PlanHookTypes {
		if h == s {
			return true
		}
	}
	return false
}

// hookTypeOf 取归一化后的钩子类型（去空白）。
func hookTypeOf(sh Shot) string { return strings.TrimSpace(sh.HookType) }

// InList 判断 s 是否在 list 里（钩子词表判据的小工具，避免各调用点各写一遍循环）。
func InList(s string, list []string) bool {
	for _, v := range list {
		if s == v {
			return true
		}
	}
	return false
}
