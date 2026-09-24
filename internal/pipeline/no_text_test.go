package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 画面文字的硬约束闸门(2026-09-24 用户报「视频渲染中的文字有时候出现错误的字」)。
//
// 根因不是"约束写错了",而是**根本没兜住**:分镜提示词里那句 exclusions 只是 `e.g.`
// (例如不要字幕/水印),LLM 写不写全凭自觉;而招牌/牌匾/书页/胸牌一旦进了画面描述,
// 视频模型就会自己发明几个字 —— 汉字它认不出,于是画出形近错字或糊成一团的假字。
//
// 所以这条在**代码装配点**兜底(与台词语言标记同一思路),下面把它钉住:
//   - 每条出像素的提示词都必须带 NO ON-SCREEN TEXT 段(不管 LLM 写没写);
//   - 幂等(重复调用不叠加);
//   - 三种情形都要说清:默认不画字 / 必须照抄给出的原字 / 不认识的远景文字用抽象笔画,
//     绝不自己发明 —— 只写"不要文字"会让模型走极端(该有字处留白,或照样写错字)。
func TestNoOnScreenTextIsEnforced(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国漫二维动画", Characters: []project.Character{{ID: "c1", Name: "小凡"}}}
	shot := &project.Shot{ID: 1, H3Prompt: "detailed_description:\n[Shot 1] a plaque on the gate reads \"天宫\"", Mode: "ref2va", Duration: 6}

	got := ShotRenderPrompt(proj, plan, shot)
	if !strings.Contains(got, noOnScreenTextMarker) {
		t.Fatal("每条出像素的提示词都必须带画面文字硬约束(用户报的错字就出在这条缺失时)")
	}
	for _, want := range []string{
		"do not render any readable text in the frame",
		"never invent, guess or approximate characters",
		"render EXACTLY those characters",
		"抽象笔画",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("画面文字约束缺少 %q —— 只说「不要文字」会让模型走极端:\n%s", want, got)
		}
	}
	// 幂等:装配点可能被多路复用(主渲染/单镜2K/局部重绘/音频驱动),重复调用不得叠加
	twice := EnsureNoOnScreenText(got)
	if strings.Count(twice, noOnScreenTextMarker) != 1 {
		t.Errorf("画面文字约束必须幂等,实际出现 %d 次", strings.Count(twice, noOnScreenTextMarker))
	}
}

// TestNoOnScreenTextOnEveryPixelPath 五条出像素路径都要带上(与介质锁/风格段同一要求)。
func TestNoOnScreenTextOnEveryPixelPath(t *testing.T) {
	proj, plan, shot := pixFixtures()
	paths := map[string]string{
		"主渲染与单镜2K与局部重绘与音频驱动(ShotRenderPrompt)": ShotRenderPrompt(proj, plan, shot),
		"成片2K(final2KPrompt)": final2KPrompt(proj, plan),
	}
	for name, p := range paths {
		if !strings.Contains(p, noOnScreenTextMarker) {
			t.Errorf("%s 缺少画面文字硬约束", name)
		}
	}
}
