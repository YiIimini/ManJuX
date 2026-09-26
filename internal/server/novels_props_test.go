package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// patchProps 往某集的 plan.json 里塞 props(测试造包用:mkPackage 造的包里没有道具)。
func patchProps(t *testing.T, planPath, propsJSON string) {
	t.Helper()
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("读 plan 失败: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("plan 解析失败: %v", err)
	}
	var props []any
	if err := json.Unmarshal([]byte(propsJSON), &props); err != nil {
		t.Fatalf("props 解析失败: %v", err)
	}
	m["props"] = props
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(planPath, out, 0o644); err != nil {
		t.Fatalf("写 plan 失败: %v", err)
	}
}

// TestNovelScriptIncludesProps 小说库详情弹窗的「道具」页签(用户 2026-09-26:
// 「在小说库的弹窗里新增 道具 TAG」)。
//
// 钉三件事:
//
//	① plan 里的 props 必须**真的**被读出来并汇总(此前 project.Plan 根本没有这个字段 ——
//	   技能侧写的道具被平台静默丢弃,平台再重写一次 plan.json 就永久没了);
//	② 跨集按 ID 去重并计数(技能侧的池子是全书共享的,同一件道具会在 40 集里反复出现);
//	③ 字段容错:老包只有 en(英文外观短语),新包才有 description / image_prompt ——
//	   缺字段不许崩,也不许凭空造一个空提示词出来骗前端。
func TestNovelScriptIncludesProps(t *testing.T) {
	s := newTestServer(t)
	book := mkBookWithScript(t, s, "书道具", "脚本", 2)
	p1 := filepath.Join(book, "脚本", epName(1), "plan", "plan.json")
	p2 := filepath.Join(book, "脚本", epName(2), "plan", "plan.json")
	// 第 1 集:新格式(描述 + 提示词都在)
	patchProps(t, p1, `[{"id":"prop_01","name":"旧铜镜","en":"an old bronze mirror",
		"description":"旧铜镜,镜背浮雕缠枝纹,边缘磕碰、绿锈斑驳",
		"image_prompt":"a close-up of an old bronze mirror with engraved vines, plain background, no people"}]`)
	// 第 2 集:同一件道具(只重复出现,不计新条目)+ 一件只有 en 的老格式道具
	patchProps(t, p2, `[{"id":"prop_01","name":"旧铜镜","en":"an old bronze mirror"},
		{"id":"prop_02","name":"木匣","en":"a carved wooden box"}]`)

	code, out := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(book), nil)
	if code != 200 {
		t.Fatalf("应 200,实际 %d(%v)", code, out)
	}
	props, _ := out["props"].([]any)
	if len(props) != 2 {
		t.Fatalf("两集里共 2 件道具(prop_01 跨集重复只算一条),实际 %d:%v", len(props), props)
	}
	first, _ := props[0].(map[string]any)
	if first["name"] != "旧铜镜" {
		t.Errorf("首条应是池子顺序里的第一件,实际 %v", first["name"])
	}
	if in, _ := first["in"].(float64); int(in) != 2 {
		t.Errorf("旧铜镜出现在 2 集,in 应为 2,实际 %v", first["in"])
	}
	if p, _ := first["prompt"].(string); !strings.Contains(p, "bronze mirror") {
		t.Errorf("新格式的 image_prompt 必须带出来,实际 %q", p)
	}
	if d, _ := first["desc"].(string); !strings.Contains(d, "绿锈") {
		t.Errorf("中文描述必须带出来,实际 %q", d)
	}

	// 老格式:不崩,en 带出来,而且**不造假**一个空 prompt/desc
	second, _ := props[1].(map[string]any)
	if second["name"] != "木匣" {
		t.Errorf("第二件应是木匣,实际 %v", second["name"])
	}
	if en, _ := second["en"].(string); !strings.Contains(en, "wooden box") {
		t.Errorf("老格式的 en 要带出来(它是那时候唯一的外观信息),实际 %q", en)
	}
	if _, has := second["prompt"]; has {
		t.Error("技能侧没给的提示词不许凭空造一个出来 —— 前端据此显示「还没输出」")
	}
	if _, has := second["desc"]; has {
		t.Error("技能侧没给的描述同理")
	}
}
