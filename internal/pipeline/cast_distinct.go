package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// cast_distinct.go **撞脸自检**：同一部剧里两个角色不能被画成同一个人。
//
// ## 由来（2026-09-27 用户报「周廷和主角脸相似，这不合理吧」）
//
// 查证：两边的外形锁分得很开 —— 沈砚 19 岁、瘦长脸、凤眼、浅麦色；周廷 31 岁、方脸、
// 高颧骨、深眼窝、蜡黄。可两张定妆照看起来是同一个人（都是瘦长脸的年轻人，连发髻都差不多）。
// 也就是说**提示词写对了，渲染器没照做**：
//
//   - 平台自己的画风块里写着 "idealised… simplified, readable features" —— 那是往
//     **一个模板**上拉的力，专治"网红脸"却也把"31 岁方脸"抹平成"年轻瘦长脸"；
//   - 而"要跟别人不一样"这件事此前**没有任何验证**：读提示词的检查看不见五官，
//     比例自检只管三庭/眼距/头身比（都正常），朝向自检只管是不是正面 —— 三张网都漏。
//
// 所以这一层是**看图判身份**（与多视角身份校验同一手法，复用设置页的同一份 VLM 配置）：
// 新出的定妆照与同剧已生成的角色逐张比一次，判成"同一个人"就带**分离指令**换种子重出一次。
//
// ## 口径
//
//   - 开关：跟质检总开关与 VLM 配置走（没配 VLM → 如实降级，**不当作通过**）；
//   - 失败（读图/调用/解析）→ 告警说"未判定"，不当作通过；
//   - 重出**只给一次**：与朝向自检同一政策（换种子+加强指令后仍撞脸就如实告警收手，
//     让人在资产详情里点「重新生成」——那条路是随机种子，能再抽一次）；
//   - 比对**最多 3 个**已生成角色：撞脸通常发生在"同性别同年龄段的前几个主要角色"之间，
//     全比一遍是 N² 次视觉调用，不值当（顺序生成时前面的角色已经比过了）。
const maxCastCompare = 3

// castCompareLimit 一次比对里最多看几张别人的定妆照。
type castPeer struct {
	name string
	id   string // 卡片 id：同名不同卡时报错信息里要能分清是谁（实测栽过一次）
	path string
}

// normalizeCardID 把各种拼法归一到**裸卡 id**：去空白、去干净重复的 "char:" 前缀。
//
// 为什么需要它：同一份代码里"卡 id"有几种形态在流动 —— 方案里是裸的 `char_01`，
// 资产表 key 是 `char:char_01`，而调用点有的传裸的、有的传带前缀的。2026-09-28 实测栽在
// "调用点传资产 id、被调方又拼一次前缀"上：双前缀 ⇒ 下面三道"排除自己"的判据全部落空
// ⇒ 碑生被拿去跟自己比 ⇒ 报出「碑生（丙十七）与碑生（丙十七）长得像同一个人」。
func normalizeCardID(id string) string {
	id = strings.TrimSpace(id)
	for strings.HasPrefix(id, "char:") {
		id = strings.TrimPrefix(id, "char:")
	}
	return id
}

