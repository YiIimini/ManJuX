package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 风格串"否定式"处理的回归(2026-09-25 用户报「动漫化角色还很丑」+ 误报告警)。
//
// 本机《全城榜一》方案里的风格串是:
//
//	CHINESE 2D DONGHUA ANIMATION STYLE, CLEAN TAPERED INK LINEWORK, VIBRANT FLAT CEL
//	SHADING, MATTE POWDERY SKIN WITH A DULL EVEN FINISH, **NO GLOSS, NO WET HIGHLIGHTS,
//	NO PHOTOREAL DETAIL**
//
// 末尾那串否定带来两个后果,都是这类写法必然踩的:
//  1. **画质**:Krea-2 是蒸馏模型(CFG 1.0),否定词不生效、还会把名词当指令画出来 ——
//     "NO GLOSS / NO WET HIGHLIGHTS" 被读成"要油光/湿高光"。定妆照上那一片片发灰的
//     污渍感正对应这里。
//  2. **误报**:"NO PHOTOREAL DETAIL" 里的 photoreal 命中了写实关键词,于是弹出
//     "风格里含真人/写实请求…已按动漫化渲染" 的告警(用户拿着它来问),并且让
//     styleExpand 走"覆盖"分支、把技能端指定的画风术语丢掉。
func TestStripNegationsRemovesNegativeStyleClauses(t *testing.T) {
	style := "CHINESE 2D DONGHUA ANIMATION STYLE, CLEAN TAPERED INK LINEWORK, VIBRANT FLAT CEL SHADING, MATTE POWDERY SKIN WITH A DULL EVEN FINISH, NO GLOSS, NO WET HIGHLIGHTS, NO PHOTOREAL DETAIL"
	got := StripNegations(style)
	if strings.Contains(strings.ToUpper(got), "NO GLOSS") ||
		strings.Contains(strings.ToUpper(got), "NO WET HIGHLIGHTS") ||
		strings.Contains(strings.ToUpper(got), "NO PHOTOREAL") {
		t.Errorf("否定式条款必须被剥掉(Krea-2 会把它们当指令画出来):%q", got)
	}
	for _, keep := range []string{"DONGHUA", "INK LINEWORK", "CEL SHADING", "MATTE POWDERY SKIN"} {
		if !strings.Contains(strings.ToUpper(got), keep) {
			t.Errorf("正面描述必须保留(%s):%q", keep, got)
		}
	}
	// 常见变体:without / avoid / 段内否定短语
	if s := StripNegations("ink linework, without gloss"); strings.Contains(strings.ToLower(s), "gloss") {
		t.Errorf("without 也应被剥掉:%q", s)
	}
	if s := StripNegations("cel shading with no oily highlights, flat colour"); strings.Contains(strings.ToLower(s), "oily") {
		t.Errorf("段内否定短语也应剥掉:%q", s)
	}
	// 剥完不该留下空段/多余逗号
	if strings.Contains(got, ", ,") || strings.HasPrefix(got, ",") || strings.HasSuffix(got, ",") {
		t.Errorf("剥完的风格串不该留空段:%q", got)
	}
}

// TestPhotorealRequestedIgnoresNegations 误报回归:「NO PHOTOREAL DETAIL」不算要写实。
func TestPhotorealRequestedIgnoresNegations(t *testing.T) {
	negated := "CHINESE 2D DONGHUA ANIMATION STYLE, NO GLOSS, NO PHOTOREAL DETAIL"
	if photorealRequested(negated) {
		t.Error("只有否定式提到 photoreal 时不该判为写实请求(这正是那条误报的根因)")
	}
	// 真的要写实:仍然要认出来
	for _, s := range []string{"photorealistic cinematic", "写实电影感", "live-action drama", "实拍质感"} {
		if !photorealRequested(s) {
			t.Errorf("%q 是要写实,应被识别", s)
		}
	}
}

// TestPortraitPromptDropsNegatedStyle 端到端:定妆照提示词里不该出现被否定的名词。
func TestPortraitPromptDropsNegatedStyle(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	proj.Config.Params.Script.StylizedFaces = project.Bool(true)
	plan := &project.Plan{
		Style:      "CHINESE 2D DONGHUA ANIMATION STYLE, VIBRANT FLAT CEL SHADING, NO GLOSS, NO WET HIGHLIGHTS, NO PHOTOREAL DETAIL",
		Characters: []project.Character{{ID: "c1", Name: "顾青禾"}},
	}
	got := PortraitPromptForProject(proj, plan, plan.Characters[0])
	if strings.Contains(strings.ToUpper(got), "NO GLOSS") || strings.Contains(strings.ToUpper(got), "NO WET HIGHLIGHTS") {
		t.Errorf("定妆照提示词里不该出现被否定的名词(会被当成指令画出来):\n%s", got)
	}
	if !strings.Contains(strings.ToUpper(got), "CEL SHADING") {
		t.Errorf("正面的画风描述要保留:\n%s", got)
	}
}
