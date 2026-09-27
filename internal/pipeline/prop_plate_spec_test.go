package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// prop_plate_spec_test.go 道具图的画面纪律（技能侧《平台侧生成规范》§5.1 骨架）。
//
// 规范里三样东西此前平台没写进道具提示词，而它们直接决定道具能不能"跨镜认得出是同一件"：
// ① ≥4 种材质混搭 ＋ 至少一处非功能性装饰（质感来源）；② 尺寸参照（大印与书签不写尺寸会长一样）；
// ③ 面板类道具内容留白（系统 UI 那块板绝不许画字）。
func TestPropPlateClauseCarriesSpecRequirements(t *testing.T) {
	for _, want := range []string{
		"Single object only", "1:1 square", "plain pure white seamless background",
		"no text, no letters, no numerals",
		"at least four premium materials",    // §5 品质要求
		"at least one purely decorative",     // 非功能性装饰
		"SIZE against a human reference",     // 八项里的"尺寸"
		"keep its interior completely BLANK", // 面板类留白
	} {
		if !strings.Contains(propPlateClause, want) {
			t.Errorf("道具图纪律缺 %q（规范 §5.1/§5 的硬要求）", want)
		}
	}
}

// TestPropImagePromptAppendsClause 装配点必须真的把它接上（卡片自己写没写都要接）。
func TestPropImagePromptAppendsClause(t *testing.T) {
	proj := &project.Project{}
	plan := &project.Plan{}
	pr := project.Prop{ID: "prop_01", Name: "一本簿子", ImagePrompt: "a palm-sized booklet of rough-edged paper; hemp cord binding"}
	got := PropImagePrompt(proj, plan, pr)
	if !strings.Contains(got, propPlateClause) {
		t.Fatalf("道具提示词里应有画面纪律：\n%s", got)
	}
	// 幂等：装配两次不该叠两遍
	if again := PropImagePrompt(proj, plan, project.Prop{ID: pr.ID, Name: pr.Name, ImagePrompt: got}); strings.Count(again, propPlateClause) != 1 {
		t.Errorf("装配点被调用两次时纪律串只该有一份：\n%s", again)
	}
}
