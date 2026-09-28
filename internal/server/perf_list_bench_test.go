package server

// 首页列表的**实测**工具（性能审计 #6：列表摘要缓存）。
//
// 前端每 15 秒拉一次 `/api/projects`，而它原来对**每个项目**做一次完整
// `LoadPlan`（解析 + 全镜清洗 + HealPlan）与 `LoadManifest` —— 项目多起来之后
// 这是"用户什么都没点、只在首页看着"时持续发生的读盘与分配。
//
// 这里用**真实方案文件**（MJX_PERF_PLAN 指向一份真跑出来的 plan.json）铺 N 个项目，
// 在同一进程里量两种口径：
//
//	命中    ：连着拉 N 次列表，摘要走缓存
//	未命中  ：每次拉之前清掉缓存 —— 等价于改动之前的"每次都重解析"
//
// 并顺带断言两种口径算出来的摘要**完全一致**（缓存最危险的失败是"命中了一个错的"，
// 那个错不会报错、只会让列表上的镜数/角色数慢慢跟盘上不一致）。
//
// 没有真实方案文件时自动跳过：它不该拖慢日常 `go test`。

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/project"
)

func perfPlan(tb testing.TB) []byte {
	p := os.Getenv("MJX_PERF_PLAN")
	if p == "" {
		tb.Skip("未提供 MJX_PERF_PLAN（真实 plan.json 路径），跳过这项实测")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		tb.Skipf("方案文件读不出来：%v", err)
	}
	return raw
}

func TestPerfListProjectsCache(t *testing.T) {
	planRaw := perfPlan(t)
	n := 88
	if v := os.Getenv("MJX_PERF_PROJECTS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	s := newTestServer(t)
	projSumMu.Lock()
	projSums = map[string]projSummary{}
	projSumMu.Unlock()
	t.Cleanup(func() {
		projSumMu.Lock()
		projSums = map[string]projSummary{}
		projSumMu.Unlock()
	})

	cfg := project.ProjectConfig{Params: project.DefaultStageParams()}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("名册之外，八万零一号·第%02d集·第%02d章", i+1, i+1)
		p, err := s.store.Create(id, cfg)
		if err != nil {
			t.Fatalf("建项目失败：%v", err)
		}
		// 走 Store 的真源路径（plan/plan.json）—— 这正是缓存键取 mtime 的那份文件
		dst := s.store.PlanPath(p.ID)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, planRaw, 0o644); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}

	run := func(rounds int, clearEachTime bool) (time.Duration, uint64) {
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		start := time.Now()
		for i := 0; i < rounds; i++ {
			if clearEachTime {
				projSumMu.Lock()
				projSums = map[string]projSummary{}
				projSumMu.Unlock()
			}
			rec := httptest.NewRecorder()
			s.listProjects(rec, httptest.NewRequest("GET", "/api/projects", nil))
		}
		d := time.Since(start)
		runtime.ReadMemStats(&m1)
		return d / time.Duration(rounds), m1.TotalAlloc - m0.TotalAlloc
	}

	// 先热一次缓存，再分别测
	sumCached := make([]projSummary, 0, n)
	for _, id := range ids {
		if p, ok := s.store.Get(id); ok {
			sumCached = append(sumCached, s.projectSummary(p))
		}
	}
	hitPer, hitAlloc := run(20, false)
	// 缓存口径 vs 每次重解析口径必须**逐项一致**
	projSumMu.Lock()
	projSums = map[string]projSummary{}
	projSumMu.Unlock()
	for i, id := range ids {
		p, ok := s.store.Get(id)
		if !ok {
			t.Fatal("刚建的项目就查不到")
		}
		if got := s.projectSummary(p); got != sumCached[i] {
			t.Fatalf("缓存命中的摘要与重解析不一致（%s）：缓存 %+v / 重解析 %+v", id, sumCached[i], got)
		}
	}
	missPer, missAlloc := run(5, true)

	sz := 0
	for _, id := range ids {
		if st, err := os.Stat(s.store.PlanPath(id)); err == nil {
			sz = int(st.Size())
		}
	}
	t.Logf("%d 个项目，每个带一份真实方案（%.0f KB/份，共 %.0f MB）",
		n, float64(sz)/1024, float64(n*sz)/(1<<20))
	t.Logf("每次拉列表：命中缓存 %.2f ms / 分配 %.2f MB　→　每次重解析 %.2f ms / 分配 %.2f MB（%.0f× 慢）",
		float64(hitPer.Microseconds())/1000, float64(hitAlloc)/(1<<20),
		float64(missPer.Microseconds())/1000, float64(missAlloc)/(1<<20),
		float64(missPer)/float64(hitPer))
}