// castPeers 挑出"已经生成好的其他角色定妆照"（排除自己、排除 Q 版等派生资产）。
// castPeers 挑**同剧其他角色**的已出图定妆照作比对对象。
//
// 【2026-09-27 用户报障 · 拿自己跟自己比】实测某集报了
//
//	⚠ 定妆照「碑生」与「碑生」**长得像同一个人** …请重出其中一张
//
// 同一张脸当然像同一个人 —— 那是**同一个角色的另一张造型档卡**（同一个人换服装），
// 而这里此前只按 **id** 排除自己，造型档的 id 不同、于是被当成"别人"拉来比 ✗。
// 判据改成平台既有的"是不是同一个人"（`project.SameCharacter`，与首页角色数、
// 造型档派生同一套家族键）；名字相同的一律不比（同名两张本来就没法从告警里分清是谁）。
//
// 【2026-09-28 用户报障 · 又是自己跟自己比，这次是**同一张卡**】告警里两边连卡片 id
// 都一模一样（`char:char_bingsheng`）。根因不是手法，是**参数口径**：stages_assets.go
// 的 check() 传下来的 id 已经是资产 id（`char:char_bingsheng`），这里又拼了一次 "char:"
// —— selfID 成了双前缀，于是 `id == selfID`、按 id 查 selfName、按名字排除三道判据**同时失效**，
// 自己进了比对名单（assets 的 key 恰好又是单前缀，照样取到了自己的图）。
// 修法：selfID 一律过 normalizeCardID 归一到裸卡 id 再比，不再假设调用点给的是哪种形态。
func castPeers(projDir string, plan *project.Plan, assets map[string]project.Asset, selfID string) []castPeer {
	if plan == nil {
		return nil
	}
	selfCardID := normalizeCardID(selfID)
	var selfName, selfKind string
	for _, ch := range plan.Characters {
		if normalizeCardID(ch.ID) == selfCardID {
			selfName = ch.Name
			selfKind = ch.Kind
			break
		}
	}
	// 自己是不是 Q 版派生卡（是的话，与本人的主卡**不能比** —— 那是同一个人）
	selfIsChibi := project.IsChibiCard(selfKind, selfCardID)
	var out []castPeer
	for _, ch := range plan.Characters {
		id := "char:" + ch.ID
		// Q 版**永远不比**（2026-09-28 用户：「不能自己跟自己进行判断」）。
		//
		// 【这里原来错在哪】旧代码用 `strings.HasSuffix(ch.ID, "_chibi")` 排除 —— 那是**后缀**写法，
		// 而技能侧的命名是**前缀** `chibi_<原角色id>`（`chibi_char_01` = 沈砚·Q版）。于是 Q 版
		// 根本没被排除：本人和自己的 Q 版被拿去互比，而 Q 版本来就是同一个人 ⇒ 必然报
		// 「定妆照「碑生」与「碑生」长得像同一个人」。
		// 改用平台自己那套判据 project.IsChibiCard（kind=="chibi" 或 chibi_ 前缀，两处口径合一），
		// 顺带覆盖"kind 标了 chibi 但 id 没按约定写"的卡。
		// 判据统一走 `project.ChibiBaseName`（id 与名字的三种写法一次认全：`chibi_x` / `x_chibi` /
		// 「Q版x」 / 「x·Q版」）。
		//
		// 【2026-09-29 用户：「你看一下下面的判断逻辑咋还没有改好？」】真机报的是
		//	⚠ 定妆照「Q版碑生（卡片 char_q_bisheng）」与「碑生（丙十七）（卡片 char_bisheng）」
		//	  **长得像同一个人** …
		// 技能侧这次用的是 `char_q_bisheng` + 名字「Q版碑生」（**前缀**写法），而只认
		// `chibi_`/`_chibi`/「·Q版」后缀的判据全部落空 ⇒ Q 版又被拿去和本人比。
		// 教训与上一次同形：**判据认的写法比实际写法少** —— 所以口径收成 project.ChibiBaseName 一处。
		if normalizeCardID(ch.ID) == selfCardID ||
			project.IsChibiCard(ch.Kind, ch.ID) ||
			project.ChibiBaseName(ch.ID, ch.Name) != "" {
			continue
		}
		// **自己就是 Q 版 ⇒ 谁都不比**（2026-09-29）：Q 版的五官是按本人"简化"出来的，
		// 跟本人比必然得到"像同一个人"，跟别的角色比也没有意义（画法档位不同）。
		// 与上面"比对对象是 Q 版"那条对称 —— 两边一起排干净，才不会再冒出这类结论。
		if selfIsChibi {
			continue
		}
		// **自己是 Q 版**：本人的 Q 版与本人**必然**同脸 —— 它就是同一个人（Q 版形象是本人
		// 内心演出用的派生素材）。2026-09-29 真机报障：给「碑生·Q版（肩上巴掌大的自己）」
		// 重出之后，平台一本正经地报
		//
		//	⚠ 重出后「碑生·Q版（肩上巴掌大的自己）」仍与「碑生（丙十七）」像同一个人
		//	  （脸型、眼型、眉形一致，额前碎发和额头印记相同，仅年龄不同。）
		//
		// —— 用户回了一句「这判断结论给我搞笑了」。上面那道 `IsChibiCard(ch…)` 只排**比对对象**
		// 是 Q 版的情况，没排**自己**是 Q 版的情况，于是 Q 版被拿去跟本人的主卡比。
		// 判据按 id 与名字两条（技能侧 id 是 `chibi_<原id>` 前缀、名字是「<原名>·Q版」）。
		if selfIsChibi {
			if base := chibiBaseID(selfCardID); base != "" && base == normalizeCardID(ch.ID) {
				continue
			}
			if b := chibiBaseName(selfName); b != "" && b == project.CharFamilyBaseName(ch.Name) {
				continue
			}
		}
		// 反方向：比对对象是 Q 版而自己是它的主卡 —— 同理，不算"别的角色"
		if peerBase := chibiBaseID(normalizeCardID(ch.ID)); peerBase != "" && peerBase == selfCardID {
			continue
		}
		// 同一个人的另一档（或同名）：不是"别的角色"，比了只会得到"长得像同一个人"
		//
		// 【2026-09-28 审计 B9】这里原来还有**第二条**判据：`ch.Name == selfName` 就 `continue`。
		// 那一条在 `SameCharacter` 修好之前是"兜底"，修好之后它变成了**漏洞**：
		// 两个**恰好同名但确实是不同人**的角色（各有自己的卡片 id）会被它排除掉 ——
		// 于是撞脸自检对他们**根本不跑**，两人共用一张脸时没有任何告警。
		// 正确性只该由 `SameCharacter`（它按 id 判、只在"档名关系"时按名字合并）负责，
		// 多一条按名字的兜底就是多一个静默失效点。
		if project.SameCharacter(selfCardID, selfName, ch.ID, ch.Name) {
			continue
		}
		a, ok := assets[id]
		if !ok || strings.TrimSpace(a.File) == "" {
			continue
		}
		p := filepath.Join(projDir, a.File)
		if _, err := os.Stat(p); err != nil {
			continue // 还没出图 / 被删了：拿不到图就不比
		}
		out = append(out, castPeer{name: ch.Name, id: ch.ID, path: p})
		if len(out) >= maxCastCompare {
			break
		}
	}
	return out
}

