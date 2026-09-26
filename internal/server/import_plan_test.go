package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// import_plan_test.go 双侧共组的入口契约:技能端产出的 plan 从这一条路进平台。
//
// 这一层测的是**分工的接缝**,不是业务逻辑:
//  1. 坏包必须被拒(悬空引用这类"技能端写自己的、平台端认不得"的东西);
//  2. 好包必须被后处理到**与平台自产包完全等价**(台词时长预算 + 中文台词标记),
//     否则"导进来的"和"自己生成的"在渲染眼里是两种东西,双边分工就名存实亡;
//  3. 技能端直接写盘的项目,必须能靠 rescan 被平台看见(不必重启应用)。

func mkPlan() map[string]any {
	return map[string]any{
		"title": "测试集", "logline": "一句话", "style": "PHOTOREAL 3D WORLD",
		"characters": []map[string]any{{
			"id": "char_a", "name": "阿甲", "kind": "human",
			"description": "二十岁男子", "image_prompt": "a young man", "voice": "young male",
		}},
		"scenes": []map[string]any{{
			"id": "scene_room", "name": "破庙", "description": "潮湿的破庙",
			"image_prompt": "a ruined temple",
		}},
		"shots": []map[string]any{{
			"id": 1, "scene": "scene_room", "characters": []string{"char_a"},
			"shot_size": "中景", "camera": "slow push-in", "action": "他站起来",
			"dialogue": []map[string]any{{"speaker": "阿甲", "text": "我准了。"}},
			"narration": "", "duration": 5, "mode": "ref2va",
			// 故意不带 [Chinese] 标记:入口必须补上(与 script 阶段同一处理)
			"h3_prompt": "subject_definitions:\n<d>我准了。</d>",
		}},
	}
}

