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

// charIDFamily 拆 id：家族基键 + 档号。
//
// 【2026-09-28 修 · 用户报「同一个人判成两个 / 两个不同的人判成一个」】
// 原实现要求**基名以数字结尾**（只认 `char_01` / `char_01v4`）—— 那是早期"角色 id 一律
// `char_NN`"时代的假设。现在技能侧的卡片 id 是**命名式**（`char_bingsheng`、`char_qiqi`），
// 于是这里 `ok=false`、家族键**静默退回名字**，两个方向同时出错：
//
//   - 同一个人的两种写法（id `char_qiqi` ↔ 名字「祁七」）→ 家族键不同 → **判成两个人**
//     （首页角色数虚高、"变体排到主卡之后"不成立）；
//   - **两个不同的人同名** → 名字基名相同 → **判成一个**（撞脸自检把他们当自己人排除掉，
//     共用一张脸反而没人报 —— 这是"跨集换脸"最隐蔽的形态）。
//
// 现在：**有 id 就以 id 为家族键本体**（去掉 `char:` 前缀与档位后缀）；名字只在"压根没有 id"
// 时兜底。带 `v` 的档位后缀仍然认，但**必须带分隔符**（`_v2` / `-v2`），
// 免得 `foov2` 这种被当成"foo 的第 2 档"。
func charIDFamily(id string) (string, int, bool) {
	s := strings.TrimSpace(id)
	// **只有带卡片前缀的才是卡片 id**（`char_xxx` / `char:char_xxx`）。
	// 裸名（`abc`、`沈砚`）不是 —— 它可能是模型随手写的、也可能是名字被误塞进 ID 字段，
	// 拿它当家族键会让"同一个人的两张卡"永远碰不上（`char_family_test.go` 的
	// 「id 认不出」用例钉着这条）。
	if !strings.HasPrefix(s, "char:") && !strings.HasPrefix(s, "char_") {
		return "", 0, false
	}
	s = strings.TrimPrefix(s, "char:")
	if s == "" {
		return "", 0, false
	}
	base, variant := s, 0
	// 档位后缀 `_v2` / `-v2`：**分隔符可有可无**（`char_01v2` 与 `char_01_v2` 都认），
	// 但**档位前的部分必须以数字结尾** —— 否则 `foov2` 会被当成"foo 的第 2 档"。
	if i := variantSuffixIndex(s); i > 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil && n > 0 {
			// 去掉档位前的分隔符：`char_01_v2` → `char_01`（留下尾下划线会让它认不出家族）
			base, variant = strings.TrimRight(s[:i], "_-"), n
		}
	}
	nb := normCharIDBase(base)
	if nb == "" {
		return "", 0, false
	}
	return nb, variant, true
}

