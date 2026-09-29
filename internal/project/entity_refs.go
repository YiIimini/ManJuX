package project

import (
	"sort"
	"strings"
)

// entity_refs.go 分镜里 `scene` / `characters` 的**名字 → id 归一**（2026-09-29 真机实测驱动）。
//
// 【实测缺陷】开了「五步专职会话」（`split.five_sessions`）那条路上，⑤分镜脚本的
// 输出 schema 写的是 `"scene":"场景名","characters":["角色名"]`（见
// `internal/director/sessions.go` 的 sessionOutputSchema），而 `Shot.Scene` /
// `Shot.Characters` 的字段语义是 **id** —— 渲染装配按 `assets["char:"+cid]` 与
// `assets["scene:"+sid]` 取参考图（`pipeline.RefPlan`）。两边对不上，后果是**整集一张
// 参考图都装配不上**：实测第 01 集 13 镜**全部**降级成 t2va
// （日志「镜头 1 无参考图,降级 t2va」），角色逐镜换脸、场景每镜重编 ——
// 而定妆照与场景板明明都已生成、稳稳躺在项目里（`assets/asset_map.json` 的键是
// `char:char_qiqi` / `scene:scene_maintenance_bay`）。
//
// 【为什么由平台归一，而不是让模型写对】名字 → id 是**机械约定**，不是创作内容；
// 平台手上有卡片（id ＋ 名字），归一这件事它是唯一能确定做对的角色。与 `RemapPropRefs`
// 同一口径、同一个入口（`HealPlan`）：能对上的对上，**对不上的原样留着并计数**
// （群演、临时场景本来就允许不在卡片表里，静默丢弃会让提示词与引用对不上）。
//
// 幂等：已经是 id 的引用原样保留，所以"读一次修一次"不会反复改写方案。
//
// 【为什么必须在读取边界做】与器物对号同源（2026-09-28 的教训）：预检 / 人审 / 渲染
// 三个消费方必须看到**同一份**方案 —— 只在渲染里修，预检仍会报"引用了不存在的角色"。

// RemapEntityRefs 归一分镜里的人物/场景引用，返回（归一了几处, 仍对不上卡片的引用数,
// 其中按**字面重合**猜对号的处数）。
//
// 第三个返回值是给**播报**用的：字面重合是**猜**（名字与卡片名不完全一致），
// 自愈必须如实说出来（本仓纪律：自愈"改了什么"要播报），否则用户不知道
// "这一镜的场景参考图是按名字猜出来的"。
func RemapEntityRefs(plan *Plan) (fixed, unknown, loose int) {
	if plan == nil {
		return 0, 0, 0
	}
	chars, scenes := entityRefIndex(plan)
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if raw := strings.TrimSpace(sh.Scene); raw != "" {
			if id, ok, guessed := scenes.lookupRef(raw); ok {
				if id != raw {
					sh.Scene = id
					fixed++
					if guessed {
						loose++
					}
				}
			} else {
				unknown++
			}
		}
		for j, raw := range sh.Characters {
			cid := strings.TrimSpace(raw)
			if cid == "" {
				continue
			}
			if id, ok, guessed := chars.lookupRef(cid); ok {
				if id != cid {
					sh.Characters[j] = id
					fixed++
					if guessed {
						loose++
					}
				}
				continue
			}
			unknown++
		}
	}
	return fixed, unknown, loose
}

// lookupRef 与 lookup 同，另外告诉调用方"这一条是不是**猜**出来的"。
func (ix *entityIndex) lookupRef(raw string) (id string, ok bool, guessed bool) {
	if id, ok := ix.lookup(raw); ok {
		return id, true, false
	}
	if id, ok := ix.loose(raw); ok {
		return id, true, true
	}
	return "", false, false
}

// DanglingEntityRefs 分镜里**对不上卡片**的人物 / 场景引用数（归一化之后判；
// 归一化会把"写的是名字"这一类对上号，剩下的才是真的没有卡）。
//
// 与 `DanglingPropRefs` 同一用途：契约门据此播报。人物引用**不丢弃**（群演、临时角色
// 本来就允许不在卡片表里），只是渲染时拿不到参考图 —— 这正是要说出来的事。
func DanglingEntityRefs(plan *Plan) (chars, scenes int) {
	if plan == nil {
		return 0, 0
	}
	chIdx, scIdx := entityRefIndex(plan)
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if raw := strings.TrimSpace(sh.Scene); raw != "" {
			if _, ok := scIdx.lookup(raw); !ok {
				scenes++
			}
		}
		for _, raw := range sh.Characters {
			cid := strings.TrimSpace(raw)
			if cid == "" {
				continue
			}
			if _, ok := chIdx.lookup(cid); !ok {
				chars++
			}
		}
	}
	return chars, scenes
}

// entityRefIndex 人物与场景各建一份"引用键 → 卡片 id"的索引。
func entityRefIndex(plan *Plan) (chars, scenes *entityIndex) {
	chars = newEntityIndex()
	for _, c := range plan.Characters {
		chars.add(c.ID, c.Name)
	}
	scenes = newEntityIndex()
	for _, s := range plan.Scenes {
		scenes.add(s.ID, s.Name)
	}
	return chars, scenes
}

