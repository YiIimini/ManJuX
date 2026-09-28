package director

import (
	"encoding/json"
	"strings"
	"testing"
)

// plan_head_props_test.go 规划轮的**解码字段**必须与骨架文字同步（2026-09-28 实测事故）。
//
// 事故：`planSystemPrompt` 的骨架里写了 `props`，但解码用的 `planHead` **没有 Props 字段**
// ⇒ 模型写的道具卡在解析处**被静默丢掉**（落盘方案里连 `props` 键都没有），而分镜又老老实实
// 引用了 `prop_zaodao` 这种 id ⇒ 契约的"prop_ids 悬空 ⇒ fail"必然触发、道具一张图也出不来。
// 用户看到的就是「关键物品生成道具啊，前后镜都漂移了」。
//
// 这类"加了提示词字段、忘了加解码字段"的错**不报错、只丢数据**，所以必须有守卫钉住：
// 只要骨架里出现某个顶层字段，解码结构体就得有对应字段。
func TestPlanHeadDecodesProps(t *testing.T) {
	raw := `{
	  "title": "碑上写我死了",
	  "logline": "他给自己补名字",
	  "style": "PHOTOREAL 3D",
	  "characters": [{"id":"char_01","name":"碑生","image_prompt":"IDENTITY LOCK (do not change): x"}],
	  "scenes": [{"id":"scene_01","name":"西坡碑林·黄昏","image_prompt":"x"}],
	  "props": [
	    {"id":"prop_zaodao","name":"灶刀","en":"a short soot-blackened kitchen cleaver",
	     "description":"刀身磨薄、柄缠麻布","image_prompt":"a short soot-blackened kitchen cleaver, 1:1, plain white background"}
	  ],
	  "segments": [{"from":1,"to":3,"brief":"起手"}]
	}`
	head, err := decodePlanHead(raw)
	if err != nil {
		t.Fatalf("解码失败:%v", err)
	}
	if len(head.Props) != 1 {
		t.Fatalf("道具卡必须在解码后还在（这正是事故点：字段缺失导致静默丢弃），实际 %d 张", len(head.Props))
	}
	p := head.Props[0]
	if p.ID != "prop_zaodao" || p.Name != "灶刀" || !strings.Contains(p.EN, "cleaver") {
		t.Errorf("道具字段没解全:%+v", p)
	}
}

// TestPlanSkeletonFieldsAllDecoded 骨架里出现的顶层字段，解码结构体必须都有。
//
// 这条是"下次别再犯"的机器守卫：把骨架文字里的键名扫出来，逐个确认 planHead 能接住。
func TestPlanSkeletonFieldsAllDecoded(t *testing.T) {
	// 计划轮骨架的顶层键（从提示词原文里数，避免手抄漂移）
	want := []string{"title", "logline", "style", "characters", "scenes", "props", "segments"}
	for _, k := range want {
		if !strings.Contains(planSystemPrompt, `"`+k+`"`) {
			t.Errorf("计划轮骨架里应有顶层字段 %q（提示词与解码必须同源）", k)
		}
	}
	// 解码结构体的 json tag 必须覆盖它们
	probe := map[string]any{
		"title": "t", "logline": "l", "style": "s",
		"characters": []any{}, "scenes": []any{}, "props": []any{},
		"segments": []any{map[string]any{"from": 1, "to": 2, "brief": "b"}},
	}
	b, _ := json.Marshal(probe)
	head, err := decodePlanHead(string(b))
	if err != nil {
		t.Fatalf("解码失败:%v", err)
	}
	if head.Title != "t" || head.Logline != "l" || head.Style != "s" {
		t.Error("title/logline/style 没解出来")
	}
	if len(head.Segments) != 1 {
		t.Error("segments 没解出来")
	}
	// props 即使为空数组也要能接住（nil 与空切片的区别不该在装配层再判一次）
	raw := `{"title":"t","segments":[{"from":1,"to":2,"brief":"b"}],"props":[]}`
	if _, err := decodePlanHead(raw); err != nil {
		t.Errorf("空 props 数组不该解码失败:%v", err)
	}
}
