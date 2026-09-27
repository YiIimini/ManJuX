package project

import "strings"

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
