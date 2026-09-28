package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 逐集串行 + 预解析安全窗口的守卫。
//
// 背景（2026-09-28 同一天里被用户纠了两次）：
//   - 第一次「说了 LLM 解析按集来，为什么一来就直接两集开始解析」—— 违规的是**两集同时解析**；
//   - 第二次「提前预解析那也是当前没有相同任务的条件下才去执行啊」—— **有条件的提前是允许的**，
//     该修的是门，不是机制（我第一版把整个预解析删掉了，读窄了）。
//
// 所以规则落成两条，各自可机检：
//  ① 预解析必须过 canPreparse：上一集方案已落盘 + 此刻没有别的 LLM 解析在跑 + 下一集确实缺方案；
//  ② 产线启动点只有两处（当前集、预解析的下一集），而且预解析必须在**等本集跑完的循环内**动手
//     —— 起当前集之前就动手，就是"第 1 集刚开跑、第 2 集也在解析"那个形态。

// ① 判据穷举：只有"下一集缺方案 + 上一集方案已就绪 + LLM 空闲"才放行。
//
// 三个条件各自对应一次真实翻车：LLM 忙 ⇒ 两集同时解析（用户第一次报）；上一集方案没落盘
// ⇒ 跨集角色锚定读不到（bookRoster 扫的就是兄弟集的 plan.json）⇒ 下一集自己发明角色名与
// 形象 ⇒ 跨集漂移；下一集已有方案 ⇒ 白烧一遍 token。
func TestCanPreparseRequiresSafeWindow(t *testing.T) {
	cases := []struct {
		prevReady, llmBusy, nextNeeds bool
		want                          bool
	}{
		{true, false, true, true},   // 唯一放行的组合：本集方案已就绪 + LLM 空闲 + 下一集缺方案
		{true, true, true, false},   // LLM 还在解析 ⇒ 不许再起一集的解析（"一来就两集"）
		{false, false, true, false}, // 上一集方案还没落盘 ⇒ roster 断链，会跨集漂移
		{false, true, true, false},
		{true, false, false, false}, // 下一集已有方案 ⇒ 不必再解析
		{true, true, false, false},
		{false, false, false, false},
		{false, true, false, false},
	}
	for _, c := range cases {
		if got := canPreparse(c.prevReady, c.llmBusy, c.nextNeeds); got != c.want {
			t.Errorf("canPreparse(prevPlanReady=%v, llmBusy=%v, nextNeedsPlan=%v) = %v，想要 %v",
				c.prevReady, c.llmBusy, c.nextNeeds, got, c.want)
		}
	}
}

var rePipeStart = regexp.MustCompile(`s\.pipe\.Start\(([^)]*)\)`)

// ② 源码守卫：启动点的数量与位置、预解析的调用时机、以及"预解析必须过门"。
//
// 为什么不写成行为测试：真把产线拉起来（startBatch）在测试进程里会一路走到 quitApp(os.Exit)
// 掀掉整个测试进程（见 reap_batch_test.go 的说明），所以取源码级判据。
func TestProductionStartsOneEpisodeAtATime(t *testing.T) {
	src := readNovelSplit(t)

	// (a) 产线只许启动两种东西：当前集 ep、预解析的下一集 next。别的集一律不许被这里启动。
	calls := rePipeStart.FindAllStringSubmatch(src, -1)
	if len(calls) != 2 {
		var got []string
		for _, c := range calls {
			got = append(got, "Start("+c[1]+")")
		}
		t.Fatalf("产线的启动点应是 2 处（当前集 + 预解析的下一集），实际 %d 处：%v", len(calls), got)
	}
	for _, c := range calls {
		arg := strings.TrimSpace(c[1])
		if !strings.HasPrefix(arg, "ep,") && !strings.HasPrefix(arg, "next,") {
			t.Errorf("产线不许启动别的集，实际启动的是：%s", arg)
		}
	}

	// (b) 预解析必须在**等本集跑完的循环内**动手：挪回"起当前集之前"，就是"一来就两集"。
	call := strings.Index(src, "s.preparseNextScript(")
	loop := strings.Index(src, "for time.Now().Before(hardDeadline) {")
	if call < 0 || loop < 0 {
		t.Fatalf("找不到预解析调用点或等待循环（call=%d loop=%d）—— 这段结构变了，守卫要跟着更新", call, loop)
	}
	if call < loop {
		t.Error("preparseNextScript 必须在等本集跑完的循环内调用：起当前集之前就动手，" +
			"等于第 1 集刚开跑就去解析第 2 集（用户报的「一来就直接两集开始解析」）")
	}

	// (c) 预解析必须过 canPreparse 的两道门，且"LLM 是否空闲"要问 Coordinator 的**实时**阶段。
	fn := functionBody(src, "func (s *Server) preparseNextScript")
	if fn == "" {
		t.Fatal("找不到 preparseNextScript 函数体")
	}
	for _, want := range []string{"canPreparse(", "RunningPhase(", "hasUsablePlan("} {
		if !strings.Contains(fn, want) {
			t.Errorf("preparseNextScript 里缺少 %s —— 预解析必须过门（上一集方案已落盘 + 此刻无别的 LLM 解析在跑）", want)
		}
	}

	// (d) pipeline.PhaseScript（点名"只跑解析阶段"）只许出现在预解析里：产线起当前集走的是
	//     PhaseAll/PhaseAssets，不需要点名 script 阶段。
	total := strings.Count(src, "pipeline.PhaseScript")
	if n := strings.Count(fn, "pipeline.PhaseScript"); n != total {
		t.Errorf("pipeline.PhaseScript 只能出现在 preparseNextScript 里（产线起当前集走 PhaseAll/PhaseAssets）："+
			"文件里 %d 处，其中预解析内 %d 处", total, n)
	}
}

func readNovelSplit(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("novel_split.go"))
	if err != nil {
		t.Fatalf("读不到产线源码 novel_split.go: %v", err)
	}
	return string(raw)
}

// functionBody 取某个顶层函数从签名到下一个顶层 func 之间的源码。
func functionBody(src, sig string) string {
	i := strings.Index(src, sig)
	if i < 0 {
		return ""
	}
	rest := src[i+len(sig):]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		return rest[:j]
	}
	return rest
}
