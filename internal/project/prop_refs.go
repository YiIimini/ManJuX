package project

import (
	"sort"
	"strings"
	"unicode"
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

// FillMissingPropRefs 给**根本没写 `prop_ids`** 的镜，按正文/动作里提到的**卡片名**补上器物引用
// （返回补了几处）。已有引用的镜不碰（那些交给 RemapPropRefs 对号/去重/必要时丢弃）。
//
// 【为什么必须有这一步】（2026-10-06 审计）
//
// 器物跨镜一致的**唯一机制**是把该镜引用到的器物卡原文逐字锚进提示词（`EnsurePropAnchors`，
// 道具不进 H3 参考图集合），而它整条挂在 `prop_ids` 上：
//
//	· 模型写了 `prop_ids` 但拼法与卡片 id 不一致 → RemapPropRefs 对号（2026-09-28 已有）；
//	· 模型**一个字都没写** → 此前**什么都不发生**：`RemapPropRefs` 对空引用是直接 skip，
//	  于是正文里写着"碑刀握在右手"、锚定却落空 ⇒ 那把刀每镜一个形状。
//	  实测那一集 25 镜提到刀/烙铁/名册，而 `prop_ids` 全空。
//
// 匹配只用**强键**：卡片中文名、卡片 id、id 去 `prop_` 前缀形。**不用描述里的散词**——
// "磨薄""朱砂"这类词出现在散文里未必在讲这件器物；宁可少锚，也不锚错到别的器物上
// （与 RemapPropRefs 同一条纪律：它的散词匹配只用在"**已经有引用**、只是拼错"的场合）。
// 长键优先：命中"名册板"之后，被它包含的短键"名册"不再单独算一件。
func FillMissingPropRefs(plan *Plan) int {
	if plan == nil || len(plan.Props) == 0 {
		return 0
	}
	type alias struct{ key, id string }
	var aliases []alias
	seen := map[string]bool{}
	for _, p := range plan.Props {
		for _, cand := range []string{p.Name, p.ID, strings.TrimPrefix(p.ID, "prop_")} {
			k := normPropKey(cand)
			if len([]rune(k)) < 2 || seen[k] {
				continue
			}
			seen[k] = true
			aliases = append(aliases, alias{key: k, id: p.ID})
		}
	}
	if len(aliases) == 0 {
		return 0
	}
	// 长键优先（"名册板"先于"名册"）
	sort.SliceStable(aliases, func(i, j int) bool {
		return len([]rune(aliases[i].key)) > len([]rune(aliases[j].key))
	})
	filled := 0
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if len(sh.PropIDs) > 0 {
			continue
		}
		// 看**模型真正会画**的那几处文本：正文 + 动作 + 尾帧状态
		text := normPropKey(sh.H3Prompt + " " + sh.Action + " " + sh.EndState)
		if text == "" {
			continue
		}
		var hit, matched []string
		for _, a := range aliases {
			if !strings.Contains(text, a.key) {
				continue
			}
			skip := false
			for _, m := range matched {
				if strings.Contains(m, a.key) { // 已被更长的键包含：同一件器物，不重复算
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			matched = append(matched, a.key)
			hit = appendUnique(hit, a.id)
		}
		if len(hit) == 0 {
			continue
		}
		sh.PropIDs = hit
		filled += len(hit)
	}
	return filled
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

// FillMissingDialogueTags 给"计划里有台词/旁白、正文里却没有对应的 `<d>`"的镜**补上台词标签**，
// 返回补了几镜。
//
// 【实测缺陷】2026-09-28 真章节那一跑：**4 镜**的 `dialogue` 字段里有词，`h3_prompt` 正文里却
// 没有任何 `<d>` 标记 —— 契约的正确处置是 fail（成片里那几句会被说成听不懂的话），
// 但用户看到的是"又说不能渲"。而这件事平台**能确定做对**：台词原文在计划里、说话人是谁也在
// 计划里，缺的只是把它写进正文这一道手工活。
//
// 【2026-10-06 审计：只判"一个都没有"是不够的】原判据是
// `if strings.Contains(h3_prompt, "<d>") { continue }` —— 于是**部分缺**（计划 2 句、
// 正文只写了 1 句）既不补也不报：配音读正文的 `<d>`、字幕读 `Dialogue[]`，
// 成片里那句话**只有字幕没有声音**，而所有门都是绿的。
//
// 现在按**逐句对账**：把正文里已有的 `<d>…</d>` 抽出来（去标点与语言标记后比较 —— 官方要求
// 台词标点归一成 `, . ? !`，逐字符比较会把"同一句话"判成没说过 ⇒ 重复配音），
// 只把**确实没说过的那几句**追加到正文末尾，逐字用计划里的原文，不做任何改写。
// 幂等：补完之后那几句就在 `<d>` 里了，再跑不会重复追加。
//
// 边界：**正文整体是空的就不补** —— 往空正文里塞台词会造出"只有台词、没有画面描述"的镜，
// 那是另一件事（空提示词闸门 + 自纠环负责），这里不越界。
func FillMissingDialogueTags(plan *Plan) int {
	if plan == nil {
		return 0
	}
	n := 0
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if strings.TrimSpace(sh.H3Prompt) == "" {
			continue
		}
		// **句数已经够就不补**（2026-10-07 修）：下面那个逐句循环在"模型把某句**改写**了"时
		// 会判成"没说"，于是**再追加一句正确文本** —— 同一句台词在正文里出现两次，
		// 用户听到的就是"重复配音"（2026-09-28 报过的那个症状）。
		// 句数相等而文本不同的情形属于"台词通道对不上"，交给契约门
		// （checkSpokenTextMatchesDialogue，fail + 喂回去重写），**不在这里悄悄叠一句**。
		if shouldSkipDialogueFill(sh) {
			continue
		}
		said := spokenInPrompt(sh.H3Prompt)
		var lines []string
		for _, ln := range sh.Dialogue {
			txt := strings.TrimSpace(ln.Text)
			if txt == "" || alreadySpoken(said, txt) {
				continue
			}
			lines = append(lines, " "+speakerTagFor(sh.H3Prompt, ln.Speaker, plan)+" says: <d>[Chinese]"+txt+"</d>")
		}
		if nar := strings.TrimSpace(sh.Narration); nar != "" && !alreadySpoken(said, nar) {
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

// shouldSkipDialogueFill 这一镜的正文**句数已经够**（`<d>` 数 ≥ 计划该说的句数）⇒ 不再追加。
//
// 只有"真的少说了几句"才需要平台补。改写过词的情形由契约门报出来、喂回去重写 ——
// 平台在这里再叠一句，只会让同一句被念两遍。
func shouldSkipDialogueFill(sh *Shot) bool {
	planned := len(plannedSpokenLines(sh))
	return planned == 0 || len(spokenInPrompt(sh.H3Prompt)) >= planned
}

// spokenInPrompt 正文里**已经说过的话**（每个 `<d>…</d>` 的内容，做「去标点 + 小写」归一）。
func spokenInPrompt(h3 string) []string {
	var out []string
	rest := h3
	for {
		i := strings.Index(rest, "<d>")
		if i < 0 {
			break
		}
		rest = rest[i+len("<d>"):]
		j := strings.Index(rest, "</d>")
		if j < 0 {
			break // 没有闭合标签：这一段不算"说过了"（宁可补一次，也别漏掉一句）
		}
		out = append(out, normSpokenText(rest[:j]))
		rest = rest[j+len("</d>"):]
	}
	return out
}

// alreadySpoken 计划里的这句台词，正文里是不是已经说过了。
//
// 归一化后再比：官方要求台词标点归一成 `, . ? !`、`<d>` 里还带着 `[Chinese]` 语言标记，
// 而计划里的原文可能与正文逐字不同 —— 逐字符比较会把同一句判成"没说" ⇒ 重复追加一次
// （成片里那句话被念两遍）。去标点 + 小写 + 子串判据对这两件事都免疫。
func alreadySpoken(said []string, text string) bool {
	want := normSpokenText(text)
	if want == "" {
		return false
	}
	for _, s := range said {
		if s == "" {
			continue
		}
		if strings.Contains(s, want) || strings.Contains(want, s) {
			return true
		}
	}
	return false
}

// normSpokenText 归一化一句话用于比较：只留汉字/字母/数字，其余（标点、空格、方括号、
// 破折号等）全部去掉，字母转小写。语言标记里的字母会留下（"chinese…"），子串判据不受影响。
//
// 【2026-10-08 起先把**字母编号换成中文读法**】`U-80001` 与它的读法「尤八零零零一」是同一句话的
// 两种写法：`<d>` 里放读法（模型只需要念中文，见 speech_codes.go），而 `dialogue`（字幕源）保留原编号。
// 归一化在这一步统一 ⇒ 逐句对账（checkSpokenTextMatchesDialogue）不会把"读音"判成"改了词"、
// 对齐（AlignDialogueTags）也不会把读法又刷回原编号（那会每读一次方案就来回改一次）。
func normSpokenText(s string) string {
	s = PronounceCodesInText(s)
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
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

// HealResult 自愈各件的计数（各处按需播报，不吞掉信息）。
type HealResult struct {
	PropRefs int // 器物引用对号了几处
	// PropRefsFilled **根本没写 prop_ids** 的镜、按正文/动作里提到的卡片名补上的引用处数
	//（2026-10-06 审计：锚定机制整条挂在 prop_ids 上，空的就一次都不锚 ⇒ 那几镜的器物
	// 每镜重新发明；名字是已知量，对号这件事平台能确定做对）。
	PropRefsFilled int
	Dialogue       int // 补了几镜的台词标签
	// AlignTags 把几镜的 `<d>` 文本**对齐到了 dialogue 的原文**（同一位置原本两句不一样）。
	//
	// 【为什么是平台做（2026-10-07 用户「配音存在乱配音」）】声音念 `<d>`、字幕烧 dialogue ——
	// 两条独立通道。模型在 `<d>` 里改一个词，观众就是"听到的跟写着的不一样"。
	// 逐句位置（第几句）是**已知量**（句数相等时按顺序一一对应），文本来处也是已知量 ⇒
	// 平台能确定性地对齐，不该留给模型自觉。句数不等时不猜（那是"少说了一句/多说了一句"，报 fail）。
	AlignTags int
	Voiceover int // 补了几镜的画外演出素材引用
	MusicNA   int // 把几镜的 non_diegetic_music 改写成 N/A（平台政策，见 music_na.go）
	// EntityRefs 分镜里的**名字引用**归一成卡片 id 的处数（五步会话输出的是名字，
	// 见 entity_refs.go）；EntityRefsUnknown 是归一之后仍对不上卡片的引用数。
	EntityRefs int
	// EntityRefsLoose 其中按**字面重合**猜对号的处数（名字与卡片名不完全一致 —— 要播报，
	// 因为那是猜的，用户得知道这一镜的参考图是猜出来的）。
	EntityRefsLoose   int
	EntityRefsUnknown int
	// CharDupes 同名两卡被并掉的张数（②人物道具与③角色多视图 各起了一个 id 的同一个角色）。
	CharDupes int
	// Cuts 从正文**回填**出小镜头结构的片段数（2026-10-06 审计 §八-11）。
	//
	// 权威字段 `shot.cuts` 在平台自己的生成链上**从来没被写过**（schema 里没要它、也没人回填），
	// 而分镜实际是把内切写在正文里（`[Shot N] At MM:SS.mmm`）⇒ 于是 `CutIssues` 的
	// **每刀 1.5–4 秒 / 首刀为 0 / 时间点严格递增 / 各刀之和 = 片段时长**这几条（用户文件里的
	// 硬要求）**永久空转**。`ParseCutsFromPrompt` 早就写好了，缺的只是在**唯一读取边界**调用它。
	Cuts int
	// Voices 补上声线的**角色卡张数**（2026-10-07：走锚表补出来的卡从来不写 voice，
	// 渲染侧所有"无声线卡"落到同一个常数 ⇒ 几个角色一个嗓子；见 voice_defaults.go）。
	Voices int
	// VoiceNames 被补上声线的角色名（要播报"补了谁的嗓子"）。
	VoiceNames []string
	// CodeShots / CodeIDs / CodeCount 台词里的**字母编号定了中文读法**的镜数、
	// 哪几镜、多少处（2026-10-08：`U-80001` → 「尤八零零零一」，见 speech_codes.go）。
	CodeShots int
	CodeIDs   []string
	CodeCount int
	// Bodies / BodyIDs **平台替模型补写的空正文**镜数与镜号（见 empty_body.go）。
	//
	// 【为什么由平台兜（2026-10-08 用户第二次报「还是报一样的错误」）】真机镜 12 的材料齐全、
	// 只有正文是空的，模型在自纠一轮里补不出来 ⇒ 渲染入口的致命门不让启动 ⇒ 48 集的批量产线
	// 停在第 1 集。材料是已知量 ⇒ 平台按方案自己的材料拼一版最小合规正文（**不是新编内容**），
	// 拼过哪几镜由各阶段如实播报，用户不满意就在故事板里改那几镜。
	Bodies  int
	BodyIDs []int
}

// HealPlan **自愈各件**的唯一入口（幂等）：器物引用对号、分镜人物/场景引用归一、
// 缺失的台词标签补全、画外演出素材引用补齐。都只把**已知量**写到位，不改任何创作内容。
//
// 【为什么必须是一个入口】2026-09-28 实测教训：这些修复先只挂在 director 上，
// 于是 `cmd/preflight` 直接读 JSON 时看到的是**未修复**的方案 —— 预检报出 5 条 fail，
// 而渲染器看到的方案是 0 条，用户被假警吓一遍。凡是"读到方案"的地方都必须走同一份。
func HealPlan(plan *Plan) HealResult {
	if plan == nil {
		return HealResult{}
	}
	// **空切片也要是 []，不能是 null**（2026-09-29 用户：工作台报
	// `Cannot read properties of null (reading 'length')`）。
	// Go 的 nil 切片 marshal 成 `null`，而界面到处在数 `plan.characters.length` ——
	// 方案里某一段为空（例如刚拆集、还没生成角色）时整页就崩。
	// 收在**唯一读取边界**这一处，比在前端每个 `?.` 上打补丁可靠（新界面不会再踩）。
	if plan.Characters == nil {
		plan.Characters = []Character{}
	}
	if plan.Scenes == nil {
		plan.Scenes = []Scene{}
	}
	if plan.Props == nil {
		plan.Props = []Prop{}
	}
	if plan.Shots == nil {
		plan.Shots = []Shot{}
	}
	// 实体引用归一**要排在器物对号之前**：器物对号会退回分镜的动作文本里找线索，
	// 而"这一镜有谁"在归一之前可能还是名字 —— 顺序反了会让它拿着对不上的名单去猜。
	// 同名两卡先并掉（②与③会话各起过 id），再归一引用 —— 顺序不能反：
	// 并卡会把被丢掉的 id 的引用改写成保留的 id，跑在归一之前才对得上。
	charDupes := DedupeCharacters(plan)
	entFixed, entUnknown, entLoose := RemapEntityRefs(plan)
	// 声线兜底排在并卡之后：并掉的卡不必再补嗓子（不然播报里会多出几个已不存在的名字）。
	// 它只填空的，已有 voice 的卡一个字不动；同名同声、异名不同声（见 voice_defaults.go）。
	voices, voiceNames := FillMissingVoices(plan)
	// 台词通道：补缺标签 → 对齐到 dialogue（顺序不能反，理由见下面 HealResult 的注释）。
	dialogueFilled := FillMissingDialogueTags(plan)
	alignTags := AlignDialogueTags(plan)
	// **编号读法排在补/对齐之后**：那两步决定"这一镜有哪几句、每句的权威文本是什么"，
	// 若排在它们前面，补出来的那句会带着原样的 `U-80001` 进提示词（等于没做）。
	// 逐句对账与对齐都走 normSpokenText（它把编号折算成读法），所以读法与 dialogue 原文
	// 不会被判成"不一致"、也不会被对齐刷回去（见 speech_codes.go）。
	codeShots, codeIDs, codeCount := NormalizeSpokenCodes(plan)
	// **空正文的装配点兜底**（2026-10-08 用户第二次报「还是报一样的错误」）：材料齐全、正文为空的镜
	// 由平台按方案自己的材料拼一版最小合规正文（见 empty_body.go）。
	//
	// 为什么排在**这里**（补台词标签之后、小镜头回填之前）：
	// ・补台词标签/编号读法要先跑 —— 它们决定"这一镜有哪几句、每句的权威文本是什么"，
	//   排在前面，拼正文时拿到的就是**已经补全、已经折成中文读法**的那几句；
	// ・小镜头回填（FillCutsFromPrompt）要后跑 —— 它给**结构字段为空**的镜从正文里读回小镜头；
	//   拼出来的正文本身带内切，结构字段也由兜底一并写好了，所以顺序不影响结果，但"先有正文、
	//   再统一回填"这条读起来不会骗人。
	//
	// 只在**唯一读取边界**调用，所以预检 / 渲染 / 人审三个消费方看到的是同一份（2026-09-28 的教训）。
	filledBodies, filledBodyIDs := FillEmptyShotBodies(plan)
	return HealResult{
		EntityRefs:        entFixed,
		EntityRefsUnknown: entUnknown,
		EntityRefsLoose:   entLoose,
		CharDupes:         charDupes,
		PropRefs:          RemapPropRefs(plan),
		PropRefsFilled:    FillMissingPropRefs(plan),
		Dialogue:          dialogueFilled,
		AlignTags:         alignTags,
		Voiceover:         FillVoiceoverAssets(plan),
		MusicNA:           EnsureMusicNA(plan),
		// 小镜头结构回填：排在最后（它只读正文，与上面各项互不依赖），
		// 补上之后 `CutIssues` 那几条（每刀 1.5–4 秒等）才真正开始工作。
		Cuts:       plan.FillCutsFromPrompt(),
		Voices:     voices,
		VoiceNames: voiceNames,
		CodeShots:  codeShots,
		CodeIDs:    codeIDs,
		CodeCount:  codeCount,
		Bodies:     filledBodies,
		BodyIDs:    filledBodyIDs,
	}
}
