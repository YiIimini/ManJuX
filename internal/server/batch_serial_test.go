package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 逐集串行的守卫：**一集的活只属于那一集，主线跑完之前不许插别的**。
//
// 用户在同一天里连纠三次，三次都是"趁主线忙着先做点别的"：
//
//	①「说了 LLM 解析按集来，为什么一来就直接两集开始解析」
//	②「资产渲染完成，咋直接 LLM 解析下一集，分镜渲染进程又干嘛了」
//	③「我有没有说合理分配任务，有叫你主线任务流程中断不做吗？」
//
// 前两次我都在给"提前解析下一集"加条件（先加门、再挪到安全窗口），方向就是错的：
// 用户要的是**主线不被打断**，不是"有条件地插队"。所以那段逻辑现在是**不存在的**。
//
// 判据收在"产线的启动点"上（源码扫描，与 frontend_guard_test.go 同一路数）：
//
//	① novel_split.go 里 `s.pipe.Start(` 只能出现 1 次，且第一个实参必须是当前集 ep；
//	② 这个文件里不许出现 pipeline.PhaseScript（"提前点名解析某一集"的指纹）；
//	③ 不许出现 preparseNextScript / canPreparse 这类名字（那段逻辑已经两度复活）。
//
// 为什么不写成行为测试：真把产线拉起来（startBatch）在测试进程里会一路走到
// quitApp(os.Exit) 掀掉整个测试进程（见 reap_batch_test.go 的说明），所以取源码级判据。
var rePipeStart = regexp.MustCompile(`s\.pipe\.Start\(([^)]*)\)`)

func TestProductionStartsOneEpisodeAtATime(t *testing.T) {
	src := readNovelSplit(t)

	// (a) 启动点有且只有一处，且只能是当前集。
	calls := rePipeStart.FindAllStringSubmatch(src, -1)
	if len(calls) != 1 {
		var got []string
		for _, c := range calls {
			got = append(got, "Start("+c[1]+")")
		}
		t.Fatalf("产线的启动点必须**有且只有一处**（起当前集），实际 %d 处：%v —— "+
			"多出来的那处就是趁主线忙着去动别的集", len(calls), got)
	}
	if arg := strings.TrimSpace(calls[0][1]); !strings.HasPrefix(arg, "ep,") {
		t.Errorf("产线只允许启动**当前集** ep，实际启动的是：%s", arg)
	}

	// (b) 不许再出现"点名解析某一集"的相位，也不许再长出提前解析那套名字。
	if strings.Contains(src, "PhaseScript") {
		t.Error("novel_split.go 里不该出现 pipeline.PhaseScript —— 那是「提前把某一集解析掉」的指纹；" +
			"轮到哪一集才解析哪一集")
	}
	for _, banned := range []string{"preparseNextScript", "canPreparse"} {
		if strings.Contains(src, banned) {
			t.Errorf("%s 又回来了 —— 它两度把主线插断（用户在同一天里点名三次）", banned)
		}
	}

	// (c) 产线起当前集必须是**全流程 PhaseAll** —— 它会自己按"这一集有没有方案"决定跳不跳 script
	//     （见 pipeline.pipelineStages）。2026-09-28 用户报「资产渲染完成，咋直接 LLM 解析下一集」：
	//     根因就是这里曾降级成 `PhaseAssets`（**单阶段**语义）⇒ 任何「已有方案」的集在产线里只出完
	//     资产就被当成跑完、产线立刻推进下一集，一个镜头都没渲。
	if !strings.Contains(src, `s.pipe.Start(ep, pipeline.PhaseAll, nil, "", 0)`) {
		t.Error("产线起当前集必须用 pipeline.PhaseAll（全流程）：有方案的集由 pipelineStages 自动跳过 script，" +
			"而不是由这里降级成单阶段")
	}
	if strings.Contains(src, "pipeline.PhaseAssets") {
		t.Error("产线不许把当前集降级成单阶段 PhaseAssets —— 那会让「已有方案」的集只出资产就当成跑完，一个镜头都不渲")
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
