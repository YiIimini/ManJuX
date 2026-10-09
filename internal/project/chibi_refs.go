package project

import "strings"

// chibi_refs.go —— Q 版引用（`chibi_ids`）的**唯一判据**：三套 id 空间一次认全，
// 装配点把显示名改写成方案里真实的卡片 id（见 NormalizeChibiIDs）。
//
// 【真机取证（2026-10-09，用户：「问题好多啊」）】第01集 15 镜里有 3 镜是内心镜
// （#8 / #14 / #15），`chibi_ids` 里写的都是**显示名**「简宁·Q版」，而方案的角色卡里
// 那张卡的 id 是 `char_jianning_q`。两处后果，都不是「少一张图」那么轻：
//
//	① 契约门 fail（`HasCharacterID("简宁·Q版")` 为假 ⇒ 「chibi_ids 引用悬空」）；
//	② 渲染侧 `pipeline/refs.go` 的顶替映射只认 `chibi_<本体id>` 前缀，且去找
//	   资产键 `char:chibi_<本体id>` —— 而生产上根本不存在这个键：
//	   平台自己派生的 Q 版写的是 `char:<本体id>_chibi`（stages_assets.go），
//	   库里搬来的 Q 版卡写的是 `char:<卡id>`（本集是 `char:char_jianning_q`）。
//	   ⇒ 顶替**从来没生效过**：镜 #8/#15 的出场名单里有本人（`char_jianning`），
//	   于是附的是**本人定妆照** —— 内心戏由本人演，正是口径明令禁止的那件事。
//
// 教训与 2026-09-29 那次同型（见 proportion_anchor.go 文件头）：
// **判据认的写法比实际写法少** ⇒ 判据全部落空，且日志一片安静。
// 所以这里把「一条引用该解析成哪张卡」收成一处，装配点与渲染侧共用。

// CardRef 解析 Q 版引用时可用的**一张卡**（方案的角色卡，或渲染侧的资产条目）。
type CardRef struct {
	ID   string
	Name string
}

// ChibiCardsOf 把方案的角色卡表转成解析用的卡片表。
func ChibiCardsOf(p *Plan) []CardRef {
	if p == nil {
		return nil
	}
	out := make([]CardRef, 0, len(p.Characters))
	for _, c := range p.Characters {
		out = append(out, CardRef{ID: c.ID, Name: c.Name})
	}
	return out
}

// IsChibiRef 这张卡是不是 Q 版（id 两种写法 / 名字里带 Q 版标记）。
func IsChibiRef(c CardRef) bool {
	return IsChibiCard("", strings.TrimSpace(c.ID)) || ChibiBaseName(c.ID, c.Name) != ""
}

// normChibiKey 归一化一张卡的名字/一条引用，用于**相等比较**：
// 去空白、去间隔号与连接符、去括注、大小写归一。
//
// 为什么必须归一：「简宁·Q版」「简宁Q版」「简宁 · Q版」是同一个形象的三种写法，
// 而库条目名（`简宁·Q版`）、平台派生名（`简宁 · Q版`）、模型写的引用（`简宁Q版`）
// 恰好各占一种 —— 不归一就是三套互不相等。
func normChibiKey(s string) string {
	s = StripBracketedName(strings.TrimSpace(s))
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\u3000', '·', '・', '-', '_', '—', '–', '「', '」', '“', '”', '、', ',', ':', '：':
			continue
		}
		sb.WriteRune(r)
	}
	return strings.ToLower(sb.String())
}