// selfChibi 这张卡自己是不是 Q 版（按 id 找它的 kind）。
func selfChibi(plan *project.Plan, cardID string) bool {
	if plan == nil {
		return false
	}
	for _, ch := range plan.Characters {
		if ch.ID == cardID {
			return project.IsChibiCard(ch.Kind, ch.ID)
		}
	}
	return project.IsChibiCard("", cardID)
}

// chibiBaseID 把 Q 版派生卡的 id 还原成主卡 id（不是 Q 版就返回空串）。
//
// 技能侧两种写法都认：前缀 `chibi_char_bingsheng`、后缀 `char_bingsheng_chibi`。
func chibiBaseID(id string) string {
	s := normalizeCardID(id)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "chibi_") {
		return strings.TrimPrefix(s, "chibi_")
	}
	if strings.HasSuffix(s, "_chibi") {
		return strings.TrimSuffix(s, "_chibi")
	}
	return ""
}

// chibiBaseName 把「碑生·Q版（肩上巴掌大的自己）」还原成基名「碑生」（不是 Q 版就返回空串）。
func chibiBaseName(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	// 先在**原名**上认 Q 版（剥完括注与档名之后就看不出来了）
	if !strings.Contains(strings.ToUpper(s), "Q版") && !strings.Contains(strings.ToUpper(s), "Q 版") {
		return ""
	}
	// 「碑生·Q版（肩上巴掌大的自己）」→（去括注）→「碑生·Q版」→（去档名）→「碑生」
	if i := strings.IndexAny(s, "（(【["); i > 0 {
		s = s[:i]
	}
	return project.CharFamilyBaseName(s)
}

// peerLabelByID 从比对对象里取某张卡的 id（告警里用来消除同名歧义）。
func peerLabelByID(peers []castPeer, name string) string {
	for _, p := range peers {
		if p.name == name {
			return p.id
		}
	}
	return ""
}

