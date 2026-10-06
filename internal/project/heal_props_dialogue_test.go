package project

import (
	"strings"
	"testing"
)

// heal_props_dialogue_test.go 自愈两件的**逐句/逐件对账**（2026-10-06 审计）。
//
// 这两条都是"机器能判定、平台能确定做对"的事，所以判据落在这里，而不是靠提示词里的散文规则：
//
//	① 器物锚定的**唯一机制**挂在 `prop_ids` 上（道具不进 H3 参考图集合），而模型常常
//	   一个字都不写 id —— 此前对空引用是直接 skip，正文里写着"碑刀握在右手"，锚定却落空，
//	   那把刀每镜一个形状（实测那一集 25 镜提到刀/烙铁/名册、prop_ids 全空）。
//	② 台词标签此前只在**一个 `<d>` 都没有**时才补 —— 于是"计划 2 句、正文只写 1 句"
//	   既不补也不报：配音读正文的 `<d>`、字幕读 `dialogue` 字段，成片里那句只有字幕没有声音。

// propCards 照真实形态写的道具卡（中文名 + 英文外观串 + 描述）。
func propCards() []Prop {
	return []Prop{
		{ID: "prop_blade", Name: "碑刀", EN: "a thin worn stone-cutting blade with a hemp-wrapped handle",
			Description: "磨薄的旧碑石刀，柄缠麻布，刃口沁朱砂"},
		{ID: "prop_ledger", Name: "名册板", EN: "a palm-sized wooden ledger board",
			Description: "掌宽的木板名册，边角磨圆"},
	}
}

// TestFillMissingPropRefsByName 正文/动作里点了名的器物，要补上 prop_ids（否则一次都不锚）。
func TestFillMissingPropRefsByName(t *testing.T) {
	plan := &Plan{
		Props: propCards(),
		Shots: []Shot{
			// ① 正文提到碑刀、prop_ids 是空的 → 补
			{ID: 1, H3Prompt: "detailed_description:\n[Shot 1] 碑生把碑刀握在右手，刃口朝下。", Action: "他把碑刀握住"},
			// ② 已经有引用（哪怕拼法怪）→ **这一步不碰**（对号/去重是 RemapPropRefs 的事）
			{ID: 2, H3Prompt: "detailed_description:\n[Shot 1] 他把碑刀搁在石台上。", PropIDs: []string{"prop_ledger"}},
			// ③ 什么都没提到 → 不许趁空塞满道具
			{ID: 3, H3Prompt: "detailed_description:\n[Shot 1] 他推开门，屋里没有人。", Action: "推门"},
			// ④ 动作里提到（正文没提）→ 也要补
			{ID: 4, H3Prompt: "detailed_description:\n[Shot 1] 他低头看着那块板。", Action: "他把名册板翻开"},
			// ⑤ 正文里是英文外观串里的实词（"hemp-wrapped"）—— **不该**被当成点名（只认强键）
			{ID: 5, H3Prompt: "detailed_description:\n[Shot 1] A hemp-wrapped handle rests on the stone."},
		},
	}
	n := FillMissingPropRefs(plan)
	if n != 2 {
		t.Errorf("该补 2 处（镜1 的碑刀 + 镜4 的名册板），实际 %d", n)
	}
	if got := plan.Shots[0].PropIDs; len(got) != 1 || got[0] != "prop_blade" {
		t.Errorf("镜 1 该补上 prop_blade，实际 %v", got)
	}
	if got := plan.Shots[1].PropIDs; len(got) != 1 || got[0] != "prop_ledger" {
		t.Errorf("镜 2 已有引用，不许被改动，实际 %v", got)
	}
	if got := plan.Shots[2].PropIDs; len(got) != 0 {
		t.Errorf("没提到任何器物的镜不该被塞引用，实际 %v", got)
	}
	if got := plan.Shots[3].PropIDs; len(got) != 1 || got[0] != "prop_ledger" {
		t.Errorf("动作里点名的器物要补上，实际 %v", got)
	}
	if got := plan.Shots[4].PropIDs; len(got) != 0 {
		t.Errorf("英文外观串里的散词不算点名（宁可少锚也不锚错），实际 %v", got)
	}
	// 幂等：再跑一次不该重复补
	if again := FillMissingPropRefs(plan); again != 0 {
		t.Errorf("幂等性坏了：第二次还补了 %d 处", again)
	}
}

// TestFillMissingPropRefsPrefersLongestName 名字互为子串时只算一件（"名册板"命中后，"名册"不再重复）。
func TestFillMissingPropRefsPrefersLongestName(t *testing.T) {
	plan := &Plan{
		Props: []Prop{{ID: "prop_ledger", Name: "名册板"}, {ID: "prop_register", Name: "名册"}},
		Shots: []Shot{{ID: 1, H3Prompt: "detailed_description:\n[Shot 1] 他翻开名册板，指尖点在页角。"}},
	}
	FillMissingPropRefs(plan)
	got := plan.Shots[0].PropIDs
	if len(got) != 1 || got[0] != "prop_ledger" {
		t.Errorf("长名优先：该只补 prop_ledger（名册板），实际 %v —— 补成两件会让提示词里出现两个器物锚块", got)
	}
}

