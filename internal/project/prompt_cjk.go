package project

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// prompt_cjk.go 提示词「汉字卫生」的唯一判据（技能侧 ↔ 平台侧同源口径）。
//
// ## 为什么有这一条（2026-09-26 用户报障「成片出现名字都没有的字幕」）
//
// H3 是**音视频同一次生成**，官方 ON-SCREEN TEXT 规则的判据很直白：
// **没被英文引号括起来的文字，就是模型会画成字形的来源**。依据与 prompts.go 同一份
// 官方指南（本机实测取到正文）：
//
//	https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/base-en.txt
//
// 而汉字合法的位置只有两处：
//  1. `<d>[Chinese]…</d>` —— 官方指定给「说」的通道（它同时供 H3 出原生中文配音，删不得）；
//  2. **主体名** —— 官方要求「用文字点名主体」（渲染期 PICTURE MANIFEST 靠它对编号）。
//
// 于是判据只剩一条：**`<d>` 块外的汉字 = 往画面上写字**。实测（《翻篇》第 01 集）是
// `<d>` 外 7083 字 vs `<d>` 内 350 字 —— 根因不是台词，是**场景卡的中文美术卡整段被
// 拼进了 subject_definitions**。
//
// ## 两侧同源（一张规则表，两侧各跑一遍）
//
// 技能侧 `manju-novel/scripts/manju_plan.py`：`CJK_RE = [\u4e00-\u9fff]`（计数）、
// `sanitize_prompt()`（产出前清洗）、`cjk_outside_d()`（产出前自检）；
// 平台侧就是本文件（导入时守门，见 plan_contract.go 的 validatePlan）。
// **阈值也同源**：`<d>` 外可避免的汉字 >60 → warn、>200 → fail。
//
// 判据若两侧走散，就会出现「技能侧报干净、平台侧报脏」这种没人判得清对错的争吵 ——
// 所以字符类与阈值都必须逐字对齐，谁都不许单侧「顺手放宽」。
//
// ## 主体名必须扣掉
//
// 官方要求点名主体，而 9 个角色同镜时名字本身就近百字，且提示词里一个名字会被反复提到
// （subject_definitions / retention_analysis / detailed_description）—— 那是**不可避免**
// 的汉字。不扣会天天误报，真出问题时反而被淹掉（误拦的规则比没有规则更糟）。
// 扣减集合 = characters + scenes + props 的 name，名字出现在哪就扣到哪。

// 汉字卫生的阈值。**与技能侧 manju_plan.py 同源，单侧改就是双侧走散**。
const (
	// PlanCJKWarnOutsideDialogue `<d>` 外可避免的汉字超过这个数 → warn。
	PlanCJKWarnOutsideDialogue = 60
	// PlanCJKFailOutsideDialogue `<d>` 外可避免的汉字超过这个数 → fail（拒收）。
	//
	// 为什么 fail 线定在 200 而不是 60：这个门要拦的是「中文美术卡/散文整段灌进提示词」
	// （实测单集 7083 字、单镜最高 90 字，见 testdata 里的技能侧真产物），不是零星几个字。
	// 60–200 之间只提示：那是「有几个字混进来了」的量级，拦住它会把正常的包也拒掉。
	PlanCJKFailOutsideDialogue = 200
)

// HanRune 汉字（CJK 统一表意基本区 U+4E00–U+9FFF）。
//
// 口径**逐字对齐技能侧** `CJK_RE = [\u4e00-\u9fff]`：扩展区/兼容区、中文标点（，。「」）
// 与全角符号都不算。两侧一个宽一个严，计数就对不上，门也就失去意义。
func HanRune(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }

// CountCJK 一段文本里的汉字个数（不做任何保护）。
//
// 只给「出现任何汉字都要提醒」的场景用（如 keyframe_prompt：它是 T2I 静帧提示词，
// 图生图模型画中文几乎必然成乱码，没有 `<d>` 这条合法通道）。判断提示词卫生请用
// CJKOutsideDialogue。
func CountCJK(text string) int {
	n := 0
	for _, r := range text {
		if HanRune(r) {
			n++
		}
	}
	return n
}

// reDialogueBlock 官方「说」的通道：`<d>…</d>`（跨行、非贪婪）。
var reDialogueBlock = regexp.MustCompile(`(?s)<d>.*?</d>`)

// reSentenceSplit 句末标点后的空白（用来把一段提示词切成句子，便于整段丢弃含中文的句子）。
var reSentenceSplit = regexp.MustCompile(`([.。!！?？])\s+`)

