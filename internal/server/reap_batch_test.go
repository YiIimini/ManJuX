package server

import (
	"testing"
	"time"
)

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

	// 回收之后,新批次必须能正常启动
	if !s.startBatch("新书", []string{"新书·第01集·第一"}) {
		t.Fatal("回收后新批次仍起不来")
	}
	batch.mu.Lock()
	got := batch.prefix
	batch.mu.Unlock()
	if got != "新书" {
		t.Errorf("产线应归新批次,实际 %q", got)
	}
	// 清理:批次是全局单例,别把状态漏给别的用例
	batch.mu.Lock()
	batch.stop, batch.running, batch.projects, batch.prefix, batch.cur = false, false, nil, "", 0
	batch.mu.Unlock()
}
