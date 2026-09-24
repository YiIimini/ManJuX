package server

import (
	"os"
	"path/filepath"
	"strings"
)

// ComfyUI 目录布局的**唯一解析点**。
//
// 起因(2026-09-24 用户:「一键清理也要清掉 \ComfyUI\shared\models\conditioning\」):
// 清理代码一直按 `<root>/ComfyUI/models/<子目录>` 拼路径,而本机的模型实际放在
// `<root>/ComfyUI/shared/models/` 下(extra_model_paths.yaml 把 base_path 指到
// `D:/Ai/ComfyUI/shared/`)—— 于是"条件缓存数据文件"这一项**清的是另一个目录**:
// 真正堆着 838 个文件 / 17.4GB 的那份一直没被碰到,而按钮报告"清理完成"。
//
// 为什么不写死一个正确的路径:布局是**部署方式**决定的,同一台机器上都可能有
// 两份(标准布局 + shared 布局,各有一批残留)。所以这里列出所有候选、只返回
// **实际存在**的那些,清理侧对每一份都执行 —— 既不漏,也不误报"清过了"。

// comfyModelDirs 返回某个模型子目录在所有已知布局下的实际位置(仅存在的)。
//
// 候选顺序即优先级:
//  1. <root>/ComfyUI/models/<sub>           标准布局
//  2. <root>/ComfyUI/shared/models/<sub>    shared 布局(ComfyUI 在 root/ComfyUI 下时)
//  3. <root>/shared/models/<sub>            root 直接指向 shared(本机即此形态之一的邻居)
//  4. <root>/models/<sub>                   root 就是 ComfyUI 本体
//  5. extra_model_paths.yaml 里显式映射到该子目录的路径(以及各段的 base_path <base>/models/<sub>)
func comfyModelDirs(rootDir, sub string) []string {
	if strings.TrimSpace(rootDir) == "" {
		return nil
	}
	root := filepath.Clean(rootDir)
	cands := []string{
		filepath.Join(root, "ComfyUI", "models", sub),
		filepath.Join(root, "ComfyUI", "shared", "models", sub),
		filepath.Join(root, "shared", "models", sub),
		filepath.Join(root, "models", sub),
	}
	for _, sec := range extraModelPathSections(root) {
		if p, ok := sec.mapping[sub]; ok {
			cands = append(cands, p)
		}
		cands = append(cands, filepath.Join(sec.base, "models", sub))
	}
	return existingDirs(cands)
}

// comfyRunDirs 返回 ComfyUI 运行期目录(output / input / logs 等)的实际位置。
// 这些目录跟着 ComfyUI 本体走,但 root 也可能直接指向本体,故一并尝试。
func comfyRunDirs(rootDir, name string) []string {
	if strings.TrimSpace(rootDir) == "" {
		return nil
	}
	root := filepath.Clean(rootDir)
	return existingDirs([]string{
		filepath.Join(root, "ComfyUI", name),
		filepath.Join(root, name),
	})
}

// existingDirs 去重并只保留真实存在的目录。
func existingDirs(cands []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		if c == "" {
			continue
		}
		clean := filepath.Clean(c)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		if st, err := os.Stat(clean); err == nil && st.IsDir() {
			out = append(out, clean)
		}
	}
	return out
}

// extraPathSection extra_model_paths.yaml 的一个段:一个 base_path + 若干
// "目录类型 → 相对路径"的映射。
type extraPathSection struct {
	base    string
	mapping map[string]string // key = 目录类型(如 conditioning / loras)
}

// extraModelPathSections 解析 ComfyUI 的 extra_model_paths.yaml(存在才解析)。
//
// 为什么要读它而不是硬拼路径:这个文件是**部署方**表达"模型放在哪"的地方;
// 用户把模型搬到别处时改的正是它。不读它,清理器就只能猜到几种常见布局。
// 解析刻意宽松:只认 `key: value` 与 `base_path`,注释与空行跳过;解析失败
// 一律返回已解析到的部分(清理不该因为一个配置文件格式怪就整体失效)。
func extraModelPathSections(rootDir string) []extraPathSection {
	var files []string
	for _, p := range []string{
		filepath.Join(rootDir, "ComfyUI", "extra_model_paths.yaml"),
		filepath.Join(rootDir, "extra_model_paths.yaml"),
	} {
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	var out []extraPathSection
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var cur *extraPathSection
		for _, ln := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(ln)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indented := ln != strings.TrimLeft(ln, " \t")
			key, val, ok := splitYAMLKV(trimmed)
			if !ok {
				continue
			}
			if !indented { // 段头
				if cur != nil {
					out = append(out, *cur)
				}
				cur = &extraPathSection{mapping: map[string]string{}}
				continue
			}
			if cur == nil {
				continue
			}
			if key == "base_path" || key == "basePath" {
				cur.base = resolvePath(rootDir, val)
				continue
			}
			cur.mapping[key] = resolvePath(cur.base, val)
		}
		if cur != nil {
			out = append(out, *cur)
		}
	}
	// 丢掉没有 base_path 的段(无法定位)
	kept := out[:0]
	for _, sec := range out {
		if sec.base != "" {
			kept = append(kept, sec)
		}
	}
	return kept
}

// splitYAMLKV 拆 `key: value`(值为空的也算)。
func splitYAMLKV(s string) (key, val string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	val = strings.TrimSpace(s[i+1:])
	val = strings.Trim(val, `"'`)
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

// resolvePath 相对路径按 base 展开(ComfyUI 的 yaml 允许相对写法)。
func resolvePath(base, p string) string {
	if p == "" {
		return ""
	}
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if base == "" {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}
