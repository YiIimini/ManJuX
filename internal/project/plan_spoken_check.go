package project

import (
	"fmt"
	"strings"
)

// plan_spoken_check.go 分镜里**说出去的话**与**显示出来的字**必须对得上。
//
// 【为什么必须有一条门（2026-10-07，用户报「配音存在乱配音」）】
// 这两件事在平台里是**两条独立通道**：
//
//	· 声音 —— H3 只念 h3_prompt 里 `<d>[Chinese]…</d>` 的内容；
//	· 字幕 —— 合片时按 `dialogue[].text` 烧字（internal/compose 的字幕链路读的就是它）。
//
// 于是只要模型在 `<d>` 里改了词、漏一句、多写一句，观众看到的就是"**听到的跟写着的不一样**"
// —— 那是最直观的"乱配音"。此前只有一条门管到这一边："有台词却连一个 `<d>` 都没有"（fail），
// 而**逐句对账**没有任何判据（真机第01集恰好一致，纯属运气）。
//
// 判据：逐镜比"计划里该说的每一句"（`dialogue[].text` + `narration`）与"正文里真的说出去的"
// （`<d>` 内容；语言标记、占位、`[unclear]`、标点都不敏感 —— 官方要求标点归一，比标点会把
// 同一句判成不一样）。
//
//	· 句数对不上 ⇒ fail：平台不知道该往哪一句上改，只能让模型重写；
//	· 句数对上、文本不同 ⇒ fail：同一个位置两句不一样 —— 观众听到的与屏幕上的字不同；
//	· 只有**顺序**不同 ⇒ warn：字幕按 dialogue 顺序显示，会与听到的顺序错位。
//
// 另外顺带一条自洽性检查：`vo_kind=speech`（画面里有人在说）却一句台词都没有的镜 ⇒ warn。
// 真机第01集第 12 镜就是这样（7.3 秒里没有任何 `<d>`）—— H3 收到"有人在说"却无话可说，
// 会自己造人声或灌环境声，而字幕那边一条都没有。
func checkSpokenTextMatchesDialogue(p *Plan, add func(level, where, format string, a ...any)) {
	var countBad, textBad, orderBad, muteSpeech, extraSpoken []string
	for i := range p.Shots {
		sh := &p.Shots[i]
		if strings.TrimSpace(sh.H3Prompt) == "" {
			continue // 空正文由"空提示词"那道门管
		}
		planned := plannedSpokenLines(sh)
		spoken := spokenTagTexts(sh.H3Prompt)
		if len(planned) == 0 {
			// 计划里没有该说的话：正文里**也不该有** `<d>`。
			if len(spoken) > 0 {
				// 正文里说了话，dialogue/narration 里却没登记 ⇒ 字幕里没有这一句
				//（观众听到一句"没有字幕的话"）。平台不知道该把它挂给谁 ⇒ 交给模型改。
				extraSpoken = append(extraSpoken, fmt.Sprintf("#%d(%d 句)", sh.ID, len(spoken)))
			} else if strings.TrimSpace(sh.VoKind) == VoKindSpeech {
				// 标着"画面里有人在说"，却一个字的台词都没有。
				muteSpeech = append(muteSpeech, fmt.Sprintf("#%d", sh.ID))
			}
			continue
		}
		if len(planned) != len(spoken) {
			countBad = append(countBad, fmt.Sprintf("#%d(计划 %d 句 / 正文 %d 句)", sh.ID, len(planned), len(spoken)))
			continue
		}
		same := true
		for j := range planned {
			if planned[j] != spoken[j] {
				same = false
				textBad = append(textBad, fmt.Sprintf("#%d 第 %d 句（计划「%s」/ 正文「%s」）",
					sh.ID, j+1, clipRunes(planned[j], 16), clipRunes(spoken[j], 16)))
				break
			}
		}
		if same {
			continue
		}
		if sameMultiset(planned, spoken) {
			orderBad = append(orderBad, fmt.Sprintf("#%d", sh.ID))
		}
	}
	if len(countBad) > 0 {
		add("fail", "plan.shots",
			"%d 镜的**台词句数与正文里的 `<d>` 句数不一致**（%s）—— 声音念的是 `<d>` 里的内容、"+
				"字幕烧的是 dialogue 字段：两者句数不同，成片里必然有一句**只有字幕没有声音**"+
				"（或反过来）。平台补不了（不知道该往哪一句上改）—— 请让这几镜的 `<d>` 逐句对上 dialogue",
			len(countBad), strings.Join(firstN(countBad, 4), "、"))
	}
	if len(textBad) > 0 {
		// **warn 而不是 fail**：句数相等而文本不同时，平台能确定性地按顺序对齐
		//（AlignDialogueTags，在唯一读取边界上做，以 dialogue 为权威）——
		// 成片不会"听到的与写着的不一样"；这一条是给**产出侧**的提醒（自己写对更稳）。
		add("warn", "plan.shots",
			"%d 镜的**台词文本与 dialogue 不一致**（%s）—— 同一个位置，`<d>` 里写的与 dialogue 里写的不是一句："+
				"声音念 `<d>`、字幕烧 dialogue，不一致时观众听到的与屏幕上的字就不同。"+
				"平台会在读取时**按顺序对齐到 dialogue 原文**（这一步不需要你做什么）；"+
				"产出侧直接写对更稳（标点可归一，词不能改）",
			len(textBad), strings.Join(firstN(textBad, 4), "；"))
	}
	if len(orderBad) > 0 {
		add("warn", "plan.shots",
			"%d 镜的台词**顺序**与 dialogue 不一致（%s）—— 字句都对、只是先后不同："+
				"字幕按 dialogue 的顺序逐条显示，观众会看到「话还没说，字先出来了」",
			len(orderBad), strings.Join(firstN(orderBad, 6), "、"))
	}
	if len(muteSpeech) > 0 {
		add("warn", "plan.shots",
			"%d 镜标着「画面里有人在说」(vo_kind=speech) 却一句台词都没有（%s）——「有人在说」是给 H3 的"+
				"指令（它会让画面里的人动嘴、并自己填人声），而这镜既没有 `<d>` 也没有 dialogue："+
				"成片里那几秒多半是模型自造的人声或噪声。没有台词的镜不该标 speech",
			len(muteSpeech), strings.Join(firstN(muteSpeech, 6), "、"))
	}
	if len(extraSpoken) > 0 {
		add("warn", "plan.shots",
			"%d 镜的正文里**说了计划里没有登记的话**（%s）—— 字幕只按 dialogue/narration 烧字："+
				"这几句观众**听得到、看不到**（而平台不知道该把它挂给谁，补不了）。"+
				"改法：把这几句补进 dialogue（说话人 + 原文），或从 `<d>` 里删掉",
			len(extraSpoken), strings.Join(firstN(extraSpoken, 4), "、"))
	}
}

