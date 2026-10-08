package project

import (
	"fmt"
	"math"
	"strings"
)

// empty_body.go 空正文的**装配点兜底**（2026-10-08，用户第二次报「还是报一样的错误」）。
//
// ## 现场（取证）
//
// 真机《我捡的团子，暖了三千人的船》第01集镜 12：场次 / 人物 / 走位 / 景别 / 机位 /
// 台词（两句，逐字都在）/ 时长**材料齐全**，只有 `action` 与 `h3_prompt` 是空的。
// 剧本阶段的自纠一轮**点名了它、也没能补上**（真因见 director.emptyShotFix：那条修复指令
// 当时只把「时长 + 动作」给了模型，而这一镜的动作本身也是空的 ⇒ 模型手上没有任何内容，
// 写出来的正文里当然没有计划里的那两句 `<d>` ⇒ `adoptRepaired` 的「台词条数必须等于
// 计划里那两句」把它判成改坏了、退回原文（原文是空的））。
//
// 后果不是「这一镜不好看」，而是**整条产线停在第 1 集**：渲染入口的致命门
// 「h3_prompt 为空」直接不启动（48 集的批量产线一级都过不去）。
//
// ## 为什么由平台拼
//
// 这一镜的材料**平台手上全有**（场次卡、角色卡、走位、景别、机位、台词原文、时长），
// 所以按「能确定性做到的事不要让模型自觉」的纪律：在**唯一读取边界**（`HealPlan`）与
// 剧本阶段存盘前，按**方案自己的材料**拼一版最小合规正文，渲染就能继续；补过哪几镜
// 由 `Plan.FilledBodyShots` 记着，剧本阶段与渲染阶段**如实播报**（"不是新编内容"）。
//
// **材料真的什么都没有**（没有场次、没有人物、没有台词）才不拼 —— 那种镜拼出来等于替
// 编剧新编一镜，那是创作不是兜底，留给渲染入口的致命门如实拦。
//
// 拼出来的正文必须能过 `H3FormatViolations` 的**全部**机器判据（六段齐与顺序、风格句位置、
// 首镜无时间戳 / 其余三毫秒位、严格递增、亮度守卫句、排除句、保留标记、`<Subject N>` 连号、
// `<d>` 语言标签……），判据见 empty_body_test.go。

const (
	// EmptyBodyGuard 官方**强制亮度守卫句**（`h3_format.go` ⑤ 的门同一条判据）。
	EmptyBodyGuard = "Subjects must remain clearly visible and adequately lit throughout the video; " +
		"avoid rendering any frame nearly black or overly dark."
	// EmptyBodyExclusion 官方**排除句**（不写字/水印/LOGO/字幕/衣物加字，同 ⑥ 那条门）。
	EmptyBodyExclusion = "No watermarks, no logos, no subtitles, no added lettering on clothing or props."
	// emptyBodyDescRunes 单张卡的外形描述写进正文时的截断长度。
	// 正文词数有上限（官方 350–500 词，超了会被自纠再改一遍），描述是「逐项照抄」的那部分，
	// 掐到第一句就够锁住身份；截断时留省略号（**不静默**改字面之外的措辞）。
	emptyBodyDescRunes = 120
	// emptyBodyNeutralVoice 拼句子时用的**中性**声线措辞。
	//
	// 【为什么不写男女/年龄】与 `FillMissingVoices` 同一条纪律：不许假定人物的性别与年龄。
	// 这里只是给模型一句"该念了"的引述，真正的权威声线由 `pipeline.BindLineVoices`
	// 按角色卡逐字重绑（`replaceVoiceClause` 认得出 `says … in … voice` 这个形状）。
	emptyBodyNeutralVoice = "a neutral voice"
)

// emptyBodySubject 拼正文时用的一次角色材料（每一项都取自方案自己，不外部查表）。
type emptyBodySubject struct {
	name string
	desc string
	pos  string
}

// emptyBodyLine 方案里记着的一句要念的话。
type emptyBodyLine struct {
	speaker string
	text    string
	os      bool
}

