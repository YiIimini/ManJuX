package compose

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// ASS 里 `{}` 是**覆写标签块**，块内不识别的内容会被**静默忽略** ⇒ 写进字幕前必须转义，
// 否则一句台词/一条场次字幕里只要有一个 `{`，从那里到 `}` 的字就凭空消失，而且不报错。
// 依据：技能侧 2026-09-28 通知（引 Subtitle Edit《ASSA Override Tags Reference》）。
//
// 做法是**转义**而不是替字：`\{` `\}` 在 libass 里渲染成 `{` `}` 本身 —— 屏幕上看到的仍是原文，
// 只是不再被当标签块（技能侧明确要求不要静默改字）。
func TestASSEscapesCurlyBraces(t *testing.T) {
	plan := &project.Plan{
		Scenes: []project.Scene{{ID: "scene_a", Name: "碑{林}"}},
		Shots: []project.Shot{{
			ID: 1, Scene: "scene_a", Duration: 5,
			Dialogue: []project.Line{{Speaker: "丙十七", Text: "这里写着{生}字"}},
		}},
	}
	ass := BuildASS(plan, []float64{0}, []float64{5}, 6, 1080, 1920)
	if ass == "" {
		t.Fatal("应产出 ASS")
	}
	if strings.Contains(ass, "{生}") || strings.Contains(ass, "{林}") {
		t.Errorf("未转义的花括号会让 ASS 静默吞字:\n%s", ass)
	}
	if !strings.Contains(ass, `\{生\}`) {
		t.Errorf("应转义为 \\{生\\}（libass 渲染出的仍是原文），实际:\n%s", ass)
	}
}

// 没有花括号的文本**一个字符都不该动**（转义只治 `{` `}`，不要顺手改别的）。
func TestASSEscapeLeavesNormalTextAlone(t *testing.T) {
	const s = "他念出：碑上写我死了。\n第二行 \\N 与 [方括号] (圆括号) 保持原样"
	if got := assEscape(s); got != s {
		t.Errorf("不含花括号的文本不该被改动\n实际: %q", got)
	}
}