// variantSuffixIndex 找出档位后缀 `v2` 里 `v`/`V` 的下标（找不到、或档位前的部分不以数字
// 结尾，都返回 -1）。
//
// 与旧实现的唯一差别：**不再要求 `v` 前面有分隔符**。旧版只认 `char_01_v4`、不认
// `char_01v2`（而真实卡片写的正是后者），于是变体卡排不到主卡之后。分隔符由下面那句
// "档位前必须以数字结尾"来兜底，`foov2` 依然不会被误判。
func variantSuffixIndex(s string) int {
	j := len(s)
	for j > 0 && s[j-1] >= '0' && s[j-1] <= '9' {
		j--
	}
	if j == len(s) || j == 0 {
		return -1 // 没有数字尾
	}
	k := j - 1
	if s[k] != 'v' && s[k] != 'V' {
		return -1
	}
	cand := strings.TrimRight(s[:k], "_-")
	if cand == "" || !lastIsDigit(cand) {
		return -1 // 档位前不是家族编号
	}
	return k
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
	// 【2026-09-28 重写 · 用户报「同一个人判成两个 / 两个不同的人判成一个」】
	//
	// 旧实现是"两边 id 都能解析时**只看 id**"，而当时"能解析"被定义成"id 以数字结尾" ——
	// 那个定义把**命名式卡片 id**（`char_bingsheng`、`char_qiqi`，也就是技能侧现在实际用的形态）
	// 判为"解析不了"，家族键静默退回名字，于是两个方向同时错：
	//   同名不同人 → 判成一个；同人不同写法 → 判成两个。
	//
	// 现在的规则按**四种情形**分开（每条都由 char_family_stage_test.go 的用例钉着）：
	// ① 两边 id 都说得出来且相同            → 同一个（这是最可靠的一路）
	// ② 只有一边有 id（另一张卡没写 id）    → 名字兜底：那是同一个人的两张卡
	// ③ 两边 id 不同 → 只有当名字是"档名关系"（`沈砚` ↔ `沈砚·少年`）才算同一个：
	//    那是"同一个人按场换服装"；**两个不同的人恰好同名时名字没有档名** → 判不同
	// ④ 其余 → 不同
	if aIDKey != "" && bIDKey != "" {
		if aIDKey == bIDKey {
			return true
		}
		return aNameKey != "" && aNameKey == bNameKey && nameHasVariantSuffix(aName, bName)
	}
	if (aIDKey == "") != (bIDKey == "") {
		return aNameKey != "" && aNameKey == bNameKey
	}
	return aNameKey != "" && aNameKey == bNameKey && nameHasVariantSuffix(aName, bName)
}

// nameHasVariantSuffix 两个名字里是否**至少有**一个带档名（`沈砚·少年` 的 `·少年`）。
//
// 判据用 CharFamilyBaseName 反推（基名 != 原名 ⇒ 带档名），不另立一套分隔符表 ——
// 名字的档名写法只该有一个定义处。
func nameHasVariantSuffix(names ...string) bool {
	for _, n := range names {
		s := strings.TrimSpace(n)
		if s != "" && CharFamilyBaseName(s) != s {
			return true
		}
	}
	return false
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
	// find 用**迭代**而不是递归，并且**带步数上限**。
	//
	// 【2026-09-28 实测】原实现是无上限递归，一旦 parent 里出现互相指向的两个节点，
	// 无上限递归会一路把栈打爆 —— 整个测试进程 `fatal error: stack overflow`，
	// 连"哪个用例出问题"都看不到。改成迭代 + 上限之后，最坏情况是把某个节点自立为根，
	// 而不是把进程干掉；归族结果哪怕退化了也还能看、还能报。
	find := func(x string) string {
		root := x
		for i := 0; i <= len(parent)+1; i++ {
			p, ok := parent[root]
			if !ok {
				parent[root] = root
				return root
			}
			if p == root {
				return root
			}
			root = p
		}
		parent[x] = x // 兜底：真出现环，把 x 立为根，不让它继续扩散
		return x
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
	nameOf := map[string]string{} // nameKey → 该 key 首个卡的名字（判"档名关系"用）
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
				// **只有"档名关系"才按名字合并**：两个不同的人恰好同名时 nameKey 也相同，
				// 无条件合并会把两个人并成一个（`char_family_stage_test.go` 的 ⑤ 号用例：
				// `char_01/沈砚` 与 `char_02/沈砚` 必须判成两个人）。
				//
				// 这里替掉了原来"先无条件并、再事后按 id 拆"的写法 —— 那段补丁做的是
				// `parent[find(x)] = x`，也就是**把别人的根指向一个非根节点**，会破坏并查集；
				// 一旦因此成环，`find` 的无上限递归直接把进程打到 `stack overflow`
				// （2026-09-28 实测：整个 project 包测试跑不起来，连哪个用例出问题都看不到）。
				if nameHasVariantSuffix(c.Name) || nameHasVariantSuffix(nameOf[nameKey]) {
					union(n, first)
				}
			} else {
				byNameKey[nameKey] = n
				nameOf[nameKey] = c.Name
			}
		}
	}
	// 注：这里**不再**需要"事后按 id 把并错的人拆开"那一步 —— 合并时已经只认档名关系，
	// 而"两个 id 明确不同族的人"从一开始就不会被并到一起。

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
