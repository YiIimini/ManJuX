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

// 【夹具必须照真实形态写】每一刀**各占一行** —— 正文里的多镜形式是一行一刀
// （解析器的正则锚在行首：`retention_analysis` 段里还有 `(appears in [Shot 1])` 这类**引用**，
// 不锚行首就会把引用当切点）。第一版夹具把四刀挤在同一行，解析器读回 0 刀 —— 门当场就响了。
func cutPromptFixture() string {
	return "subject_definitions:\n<Subject 1> is 沈砚.\n\n" +
		// 风格句**单独一行**，`[Shot 1]` 自己在行首 —— 解析器的正则锚行首，
		// 把 `[Shot 1]` 跟在 "GUOFENG." 后面会让它一枪不放（第一版夹具就这么错的，门当场响了）。
		"detailed_description:\nGUOFENG.\n" +
		"[Shot 1] he steps onto the stone platform.\n" +
		"[Shot 2] At 00:02.500, the overseer raises a hand to stop him.\n" +
		"[Shot 3] At 00:05.000, he sets the blade down, edge first.\n" +
		"[Shot 4] At 00:07.500, the crowd leans in as the ledger page turns.\n"
}

// TestCutsAreBackfilledAtTheReadBoundary 小镜头结构必须**从正文回填进结构字段**（2026-10-06 审计 §八-11）。
//
// 原状：权威字段 `shot.cuts` 在平台生成链上从来没被写过（schema 没要它、也没人回填），而分镜实际
// 把内切写在正文里 ⇒ `CutIssues` 的「每刀 1.5–4 秒 / 首刀为 0 / 时间点严格递增 / 各刀之和 = 片段时长」
// 四条判据**永久空转**。回填之后这批判据才开始工作 —— 本测试同时钉住"回填"与"判据真的会响"。
func TestCutsAreBackfilledAtTheReadBoundary(t *testing.T) {
	plan := &Plan{Shots: []Shot{{ID: 1, Duration: 10, H3Prompt: cutPromptFixture()}}}
	hr := HealPlan(plan)
	if hr.Cuts != 1 {
		t.Fatalf("该回填 1 个片段的小镜头结构，实际 %d", hr.Cuts)
	}
	if n := len(plan.Shots[0].Cuts); n != 4 {
		t.Fatalf("这段正文切了 4 刀，回填出 %d 刀 —— 解析器或回填接错了", n)
	}
	// 这一段是**合规**的（4 刀 × 2.5 秒 = 10 秒）：判据不该报它
	if got := CutIssues(plan); len(got) != 0 {
		t.Errorf("合规的多刀片段被误报（回填出来的时间点算错了）：%v", got)
	}
	// 幂等：已有结构数据时不覆盖
	if again := plan.FillCutsFromPrompt(); again != 0 {
		t.Errorf("已经回填过还再回填了 %d 次（会覆盖人工改过的结构）", again)
	}
}

// TestBackfilledCutsMakeCutIssuesLive 回填之后**判据真的会响**：
// 把最后一刀挪到片段之外（正文里写超了时长）→ `CutIssues` 必须报出来。
//
// 这一条挡的是"回填了但没人消费"这种假修：`CutIssues` 是 warn 级契约门
// （`plan_checks.go` 的 `for _, msg := range CutIssues(p)`），它响了用户才看得见。
func TestBackfilledCutsMakeCutIssuesLive(t *testing.T) {
	bad := cutPromptFixture() + "[Shot 5] At 00:11.500, the light goes out.\n"
	plan := &Plan{Shots: []Shot{{ID: 1, Duration: 10, H3Prompt: bad}}}
	HealPlan(plan)
	if len(plan.Shots[0].Cuts) != 5 {
		t.Fatalf("夹具/解析不对：该读回 5 刀，实际 %d", len(plan.Shots[0].Cuts))
	}
	msgs := strings.Join(CutIssues(plan), " ")
	if msgs == "" {
		t.Fatal("回填之后 CutIssues 仍然不吭声 —— 那这几条判据等于没接上")
	}
	if !strings.Contains(msgs, "片段时长之外") {
		t.Errorf("第 5 刀落在 10 秒片段之外，该报「有刀落在片段时长之外」，实际：%s", msgs)
	}
}
