package library

import (
	"os"
	"path/filepath"
	"testing"
)

// 进程内索引的正确性（2026-09-29 性能审计 #1）。
//
// ## 为什么要专门测它
//
// 加索引是为了把"每卡 3~5 次全库扫描"（实测库 600 条时每集白烧 1.6~2.6 秒）压成一次。
// 但索引带来一类**新的**失败模式：**写完读不到自己刚写的**（写操作没标版本、或索引
// 只在首次构建）—— 而它的表现极其难查：界面上"刚重命名完，刷新又是旧名字"、
// "刚上传的图查不到"。
//
// 所以这个测试的正事不是"索引命中"，而是**每一个写操作之后立刻读**。
func TestIndexSeesOwnWrites(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	img := func(name string) string {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// ① 首次写入 → 立刻按名字能查到（索引首次构建）
	if _, err := l.Put("characters", "沈砚", "清瘦长脸", "PROMPT", img("a.png"), "p1", "krea2"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.Match("characters", "沈砚"); !ok {
		t.Fatal("刚写入的条目必须立刻能查到（索引没失效就是这种症状）")
	}

	// ② SetMeta 改描述与提示词 → 立刻读到**新值**
	// （它不改名 —— 名字是 `locateByNameLocked` 的键，改名是另一条路）
	if _, err := l.SetMeta("characters", "沈砚", "改了描述", "PROMPT2"); err != nil {
		t.Fatal(err)
	}
	e0, _, ok := l.Match("characters", "沈砚")
	if !ok {
		t.Fatal("SetMeta 之后条目必须还在")
	}
	if e0.Description != "改了描述" {
		t.Errorf("SetMeta 之后立刻读到的描述必须是新的，实际 %q（索引没失效就会读到旧值）", e0.Description)
	}
	if e0.ImagePrompt != "PROMPT2" {
		t.Errorf("提示词也没跟上，实际 %q", e0.ImagePrompt)
	}

	// ③ 追加一条 → 立刻能查到（两次写入之间索引确实失效了）
	if _, err := l.Put("characters", "守钟女", "白发", "P2", img("b.png"), "p1", "krea2"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.Match("characters", "守钟女"); !ok {
		t.Error("第二条写入之后必须立刻能查到")
	}

	// ④ 复用计数（BumpUsed）走的是另一条写路径 —— 它改了 asset.json，索引也该失效
	l.BumpUsed("characters", "守钟女")
	e, _, ok := l.Match("characters", "守钟女")
	if !ok {
		t.Fatal("BumpUsed 之后条目必须还在")
	}
	if e.UsedCount == 0 {
		t.Error("BumpUsed 之后立刻读到的 UsedCount 必须是新的（索引没失效就会读到旧值）")
	}

	// ⑤ 删除 → 立刻查不到
	if err := l.Delete("characters", "守钟女"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.Match("characters", "守钟女"); ok {
		t.Error("删除之后必须立刻查不到 —— 否则界面上是「删了刷新又回来」")
	}

	// ⑥ List 走同一份索引，数量必须与逐条查询一致
	list := l.List()
	if n := len(list["characters"]); n != 1 {
		t.Errorf("删剩 1 条，List 该给 1 条，实际 %d（List 与 Match 必须看同一份索引）", n)
	}

	// ⑦ 索引跨 kind 隔离：往 scenes 写不该让 characters 的条目丢失或串味
	if _, err := l.Put("scenes", "破庙", "残破", "P3", img("c.png"), "p1", "krea2"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.Match("characters", "沈砚"); !ok {
		t.Error("写另一个 kind 之后，原来那个 kind 的条目必须还在")
	}
	if _, _, ok := l.Match("scenes", "破庙"); !ok {
		t.Error("新 kind 的条目必须查得到")
	}
}
