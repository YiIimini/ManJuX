package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 画面文字规则 + 音频驱动官方标记的闸门 —— **依据官方指南**。
//
// 2026-09-25 用户要求"参考全网找解决方案,不要自己想当然",据此核对了官方两份指南:
//
//	基础模式 https://huggingface.co/MiniMaxAI/MiniMax-H3/blob/main/docs/VIDEO_PROMPT_WRITING_GUIDE_base_en.md
//	全参考   https://huggingface.co/MiniMaxAI/MiniMax-H3/blob/main/docs/VIDEO_PROMPT_WRITING_GUIDE_ref_en.md
//	ComfyUI  https://docs.comfy.org/zh/tutorials/video/minimax/minimax-h3-prompt-guide
//
// 结论修正了我先前两处"想当然":① 画面文字不是"一律禁止",官方是**引号枚举制**;
// ② 音频驱动必须带 <Audio N> + [audio reuse] + fully_copy 三个标记,否则 H3 只把音频
// 当音色参考、口型与配音脱节(ComfyUI 官方页记载的社区实测)。
func TestOnScreenTextRuleFollowsOfficialGuide(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国漫二维动画", Characters: []project.Character{{ID: "c1", Name: "小凡"}}}
	shot := &project.Shot{ID: 1, H3Prompt: "detailed_description:\n[Shot 1] a plaque on the gate reads \"天宫\"",
		Mode: "ref2va", Duration: 6}

	got := ShotRenderPrompt(proj, plan, shot)
	if !strings.Contains(got, onScreenTextMarker) {
		t.Fatal("每条出像素的提示词都必须带画面文字规则")
	}
	for _, want := range []string{"English double quotation marks", "verbatim", "No other text", "do not invent"} {
		if !strings.Contains(got, want) {
			t.Errorf("画面文字规则缺少 %q(官方口径):\n%s", want, got)
		}
	}
	if n := strings.Count(EnsureOnScreenTextRule(got), onScreenTextMarker); n != 1 {
		t.Errorf("画面文字规则必须幂等,实际 %d 次", n)
	}
}

func TestOnScreenTextRuleOnEveryPixelPath(t *testing.T) {
	proj, plan, shot := pixFixtures()
	paths := map[string]string{
		"主渲染与单镜2K与局部重绘与音频驱动(ShotRenderPrompt)": ShotRenderPrompt(proj, plan, shot),
		"成片2K(final2KPrompt)": final2KPrompt(proj, plan),
	}
	for name, p := range paths {
		if !strings.Contains(p, onScreenTextMarker) {
			t.Errorf("%s 缺少画面文字规则", name)
		}
	}
}

// TestAudioDriveCarriesOfficialMarkers 音频驱动的官方标记(口型脱节的根治)。
func TestAudioDriveCarriesOfficialMarkers(t *testing.T) {
	base := "detailed_description:\n[Shot 1] a woman says: <d>[Chinese]我不签。</d>"
	got := EnsureAudioDriveMarkers(base)
	for _, want := range []string{
		audioMarkerKey,
		"[audio reuse]", // summary 任务类型前缀(官方 §3)
		"<Audio 1>",     // subject_definitions 定义(官方 §2.4)
		"fully_copy",    // retention 关系标记(官方 §4.2)
		"lip-sync",      // 口型必须跟随
	} {
		if !strings.Contains(got, want) {
			t.Errorf("音频驱动提示词缺少官方标记 %q:\n%s", want, got)
		}
	}
	if n := strings.Count(EnsureAudioDriveMarkers(got), audioMarkerKey); n != 1 {
		t.Errorf("音频标记必须幂等,实际 %d 次", n)
	}
	if !strings.Contains(got, "我不签") {
		t.Error("原有提示词内容不得丢失")
	}
}
