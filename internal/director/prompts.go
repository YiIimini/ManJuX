package director

import (
	"embed"
	"strings"
)

// prompts.go 提示词的**唯一载体**：`prompts/*.md`（go:embed）。
//
// 【为什么不在 Go 里写 raw string】（2026-09-28 根因治理）
// 提示词原本是 `const sysXxx = \`...\“ 的 raw string —— Go 的 raw string 用反引号定界，
// 于是**正文里出现任何一个反引号**（比如写 `[Shot 1]`、写文件路径）都会截断字符串、编译报错。
// 那天为此栽了**七次**，每一次都得中断思路去修一句本可以自由写的文字。
//
// 搬进 Markdown 之后：反引号自由、提示词可以像文章一样读与 review（diff 也干净得多），
// 而且"改提示词"这件事不再需要懂 Go 语法。
//
// 【纪律】`prompts/` 下的文件**就是**提示词正文，一个字符都不许在代码里二次加工
// （拼接顺序在 director.go 里，正文本身原样进模型）。搬运用 tools/extractprompts（一次性工具）。
//
//go:embed prompts
var promptFS embed.FS

// promptText 取一段提示词正文（文件不存在时**大声失败**：提示词缺一段比编译不过更危险）。
func promptText(name string) string {
	b, err := promptFS.ReadFile("prompts/" + name + ".md")
	if err != nil {
		// 用 panic 而不是返回空串：空提示词会让模型凭感觉干活，那比崩溃难查一百倍。
		panic("提示词缺失：prompts/" + name + ".md —— " + err.Error())
	}
	return string(b)
}

// PromptNames 列出全部提示词段名（诊断/测试用）。
func PromptNames() []string {
	entries, err := promptFS.ReadDir("prompts")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	return out
}