func newProjectForTest(t *testing.T, s *Server, name string) string {
	t.Helper()
	code, out := doReq(t, s, "POST", "/api/projects", map[string]any{"name": name})
	if code != 200 {
		t.Fatalf("建项目失败: %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		if p, ok := out["project"].(map[string]any); ok {
			id, _ = p["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("建项目响应里没有 id: %v", out)
	}
	return id
}

// TestImportPlanRejectsBadContract 坏包必须 400,且**不许落盘**。
//
// 为什么"不许落盘"要单独断言:落盘了的话,用户点"开始渲染"照样能跑 ——
// 校验就成了只挡一时、挡不住一路的装饰。
func TestImportPlanRejectsBadContract(t *testing.T) {
	s := newTestServer(t)
	id := newProjectForTest(t, s, "导入契约坏包")
	bad := mkPlan()
	bad["shots"].([]map[string]any)[0]["scene"] = "scene_nope" // 悬空场景引用
	code, out := doReq(t, s, "POST", "/api/projects/"+id+"/import-plan",
		map[string]any{"plan": bad})
	if code != 400 {
		t.Fatalf("悬空场景引用必须 400,实际 %d(%v)", code, out)
	}
	fails, _ := out["fails"].([]any)
	if len(fails) == 0 {
		t.Fatalf("响应必须列出具体哪一条 fail,实际 %v", out)
	}
	if joined := strings.Join(anyStrings(fails), ";"); !strings.Contains(joined, "不存在的场景") {
		t.Fatalf("fail 文案要点名悬空引用,实际 %s", joined)
	}
	if _, err := s.store.LoadPlan(id); err == nil {
		t.Fatal("校验失败却把方案落盘了 —— 校验必须挡在存盘之前")
	}
}

// TestImportPlanPostProcessesLikeScriptStage 好包导入后必须与自产包等价。
func TestImportPlanPostProcessesLikeScriptStage(t *testing.T) {
	s := newTestServer(t)
	id := newProjectForTest(t, s, "导入契约好包")
	code, out := doReq(t, s, "POST", "/api/projects/"+id+"/import-plan",
		map[string]any{"plan": mkPlan()})
	if code != 200 {
		t.Fatalf("合规方案应当 200,实际 %d(%v)", code, out)
	}
	if got, _ := out["shots"].(float64); got != 1 {
		t.Fatalf("响应 shots 应为 1,实际 %v", out["shots"])
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		t.Fatalf("方案未落盘: %v", err)
	}
	// ① 中文台词标记兜底(与 stages_script 同一个 EnsureChineseDialogue)
	if !strings.Contains(plan.Shots[0].H3Prompt, "<d>[Chinese]") {
		t.Fatalf("入口没补 [Chinese] 标记,成片会说听不懂的话: %q", plan.Shots[0].H3Prompt)
	}
	if !strings.Contains(plan.Shots[0].H3Prompt, "MANDARIN ONLY") {
		t.Fatal("入口没追加普通话强制句")
	}
	// ② 台词时长预算("我准了。"只要 4 秒,5 秒镜头不该被改)
	if got, _ := out["duration_adjusted"].(float64); got != 0 {
		t.Fatalf("只装得下的台词不该被改时长,实际调整 %v 镜(%v)",
			out["duration_adjusted"], out["duration_details"])
	}
	// ③ 契约信息如实回显
	if c, _ := out["contract"].(string); !strings.Contains(c, "manjux.plan") {
		t.Fatalf("响应必须回显契约版本,实际 %v", out["contract"])
	}
}

// TestImportPlanFitsDialogueDuration 台词装不下时入口按平台规则抬时长,抬不动要如实报。
func TestImportPlanFitsDialogueDuration(t *testing.T) {
	s := newTestServer(t)
	id := newProjectForTest(t, s, "导入契约台词")
	p := mkPlan()
	sh := p["shots"].([]map[string]any)[0]
	sh["duration"] = 4
	sh["dialogue"] = []map[string]any{{"speaker": "阿甲", "text": strings.Repeat("字", 30)}}
	code, out := doReq(t, s, "POST", "/api/projects/"+id+"/import-plan",
		map[string]any{"plan": p})
	if code != 200 {
		t.Fatalf("应当 200(抬时长是平台的正常动作),实际 %d(%v)", code, out)
	}
	if got, _ := out["duration_adjusted"].(float64); got != 1 {
		t.Fatalf("30 字台词装不进 4 秒,应调整 1 镜,实际 %v(%v)",
			out["duration_adjusted"], out["duration_details"])
	}
	plan, _ := s.store.LoadPlan(id)
	// 30 字 ÷ 4.5 + 0.3 = 6.97 → 7 秒
	if plan.Shots[0].Duration != 7 {
		t.Fatalf("时长应被抬到 7 秒,实际 %d", plan.Shots[0].Duration)
	}

	// 抬不动的:60 字需约 13.6 秒 > 10 秒上限 → 如实报 overflow,不许静默
	id2 := newProjectForTest(t, s, "导入契约超长台词")
	p2 := mkPlan()
	sh2 := p2["shots"].([]map[string]any)[0]
	sh2["duration"] = 10
	sh2["dialogue"] = []map[string]any{{"speaker": "阿甲", "text": strings.Repeat("字", 60)}}
	code, out2 := doReq(t, s, "POST", "/api/projects/"+id2+"/import-plan",
		map[string]any{"plan": p2})
	if code != 200 {
		t.Fatalf("超长台词不拒收(拒了没法拆镜),实际 %d(%v)", code, out2)
	}
	overflow, _ := out2["dialogue_overflow"].([]any)
	if len(overflow) == 0 {
		t.Fatalf("抬到上限仍装不下时必须如实报 overflow,实际 %v", out2)
	}
}

// TestRescanPicksUpSkillProducedProject 技能端直接写盘 → 平台不重启也能看见。
//
// 这是双边分工的最后一环:技能端把成套项目包写进 projects/<书名>/<集名>/,
// 若平台只在启动时扫盘,用户就得重启应用才看得见刚产出的集。
func TestRescanPicksUpSkillProducedProject(t *testing.T) {
	s := newTestServer(t)
	const series, ep = "桥接测试书", "桥接测试书·第01集·开头"
	dir := filepath.Join(s.store.Root(), series, ep)
	for _, sub := range []string{"plan", "assets/characters", "assets/scenes", "clips", "output"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	meta := map[string]any{"id": ep, "name": ep, "config": map[string]any{}}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "project.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	planRaw, _ := json.Marshal(mkPlan())
	if err := os.WriteFile(filepath.Join(dir, "plan", "plan.json"), planRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := doReq(t, s, "POST", "/api/projects/rescan", nil)
	if code != 200 {
		t.Fatalf("rescan 应 200,实际 %d(%v)", code, out)
	}
	added, _ := out["added"].([]any)
	found := false
	for _, a := range added {
		if s, _ := a.(string); s == ep {
			found = true
		}
	}
	if !found {
		t.Fatalf("外部写盘的项目没被发现,added=%v", added)
	}
	// 看不见 = 没装载;再查一次项目详情确认它真的进了索引
	if code, _ := doReq(t, s, "GET", "/api/projects/"+ep, nil); code != 200 {
		t.Fatalf("重扫后项目详情仍取不到(%d)—— 只加进 added 列表没进索引", code)
	}
}

func anyStrings(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

var _ = project.PlanContractVersion // 断言契约版本可被 server 侧读到(编译期即可)
