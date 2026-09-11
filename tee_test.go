package main

import (
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
