package project

import (
	"strings"
	"unicode/utf8"
)

// style_medium.go —— 风格串里「介质族」的判据（**唯一一份**）。
//
// 【为什么要有它】2026-10-10 真机：`plan.style` 被剧本会话自编成
// 「中式梦幻磅礴的半写实绘画风，**3D CG 质感**：…」——把"半写实绘画"与"3D CG"两个互斥介质并列，
// 而本书风格卡写的是 `a semi-realistic hand-painted finish … **NOT a plastic 3D render**`。
// 平台随后按 `3d cg` 展开成 "next-generation 3D CG **character render, realistic skin shading**"，
// 出图就成了塑料娃娃（平台自己的 VLM 读图原话：「皮肤蜡感、磨皮重」「像 3D 塑料娃娃」）。
//
// 此前这条判据只活在 `pipeline.StyleMediumConflicts`，而且**只吃用户勾的预设 key**
// —— LLM 自编的那一句从来没人判。现在把词表与判定收进本包（`ValidatePlan` 拿得到），
// pipeline 的预设版改为委托本包，**同一句话不判两遍**。
//
// 【最容易踩的那一脚】本书自己的风格串里**明写着** `NOT a plastic 3D render`：
// 那是**否定句**（风格卡还专门解释了 qwen2.1 是 cfg=1.0、负面词无效，所以禁区必须写成
// "是 X，不是 Y" 的正向排除句）。把否定里的 "3D render" 当成"声明了 3D 介质"就会**误报**——
// 所以判定带**否定窗口**，并且有专门一条门用真机那句原文盯着（`style_medium_test.go`）。

// styleMediumWords 介质词表：**只收"明确写了介质"的词**。
//
// 刻意保守：题材/调子类（武侠、赛博、黑色电影、悬疑…）不收 —— 它们不声明介质，
// 收进来只会误报（与 pipeline 预设表里"题材类留空 Medium"同一条纪律）。
var styleMediumWords = []struct {
	Family string
	Words  []string
}{
	{"live", []string{"live-action", "live action", "photorealistic", "photoreal", "real photography",
		"真人实拍", "实拍", "真人"}},
	{"2d", []string{"2d-animated", "2d animated", "2d animation", "cel-shading", "cel shading",
		"anime", "donghua", "manga", "cartoon", "国漫", "二维", "赛璐璐", "动漫", "卡通"}},
	{"3d", []string{"3d cg", "3dcg", "3d render", "3d-render", "3d 渲染", "cgi",
		"三维", "皮克斯", "pixar", "unreal"}},
	{"painted", []string{"hand-painted", "hand painted", "painterly", "brushwork", "watercolour",
		"watercolor", "ink-wash", "ink wash", "水墨", "手绘", "绘画", "工笔", "国画"}},
	{"stopmotion", []string{"stop-motion", "stop motion", "claymation", "定格"}},
}

// mediumNegations 否定窗口里出现这些词 ⇒ 这一处**不算声明介质**（真机风格串就是这么写禁区的）。
var mediumNegations = []string{"not ", "not,", "not.", "no ", "never", "without", "avoid",
	"不是", "不许", "不准", "不要", "禁止", "避免", "非"}

// styleMediumNegationWindow 往前看多少个字符判否定（"NOT a plastic 3D render" 里
// 否定词离介质词 13 个字符；留一倍余量）。
const styleMediumNegationWindow = 28

// MediumFamilyLabel 介质族的中文说法（**唯一一份**：播报、界面、判据文案都用它）。
func MediumFamilyLabel(m string) string {
	switch m {
	case "live":
		return "真人实拍"
	case "2d":
		return "二维动画"
	case "3d":
		return "三维 CG"
	case "painted":
		return "手绘/水墨"
	case "stopmotion":
		return "定格动画"
	}
	return m
}

// StyleMediumFamilies 这句风格串里**声明过的**介质族（按出现顺序、已去重；无声明返回 nil）。
func StyleMediumFamilies(style string) []string {
	if strings.TrimSpace(style) == "" {
		return nil
	}
	low := strings.ToLower(style)
	var out []string
	seen := map[string]bool{}
	for _, g := range styleMediumWords {
		if seen[g.Family] {
			continue
		}
		for _, w := range g.Words {
			i := strings.Index(low, w)
			if i < 0 || negatedAt(low, i) {
				continue
			}
			seen[g.Family] = true
			out = append(out, g.Family)
			break
		}
	}
	return out
}

// negatedAt 位置 i 处的介质词是不是落在**否定窗口**里（"NOT a plastic 3D render" ⇒ true）。
func negatedAt(low string, i int) bool {
	start := i - styleMediumNegationWindow
	if start < 0 {
		start = 0
	}
	win := low[start:i]
	for _, n := range mediumNegations {
		if strings.Contains(win, n) {
			return true
		}
	}
	return false
}

// StyleMediumConflictNote 风格串里介质互相矛盾时的点名说明（空串 = 不矛盾，或者一句介质都没声明）。
//
// 返回的文案**可直接播报**：说清是哪几样、后果是什么、平台会怎么做。
func StyleMediumConflictNote(style string) string {
	fams := StyleMediumFamilies(style)
	if len(fams) < 2 {
		return ""
	}
	names := make([]string, 0, len(fams))
	for _, f := range fams {
		names = append(names, MediumFamilyLabel(f))
	}
	return strings.Join(names, " + ")
}

// styleMediumConflictAdvice 矛盾时的"平台会怎么做"（与用户勾预设那条提示同一口径：
// **照你写的做**，平台只负责当场说清）。单独成句是为了两处播报不各写一套。
const styleMediumConflictAdvice = "一个画面不可能同时是这几样，画面会被拉成四不像（如塑料 CG 脸）。" +
	"平台仍按这个风格渲染，建议只留一项后重跑「剧本分镜」。"

// StyleMediumConflictAdvice 供外部（管线/界面）复用同一句建议文案。
func StyleMediumConflictAdvice() string { return styleMediumConflictAdvice }

// RuneLenHead 取前 n 个字符（风格串在播报里只截一段；放在本包免得各调用方各写一份）。
func RuneLenHead(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