// FillEmptyShotBodies 把**正文为空、但材料够**的镜按方案自己的材料拼一版最小合规正文（幂等）。
//
// 返回 (补了几镜, 哪几镜)。补过的镜号同时记在 `Plan.FilledBodyShots` 上 —— 各阶段
// （剧本阶段 / 渲染阶段）据此如实播报；正文非空的镜**一个字都不碰**。
// `Plan.FilledBodyCJK` 记下其中几镜有**中文材料没能进正文**（汉字卫生门，见下）。
func FillEmptyShotBodies(p *Plan) (int, []int) {
	if p == nil {
		return 0, nil
	}
	var ids []int
	dropped := 0
	for i := range p.Shots {
		sh := &p.Shots[i]
		if strings.TrimSpace(sh.H3Prompt) != "" {
			continue
		}
		body, ok, cjkDropped := assembleMinimalBody(p, sh)
		if !ok {
			continue // 材料不够：不硬编（交给渲染入口的致命门如实拦）
		}
		sh.H3Prompt = body
		if len(sh.Cuts) == 0 {
			// 拼出来的正文本来就带了内切，结构字段一起回填 —— 否则界面上"这一镜没切"，
			// 而正文里明明有 `[Shot 2] At …`（两边说的不是一件事）。
			sh.Cuts = minimalCuts(sh.Duration)
		}
		if cjkDropped {
			dropped++
		}
		ids = append(ids, sh.ID)
	}
	if len(ids) > 0 {
		p.FilledBodyShots = ids
		p.FilledBodyCJK = dropped
	}
	return len(ids), ids
}

