package project

import (
	"strconv"
	"strings"
)

// char_family.go 「同角色的多张卡」当**一个人**的唯一判据(技能侧《平台侧整改清单》§4.2)。
//
// ## 为什么需要它
//
// 用户问「角色的服装发型会随剧情而改变吗?」,技能侧的解法是把**每个造型档做成一张
// 独立的卡**:主卡 + 变体卡 `翻篇·归零` / `糖球·雨夜` / `老墙·认错`……
// (名字带档名,所以 PICTURE MANIFEST 仍能逐张对上;声线与 sid 继承主卡,换档不换嗓子)。
// 平台侧于是拿到这样一份数据(《翻篇》真机实测):
//
//	char_01v1  翻篇·收容所
//	char_01v2  翻篇·进城
//	char_01v4  翻篇·归零
//	char_05v2  糖球·雨夜
//
// 每张卡都是**同一个人换了一身衣服/换了一个状态**。而平台里凡按「角色」聚合的地方
// 都会把它当成新角色:资产库把三张卡算成三个人(跨集复用时"新角色需要重新定妆")、
// 人物面板显示三个「翻篇」、首页角色数虚高、资产范围统计按卡计数。
//
// ## 判据(两列都认)
//
//	id:   `char_NN` 与 `char_NNvK` 归一到 `char_N`(前导零归一;K 是造型档号)
//	name: `X` 与 `X·档名` 归一到 `X`(分隔符见 charVariantSep)
//
// ## 一条不能越过的取舍
//
// **这里只判"是不是同一个人的不同造型档",不判"形象是否相同"。** 同一个人换档就是
// 换服装/状态,像素**不能互相复用** —— 把主卡那张图直接当变体卡的定妆照,变体的服装
// 就永远不会出现在画面里(见 library.Find 的分档守卫)。所以本文件只给"归属",
// 复用与派生各自按自己的口径走。
//
// id 与名字**互相矛盾**时宁可判"不是一个人"(多生成一张卡),也不能把两个不同角色
// 并成一个 —— 那会让两个人共用一张脸(与 library.Find 的既有取舍同一条)。

// charVariantSep 造型档分隔符。
//
// 技能侧实际用的是**间隔号 `·`**(U+00B7,例:「翻篇·归零」);另外容忍全角中点
// `・`(U+30FB)、连字点 `‧`(U+2027)、项目符号 `•`(U+2022)与下划线 `_`
// —— 它们都是同一件事的不同写法(手写档名/导出工具替换字符时会出现)。
// **不含**普通连字符与空格:那两者在正常人名里太常见(「小-黑」「阿 枢」),
// 认成档名会把一个人拆成两个。
func charVariantSep(r rune) bool {
	switch r {
	case '·', '・', '‧', '•', '_':
		return true
	}
	return false
}

// CharVariantLabel 名字里的造型档名(分隔符之后的那一段);空 = 不是变体卡。
//
// 分隔符在首尾(「·归零」/「翻篇·」)时不算档名:那不是"主名·档名"的写法。
func CharVariantLabel(name string) string {
	s := strings.TrimSpace(name)
	for i, r := range s {
		if !charVariantSep(r) {
			continue
		}
		base := strings.TrimSpace(s[:i])
		rest := strings.TrimSpace(s[i+len(string(r)):])
		if base == "" || rest == "" {
			return ""
		}
		return rest
	}
	return ""
}

// CharFamilyBaseName 名字的家族基名(「翻篇·归零」→「翻篇」);没有档名时返回原名。
//
// 与 CharFamilyKey 的分工:这个是**给人看的基名**(前端按人分组的标题),
// 那个是**给程序用的键**(可能取自 id)。
func CharFamilyBaseName(name string) string {
	s := strings.TrimSpace(name)
	for i, r := range s {
		if !charVariantSep(r) {
			continue
		}
		base := strings.TrimSpace(s[:i])
		rest := strings.TrimSpace(s[i+len(string(r)):])
		if base == "" || rest == "" {
			return s // 分隔符在首尾:不是"主名·档名"的写法,原名即基名
		}
		return base
	}
	return s
}

// CharVariantNumberOf id 里的造型档号:0 = 家族主卡(或认不出档位),K>0 = 第 K 档。
//
// 只认 `…<数字>v<数字>`(分隔符 `_`/`-` 可选)这一种写法:主卡编号本身就以数字结尾
// (`char_01`),所以"以 v 接数字结尾"才是档位;否则 `id="abc2"` 这类会被误判成
// "abc 的第 2 档"。
func CharVariantNumberOf(id string) int {
	_, v, ok := charIDFamily(id)
	if !ok {
		return 0
	}
	return v
}