// checkCastDistinctness 与已生成的其他角色比一次脸；撞脸则**带分离指令换种子重出一次**，
// 返回最终可用的图路径（不撞、没条件比、或重出后仍撞都返回原路径 —— 重出失败不吞掉原图）。
// 返回 (最终图路径, 是否"没能力验")。**没能力那条由调用方记账**（与比例自检同一政策：
// "一次运行只提醒一次"是本次运行的性质，而 Coordinator 是进程级单例）。
func (c *Coordinator) checkCastDistinctness(ctx context.Context, proj *project.Project, assets map[string]project.Asset,
	kind, id, name, imagePrompt string, ek EntityKind, path string,
	gen assetGenerator) (string, bool) {
	if kind != "characters" || ctx.Err() != nil || strings.TrimSpace(path) == "" {
		return path, false
	}
	// 与比例自检同一条三态口径：关了 → 连事件都不发；没配 VLM → 如实说"没验"。
	if !proj.Config.Params.QC.Enabled {
		return path, false
	}
	if TurnaroundVerifier(c.cfg.Get().VLM) == nil {
		return path, true
	}
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		return path, false // 读不到方案就拿不到"别人是谁"：少比几个人，不是没能力
	}
	// 本卡是 Q 版 ⇒ 不做撞脸判定：Q 版与它的主卡是**同一个人**（比了必然"像"），
	// 而 Q 版之间的相似是画风使然、不是身份事故（用户 2026-09-28：「不能自己跟自己进行判断」）。
	if selfChibi(plan, id) {
		return path, false
	}
	peers := castPeers(c.store.Dir(proj.ID), plan, assets, "char:"+id)
	if len(peers) == 0 {
		return path, false
	}
	hit, other, note := c.castCollision(ctx, proj, proj.ID, path, peers)
	if !hit {
		return path, false
	}
	// 告警里带上**卡片 id**：同名的两张卡（不该发生，但数据脏时会有）光看名字分不清是谁，
	// 而人名相同又确实是不同人的情况在源文里并不罕见（"阿婆""小满"这类通名）。
	otherLabel := other
	if p := peerLabelByID(peers, other); p != "" {
		otherLabel = other + "（卡片 " + p + "）"
	}
	c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⚠ %s", qc.SamePersonWarnText(name+"（卡片 "+id+"）", otherLabel, &qc.SamePersonVerdict{Same: true, Note: note}))
	// 带**分离指令**换种子重出一次（只给一次，与朝向自检同一政策）
	sep := imagePrompt + CastSeparationClause(other)
	next, err := gen(kind, id, name, sep, "", ek, 1)
	if err != nil || strings.TrimSpace(next) == "" {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
			"↻ 撞脸重出失败(%v)，保留原图「%s」——可在资产详情里点「重新生成」再抽一次", err, name)
		return path, false
	}
	if hit2, o2, n2 := c.castCollision(ctx, proj, proj.ID, next, peers); hit2 {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
			"⚠ 重出后「%s」仍与「%s」像同一个人（%s）—— 已收手，请在资产详情里改描述/提示词后「重新生成」，"+
				"或把两人的年龄/脸型/眼型差异写得再具体些", name, o2, n2)
		return next, false // 新图比旧图新，且已如实告警；不吞掉产物
	}
	c.emit(proj.ID, PhaseAssets, 0, "success", 0,
		"✔ 撞脸自检:「%s」重出后与同剧其他角色可辨（原图与「%s」太像）", name, other)
	return next, false
}

// CastSkipNoCapability 没配视觉模型时给用户的一句实话（调用方每**次运行**只发一次）。
const CastSkipNoCapability = "未配置视觉模型，**撞脸自检没跑**（同剧两个角色被画成同一张脸时不会被发现）；" +
	"设置 → 视觉模型里填一个能读图的模型即可开启"

// castCollision 与 peers 逐张比对，返回(是否撞脸, 撞到谁, 说明)。
//
// 调用失败一律**当作没判定**（不当作通过）：返回 false 但发出告警 —— 与比例自检同一口径。
func (c *Coordinator) castCollision(ctx context.Context, proj *project.Project, projectID, path string, peers []castPeer) (bool, string, string) {
	client := TurnaroundVerifier(c.cfg.Get().VLM)
	if client == nil {
		return false, "", ""
	}
	cache := loadCastCache(c.store.Dir(projectID))
	dirty := false
	defer func() {
		if dirty {
			_ = saveCastCache(c.store.Dir(projectID), cache)
		}
	}()
	for _, p := range peers {
		if ctx.Err() != nil {
			return false, "", ""
		}
		// **兜底：同一张图跟自己比，永远没有意义。**
		//
		// 2026-09-28 实证：上游 id 口径写错（资产 id 又被拼了一次 "char:"），自己进了 peers，
		// 于是"碑生与自己长得像同一个人" —— 白烧一次视觉调用，还按"要和碑生不像"的指令
		// 重出了一张碑生的脸（产物反而更糟）。上游的口径已经修了（normalizeCardID + cardID），
		// 这道保险放在**比对点上**：将来任何一处拼法再写错，最多是少比一个人，不会自伤。
		if filepath.Clean(p.path) == filepath.Clean(path) {
			continue
		}
		// 按**图片指纹**记账：同一对脸问过一次就不再问（用户为了修一张脸会反复重跑同一段，
		// 每次从头再问一遍 = 接口被压到限流的主因之一，见 vlm_guard.go 的说明）。
		if e, ok := cache.lookup(path, p.path); ok {
			if e.Same {
				return true, p.name, e.Note
			}
			continue
		}
		// 限流静默 / 预算用尽：跳过（原因已各播报过一次）
		if c.vlmSkipReason(proj) != "" {
			return false, "", ""
		}
		v, err := qc.DetectSamePerson(ctx, client, path, p.path)
		if err != nil {
			if llm.IsRateLimited(err) {
				c.vlmNote(proj, err)
				return false, "", ""
			}
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
				"⚠ 撞脸自检未判定(不当作通过):与「%s」比对失败:%v", p.name, err)
			return false, "", ""
		}
		cache.put(path, p.path, v.Same, v.Note)
		dirty = true
		if v.Same {
			return true, p.name, v.Note
		}
	}
	return false, "", ""
}