// assembleMinimalBody 按方案自己的材料拼一版最小合规正文。材料不够时返回 ok=false。
//
// 第二个 bool 表示"这一镜有**中文材料**（走位/动作/外形描述）因为汉字卫生门而没能进正文"。
//
// 【为什么中文材料一律不进正文（2026-10-08，真机 preflight 当场抓出来的）】
// 入口有一条 fail 级契约门：`h3_prompt` 里 `<d>` 外**可避免的汉字** > 200 就**拒收**
// （`prompt_cjk.go`：合法位置只有 `<d>[Chinese]…</d>` 与**主体名**，判据与技能侧同源）。
// 第一版兜底正文把卡片中文描述 + 中文走位照抄进去 → 那一镜 236 个字 ⇒ **fail**
// （等于把"空提示词"这个 fatal 换成"汉字卫生"这个 fatal）。所以这里只用**英文材料**：
// 卡片的英文描述/英文定妆提示词、机位、景别（封闭词表 → 英文）、台词（本来就进 `<d>`），
// 而**名字**是豁免的（官方要求点名主体）。
// 中文的走位/动作因此进不去 —— 这一镜少了这部分信息（靠参考图与机位补），**如实记账并播报**。
func assembleMinimalBody(p *Plan, sh *Shot) (string, bool, bool) {
	if p == nil || sh == nil {
		return "", false, false
	}
	sceneName, sceneDesc, hasScene := sceneMaterial(p, sh)
	dropped := false
	var subs []emptyBodySubject
	for _, cid := range sh.Characters {
		c, ok := charMaterial(p, cid)
		if !ok || strings.TrimSpace(c.Name) == "" {
			continue
		}
		// 外形：中文描述进不了正文（汉字卫生门），但卡片**本来就有英文定妆提示词**（翻译会话写的），
		// 那一份是逐项照抄的英文外形 —— 用它，身份信息一点没丢 ⇒ **不算"丢了材料"**。
		// 只有连英文那一份也拿不到时，才记一笔"这镜的外形没能进正文"。
		desc := englishClause(c.Description, c.ImagePrompt)
		if CountCJK(c.Description) > 0 && desc == "" {
			dropped = true
		}
		// 走位：优先用**英文版**（专职翻译会话补的 `positions_en`），其次用方案里那份（本来就是英文时）。
		// 两样都拿不到才记一笔"这一镜的走位没能进正文" —— 那是真的少了信息（只能靠参考图与机位兜）。
		zhPos := posOf(sh, c)
		pos := englishOnly(posENOf(sh, c))
		if pos == "" {
			pos = englishOnly(zhPos)
		}
		if pos == "" && CountCJK(zhPos) > 0 {
			dropped = true
		}
		subs = append(subs, emptyBodySubject{name: strings.TrimSpace(c.Name), desc: desc, pos: pos})
	}
	sceneDesc = englishClause(sceneDesc, sceneImagePrompt(p, sh.Scene))
	if sceneDesc == "" && strings.TrimSpace(sh.Scene) != "" {
		// 场景描述进不去（中文且没有英文定妆提示词）—— 这一镜只剩场景名与参考图。
		// 只有确实**写了**中文描述才算丢（本来就空的不算）。
		if sc := sceneByID(p, sh.Scene); sc != nil && CountCJK(sc.Description) > 0 {
			dropped = true
		}
	}
	lines := shotSpeechLines(sh)
	if !hasScene && len(subs) == 0 && len(lines) == 0 {
		return "", false, false
	}
	d := sh.Duration
	if d <= 0 {
		d = PlanMinShotSeconds
	}
	cuts := minimalCuts(d)
	byCut := distributeLines(lines, len(cuts))
	// 动作：同理 —— 英文版（`action_en`，翻译会话补的）优先，其次方案里那份（本来就是英文时）；
	// 两者都拿不到而原字段是中文，才记一笔"这一镜的动作没能进正文"。
	if actionOf(sh) == "" && CountCJK(sh.Action) > 0 {
		dropped = true
	}

	var b strings.Builder
	// ① subject_definitions —— 每个 <Subject N> 对应一张卡（编号连续，官方 §2.1）
	b.WriteString("subject_definitions:\n")
	for i, s := range subs {
		line := fmt.Sprintf("<Subject %d> is %s, the character in the first attached picture", i+1, s.name)
		if s.desc != "" {
			line += ": " + sentenceDot(s.desc)
		}
		if s.pos != "" {
			line += " In this shot: " + sentenceDot(s.pos)
		}
		b.WriteString(sentenceDot(line) + "\n")
	}
	if hasScene {
		line := "The scene is the LAST attached picture: " + sceneName
		if sceneDesc != "" {
			line += ", " + sceneDesc
		}
		b.WriteString(sentenceDot(line) + "\n")
	}
	if len(subs) > 0 {
		fmt.Fprintf(&b, "Their front sheets are the first %d attached pictures; "+
			"each is the SAME subject in every other picture showing it.\n", len(subs))
	}

	// ② summary —— 必须以方括号任务类型开头（官方 §3）
	b.WriteString("\nsummary:\n")
	fmt.Fprintf(&b, "[reference generation] %s\n", summaryLine(sceneName, hasScene, subs, lines))

	// ③ retention_analysis —— 逐行说"每张参考图保留了什么"，这里**不许出现 (Sx)**（官方硬规则①）
	b.WriteString("\nretention_analysis:\n")
	for i := range subs {
		fmt.Fprintf(&b, "<Subject %d> (appears in [Shot 1]): fully_preserved.\n", i+1)
	}
	if hasScene {
		fmt.Fprintf(&b, "The %s scene (the LAST attached picture): weak_reference - "+
			"layout, landmark positions and interior light referenced, camera free.\n", sceneName)
	}

	// ④ detailed_description —— 风格句 + 逐刀正文 + 官方两句结尾
	b.WriteString("\ndetailed_description:\n")
	b.WriteString(styleSentence(p, sh) + "\n\n")
	for i := range cuts {
		b.WriteString(cutParagraph(sh, subs, byCut[i], i, cuts[i].At))
	}
	b.WriteString("\n" + EmptyBodyExclusion + " " + EmptyBodyGuard + "\n")

	// ⑤⑥ 声音两段（官方：各一段连续散文；本镜没有有源音乐时音乐段写 N/A）
	b.WriteString("\noverall_soundscape:\n")
	b.WriteString(soundscapeLine(subs, hasScene))
	b.WriteString("\n\nnon_diegetic_music:\nN/A\n")
	return b.String(), true, dropped
}