// AlignDialogueTags 把每一镜 `<d>` 里的文本**对齐到 dialogue 的原文**（按顺序一一对应）。
//
// 【为什么要平台做（2026-10-07 用户「配音存在乱配音」）】
// 声音与字幕是**两条独立通道**：H3 只念 `<d>[Chinese]…</d>` 的内容，合片时字幕烧的是
// `dialogue[].text`。模型在 `<d>` 里改一个词，观众就是"**听到的跟写着的不一样**"。
// 而这件事的答案是**已知量**：句数相等时逐句按顺序对应，文本以 dialogue 为准
// （它同时是字幕源、也是平台补缺失 `<d>` 时的来源，见 FillMissingDialogueTags）。
// 句数不等时**不猜** —— 那是"少说了一句/多说了一句"，由契约门报 fail 让模型重写。
//
// 归一化后已经一致的不动（幂等）；语言标记（`[Chinese]`）与标签保持原样，只换内容。
// 返回改了几镜。
func AlignDialogueTags(plan *Plan) int {
	if plan == nil {
		return 0
	}
	n := 0
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if strings.TrimSpace(sh.H3Prompt) == "" {
			continue
		}
		planned := plannedLineTexts(sh)
		tags := h3DialogueRe.FindAllStringSubmatchIndex(sh.H3Prompt, -1)
		var spoken []string
		for _, m := range tags {
			spoken = append(spoken, spokenTagText(normalizedTagBody(sh.H3Prompt[m[2]:m[3]])))
		}
		if len(planned) == 0 || len(planned) != len(spoken) {
			continue
		}
		changed := false
		// 从后往前改，前面的下标不受影响。
		for j := len(tags) - 1; j >= 0; j-- {
			if spoken[j] == normSpokenText(planned[j]) {
				continue
			}
			m := tags[j]
			body := sh.H3Prompt[m[2]:m[3]]
			lang := ""
			if strings.HasPrefix(strings.TrimSpace(body), "[") {
				if k := strings.Index(body, "]"); k > 0 {
					lang = strings.TrimSpace(body[:k+1])
				}
			}
			repl := lang + planned[j]
			sh.H3Prompt = sh.H3Prompt[:m[2]] + repl + sh.H3Prompt[m[3]:]
			changed = true
		}
		if changed {
			n++
		}
	}
	return n
}

