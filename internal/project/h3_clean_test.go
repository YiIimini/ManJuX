package project

import (
	"strings"
	"testing"
)

// h3_clean_test.go 「提示词里的生成器指令行」剔除契约。
//
// 样本**逐字取自真实产出**（`data/小说/求你们骂我/漫剧脚本/第26集/plan/plan.json` 的
// `h3_prompt` 开头四行）—— 用真样本而不是我编的句子：这条守卫的价值全在"能不能认出真实形态"。

const realDirectiveHead = `subject_definitions:
REFERENCE ORDER: for each character in the shot's characters array, in array order, FIRST that character's front sheet, and THEN (if it has one) ONE multi-view picture of the SAME subject — never a second character. After all character pictures comes exactly ONE scene picture, always LAST. Props never get a picture; describe props in text only.
Locate every subject by PICTURE CONTENT, never by counting indexes: a front sheet and its multi-view strip are the SAME subject. Never state how many pictures are attached and never assert which picture number the scene is — the renderer appends an authoritative PICTURE MANIFEST that fixes the numbering. Always name the subject in words too.
<Subject 1> is 临水城 — the location itself, kept consistent with its reference plate. The scene is the LAST attached picture.
Every <Subject> must map to a picture that exists in the order above; never invent picture tags beyond that count. The attached sheets are the AUTHORITY on how each subject looks: describe wardrobe, props, pose and emotion only, and never add marks the sheet does not show. Never describe a non-human subject in human terms.

summary:
[reference generation] One continuous 7-second shot. The location follows its reference plate and no character is in frame.

retention_analysis:
- One line per reference picture, numbered exactly as in subject_definitions. Name the subject in words in every line so the render-time PICTURE MANIFEST can correct a numbering mismatch.
- Picture 1 — 临水城 (scene): weak_reference - scene layout and mood referenced, camera free.`

func TestStripH3WriterDirectivesOnRealSample(t *testing.T) {
	cleaned, n := StripH3WriterDirectives(realDirectiveHead)
	if n != 4 {
		t.Fatalf("真实样本里应剔掉 4 行指令，实际 %d 行：\n%s", n, cleaned)
	}
	for _, gone := range []string{"REFERENCE ORDER", "characters array", "Locate every subject by PICTURE CONTENT", "Every <Subject> must map"} {
		if strings.Contains(cleaned, gone) {
			t.Errorf("残留指令 %q：\n%s", gone, cleaned)
		}
	}
	// **内容必须一字不动**：段名、主体定义、summary、retention 条目都留着
	for _, keep := range []string{
		"subject_definitions:", "summary:", "retention_analysis:",
		"<Subject 1> is 临水城", "The scene is the LAST attached picture.",
		"[reference generation] One continuous 7-second shot.",
		"- Picture 1 — 临水城 (scene): weak_reference - scene layout and mood referenced, camera free.",
	} {
		if !strings.Contains(cleaned, keep) {
			t.Errorf("误删了正文 %q：\n%s", keep, cleaned)
		}
	}
	// 段名后面不该留一串空行
	if strings.Contains(cleaned, "subject_definitions:\n\n\n") {
		t.Errorf("剔除后应把连续空行收拢：\n%s", cleaned)
	}
	// 幂等
	if again, n2 := StripH3WriterDirectives(cleaned); n2 != 0 || again != cleaned {
		t.Fatalf("二次清理不该再动：n=%d\n%s", n2, again)
	}
}

func TestStripH3WriterDirectivesKeepsCleanPrompt(t *testing.T) {
	clean := "subject_definitions:\n<Subject 1> is Shen Yan — the young man in grey linen, identity locked.\n\n" +
		"summary:\n[reference generation] A 5-second close shot.\n\n" +
		"retention_analysis:\n<Subject 1> (appears in [Shot 1]): fully_preserved - face and costume held.\n"
	got, n := StripH3WriterDirectives(clean)
	if n != 0 || got != clean {
		t.Fatalf("干净提示词不该被改：n=%d\n%q", n, got)
	}
	if HasH3WriterDirectives(clean) {
		t.Error("干净提示词不该被判成含指令")
	}
	if !HasH3WriterDirectives(realDirectiveHead) {
		t.Error("真实样本必须被判成含指令")
	}
	if got, n := StripH3WriterDirectives(""); got != "" || n != 0 {
		t.Errorf("空输入应原样返回：%q %d", got, n)
	}
	// 边界：像规则又像内容的句子**留着**（它确实在告诉模型场景是哪张图）
	if !strings.Contains(realDirectiveHead, "The scene is the LAST attached picture.") {
		t.Skip("样本变了")
	}
	if !strings.Contains(Strip2(t, realDirectiveHead), "The scene is the LAST attached picture.") {
		t.Error("『The scene is the LAST attached picture.』属于内容，不该删")
	}
}

func Strip2(t *testing.T, s string) string {
	t.Helper()
	out, _ := StripH3WriterDirectives(s)
	return out
}

// TestLoadPlanStripsWriterDirectives 读取边界（唯一入口）必须真的清理，并记下条数。
func TestLoadPlanStripsWriterDirectives(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.Create("被测项目", ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	plan := samplePlan()
	plan.Shots[0].H3Prompt = realDirectiveHead
	if err := st.SavePlan(proj.ID, plan); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadPlan(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StrippedDirectives != 4 {
		t.Errorf("应记下剔除 4 行，实际 %d", got.StrippedDirectives)
	}
	if HasH3WriterDirectives(got.Shots[0].H3Prompt) {
		t.Errorf("读取后仍含指令：%s", got.Shots[0].H3Prompt)
	}
	if !strings.Contains(got.Shots[0].H3Prompt, "<Subject 1> is 临水城") {
		t.Error("清理时误删了主体定义")
	}
	// 契约门要在**导入时**就提醒（渲染前剔掉只是兜底，根治要上游改）。
	// 注意判据的对象：门看的是**盘上那份**（导入时会先过门再进管线），
	// 而 LoadPlan 返回的已经是清理过的版本 —— 两条都要钉住，否则"清理"会顺手把提醒也吞掉。
	if !hasIssue(ValidatePlan(plan), "warn", "混着**生成器指令**") {
		t.Errorf("含指令的包必须在导入时报一次：%v", PlanIssuesText(ValidatePlan(plan)))
	}
	if hasIssue(ValidatePlan(got), "warn", "混着**生成器指令**") {
		t.Error("清理过之后再报同一条就是在自说自话")
	}
	if hasIssue(ValidatePlan(samplePlan()), "warn", "混着**生成器指令**") {
		t.Error("干净方案不该报这条")
	}
}
