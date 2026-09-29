package director

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 机位回填是**确定性**的那一半（2026-09-29 用户：「前后镜漂移，压不住场」）：
// 分镜经常整集不写 view（实测 11 镜里 8 镜既没 view 也没 view_note），
// 而纪律与 schema 都拦不住模型 —— 所以补一趟会话，只回填缺的字段、幂等。
//
// 判据（与契约门同源）：**留空且没写原因**才算缺；补的时候两样至少要有一样。
func TestApplyViewPatchesOnlyFillsWhatIsMissing(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, H3Prompt: "p", View: "ultra-wide aerial view"}, // 已有 view：不许动
		{ID: 2, H3Prompt: "p", ViewNote: "平视中近景，官方表无对应项"},      // 已有 note：不许动
		{ID: 3, H3Prompt: "p"}, // 缺：应被补上 view
		{ID: 4, H3Prompt: "p"}, // 缺：应被补上 note
		{ID: 5, H3Prompt: "p"}, // 缺：模型没给 → 仍缺
		{ID: 6, H3Prompt: ""},  // 没有提示词的镜不参与
	}}
	// 前置：缺的正好是 3/4/5
	if got := len(shotsMissingView(plan)); got != 3 {
		t.Fatalf("缺机位的镜数应为 3，实得 %d", got)
	}
	n := applyViewPatches(plan, []ShotViewPatch{
		{ID: 1, View: "telephoto compression"},                  // 已有值，忽略
		{ID: 3, View: "low-angle upward view"},                  // 填 view
		{ID: 4, ViewNote: "主观镜头"},                               // 填 note
		{ID: 5, View: "  ", ViewNote: " "},                      // 两样都空 = 没补
		{ID: 99, View: "ultra-wide eye-level deep perspective"}, // 不存在的镜号，忽略
	})
	if n != 2 {
		t.Fatalf("应补 2 镜，实得 %d", n)
	}
	if plan.Shots[0].View != "ultra-wide aerial view" || plan.Shots[0].ViewNote != "" {
		t.Error("已有 view 的镜被改动了")
	}
	if plan.Shots[1].ViewNote != "平视中近景，官方表无对应项" {
		t.Error("已有 view_note 的镜被改动了")
	}
	if plan.Shots[2].View != "low-angle upward view" || plan.Shots[3].ViewNote != "主观镜头" {
		t.Error("缺的镜没被补上")
	}
	if plan.Shots[4].View != "" || plan.Shots[4].ViewNote != "" {
		t.Error("模型没给值的镜不该被写成空串（会被当成「已处理」）")
	}
	// 幂等：再跑一次不该再有可补的
	if got := len(shotsMissingView(plan)); got != 1 {
		t.Fatalf("补过之后只剩 #5 缺，实得 %d", got)
	}
}