// ResolveChibiRef 把 `chibi_ids` 里的一条引用解析成**真实的 Q 版卡 id**。
//
// 接受的写法（三套并存是历史事实，缺一套就有方案绑不上图）：
//
//	① 卡片 id 本身：`char_jianning_q`（可带 `char:` 前缀）；
//	② 技能侧前缀/后缀：`chibi_<本体id>` / `<本体id>_chibi`（平台自己派生的 Q 版就是这种）；
//	③ 显示名：`简宁·Q版` / `Q版简宁` / `简宁Q版`（含括注）。
//
// 认不出来时返回 ok=false —— **不改写**，交给契约门如实 fail（不许猜一张卡顶上去：
// 猜错的后果是把别的角色的 Q 版画进这一镜，比悬空更糟）。
func ResolveChibiRef(ref string, cards []CardRef) (id, name string, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", false
	}
	bare := strings.TrimSpace(strings.TrimPrefix(ref, "char:"))
	// ① id 直接命中（一条也不动）。
	for _, c := range cards {
		if strings.TrimSpace(c.ID) == bare || strings.TrimSpace(c.ID) == ref {
			return c.ID, c.Name, true
		}
	}
	key := normChibiKey(ref)
	if key == "" {
		return "", "", false
	}
	// ② Q 版卡的**显示名**（或它自己的 id）归一后相等（含括注）：
	//    真机第01集写的 `简宁·Q版` 走的就是这一条。只在 Q 版卡里找 ——
	//    不排除的话「简宁」本人卡会被当成 Q 版，内心镜反而附了本人照。
	for _, c := range cards {
		if !IsChibiRef(c) {
			continue
		}
		if normChibiKey(c.Name) == key || normChibiKey(c.ID) == key {
			return c.ID, c.Name, true
		}
	}
	// ③ 剥掉 Q 版标记后的「本体」对号（覆盖 `Q版简宁`、`chibi_char_jianning`、
	//    `char_jianning_chibi` 这些写法）：
	//    ③.1 直接命中某张 Q 版卡自己的本体键；
	//    ③.2 命中某张本体卡（名字或 id）⇒ 再找它的 Q 版。
	//    认不出来就返回 false —— **不许猜一张卡顶上去**（猜错是把别人的 Q 版画进这一镜，
	//    比悬空更糟），交给契约门点名。
	bases := chibiBaseKeys(ref)
	// 只有"Q 版写法"才走本体对号 —— 平白一个本人名（`简宁`）**不许**被解析成 Q 版：
	// 那会让"写错人的引用"悄悄变成"附了别人的 Q 版"，比悬空更难查。
	if len(bases) == 0 || !isChibiStyled(ref) {
		return "", "", false
	}
	inBases := func(keys []string) bool {
		for _, k := range keys {
			for _, b := range bases {
				if k != "" && k == b {
					return true
				}
			}
		}
		return false
	}
	for _, c := range cards {
		if IsChibiRef(c) && inBases(cardBaseKeys(c)) {
			return c.ID, c.Name, true
		}
	}
	for _, owner := range cards {
		if IsChibiRef(owner) || !inBases(cardBaseKeys(owner)) {
			continue
		}
		own := cardBaseKeys(owner)
		for _, c := range cards {
			if !IsChibiRef(c) {
				continue
			}
			// 「这张 Q 版是谁的」：本体键（名字键 / 去掉 chibi 标记的 id 键）里有一个与本体卡相同。
			if inBases(cardBaseKeys(c)) || sharesAny(cardBaseKeys(c), own) {
				return c.ID, c.Name, true
			}
		}
	}
	return "", "", false
}