// CastSeparationClause 撞脸后重出时追加的**分离指令**（不改身份锁，只逼它换一张脸）。
//
// 为什么要写得这么具体：模型上一轮已经"按着身份锁"画出了一张通用脸 ——
// 再说一句"要不一样"没有用，必须点明**改哪几个维度**（脸型骨相/年龄感/眼眉/肤色/发型剪影），
// 同时明确"别改成一个新角色"（身份锁里的记号必须留着，否则换个脸就换了个人，等于另一种失败）。
func CastSeparationClause(otherName string) string {
	other := strings.TrimSpace(otherName)
	if other == "" {
		other = "另一个角色"
	}
	return "\nCAST SEPARATION (mandatory — the previous attempt was rejected): the last render of this character " +
		"read as the SAME PERSON as「" + other + "」in this show. Redraw so a side-by-side comparison is unambiguous: " +
		"change the FACE SHAPE and bone structure (jaw line, cheekbones, brow ridge), the AGE READ, the eye shape, " +
		"the brow shape, the skin tone, and the hair silhouette (tie, length, forehead state). " +
		"Everything the identity lock says about this character stays — keep the fixed markers and the costume; " +
		"do not invent a different person, and do not copy「" + other + "」features. " +
		"(中文对照:上一次生成被判为与同剧「" + other + "」像同一个人。这次必须让人一眼看出是两个不同的人:" +
		"改**脸型与骨相(下颌/颧骨/眉骨)、年龄感、眼型、眉形、肤色、发型剪影**;" +
		"身份锁里的固定记号与服装保持不变 —— 不是换一个人,是不许和「" + other + "」共用一张脸。)\n"
}

// ---------- 判定记账（按图片指纹）----------
//
// 用户为了修一张脸会**反复重跑同一段资产阶段**，而上一轮的判定完全没记账 ——
// 每次从头把同一对脸再问一遍，是接口被压到限流的主因之一（见 vlm_guard.go）。
// 这里按**两张图的指纹**记账：指纹没变就直接用上次的结论，一次视觉调用都不花。
// 放在项目目录里（`cast_checks.json`），随项目走；指纹变了（重出了一张）自然失效。

const castCacheFile = "cast_checks.json"

type castCacheEntry struct {
	A    string `json:"a"` // 图 A 的内容指纹
	B    string `json:"b"` // 图 B 的内容指纹
	Same bool   `json:"same"`
	Note string `json:"note,omitempty"`
}

type castCache map[string]castCacheEntry

func castCacheKey(pathA, pathB string) string {
	// key 只用来索引，真实性由条目里的指纹保证（读的时候再比一次）。
	return filepath.Base(pathA) + "|" + filepath.Base(pathB)
}

func loadCastCache(dir string) castCache {
	c := castCache{}
	raw, err := os.ReadFile(filepath.Join(dir, castCacheFile))
	if err != nil {
		return c
	}
	_ = json.Unmarshal(raw, &c)
	return c
}

func saveCastCache(dir string, c castCache) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, castCacheFile), raw, 0o644)
}

// lookup 命中条件：**两张图的指纹都没变**（只比文件名会被"重出了一张"骗过去）。
func (c castCache) lookup(pathA, pathB string) (castCacheEntry, bool) {
	e, ok := c[castCacheKey(pathA, pathB)]
	if !ok {
		return castCacheEntry{}, false
	}
	fa, fb := project.FingerprintFile(pathA), project.FingerprintFile(pathB)
	if fa == "" || fb == "" || e.A != fa || e.B != fb {
		return castCacheEntry{}, false
	}
	return e, true
}

func (c castCache) put(pathA, pathB string, same bool, note string) {
	fa, fb := project.FingerprintFile(pathA), project.FingerprintFile(pathB)
	if fa == "" || fb == "" {
		return
	}
	c[castCacheKey(pathA, pathB)] = castCacheEntry{A: fa, B: fb, Same: same, Note: note}
}
