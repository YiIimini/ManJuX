package project

import (
	"strings"
	"testing"
)

// placeholder_dialogue_test.go 占位台词标签的清理（2026-09-28 用户：「配音也怪的很，重复配音」）。
//
// 实测：真渲那一集成片里 **36/74 个 `<d>` 内容只有「…」**（48 镜里 36 镜各一个），
// 而同一批正文里"计划台词＋旁白"与正文**一处不差** ⇒ 该念的都念对了，多出来的正是这些空标签。
// 给模型一个"这里要说点什么"的标签，它就会自己编一句或发出叹气/清嗓/半个音节。
func TestStripPlaceholderDialogue(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantGone string // 清理后不该再出现的片段
		wantKeep string // 清理后必须还在的片段
		n        int
	}{
		{
			"真案：旁白句里只有省略号",
			"Off-screen narration: The narrator (S1) says in an off-screen voiceover: <d>[Chinese]…</d> while the on-screen characters' lips remain completely closed.",
			"<d>", "lips remain completely closed", 1, // 只要标签消失即可：句子其它部分是有用约束，必须留
		},
		{
			"空标签",
			"He turns. <Subject 1> (S1) says in a low voice: <d></d>. The wind rises.",
			"<d>", "The wind rises.", 1,
		},
		{
			"写「无」",
			"<Subject 2> (S2) says: <d>[Chinese]（无）</d> She looks away.",
			"（无）", "She looks away.", 1,
		},
		{
			"英文 N/A",
			"<Subject 1> says: <d>N/A</d> He walks on.",
			"N/A", "He walks on.", 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, n := StripPlaceholderDialogue(tc.in)
			if n != tc.n {
				t.Errorf("应删 %d 句，实际 %d：%q", tc.n, n, got)
			}
			if strings.Contains(got, tc.wantGone) {
				t.Errorf("占位标签还在：%q", got)
			}
			if !strings.Contains(got, tc.wantKeep) {
				t.Errorf("**同一句里的有效约束被删了**（第一版正是这么错的）：%q", got)
			}
			if !strings.Contains(got, "no spoken words") {
				t.Errorf("替换文本要明确说「这镜不说话」（把「说点什么」的信号翻成反面）：%q", got)
			}
		})
	}
}

// TestStripPlaceholderDialogueKeepsRealLines 真台词一个字节都不许动。
func TestStripPlaceholderDialogueKeepsRealLines(t *testing.T) {
	in := "<Subject 1> 碑生 (S1) says in a hoarse voice: <d>[Chinese]三年了，你还敢出门。</d> " +
		"<Subject 2> 卖菜婆 (S2) says: <d>[Chinese]我记着。</d>"
	got, n := StripPlaceholderDialogue(in)
	if n != 0 || got != in {
		t.Errorf("全是真台词时不该改动（删了 %d 句）:\n%s", n, got)
	}
	if HasPlaceholderDialogue(in) {
		t.Error("真台词不该被判成占位")
	}
}

// TestLoadPlanStripsPlaceholderDialogue 清理挂在**唯一读取边界**上（四条消费路径一起干净）。
func TestLoadPlanStripsPlaceholderDialogue(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.Create("占位台词测试", ProjectConfig{Params: DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{
		Characters: []Character{{ID: "c1", Name: "碑生", ImagePrompt: "x"}},
		Scenes:     []Scene{{ID: "s1", Name: "西坡碑林·黄昏", Description: "x", ImagePrompt: "x"}},
		Shots: []Shot{{
			ID: 1, Scene: "s1", ShotSize: "中景", Duration: 6, Mode: "ref2va",
			Characters: []string{"c1"}, Action: "他站着",
			H3Prompt: "detailed_description:\n[Shot 1] He stands. " +
				"<Subject 1> (S1) says in an off-screen voiceover: <d>[Chinese]…</d>",
		}},
	}
	if err := st.SavePlan(proj.ID, p); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadPlan(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Shots[0].H3Prompt, "<d>") || !strings.Contains(got.Shots[0].H3Prompt, "no spoken words") {
		t.Errorf("读取时应已清掉占位台词标签：%s", got.Shots[0].H3Prompt)
	}
	if got.StrippedPlaceholders != 1 {
		t.Errorf("要如实计数（用户听到的怪配音就来自这些标签），实际 %d", got.StrippedPlaceholders)
	}
	if !strings.Contains(got.Shots[0].H3Prompt, "He stands.") {
		t.Error("真正的画面描述不许被误删")
	}
}