// summaryLine summary 段的第一句：只写方案自己记着的东西（地点 + 谁在场 + 有几句要说）。
func summaryLine(sceneName string, hasScene bool, subs []emptyBodySubject, lines []emptyBodyLine) string {
	place := "This shot"
	if hasScene && strings.TrimSpace(sceneName) != "" {
		place = "The " + sceneName + " scene"
	}
	var who string
	switch {
	case len(subs) > 0 && len(lines) > 0:
		who = " " + speakerNames(subs) + " are in frame and the lines below are what is heard:"
	case len(subs) > 0:
		who = " " + speakerNames(subs) + " are in frame:"
	case len(lines) > 0:
		who = " the lines below are heard over the scene:"
	}
	return fmt.Sprintf("%s, the staging is the one recorded for this beat;%s "+
		"the attached front sheets lock each face and the scene picture locks the place, camera free.",
		place, who)
}

// speakerNames 把"谁在场"写成一句人话（去重、按出场顺序）。
func speakerNames(subs []emptyBodySubject) string {
	seen := map[string]bool{}
	var names []string
	for _, s := range subs {
		if s.name == "" || seen[s.name] {
			continue
		}
		seen[s.name] = true
		names = append(names, s.name)
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// cutParagraph 一刀的正文：镜号（首刀不带时间戳）＋ 景别/机位 ＋ 走位 ＋ 这一刀要念的话。
//
// 只写方案里**已有的**东西：景别（词表 → 英文）、机位、走位（仅英文时）、台词原文、动作描述（仅英文时）。
// **不写任何新编的动作/运镜/情绪** —— 那是创作，归编剧与分镜会话。
// 中文的动作/走位在这里一律**不进正文**（汉字卫生门，见 assembleMinimalBody 的注释）。
func cutParagraph(sh *Shot, subs []emptyBodySubject, lines []emptyBodyLine, idx int, at float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Shot %d]", idx+1)
	if idx > 0 {
		b.WriteString(" " + cutStamp(at))
	}
	if s := shotSizeLabel(sh.ShotSize); s != "" {
		b.WriteString(" " + s)
	}
	if c := englishOnly(sh.Camera); c != "" {
		b.WriteString(" " + c)
	}
	b.WriteString(": ")
	var parts []string
	if idx == 0 {
		if act := actionOf(sh); act != "" {
			parts = append(parts, act)
		}
	}
	if idx == 0 {
		if st := stagingOf(subs); st != "" {
			parts = append(parts, "In frame: "+st)
		}
	} else if names := speakerNames(subs); names != "" {
		parts = append(parts, names+" keep the positions recorded for [Shot 1]")
	}
	parts = append(parts, spokenPart(lines))
	// 各段自己带标点（动作原文常以「。」收尾），先削掉尾部标点再逐句用 `. ` 连起来，
	// 免得正文里出现「。」后面又跟一个 `.`（模型读到双标点会当成两句话）。
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		clean = append(clean, strings.TrimRight(strings.TrimSpace(part), ".。 "))
	}
	b.WriteString(sentenceDot(strings.Join(clean, ". ")))
	b.WriteString("\n\n")
	return b.String()
}

// stagingOf 逐个人物的走位（方案 `positions` 的原文，不翻译不改写）。
func stagingOf(subs []emptyBodySubject) string {
	var parts []string
	for _, s := range subs {
		if strings.TrimSpace(s.pos) == "" {
			continue
		}
		parts = append(parts, s.name+" "+strings.TrimSpace(s.pos))
	}
	return strings.Join(parts, "; ")
}

