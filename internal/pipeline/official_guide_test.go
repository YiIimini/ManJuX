package pipeline

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 画面文字规则 + 音频驱动官方标记的闸门 —— **依据官方指南**。
//
// 2026-09-25 用户要求"参考全网找解决方案,不要自己想当然",据此核对了官方两份指南:
//
//	基础模式 https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/base-en.txt
//	全参考   https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/ref-en.txt
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

// verifiedGuideURLs 是**本机实测取到正文**的官方依据清单(拉取返回 HTTP 200)。
//
// 为什么要有这份清单:2026-09-25 用户要求"参考全网找解决方案,不要你自己想当然的去改调整"。
// 我核对自己上一轮写的依据时发现,注释里引的
// `huggingface.co/.../docs/VIDEO_PROMPT_WRITING_GUIDE_*.md` **本机取不到**(请求 TypeError),
// 而官方正文实际在 GitHub 仓库的 skills/h3-prompt-writing/references/ 下(两次 raw 拉取均 200)。
// 引用一个自己没打开过的链接,和"想当然"是同一件事 —— 所以把它钉成闸门:
// 提示词权威相关文件里出现清单外的链接,check.ps1 直接挂。
var verifiedGuideURLs = map[string]bool{
	"https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/base-en.txt": true,
	"https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/ref-en.txt":  true,
	"https://docs.comfy.org/zh/tutorials/video/minimax/minimax-h3-prompt-guide":                          true,
}

func TestCitedGuideURLsAreVerified(t *testing.T) {
	// 提示词的"权威依据"只可能出现在这两处:装配一处的 pipeline 包,与导演提示词。
	files := []string{
		"audio_ref.go", "prompts.go",
		"../director/director.go",
	}
	re := regexp.MustCompile("https?://[^\\s)\"'\x60]+")
	seen := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读不到 %s: %v", f, err)
		}
		for _, u := range re.FindAllString(string(b), -1) {
			seen[u] = true
			if !verifiedGuideURLs[u] {
				t.Errorf("%s 引用了未经验证的链接 %q —— 官方依据必须落在 verifiedGuideURLs 里"+
					"(要么本机实测打开过,要么别引)", f, u)
			}
		}
	}
	// 反向:两份官方指南都必须还在被引用,不能被悄悄删掉
	for _, must := range []string{
		"https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/base-en.txt",
		"https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/ref-en.txt",
	} {
		if !seen[must] {
			t.Errorf("官方指南 %s 不再被引用(改提示词前必须先查它)", must)
		}
	}
}