// charIDFamily 拆 id:`char_01v4` → ("char_1", 4, true);`char_01` → ("char_1", 0, true);
// 拆不出家族编号(裸名、`char_01_chibi`)→ ok=false。
func charIDFamily(id string) (string, int, bool) {
	s := strings.TrimSpace(id)
	if s == "" {
		return "", 0, false
	}
	base, variant := s, 0
	// 尾部数字段
	j := len(s)
	for j > 0 && s[j-1] >= '0' && s[j-1] <= '9' {
		j--
	}
	if j < len(s) && j > 0 && (s[j-1] == 'v' || s[j-1] == 'V') {
		k := j - 1
		if k > 0 && (s[k-1] == '_' || s[k-1] == '-') {
			k-- // 容忍 char_01_v4 这种带分隔符的写法
		}
		cand := strings.TrimSpace(s[:k])
		// 档位前缀必须以数字结尾(家族编号),否则 "foov2" 会被当成 "foo 的第 2 档"
		if cand != "" && lastIsDigit(cand) {
			if n, err := strconv.Atoi(s[j:]); err == nil && n > 0 {
				base, variant = cand, n
			}
		}
	}
	nb := normCharIDBase(base)
	if nb == "" || !lastIsDigit(nb) {
		return "", 0, false
	}
	return nb, variant, true
}

// normCharIDBase id 基名归一:小写 + 尾部数字去前导零(`char_01` 与 `char_1` 同族)。
func normCharIDBase(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) || i == 0 {
		return s // 没有数字尾(或整串都是数字):原样
	}
	num := strings.TrimLeft(s[i:], "0")
	if num == "" {
		num = "0"
	}
	return s[:i] + num
}

func lastIsDigit(s string) bool {
	return s != "" && s[len(s)-1] >= '0' && s[len(s)-1] <= '9'
}

// CharFamilyKeys 同一张卡的两个家族口径:
//
//	idKey   机器编号口径(`char_01v4` → `char_1`);id 认不出来时为空
//	nameKey 名字口径     (`翻篇·归零` → `翻篇`);没有档名时就是原名
//
// 为什么两个都留:判"是不是一家人"用 CharFamilyKey(见那里的 id 优先说明),而
// **找派生参考**这类"宁多认一格"的地方要用任一命中的宽松口径 —— 技能侧的 id 是自动
// 编号、名字是手写档名,两边理论上一致,一旦有一边不规范(主卡沿用旧编号),只认单边
// 就会漏判;漏判的后果是变体卡退回文生图(同一角色几张脸会漂),而那正是 §4.1 要防的事。
func CharFamilyKeys(id, name string) (idKey, nameKey string) {
	// Q 版形象**不是造型档**（2026-09-27 技能侧新口径）：造型档是"同一个人按场换服装"，
	// 而 Q 版是"同一个人的另一种画法"，观众靠它认"这段是心里话"。
	// 不排除的话，`chibi_person_01`（名字常常只是主卡名加后缀）会被并进主卡的家族里，
	// 界面上就显示成"沈砚 2 个造型档"——用户看不懂多出来的那张是什么。
	// 给它一个**独占**的家族键（前缀区分），于是它与主卡各自成组、互不影响。
	if IsChibiCard("", id) {
		return "chibi:" + strings.TrimSpace(id), ""
	}
	if base, _, ok := charIDFamily(id); ok {
		idKey = base
	}
	nameKey = CharFamilyBaseName(name)
	return idKey, nameKey
}

// CharFamilyKey 家族键(唯一判据):id 能解析时**以 id 为准**,否则用名字基名。
//
// 为什么 id 优先:id 是机器写的,名字是人(或 LLM)写的 —— 同一角色换个称呼在分集
// 分镜里很常见。id 与名字矛盾时不合并,是"多生成一张卡"而不是"两个人共用一张脸"。
func CharFamilyKey(id, name string) string {
	idKey, nameKey := CharFamilyKeys(id, name)
	if idKey != "" {
		return idKey
	}
	if nameKey != "" {
		return nameKey
	}
	return strings.TrimSpace(id)
}

// SameCharacter 两张卡是不是**同一个人**(同角色的不同造型档也算同一个人)。
//
// 【2026-09-28 补：两边混用时的漏洞】此前只比 `CharFamilyKey`（id 优先）——
// 主卡 id 规范（`char_01`）、档卡 id 不规范（`char_01_young` 这类没有档位号）时，
// 主卡按 **id** 得到家族键、档卡按 **名字** 得到另一个键，**永远碰不上** ✗。
// 后果不只是计数：撞脸自检会把"同一个人的少年期与成年期"当成两个角色比，
// 报出"这两个人长得像同一个人"（用户报过的那类假警的另一种形态）。
//
// 补的规则**只在 id 说不出话时**生效：
//
//	(a) 两边 id 家族都能解析 → 相同才算同一个人（既有取舍：id 是人写的机器编号，
//	    它说不同就宁可多生成一张卡，也不能让两个不同角色共用一张脸）；
//	(b) 有一边 id 家族解析不出来（id 本身没信息）→ 只要**名字基名**相同就算同一个人。
func SameCharacter(aID, aName, bID, bName string) bool {
	aIDKey, aNameKey := CharFamilyKeys(aID, aName)
	bIDKey, bNameKey := CharFamilyKeys(bID, bName)
	if aIDKey != "" && bIDKey != "" {
		return aIDKey == bIDKey
	}
	if aNameKey != "" && aNameKey == bNameKey {
		return true
	}
	// 名字也给不出结论时退回 id 家族（至少有一边可解析的情况上面已覆盖，这里兜底）
	return aIDKey != "" && aIDKey == bIDKey
}

