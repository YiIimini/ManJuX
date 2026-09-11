package main

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// teeWriter 同时写日志文件与标准输出,并在文件超过 maxBytes 时于**运行期**
// 轮转归档。
//
// 此前只在启动时判一次大小,且归档固定写 `.old` —— 长会话跑到中途文件仍会
// 无限增长,而历史归档永远只有一份(下次启动就被覆盖)。现在按上限自动归档、
// 以时间戳命名并保留最近若干份;归档失败也只影响归档本身,不拖累主流程。
type teeWriter struct {
	path     string
	maxBytes int64
	console  io.Writer
	keep     int // 保留的归档份数

	mu sync.Mutex
	f  *os.File
	n  int64
}

// newTee 打开(或创建)日志文件。console 可为 nil(windowsgui 下 os.Stdout 无效)。
func newTee(path string, maxBytes int64, console io.Writer) (*teeWriter, error) {
	w := &teeWriter{path: path, maxBytes: maxBytes, console: console, keep: 5}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *teeWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f, w.n = f, st.Size()
	return nil
}

func (w *teeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.console != nil {
		// windowsgui 模式下 os.Stdout 可能无效,忽略其错误
		_, _ = w.console.Write(p)
	}
	if w.f == nil {
		return len(p), nil // 日志不可用绝不拖垮主流程
	}
	n, err := w.f.Write(p)
	w.n += int64(n)
	if w.n >= w.maxBytes {
		w.rotateLocked()
	}
	return n, err
}

// rotateLocked 归档当前文件并重开。调用方须持有 w.mu。
func (w *teeWriter) rotateLocked() {
	_ = w.f.Close()
	// 带毫秒:同一秒内连续轮转(极限测试/异常暴量)也不会互相覆盖
	archive := w.path + "." + time.Now().Format("20060102-150405.000")
	if err := os.Rename(w.path, archive); err != nil {
		// 归档失败(文件被占用等)就继续写原文件,不能在日志上再炸一次
		_ = w.open()
		return
	}
	if err := w.open(); err != nil {
		w.f = nil
		return
	}
	w.pruneLocked()
}

// pruneLocked 只保留最近 keep 份归档。时间戳命名使字典序等于时间序。
func (w *teeWriter) pruneLocked() {
	matches, err := filepath.Glob(w.path + ".*")
	if err != nil || len(matches) <= w.keep {
		return
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-w.keep] {
		_ = os.Remove(old)
	}
}

// Close 关闭日志文件。
func (w *teeWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
