package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 逐集串行的守卫：**一集的 LLM 解析只属于那一集**。
//
// 回归 2026-09-28（用户报「说了 LLM 解析按集来，为什么一来就直接两集开始解析？」）：
// runNovelBatch 里曾有一句 preparseNextScript —— 起第 N 集之前，先把第 N+1 集的
// **剧本分镜**也启动（理由写的是"解析是纯 LLM、不碰显卡，与渲染并行"）。后果是导入后
// 第 1 集刚开跑、第 2 集的分镜也开始解析：两集的日志、两路 LLM 调用同时在跑，与用户两次
// 强调的口径（「LLM 一集一集地去解析」「流水线一样到了那一步就做那一步」）正面冲突。
//
// 判据刻意收在"产线启动点的数量"上（源码扫描，与 frontend_guard_test.go 同一路数）：
//
//	① novel_split.go 里 `s.pipe.Start(` 只能出现 **1 次**，且第一个实参必须是当前集 ep；
//	② 这个文件里不许再出现 pipeline.PhaseScript —— 它是"提前点名解析某一集"的指纹
//	   （产线正常起流程走 PhaseAll / PhaseAssets，不需要点名 script 阶段）。
//
// 为什么不写成行为测试：真把产线拉起来（startBatch）在测试进程里会一路走到
// quitApp(os.Exit) 掀掉整个测试进程（见 reap_batch_test.go 的说明），所以取源码级判据。
var rePipeStart = regexp.MustCompile(`s\.pipe\.Start\(([^)]*)\)`)

func TestProductionStartsOneEpisodeAtATime(t *testing.T) {
	path := filepath.Join("novel_split.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到产线源码 %s: %v", path, err)
	}
	src := string(raw)

	calls := rePipeStart.FindAllStringSubmatch(src, -1)
	if len(calls) != 1 {
		var got []string
		for _, c := range calls {
			got = append(got, "Start("+c[1]+")")
		}
		t.Fatalf("批量产线的启动点必须**有且只有一处**（起当前集），实际 %d 处：%v —— "+
			"多出来的那处就是替别的集抢跑，会重现「一来就两集开始解析」", len(calls), got)
	}
	if arg := strings.TrimSpace(calls[0][1]); !strings.HasPrefix(arg, "ep,") {
		t.Errorf("产线只允许启动**当前集** ep，实际启动的是：%s", arg)
	}
	if strings.Contains(src, "PhaseScript") {
		t.Error("novel_split.go 里不该出现 pipeline.PhaseScript —— 那是「提前把某一集解析掉」的指纹；" +
			"产线起流程走 PhaseAll/PhaseAssets，轮到哪一集才解析哪一集")
	}
	// 判据用「调用形式」而不是裸名字：源码里刻意留了一段说明这段历史的注释（它提到过
	// preparseNextScript 这个名字），注释不该让守卫变红，真正要挡的是**调用**与**定义**。
	if strings.Contains(src, ".preparseNextScript(") || strings.Contains(src, "func (s *Server) preparseNextScript") {
		t.Error("preparseNextScript 又回来了 —— 它就是「一来就两集」的根因（2026-09-28 删除）")
	}
}
