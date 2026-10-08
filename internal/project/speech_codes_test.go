package project

import (
	"strings"
	"testing"
)

// realCodeLines 真机第01集台词里**全部带字母编号的句子**（照 `plan\plan.json` 原文抄）：
// 镜 1/6「钢印 U-80001」、镜 8「U-80001。你们归我。」、镜 12「钢印 U-80000」。
var realCodeLines = []struct {
	shot int
	text string
}{
	{1, "硬件钢印 U-80001。"},
	{6, "钢印 U-80001。"},
	{8, "U-80001。你们归我。"},
	{12, "重新检测到生命反应，钢印 U-80000。"},
}

// TestPronounceCodesInTextRealLines 真机那四句：读法逐字对上（字母表读音 + 数字逐位）。
func TestPronounceCodesInTextRealLines(t *testing.T) {
	cases := []struct{ in, want string }{
		{"硬件钢印 U-80001。", "硬件钢印 尤八零零零一。"},
		{"钢印 U-80001。", "钢印 尤八零零零一。"},
		{"U-80001。你们归我。", "尤八零零零一。你们归我。"},
		{"重新检测到生命反应，钢印 U-80000。", "重新检测到生命反应，钢印 尤八零零零零。"},
		{"U80001", "尤八零零零一"},         // 没有连字符的写法
		{"名册最后一行是多少。", "名册最后一行是多少。"}, // 没有编号：一个字不动
		{"七十二标准时。", "七十二标准时。"},       // 汉字数字不动
		{"八万。", "八万。"},               // 剧本自己写好的中文读法不动
	}
	for _, c := range cases {
		if got := PronounceCodesInText(c.in); got != c.want {
			t.Errorf("PronounceCodesInText(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	// 幂等：读法是纯汉字，再跑一遍不变。
	for _, c := range cases {
		once := PronounceCodesInText(c.in)
		if twice := PronounceCodesInText(once); twice != once {
			t.Errorf("不幂等：%q → %q → %q", c.in, once, twice)
		}
	}
}

// TestNormalizeSpokenCodesOnlyTouchesTags 只改 `<d>` 里的编号：标签外（含 dialogue 通道）一个字不动。
//
// 真机形态照抄：`<d>[Chinese]硬件钢印 U-80001。</d>`，同一句在正文别处也出现过（旁白/描述里）。
func TestNormalizeSpokenCodesOnlyTouchesTags(t *testing.T) {
	plan := &Plan{Shots: []Shot{{
		ID: 1,
		H3Prompt: "[Shot 2] At 00:02.500 the bay speaker says: <d>[Chinese]硬件钢印 U-80001。</d>\n" +
			"overall_soundscape: the U-80001 stamp glows on the panel.\n",
		Dialogue: []Line{{Speaker: "舱内系统", Text: "硬件钢印 U-80001。"}},
	}}}
	n, ids, codes := NormalizeSpokenCodes(plan)
	if n != 1 || len(ids) != 1 || ids[0] != "#1" || codes != 1 {
		t.Fatalf("应改 1 镜 1 处，实得 %d 镜 %v %d 处", n, ids, codes)
	}
	got := plan.Shots[0].H3Prompt
	if !strings.Contains(got, "<d>[Chinese]硬件钢印 尤八零零零一。</d>") {
		t.Errorf("`<d>` 里的编号没换成读法：\n%s", got)
	}
	if !strings.Contains(got, "Language tag") && strings.Contains(got, "<d>[Chinese]硬件钢印 U-80001。</d>") {
		t.Error("`<d>` 里还是原编号")
	}
	if !strings.Contains(got, "the U-80001 stamp glows") {
		t.Error("标签外的正文被改了 —— 只该动 `<d>` 里的台词")
	}
	if plan.Shots[0].Dialogue[0].Text != "硬件钢印 U-80001。" {
		t.Errorf("dialogue（字幕源）被改了：%q", plan.Shots[0].Dialogue[0].Text)
	}
	// 幂等
	if n2, _, _ := NormalizeSpokenCodes(plan); n2 != 0 {
		t.Errorf("第二次不该再改，实得 %d 镜", n2)
	}
}

// TestCodeNormalizationKeepsGatesQuiet 换了读法之后，本仓那两道"声音↔字幕"判据不许报警、不许刷回去。
//
// 这两件事此前会互相打架：逐句对账比"归一化后的文本"，对齐又按 dialogue 原文改写 `<d>` ——
// 若不在同一个归一化口径里把编号折算成读法，读法会被判成"改了词"（warn）并被对齐刷回原编号。
func TestCodeNormalizationKeepsGatesQuiet(t *testing.T) {
	sh := &Shot{
		ID:       6,
		H3Prompt: "[Shot 2] At 00:02.500 says: <d>[Chinese]钢印 U-80001。</d>",
		Dialogue: []Line{{Speaker: "舱内系统", Text: "钢印 U-80001。"}},
	}
	plan := &Plan{Shots: []Shot{*sh}}
	NormalizeSpokenCodes(plan)

	var msgs []string
	checkSpokenTextMatchesDialogue(plan, func(level, where, format string, a ...any) {
		msgs = append(msgs, level+": "+format)
	})
	for _, m := range msgs {
		if strings.Contains(m, "台词") {
			t.Errorf("换了读法之后逐句对账不该再报台词问题，实得：%s", m)
		}
	}
	before := plan.Shots[0].H3Prompt
	if n := AlignDialogueTags(plan); n != 0 {
		t.Errorf("对齐不该把读法刷回原编号（改了 %d 镜）", n)
	}
	if plan.Shots[0].H3Prompt != before {
		t.Errorf("对齐改动了提示词：\n前：%s\n后：%s", before, plan.Shots[0].H3Prompt)
	}
	// 而且计划台词（QC 的权威版）必须是**可听版**
	want := []string{"钢印 尤八零零零一。"}
	if got := PlanSpokenTexts(&plan.Shots[0]); len(got) != 1 || got[0] != want[0] {
		t.Errorf("PlanSpokenTexts 应给可听版 %v，实得 %v", want, got)
	}
}

// TestHealPlanFillsCodes 唯一读取边界上真的做了这件事（否则渲染端看到的还是原编号）。
func TestHealPlanFillsCodes(t *testing.T) {
	plan := &Plan{Shots: []Shot{{
		ID:       1,
		H3Prompt: "[Shot 2] At 00:02.500 says: <d>[Chinese]硬件钢印 U-80001。</d>",
		Dialogue: []Line{{Speaker: "舱内系统", Text: "硬件钢印 U-80001。"}},
	}}}
	hr := HealPlan(plan)
	if hr.CodeShots != 1 || hr.CodeCount != 1 || len(hr.CodeIDs) != 1 {
		t.Fatalf("HealPlan 应记下 1 镜 1 处，实得 %d 镜 / %d 处 / %v", hr.CodeShots, hr.CodeCount, hr.CodeIDs)
	}
	if !strings.Contains(plan.Shots[0].H3Prompt, "尤八零零零一") {
		t.Errorf("HealPlan 之后 `<d>` 里仍是原编号：\n%s", plan.Shots[0].H3Prompt)
	}
	if gaps := CodeGaps(plan); len(gaps) != 0 {
		t.Errorf("补过之后不该再点名未定读音：%v", gaps)
	}
	if CodeSummary(hr.CodeIDs, hr.CodeCount) == "" {
		t.Error("要有给日志用的话（补了什么必须看得见）")
	}
}

// TestCodeGapsPointsAtUnreadCodes 绕过读取边界的方案要能被点名。
func TestCodeGapsPointsAtUnreadCodes(t *testing.T) {
	p := &Plan{Shots: []Shot{
		{ID: 3, H3Prompt: "[Shot 1] says: <d>[Chinese]钢印 U-80001。</d>"},
		{ID: 4, H3Prompt: "[Shot 1] says: <d>[Chinese]该钢印不在名册内。</d>"},
	}}
	gaps := CodeGaps(p)
	if len(gaps) != 1 || gaps[0] != "#3" {
		t.Errorf("该点名 #3，实得 %v", gaps)
	}
}
