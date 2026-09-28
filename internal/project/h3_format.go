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

	// ── 以下为 2026-09-27 扩的「官方硬要求」输出门 ─────────────────────────────
	//
	// 为什么扩：此前只兜三条（retention 里的 (Sx)、时间戳、风格句位置），而内嵌的官方规范
	// 有二十来条**可机检**的硬要求（六段齐与顺序、亮度守卫、排除句、保留标记、声音段句数、
	// `<d>` 语言标签…）。提示词写全了不等于模型照做 —— 用户问"LLM 解析是否满足 H3 官方要求"
	// 时，如果没有这些门，唯一的答案就是"没法知道"。门让"满不满足"变成可测的。
	//
	// 判据一律**宽松**（有即可，不判措辞）：这些是质量级问题，宁少报不误报。

	// ④ 模式判定与段落齐备：Ref2VA 六段 / T2VA 三段，且顺序固定
	ref2va := containsAny(h3, "subject_definitions:", "retention_analysis:", "detailed_description:")
	if ref2va {
		order := []string{"subject_definitions:", "summary:", "retention_analysis:",
			"detailed_description:", "overall_soundscape:", "non_diegetic_music:"}
		if miss := missingSections(h3, order); len(miss) > 0 {
			out = append(out, fmt.Sprintf(
				"Ref2VA 六段不齐：缺 %s —— 官方要求这六段**顺序固定**、逐段存在（缺段时模型收不到那部分约束）",
				strings.Join(miss, "、")))
		} else if bad := firstOutOfOrder(h3, order); bad != "" {
			out = append(out, fmt.Sprintf(
				"六段顺序不对：%s 出现在它前面那一段之前 —— 官方顺序是 %s",
				bad, strings.Join(order, " → ")))
		}
	} else {
		order := []string{"integrated_multimodal_description:", "overall_soundscape:", "non_diegetic_music:"}
		if miss := missingSections(h3, order); len(miss) > 0 {
			out = append(out, fmt.Sprintf(
				"T2VA 三段不齐：缺 %s —— 官方要求这三段顺序固定", strings.Join(miss, "、")))
		}
	}

	// ⑤ 亮度守卫句（官方强制：正文最后一句）
	if !containsAny(h3, "clearly visible and adequately lit", "adequately lit throughout") {
		out = append(out, "缺官方**强制亮度守卫句** —— detailed_description 的最后一句必须是 "+
			"\"Subjects must remain clearly visible and adequately lit throughout the video; avoid rendering any frame nearly black or overly dark.\"")
	}

	// ⑥ 排除句（不写字/水印/LOGO/字幕 —— 与平台的「画面文字禁止」同一条）
	if !containsAny(strings.ToLower(h3), "no watermark", "no logo", "no captions", "no added lettering") {
		out = append(out, "缺**排除句** —— 正文结尾要用一句散文排除水印/LOGO/字幕/衣物上加字（否则模型会自己加字）")
	}

	// ⑦ retention_analysis 的保留标记必须用官方词表
	if seg, ok := sectionOf(h3, "retention_analysis:"); ok {
		if !containsAny(seg, "fully_preserved", "partially_preserved", "attribute_transfer", "weak_reference") {
			out = append(out, "retention_analysis 里没有任何官方保留标记（fully_preserved / partially_preserved / "+
				"attribute_transfer / weak_reference）—— 这一段逐行说「每张参考图保留了什么」")
		}
	}

	// ⑧ 声音两段：非空（或 N/A）＋句数上限（官方 1–4 句 / 1–3 句）
	if seg, ok := sectionOf(h3, "overall_soundscape:"); ok {
		if n := sentenceCount(seg); n > h3MaxSoundscapeSentences {
			out = append(out, fmt.Sprintf(
				"overall_soundscape 写了 %d 句（官方上限 %d 句、一段连续散文）—— 这里只写环境音与非语言人声，"+
					"对白/歌唱/画面内有源音乐都在正文里，不该复述", n, h3MaxSoundscapeSentences))
		}
	}
	if seg, ok := sectionOf(h3, "non_diegetic_music:"); ok {
		if n := sentenceCount(seg); n > h3MaxMusicSentences {
			out = append(out, fmt.Sprintf(
				"non_diegetic_music 写了 %d 句（官方上限 %d 句）—— 只写乐器/速度/节奏/动态，不写抽象情绪词",
				n, h3MaxMusicSentences))
		}
	}

	// ⑨ 说话人标记只许出现在正文段（retention 那条已在 ① 报了，这里管别处）
	if m := speakerTagOutsideDescription(h3); m != "" {
		out = append(out, fmt.Sprintf(
			"说话人标记 %s 出现在正文段之外 —— (Sx) 只写在 detailed_description 的台词句里"+
				"（retention_analysis 尤其不许），实体锁定句写在正文段开头", m))
	}

	// ⑨b 分镜/内部标签（官方 qc-checklist STEP 8：成片不许带分镜痕迹）
	if tag := storyboardTraceIn(h3); tag != "" {
		out = append(out, fmt.Sprintf(
			"正文里出现了内部标签 %s —— 官方终检要求成片「no storyboard traces」；"+
				"这类方括号标签会被渲染成画面上的乱码字形（与汉字卫生同一类事故），请改成自然语言描述",
			tag))
	}

	// ⑩ 时间戳必须**严格递增**（此前只判形态）
	if times := shotTimestamps(h3); len(times) > 1 {
		for i := 1; i < len(times); i++ {
			if times[i].ms <= times[i-1].ms {
				out = append(out, fmt.Sprintf(
					"时间戳没有严格递增：[Shot %d] %s 之后是 [Shot %d] %s —— 官方要求严格递增（两位小数）",
					times[i-1].n, times[i-1].raw, times[i].n, times[i].raw))
				break
			}
		}
	}
	return out
}

// StyleSentenceOf 取 `h3_prompt` 里的**风格句**：`detailed_description:` 之后、第一个 `[Shot`
// 之前的那一段（官方格式规定风格句写在这儿，`ref-en.txt` §5.2）。
//
// 为什么要有它（2026-09-27）：技能侧的提示词**自带**这段风格句，而平台装配时也会插一段
// 平台侧的风格块 —— 一段提示词两套风格，模型只会取中值（与角色卡那条是同一条机理）。
// 平台不删上游文本（那是它的定义权），而是**让位**：装配时发现已有风格句就不再注入。
// 同一判据两处用：`pipeline`（装配让位）与 `server`（导入时把风格句提成 plan.Style，
// 好让参考图也服从同一套风格）。**只能有一份实现** —— 两处各写一段正则必然走散。
//
// 返回 (风格句, 是否存在)。切不出就返回空与 false（不做猜测）。
func StyleSentenceOf(h3 string) (string, bool) {
	i := strings.Index(h3, "detailed_description:")
	if i < 0 {
		return "", false
	}
	head := i + len("detailed_description:")
	j := strings.Index(h3[head:], "[Shot")
	if j < 0 {
		return "", false
	}
	if s := strings.TrimSpace(h3[head : head+j]); s != "" {
		return s, true
	}
	return "", false
}

// HasStyleSentence 提示词里是否已经带了风格句（装配让位用的判据）。
func HasStyleSentence(h3 string) bool {
	_, ok := StyleSentenceOf(h3)
	return ok
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