// CharFamilyIndex 一批角色卡 → **卡片 id → 归一后的家族键**（同一个人拿到同一个键）。
//
// 为什么要有它（2026-09-28）：`CharFamilyKey` 是**单卡**口径（id 优先），而"归族"是
// **成对**关系 —— 上面 SameCharacter 那条"一边 id 说不出话就看名字"的规则，用单键没法表达
// （A 与 B 同族、B 与 C 同族，A 与 C 也必须同族）。这里用并查集把两种口径合起来，
// 于是首页角色数、界面分组、撞脸比对三处**共用同一份归族结果**，不会再各写一遍。
//
// Q 版形象独立成族（不是造型档，见 CharFamilyKeys 的说明）。
func CharFamilyIndex(chars []Character) map[string]string {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		p, ok := parent[x]
		if !ok {
			parent[x] = x
			return x
		}
		if p == x {
			return x
		}
		r := find(p)
		parent[x] = r
		return r
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	// 先建节点：每张卡用自己的 id 当节点名（id 为空时退回名字）
	node := func(c Character) string {
		if s := strings.TrimSpace(c.ID); s != "" {
			return "card:" + s
		}
		return "name:" + strings.TrimSpace(c.Name)
	}
	byIDKey := map[string]string{}
	byNameKey := map[string]string{}
	for _, c := range chars {
		n := node(c)
		find(n)
		idKey, nameKey := CharFamilyKeys(c.ID, c.Name)
		if idKey != "" {
			if first, ok := byIDKey[idKey]; ok {
				union(n, first)
			} else {
				byIDKey[idKey] = n
			}
		}
		if nameKey != "" {
			if first, ok := byNameKey[nameKey]; ok {
				union(n, first)
			} else {
				byNameKey[nameKey] = n
			}
		}
	}
	// 再把"同一名字但两边 id 都能解析且不同族"的情形拆开：
	// 名字并了、而 id 明确说不是一个人 ⇒ 以 id 为准（既有取舍）。
	for _, c := range chars {
		for _, d := range chars {
			if c.ID == d.ID {
				continue
			}
			ci, _ := CharFamilyKeys(c.ID, c.Name)
			di, _ := CharFamilyKeys(d.ID, d.Name)
			if ci != "" && di != "" && ci != di {
				// 两张 id 明确不同族的卡不能同族 —— 若被名字并到了一起，按 id 拆开
				if find(node(c)) == find(node(d)) {
					parent[find(node(d))] = node(d)
				}
			}
		}
	}
	// 每组取一个**人可读的规范标签**（不是内部节点名）：
	// 优先用组内最小的 id 家族键（`char_1` 这种，与旧行为一致 —— 界面与投影都在用它），
	// 没有 id 家族键时才用名字基名。**要确定性**：同一批卡换顺序也得给出同一个标签，
	// 否则前端的分组会随数据顺序抖。
	type canon struct {
		idKey, nameKey, fallback string
	}
	groups := map[string][]canon{}
	for _, c := range chars {
		idKey, nameKey := CharFamilyKeys(c.ID, c.Name)
		fallback := strings.TrimSpace(c.ID)
		if fallback == "" {
			fallback = "name:" + strings.TrimSpace(c.Name)
		}
		root := find(node(c))
		groups[root] = append(groups[root], canon{idKey: idKey, nameKey: nameKey, fallback: fallback})
	}
	label := map[string]string{}
	for root, members := range groups {
		best := ""
		for _, m := range members {
			if m.idKey != "" && (best == "" || m.idKey < best) {
				best = m.idKey
			}
		}
		if best == "" {
			for _, m := range members {
				if m.nameKey != "" && (best == "" || m.nameKey < best) {
					best = m.nameKey
				}
			}
		}
		if best == "" {
			for _, m := range members {
				if best == "" || m.fallback < best {
					best = m.fallback
				}
			}
		}
		label[root] = best
	}
	out := make(map[string]string, len(chars))
	for _, c := range chars {
		out[c.ID] = label[find(node(c))]
	}
	return out
}

// CharFamilyCount 一批角色卡里有几个「人」(同一角色的多张造型档算一个人)。
//
// 用于一切"给人看的角色数":首页统计、方案概览、资产阶段播报。按卡计数会把
// 《翻篇》的 12 个角色报成 21 个(4 档翻篇 + 3 档糖球 + 2 档老墙),用户看到的就是
// "怎么多出来一堆新角色"。
func CharFamilyCount(chars []Character) int {
	seen := map[string]bool{}
	for _, k := range CharFamilyIndex(chars) {
		seen[k] = true
	}
	return len(seen)
}