// spokenPart 这一刀的人声部分：一句一句都要**逐字**进 `<d>[Chinese]…</d>`；
// 一句话都没有的那些刀要**明说没人说话**（否则模型会照填空、自己编一句）。
func spokenPart(lines []emptyBodyLine) string {
	if len(lines) == 0 {
		return "no dialogue in this cut: nobody speaks, no off-screen voice and no lip movement"
	}
	var parts []string
	for _, l := range lines {
		head := strings.TrimSpace(l.speaker)
		if head == "" {
			head = "旁白"
		}
		if l.os {
			head += " says off-screen in " + emptyBodyNeutralVoice
		} else {
			head += " says in " + emptyBodyNeutralVoice
		}
		parts = append(parts, fmt.Sprintf("%s: <d>[Chinese]%s</d>", head, l.text))
	}
	return "these lines are spoken once each, word for word: " + strings.Join(parts, " ")
}

// distributeLines 把台词按顺序摊到各刀上（每刀一句为上限；多出来的句子留给**最后一刀**，
// 因为最后一刀用它自己的时间补足，句子挤在一起会让语速离谱）。
func distributeLines(lines []emptyBodyLine, n int) [][]emptyBodyLine {
	out := make([][]emptyBodyLine, n)
	if n <= 0 {
		return out
	}
	for i, l := range lines {
		k := i
		if k >= n {
			k = n - 1
		}
		out[k] = append(out[k], l)
	}
	return out
}

// minimalCuts 按片段时长给出最小内切结构（每刀 1.5–4 秒、各刀之和 = 片段时长）：
//
//	4 秒以上至少 2 刀（与 `PlanOneTakeSeconds` 那条自纠判据同口径），最多 `CutPlanMaxCuts` 刀。
//
// 切点是**均分**的 —— 均分不引入任何创作信息（不编动作），而"这一镜要切几刀"是硬要求。
func minimalCuts(d int) []Cut {
	if d <= 0 {
		return []Cut{{At: 0}}
	}
	if d < PlanOneTakeSeconds {
		return []Cut{{At: 0}}
	}
	n := int(math.Ceil(float64(d) / CutPlanMaxSeconds))
	if n < CutPlanMinCuts {
		n = CutPlanMinCuts
	}
	if n > CutPlanMaxCuts {
		n = CutPlanMaxCuts
	}
	cuts := make([]Cut, 0, n)
	for i := 0; i < n; i++ {
		at := math.Round(float64(d)*float64(i)/float64(n)*1000) / 1000
		cuts = append(cuts, Cut{At: at})
	}
	return cuts
}

// cutStamp 官方写法 `At MM:SS.mmm`（毫秒恰好三位；不足三位会被判成"没写时间戳"）。
func cutStamp(at float64) string {
	if at < 0 {
		at = 0
	}
	whole := int(at)
	frac := int(math.Round((at - float64(whole)) * 1000))
	if frac >= 1000 { // 浮点进位（3.9996 秒这类）
		whole++
		frac -= 1000
	}
	return fmt.Sprintf("At %02d:%02d.%03d,", whole/60, whole%60, frac)
}

// styleSentence 风格句：优先取**同集兄弟镜**正文里那句（那样这一镜与其它镜是同一套画风），
// 其次用方案自己的 `style`，都没有才落一句中性的兜底。
//
// 为什么要有兜底：官方格式要求 `detailed_description` 的第一个 `[Shot` **之前**必须有内容
// （写进首镜等于只有那一镜是这部片的画风），空着会被 ③ 那条门咬。
func styleSentence(p *Plan, sh *Shot) string {
	for i := range p.Shots {
		if sh != nil && p.Shots[i].ID == sh.ID {
			continue
		}
		if s, ok := StyleSentenceOf(p.Shots[i].H3Prompt); ok {
			return s
		}
	}
	if s := strings.TrimSpace(p.Style); s != "" {
		return s
	}
	return "Cinematic look with natural colour and consistent lighting across the cut."
}

// soundscapeLine 环境音段（一段连续散文，官方上限 4 句）。
func soundscapeLine(subs []emptyBodySubject, hasScene bool) string {
	place := "the room"
	if hasScene {
		place = "the location"
	}
	if len(subs) > 0 {
		return "Ambient tone of " + place + " plus light cloth movement; " +
			"no music, and no voice other than the lines above."
	}
	return "Ambient tone of " + place + "; no music, and no voice other than the lines above."
}

