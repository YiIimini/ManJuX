package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// putShot / importPlan 两条改方案入口的契约测试(审计 2026-09-26:M30 / M31)。
//
// 这两条都是"改完方案、平台却还按旧方案说话"的静默失效:
//   - M30 putShot 静默忽略非法时长还回 200 ok,且不跑与分镜/导入同等的那份后处理
//     (台词时长预算 / [Chinese] 标记 / 画外音约束 / 台词溢出告警);
//   - M31 改完方案不作废派生品:成片就绪的判据只是 output/final.mp4 存在,
//     于是用户下载到的是旧方案的成片,质检结论也停在上一版。
//
// 两条都用真装配的服务器跑(http_contract_test.go 的 newTestServer + doReq),
// 断言落在**文件系统与响应体**上,而不是函数调用次数。

// seedProjectWithPlan 建一个真项目并写入一份分镜方案与"已产出的派生品"。
func seedProjectWithPlan(t *testing.T, s *Server, shots []project.Shot) string {
	t.Helper()
	code, out := doReq(t, s, "POST", "/api/projects", map[string]any{"name": "改方案契约"})
	if code != 200 {
		t.Fatalf("创建项目应 200,实际 %d(%v)", code, out)
	}
	proj, _ := out["project"].(map[string]any)
	if proj == nil {
		proj = out
	}
	id, _ := proj["id"].(string)
	if id == "" {
		t.Fatalf("创建响应缺少项目 id:%v", out)
	}
	if err := s.store.SavePlan(id, &project.Plan{Shots: shots}); err != nil {
		t.Fatalf("写方案失败:%v", err)
	}
	// 伪造派生品:成片 + 质检报告
	outDir := filepath.Join(s.store.Dir(id), "output")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"final.mp4", "qc_report.json"} {
		if err := os.WriteFile(filepath.Join(outDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func derivedCount(t *testing.T, s *Server, id string) int {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(s.store.Dir(id), "output"))
	if err != nil {
		return 0
	}
	return len(ents)
}

// TestPutShotInvalidatesDerivedAndRejectsBadDuration M30 + M31。
func TestPutShotInvalidatesDerivedAndRejectsBadDuration(t *testing.T) {
	s := newTestServer(t)
	id := seedProjectWithPlan(t, s, []project.Shot{
		{ID: 1, Duration: 6, H3Prompt: "镜1", Dialogue: []project.Line{{Speaker: "甲", Text: "一二三四五"}}},
		{ID: 2, Duration: 6, H3Prompt: "镜2"},
	})
	if got := derivedCount(t, s, id); got != 2 {
		t.Fatalf("前置条件:应有 2 项派生品,实际 %d", got)
	}

	// ① 非法时长(12 秒,契约区间 4–10)必须**明确报错**,不能静默忽略还回 ok
	code, out := doReq(t, s, "PUT", "/api/projects/"+id+"/shot", map[string]any{
		"shot_id": 1, "duration": 12,
	})
	if code != 400 {
		t.Errorf("区间外时长应 400(旧实现静默忽略并回 200 ok),实际 %d(%v)", code, out)
	}
	plan, _ := s.store.LoadPlan(id)
	if plan != nil && plan.Shots[0].Duration != 6 {
		t.Errorf("被拒的请求不该改动方案,实际时长 %d", plan.Shots[0].Duration)
	}
	if got := derivedCount(t, s, id); got != 2 {
		t.Errorf("被拒的请求不该作废派生品,实际剩 %d 项", got)
	}

	// ② 合法改时长:方案要落地、派生品要作废、响应要如实回显
	code, out = doReq(t, s, "PUT", "/api/projects/"+id+"/shot", map[string]any{
		"shot_id": 1, "duration": 8,
	})
	if code != 200 {
		t.Fatalf("合法改时长应 200,实际 %d(%v)", code, out)
	}
	plan, _ = s.store.LoadPlan(id)
	if plan == nil || plan.Shots[0].Duration != 8 {
		t.Fatalf("时长未落盘:%+v", plan)
	}
	if got := derivedCount(t, s, id); got != 0 {
		t.Errorf("改了时长必须作废成片/质检报告(否则「成片就绪」指向旧成片),实际剩 %d 项", got)
	}
	if n, _ := out["derived_invalidated"].(float64); n != 2 {
		t.Errorf("响应应回显作废了几项,实际 %v", out["derived_invalidated"])
	}

	// ③ 台词装不下的镜次:响应必须带上溢出告警(而不是静默放过成片截断)
	if err := s.store.SavePlan(id, &project.Plan{Shots: []project.Shot{{
		ID: 1, Duration: 4, H3Prompt: "镜1",
		Dialogue: []project.Line{{Speaker: "甲", Text: "这是一句非常非常长的台词要占满四秒镜头根本说不完还会被截断"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	code, out = doReq(t, s, "PUT", "/api/projects/"+id+"/shot", map[string]any{
		"shot_id": 1, "h3_prompt": "镜1改",
	})
	if code != 200 {
		t.Fatalf("改提示词应 200,实际 %d(%v)", code, out)
	}
	if _, ok := out["dialogue_overflow"]; !ok {
		t.Errorf("响应必须带 dialogue_overflow(台词装不下要如实回显),实际 %v", out)
	}
	if _, ok := out["duration_details"]; !ok {
		t.Errorf("响应必须带 duration_details(时长被后处理调整过要如实回显),实际 %v", out)
	}
}

// TestImportPlanInvalidatesDerived M31:重新导入方案之后,上一版成片必须作废。
func TestImportPlanInvalidatesDerived(t *testing.T) {
	s := newTestServer(t)
	id := seedProjectWithPlan(t, s, []project.Shot{{ID: 1, Duration: 6, H3Prompt: "旧方案"}})
	if got := derivedCount(t, s, id); got != 2 {
		t.Fatalf("前置条件:应有 2 项派生品,实际 %d", got)
	}

	newPlan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 6, H3Prompt: "新方案镜1"},
		{ID: 2, Duration: 5, H3Prompt: "新方案镜2"},
	}}
	code, out := doReq(t, s, "POST", "/api/projects/"+id+"/import-plan", map[string]any{
		"plan": newPlan,
	})
	if code != 200 {
		t.Fatalf("导入方案应 200,实际 %d(%v)", code, out)
	}
	plan, _ := s.store.LoadPlan(id)
	if plan == nil || len(plan.Shots) != 2 {
		t.Fatalf("新方案未落盘:%+v", plan)
	}
	if got := derivedCount(t, s, id); got != 0 {
		t.Errorf("导入新方案必须作废上一版成片/质检报告,实际剩 %d 项", got)
	}
	if n, _ := out["derived_invalidated"].(float64); n != 2 {
		t.Errorf("响应应回显作废了几项,实际 %v", out["derived_invalidated"])
	}
}
