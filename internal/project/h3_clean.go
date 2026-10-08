package project

import (
	"regexp"
	"strings"
)

// h3_clean.go 剔除混进 `h3_prompt` 的**"写给生成器的话"**。
//
// 背景（2026-09-27 晚，平台侧验收技能侧重出的脚本时实测）：
// 技能侧产出的 `plan.json` 里，**全书 2462 镜每一镜**的 `h3_prompt.subject_definitions`
// 段都以这样几行开头：
//
//	REFERENCE ORDER: for each character in the shot's **characters array**, in array order,
//	FIRST that character's front sheet, and THEN (if it has one) ONE multi-view picture …
//	Locate every subject by PICTURE CONTENT, never by counting indexes: …
//	Every <Subject> must map to a picture that exists in the order above; …
//
// 这不是提示词内容，而是**写给"拼提示词的人"的规则**：它点名了 JSON 字段
// （"the shot's characters array"）、点名了渲染器行为（"the renderer appends an authoritative
// PICTURE MANIFEST that fixes the numbering"）。三条独立证据说明它不该进正文：
//  ① 官方 `MiniMax-H3/skills/**` 里**一句都没有**（全仓搜 `REFERENCE ORDER` / `PICTURE MANIFEST` 为 0）；
//  ② 双方的共享契约 `docs/渲染脚本契约.md` 没有把它定义成 `h3_prompt` 的组成部分；
//  ③ 平台渲染时是把 `shot.H3Prompt` **原样**拼给模型的（只在前缀追加风格块/比例锚点/画外音条款），
//     连平台自己的 `PICTURE MANIFEST (authoritative…)` 也是渲染期另外追加的
//     —— 于是模型会同时收到"规则"与"权威清单"，多出来那段是纯噪声。
//
// 为什么由平台兜底（而不是只等技能侧改）：平台的 `sysH3Format` 是**规则与输出骨架混排**的模板，
// LLM 直出时同样可能把规则行照抄进正文 —— 这条守卫对两条来源一视同仁。
//
// 边界（**宁可漏、不可误删**）：只按**行首前缀**匹配下列几条，且这些前缀**不可能**出现在
// 真正的提示词内容里（真内容不会以 "REFERENCE ORDER:" 或 "- One line per reference picture"
// 开头）。像 "The scene is the LAST attached picture." 这种**既像规则又像内容**的句子**不动它**
// —— 它确实在告诉模型"场景是哪张图"。

// H3WriterDirectivePrefixes 生成器/作者指令行的行首前缀（去掉行首空白后比对）。
var H3WriterDirectivePrefixes = []string{
	"REFERENCE ORDER",                         // 技能侧 & 平台模板都有这条
	"Locate every subject by PICTURE CONTENT", // 同上
	"Every <Subject> must map to a picture",   // 同上
	"- One line per reference picture",        // retention_analysis 段的写法说明
	"- The scene index is not fixed",          // 同上
	"- NEVER write speaker tags",              // 同上
	"(Use official markers",                   // 标记取值表——写给作者的提示，不是内容
	"(Entry format, official",                 // 条目格式说明
	"Official label discipline",               // 官方标签纪律（写给作者的）
}

// h3DirectiveMarkers 契约门用的**任意位置**判据：命中即认为这一镜的提示词里混进了指令行。
// 比行首前缀宽一点（用来报警而不是用来删），但仍是"不可能出现在真内容里"的短语。
var h3DirectiveMarkers = []string{
	"REFERENCE ORDER",
	"characters array",
	"PICTURE MANIFEST",
	"Locate every subject by PICTURE CONTENT",
	"One line per reference picture",
}

// HasH3WriterDirectives 这一镜的提示词里是否混进了生成器指令。
func HasH3WriterDirectives(prompt string) bool {
	if strings.TrimSpace(prompt) == "" {
		return false
	}
	for _, m := range h3DirectiveMarkers {
		if strings.Contains(prompt, m) {
			return true
		}
	}
	return false
}

// StripH3WriterDirectives 剔除指令行，返回清理后的提示词与被删掉的行数。
//
// 幂等：清理后再来一次返回 0 行。只删**整行**，不碰行内内容；删完把连续空行收到一行，
// 免得在 `subject_definitions:` 段留下孤立的空行。
func StripH3WriterDirectives(prompt string) (string, int) {
	if strings.TrimSpace(prompt) == "" {
		return prompt, 0
	}
	lines := strings.Split(prompt, "\n")
	out := make([]string, 0, len(lines))
	removed := 0
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		hit := false
		for _, p := range H3WriterDirectivePrefixes {
			if strings.HasPrefix(t, p) {
				hit = true
				break
			}
		}
		if hit {
			removed++
			continue
		}
		out = append(out, ln)
	}
	if removed == 0 {
		return prompt, 0
	}
	// 收拢连续空行（最多保留一行）
	packed := make([]string, 0, len(out))
	blank := false
	for _, ln := range out {
		isBlank := strings.TrimSpace(ln) == ""
		if isBlank && blank {
			continue
		}
		blank = isBlank
		packed = append(packed, ln)
	}
	return strings.Join(packed, "\n"), removed
}

