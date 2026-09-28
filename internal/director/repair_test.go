package director

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// repair_test.go 自纠一轮的**判据与安全阀**（2026-09-28 实测驱动）。
//
// 背景：真章节那一跑 35 镜**全都还是一镜到底**，而提示词里明明写着"5 秒以上必须内部切
// 2-4 个小镜头" —— 散文规则对模型不起作用。所以把机器能判定的违规当反馈信号用一次。
func TestCollectRepairableFindsOneTake(t *testing.T) {
	plan := &project.Plan{
		Shots: []project.Shot{
			{ID: 1, Duration: 6, H3Prompt: "detailed_description:\n[Shot 1] he walks slowly to the gate."},
			{ID: 2, Duration: 3, H3Prompt: "detailed_description:\n[Shot 1] he turns."}, // 短镜一镜到底不算
			{ID: 3, Duration: 6, H3Prompt: "detailed_description:\n[Shot 1] x.\n[Shot 2] At 00:03.000, y."},
		},
	}
	got := collectRepairable(plan)
	if len(got) != 1 || got[0].shotID != 1 {
		t.Fatalf("只该点名 #1（6 秒且只有一个内部镜头），实际 %+v", got)
	}
	if !strings.Contains(got[0].fix, "[Shot 2] At") {
		t.Error("给模型的修法要具体到官方多镜写法，不能只说'切一下'")
	}
}

func TestCollectRepairableFindsLongPrompt(t *testing.T) {
	long := "detailed_description:\n[Shot 1] " + strings.Repeat("the camera holds on his face and the light shifts. ", 120) +
		"\n[Shot 2] At 00:03.000, closer."
	plan := &project.Plan{Shots: []project.Shot{{ID: 1, Duration: 6, H3Prompt: long}}}
	got := collectRepairable(plan)
	if len(got) != 1 || !strings.Contains(got[0].note, "词") {
		t.Fatalf("超长正文要点名（官方 350-500 词），实际 %+v", got)
	}
}

// TestAdoptRepairedSafetyValve "自纠"绝不能把方案改坏：格式变差、台词被增删、或两个毛病
// 一个都没改善 —— 三种情况一律退回原文。
func TestAdoptRepairedSafetyValve(t *testing.T) {
	mk := func(prompt string) *project.Shot {
		return &project.Shot{ID: 1, Duration: 6, H3Prompt: prompt}
	}
	base := "summary:\nx\n\nretention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved\n\n" +
		"detailed_description:\nThe style is cinematic.\n[Shot 1] he walks.<d>[Chinese]走。</d>\n\n" +
		"overall_soundscape:\nwind.\n\nnon_diegetic_music:\nN/A."
	// ① 台词条数变了 ⇒ 拒绝
	sh := mk(base)
	if adoptRepaired(sh, strings.Replace(base, "[Shot 1] he walks.", "[Shot 1] he walks.\n[Shot 2] At 00:03.000, closer.", 1)+"\n<d>[Chinese]又一句。</d>") {
		t.Error("台词被增删时必须退回原文（台词是内容，不是格式）")
	}
	if sh.H3Prompt != base {
		t.Error("拒绝时不该改动原文")
	}
	// ② 格式变差（丢掉六段）⇒ 拒绝
	sh2 := mk(base)
	if adoptRepaired(sh2, "[Shot 1] he walks.\n[Shot 2] At 00:03.000, closer.") {
		t.Error("格式变差时必须退回原文")
	}
	// ③ 什么都没改善（还是只有一个镜头、还是那么长）⇒ 拒绝
	sh3 := mk(base)
	if adoptRepaired(sh3, base) {
		t.Error("没有任何改善时不该白换一次")
	}
	// ④ 真的切了镜、格式没变差、台词条数不变 ⇒ 采纳
	good := strings.Replace(base, "[Shot 1] he walks.",
		"[Shot 1] he walks.\n[Shot 2] At 00:03.000, the camera cuts closer.", 1)
	sh4 := mk(base)
	if !adoptRepaired(sh4, good) {
		t.Error("切了镜且没改坏，就该采纳")
	}
	if sh4.H3Prompt != good {
		t.Error("采纳时应写回新正文")
	}
}

// TestRepairPlanWithoutClientIsNoop 没配 LLM 的部署不该因此失败。
func TestRepairPlanWithoutClientIsNoop(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{{ID: 1, Duration: 6, H3Prompt: "[Shot 1] x."}}}
	if n, note := RepairPlan(nil, nil, plan, Options{}); n != 0 || note != "" {
		t.Errorf("client 为 nil 时应直接返回，实际 n=%d note=%q", n, note)
	}
}
