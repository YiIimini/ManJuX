package library

// 「读路径不得扫盘」的守卫（2026-09-29 真机实测补）。
//
// ## 为什么需要它
//
// 库索引那次改动（性能审计 #1）把 `matchByNameLocked` / `findWith` / `List` 改走索引，
// 唯独漏了 `matchByIDLocked` —— 而它正是**资产阶段最热的那个入口**：
// `FindForCard(kind, cardID, …)`（每张卡第一步就问库）先落到它。
// 实测（600 条真实卡片）：按卡 ID 查 25~30 ms/次、一集 15 张卡 ≈ 210 ms 白烧，
// 与"加索引前"一模一样。
//
// 这类漏检是**静默**的：结果全对、没有任何测试变红、只在产量上来之后表现为"平台变慢"。
// 所以判据不能是"查得到"，而是"**建好索引之后，一次读都不该枚举目录**"。
//
// 另外钉住这条设计的代价：写操作会让索引整份失效（下一次读重建一次）。
// 那是刻意的取舍（漏标 = 永久脏数据），这里把它写成事实，避免以后有人"顺手优化"成不失效。

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// countScans 把 readDirFn 换成计数器，返回计数器的取值与还原函数。
func countScans(t *testing.T) (*int64, func()) {
	t.Helper()
	orig := readDirFn
	var n int64
	readDirFn = func(dir string) ([]os.DirEntry, error) {
		atomic.AddInt64(&n, 1)
		return orig(dir)
	}
	return &n, func() { readDirFn = orig }
}

func scanFixture(t *testing.T, cards int) *Library {
	t.Helper()
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
	for i := 0; i < cards; i++ {
		name := "角色" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		if _, err := l.PutCard("characters", "char_"+name, name, "描述"+name, "PROMPT", img, "p1", "test"); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestReadPathsDoNotScanDisk(t *testing.T) {
	l := scanFixture(t, 30)
	// 先热身：索引第一次读由这里建起来（重建本身当然要枚举目录）
	l.Match("characters", "角色Aa")
	l.FindForCard("characters", "char_角色Aa", "角色Aa", "")

	n, restore := countScans(t)
	defer restore()

	// ---- 全部读入口：建好索引之后一次目录枚举都不该发生 ----
	l.Match("characters", "角色Aa")                        // 按名字
	l.MatchCard("characters", "char_角色Ab", "角色Ab")       // 按卡片 ID（资产阶段）
	l.FindForCard("characters", "char_角色Ac", "角色Ac", "") // 资产阶段主入口
	l.Find("characters", "角色Ad", "")
	l.FindForCard("characters", "char_角色Ae", "角色Ae", "描述") // 未命中也要走索引
	l.List()
	l.RelatedFiles("characters", "角色Aa")
	if got := atomic.LoadInt64(n); got != 0 {
		t.Fatalf("读路径扫了 %d 次目录 —— 说明有入口没走索引（真机实测漏掉的就是 matchByIDLocked 这一路）", got)
	}

	// ---- 写操作只更新**这一条**，不许让整张索引作废 ----
	//
	// 这是资产阶段的真实形态（每张卡：查库 → 入库/记复用），也是本仓实测踩过的第二个坑：
	// "写完就整份失效"看起来更保险，实际让交替读写的阶段**比不加索引还慢**
	// （600 条、一集 15 张卡：整份失效 648 ms/集 vs 每次全扫 195 ms/集）。
	// 判据因此有两半：写完再读**零次扫盘**，且**必须读到新值**（少了后一半就是永久脏数据）。
	before, _, ok := l.MatchCard("characters", "char_角色Ab", "角色Ab")
	if !ok {
		t.Fatal("热身时应该能查到条目")
	}
	l.BumpUsed("characters", "角色Ab")
	atomic.StoreInt64(n, 0)
	after, _, ok := l.MatchCard("characters", "char_角色Ab", "角色Ab")
	if !ok {
		t.Fatal("写完之后条目不该消失")
	}
	if after.UsedCount != before.UsedCount+1 {
		t.Fatalf("写完之后读到的是旧值（UsedCount %d → %d）—— 索引没跟着盘上的那一份更新",
			before.UsedCount, after.UsedCount)
	}
	if got := atomic.LoadInt64(n); got != 0 {
		t.Fatalf("写之后的读扫了 %d 次目录 —— 说明写操作把整张索引作废了（交替读写阶段会退化成每次全扫）", got)
	}
	// 删条目同样只动这一条：索引里不许留着已经不存在的目录
	if err := l.Delete("characters", "角色Ab"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.MatchCard("characters", "char_角色Ab", "角色Ab"); ok {
		t.Fatal("删掉的条目还查得到 —— 索引没跟着删")
	}
	if got := atomic.LoadInt64(n); got != 0 {
		t.Fatalf("删完之后的读扫了 %d 次目录", got)
	}
}
