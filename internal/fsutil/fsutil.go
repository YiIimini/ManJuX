// Package fsutil 提供跨包共用的文件系统工具。
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic 原子写文件:先写 <path>.tmp,再 rename 覆盖目标。
//
// 直接用 os.WriteFile 覆盖时,若写入中途崩溃或断电,目标文件会留下半截内容;
// 而 rename 在同一文件系统内是原子的 —— 读方要么看到完整旧内容、要么看到完整
// 新内容,绝不会读到损坏的中间态。会自动创建父目录。
//
// 项目元数据、方案、清单与跨项目资产库都依赖这个保证:资产库条目一旦写坏,
// Match() 会把解析失败当成「未命中」静默跳过,表现为角色形象莫名其妙漂移,
// 且界面上完全看不到这条坏数据 —— 属于最难排查的一类故障。
func WriteFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
