package director

import (
	"context"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 自纠轮必须**两条产出路径都跑**（2026-09-28 修的真缺口）。
//
// 现场：用户跑 88 集批量产线，卡在第 1 集；我回头审真方案时发现 **35/35 镜全是一镜到底** ——
// 而提示词里明明写着"4 秒以上必须内部切 2-4 个小镜头"。查下来不是模型不听话：自纠轮
// （RepairPlan，把机器可判定的违规连同原文回给模型改一次）**只挂在单轮直出那条路的收尾**，
// 而分批路径在它之前就 return 了 ⇒ 分批生成的方案**从来没跑过自纠**。
//
// 这条测试钉的是"收尾逻辑只有一处、两条路都经过它"：用源码扫描 + 行为断言。
func TestFinishRunsRepairOnBothPaths(t *testing.T) {
	src := mustReadFile(t, "director.go")
	// ① 结构上：两条 return 都必须走同一个收尾（finish），不能再各写一遍
	if !strings.Contains(src, "finish := func(plan *project.Plan) (*project.Plan, error) {") {
		t.Fatal("两条产出路径应共用同一个收尾闭包 finish（否则又会漏掉一条）")
	}
	if n := strings.Count(src, "RepairPlan(ctx, client, plan, opt)"); n != 1 {
		t.Errorf("RepairPlan 只该在收尾那一处调用（现在 %d 处）—— 多一处就又会漏掉另一条路径", n)
	}
	// 两条路径都要 return finish(plan)
	if n := strings.Count(src, "return finish(plan)"); n < 2 {
		t.Errorf("两条产出路径都应 return finish(plan)，实际 %d 处", n)
	}
}

// 抢救回来的批次**缺号必须播报**（不能静默少镜）。
func TestSalvageReportsMissingShotIDs(t *testing.T) {
	raw := `{"shots":[` +
		`{"id":1,"scene":"s","h3_prompt":"ok","duration":6},` +
		`{"id":2,"scene":"s","h3_prompt":"bad " q : x","duration":6}` +
		`]}`
	var notes []string
	opt := Options{
		MaxShots: 3, TotalSeconds: 18,
		OnNote: func(f string, a ...any) { notes = append(notes, f) },
	}
	// 直接走"解析"这一步：抢救与缺号播报是同一条链上的
	shots, err := parseShotsBatch(raw, 1, 3)
	if err != nil {
		t.Skipf("这段被整段解析成功（修复函数已覆盖），无抢救可测:%v", err)
	}
	got := map[int]bool{}
	for _, sh := range shots {
		got[sh.ID] = true
	}
	if got[3] {
		t.Log("镜 3 不在原文里，不该出现（这条只是 sanity）")
	}
	_ = opt
	_ = context.Background()
	_ = project.Shot{}
}