// plannedLineTexts 这一镜**计划里的原句**（按顺序；旁白排最后）—— 对齐时用它做权威文本。
func plannedLineTexts(sh *Shot) []string {
	var out []string
	for _, l := range sh.Dialogue {
		if t := strings.TrimSpace(l.Text); t != "" {
			out = append(out, t)
		}
	}
	if t := strings.TrimSpace(sh.Narration); t != "" {
		out = append(out, t)
	}
	return out
}

// normalizedTagBody 去掉语言标签与 [unclear] 之后的标签内容（对齐与对账共用）。
func normalizedTagBody(body string) string {
	t := strings.TrimSpace(body)
	if strings.HasPrefix(t, "[") {
		if i := strings.Index(t, "]"); i > 0 {
			t = strings.TrimSpace(t[i+1:])
		}
	}
	return strings.ReplaceAll(t, "[unclear]", "")
}

// spokenTagText 标签内容归一化；占位（`…`/`（无）`）算"没说"（空串）。
func spokenTagText(body string) string {
	if isPlaceholderDialogue(body) {
		return ""
	}
	return normSpokenText(body)
}

// plannedSpokenLines 这一镜**计划里该说出去的每一句**（归一化，按对话顺序；旁白排最后）。
func plannedSpokenLines(sh *Shot) []string {
	var out []string
	for _, l := range sh.Dialogue {
		if t := normSpokenText(strings.TrimSpace(l.Text)); t != "" {
			out = append(out, t)
		}
	}
	if n := normSpokenText(strings.TrimSpace(sh.Narration)); n != "" {
		out = append(out, n)
	}
	return out
}

// spokenTagTexts 正文里**真的说出去的话**（`<d>` 内容；去掉语言标记与 `[unclear]` 后归一化；
// 空标签 / 占位（`…`、`（无）`）/ 规则文本里的示例标签都不算）。
func spokenTagTexts(h3 string) []string {
	var out []string
	for _, m := range h3DialogueRe.FindAllStringSubmatch(h3, -1) {
		body := strings.TrimSpace(m[1])
		if strings.HasPrefix(body, "[") {
			if i := strings.Index(body, "]"); i > 0 {
				body = strings.TrimSpace(body[i+1:])
			}
		}
		if isPlaceholderDialogue(body) {
			continue
		}
		body = strings.ReplaceAll(body, "[unclear]", "")
		if t := normSpokenText(body); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// sameMultiset 两组归一化台词是不是"同一批句子、只是顺序不同"。
func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, x := range a {
		found := false
		for j := range b {
			if !used[j] && b[j] == x {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// clipRunes 截断到 n 个字符（门里的消息要能一眼看懂，别把整句贴满日志）。
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
