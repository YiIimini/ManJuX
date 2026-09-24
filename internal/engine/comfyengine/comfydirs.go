package comfyengine

import (
	"os"
	"path/filepath"
	"strings"
)

// ComfyUI 目录布局的**唯一解析点**。
//
// 为什么必须收口:同一件"ComfyUI 把模型放在哪"的知识此前散在三处,而三处各不相同 ——
//   - 清理逻辑拼 `<root>/ComfyUI/models/...`(漏了 shared 布局 → 17.4GB 条件缓存清不掉);
//   - `modelFileExists` 猜 `<root>/ComfyUI/models` 与 `<root>/shared/models`
//     (同样漏了 `ComfyUI/shared/models`,于是 express 档的 int8 VAE 与 quality 档的
//     Acc-8Step 在本机被判定为"不存在",静默降级);
//   - 管理页的模型核对走 ComfyUI 自己的 combo(唯一正确的一处)。
//
// 现在实现只在这里,server 侧与引擎内部都调它。

// ModelDirs 某个模型子目录在所有已知布局下的**实际存在**位置。
//
// 候选:标准布局、ComfyUI/shared 布局、root 直接挂 shared、root 就是本体,
// 外加 extra_model_paths.yaml 里显式映射的路径与各段 base_path 下的默认模型目录。
// 读 yaml 而不是只硬拼路径,是因为"模型放在哪"正是部署方在该文件里表达的东西。
func ModelDirs(rootDir, sub string) []string {
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

// RunDirs ComfyUI 运行期目录(output / input / logs 等)的实际位置。
func RunDirs(rootDir, name string) []string {
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

// extraPathSection extra_model_paths.yaml 的一个段。
type extraPathSection struct {
	base    string
	mapping map[string]string
}

// extraModelPathSections 解析 extra_model_paths.yaml(存在才解析)。
// 解析刻意宽松:只认 `key: value` 与 `base_path`,注释与空行跳过;坏格式不该让
// 清理/体检整体失效。
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
	kept := out[:0]
	for _, sec := range out {
		if sec.base != "" {
			kept = append(kept, sec)
		}
	}
	return kept
}

func splitYAMLKV(s string) (key, val string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	val = strings.Trim(strings.TrimSpace(s[i+1:]), `"'`)
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

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
