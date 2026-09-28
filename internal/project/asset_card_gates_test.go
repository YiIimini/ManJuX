package project

import (
	"strings"
	"testing"
)

// 角色卡 / 道具卡 / 三视图板的补充判据（欠账小项 #11/#21/#23/#24）。
func TestAssetCardGates(t *testing.T) {
	// ── #21 年龄锚 ─────────────────────────────────────────────────────────
	// 结构字段写了就是锚
	if got := AgeIssues([]Character{{Name: "沈砚", Kind: "human", Age: "19"}}); len(got) != 0 {
		t.Errorf("写了 Age 字段的角色不该报缺年龄锚，实际 %v", got)
	}
	// 没字段但提示词里有年龄词（存量包）也算
	if got := AgeIssues([]Character{{Name: "周廷", Kind: "human",
		ImagePrompt: "a 45-year-old man, weathered face"}}); len(got) != 0 {
		t.Errorf("提示词里有年龄锚的存量卡不该报，实际 %v", got)
	}
	// 两处都没有 → 报（用户报过「中年人渲染出来跟个小年轻一样」）
	if got := AgeIssues([]Character{{Name: "无名", Kind: "human", ImagePrompt: "a man in robes"}}); len(got) != 1 {
		t.Errorf("既无字段也无锚的角色必须报，实际 %v", got)
	}
	// **器物/妖兽没有年龄这回事**，不许报
	if got := AgeIssues([]Character{{Name: "铜雀", Kind: "object"}, {Name: "青狐", Kind: "beast"}}); len(got) != 0 {
		t.Errorf("非人形实体不该要求年龄锚，实际 %v", got)
	}

	// ── #24 三视图板纯白底 ─────────────────────────────────────────────────
	if got := BoardWhiteBackdropIssues([]Character{{Name: "阿甲", Kind: "human",
		BoardPrompt: "reference sheet on a pure white seamless backdrop"}}); len(got) != 0 {
		t.Errorf("写了白底的板不该报，实际 %v", got)
	}
	if got := BoardWhiteBackdropIssues([]Character{{Name: "阿甲", Kind: "human",
		BoardPrompt: "reference sheet in a stone courtyard"}}); len(got) != 1 {
		t.Errorf("板子带场景必须报（规格书不是定妆照），实际 %v", got)
	}

	// ── #23 道具材质 ───────────────────────────────────────────────────────
	rich := Prop{Name: "旧柴刀", ImagePrompt: "aged iron blade, hemp-wrapped wooden handle, worn leather cord"}
	if got := MaterialIssues([]Prop{rich}); len(got) != 0 {
		t.Errorf("三种材质的道具不该报，实际 %v", got)
	}
	poor := Prop{Name: "塑料片", Description: "一块白色的片", ImagePrompt: "a white plastic sheet"}
	if got := MaterialIssues([]Prop{poor}); len(got) != 1 {
		t.Errorf("材质单薄的道具必须报（道具图是拿卡单独出的），实际 %v", got)
	}

	// ── #11 场景配套物件（**只判下限方向**）───────────────────────────────
	if got := ScenePropGroupIssues([]Scene{{Name: "破庙",
		ImagePrompt: "two groups of fixed furnishings, a stone censer and a leaning pillar base in the midground"}}); len(got) != 0 {
		t.Errorf("提了配套物件的场景不该报，实际 %v", got)
	}
	if got := ScenePropGroupIssues([]Scene{{Name: "荒原",
		ImagePrompt: "an empty wasteland under a grey sky"}}); len(got) != 1 {
		t.Errorf("一处物件都没提的场景必须报，实际 %v", got)
	}
	// 判据的边界写清楚：**它不数"几组"**（平台数不出），只报"一处都没有"
	one := ScenePropGroupIssues([]Scene{{Name: "走廊", ImagePrompt: "a single set of hanging lanterns"}})
	if len(one) != 0 {
		t.Errorf("提了一组就不该报（这条只判下限方向，不猜够不够），实际 %v", one)
	}
}

// 人形判据的取值必须与 pipeline 的 KindHuman/KindChibi 对齐（project 不能 import pipeline，
// 所以只能靠这条测试钉住两边的字符串）。
func TestHumanoidKindMatchesPipelineValues(t *testing.T) {
	if !humanoidKind("human") || !humanoidKind("chibi") {
		t.Error("human / chibi 必须判为人形")
	}
	if humanoidKind("object") || humanoidKind("beast") || humanoidKind("") {
		t.Error("器物/妖兽/空值不是人形")
	}
	if !strings.EqualFold("HUMAN", "human") || !humanoidKind("  Human  ") {
		t.Error("判据该容忍大小写与空白（技能侧字段不总是干净的）")
	}
}
