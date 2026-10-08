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

// open 打开(或创建)日志文件,并把文件指针移到末尾。
//
// **刻意不用 O_APPEND**:Windows 上以追加方式打开会拿到 FILE_APPEND_DATA 权限而**没有**
// FILE_WRITE_DATA,于是同一个句柄调 Truncate 会被系统拒掉(Access is denied)——
// 而"一键清理要能清空运行日志"恰恰依赖原地截断(见 Reset)。追加语义由 w.mu 保证:
// 这个文件只有本进程这一个写者(单实例锁),加锁后 seek-末尾+write 与 O_APPEND 等价。
func (w *teeWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
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

// Reset 一键清理用:**把最后一段留成归档**,然后清空当前日志文件(句柄复用,不删文件、不改名)。
//
// 为什么清日志必须由持有句柄的这里来做:「一键清理」原先在 server 侧直接
// `os.Rename(manjux.log, manjux.log.1)` 再写一个空文件 —— 而写句柄是**跟着文件走的**,
// 改名之后日志继续往那个已改名的文件里灌,磁盘上新建的空 manjux.log 永远空着,直到下一次
// 轮转才把这段错位暴露出来。同一类错误还有"直接删掉正在写的文件"(Windows 上要么删不掉,
// 要么删掉后写入进了一个谁也看不见的孤儿文件)。截断不会转移句柄,写完照旧回到同一个文件。
//
// 【2026-10-08 改:不再把现场一起清掉】原实现是"删掉**全部**归档 + 原地截断到 0"。
// 代价在真机上付过一次:查"某个项目怎么没了"时,唯一一条能指认现场的记录
// (`🗑 已删除项目「…」`,只写在这个日志里)连同 5 份归档一起被用户点的一次「一键清理」抹掉了 ——
// 于是那件事永远查不清。现在先**留尾**:把当前文件最后 keepTailBytes 字节落成一份带时间戳的归档,
// 再删旧归档、再截断。空间照样释放(10MB 的日志只留 ~256KB),但"刚刚发生了什么"永远查得到。
//
// 返回:释放的字节数(含旧归档)与保留下来的归档份数。
const keepTailBytes = 256 << 10

func (w *teeWriter) Reset() (int64, int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var freed int64
	keptCount := 0
	matches, _ := filepath.Glob(w.path + ".*")
	sort.Strings(matches) // 时间戳命名 ⇒ 字典序即时间序（与 pruneLocked 同一口径）
	// ① 先留尾（必须在截断之前）：把当前文件最后一段写成归档。
	kept := ""
	if w.f != nil && w.n > 0 {
		if name, err := w.keepTailLocked(); err == nil {
			kept = name
			keptCount = 1
		}
	}
	// ①′ 当前文件是空的（上一次写入正好触发了轮转，内容全在归档里）⇒ 不能把归档全删：
	// 那样"刚刚发生了什么"还是被抹掉 —— 而这一条正是这次改动的目的。留下**最新的一份归档**当尾巴。
	if kept == "" && len(matches) > 0 {
		kept = matches[len(matches)-1]
		keptCount = 1
	}
	// ② 再清旧归档（用户的意图是释放空间）；刚刚留的那一份**不能删**。
	for _, old := range matches {
		if kept != "" && old == kept {
			continue
		}
		if st, e := os.Stat(old); e == nil {
			freed += st.Size()
		}
		// 删掉旧归档：释放空间是用户点"清理"的本意。
		// 删失败（被占用等）不报错 —— 但**也不计入**"保留下来的份数"（那个数只数留在盘上的）。
		_ = os.Remove(old)
	}
	archives := keptCount
	if w.f == nil {
		return freed, archives, nil // 日志不可用(启动时就没打开成):归档清了就算清完
	}
	freed += w.n
	if err := w.f.Truncate(0); err != nil {
		return freed - w.n, archives, err
	}
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return freed - w.n, archives, err
	}
	w.n = 0
	return freed, archives, nil
}

// keepTailLocked 把当前日志文件最后 keepTailBytes 字节写成一份归档,返回归档路径。
// 调用方须持有 w.mu;失败返回错误(调用方据此如实说"这次没留成")。
func (w *teeWriter) keepTailLocked() (string, error) {
	if w.f == nil || w.n <= 0 {
		return "", nil
	}
	start := w.n - keepTailBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, w.n-start)
	if _, err := w.f.ReadAt(buf, start); err != nil {
		return "", err
	}
	name := w.path + "." + time.Now().Format("20060102-150405.000") + ".tail"
	if err := os.WriteFile(name, buf, 0o644); err != nil {
		return "", err
	}
	return name, nil
}
