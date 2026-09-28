package project

import (
	"strings"
	"testing"
)

// prop_contract_test.go 关键道具的契约（用户 2026-09-28：「关键物品生成道具啊，前后镜都漂移了」）。
//
// 实测那一集的 plan **连 props 字段都没有**、25 镜提到刀/烙铁/名册、prop_ids 全空 ⇒
// 招牌物件每镜重新发明 = 漂移。道具不进参考图（官方规则），一致性只能靠**文字锚定**，
// 所以"没有卡"就等于"没有一致性" —— 这条门就是防它再静默发生。
func TestNoPropsIsWarned(t *testing.T) {
	p := &Plan{
		Characters: []Character{{ID: "c1", Name: "碑生", ImagePrompt: "x"}},
		Scenes:     []Scene{{ID: "s1", Name: "西坡碑林·黄昏", Description: "x", ImagePrompt: "x"}},
		Shots: []Shot{{
			ID: 1, Scene: "s1", ShotSize: "中景", Duration: 6, Mode: "ref2va",
			Characters: []string{"c1"}, Action: "他握着碑刀在字口里添刻",
			H3Prompt: compliantH3ForSpeakerTest(),
		}},
	}
	var hit bool
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "一件道具都没提取") {
			hit = true
		}
	}
	if !hit {
		t.Error("有器物出现却一件道具都没提取，必须报（否则每镜自己发明一件 = 漂移）")
	}
}

func TestDanglingPropIDsAreFatal(t *testing.T) {
	p := &Plan{
		Characters: []Character{{ID: "c1", Name: "碑生", ImagePrompt: "x"}},
		Scenes:     []Scene{{ID: "s1", Name: "西坡碑林·黄昏", Description: "x", ImagePrompt: "x"}},
		Props:      []Prop{{ID: "prop_blade", Name: "碑刀", EN: "a thin worn stone-cutting blade", ImagePrompt: "x"}},
		Shots: []Shot{{
			ID: 1, Scene: "s1", ShotSize: "中景", Duration: 6, Mode: "ref2va",
			Characters: []string{"c1"}, PropIDs: []string{"prop_nope"}, Action: "他握着刀",
			H3Prompt: compliantH3ForSpeakerTest(),
		}},
	}
	var fails []string
	for _, is := range ValidatePlan(p) {
		if is.Level == "fail" {
			fails = append(fails, is.Msg)
		}
	}
	if !hasMsg(fails, "不存在的道具") {
		t.Errorf("悬空 prop_ids 要判 fail（锚定文本会落空）：%v", fails)
	}
}

func TestPropMentionedButNotReferenced(t *testing.T) {
	p := &Plan{
		Characters: []Character{{ID: "c1", Name: "碑生", ImagePrompt: "x"}},
		Scenes:     []Scene{{ID: "s1", Name: "西坡碑林·黄昏", Description: "x", ImagePrompt: "x"}},
		Props:      []Prop{{ID: "prop_blade", Name: "碑刀", EN: "blade", ImagePrompt: "x"}},
		Shots: []Shot{
			{ID: 1, Scene: "s1", ShotSize: "中景", Duration: 6, Mode: "ref2va", Characters: []string{"c1"},
				PropIDs: []string{"prop_blade"}, Action: "他把刀尖压进字口", H3Prompt: compliantH3ForSpeakerTest()},
			{ID: 2, Scene: "s1", ShotSize: "中景", Duration: 6, Mode: "ref2va", Characters: []string{"c1"},
				Action: "他抬手把烙铁举高", H3Prompt: compliantH3ForSpeakerTest()}, // 提到器物但没带 prop_ids
		},
	}
	var hit bool
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "没写 prop_ids") {
			hit = true
		}
	}
	if !hit {
		t.Error("动作里出现器物却不带 prop_ids 要报（漂移高发区）")
	}
	// 带了 prop_ids 的那一镜不该被牵连
	if firstPropWord("他抬手把烙铁举高") == "" {
		t.Error("器物词表要认得出「烙铁」这类真实器物")
	}
}
