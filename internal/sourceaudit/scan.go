// Package sourceaudit 提供源码扫描原语,供**契约测试**共用。
//
// 为什么它不是 _test.go:同一个扫描逻辑要在多个包里用(config 的设置字段闸门、
// project 的阶段参数字段闸门),而 Go 的测试辅助函数不能跨包导入 —— 各写一份
// 必然漂移,而"闸门之间口径不一"本身就是审计要消灭的那类缺陷。
// 生产代码不应引用本包(它只读文件、不改变行为)。
package sourceaudit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Sources 读入仓库里全部**非测试** Go 源码(键 = 相对 root 的斜杠路径)。
func Sources(root string) map[string]string {
	out := map[string]string{}
	dirs := []string{filepath.Join(root, "internal"), root}
	for _, base := range dirs {
		_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				switch info.Name() {
				case "node_modules", "third_party", "webdist", "build", ".git", "data":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			out[filepath.ToSlash(rel)] = string(raw)
			return nil
		})
	}
	return out
}

// FieldIsRead 字段是否在源码里以 `.字段名` 的形式被读到(注释行不算)。
//
// 注意边界:这只证明"有人读过这个字段",**不证明读它的地方就是对的地方** ——
// 例如只在校验里被读、却从不影响行为(审计 P1-1 的 assemble.peak 就是这样)。
// 那类"读了但没接到底"要靠针对性测试补,不能指望这个扫描。
func FieldIsRead(files map[string]string, field string) bool {
	re := regexp.MustCompile(`\.` + regexp.QuoteMeta(field) + `\b`)
	for _, src := range files {
		for _, loc := range re.FindAllStringIndex(src, -1) {
			lineStart := strings.LastIndexByte(src[:loc[0]], '\n') + 1
			line := strings.TrimSpace(src[lineStart:])
			if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "*") {
				continue // 注释里提到字段名不算读取
			}
			return true
		}
	}
	return false
}
