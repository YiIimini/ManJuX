package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"

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
	path string
}

// castPeers 挑出"已经生成好的其他角色定妆照"（排除自己、排除 Q 版等派生资产）。
func castPeers(projDir string, plan *project.Plan, assets map[string]project.Asset, selfID string) []castPeer {
	if plan == nil {
		return nil
	}
	var out []castPeer
	for _, ch := range plan.Characters {
		id := "char:" + ch.ID
		if id == selfID || strings.HasSuffix(ch.ID, "_chibi") {
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
		out = append(out, castPeer{name: ch.Name, path: p})
		if len(out) >= maxCastCompare {
			break
		}
	}
	return out
}

// checkCastDistinctness 与已生成的其他角色比一次脸；撞脸则**带分离指令换种子重出一次**，
// 返回最终可用的图路径（不撞、没条件比、或重出后仍撞都返回原路径 —— 重出失败不吞掉原图）。
// 返回 (最终图路径, 是否"没能力验")。**没能力那条由调用方记账**（与比例自检同一政策：
// "一次运行只提醒一次"是本次运行的性质，而 Coordinator 是进程级单例）。
func (c *Coordinator) checkCastDistinctness(ctx context.Context, proj *project.Project, assets map[string]project.Asset,
	kind, id, name, imagePrompt string, ek EntityKind, path string,
	gen func(kind, id, name, imagePrompt string, ek EntityKind, attempt int) (string, error)) (string, bool) {
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
	peers := castPeers(c.store.Dir(proj.ID), plan, assets, "char:"+id)
	if len(peers) == 0 {
		return path, false
	}
	hit, other, note := c.castCollision(ctx, proj, path, peers)
	if !hit {
		return path, false
	}
	c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⚠ %s", qc.SamePersonWarnText(name, other, &qc.SamePersonVerdict{Same: true, Note: note}))
	// 带**分离指令**换种子重出一次（只给一次，与朝向自检同一政策）
	sep := imagePrompt + CastSeparationClause(other)
	next, err := gen(kind, id, name, sep, ek, 1)
	if err != nil || strings.TrimSpace(next) == "" {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
			"↻ 撞脸重出失败(%v)，保留原图「%s」——可在资产详情里点「重新生成」再抽一次", err, name)
		return path, false
	}
	if hit2, o2, n2 := c.castCollision(ctx, proj, next, peers); hit2 {
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
func (c *Coordinator) castCollision(ctx context.Context, proj *project.Project, path string, peers []castPeer) (bool, string, string) {
	client := TurnaroundVerifier(c.cfg.Get().VLM)
	if client == nil {
		return false, "", ""
	}
	for _, p := range peers {
		if ctx.Err() != nil {
			return false, "", ""
		}
		v, err := qc.DetectSamePerson(ctx, client, path, p.path)
		if err != nil {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
				"⚠ 撞脸自检未判定(不当作通过):与「%s」比对失败:%v", p.name, err)
			return false, "", ""
		}
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
