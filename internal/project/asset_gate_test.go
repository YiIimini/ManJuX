package project

import (
	"strings"
	"testing"
)

// 场景板与道具卡的补充判据。
//
// 【2026-09-28 用户问「道具和场景的呢」】此前这两类里，用户参考库明写、而平台
// **一条判据都没有**的是：场景的「严禁人影」与「事件隔离」、道具八项里的「功能作用/独有特征」。
func TestSceneAndPropAssetIssues(t *testing.T) {
	hasKind := func(issues []SceneIssue, k SceneIssueKind) bool {
		for _, i := range issues {
			if i.Kind == k {
				return true
			}
		}
		return false
	}

	// ① 板上写了人群 → 报（用户《四》§4.1 严禁人影）
	if got := SceneIssues([]Scene{{Name: "集市",
		ImagePrompt: "a market street with a scattered crowd of villagers"}}); !hasKind(got, SceneHasPeople) {
		t.Errorf("板上有人群该被报出来，实际 %v", got)
	}
	// ② 板上带着事件后果 → 报（§4.3 事件隔离）
	if got := SceneIssues([]Scene{{Name: "破庙",
		ImagePrompt: "a ruined temple, a corpse on the floor, bloodstain on the wall"}}); !hasKind(got, SceneHasEvent) {
		t.Errorf("板上有尸体血渍该被报出来，实际 %v", got)
	}

	// ③ **平台自己的固定否定串不算** —— 这是第一版栽过的地方：
	//    `PlanScenePreEventClause` 里就写着 "no blood, no corpses, …"，
	//    不做否定语境判断的话，**合规的场景板会被自家禁词判成违规**
	//    （TestValidatePlanCleanSample 当场咬住）。
	clean := []Scene{{
		Name: "荒山破庙·雨夜",
		ImagePrompt: "wet flagstones, cold light on damp moss, " + PlanScenePreEventClause +
			", no humans, empty, landscape only",
	}}
	if got := SceneIssues(clean); len(got) != 0 {
		t.Errorf("带平台固定否定串的合规场景板不该被报，实际 %v", got)
	}
	// ④ 允许的**时间痕迹**（风化/旧锈/苔藓/旧裂纹）不该报 —— 事件隔离禁的是"刚刚发生过什么"
	aged := []Scene{{Name: "旧城",
		ImagePrompt: "weathered stone, old rust on the gate hinges, moss in the cracks, faded paint"}}
	if got := SceneIssues(aged); len(got) != 0 {
		t.Errorf("时间的痕迹（风化/旧锈/苔藓/褪色）不该报，实际 %v", got)
	}

	// ⑤ 道具缺八项里的两项 → 报
	bad := []Prop{{ID: "p1", Name: "旧柴刀", EN: "a short worn chopping knife",
		Description: "磨薄的旧柴刀，柄缠麻布", ImagePrompt: "a short knife, 1:1, plain white background"}}
	issues := PropFeatureIssues(bad)
	if len(issues) != 2 {
		t.Fatalf("该报两项缺失（功能作用/独有特征），实际 %d 条：%v", len(issues), issues)
	}
	if !strings.Contains(PropIssueText(issues[0]), "旧柴刀") {
		t.Errorf("缺失说明要点名是哪件道具，实际 %q", PropIssueText(issues[0]))
	}
	// ⑥ 写全了就不报
	full := []Prop{{ID: "p1", Name: "旧柴刀", EN: "a short worn chopping knife with a chipped spine",
		Description: "磨薄的旧柴刀；功能作用：劈柴与防身，砍不动硬木；独有特征：刀背有一道豁口"}}
	if got := PropFeatureIssues(full); len(got) != 0 {
		t.Errorf("写全八项的道具不该被报，实际 %v", got)
	}
}