// TestFillMissingDialogueTagsReconcilesPerLine 逐句对账：只补**确实没说过**的那几句。
func TestFillMissingDialogueTagsReconcilesPerLine(t *testing.T) {
	// ① 部分缺：正文只写了第一句（标点已被官方口径归一成句号），第二句没写 → 只补第二句
	plan := &Plan{Shots: []Shot{{
		ID: 1,
		H3Prompt: "detailed_description:\n<Subject 1> is 谢无咎.\n[Shot 1] 谢无咎站在碑林入口。" +
			"<Subject 1> (S1) says: <d>[Chinese]你走不出这座碑林。</d>",
		Dialogue: []Line{{Speaker: "谢无咎", Text: "你走不出这座碑林"}, {Speaker: "沈砚", Text: "我不认"}},
	}}}
	if n := FillMissingDialogueTags(plan); n != 1 {
		t.Fatalf("该补 1 镜（第二句没写进正文），实际 %d", n)
	}
	got := plan.Shots[0].H3Prompt
	if strings.Count(got, "你走不出这座碑林") != 1 {
		t.Errorf("第一句**已经说过**（只是标点不同）不许重复追加 —— 重复 = 成片把同一句念两遍：\n%s", got)
	}
	if !strings.Contains(got, "我不认") {
		t.Errorf("该补的第二句没进正文：\n%s", got)
	}
	if strings.Count(got, "<d>") != 2 {
		t.Errorf("补完之后该有 2 个 <d>，实际 %d：\n%s", strings.Count(got, "<d>"), got)
	}
	// 幂等
	if again := FillMissingDialogueTags(plan); again != 0 {
		t.Errorf("幂等性坏了：第二次还补了 %d 镜", again)
	}
}

// TestFillMissingDialogueTagsBoundaries 边界：空正文不补、一个 <d> 都没有要全补、旁白也算一句。
func TestFillMissingDialogueTagsBoundaries(t *testing.T) {
	plan := &Plan{Shots: []Shot{
		// ① 正文是空的：不补 —— 往空正文里塞台词会造出"只有台词、没有画面描述"的镜，
		//    那是空提示词闸门 + 自纠环的事，这里不越界
		{ID: 1, Dialogue: []Line{{Speaker: "沈砚", Text: "我不认"}}},
		// ② 一个 <d> 都没有（2026-09-28 实测那 4 镜的形态）→ 两句台词 + 旁白全补
		{ID: 2, H3Prompt: "detailed_description:\n[Shot 1] 他站在碑林入口。",
			Dialogue:  []Line{{Speaker: "谢无咎", Text: "你走不出这座碑林"}},
			Narration: "远处的钟声停了"},
	}}
	if n := FillMissingDialogueTags(plan); n != 1 {
		t.Fatalf("只有镜 2 该补（镜 1 正文是空的），实际 %d", n)
	}
	if strings.TrimSpace(plan.Shots[0].H3Prompt) != "" {
		t.Errorf("空正文不该被塞进台词：%q", plan.Shots[0].H3Prompt)
	}
	got := plan.Shots[1].H3Prompt
	for _, want := range []string{"<d>[Chinese]你走不出这座碑林</d>", "<d>[Chinese]远处的钟声停了</d>", "lips remain completely closed"} {
		if !strings.Contains(got, want) {
			t.Errorf("补出来的正文缺少 %q：\n%s", want, got)
		}
	}
}

// TestHealPlanWiresBothFixes 接线：两条判据都必须在**唯一自愈入口**里真的被调用。
func TestHealPlanWiresBothFixes(t *testing.T) {
	plan := &Plan{
		Props: propCards(),
		Shots: []Shot{{
			ID:       1,
			H3Prompt: "detailed_description:\n[Shot 1] 碑生把碑刀握在右手。",
			Dialogue: []Line{{Speaker: "碑生", Text: "这字改了"}},
		}},
	}
	hr := HealPlan(plan)
	if hr.PropRefsFilled == 0 {
		t.Error("HealPlan 没接上 FillMissingPropRefs —— 器物锚定对空 prop_ids 依旧一次都不锚")
	}
	if hr.Dialogue == 0 {
		t.Error("HealPlan 没接上 FillMissingDialogueTags —— 计划里的台词不会进正文")
	}
	if len(plan.Shots[0].PropIDs) == 0 || !strings.Contains(plan.Shots[0].H3Prompt, "这字改了") {
		t.Error("自愈结果没写回方案（渲染读的是方案，不是计数）")
	}
}