// cardBaseKeys 一张卡的「本体键」集合：显示名/id 归一（含去掉 char_/chibi_ 前缀），
// 以及**剥掉 Q 版标记后**剩下的那一段 —— Q 版卡因此也能回答"我是谁的本体"。
//
// 两侧都要剥标记，这一点是**必须的**：Q 版卡的名字键是 `简宁q版`，若不剥，
// 它和本体卡的键（`简宁`）永远不相交 ⇒ 本节所有对号全部落空（真机第一次就是这么失败的）。
func cardBaseKeys(c CardRef) []string {
	set := map[string]bool{}
	add := func(s string) {
		if s = normChibiKey(strings.TrimSpace(s)); s != "" {
			set[s] = true
		}
	}
	add(c.Name)
	add(strings.TrimPrefix(strings.TrimSpace(c.ID), "char_"))
	add(strings.TrimPrefix(strings.TrimSpace(c.ID), "chibi_"))
	for _, s := range []string{c.Name, c.ID} {
		for _, b := range chibiBaseKeys(s) {
			set[b] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// isChibiStyled 这条引用是不是"Q 版写法"（名字里带 Q版/chibi 标记，或 id 是 chibi 前缀/后缀）。
//
// 为什么要有这道闸：`简宁`（本人名）剥不出标记，但它的"本体键"与简宁的 Q 版卡是相交的
// ⇒ 不设闸就会把"写成本人名"的引用解析成 Q 版卡。写成本人名是**数据错**，
// 该让门点名，不该被悄悄改成另一张卡。
func isChibiStyled(ref string) bool {
	k := normChibiKey(ref)
	if k == "" {
		return false
	}
	for _, w := range []string{"q版", "q-版", "chibi"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// sharesAny 两组键有没有交集。
func sharesAny(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x != "" && x == y {
				return true
			}
		}
	}
	return false
}

// chibiBaseKeys 从一条引用里取出「本体」的候选键（够用即可，取不到返回空）。
//
// 归一之后所有分隔符都没了，所以三种写法分别长这样：
//
//	`简宁·Q版`            → `简宁q版`          剥标记 → `简宁`
//	`chibi_char_jianning` → `chibicharjianning` 剥前缀 → `charjianning` → 再去 char → `jianning`
//	`char_jianning_chibi` → `charjianningchibi` 剥后缀 → `charjianning` → 再去 char → `jianning`
//
// 关键点：剥完标记后**整段**参与比较（不做"变体档"截断）—— 否则 `简宁·Q版2` 会被
// 截成 `简宁` 而对上简宁的 Q 版（多出来的那个字被悄悄吃掉）。
func chibiBaseKeys(ref string) []string {
	k := normChibiKey(ref)
	if k == "" {
		return nil
	}
	set := map[string]bool{}
	add := func(s string) {
		if s != "" {
			set[s] = true
		}
	}
	// ① 原样 + 去掉 char/chibi 前缀的两版（id 写法：`char_jianning_q` / `chibicharjianning`）。
	add(k)
	add(strings.TrimPrefix(k, "char"))
	add(strings.TrimPrefix(k, "chibi"))
	add(strings.TrimPrefix(strings.TrimPrefix(k, "chibi"), "char"))
	// ② 剥掉 Q 版标记后的**整段**（名字写法，也覆盖 `chibicharjianning`）。
	for _, w := range []string{"q版", "q-版", "chibi"} {
		if i := strings.Index(k, w); i >= 0 {
			rest := k[:i] + k[i+len(w):]
			add(rest)
			add(strings.TrimPrefix(rest, "char"))
		}
	}
	// ③ 后缀写法 `<本体>chibi`。
	if strings.HasSuffix(k, "chibi") && len(k) > len("chibi") {
		rest := strings.TrimSuffix(k, "chibi")
		add(rest)
		add(strings.TrimPrefix(rest, "char"))
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	return out
}

// NormalizeChibiIDs 装配点自愈（幂等）：把 `chibi_ids` 里的显示名/旧写法改写成方案里
// **真实的 Q 版卡 id**，并把认不出来的引用如实记账。
//
// 【为什么由平台做（2026-10-09）】「这张 Q 版是谁的」是**已知量**：方案的角色卡表就在同一份
// JSON 里（`char_jianning_q` / `简宁·Q版`）。模型写显示名是措辞问题，不是创作问题 ⇒
// 平台能确定性地对上，不该留给模型自觉（否则渲染侧静默丢图、内心戏由本人演）。
//
// 认不出来的**不改写**：契约门会点名 fail，用户能看见并去改那一镜。
func (p *Plan) NormalizeChibiIDs() (fixed int, unknown []string) {
	if p == nil {
		return 0, nil
	}
	cards := ChibiCardsOf(p)
	if len(cards) == 0 {
		return 0, nil
	}
	seen := map[string]bool{}
	for i := range p.Shots {
		sh := &p.Shots[i]
		for j, raw := range sh.ChibiIDs {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			// 已经是方案里的卡片 id：一个字不动（幂等的前提）。
			if p.HasCharacterID(raw) {
				continue
			}
			id, _, ok := ResolveChibiRef(raw, cards)
			if !ok {
				if !seen[raw] {
					seen[raw] = true
					unknown = append(unknown, raw)
				}
				continue
			}
			sh.ChibiIDs[j] = id
			fixed++
		}
	}
	// 装配期诊断量（与 SpeakerOSFixed 同规矩：是这次的账，不进 JSON）。
	p.ChibiRefFixed += fixed
	p.ChibiRefUnknown = unknown
	return fixed, unknown
}

// ChibiKey 归一化比较键（渲染侧按「本体名」对号时用得到）。
//
// 为什么导出而不是各写一份：两侧（装配点 / 渲染侧）必须算出**同一个键**，
// 否则又变成"两套判据、一处生效"——那正是这个文件要修的病。
func ChibiKey(s string) string { return normChibiKey(s) }