// entityIndex 两套键：
//
//	exact —— 归一后的**全名**（id、去前缀 id、名字）
//	base  —— 去掉括注与造型档名后的**基名**（「祁七（主角）」→ 祁七）
//
// 为什么要有 base：分镜里的名字常带注解（「祁七（主角）」「机关人(一三七四一号)」），
// 而卡片名是干净的 —— 只按全名比会对不上，而那正是"拿不到参考图"的入口。
// 基名撞车（同一个基名对到两张卡）时**不用基名匹配**：宁可报"对不上"，
// 也不能把 A 的定妆照锚到 B 身上（本仓在角色家族那一处已经吃过认错人的亏）。
type entityIndex struct {
	exact map[string]string
	base  map[string]string
	dup   map[string]bool
}

func newEntityIndex() *entityIndex {
	return &entityIndex{exact: map[string]string{}, base: map[string]string{}, dup: map[string]bool{}}
}

func (ix *entityIndex) add(id, name string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	for _, k := range []string{
		normEntityKey(id),
		normEntityKey(strings.TrimPrefix(id, "char:")),
		normEntityKey(strings.TrimPrefix(id, "scene:")),
		normEntityKey(strings.TrimPrefix(id, "prop:")),
		normEntityKey(name),
	} {
		if k == "" {
			continue
		}
		if _, ok := ix.exact[k]; !ok {
			ix.exact[k] = id
		}
	}
	if b := entityBaseKey(name); b != "" {
		if prev, ok := ix.base[b]; ok && prev != id {
			ix.dup[b] = true
		} else {
			ix.base[b] = id
		}
	}
}

// lookup 按"归一全名 → 基名"的顺序找一个 id（**不含**字面重合兜底）。
func (ix *entityIndex) lookup(raw string) (string, bool) {
	for _, k := range []string{normEntityKey(raw), entityBaseKey(raw)} {
		if k == "" {
			continue
		}
		if id, ok := ix.exact[k]; ok {
			return id, true
		}
		if !ix.dup[k] {
			if id, ok := ix.base[k]; ok {
				return id, true
			}
		}
	}
	return "", false
}

// loose 字面重合兜底（2026-09-29 真机实测驱动）。
//
// 【为什么要它】⑤分镜脚本是按**场景名**引用场景的，而它此前与 ④场景 会话**并行**跑 ——
// 场景名还没定，模型就自己起了一个。实测第 01 集：
//
//	分镜写的是「归墟环断口维护舱」，而场景卡是「归墟环维护舱大厅 / 东头值班终端台 / 舱门裂隙外的灰雾」
//	⇒ 名字对不上 ⇒ 那一镜装配不到场景参考图，日志「⚠ 镜头 1 缺 1 张参考图(归墟环断口维护舱)」，
//	  环境每镜重编、背景漂移。
//
// 并行那件事已在生成侧改成"④ 回来再跑 ⑤"（治根），这里治**存量方案**与"模型就是写歪了"：
// 按**汉字双字组重合度**取最像的那张卡。
//
// 纪律（与基名撞车同一条）：**必须有一个明确赢家**才认 —— 分数 ≥2 且严格高于第二名，
// 否则宁可报"对不上"。把 A 的场景板锚到 B 身上，比没有参考图更糟。
func (ix *entityIndex) loose(raw string) (string, bool) {
	q := cjkBigrams(raw)
	if len(q) < 2 {
		return "", false
	}
	keys := make([]string, 0, len(ix.exact))
	for k := range ix.exact {
		keys = append(keys, k)
	}
	sort.Strings(keys) // map 遍历无序 —— 同分判定的行为必须稳定
	bestKey, bestScore, tie := "", 0, false
	for _, k := range keys {
		if strings.ContainsRune(k, '_') || !hasCJK(k) {
			continue // id 形态的键（scene_ring_bay）不参与字面比对
		}
		score := 0
		for bg := range cjkBigrams(k) {
			if q[bg] {
				score++
			}
		}
		switch {
		case score > bestScore:
			bestKey, bestScore, tie = k, score, false
		case score == bestScore && score > 0:
			tie = true
		}
	}
	if bestKey == "" || bestScore < 2 || tie {
		return "", false
	}
	return ix.exact[bestKey], true
}

// cjkBigrams 汉字双字组（标点与 ASCII 一律不算）。
func cjkBigrams(s string) map[string]bool {
	rs := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			rs = append(rs, r)
		}
	}
	out := make(map[string]bool, len(rs))
	for i := 0; i+1 < len(rs); i++ {
		out[string(rs[i:i+2])] = true
	}
	return out
}

func hasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// entityBaseKey 名字的"基名键"：去掉括注、再去掉造型档名，最后归一。
func entityBaseKey(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	s = stripBracketed(s)
	s = CharFamilyBaseName(s)
	return normEntityKey(s)
}

// stripBracketed 去掉名字里的括注段（中英文括号与方头括号都算）。
// 「祁七（主角）」→「祁七」；「机关人(一三七四一号)」→「机关人」。
func stripBracketed(s string) string {
	pairs := map[rune]rune{'（': '）', '(': ')', '[': ']', '【': '】', '《': '》', '「': '」'}
	var b strings.Builder
	var closing rune
	for _, r := range s {
		if closing != 0 {
			if r == closing {
				closing = 0
			}
			continue
		}
		if c, ok := pairs[r]; ok {
			closing = c
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// normEntityKey 归一键：去空白、下划线、连字符与常见中英文标点后小写。
// 分镜里的"场景名"常带书名号/引号/尾注，归一之后才对得上卡片名。
func normEntityKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '_', '-', '—', '·', '.', '．', '、', '，', ',', '。',
			'「', '」', '《', '》', '“', '”', '"', '\'', '(', ')', '（', '）', '[', ']',
			'【', '】', ':', '：', '/', '\\', '|', '+':
			continue
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}
