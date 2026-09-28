package project

import (
	"strings"
	"testing"
)

// shot_budget_test.go 镜头数与片段时长（用户 2026-09-28：「为什么还是存在那么多的镜头？」）。
//
// 口径来源（用户自己的分镜文件）：《【额外】仿真人10S分镜头指令》
// 「非最后一个片段时长优先在 7、8、9、10 秒中选择，最后一个片段按实际剩余时长」。
// 而平台提示词此前写的是"镜头数由内容决定、没有上限" ⇒ 实测 35 镜（均 6.8 秒）、上一集 55 镜。
// 每多一镜 = 多一次 GPU 生成 + 多一个接缝 + 多一次漂移机会。
func TestShotBudgetTooManyShortClips(t *testing.T) {
	p := &Plan{}
	for i := 1; i <= 10; i++ { // 10 镜、每镜 4 秒 = 40 秒，全是碎镜
		p.Shots = append(p.Shots, mkShot(i, "中景", 4, "setup", "短句。"))
	}
	p.Shots[9].Duration = 4 // 末镜也短，但末镜不参与"7 秒"那条判据
	issues := ValidatePlan(p)
	if !hasIssue(issues, "warn", "非末尾片段的时长短于") {
		t.Error("十个 4 秒的碎镜要报（口径是非末尾 7-10 秒）")
	}
}

func TestShotBudgetAcceptsSpecCompliantPlan(t *testing.T) {
	p := &Plan{}
	for i := 1; i <= 8; i++ { // 8 镜 × 8 秒 = 64 秒 ⇒ 约 64/8.5 ≈ 7.5 镜，符合口径
		p.Shots = append(p.Shots, mkShot(i, "中景", 8, "setup", "一句台词。"))
	}
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "非末尾片段") || strings.Contains(is.Msg, "却有") {
			t.Errorf("符合口径的方案不该被报：%s", is.Msg)
		}
	}
}

func TestShotBudgetIgnoresTinyFixtures(t *testing.T) {
	// 夹具/短片（少于 6 镜）不该被这条门咬 —— 误拦比漏拦更糟。
	p := samplePlan()
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "非末尾片段") || strings.Contains(is.Msg, "却只有") {
			t.Errorf("少于 6 镜的方案不该被这条门咬：%s", is.Msg)
		}
	}
}
