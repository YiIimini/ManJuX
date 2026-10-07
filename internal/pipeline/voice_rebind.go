package pipeline

import (
	"regexp"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// voice_rebind.go 把**每一句台词的声线**绑成"唯一权威文本"（角色卡 voice 逐字 / 平台默认）。
//
// 【为什么必须做第二次（2026-10-07，用户报「配音存在乱配音」）】
//
// 声线锚定在 2026-09-25 就做过了（见 prompts.go 的【声线(逐字照抄,不得改写)】块），
// 但那次只是**在提示词里写了一条要求**；真正的绑定点（把音色写进每句台词的引述里）
// 只处理了"字面占位符 `[voice description]`"这一种情形。于是模型自己写出来的**改写版**
// （近义换词）一个字都没被纠正 —— 而 H3 是把这段文字当**音频条件**用的，
// 平台自己的官方格式文件写得很直白：
//
//	"Establish the voice once, at that speaker's first appearance, as a short identifying
//	 phrase placed BEFORE <d> … That phrase MUST be copied verbatim from that character's
//	 "voice" field in the cast list: never reword it … (H3 uses this text as audio
//	 conditioning — rewording it changes the timbre)."
//	                                    —— sysH3Format__H3_PROMPT_FORMAT_CRITICAL.md §4.4
//
// 真机取证（第01集，13 镜）：角色卡上祁七的 voice 是
//
//	"A cool, even, slightly hollow young female voice, measured and clipped, each word read
//	 out flatly like a calibration readout rather than a feeling."
//
// 而 11 句台词的引述里写的是**11 种不同的改写**（卡片原文一次都没出现）：
//
//	shot2  She asks, in a clear, cool young voice with a slight mechanical edge (S2) says:
//	shot5  … and speaks with 祁七's own voice, off-screen: the android girl with a clear,
//	       cool young voice and a slight mechanical edge (S2) says:
//	shot8  She says, without turning her head, in the same clear, cool young voice …
//	shot13 … speaking in 祁七's own voice, cool, slightly mechanical (S2) says:
//
// 同一个 AI（舱内AI）也一样，每镜现编："Off-screen speaker voice (S1), flat, sexless and
// synthetic" / "a flat, sexless synthetic voice, even-paced and breathless" / "flat sexless
// synthetic" …… 而它是**没有角色卡的说话人**，`EnsureVoiceInline` 连一份音色清单都不给它
// （真机 shot 1 的 VOICE CASTING 块直接缺席）。
//
// 结论与平台一贯口径一致（AGENTS.md「能确定性做到的事，不要让模型自觉」）：
// 声线**不能让模型每镜现写**，要在装配点用平台自己知道的量（角色卡 voice / 说话人名）
// 确定性绑定，并如实播报绑了几处。
//
// 判据只用确定性信息：<d> 里的台词文本 → 方案里的说话人 → 卡片 voice，或平台默认。

// 平台默认声线（无角色卡时的**唯一**来源）。措辞取真机里模型自己反复写的那一句 ——
// 与既有产出保持一致，改动面最小（真机里模型自己在多镜写的就是这一句）。
const (
	syntheticVoiceDefault = "a flat, sexless synthetic voice, even-paced and breathless"
	genericVoiceDefault   = "a natural, clearly articulated Mandarin voice, steady and even"
)

// 一句台词标签；语言标签（[Chinese]/[English]…）不算台词内容。
var (
	reDialogueTag = regexp.MustCompile(`<d>([^<]*)</d>`)
	reLangTag     = regexp.MustCompile(`^\s*\[\s*[A-Za-z]+\s*\]\s*`)
	// 引述末尾的说话动词 + 冒号（"… (S2) says: "）—— 重绑时连它一起换掉。
	reSayTail      = regexp.MustCompile(`(?i)[\s,;、，]*(says|say|asked|asks|replied|replies|answered|answers|shouts|murmurs|whispers|adds|continues|sings|spoke|speaks|speaking)\s*[:：]\s*$`)
	reHeadTailVerb = regexp.MustCompile(`(?i)[\s,]*(and\s+)?(she|he|they|it|the\s+[a-z]+|[a-z]+)\s+(says|say|asked|asks|speaks|speaking|whispers|murmurs)\s*[,;]\s*$`)
	// 引述头**开头**的说话动词从句（"She says, without turning her head, " → 去掉前半句）。
	reVoiceVerbHead = regexp.MustCompile(`(?i)^\s*(and\s+)?(she|he|they|it|the\s+[a-z]+|[a-z]+)\s+(says|say|asked|asks|speaks|speaking|whispers|murmurs)\s*,\s*`)
	// 说话人 id（官方 §4.4：一个说话人跨镜沿用同一个 (Sx)）。
	reSpeakerID = regexp.MustCompile(`\(S\d+(?:\s*,\s*S\d+)*\)`)
	// "这一段像声线子句"的判据：出现 voice/timbre/说话动词/off-screen 之类。
	reVoiceish = regexp.MustCompile(`(?i)(voice|voices|vocal|timbre|tone|says|say|asks|speaks|speaking|voiceover|off-?screen|narrat|breath|whisper)`)
	// 引述子句的切分：逗号，或**冒号 + 空格**（"Her lips part: a clear, cool young voice…" 要从冒号处切开）。
	// 冒号必须要求后面有空格 —— 时刻 "00:02.700" 里的冒号不该切开句子。
	rePauseSplit = regexp.MustCompile(`(?:,\s*|:\s+)`)
)

// voiceClauseWords 声线子句的**词汇表**：左扩（把改写版整段吃掉）时逐词判断。
//
// 命中表外的实词就停下 —— 那些是画面描述（nameplate/palm/chin/lifts/crouches…），一个字都不许删。
// 表里既收"声线形容词"，也收"谁在说话/什么音源"这类**说话人描述词**
// （the android girl with a clear, cool young voice / the bay's overhead speaker voice）——
// 它们属于引述的一部分，重绑时一起换掉才对（换掉后由平台子句重新写明说话人与音色）。
var voiceClauseWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
		in a an the with and then or as of to at from is are was were her his their its it
		she he they his her own same all both very quite just still so but
		voice voices vocal timbre tone says say saying asked asks speaks speaking spoke
		whisper whispers murmured murmurs breath breathy
		off on screen over voiceover narration narrator
		clear cool warm cold soft hard deep flat even low high bright dry thin hollow
		measured clipped quiet steady smooth natural neutral calm crisp harsh light heavy
		quick slow slight slightly faint small tiny mechanical robotic android synthetic
		digital metallic young old gendered female male feminine masculine genderless sexless
		flatly evenly paced measured unhurried clipped
		girl boy man woman lady figure self version speaker overhead bay announcer
		computer machine broadcast terminal system robot ai q
		breathless raspy husky gravelly lilting melodic monotone singsong terse spoken
	`) {
		m[w] = true
	}
	return m
}()

// voiceFor 这个说话人该用的**权威声线**：优先角色卡上的 voice（逐字），否则平台默认。
//
// 平台默认也必须有：真机那个「舱内AI」不是角色卡（全片反复出现的说话人），
// 每镜现编的结果是同一个 AI 在 13 镜里有 4 种措辞 —— 正是"乱配音"的一种。
func voiceFor(plan *project.Plan, speaker string) (string, bool) {
	name := strings.TrimSpace(speaker)
	if name == "" {
		return "", false
	}
	if v, ok := cardVoiceOf(plan, name); ok {
		return v, true
	}
	if d := defaultVoiceFor(name); d != "" {
		return d, true
	}
	return "", false
}

// cardVoiceOf 按说话人名找角色卡的 voice（名/去括注名/族基名几种写法都认，与
// voiceNameKeys 同一口径 —— 卡片名常带造型档后缀）。
func cardVoiceOf(plan *project.Plan, speaker string) (string, bool) {
	if plan == nil {
		return "", false
	}
	want := map[string]bool{}
	for _, k := range voiceNameKeys(speaker) {
		want[strings.ToLower(strings.TrimSpace(k))] = true
	}
	for i := range plan.Characters {
		c := plan.Characters[i]
		v := strings.TrimSpace(c.Voice)
		if v == "" {
			continue
		}
		if want[strings.ToLower(strings.TrimSpace(c.Name))] {
			return v, true
		}
		for _, k := range voiceNameKeys(c.Name) {
			if want[strings.ToLower(strings.TrimSpace(k))] {
				return v, true
			}
		}
	}
	return "", false
}

// defaultVoiceFor 没有角色卡的说话人的**平台默认声线**（跨镜、跨集同一个值）。
//
// 判据按"这是什么音源"排优先级：
//
//	系统音（舱内AI/主控/广播/终端/面板…）→ 合成声默认。**必须先判它**：
//	  「舱内AI」同时命中 IsVoiceoverSpeaker（名字里有 AI），若先走旁白那条，一个舱内电子音
//	  会被配成"warm, low-pitched adult male narrator"（真机实测第一次重绑就是这个结果 ——
//	  比原来更糟，所以顺序在这里是有语义的，不是风格问题）。
//	旁白/画外音/内心 → 旁白默认（narratorVoiceDefault，与既有旁白句同一句）。
//	其余（认不出的说话人，如「家丁」「路人」）→ 中性默认。
//
// 认不出来也给默认：**宁可用一句通用声线，也不要让模型每镜现编**（那才是音色漂移的来源）。
func defaultVoiceFor(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return ""
	}
	if project.IsSystemVoiceSpeaker(n) {
		return syntheticVoiceDefault
	}
	if project.IsVoiceoverSpeaker(n) {
		return narratorVoiceDefault
	}
	// 平台词表没覆盖到的英文写法（技能侧包可能写 "Bay Computer"/"Station AI"）。
	low := strings.ToLower(n)
	for _, kw := range []string{"ai", "system", "computer", "robot", "machine", "broadcast", "speaker", "terminal", "voice-over", "voiceover", "synthetic"} {
		if strings.Contains(low, kw) {
			return syntheticVoiceDefault
		}
	}
	// 认不出的说话人（家丁甲/路人乙…）：**按名字派生**一条池子里的声线，不是所有人共用一句。
	// 【2026-10-07 真机复核】此前这里返回常数 genericVoiceDefault ⇒ 无卡说话人**同一个嗓子**
	// （家丁甲与家丁乙、路人丙，一个声音）；与"角色卡缺 voice"那条撞声通路同源。
	// 派生是确定性的：同名永远同一条（跨镜、跨集），不同名不同条（见 project.DerivedVoice）。
	if v := project.DerivedVoice(n, ""); v != "" {
		return v
	}
	return genericVoiceDefault
}

// ShotCasting 本镜台词会用到的**全部音色**（有卡的用卡片原文，没有卡的用平台默认）。
//
// 与 ShotSpeakers 的差别只有一条：**无卡说话人也进清单**。真机 shot 1 里说话的只有
// 「舱内AI」（不是角色卡），于是那份 casting 块**整段缺席** —— 一个正在说话的人没有任何
// 音色约束，模型只能自己编。（ShotSpeakers 保持原样：它的契约就是"有卡的人"。）
func ShotCasting(plan *project.Plan, shot *project.Shot) []project.Character {
	if plan == nil || shot == nil {
		return nil
	}
	out := ShotSpeakers(plan, shot)
	seen := map[string]bool{}
	for _, c := range out {
		seen[strings.ToLower(strings.TrimSpace(c.Name))] = true
	}
	var extra []project.Character
	for _, l := range shot.Dialogue {
		name := strings.TrimSpace(l.Speaker)
		if name == "" || strings.TrimSpace(l.Text) == "" {
			continue
		}
		low := strings.ToLower(name)
		if seen[low] {
			continue
		}
		v, ok := voiceFor(plan, name)
		if !ok {
			continue
		}
		seen[low] = true
		extra = append(extra, project.Character{Name: name, Voice: v})
	}
	return append(out, extra...)
}

// BindLineVoices 把每一句 <d> 台词的声线**绑成权威文本**（角色卡 voice 逐字 / 平台默认）。
//
// 返回 (新提示词, 重绑句数, 判不出而保留原样的句数)。
// 判据链：<d> 里的台词文本 → 方案里的说话人（逐字对上）→ 卡片 voice 或平台默认；
// 任何一步对不上就**不动那一句**（宁可留着让模型自由发挥，也不要塞错人的音色），并计入 unresolved。
//
// 幂等：绑过之后引述里已经含那句权威声线 ⇒ 再调用一次不再改写（bound=0）。
func BindLineVoices(prompt string, plan *project.Plan, shot *project.Shot) (string, int, int) {
	if shot == nil || len(shot.Dialogue) == 0 || !strings.Contains(prompt, "<d>") {
		return prompt, 0, 0
	}
	speaker := speakerByLine(shot)
	out := prompt
	bound, unresolved := 0, 0
	ms := reDialogueTag.FindAllStringSubmatchIndex(out, -1)
	// 从后往前改：前面的下标不受后面改写影响。
	for i := len(ms) - 1; i >= 0; i-- {
		content := out[ms[i][2]:ms[i][3]]
		if !hasSpokenText(content) {
			continue // 空标签 / 省略号 / 「无」这类占位，以及规则文本里的示例标签
		}
		sp := speaker[normSpoken(content)]
		if strings.TrimSpace(sp) == "" {
			unresolved++
			continue
		}
		voice, ok := voiceFor(plan, sp)
		if !ok {
			unresolved++
			continue
		}
		clause := voiceClause(sp, voice, offScreenFor(plan, shot, sp))
		next, changed := replaceVoiceClause(out, ms[i][0], voice, clause)
		if changed {
			bound++
		}
		out = next
	}
	return out, bound, unresolved
}

// speakerByLine 归一化台词文本 → 说话人名（<d> 与 dialogue 逐字对上时才算认得出）。
func speakerByLine(shot *project.Shot) map[string]string {
	m := map[string]string{}
	for _, l := range shot.Dialogue {
		key := normSpoken(l.Text)
		if key == "" || strings.TrimSpace(l.Speaker) == "" {
			continue
		}
		if _, dup := m[key]; !dup {
			m[key] = strings.TrimSpace(l.Speaker)
		}
	}
	return m
}

// offScreenFor 这句台词是不是**画外**（说的人不在画面里 / 整镜画外演出）。
//
// 用平台自己知道的量判：vo_kind / 画外说话人标签 / 说话人在不在本镜出场名单 ——
// **不看模型标的 os**：真机 13 镜里 11 镜把画面里的说话人（祁七）也标了 os=true，
// 那个字段不可信（见 project.OffScreenSpeakers 的说明）。
func offScreenFor(plan *project.Plan, shot *project.Shot, speaker string) bool {
	if shot == nil {
		return false
	}
	if project.IsVoiceoverSpeaker(speaker) || project.HasVoiceover(*shot) {
		return true
	}
	for _, s := range project.OffScreenSpeakers(plan, *shot) {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(speaker)) {
			return true
		}
	}
	return false
}

// voiceClause 一句台词的引述（官方句式：说话人 + (Sx) + 说话动词 + 音色）。
// voiceClause 一句台词的引述模板（官方句式：说话人 + (Sx) + 说话动词 + 音色）。
//
// `%ID%` 由 replaceVoiceClause 用**模型自己写的那个 (Sx)** 填 —— id 是官方用来把同一个
// 说话人跨镜串起来的（一个说话人跨镜沿用同一个 (Sx)），所以只保留、绝不新造。
func voiceClause(speaker, voice string, offScreen bool) string {
	name := strings.TrimSpace(speaker)
	if project.IsVoiceoverSpeaker(name) {
		if project.IsVoiceoverSpeaker(name) && !project.IsSystemVoiceSpeaker(name) {
			name = "the narrator" // 旁白用英文标签，与 narratorVoiceDefault 同源；系统音保留自己的说话人名
		}
	}
	if name == "" {
		name = "the speaker"
	}
	v := embedVoice(voice)
	if offScreen {
		return name + "%ID% says off-screen in " + v + ": "
	}
	return name + "%ID% says in " + v + ": "
}

// embedVoice 把卡片上的 voice 放进引述里：去掉换行；**吞掉末尾句号**（否则会与语法的冒号
// 撞成 ".："）。其余字符逐字保留。
func embedVoice(v string) string {
	s := strings.TrimSpace(strings.ReplaceAll(v, "\n", " "))
	s = strings.TrimRight(s, " .。")
	return s
}

// replaceVoiceClause 把 <d> 之前那段"模型自己写的声线子句"换成权威子句。返回是否真的改了。
//
// 切点规则（照真机语料定，13 句全覆盖）：
//  1. 先去掉引述末尾的说话动词 + 冒号（"… (S2) says:"），它由新子句重写；
//  2. 在**同一段**内找最后一个"像声线"的逗号段（含 (Sx) 或 voice/says/off-screen 之类）；
//  3. 往左扩：只要前一段的每个词都在声线词汇表里，就一起吃进来
//     （这样 "She asks, in a clear, cool young voice …" 整段都会被换掉，而不是留下 "in a clear,"）；
//  4. 扩到"画面描述"（nameplate/palm/chin lifts…）就停 —— 那些是画面内容，一个字都不许删。
func replaceVoiceClause(prompt string, dIdx int, voice, clause string) (string, bool) {
	lineStart := strings.LastIndex(prompt[:dIdx], "\n") + 1
	seg := prompt[lineStart:dIdx]
	if seg == "" {
		return prompt, false
	}
	// 幂等：这一句的引述已经是**平台子句的形状**（"… says in <权威声线>"）⇒ 不再动它。
	//
	// 判据必须看整句形状、不能只看"这句声线文本出现过"：真机里模型自己就写过
	// "…a flat, sexless synthetic voice, even-paced and breathless, off-screen (S1) says:"
	// —— 声线文本恰好与平台默认一致，但那仍是**模型写的**子句（措辞/语序每镜不同），
	// 只看文本会把该归一化的那一句放过去。
	win := normLoose(prompt[maxInt(0, dIdx-320):dIdx])
	if v := normLoose(embedVoice(voice)); strings.Contains(win, "saysin"+v) || strings.Contains(win, "saysoffscreenin"+v) {
		return prompt, false
	}
	trimmed := strings.TrimRight(seg, " \t")
	// ① 去尾部动词 + 冒号
	if loc := reSayTail.FindStringIndex(trimmed); loc != nil {
		trimmed = strings.TrimRight(trimmed[:loc[0]], " \t")
	}
	if trimmed == "" {
		return prompt, false
	}
	// ④ 句子边界（floor）：不跨句删东西
	floor := 0
	for _, sep := range []string{". ", "! ", "? ", "。", "！", "？"} {
		if i := strings.LastIndex(trimmed, sep); i >= 0 {
			if e := i + len(sep); e > floor && e < len(trimmed) {
				floor = e
			}
		}
	}
	body := trimmed[floor:]
	// ② 逗号段：从右往左找第一个"像声线"的段
	parts, starts := splitByPause(body)
	cut := -1
	for i := len(parts) - 1; i >= 0; i-- {
		joined := strings.Join(parts[i:], ", ")
		if reVoiceish.MatchString(joined) || reSpeakerID.MatchString(joined) {
			cut = i
			break
		}
	}
	if cut < 0 {
		// 整段都没声线痕迹：不猜（可能模型根本没写引述）
		return prompt, false
	}
	// ③ 往左扩（只吃"声线味"的段）
	for cut > 0 && allVoiceWords(parts[cut-1]) {
		cut--
	}
	cutAbs := floor + starts[cut]
	head := trimmed[:cutAbs]
	// 引述头里残留的说话动词：尾部（"…and she says, "）与**头部**（"She says, without turning
	// her head, "）都要清掉 —— 新子句自己带 "says in"，留着就会出现两个 says。
	head = stripVoiceVerbTail(head)
	head = stripVoiceVerbHead(head)
	// 模型自己写的说话人 id（(S2)）**保留**：官方靠它把同一个说话人跨镜串起来。
	idPart := ""
	if id := reSpeakerID.FindString(trimmed[cutAbs:]); id != "" {
		idPart = " " + id
	}
	final := strings.Replace(clause, "%ID%", idPart, 1)
	sep := ""
	if h := strings.TrimRight(head, " \t"); h != "" {
		if !strings.ContainsRune(",:;.!?,]）)。，、；：？！", rune(lastRune(h))) {
			sep = ", "
		}
	}
	newSeg := head + sep + final
	if newSeg == seg {
		return prompt, false
	}
	return prompt[:lineStart] + newSeg + prompt[dIdx:], true
}

// splitByPause 把一段话按"逗号 / 冒号+空格"切成片段，并返回每段在原文里的起点。
func splitByPause(s string) ([]string, []int) {
	var parts []string
	var starts []int
	idx := rePauseSplit.FindAllStringIndex(s, -1)
	pos := 0
	for _, m := range idx {
		parts = append(parts, s[pos:m[0]])
		starts = append(starts, pos)
		pos = m[1]
	}
	parts = append(parts, s[pos:])
	starts = append(starts, pos)
	return parts, starts
}

// stripVoiceVerbTail 去掉引述头**末尾**残留的说话动词（"…and she says, " → "…"）。
func stripVoiceVerbTail(head string) string {
	for {
		loc := reHeadTailVerb.FindStringIndex(head)
		if loc == nil || loc[1] != len(head) {
			return head
		}
		head = head[:loc[0]]
	}
}

// stripVoiceVerbHead 去掉引述头**开头**的说话动词从句（"She says, without turning her head, "
// → "without turning her head, "）：新子句自己带 "says in"，留着就会出现两个 says。
// 只在"动词后面还有别的话"时才动它（整段就是"… says," 的话由尾部那条处理）。
func stripVoiceVerbHead(head string) string {
	loc := reVoiceVerbHead.FindStringIndex(head)
	if loc == nil || loc[0] != 0 {
		return head
	}
	rest := head[loc[1]:]
	if strings.TrimSpace(rest) == "" {
		return head
	}
	return rest
}

// allVoiceWords 这一段是不是"声线子句"的一部分（每个词都在词汇表里才算）。
// 先把所有格 "'s"/"’s" 去掉再切词（"bay's overhead speaker voice" 要按 bay 认）。
func allVoiceWords(seg string) bool {
	seg = strings.ReplaceAll(strings.ReplaceAll(seg, "'s", ""), "’s", "")
	ws := strings.FieldsFunc(seg, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\'' || r == '’' || r == '-' || r == '/' || r == '(' || r == ')'
	})
	if len(ws) == 0 {
		return false
	}
	for _, w := range ws {
		w = strings.ToLower(strings.Trim(w, ",.;:!?()[]“”\"'’"))
		if w == "" {
			continue
		}
		// 纯汉字词一律放行：英文提示词里出现的汉字几乎只有**说话人名**
		//（"…and speaks with 祁七's own voice"），它属于引述的一部分；画面描述是英文写的。
		if isHanOnly(w) {
			continue
		}
		if !voiceClauseWords[w] {
			return false
		}
	}
	return true
}

// isHanOnly 这个词是不是纯汉字。
func isHanOnly(s string) bool {
	for _, r := range s {
		if r < 0x4E00 || r > 0x9FFF {
			return false
		}
	}
	return s != ""
}

// hasSpokenText 这个 <d> 标签里是不是**真有人在说话**（占位/省略号/「无」不算）。
func hasSpokenText(content string) bool {
	t := strings.TrimSpace(reLangTag.ReplaceAllString(strings.TrimSpace(content), ""))
	t = strings.Trim(t, "….。、,，!！?？;；:：-—~～()（）[]【】\"'“”‘’ \t")
	switch strings.ToLower(t) {
	case "", "无", "无台词", "无对白", "none", "n/a", "na":
		return false
	}
	return true
}

// normSpoken 台词文本归一化（只留汉字/字母/数字）：<d> 与 dialogue 常有一处标点差异。
func normSpoken(s string) string {
	t := strings.TrimSpace(reLangTag.ReplaceAllString(strings.TrimSpace(s), ""))
	var sb strings.Builder
	for _, r := range t {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			sb.WriteRune(r)
		case r >= 0x4E00 && r <= 0x9FFF:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// normLoose 幂等判据用的宽松归一化（去标点与空白，不改大小写）。
func normLoose(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			sb.WriteRune(r)
		case r >= 0x4E00 && r <= 0x9FFF:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func lastRune(s string) rune {
	if s == "" {
		return 0
	}
	r := []rune(s)
	return r[len(r)-1]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
