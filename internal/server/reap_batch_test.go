package server

import (
	"os"
	"strings"
	"testing"
	"time"
)

// 【2026-09-28 审计实测】回收守卫的判据**不许按下标算**。
//
// 原写法 `batch.projects[batch.cur]` 而 cur 是 1 基 ⇒ 检查的是**下一集**：产线跑第 1 集时
// 守卫读到"下一集没在跑"，就把活跃批次当死批次回收（并播报"旧批次已让位"这种假消息），
// 随后两个批次一起遍历 projects 调 Start ⇒ 两集并发抢一张显卡
// （2026-09-12「定妆照整批失败、单镜 20+ 分钟」的形态）。
//
// 这里做源码级钉住：真把产线拉起来在测试进程里会走到 quitApp(os.Exit)（见下条用例的说明）。
func TestReapStaleBatchGuardDoesNotIndexByCur(t *testing.T) {
	raw, err := os.ReadFile("novel_split.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if strings.Contains(src, "batch.projects[batch.cur]") {
		t.Error("回收判据又在按 batch.cur 取下标了 —— cur 是 1 基，取到的是**下一集**，" +
			"产线跑第 1 集时守卫会读到「下一集没在跑」，把活跃批次当死批次回收")
	}
	if !strings.Contains(src, "for _, pid := range projects {") {
		t.Error("回收守卫要按「这一批**任一一集**在跑」判（不依赖 cur 的基），否则改成别的写法时又会静默失效")
	}
}

// TestReapStaleBatchFreesSingleton 死批次必须被自动回收 —— 这是本应用自己的 bug:
// 批量产线是单例,失败/停下来的那批 `batch.running` 仍是 true,于是之后谁点都起不来,
// 界面上一行字都没有(2026-09-26 用户:「脚本创作 导入后就完事了?」的真实原因)。
// 修法不是发通知让用户去点「停止」,而是启动前先把死批次请走。
func TestReapStaleBatchFreesSingleton(t *testing.T) {
	s := newTestServer(t)
	// 摆一个"停下来了却还占着"的批次:running=true,但没有任何管线在跑它
	stale := newProjectForTest(t, s, "旧书·第01集·某集")
	batch.mu.Lock()
	batch.running, batch.prefix, batch.projects, batch.cur, batch.stop = true, "旧书", []string{stale}, 0, false
	batch.mu.Unlock()

	start := time.Now()
	s.reapStaleBatch()
	batch.mu.Lock()
	running := batch.running
	batch.mu.Unlock()
	if running {
		t.Fatal("死批次没被回收 —— 新批次永远起不来,用户点了什么都不发生")
	}
	if d := time.Since(start); d > 9*time.Second {
		t.Errorf("回收耗时 %v,太久了(不该让用户干等)", d)
	}

	// 回收后产线必须真的空出来 —— 这就是"能起新批次"的前置条件。
	// 刻意**不在这里真的 startBatch**:那会拉起真实的产线 goroutine,而它在测试环境里
	// 会一路走到 quitApp(os.Exit)把整个测试进程掀掉(2026-09-26 实测 panic)。
	// 单例空出来 + startBatch 自身已有用例覆盖,两者合起来等价于"回收后能开跑"。
	batch.mu.Lock()
	if batch.running || len(batch.projects) != 0 || batch.prefix != "" {
		t.Errorf("回收后单例必须干净:running=%v projects=%d prefix=%q",
			batch.running, len(batch.projects), batch.prefix)
	}
	batch.mu.Unlock()
}
