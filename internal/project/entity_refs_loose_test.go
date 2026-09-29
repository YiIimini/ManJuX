package project

import (
	"strings"
	"testing"
)

// 分镜引用的场景名**对不上场景卡**时，要按字面重合兜底对上号（2026-09-29 真机实测）。
//
// 【现场】第 01 集：分镜写的是「归墟环断口维护舱」，而 ④场景 会话产出的场景卡是
// 「归墟环维护舱大厅 / 东头值班终端台 / 舱门裂隙外的灰雾」——
// 名字对不上 ⇒ 归一失败 ⇒ 那一镜装配不到场景参考图
// （日志「⚠ 镜头 1 缺 1 张参考图(归墟环断口维护舱),提示词中的画面编号会随之错位」），
// 环境每镜重编、背景逐镜漂移。
//
// 治根在生成侧（④ 先跑、⑤ 后跑，见 director.GenerateSessions）；这里治**存量方案**
// 与"模型就是写歪了"。
//
// 【纪律】必须有一个**明确赢家**（重合度 ≥2 且严格高于第二名）；两张卡分数相同时宁可
// 报"对不上" —— 把 A 的场景板锚到 B 身上，比没有参考图更糟。
func TestSceneNameMismatchFallsBackToClosestCard(t *testing.T) {
	plan := &Plan{
		Scenes: []Scene{
			{ID: "scene_ring_bay", Name: "归墟环维护舱大厅"},
			{ID: "scene_duty_terminal", Name: "东头值班终端台"},
			{ID: "scene_hatch_crack", Name: "舱门裂隙外的灰雾"},
		},
		Characters: []Character{{ID: "char_qiqi", Name: "祁七"}},
		Shots: []Shot{
			{ID: 1, Scene: "归墟环断口维护舱", Characters: []string{"祁七"}},              // 真机原文
			{ID: 2, Scene: "scene_ring_bay", Characters: []string{"char_qiqi"}}, // 已经是 id：幂等
		},
	}
	fixed, unknown := RemapEntityRefs(plan)
	if plan.Shots[0].Scene != "scene_ring_bay" {
		t.Fatalf("对不上号的场景名没有兜底对上：%q（这一镜就没有环境参考图了）", plan.Shots[0].Scene)
	}
	if plan.Shots[1].Scene != "scene_ring_bay" {
		t.Fatalf("已经是 id 的引用被改动了：%q", plan.Shots[1].Scene)
	}
	if fixed != 2 || unknown != 0 {
		t.Fatalf("计数不对：fixed=%d unknown=%d（第 1 镜的场景名与角色名各归一 1 处；unknown>0 就是还有镜头拿不到参考图）", fixed, unknown)
	}
	// 门（G108/G109）读的是这一对数字 —— 兜底生效后不该再报"引用不存在"
	if c, s := DanglingEntityRefs(plan); c != 0 || s != 0 {
		t.Fatalf("兜底之后仍有悬空引用：chars=%d scenes=%d", c, s)
	}

	// 明确赢家才认：三张卡都沾一点边时不许硬凑
	ambiguous := &Plan{
		Scenes: []Scene{
			{ID: "s1", Name: "甲号舱室东侧"},
			{ID: "s2", Name: "甲号舱室西侧"},
		},
		Shots: []Shot{{ID: 1, Scene: "甲号舱室"}},
	}
	_, unk := RemapEntityRefs(ambiguous)
	if ambiguous.Shots[0].Scene != "甲号舱室" {
		t.Fatal("两张卡同分时不许挑一个 —— 认错场景比没有参考图更糟")
	}
	if unk != 1 {
		t.Fatalf("同分时应当如实报「对不上」，实得 unknown=%d", unk)
	}

	// 完全不像的名字不许被硬塞给某张卡
	far := &Plan{
		Scenes: []Scene{{ID: "s1", Name: "归墟环维护舱大厅"}},
		Shots:  []Shot{{ID: 1, Scene: "城南破庙"}},
	}
	_, unk2 := RemapEntityRefs(far)
	if far.Shots[0].Scene != "城南破庙" || unk2 != 1 {
		t.Fatalf("毫不相干的名字被硬对号了：%q unknown=%d", far.Shots[0].Scene, unk2)
	}
	if !strings.Contains("归墟环维护舱大厅", "归墟") { // 夹具自检：别把名字写错
		t.Fatal("夹具坏了")
	}
}
