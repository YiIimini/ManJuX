// Package fsutil 提供跨包共用的文件系统工具。
package fsutil

import (
	"os"
	"path/filepath"
	"sync"
)

// WriteFileAtomic 原子写文件:先写唯一临时文件,再 rename 覆盖目标。
//
// 直接用 os.WriteFile 覆盖时,若写入中途崩溃或断电,目标文件会留下半截内容;
// 而 rename 在同一文件系统内是原子的 —— 读方要么看到完整旧内容、要么看到完整
// 新内容,绝不会读到损坏的中间态。会自动创建父目录。
//
// 项目元数据、方案、清单与跨项目资产库都依赖这个保证:资产库条目一旦写坏,
// Match() 会把解析失败当成「未命中」静默跳过,表现为角色形象莫名其妙漂移,
// 且界面上完全看不到这条坏数据 —— 属于最难排查的一类故障。
//
// 【2026-10-08 深度排查：两处都要修】
//
//	① 临时文件名必须**唯一**（原来是固定 `<path>.tmp`）：同一个 plan.json 有两个写者时
//	   （用户在工作台改镜头 ↔ 管线落盘）会互相顶掉同一份临时文件 —— 先 rename 成功、
//	   后 rename 拿到 ENOENT ⇒ **调用方看到"保存失败"，其实内容已经写进去了**。
//	② 同一路径的写者必须**串行**：Windows 上两个并发 rename 抢同一个目标会报
//	   "Access is denied"（并发替换目标时系统要独占它）。两个写者本来就在同一个进程里，
//	   用 per-path 互斥锁串起来最省事，也彻底消除这一类随机失败。
func WriteFileAtomic(path string, data []byte) error {
	mu := pathWriteLock(path)
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// 已经写进去了**（最坏的一种：报错却生效，用户按提示重试还会覆盖）。CreateTemp 生成唯一名
	// （同目录 ⇒ 仍是同一文件系统，rename 依旧原子）。
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return err
	}
	// 权限与原来的 0644 对齐（CreateTemp 默认 0600）。
	if err := f.Chmod(0o644); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// pathWriteLocks 同一路径的写者串行（见 WriteFileAtomic 的说明②）。
// 进程内两个写者（用户改镜头 ↔ 管线落盘）抢同一个目标时，Windows 上的并发 rename
// 会报 "Access is denied" —— 串行化彻底消除这一类随机失败。
var pathWriteLocks sync.Map // 清理后的路径 → *sync.Mutex

func pathWriteLock(path string) *sync.Mutex {
	key := filepath.Clean(path)
	if v, ok := pathWriteLocks.Load(key); ok {
		return v.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := pathWriteLocks.LoadOrStore(key, mu)
	return actual.(*sync.Mutex)
}