// reCJKPunct 中文/全角标点（丢掉汉字之后它们会变成孤立残渣）。
//
// 只收**中文与全角**那两类（U+3000–303F 标点符号、U+FF00–FFEF 全角形式），
// 不收 U+2014 破折号 / U+201C 花引号 / U+2026 省略号 —— 那些在英文正文里是**正常标点**，
// 一并删掉会把 "EXACTLY — the same layout" 改成 "EXACTLY the same layout"（实测踩过）。
var reCJKPunct = regexp.MustCompile(`[\x{3000}-\x{303f}\x{ff00}-\x{ffef}]`)

// reSpaceRun 连续空白。
var reSpaceRun = regexp.MustCompile(`\s+`)

// reLeadingJunk 结果开头的孤立标点/空白（整段中文被丢弃后会留下 " . ..." 这类残渣）。
var reLeadingJunk = regexp.MustCompile(`^[\s.,;:、，。；：\-—)]+`)

// reEmptyParens 删空括号（逐字删汉字后会留下 `(: )`、`( )` 这类残渣）。
var reEmptyParens = regexp.MustCompile(`[（(]\s*[:：]?\s*[）)]`)

// reCNMirror 平台提示词里的**中英对照块**：`(中文对照:……)`（半角/全角括号与冒号都认）。
//
// 【两处必须注意】
//  1. **对照块自己带括号**：正文里就有 `(旧木/纸/丝/锦/铜/漆/陶/湿石)` 这种内层括号，所以
//     不能写成"遇到第一个右括号就收"—— 那样只删掉一半，剩下的残渣（实测 `, ; **** ****, )`）
//     既没信息、又白占注意力，还会**破坏幂等**（残渣里空格数每次压缩结果不同，
//     `TestPortraitPromptIsIdempotent` 就是被这一个空格咬红的）。用 `[^\n]*[)）]` 贪婪取到
//     **行内最后一个**右括号。
//  2. **不跨行**（`[^\n]`）：对照块永远收在它那一行末尾，跨行会连下一段的英文一起吃进去。
//
// 【为什么整块删，而不是删掉里面的汉字】逐字删汉字同样会留符号垃圾，理由同上。
var reCNMirror = regexp.MustCompile(`[（(]\s*中文对照\s*[:：][^\n]*[)）]`)

// DropHanRunes 清掉"中英对照"，**保留其余全部字符**（只摘中文，英文一个字不动）。
//
// 【与 StripCJK 的分界 —— 2026-09-28 实测出来的】
//   - `StripCJK` 是**按句丢弃**：一句里只要含汉字，整句都不留。它服务的是"卡原文里混进来的
//     中文注释"—— 那种句子的英文部分通常也没有保留价值。
//   - 本函数服务**平台追加的块**：它们一律写成 `英文句子(中文对照:……)`。
//
// 用错哪一个的后果是实打实的：中英对照里中文句号后**没有空格**、不构成句边界，用 `StripCJK`
// 清会把**整段**（连同英文）一起丢掉 —— 定妆照提示词里的风格块、发型纪律、姿态纪律全部归零
// （`portrait_assembly_test.go`「风格块应恰好 1 份,实际 0」就是这么红的）。
func DropHanRunes(text string) string {
	if CountCJK(text) == 0 {
		return text
	}
	// ① 中英对照**整块删**（写给维护者的，一个字都不该进画面提示词）
	out := reCNMirror.ReplaceAllString(text, " ")
	// ② 剩下还带汉字的（卡原文里混着的中文注释）逐字删
	if CountCJK(out) > 0 {
		out = removeHanRunes(out)
	}
	out = reCJKPunct.ReplaceAllString(out, " ")
	out = reEmptyParens.ReplaceAllString(out, " ")
	out = reSpaceRun.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

// StripCJK 把一段**画面提示词**里的汉字清干净，供图像模型直接使用。
//
// ## 为什么需要它（而不是只靠契约门）
//
// 2026-09-27 技能侧整改单 §1.4 / 平台侧整改清单 §6.2：场景板是 T2I 出的环境参考图，
// 而它会作为 H3 的参考图被带进**用到该场景的每一镜**。中文名开头（实测
// `临水城（口径锁定正名表里的地点）. Photorealistic…` 12 张卡合计 178 字）会被画成
// 乱码字形糊在环境板上 —— 契约门能提示，但门是"拒收/提示"语义，存量包、人工改过的卡、
// 以及任何绕过门的路径都还在。**渲染输入必须干净**这件事只能由装配点保证。
//
// ## 口径
//
// 优先**整句丢弃**：按句末标点切句，含汉字的句子整句删掉（那通常是"中文名（中文注解）"
// 这一段，删掉后英文正文原样保留）。整段都是中文时退回**逐字删除**并清理残渣，
// 清完什么都不剩就**返回空串** —— 调用方（场景图装配点）会照常追加取景与风格块，
// 交出去的是一个"不指定具体地点的空场"，这比留着汉字让模型画成乱码字形好得多
// （空提示词的风险由契约门提示，乱码字形是必然发生的画面污染）。
//
// 幂等：清干净的文本再清一次不变（装配点可能被调用两次：资产阶段 + 渲染前补齐）。
func StripCJK(text string) string {
	if CountCJK(text) == 0 {
		return text
	}
	// ① 整句丢弃
	parts := reSentenceSplit.Split(text, -1)
	seps := reSentenceSplit.FindAllString(text, -1)
	var kept []string
	for i, p := range parts {
		if CountCJK(p) > 0 {
			continue // 整句含汉字 → 丢
		}
		tail := ""
		if i < len(seps) {
			tail = seps[i] // 把句末标点接回去（Split 把它切掉了）
		}
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p+tail)
		}
	}
	out := strings.Join(kept, " ")
	if CountCJK(out) > 0 || strings.TrimSpace(out) == "" {
		// ② 退回逐字删除（整段中文的情形）
		out = removeHanRunes(text)
	}
	out = reCJKPunct.ReplaceAllString(out, " ")
	out = reSpaceRun.ReplaceAllString(out, " ")
	out = reLeadingJunk.ReplaceAllString(out, "")
	return strings.TrimSpace(out)
}

