package project

import (
	"testing"
)

// 同一个角色被两个会话各起一个 id 时，方案里会出现**两张同名卡**
// （2026-09-29 用户报：「肩头虚影是重名了还是怎样？怎么渲染两个」）。
//
// 真机现场（第 01 集 plan.json 原文）：
//
//	id=char_shade              name=肩头虚影  kind=incorporeal  描述 222 字、定妆提示词 727 字、板 753 字
//	id=char_shoulder_projection name=肩头虚影  kind=(空)         描述 0 字、定妆提示词 0 字、板 1714 字
//
// 来源：②人物道具 与 ③角色多视图 的 schema 都带 id，而 ③ 的 id 是**模型自己起的**。
// 后果：资产阶段给同一个角色出两张定妆照，渲染拿两张参考图 —— 用户看到"渲染两个"。
//
// 判据（与平台别处一致）：**同名即同角色**（去括注、去空白后比）。并卡时保留信息更全的那张，
// 分镜里对旧 id 的引用要改写到保留的 id（否则那一镜反而拿不到参考图）。
func TestDedupeCharactersMergesSameNameCards(t *testing.T) {
	plan := &Plan{
		Characters: []Character{
			{ID: "char_shade", Name: "肩头虚影", Kind: "incorporeal",
				Description: "半透明的全息计数投影，只有冷青数据行", ImagePrompt: "A translucent cyan hologram figure",
				BoardPrompt: "board-A"},
			{ID: "char_shoulder_projection", Name: "肩头虚影", BoardPrompt: "board-B"},
			{ID: "char_qiqi", Name: "祁七", Description: "瓷器白装甲", ImagePrompt: "Android girl"},
		},
		Scenes: []Scene{{ID: "scene_bay", Name: "维护舱"}},
		Shots: []Shot{
			{ID: 1, Scene: "scene_bay", Characters: []string{"char_shade"}},
			{ID: 2, Scene: "scene_bay", Characters: []string{"char_shoulder_projection"}},
			{ID: 3, Scene: "scene_bay", Characters: []string{"char_qiqi"}},
		},
	}
	dropped := DedupeCharacters(plan)
	if dropped != 1 {
		t.Fatalf("应当并掉 1 张同名卡，实得 %d", dropped)
	}
	if len(plan.Characters) != 2 {
		t.Fatalf("并卡后应剩 2 张，实得 %d", len(plan.Characters))
	}
	// 保留的必须是**信息更全**的那张（有描述与定妆提示词），且补上另一张的板子
	keep := plan.Characters[0]
	if keep.ID != "char_shade" || keep.BoardPrompt != "board-A" {
		t.Fatalf("保留的主卡不对：id=%q board=%q", keep.ID, keep.BoardPrompt)
	}
	if keep.Kind != "incorporeal" {
		t.Fatalf("并卡不该丢掉类型：%q", keep.Kind)
	}
	// 分镜里对旧 id 的引用必须改写到保留的 id
	if plan.Shots[1].Characters[0] != "char_shade" {
		t.Fatalf("第 2 镜还指着被丢掉的 id：%q（这一镜会拿不到参考图）", plan.Shots[1].Characters[0])
	}
	if plan.Shots[0].Characters[0] != "char_shade" || plan.Shots[2].Characters[0] != "char_qiqi" {
		t.Fatal("其它镜的引用被误改了")
	}
	// 幂等
	if again := DedupeCharacters(plan); again != 0 {
		t.Fatalf("再并一次不该有改动，实得 %d", again)
	}
	// 接线：HealPlan（唯一读取边界）必须调到它
	plan2 := &Plan{
		Characters: []Character{
			{ID: "char_a", Name: "肩头虚影", Description: "d", ImagePrompt: "p"},
			{ID: "char_b", Name: "肩头虚影", BoardPrompt: "b"},
		},
		Shots: []Shot{{ID: 1, Characters: []string{"char_b"}}},
	}
	hr := HealPlan(plan2)
	if hr.CharDupes != 1 || len(plan2.Characters) != 1 {
		t.Fatalf("HealPlan 没有并同名卡：dup=%d chars=%d", hr.CharDupes, len(plan2.Characters))
	}
}
