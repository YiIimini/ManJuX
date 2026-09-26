package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 「同角色的多张卡当一个人」在服务端投影里的契约(技能侧清单 §4.2)。
//
// 前端不许自己抄一份家族判据(那必然与后端漂移,与 char_kinds 同一条教训),
// 所以由后端下发 char_families;首页的"角色数"也必须按**人**算 ——
// 技能侧把每个造型档做成独立卡之后,按卡计数会把《翻篇》的 12 个角色报成 21 个,
// 用户看到的就是"怎么多出来一堆新角色"。

// variantPlan 一份带造型档的角色表(《翻篇》真机数据形态)。
func variantPlan() *project.Plan {
	return &project.Plan{
		Style: "国漫二维动画",
		Characters: []project.Character{
			{ID: "char_01v1", Name: "翻篇·收容所", Kind: "spirit_pet", Description: "土黄短毛小狗,右耳豁口"},
			{ID: "char_01v2", Name: "翻篇·进城", Kind: "spirit_pet", Description: "土黄短毛小狗,右耳豁口"},
			{ID: "char_04v1", Name: "老墙·巡墙", Kind: "human", Description: "巡墙的老差役"},
			{ID: "char_02", Name: "小喇叭", Kind: "spirit_pet", Description: "灰鸽子"},
		},
		Scenes: []project.Scene{{ID: "scene_01", Name: "收容所", ImagePrompt: "a shelter"}},
		Shots: []project.Shot{{
			ID: 1, Scene: "scene_01", Characters: []string{"char_01v1"}, Mode: "ref2va",
			Duration: 5, H3Prompt: "shot", KeyframePrompt: "kf",
		}},
	}
}

func TestCharFamiliesProjection(t *testing.T) {
	s := newTestServer(t)
	proj, err := s.store.Create("翻篇·第01集", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SavePlan(proj.ID, variantPlan()); err != nil {
		t.Fatal(err)
	}

	code, out := doReq(t, s, "GET", "/api/projects/"+proj.ID, nil)
	if code != 200 {
		t.Fatalf("GET 项目详情失败:%d %v", code, out)
	}
	raw, ok := out["char_families"].(map[string]any)
	if !ok {
		t.Fatalf("项目投影必须下发 char_families(前端据此把造型档显示成一个人),实际键:%v", keysOf(out))
	}
	type info struct {
		Family, Base, Variant string
		Master                bool
	}
	got := map[string]info{}
	for id, v := range raw {
		m, _ := v.(map[string]any)
		str := func(k string) string { s, _ := m[k].(string); return s }
		b, _ := m["master"].(bool)
		got[id] = info{Family: str("family"), Base: str("base"), Variant: str("variant"), Master: b}
	}
	want := map[string]info{
		"char_01v1": {"char_1", "翻篇", "收容所", true}, // 档位最靠前 = 主卡(派生起点)
		"char_01v2": {"char_1", "翻篇", "进城", false}, // 同一个人,换了一身衣服
		"char_04v1": {"char_4", "老墙", "巡墙", true},
		"char_02":   {"char_2", "小喇叭", "", true}, // 没有档位的普通角色自成一族
	}
	if len(got) != len(want) {
		t.Fatalf("角色卡与家族信息必须一一对应,实际 %v", got)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s 的家族信息 = %+v,期望 %+v", id, got[id], w)
		}
	}
	// 同一角色的两张卡必须共用一个 family 键(前端分组全靠它)
	if got["char_01v1"].Family != got["char_01v2"].Family {
		t.Error("同一角色的两张造型卡必须共用一个 family 键")
	}
}

// 首页/概览的"角色数"按**人**算:4 张造型卡 = 3 个人。
func TestListProjectsCountsPeopleNotCards(t *testing.T) {
	s := newTestServer(t)
	proj, err := s.store.Create("翻篇·第02集", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SavePlan(proj.ID, variantPlan()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/projects", nil)
	req.Host = "127.0.0.1:8790"
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("项目列表失败:%d %s", w.Code, w.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("项目列表不是数组:%v", err)
	}
	for _, it := range list {
		if it["id"] != proj.ID {
			continue
		}
		n, _ := it["characters"].(float64)
		if int(n) != 3 {
			t.Errorf("首页角色数应按人算:4 张造型卡 = 3 个人,实际 %d(按卡计数会让用户以为多出 1 个新角色)", int(n))
		}
		return
	}
	t.Fatalf("列表里没有刚建的项目:%v", list)
}
