package project

import (
	"strings"
)

// prop_refs.go 分镜里 `prop_ids` 的**归一化与对号**（2026-09-28 实测驱动）。
//
// 【实测缺陷】真章节那一跑：道具卡老老实实出了 5 张（碑刀 / 已殁名册 / 旧拓布 / 丙十七的碑 /
// 碑号烙铁），分镜里也有 16 镜带了 `prop_ids` —— 但其中 **14 处指向方案里不存在的 id**
// （卡片叫一个拼法，分镜里写另一个拼法：`prop_beidao` vs 卡片 id）。后果与"没道具"一样：
// 装配时那件器物的锚定文本落空，这一镜的刀又变成模型自己编的 ⇒ 前后镜漂移。
//
// 【为什么由平台对号，而不是等模型写对】id 拼法是**机械约定**，不是创作内容。模型在同一轮里
// 既要写卡片 id、又要在几十镜里逐镜复述，拼错是必然；而平台手上有卡片的名字与英文串，
// 对号这件事它是唯一能确定做对的角色。做法保守：**名字/英文串/词根命中**才改，改不了就丢掉
// 这条引用（宁可不锚，也不锚错到别的器物上）。
func RemapPropRefs(plan *Plan) int {
	if plan == nil || len(plan.Props) == 0 {
		return 0
	}
	// 别名索引：卡片 id（去 prop_ 前缀）、中文名、英文串里的实词 → 卡片 id
	alias := map[string]string{}
	add := func(k, id string) {
		k = normPropKey(k)
		if k != "" {
			if _, dup := alias[k]; !dup {
				alias[k] = id
			}
		}
	}
	for _, p := range plan.Props {
		add(p.ID, p.ID)
		add(strings.TrimPrefix(p.ID, "prop_"), p.ID)
		add(p.Name, p.ID)
		for _, w := range strings.FieldsFunc(p.EN, func(r rune) bool {
			return r == ' ' || r == ',' || r == '-' || r == '_'
		}) {
			if len([]rune(w)) >= 3 {
				add(w, p.ID)
			}
		}
		for _, w := range strings.FieldsFunc(p.Description, func(r rune) bool {
			return r == '，' || r == '、' || r == ' ' || r == '（' || r == '）'
		}) {
			if len([]rune(w)) >= 2 {
				add(w, p.ID)
			}
		}
	}
	known := map[string]bool{}
	for _, p := range plan.Props {
		known[p.ID] = true
	}
	fixed := 0
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if len(sh.PropIDs) == 0 {
			continue
		}
		var out []string
		for _, id := range sh.PropIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if known[id] {
				out = append(out, id)
				continue
			}
			if hit, ok := alias[normPropKey(id)]; ok {
				out = appendUnique(out, hit)
				fixed++
				continue
			}
			// 退一步：用分镜自己的动作文本找（"他把碑刀搁在…" ⇒ 命中"碑刀"）
			if hit := matchPropInText(alias, sh.Action+" "+sh.EndState); hit != "" {
				out = appendUnique(out, hit)
				fixed++
			}
			// 都不中：丢掉这条引用（宁可少锚，也不锚错）
		}
		sh.PropIDs = out
	}
	return fixed
}

// matchPropInText 在文本里找命中别名表的器物（取**最长**命中，避免"刀"命中到无关的刀）。
func matchPropInText(alias map[string]string, text string) string {
	t := normPropKey(text)
	if t == "" {
		return ""
	}
	best, bestLen := "", 0
	for k, id := range alias {
		if len([]rune(k)) < 2 {
			continue
		}
		if strings.Contains(t, k) && len([]rune(k)) > bestLen {
			best, bestLen = id, len([]rune(k))
		}
	}
	return best
}

