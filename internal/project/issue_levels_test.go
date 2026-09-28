package project

import (
	"strings"
	"testing"
)

// 契约门的**三级制**（2026-09-28）：fail / warn / info。
//
// info 的语义是"平台**已经自动处理**，或仅供参考" —— 它必须存在，因为把"平台自己会补齐"的事
// 报成 warn，会让真正的警告淹在噪音里（平台自己的话：假告警会让人不再看告警）。
// 真方案上此前 22 条 warn 里有 3 条属于这一类（画外音闭嘴句 ×2、建立镜头串 ×1）。
//
// 这条守卫钉三件事：
//  1. 用 info 的门**确实存在**（哪天被人改回 warn，这里会红）；
//  2. info 的话术里必须写明"平台会/已处理"——不能让人以为是需要自己动手的问题；
//  3. info **不算 fail**（不能拦住渲染）。
func TestInfoLevelMeansAutoHandled(t *testing.T) {
	p := samplePlan()
	// 造一个"画外音没写闭嘴句"的镜
	// vo_kind 是**判据的第一顺位**（见 HasVoiceover）：直接标成真画外，比靠字段启发式更明确
	p.Shots[0].VoKind = VoKindNarration
	p.Shots[0].Narration = "雨还在下。"
	p.Shots[0].H3Prompt = strings.ReplaceAll(p.Shots[0].H3Prompt, VoiceoverClauseMarker, "")
	issues := ValidatePlan(p)
	found := false
	for _, it := range issues {
		if it.Level == "info" && strings.Contains(it.Msg, VoiceoverClauseMarker) {
			found = true
			// 注意别被 Markdown 的 ** 分隔：查"自动补齐"这个稳定的子串
			if !strings.Contains(it.Msg, "自动补齐") {
				t.Errorf("info 话术必须写明平台已处理，实际 %q", it.Msg)
			}
		}
		if it.Level == "info" && strings.Contains(it.Msg, "请") && !strings.Contains(it.Msg, "不需要你") {
			t.Errorf("info 不该要求用户做事（除非明确说明不需要），实际 %q", it.Msg)
		}
	}
	if !found {
		t.Error("画外音缺闭嘴句这条应是 info 级（平台会补齐，用户无事可做）")
	}
	// info 不得出现在 fail 集合里（否则会被"入口拒收"的逻辑当成硬失败）
	for _, it := range PlanFails(issues) {
		if it.Level != "fail" {
			t.Errorf("PlanFails 只该含 fail，实际含 %s", it.Level)
		}
	}
}
