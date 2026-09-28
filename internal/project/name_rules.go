package project

import (
	"fmt"
	"strings"
)

// name_rules.go 章名／书名的**平台侧检查**（对齐技能侧 2026-09-28 的符号规定）。
//
// 【口径来源】`docs/技能侧通知-章名书名符号规定-2026-09-28.md`（技能侧 → 平台侧）：
//   - **只准全角标点**，禁一切 ASCII 标点与空格 —— 这条平台**硬拦**（`IsAllowedNameRune`，
//     导入时判到就拒收该集并请技能侧改名，**不静默替换**）；
//   - **章名 ≤ 14 字 ／ 书名 ≤ 20 字** —— 他们给平台留的余量（片头卡一屏一行）。
//     平台不烧片头卡（管线只有 script/assets/render/qc/assemble 五段），但**项目目录名**里嵌着章名，
//     太长会顶到 Windows 路径长度上限，所以平台按 **WARN** 报（他们通知里也允许分项做 WARN）。
//   - **保留名**（CON/PRN/AUX/NUL/COM1-9/LPT1-9）—— 章名即文件名，撞上会建不出目录 ⇒ 也算 WARN
//     （平台的项目目录名前面还有书名前缀，实际撞不上，但报出来免得哪天改结构踩到）。
//
// 为什么硬拦与 WARN 要分开：硬拦的判据是"**平台写不出这个文件/目录**"（确定性、必然失败），
// WARN 的判据是"**能写但不好**"（长度、保留名）。把后者也做成 fail 会让一堆能跑的集数被拒，
// 那是把口径的洁癖转嫁给用户。

// NameRuleProblem 一条可播报的名字规则问题（空切片 = 没问题）。
type NameRuleProblem struct {
	Kind string // 长度 / 保留名
	Msg  string
}

// ChapterNameProblems 章名（＝集名）的规则问题。硬拦项不在这里（`IsAllowedNameRune` 负责）。
func ChapterNameProblems(name string) []NameRuleProblem {
	return nameRuleProblems(name, 14, "章名")
}

// BookNameProblems 书名的规则问题。
func BookNameProblems(name string) []NameRuleProblem {
	return nameRuleProblems(name, 20, "书名")
}

func nameRuleProblems(name string, maxRunes int, what string) []NameRuleProblem {
	var out []NameRuleProblem
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return out // 空名由调用方兜底（平台用「未命名」），不算规则问题
	}
	if n := len([]rune(trimmed)); n > maxRunes {
		out = append(out, NameRuleProblem{
			Kind: "长度",
			Msg: fmt.Sprintf("%s %q 有 %d 字，超过技能侧规定的 %d 字上限"+
				"（技能侧按「片头卡一屏一行」留的余量；平台的目录名还嵌着书名前缀，太长会顶到路径长度上限）",
				what, trimmed, n, maxRunes),
		})
	}
	if isWindowsReservedName(trimmed) {
		out = append(out, NameRuleProblem{
			Kind: "保留名",
			Msg: fmt.Sprintf("%s %q 是 Windows 保留名（CON/PRN/AUX/NUL/COM1-9/LPT1-9）——"+
				"章名即文件名，撞上会建不出目录", what, trimmed),
		})
	}
	return out
}

// isWindowsReservedName 是否 Windows 保留设备名（大小写不敏感，带扩展名也算：`CON.md` 同样被拒）。
func isWindowsReservedName(name string) bool {
	base := strings.ToUpper(name)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	for _, p := range []string{"COM", "LPT"} {
		if len(base) == 4 && strings.HasPrefix(base, p) && base[3] >= '1' && base[3] <= '9' {
			return true
		}
	}
	return false
}
