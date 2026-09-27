package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// style_yield_test.go 装配层的**风格让位**：上游提示词里已带风格句时，平台不再插第二段。
//
// 由来（2026-09-27）：技能侧的 `h3_prompt` 自带一段风格句（官方格式要求它写在 `[Shot 1]`
// 之前），而平台装配时也会插"【全片视觉风格】…"。一段提示词两套风格 → 模型取中值 ——
// 这正是本项目反复记过的那类"丑"。修法与比例锚点同一条设计：**不改上游文本，只让自己那段
// 不重复**（`project.HasStyleSentence` 判据，导入侧提 `plan.Style` 用的是同一个）。

// testYieldProject 最小项目（ShotRenderPrompt 需要它取 facesOn 等开关）。
func testYieldProject() *project.Project {
	return &project.Project{Config: project.ProjectConfig{Params: project.DefaultStageParams()}}
}

const yieldStyle = "GUOFENG ANIMATED-FEATURE ART DIRECTION (period Chinese): a photoreal 3D surface."

func TestStyleBlockYieldsWhenPromptAlreadyCarriesStyle(t *testing.T) {
	plan := &project.Plan{
		Style: yieldStyle,
		Shots: []project.Shot{{
			ID: 1, ShotSize: "中景", Characters: []string{"char_01"}, Duration: 5, Mode: "ref2va",
			H3Prompt: "subject_definitions:\n<Subject 1> is 沈砚.\n\ndetailed_description:\n" + yieldStyle + "\n" +
				"[Shot 1] he walks.",
		}},
	}
	prompt := ShotRenderPrompt(testYieldProject(), plan, &plan.Shots[0])
	if n := strings.Count(prompt, yieldStyle); n != 1 {
		t.Errorf("风格句应恰好出现一次（上游那句），实际 %d 次：\n%.400s", n, prompt)
	}
	if strings.Contains(prompt, "【全片视觉风格】") {
		t.Errorf("上游已带风格句时平台不该再插一段风格块：\n%.400s", prompt)
	}
	// 但**法务/原创性**底线必须留着（它不规定画风，任何风格下都成立）
	if !strings.Contains(prompt, "never make a character resemble a real person") {
		t.Errorf("让位不等于丢掉原创性底线：\n%.400s", prompt)
	}
}

func TestStyleBlockInjectedWhenPromptHasNoStyle(t *testing.T) {
	plan := &project.Plan{
		Style: yieldStyle,
		Shots: []project.Shot{{
			ID: 1, ShotSize: "中景", Characters: []string{"char_01"}, Duration: 5, Mode: "ref2va",
			H3Prompt: "subject_definitions:\n<Subject 1> is 沈砚.\n\ndetailed_description:\n[Shot 1] he walks.",
		}},
	}
	prompt := ShotRenderPrompt(testYieldProject(), plan, &plan.Shots[0])
	if !strings.Contains(prompt, "【全片视觉风格】") || !strings.Contains(prompt, yieldStyle) {
		t.Errorf("上游没写风格句时平台必须自己注入（否则整片没有风格）：\n%.400s", prompt)
	}
	if n := strings.Count(prompt, yieldStyle); n != 1 {
		t.Errorf("注入后也只应出现一次，实际 %d 次", n)
	}
}
