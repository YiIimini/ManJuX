package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestWriteFileAtomicConcurrentWritersDoNotCollide 【2026-10-08 深度排查】
//
// 原实现写**固定名** `<path>.tmp`：两个写者同时写同一个文件（真机形态：用户在工作台改镜头
// ↔ 管线落盘同一个 plan.json）会互相顶掉同一份临时文件 —— 先 rename 成功、后 rename 拿到
// ENOENT ⇒ **调用方看到"保存失败"，其实内容已经写进去了**（最坏的一种：报错却生效）。
//
// 判据：并发写同一个路径，**每一个调用都必须成功**，且最终文件是其中一份完整内容
// （不是半截、也不是两份的混合）。
func TestWriteFileAtomicConcurrentWritersDoNotCollide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")
	payloads := make([]string, 8)
	for i := range payloads {
		// 每一份都够长，混合出来一定看得出来。
		payloads[i] = strings.Repeat(string(rune('A'+i)), 4096)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(payloads))
	start := make(chan struct{})
	for i := range payloads {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for n := 0; n < 20; n++ {
				if err := WriteFileAtomic(path, []byte(payloads[i])); err != nil {
					errs[i] = err
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个写者失败了：%v —— 固定名 .tmp 会互相顶掉", i, err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, p := range payloads {
		if got == p {
			// 必须**恰好**是其中一份（不能是两份拼接/半截）。
			if len([]rune(got)) != len([]rune(p)) {
				t.Fatalf("文件长度不对：%d ≠ %d", len(got), len(p))
			}
			return
		}
	}
	t.Fatalf("最终文件不是任何一份完整内容（前 32 字节：%q）", got[:min(32, len(got))])
}

// TestWriteFileAtomicLeavesNoTempFiles 成功之后不许在盘上留下临时文件（否则清理清单会越积越多）。
func TestWriteFileAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := WriteFileAtomic(path, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("盘上留着临时文件：%s", e.Name())
		}
	}
	if len(ents) != 1 {
		t.Errorf("目录里应当只有目标文件，实际 %d 个", len(ents))
	}
}
