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
func castPeers(projDir string, plan *project.Plan, assets map[string]project.Asset, selfID string) []castPeer {
	if plan == nil {
		return nil
	}
	selfCardID := strings.TrimPrefix(selfID, "char:")
	var selfName string
	for _, ch := range plan.Characters {
		if ch.ID == selfCardID {
			selfName = ch.Name
			break
		}
	}
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
		if id == selfID || project.IsChibiCard(ch.Kind, ch.ID) {
			continue
		}
		// 同一个人的另一档（或同名）：不是"别的角色"，比了只会得到"长得像同一个人"
		if project.SameCharacter(selfCardID, selfName, ch.ID, ch.Name) {
			continue
		}
		if selfName != "" && strings.TrimSpace(ch.Name) == strings.TrimSpace(selfName) {
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