// ---------- 材料读取（都只读方案自己，缺就不写，不猜） ----------

// sceneMaterial 按 id / 名字找场景卡；只认到 id 也算有材料（参考图仍然挂着）。
func sceneMaterial(p *Plan, sh *Shot) (name, desc string, ok bool) {
	key := strings.TrimSpace(sh.Scene)
	if key == "" {
		return "", "", false
	}
	for i := range p.Scenes {
		sc := p.Scenes[i]
		if strings.EqualFold(sc.ID, key) || (sc.Name != "" && sc.Name == key) {
			n := strings.TrimSpace(sc.Name)
			if n == "" {
				n = key
			}
			return n, firstClause(strings.TrimSpace(sc.Description), emptyBodyDescRunes), true
		}
	}
	return key, "", true
}

// charMaterial 按 id / 名字找角色卡（存量方案里有按名字写的引用）。
func charMaterial(p *Plan, key string) (Character, bool) {
	k := strings.TrimSpace(key)
	if k == "" {
		return Character{}, false
	}
	for i := range p.Characters {
		c := p.Characters[i]
		if strings.EqualFold(c.ID, k) || (c.Name != "" && c.Name == k) {
			return c, true
		}
	}
	return Character{}, false
}

// posOf 这一镜里这个人的走位（`positions` 的键可能是 id、也可能是名字）。
func posOf(sh *Shot, c Character) string {
	for _, k := range []string{c.ID, c.Name} {
		if k == "" {
			continue
		}
		if v := strings.TrimSpace(sh.Positions[k]); v != "" {
			return v
		}
	}
	return ""
}

// shotSpeechLines 这一镜要说的每一句（台词在前，旁白最后 —— 与 `plannedLineTexts` 同序）。
func shotSpeechLines(sh *Shot) []emptyBodyLine {
	var out []emptyBodyLine
	for _, l := range sh.Dialogue {
		if t := strings.TrimSpace(l.Text); t != "" {
			out = append(out, emptyBodyLine{speaker: strings.TrimSpace(l.Speaker), text: t, os: l.OS})
		}
	}
	if n := strings.TrimSpace(sh.Narration); n != "" {
		out = append(out, emptyBodyLine{speaker: "旁白", text: n, os: true})
	}
	return out
}

// firstClause 取第一句（。．.；;\n 处断开）并掐到 max 个字符，超出时留省略号。
func firstClause(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	cut := len(s)
	for _, sep := range []string{"。", "．", ".", "；", ";", "\n"} {
		if i := strings.Index(s, sep); i >= 2 && i+len(sep) < cut {
			cut = i + len(sep)
		}
	}
	s = strings.TrimSpace(s[:cut])
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimRight(string(r[:max]), " ,，、;；") + "…"
}

// sentenceDot 给一句话收尾：已经在 `。．.!！?？` 收尾的不再补点（避免「。.」这种双标点）。
func sentenceDot(s string) string {
	t := strings.TrimRight(strings.TrimSpace(s), " \t")
	if t == "" {
		return ""
	}
	r := []rune(t)
	switch r[len(r)-1] {
	case '。', '．', '.', '!', '！', '?', '？':
		return t
	}
	return t + "."
}

// englishOnly 只放行**含零个汉字**的材料（首尾空白修掉）：汉字出现在 `<d>` 外会被入口的
// 汉字卫生门计数（>200 即拒收），所以兜底正文的每一段材料都要先过这一关。
func englishOnly(s string) string {
	t := strings.TrimSpace(s)
	if t == "" || CountCJK(t) > 0 {
		return ""
	}
	return t
}