// ── 占位台词标签（2026-09-28 用户：「配音也怪的很，重复配音」）────────────────────
//
// 实测病根：真渲那一集成片里有 **36/74 个 `<d>` 标签的内容只有「…」**（48 镜里 36 镜各一个），
// 而同一批正文里"计划台词+旁白"与正文**一处不差** —— 也就是说模型把该念的都念对了，
// 多出来的正是这些**空标签**：给模型一个"这里要说点什么"的标签，它就会自己编一句、
// 或者发出叹气/清嗓/半个音节。用户听到的就是"怪配音、重复配音"。
//
// 为什么由平台兜底：`sysH3Format` 是"规则与输出骨架混排"的模板，LLM 直出时会把骨架里的
// 占位示例照抄进正文（`<d>[Chinese]…</d>` 就是骨架里那一行的样子）。等上游改等于这 36 镜永远在。
//
// 清理口径（**只删整句，不留残句**）：仅删标签是不够的 —— 留下 "The narrator says in an
// off-screen voiceover:" 这种半句，模型照样会张嘴。所以命中占位标签时，把**包含它的那一句**
// 整句删掉。边界：只在 `<d>` 内容是"空 / 纯标点省略号 / 无 / N-A"时才动手，宁可漏不可误删。
var placeholderDialogueRe = regexp.MustCompile(`<d>\s*(?:\[(?:Chinese|Mandarin|普通话)\]\s*)?([^<]{0,40}?)\s*</d>`)

// isPlaceholderDialogue 标签内容是不是"没有真话可说"的占位。
func isPlaceholderDialogue(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	// 纯标点/省略号/破折号/空格
	only := true
	for _, r := range t {
		switch r {
		case '.', '。', '…', '·', '、', '-', '—', '～', '~', ' ', '\t', '(', ')', '（', '）':
		default:
			only = false
		}
		if !only {
			break
		}
	}
	if only {
		return true
	}
	// 去掉包裹的括号再判：`（无）` / `(N/A)` 这类真实写法（第一版漏了，测试抓到）
	t = strings.Trim(t, "()（）[]【】 ")
	switch strings.ToLower(t) {
	case "无", "n/a", "na", "none", "silence", "silent", "no dialogue", "此处无台词":
		return true
	}
	return false
}

// IsPlaceholderSpoken 标签内容是不是"没有真话可说"的占位（空 / 纯标点省略号 / 无 / N-A）。
//
// 【为什么导出】pipeline 的**逐刀人声清单**（cutSpeechMap）必须与这里用**同一条**判据：
// 2026-10-08 真机实测，条款正文里引用 `<d>…</d>` 当例子（`enclosed in <d>…</d> tags`、
// `就是 <d>…</d> 里的那几句台词`），按字面扫就把 3 个**示例**当成 3 句台词 ⇒ 逐刀清单写
// 「这一刀 4 行，每行念一遍」而真实只有 1 行 ⇒ 模型把空出来的格子填满（镜 1 重复念、
// 镜 5 凭空多一句「或是血」）。判据只有一处实现，两处共用。
func IsPlaceholderSpoken(s string) bool { return isPlaceholderDialogue(s) }

// HasPlaceholderDialogue 提示词里有没有占位台词标签（契约门用）。
func HasPlaceholderDialogue(prompt string) bool {
	for _, m := range placeholderDialogueRe.FindAllStringSubmatch(prompt, -1) {
		if isPlaceholderDialogue(m[1]) {
			return true
		}
	}
	return false
}

// placeholderSilenceNote 占位标签的替换文本。
//
// 为什么不是"删掉整句"（我第一版就是那么写的，被测试抓了）：`<d>[Chinese]…</d>` 常常嵌在
// 一句有用的话中间（"…voiceover: <d>…</d> while the on-screen characters' lips remain
// completely closed."）—— 按句删会把**后半句有效约束一起删掉**。换成一句明确的指令既保住句子，
// 又把"这里要说点什么"的信号**翻成它的反面**（这镜不说话）。
const placeholderSilenceNote = "(this shot has no spoken words here — never voice a line, no sigh, no filler sound)"

// StripPlaceholderDialogue 把占位台词标签换成明确的"无声"指令，返回(新文本, 替换处数)。
func StripPlaceholderDialogue(prompt string) (string, int) {
	if !strings.Contains(prompt, "<d>") {
		return prompt, 0
	}
	n := 0
	out := placeholderDialogueRe.ReplaceAllStringFunc(prompt, func(m string) string {
		sub := placeholderDialogueRe.FindStringSubmatch(m)
		if len(sub) < 2 || !isPlaceholderDialogue(sub[1]) {
			return m
		}
		n++
		return placeholderSilenceNote
	})
	return out, n
}
