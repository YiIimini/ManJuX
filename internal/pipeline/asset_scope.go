package pipeline

import (
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 资产补齐的**范围**判定(审计 P0-9)。
//
// 起因:单镜重渲(`only=[shotID]`)只过滤了镜头循环,紧跟着的 `backfillAssets` /
// `backfillAssetViews` 传的仍是**整份 plan** —— 于是"只改一镜的 seed"可能顺手生成
// 全片所有缺失定妆照(Krea-2)与所有角色的多视角(每个角色数分钟 H3)。
// 用户为此付出的是几十分钟的 GPU 时间,而且界面上只看到"渲染前自动补齐…"。
//
// 所以"要补什么"必须由**本次真要渲的镜头**反推,而不是"全片有哪些资产"。

// assetNeed 本次渲染会用到的资产集合(角色/场景 id,不含 "char:"/"scene:" 前缀)。
type assetNeed struct {
	chars  map[string]bool
	scenes map[string]bool
}

// needForShots 从镜头集反推所需资产。shots 为空时返回 nil(= 不做范围限制)。
//
// 只统计 ref2va 镜头:纯文生(t2va)镜头不吃参考图,为它生成资产是纯浪费 ——
// 这一条同时修掉"某镜没有参考图也照样生成一堆资产"的浪费。
func needForShots(shots []project.Shot) *assetNeed {
	if len(shots) == 0 {
		return nil
	}
	need := &assetNeed{chars: map[string]bool{}, scenes: map[string]bool{}}
	for _, s := range shots {
		if s.Mode != "ref2va" {
			continue
		}
		for _, id := range s.Characters {
			need.chars[id] = true
		}
		if s.Scene != "" {
			need.scenes[s.Scene] = true
		}
	}
	return need
}

// wantsChar / wantsScene 范围判据:nil = 全都要(资产阶段、整片渲染)。
func (n *assetNeed) wantsChar(id string) bool {
	if n == nil {
		return true
	}
	return n.chars[id]
}

func (n *assetNeed) wantsScene(id string) bool {
	if n == nil {
		return true
	}
	return n.scenes[id]
}

// wantsProp 道具**不做取镜范围限制**：始终为真。
//
// 为什么与角色/场景不一样（2026-09-27）：范围是从**镜头引用**反推的，而镜头里**没有道具引用**
// —— 技能侧的明细还没有 `prop_ids` 字段（那是唯一还差的一条，见导入侧注释）。既然反推不出来，
// 就不能拿"没有引用"当"不需要"，否则道具图会被静默跳过。
//
// 代价可控：道具是**书级**的（本机实例 15 件），且生成后按名字进资产库跨集复用（`reuseFromLibrary`）
// —— 一本剧最多出 15 张白底单件图，第二次起都是 ♻。
// `prop_ids` 到位后这里改成 `return n.props[id]`（连同 needForShots 一起填），范围限制自然生效。
func (n *assetNeed) wantsProp(string) bool { return true }

// isEmpty 本次范围内没有任何资产需求(例如单镜重渲一个 t2va 镜头)。
func (n *assetNeed) isEmpty() bool {
	return n != nil && len(n.chars) == 0 && len(n.scenes) == 0
}

// ---------- 造型家族与"人数"统计(技能侧清单 §4.2) ----------

// charFamilyCount 这批角色 id 里有几个「人」:同一角色的多张造型档算一个人。
//
// 技能侧把"造型随剧情变"落成一角色多张卡(主卡 + `翻篇·归零` / `糖球·雨夜` …),
// 于是**按卡计数的一切"角色数"都会虚高**:《翻篇》12 个角色会报成 21 个(4 档翻篇
// + 3 档糖球 + 2 档老墙),用户看到的就是"怎么多出来一堆新角色"。给人看的数字统一走这里。
//
// ⚠ **本文件的"范围"判据本身不归族,这是刻意的**:变体卡各自需要自己的定妆照
// (§4.1:只换服装/状态,主卡那张不能顶替),所以 needForShots 仍旧按卡收范围 ——
// 归族只用在"这是几个人"这类统计口径上。变体卡之间的**一致性**由派生链兜
// (见 sheetderive.go:以主卡为参考图出图 + 身份校验)。
func charFamilyCount(ids []string) int {
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if k := project.CharFamilyKey(id, ""); k != "" {
			seen[k] = true
		}
	}
	return len(seen)
}