// normPropKey 归一化比较键：小写、去空白与常见分隔符。
func normPropKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// FillMissingDialogueTags 给"有计划台词/旁白、正文里却一个 `<d>` 都没有"的镜**补上台词标签**，
// 返回补了几镜。
//
// 【实测缺陷】真章节那一跑：**4 镜**的 `dialogue` 字段里有词，`h3_prompt` 正文里却没有任何
// `<d>` 标记 —— 契约的正确处置是 fail（成片里那几句会被说成听不懂的话），但用户看到的是
// "又说不能渲"。而这件事平台**能确定做对**：台词原文在计划里、说话人是谁也在计划里，
// 缺的只是把它写进正文这一道手工活。
//
// 保守做法：只在**一个 `<d>` 都没有**时才补（正文已经有了就绝不插手）；补在正文段末尾，
// 逐字用计划里的原文，不做任何改写。说话人用正文里已有的 `(Sx)` 标记解析（同一角色在
// subject_definitions 里是哪一号），解析不到就用 `<Subject 1>`。
func FillMissingDialogueTags(plan *Plan) int {
	if plan == nil {
		return 0
	}
	n := 0
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if strings.Contains(sh.H3Prompt, "<d>") {
			continue
		}
		var lines []string
		for _, ln := range sh.Dialogue {
			txt := strings.TrimSpace(ln.Text)
			if txt == "" {
				continue
			}
			lines = append(lines, " "+speakerTagFor(sh.H3Prompt, ln.Speaker, plan)+" says: <d>[Chinese]"+txt+"</d>")
		}
		if nar := strings.TrimSpace(sh.Narration); nar != "" {
			lines = append(lines, " "+speakerTagFor(sh.H3Prompt, "", plan)+
				" speaks in an off-screen voiceover: <d>[Chinese]"+nar+"</d> while the on-screen characters' lips remain completely closed")
		}
		if len(lines) == 0 {
			continue
		}
		sh.H3Prompt = strings.TrimRight(sh.H3Prompt, "\n ") + "\n" + strings.Join(lines, "\n")
		n++
	}
	return n
}

// speakerTagFor 在正文里找这个说话人的 `<Subject N> (Sx)` 标记；找不到退回 `<Subject 1>`。
func speakerTagFor(h3, speaker string, plan *Plan) string {
	name := strings.TrimSpace(speaker)
	if name != "" {
		// subject_definitions 里每个 <Subject N> 后面通常跟着角色名，按"名字出现在哪一行"定位
		best := ""
		for _, ln := range strings.Split(h3, "\n") {
			if !strings.Contains(ln, name) {
				continue
			}
			if tag := firstSubjectTag(ln); tag != "" {
				best = tag
				break
			}
		}
		if best != "" {
			return best
		}
	}
	if tag := firstSubjectTag(h3); tag != "" {
		return tag
	}
	return "<Subject 1> (S1)"
}

// firstSubjectTag 从一行里抽出形如 `<Subject 2> (S1)` 的标记（带说话人号时优先）。
func firstSubjectTag(line string) string {
	i := strings.Index(line, "<Subject ")
	if i < 0 {
		return ""
	}
	rest := line[i:]
	end := strings.Index(rest, ">")
	if end < 0 {
		return ""
	}
	tag := rest[:end+1]
	// 后面紧跟的 (Sx) 一并带上
	tail := strings.TrimSpace(rest[end+1:])
	if strings.HasPrefix(tail, "(") {
		if j := strings.Index(tail, ")"); j > 0 && j < 6 {
			tag += " " + tail[:j+1]
		}
	}
	return tag
}

// DanglingPropRefs 分镜里指向不存在道具卡的引用数（归一化**之前**判；归一化会把它们对号或丢弃）。
func DanglingPropRefs(plan *Plan) int { return countDanglingPropRefs(plan) }

// countDanglingPropRefs 同上（契约门内部用）。
func countDanglingPropRefs(plan *Plan) int {
	if plan == nil {
		return 0
	}
	known := map[string]bool{}
	for _, p := range plan.Props {
		known[p.ID] = true
	}
	n := 0
	for _, sh := range plan.Shots {
		for _, id := range sh.PropIDs {
			if id = strings.TrimSpace(id); id != "" && !known[id] {
				n++
			}
		}
	}
	return n
}

// HealResult 自愈三件的计数（各处按需播报，不吞掉信息）。
type HealResult struct {
	PropRefs  int // 器物引用对号了几处
	Dialogue  int // 补了几镜的台词标签
	Voiceover int // 补了几镜的画外演出素材引用
	MusicNA   int // 把几镜的 non_diegetic_music 改写成 N/A（平台政策，见 music_na.go）
}

// HealPlan **自愈三件**的唯一入口（幂等）：器物引用对号、缺失的台词标签补全、
// 画外演出素材引用补齐。三条都只把**已知量**写到位，不改任何创作内容。
//
// 【为什么必须是一个入口】2026-09-28 实测教训：这三个修复先只挂在 director 上，
// 于是 `cmd/preflight` 直接读 JSON 时看到的是**未修复**的方案 —— 预检报出 5 条 fail，
// 而渲染器看到的方案是 0 条，用户被假警吓一遍。凡是"读到方案"的地方都必须走同一份。
func HealPlan(plan *Plan) HealResult {
	if plan == nil {
		return HealResult{}
	}
	return HealResult{
		PropRefs:  RemapPropRefs(plan),
		Dialogue:  FillMissingDialogueTags(plan),
		Voiceover: FillVoiceoverAssets(plan),
		MusicNA:   EnsureMusicNA(plan),
	}
}
