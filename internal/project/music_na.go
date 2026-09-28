package project

import (
	"regexp"
	"strings"
)

// music_na.go 把每镜的 `non_diegetic_music` **确定性**改写成 `N/A`。
//
// 【为什么是确定性的，而不是让模型自觉】
// 平台政策：**每个片段的 non_diegetic_music 一律 N/A**，配乐统一在合片阶段铺一条连续 BGM
// （见 compose/bgm.go，以及 docs/与官方的有意分歧.md 的 DEV-05）。理由是官方同时禁止逐镜配乐
// （qc-checklist STEP 8）—— 每个片段是独立生成的，逐镜带曲子会让成片**在每个接缝处换调**。
//
// 这条政策**已经写进了提示词**，但实测真方案里仍有 **19 镜**写着配乐 —— 再一次印证仓库那条
// 纪律：**散文规则管不住模型，能确定性做到的就别让它自觉**（同 EnsurePropAnchors / 建立镜头串）。
//
// 做法：正文里若存在非 N/A 的 `non_diegetic_music:` 段，就地改写为 N/A，并**如实播报**改了几处。
// 判据用与门相同的 isNA，避免"门说违规、改写说合规"两套判据打架。
func EnsureMusicNA(plan *Plan) int {
	if plan == nil {
		return 0
	}
	fixed := 0
	for i := range plan.Shots {
		p := plan.Shots[i].H3Prompt
		if strings.TrimSpace(p) == "" {
			continue
		}
		np, changed := musicSectionToNA(p)
		if changed {
			plan.Shots[i].H3Prompt = np
			fixed++
		}
	}
	return fixed
}

// musicSectionToNA 把 `non_diegetic_music:` 之后的**整段**替换为 N/A。
//
// 段的边界：下一个六段式段名（subject_definitions:/summary:/retention_analysis:/
// detailed_description:/overall_soundscape:）或正文结束。这样"配乐写了三句话"也能整段换掉，
// 而不是只换第一行留下残句。
func musicSectionToNA(prompt string) (string, bool) {
	idx := indexOfSection(prompt, "non_diegetic_music:")
	if idx < 0 {
		return prompt, false
	}
	head := prompt[:idx]
	// 段名自身的长度（含可能的前导换行，保持原来的排版）
	labelEnd := idx + len("non_diegetic_music:")
	rest := prompt[labelEnd:]
	// 找出本段的结束位置
	end := len(rest)
	for _, name := range h3SectionNames {
		if name == "non_diegetic_music:" {
			continue
		}
		if j := indexOfSection(rest, name); j >= 0 && j < end {
			end = j
		}
	}
	body := rest[:end]
	tail := rest[end:]
	if isNA(strings.TrimSpace(body)) {
		return prompt, false // 已经是 N/A，不动它
	}
	return head + "non_diegetic_music:\nN/A\n" + strings.TrimLeft(tail, " \t\r\n"), true
}

// h3SectionNames 六段式的段名（顺序即官方要求的顺序）。
var h3SectionNames = []string{
	"subject_definitions:",
	"summary:",
	"retention_analysis:",
	"detailed_description:",
	"overall_soundscape:",
	"non_diegetic_music:",
}

// indexOfSection 在文本里找段名（**行首**才算，避免把正文里出现的同名单词当段名）。
var reSectionLine = regexp.MustCompile(`(?m)^[ \t]*`)

func indexOfSection(s, name string) int {
	// 逐行找，行首（允许前导空白）匹配段名
	off := 0
	for off <= len(s) {
		nl := strings.IndexByte(s[off:], '\n')
		line := s[off:]
		if nl >= 0 {
			line = s[off : off+nl]
		}
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), name) {
			return off + (len(line) - len(strings.TrimLeft(line, " \t")))
		}
		if nl < 0 {
			return -1
		}
		off += nl + 1
	}
	return -1
}

var _ = reSectionLine
