package library

// 索引的**并发**守卫（2026-09-29 真机补）。
//
// 【为什么需要它】索引的**重建会发生在读路径上**（第一次读、写之后第一次读），
// 而读路径只持 `mu` 的读锁 —— 界面上列表在刷、管线在查库时，两个读同时重建就是
// "并发写 map"。原来的实现靠"读方恰好也拿着写锁"是**巧合**：`List` / `matchByID` /
// `findInFamily` 都挂着 RLock，而 `-race` 自检恰好没跑这个包（只跑 pipeline + server）
// ⇒ 这类问题可以一直潜伏到现场 panic 为止。
//
// 这条测试与 `index_scan_guard_test.go` 是一对：那条管"读路径别扫盘"，
// 这条管"并发读写别互相踩"。跑 `go test -race ./internal/library/` 才有意义
// （scripts/check.ps1 的竞态门已把这个包纳进来）。

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestIndexConcurrentReadWrite(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	img := filepath.Join(src, "p.png")
	if err := os.WriteFile(img, tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	// 先铺一批（索引此时还没建）
	for i := 0; i < 40; i++ {
		name := "并发角色" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		if _, err := l.PutCard("characters", "char_"+name, name, "描述"+name, "P", img, "p1", "test"); err != nil {
			t.Fatal(err)
		}
	}

	const readers, writers, rounds = 8, 4, 60
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读方：把库里所有读入口都压一遍（List 会触发整份重建，是最容易撞上的那条）
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				name := "并发角色" + string(rune('A'+(i+r)%26)) + "a"
				l.Match("characters", name)
				l.FindForCard("characters", "char_"+name, name, "")
				l.MatchCard("characters", "char_"+name, name)
				l.Find("characters", name, "")
				l.List()
				l.RelatedFiles("characters", name)
				select {
				case <-stop:
					return
				default:
				}
			}
		}(r)
	}
	// 写方：入库 / 改元数据 / 记复用 / 删 —— 每个都会动索引
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				name := "并发角色" + string(rune('A'+(i+w)%26)) + "a"
				switch i % 4 {
				case 0:
					_, _ = l.PutCard("characters", "char_"+name, name, "描述", "P", img, "p1", "test")
				case 1:
					_, _ = l.SetMeta("characters", name, "改过的描述", "P2")
				case 2:
					l.BumpUsed("characters", name)
				case 3:
					_, _ = l.SetEntityKind("characters", name, "human")
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)

	// 第二阶段：**强制**让多个读同时进入"重建"分支。
	// 第一阶段（上面的读写混跑）能不能撞上并发重建全靠运气 —— 窗口只有"索引失效到重建完成"
	// 那几百微秒；这里每轮先手动让索引失效，再让 N 个读一起去重建，窗口必然重叠。
	for i := 0; i < 40; i++ {
		l.mu.Lock()
		l.bumpLocked()
		l.mu.Unlock()
		var rg sync.WaitGroup
		for r := 0; r < 6; r++ {
			rg.Add(1)
			go func() {
				defer rg.Done()
				l.List()
				l.matchByID("characters", "char_并发角色Aa")
			}()
		}
		rg.Wait()
	}

	// 收尾自检：并发跑完之后，索引仍然对得上盘（没被写坏）
	names, err := os.ReadDir(filepath.Join(root, "characters"))
	if err != nil {
		t.Fatal(err)
	}
	got := len(l.List()["characters"])
	if got != len(names) {
		t.Fatalf("并发读写之后索引与盘上不一致：盘上 %d 条 / 索引 %d 条", len(names), got)
	}
}
