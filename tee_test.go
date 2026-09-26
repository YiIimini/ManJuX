package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTeeWriterRotatesInRun 回归:日志应在**运行期**按大小归档,归档名带时间戳。
// 旧实现只在启动时判一次大小,把历史固定覆盖成单份 .old —— 长会话跑到中途
// 文件仍会无限增长,历史也只有一份。
func TestTeeWriterRotatesInRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.log")
	w, err := newTee(path, 64, nil) // 极低上限,便于触发
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	for i := 0; i < 40; i++ {
		if _, err := w.Write([]byte(strings.Repeat("x", 32) + "\n")); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}
	matches, _ := filepath.Glob(path + ".*")
	if len(matches) == 0 {
		t.Fatal("超过上限后应产生归档文件")
	}
	for _, m := range matches {
		if strings.HasSuffix(m, ".old") {
			t.Errorf("归档应为时间戳命名而非固定 .old: %s", m)
		}
	}
	// 轮转后当前文件必须仍可用
	if _, err := w.Write([]byte("tail\n")); err != nil {
		t.Fatalf("轮转后应能继续写: %v", err)
	}
}

// TestTeeWriterResetClearsInPlace 回归:一键清理里的"清空运行日志"必须是**原地截断**。
//
// 旧实现是在 server 侧 `os.Rename(manjux.log, manjux.log.1)` + 写一个空文件。写句柄跟着
// 文件走 —— 改名之后日志继续往那个已改名的文件里灌,新建的空 manjux.log 永远空着,界面上
// 看着"日志清空了",其实新日志全在孤儿文件里,直到下一次轮转才露馅。
// 这条钉住:清完归档全没、当前文件为 0 字节、**后续写入仍落在原路径**。
func TestTeeWriterResetClearsInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.log")
	w, err := newTee(path, 200, nil) // 上限小,便于攒出归档
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	for i := 0; i < 30; i++ {
		if _, err := w.Write([]byte(strings.Repeat("y", 40) + "\n")); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}
	archives, _ := filepath.Glob(path + ".*")
	if len(archives) == 0 {
		t.Fatal("前置条件:先得攒出归档,否则这条测不到归档清理")
	}

	freed, n, err := w.Reset()
	if err != nil {
		t.Fatalf("Reset 失败: %v", err)
	}
	if freed <= 0 {
		t.Errorf("应如实上报释放字节数,实际 %d", freed)
	}
	if n != len(archives) {
		t.Errorf("应删掉全部 %d 份归档,实际 %d", len(archives), n)
	}
	if left, _ := filepath.Glob(path + ".*"); len(left) != 0 {
		t.Errorf("归档没清干净:%v", left)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("当前日志文件必须还在(截断而非删除): %v", err)
	}
	if st.Size() != 0 {
		t.Errorf("当前日志应被截断到 0,实际 %d 字节", st.Size())
	}

	// 关键:清完还能继续写,而且**写的是原来那个路径**(而不是某个已改名的孤儿)
	if _, err := w.Write([]byte("after-reset\n")); err != nil {
		t.Fatalf("Reset 后写入失败: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回日志失败: %v", err)
	}
	if string(b) != "after-reset\n" {
		t.Errorf("Reset 后的写入必须落在 %s 上,实际内容 %q", filepath.Base(path), string(b))
	}
}

// TestTeeWriterPruneKeepsRecent 归档只保留最近 keep 份,不无限堆积。
func TestTeeWriterPruneKeepsRecent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.log")
	w, err := newTee(path, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	w.keep = 3

	for i := 0; i < 200; i++ {
		if _, err := w.Write([]byte("0123456789\n")); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}
	matches, _ := filepath.Glob(path + ".*")
	if len(matches) > w.keep {
		t.Errorf("归档应只保留 %d 份,实际 %d: %v", w.keep, len(matches), matches)
	}
	if len(matches) == 0 {
		t.Error("应至少产生一份归档")
	}
}