// shotSizeEN 平台自己的景别词表（`PlanShotSizes`，六个值）→ 英文写法。
//
// 为什么可以"翻"：这是**封闭词表**，不是自由文本 —— 六个值一一对应，没有解释空间；
// 而景别是每镜必说的信息，汉字又进不了正文（见 englishOnly）。
// 词表若新增一项而这里没跟上，`TestShotSizeLabelsCoverTheVocabulary` 会红。
var shotSizeEN = map[string]string{
	"远景":  "Extreme wide shot",
	"全景":  "Wide shot",
	"中景":  "Medium shot",
	"中近景": "Medium close shot",
	"近景":  "Close shot",
	"特写":  "Close-up",
}

// shotSizeLabel 景别的中文值 → 英文写法；词表外的值一律返回空（宁可不说，也不编一个）。
func shotSizeLabel(s string) string {
	return shotSizeEN[strings.TrimSpace(s)]
}

// sceneByID 按 id / 名字取场景卡（找不着返回 nil）。
func sceneByID(p *Plan, key string) *Scene {
	if p == nil {
		return nil
	}
	k := strings.TrimSpace(key)
	if k == "" {
		return nil
	}
	for i := range p.Scenes {
		sc := &p.Scenes[i]
		if strings.EqualFold(sc.ID, k) || (sc.Name != "" && sc.Name == k) {
			return sc
		}
	}
	return nil
}

// englishClause 取一段**能进正文**的外形描述：优先方案的描述，它含汉字时退到英文定妆提示词，
// 两者都拿不到（或都含汉字）就返回空（宁可不说，也不把中文塞进正文 —— 那是拒收级问题）。
func englishClause(desc, fallback string) string {
	if d := firstClause(strings.TrimSpace(desc), emptyBodyDescRunes); d != "" && CountCJK(d) == 0 {
		return d
	}
	if f := firstClause(strings.TrimSpace(fallback), emptyBodyDescRunes); f != "" && CountCJK(f) == 0 {
		return f
	}
	return ""
}

// sceneImagePrompt 场景卡的英文定妆提示词（找不着返回空串）。
func sceneImagePrompt(p *Plan, key string) string {
	if sc := sceneByID(p, key); sc != nil {
		return sc.ImagePrompt
	}
	return ""
}

// posENOf 这一镜这个人的**英文**走位（专职翻译会话补的 `positions_en`；没有就返回空串）。
// 键可能是 id、也可能是名字（与 `positions` 同一套）。
func posENOf(sh *Shot, c Character) string {
	if sh == nil || len(sh.PositionsEN) == 0 {
		return ""
	}
	for _, k := range []string{c.ID, c.Name} {
		if k == "" {
			continue
		}
		if v := strings.TrimSpace(sh.PositionsEN[k]); v != "" {
			return v
		}
	}
	return ""
}

// actionOf 这一镜动作的**英文**写法：英文版（翻译会话补的 `action_en`）优先，其次原字段（本来就是英文时）。
func actionOf(sh *Shot) string {
	if sh == nil {
		return ""
	}
	if a := englishOnly(sh.ActionEN); a != "" {
		return a
	}
	return englishOnly(sh.Action)
}

// MissingBodyMaterial 这一镜**缺哪些**拼正文必需的材料（空 = 材料够）。
//
// 判据与 `assembleMinimalBody` 的可用性判据**同源**：场次、出场人物、要念的台词，三者一个都没有
// 才拼不出来（那时平台替它写正文就等于替编剧新编一镜）。渲染入口的致命门用它把
// "为什么平台没兜这一手"讲清楚（此前只说"重跑剧本分镜补上这一镜"，用户不知道该补什么）。
func MissingBodyMaterial(p *Plan, sh *Shot) []string {
	if sh == nil {
		return []string{"镜头本身"}
	}
	var miss []string
	if _, _, ok := sceneMaterial(p, sh); !ok {
		miss = append(miss, "场次")
	}
	chars := 0
	for _, cid := range sh.Characters {
		if c, ok := charMaterial(p, cid); ok && strings.TrimSpace(c.Name) != "" {
			chars++
		}
	}
	if chars == 0 {
		miss = append(miss, "出场人物")
	}
	if len(shotSpeechLines(sh)) == 0 {
		miss = append(miss, "台词/旁白")
	}
	return miss
}
