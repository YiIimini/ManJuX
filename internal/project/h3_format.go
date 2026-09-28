package project

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
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
	// ⑯ summary 段必须以 **[任务类型]** 开头（官方 ref-en.txt §3："summary 以方括号任务类型开头"：
	// [reference generation] / [video editing] / [audio continuation] / [audio reuse] / [audio reference]
	// / [keyframe completion]，多类型用 " + " 连接且不重复）。缺了它，模型不知道这一次是"生成"还是"改片"。
	if sec, ok := sectionOf(h3, "summary"); ok {
		first := ""
		for _, ln := range strings.Split(strings.TrimLeft(sec, ":： \t"), "\n") {
			if s := strings.TrimSpace(ln); s != "" {
				first = s
				break
			}
		}
		if first != "" && !strings.HasPrefix(first, "[") {
			out = append(out, "summary 段没有以 [任务类型] 开头（官方 §3：如 [reference generation]）—— "+
				"模型靠它判断这一次是生成、改片还是续音频，缺了会按默认理解走偏")
		}
	}
	// ⑰ 时间戳的毫秒位必须是**三位**（官方 §4.2/§5.1 的写法是 `At MM:SS.mmm`）。
	// 两位/一位会让"严格递增"那条判据直接漏判（正则匹配不上，等于没写时间戳）。
	if bad := h3LooseTimestamp(h3); bad != "" {
		out = append(out, fmt.Sprintf(
			"时间戳写法不规范：%s —— 官方要求 `At MM:SS.mmm`（毫秒恰好三位），毫秒位不足会被当成没写时间戳，"+
				"切点顺序也就无从校验", bad))
	}
	// ⑪–⑮ 官方 §2/§5.4 里**有要求、此前没有门**的五条（2026-09-28 全面审计补齐）。
	// 这五条都属于"写错了不报错、只是成片变差/听感变怪"的那一类，所以必须机器判。
	out = append(out, h3DialogueTagViolations(h3)...)
	out = append(out, h3LabelNumberingViolations(h3)...)
	out = append(out, h3SpeakerIDViolations(h3)...)
	return out
}

// h3DialogueTagViolations ⑪⑫ `<d>` 标签的两条官方要求（`ref-en.txt` §5.4）：
//   - 每段台词必须带**语言标签**（`<d>[Chinese]…</d>`）—— 模型靠它决定用哪种语言发声；
//   - 标点**归一化**到 , . ? !，去掉波浪号与重复装饰标点，且整句以 . ? ! 收尾
//     （原文："Standardize punctuation to the basic written marks … remove repeated tildes,
//     emoji, bullets, and repeated or decorative punctuation. End complete statements … with
//     `.`, `?`, or `!` before </d>"）。
func h3DialogueTagViolations(h3 string) []string {
	var out []string
	noLang, badPunct := 0, 0
	for _, m := range h3DialogueRe.FindAllStringSubmatch(h3, -1) {
		body := strings.TrimSpace(m[1])
		if body == "" {
			continue // 空标签由占位符那条管
		}
		if !strings.HasPrefix(body, "[") {
			noLang++
			continue
		}
		// 去掉语言标签后看标点
		if i := strings.Index(body, "]"); i > 0 {
			body = strings.TrimSpace(body[i+1:])
		}
		if body == "" {
			continue
		}
		if h3BadDialoguePunct(body) {
			badPunct++
		}
	}
	if noLang > 0 {
		out = append(out, fmt.Sprintf(
			"%d 段台词没有**语言标签**（写成 <d>[Chinese]台词</d>）—— 官方 §5.4：语言标签决定这段用什么语言发声，"+
				"缺了模型会自己猜（中英混排时最明显）", noLang))
	}
	if badPunct > 0 {
		out = append(out, fmt.Sprintf(
			"%d 段台词的标点不规范 —— 官方 §5.4：统一到基本书面标点（, . ? !），去掉波浪号、表情、项目符号与"+
				"重复/装饰性标点，整句以 . ? ! 收尾", badPunct))
	}
	return out
}

