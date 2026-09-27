package project

import (
	"fmt"
	"regexp"
	"strings"
)

// h3_format.go 官方 H3 提示词格式的**可机检部分**（技能侧整改单 §1.5 的 10 条硬规则）。
//
// ## 为什么要有
//
// 整改单把 10 条官方硬规则逐条列了出来，并点明它们的共同性质：
// **写错了不报错，只是成片变差** —— 这正是最难发现的一类问题（渲染成功、成片垃圾、
// 没人知道该怪谁）。10 条里大部分只能靠"提示词写对"（措辞约束），但有 3 条是纯文本形态、
// 机器判得出来，于是放这里：
//
//	① `retention_analysis` 段里不许出现 `(Sx)` —— 说话人/实体锁定句写在 detailed_description
//	   开头；官方原文的 retention_analysis 只描述"参考图保留了什么"，塞进说话人标记会让
//	   模型把"谁在说话"当成参考图属性（技能侧原文：不许写 (Sx)、不许写自定义行）；
//	② 时间戳形态：`[Shot 1]` **不带**时间戳，后续 `[Shot N] At MM:SS.mmm`（两位小数）；
//	③ 风格句必须在**第一个 [Shot] 之前**（1–2 句英文），不是写进首镜里 ——
//	   写进首镜 = 风格只作用于那一镜，其余镜头各自漂。
//
// 判据返回**人话清单**（每条一句、可直接贴进自检面板），空切片 = 没发现问题。
// 这些是**质量级**问题（不阻断渲染），所以契约里按聚合 warn 报，不 fail。
func H3FormatViolations(h3 string) []string {
	var out []string
	if strings.TrimSpace(h3) == "" {
		return out
	}

	// ① retention_analysis 段里的 (Sx)
	if seg, ok := sectionOf(h3, "retention_analysis:"); ok {
		if m := speakerTagRe.FindString(seg); m != "" {
			out = append(out, fmt.Sprintf(
				"retention_analysis 里出现了说话人标记 %s —— 这一段只写「参考图保留了什么」，"+
					"说话人/实体锁定句一律写在 detailed_description 开头（官方硬规则）", m))
		}
	}

	// ② 时间戳：首镜不许带，后续必须是 MM:SS.mmm
	if m := firstShotTimestampRe.FindStringSubmatch(h3); m != nil {
		out = append(out, fmt.Sprintf(
			"[Shot 1] 带了时间戳（%s）—— 官方要求首镜无时间戳，从 [Shot 2] 起才写 At MM:SS.mmm", m[1]))
	}
	for _, m := range shotTimestampRe.FindAllStringSubmatch(h3, -1) {
		if !timestampRe.MatchString(m[2]) {
			out = append(out, fmt.Sprintf(
				"[Shot %s] 的时间戳 %q 形态不对 —— 必须是 MM:SS.mmm（两位小数，严格递增）", m[1], m[2]))
		}
	}

	// ③ 风格句位置：第一个 [Shot] 之前必须有内容
	for _, marker := range []string{"detailed_description:", "integrated_multimodal_description:"} {
		seg, ok := sectionOf(h3, marker)
		if !ok {
			continue
		}
		idx := strings.Index(seg, "[Shot")
		if idx < 0 {
			continue // 没有分镜段落，说明是别的问题，不在本判据范围内
		}
		if strings.TrimSpace(seg[:idx]) == "" {
			out = append(out, fmt.Sprintf(
				"%s 的第一个 [Shot] 之前是空的 —— 风格句（1–2 句英文）必须写在这里，"+
					"写进首镜等于只有那一镜是这部片的画风", strings.TrimSuffix(marker, ":")))
		}
	}
	return out
}

// sectionOf 取 `marker` 之后、下一个六段式小节标题之前的那一段。
//
// 为什么按小节切而不是全文搜：`(S1)` 在 detailed_description 里是**合法且必须**的写法
// （说话人锁定句就在那儿），只有 retention_analysis 段里出现才是错。全文搜会把正确写法
// 判成违规 —— 误拦的规则比没有规则更糟。
func sectionOf(h3, marker string) (string, bool) {
	i := strings.Index(h3, marker)
	if i < 0 {
		return "", false
	}
	rest := h3[i+len(marker):]
	cut := len(rest)
	for _, next := range h3SectionTitles {
		if j := strings.Index(rest, next); j >= 0 && j < cut {
			cut = j
		}
	}
	return rest[:cut], true
}

// h3SectionTitles 六段式/三段式的小节标题（切段用）。
var h3SectionTitles = []string{
	"subject_definitions:", "summary:", "retention_analysis:", "detailed_description:",
	"integrated_multimodal_description:", "overall_soundscape:", "non_diegetic_music:",
}

var (
	// speakerTagRe 说话人标记：(S1) / (S1,S2) / (S12)
	speakerTagRe = regexp.MustCompile(`\(S\d+(?:,S\d+)*\)`)
	// firstShotTimestampRe [Shot 1] 紧跟的 At 值
	firstShotTimestampRe = regexp.MustCompile(`\[Shot\s+1\]\s*At\s+([0-9:.]+)`)
	// shotTimestampRe 任意 [Shot N] At 值
	shotTimestampRe = regexp.MustCompile(`\[Shot\s+(\d+)\]\s*At\s+([0-9:.]+)`)
	// timestampRe MM:SS.mmm（两位小数）
	timestampRe = regexp.MustCompile(`^\d{2}:\d{2}\.\d{2}\d?$`)
)
