package server

// 列表摘要缓存的守卫（2026-09-29 真机实测补）。
//
// 缓存最危险的失败不是"没命中"，而是**命中了过期的那个**：列表上的镜数/角色数
// 会一直停在第一次解析时的样子，而界面上没有任何异常。这条缓存的关键就是
// "键取 plan / manifest 的 mtime"——而那两个路径**必须是盘上的真实位置**。
//
// 实测现场：真跑《名册之外，八万零一号》第 01 集时，`projectSummary` 里写的是
// `<项目目录>/plan.json` 与 `<项目目录>/manifest.json`，真实位置却是
// `plan/plan.json` 与 `clips/manifest.json`（见 Store.PlanPath / ManifestPath）。
// 两个 os.Stat 永远失败 ⇒ mtime 恒为零值 ⇒ 方案从"没有"变成"13 镜 112KB"之后，
// 首页仍一直显示 has_plan=false / shots_total=0，直到重启。
//
// 所以这里的判据不是"缓存有没有生效"，而是**盘上的方案变了，摘要必须跟着变**。

import (
	"os"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/project"
)

func TestProjectSummaryFollowsPlanOnDisk(t *testing.T) {
	s := newTestServer(t)
	projSumMu.Lock()
	projSums = map[string]projSummary{}
	projSumMu.Unlock()
	t.Cleanup(func() {
		projSumMu.Lock()
		projSums = map[string]projSummary{}
		projSumMu.Unlock()
	})

	p, err := s.store.Create("缓存守卫·第01集·方案变化", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	// ① 还没有方案：解析一次，把它放进缓存
	if sum := s.projectSummary(p); sum.hasPlan || sum.total != 0 {
		t.Fatalf("此时还没有方案，摘要应为空，实得 %+v", sum)
	}
	// ② 方案落盘（走 Store 的唯一写入口，即盘上的真实位置）
	plan := &project.Plan{
		Title: "缓存守卫",
		Shots: []project.Shot{
			{ID: 1, Scene: "s1", Duration: 5, H3Prompt: "shot one"},
			{ID: 2, Scene: "s1", Duration: 5, H3Prompt: "shot two"},
			{ID: 3, Scene: "s2", Duration: 5, H3Prompt: "shot three"},
		},
		Characters: []project.Character{{ID: "c1", Name: "祁七"}},
		Scenes:     []project.Scene{{ID: "s1", Name: "舱内"}, {ID: "s2", Name: "镜面走廊"}},
	}
	if err := s.store.SavePlan(p.ID, plan); err != nil {
		t.Fatal(err)
	}
	p2, _ := s.store.Get(p.ID)
	sum := s.projectSummary(p2)
	if !sum.hasPlan {
		t.Fatal("方案已经落盘，摘要却仍说没有方案 —— 缓存键指的路径与盘上的真实位置不是一处（见本文件顶部说明）")
	}
	if sum.total != 3 || sum.chars != 1 || sum.scenes != 2 {
		t.Fatalf("摘要没跟上盘上的方案：镜 %d（应 3）/ 角色 %d（应 1）/ 场景 %d（应 2）", sum.total, sum.chars, sum.scenes)
	}
	// ③ 方案又变了（多一镜）：摘要必须再变 —— 只有 mtime 进了键才可能做到
	plan.Shots = append(plan.Shots, project.Shot{ID: 4, Scene: "s2", Duration: 5, H3Prompt: "shot four"})
	if err := s.store.SavePlan(p.ID, plan); err != nil {
		t.Fatal(err)
	}
	// **显式把 mtime 推后**：缓存键取的是修改时间，而"两次写落在同一个时间刻度上"
	// 在粗精度文件系统/高负载下会假绿（本仓 fingerprint_cache_test.go 已吃过这一课）。
	mtime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(s.store.PlanPath(p.ID), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	p3, _ := s.store.Get(p.ID)
	if sum := s.projectSummary(p3); sum.total != 4 {
		t.Fatalf("方案改成 4 镜之后摘要没跟着变（实得 %d）—— 缓存永久命中，列表会一直停在旧值", sum.total)
	}
}