// removeHanRunes 逐字删除汉字（保留其余字符，交给调用方清理残渣）。
func removeHanRunes(text string) string {
	var sb strings.Builder
	sb.Grow(len(text))
	for _, r := range text {
		if HanRune(r) {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// maskProtectedCJK 把**受保护的汉字**按 rune 等长遮成空格，返回遮罩后的文本：
//
//   - `<d>…</d>` 整块（官方指定给「说」的通道，里面的汉字合法且必须逐字保留）；
//   - keepNames 里每个主体名的**每次出现**（官方要求点名主体，名字出现在哪就保护到哪；
//     同一个名字在一份提示词里会被写很多遍，全都要扣）。
//
// 为什么等长遮罩而不是直接删掉：① 调用方要数的是「剩下的汉字」，等长遮罩让计数不受
// 保护长度影响；② 需要按字符窗口取尾部（见 TailUnprotectedCJKRuns）时，遮罩后下标仍与
// 原文对齐，否则窗口会错位、`<d>` 可能被从中间切开而"漏保护"；③ 删空会把标签两侧的文字
// 粘成一个本不存在的连续串（「他说<d>x</d>道」→「他说道」）。
func maskProtectedCJK(text string, keepNames []string) string {
	out := reDialogueBlock.ReplaceAllStringFunc(text, func(m string) string {
		return strings.Repeat(" ", utf8.RuneCountInString(m))
	})
	for _, n := range keepNames {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		out = strings.ReplaceAll(out, n, strings.Repeat(" ", utf8.RuneCountInString(n)))
	}
	return out
}

// CJKOutsideDialogue `<d>` 块外、且不属于主体名的汉字个数。
//
// **与技能侧 manju_plan.py 的 cjk_outside_d() 同源**（同样的保护、同样的扣减集合）：
// 技能侧拿它做产出前自检，平台侧拿它做导入守门 —— 一张规则表，两侧各跑一遍。
func CJKOutsideDialogue(text string, keepNames []string) int {
	return CountCJK(maskProtectedCJK(text, keepNames))
}

// UnprotectedCJKRuns `<d>` 外、非主体名的**连续汉字串**。
//
// 与计数分开是因为用途不同：计数决定 fail/warn，串用来在日志里**指出具体是哪一段** ——
// 「有 7083 个汉字」没人知道该删哪里，「『主色/结构/记忆点/光线』这 12 个字在
// subject_definitions 里」才改得动。
func UnprotectedCJKRuns(text string, keepNames []string) []string {
	return cjkRuns(maskProtectedCJK(text, keepNames))
}

// TailUnprotectedCJKRuns 文本**末尾 window 个字符**（rune）内的无防护汉字串。
//
// 用途见 pipeline/refmanifest.go 的收尾条款：PICTURE MANIFEST 是提示词的最后一块，
// 而它每条清单行都写中文名（`<Picture 1> = 翻篇`）—— 收尾之后不能再有任何"能变成字"
// 的汉字，否则前面整段的清洗都被最后一段抵消。
//
// 先遮罩再取窗口（遮罩等长，所以窗口仍对齐原文）：反过来先切窗口会把 `<d>` 从中间切开，
// 里面的台词就被误判成无防护汉字。
func TailUnprotectedCJKRuns(text string, window int, keepNames []string) []string {
	masked := []rune(maskProtectedCJK(text, keepNames))
	if window > 0 && len(masked) > window {
		masked = masked[len(masked)-window:]
	}
	return cjkRuns(string(masked))
}

// cjkRuns 抽出全部极长的连续汉字串（按 rune，遇非汉字即断开）。
func cjkRuns(text string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	for _, r := range text {
		if HanRune(r) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}
