package project

import (
	"strings"
	"testing"
)

// 分镜里的**名字引用**必须被归一到卡片 id（2026-09-29 真机实测驱动）。
//
// 夹具照真实形态抄：角色/场景的 id 与名字、以及分镜里 LLM 实际写下的那两行 ——
// 五步专职会话的 ⑤分镜 schema 是 `"scene":"场景名","characters":["角色名"]`
// （见 internal/director/sessions.go），实测第 01 集 13 镜**全部**因为对不上 id
// 而装配不到参考图、整集降级 t2va。
func TestRemapEntityRefsRealPlan(t *testing.T) {
	plan := &Plan{
		Characters: []Character{
			{ID: "char_qiqi", Name: "祁七"},
			{ID: "char_cangban", Name: "归墟环·维护舱主控语音"},
		},
		Scenes: []Scene{
			{ID: "scene_maintenance_bay", Name: "归墟环断口维护舱"},
			{ID: "scene_hull_breach", Name: "舱门裂缝外的灰域"},
		},
		Shots: []Shot{
			// ① 真实形态：名字（五步会话就是这么写的）
			{ID: 1, Scene: "归墟环断口维护舱", Characters: []string{"祁七"}},
			// ② 名字外面还带着书名号/尾注（模型常见的写法）
			{ID: 2, Scene: "《归墟环断口维护舱》", Characters: []string{"祁七（主角）", "归墟环·维护舱主控语音"}},
			// ③ 已经是 id 的：必须原样不动（幂等的前提）
			{ID: 3, Scene: "scene_hull_breach", Characters: []string{"char_cangban"}},
			// ④ 群演：对不上卡片，**原样保留**并计数（不许静默丢弃）
			{ID: 4, Scene: "scene_maintenance_bay", Characters: []string{"祁七", "路人甲"}},
		},
	}
	fixed, unknown, _ := RemapEntityRefs(plan)
	if fixed != 6 {
		t.Errorf("应归一到 6 处（镜1 场景+人物、镜2 场景+2 人物、镜3 无），实得 %d", fixed)
	}
	if unknown != 1 {
		t.Errorf("对不上的只该是镜 4 的「路人甲」，实得 %d", unknown)
	}
	if got := plan.Shots[0].Scene; got != "scene_maintenance_bay" {
		t.Errorf("镜 1 的场景应归一成 id，实得 %q", got)
	}
	if got := plan.Shots[0].Characters[0]; got != "char_qiqi" {
		t.Errorf("镜 1 的人物应归一成 id，实得 %q", got)
	}
	if got := plan.Shots[1].Characters[0]; got != "char_qiqi" {
		t.Errorf("带尾注的名字也要能对上（「祁七（主角）」），实得 %q", got)
	}
	if got := plan.Shots[2].Scene; got != "scene_hull_breach" {
		t.Errorf("已经是 id 的引用不该被动，实得 %q", got)
	}
	if got := plan.Shots[3].Characters[1]; got != "路人甲" {
		t.Errorf("群演引用必须原样保留（静默丢弃会让提示词与引用对不上），实得 %q", got)
	}
	// 幂等：再跑一次不该有改动（LoadPlan 每次读都会调 HealPlan）
	again, unknown2, _ := RemapEntityRefs(plan)
	if again != 0 || unknown2 != 1 {
		t.Errorf("第二次调用应零改动（幂等），实得 fixed=%d unknown=%d", again, unknown2)
	}
	// 归一之后的悬空数 = 剩下的那些
	if ch, sc := DanglingEntityRefs(plan); ch != 1 || sc != 0 {
		t.Errorf("悬空引用应为 人物 1 / 场景 0，实得 人物 %d / 场景 %d", ch, sc)
	}
}

// 接线：HealPlan 必须包含这一步（预检/人审/渲染三个消费方都要看到同一份方案）。
func TestHealPlanIncludesEntityRefs(t *testing.T) {
	plan := &Plan{
		Characters: []Character{{ID: "char_qiqi", Name: "祁七"}},
		Scenes:     []Scene{{ID: "scene_maintenance_bay", Name: "归墟环断口维护舱"}},
		Shots:      []Shot{{ID: 1, Scene: "归墟环断口维护舱", Characters: []string{"祁七"}}},
	}
	hr := HealPlan(plan)
	if hr.EntityRefs != 2 {
		t.Fatalf("HealPlan 应包含实体引用归一（应报 2 处），实得 %d", hr.EntityRefs)
	}
	if plan.Shots[0].Scene != "scene_maintenance_bay" || plan.Shots[0].Characters[0] != "char_qiqi" {
		t.Fatalf("HealPlan 之后分镜引用必须已是 id：%+v", plan.Shots[0])
	}
}

// 契约门：归一之后仍对不上卡片的引用要被**说出来**（这是"整集降级 t2va"的唯一前兆）。
func TestGateDanglingEntityRefs(t *testing.T) {
	plan := &Plan{
		Characters: []Character{{ID: "char_qiqi", Name: "祁七"}},
		Scenes:     []Scene{{ID: "scene_a", Name: "维护舱"}},
		Shots: []Shot{
			{ID: 1, Scene: "scene_a", Characters: []string{"char_qiqi"}},
			{ID: 2, Scene: "没建卡的场景", Characters: []string{"char_qiqi", "临时路人"}},
		},
	}
	issues := ValidatePlan(plan)
	var hitChar, hitScene bool
	for _, is := range issues {
		if strings.Contains(is.Msg, "引用的**人物**") {
			hitChar = true
		}
		if strings.Contains(is.Msg, "引用的**场景**") {
			hitScene = true
		}
	}
	if !hitChar || !hitScene {
		t.Fatalf("悬空的人物/场景引用各应有一道 warn（拿到参考图与否全靠它），实得 人物=%v 场景=%v\n%+v",
			hitChar, hitScene, issues)
	}
}
