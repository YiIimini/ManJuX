package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 画外音兜底：技能端漏写"嘴唇闭合"时，平台必须补上 —— 否则模型会让画面里的
// 角色张嘴把旁白念出来（用户报的「系统旁白,主角动嘴?」）。
//
// 与 EnsureChineseDialogue 同一套纪律:幂等、判据是那句标记串、可反复调用。
func TestEnsureVoiceoverClause(t *testing.T) {
	sh := &project.Shot{
		ID: 1,
		Dialogue: []project.Line{
			{Speaker: "旁白", Text: "雨一直在下。", OS: true},
		},
		H3Prompt: "detailed_description:\n[Shot 1] the rain falls.\n<d>[Chinese]雨一直在下。</d>",
	}
	if !EnsureVoiceoverClause(sh) {
		t.Fatal("画外音镜次应当补上嘴唇闭合子句")
	}
	if !strings.Contains(sh.H3Prompt, project.VoiceoverClauseMarker) {
		t.Fatalf("补完的提示词里没有标记串:%q", sh.H3Prompt)
	}
	first := sh.H3Prompt
	if EnsureVoiceoverClause(sh) {
		t.Fatal("重复调用不该再改（幂等）")
	}
	if sh.H3Prompt != first {
		t.Fatal("重复调用改动了提示词 —— 一句话会越叠越多")
	}

	// 画内对白不许被加画外音子句（加了等于让人闭嘴）
	on := &project.Shot{ID: 2, Dialogue: []project.Line{{Speaker: "阿甲", Text: "我准了。"}},
		H3Prompt: "detailed_description:\n[Shot 1] ..."}
	if EnsureVoiceoverClause(on) || strings.Contains(on.H3Prompt, project.VoiceoverClauseMarker) {
		t.Fatal("纯画内对白不该被加画外音子句")
	}

	// narration 字段非空也算画外音
	nar := &project.Shot{ID: 3, Narration: "香炉是空的。", H3Prompt: "x"}
	if !EnsureVoiceoverClause(nar) {
		t.Fatal("narration 非空的镜次也要补子句")
	}
}

// ShotRenderPrompt 是渲染期提示词的唯一装配点：画外音兜底必须在它里面生效，
// 否则四条出像素的路径（主渲染/2K 定稿/局部重绘/音频驱动）会各漏一次。
func TestShotRenderPromptEnsuresVoiceoverClause(t *testing.T) {
	proj := &project.Project{ID: "p1", Name: "p1"}
	plan := &project.Plan{
		Characters: []project.Character{{ID: "char_a", Name: "阿甲", Kind: "human",
			Description: "二十岁男子", Voice: "a young man's voice"}},
		Scenes: []project.Scene{{ID: "scene_1", Name: "破庙", Description: "潮湿破庙"}},
	}
	sh := &project.Shot{ID: 1, Scene: "scene_1", Characters: []string{"char_a"},
		Dialogue: []project.Line{{Speaker: "旁白", Text: "雨一直在下。", OS: true}},
		H3Prompt: "detailed_description:\n[Shot 1] the rain falls.\n<d>雨一直在下。</d>"}
	got := ShotRenderPrompt(proj, plan, sh)
	if !strings.Contains(got, project.VoiceoverClauseMarker) {
		t.Fatalf("渲染期装配后的提示词缺画外音约束:\n%s", got)
	}
	if !strings.Contains(got, "MANDARIN ONLY") {
		t.Fatal("语言规则也不能丢")
	}
}