// h3BadDialoguePunct 台词标点是否违反官方归一化要求。
func h3BadDialoguePunct(s string) bool {
	if strings.ContainsAny(s, "~～") {
		return true
	}
	// 重复标点：！！ ?? 。。。 ，，
	for _, pair := range []string{"!!", "??", "。。", "，，", "！！", "？？", "……"} {
		if strings.Contains(s, pair) {
			return true
		}
	}
	// 结尾必须是句末标点
	end := []rune(strings.TrimRight(s, " \t"))
	if len(end) == 0 {
		return false
	}
	switch end[len(end)-1] {
	case '.', '?', '!', '。', '？', '！':
		return false
	}
	return true
}

// h3LabelNumberingViolations ⑬ `<Picture N>` / `<Subject N>` 的编号必须**连续且不跳号**
// （官方 §2.1/§2.2：标签按出现顺序编号，模型靠编号认"这是第几张参考图"）。跳号会让模型
// 去猜一个不存在的参考图，或者把两张图对错号 —— 那是"人物乱配"的经典成因。
func h3LabelNumberingViolations(h3 string) []string {
	var out []string
	for _, kind := range []struct {
		name string
		re   *regexp.Regexp
	}{
		{"<Picture N>", h3PictureRe},
		{"<Subject N>", h3SubjectRe},
	} {
		nums := map[int]bool{}
		max := 0
		for _, m := range kind.re.FindAllStringSubmatch(h3, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil || n <= 0 {
				continue
			}
			nums[n] = true
			if n > max {
				max = n
			}
		}
		if max == 0 {
			continue
		}
		for i := 1; i <= max; i++ {
			if !nums[i] {
				out = append(out, fmt.Sprintf(
					"%s 编号跳号：用到 %d 号却没有 %d 号 —— 官方 §2 要求标签按出现顺序连续编号，"+
						"跳号会让模型去猜一张不存在的参考图（人物/道具对错号的经典成因）", kind.name, max, i))
				break
			}
		}
	}
	return out
}

// h3SpeakerIDViolations ⑭⑮ 说话人 ID：**分配了就必须用**，且只许用正文里分配过的号。
//
// 官方 §5.4：「Assign (Sx) once according to the order of actual vocal events … Reuse the
// corresponding ID at every actual vocal event」。分配了却从没用过 = 有台词没写（那几句会被
// 说成听不懂的话，与"缺 <d> 标记"同一后果）；正文用了没分配过的号 = 模型自造说话人。
func h3SpeakerIDViolations(h3 string) []string {
	var out []string
	assigned := map[string]bool{}
	used := map[string]bool{}
	desc, ok := sectionOf(h3, "detailed_description")
	if !ok {
		desc = h3
	}
	for _, m := range h3SpeakerRe.FindAllStringSubmatch(h3, -1) {
		id := "S" + strings.TrimSpace(m[1]) // 标记本体是 `(S1)` —— 只取数字会拼出 "(1)"，判不出来
		// 出现在 detailed_description 里的算"用过"，其余（subject_definitions 等）算"已分配"
		if strings.Contains(desc, "("+id+")") {
			used[id] = true
		} else {
			assigned[id] = true
		}
	}
	var unused []string
	for id := range assigned {
		if !used[id] {
			unused = append(unused, id)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		out = append(out, fmt.Sprintf(
			"定义了说话人 %s 却**从未在正文里说话** —— 官方 §5.4：分配了 (Sx) 就要在每次实际发声处复用；"+
				"分配了不用，说明有台词没写进正文（那几句会被说成听不懂的话）", strings.Join(unused, "、")))
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

// h3LooseTimestamp 找出毫秒位不足三位的时间戳写法（官方写法是 `At MM:SS.mmm`）。
//
// 为什么要单独判：严格递增那条用的是 `\.(\d{3})` 的正则，毫秒位写两位就**匹配不上** ——
// 于是"时间戳乱序"这件事会静默漏判（看起来像"没有时间戳"，实际有，只是写法不合规）。
func h3LooseTimestamp(h3 string) string {
	// 毫秒部分必须**贪婪吃到结尾**（\d+）：写成 \d{1,2} 会回溯，把 00:02.500 只匹配成 00:02.50，
	// 于是合法写法被误判成"两位毫秒"（实测踩过：我自己的夹具先被冤了一条）。
	re := regexp.MustCompile(`At\s+(\d{1,2}):(\d{2})(?:\.(\d+))?`)
	for _, m := range re.FindAllStringSubmatch(h3, -1) {
		if m[3] == "" || len(m[3]) != 3 {
			return m[0]
		}
	}
	return ""
}
