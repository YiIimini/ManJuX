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

	// props 道具 id 集合；propsKnown 表示"这批镜头**确实带了**道具引用信息"。
	//
	// 两件事分开记是有原因的（2026-09-27）：`props` 为空可能是"这一镜真的不用道具"，
	// 也可能是"这批方案根本没有 prop_ids 这个字段"（老渲染包/手工方案）。前者该跳过、
	// 后者该全带上 —— 只看 map 是否为空分不出这两种，就会把道具图静默跳过。
	props      map[string]bool
	propsKnown bool
}

// needForShots 从镜头集反推所需资产。shots 为空时返回 nil(= 不做范围限制)。
//
// 只统计 ref2va 镜头:纯文生(t2va)镜头不吃参考图,为它生成资产是纯浪费 ——
// 这一条同时修掉"某镜没有参考图也照样生成一堆资产"的浪费。
func needForShots(shots []project.Shot) *assetNeed {
	if len(shots) == 0 {
		return nil
	}
	need := &assetNeed{chars: map[string]bool{}, scenes: map[string]bool{}, props: map[string]bool{}}
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
		// 道具引用：**只要这一批镜头里有任意一镜带了 prop_ids**，就算"有据可依"，
		// 之后按引用收窄（包括"这一批一个道具都不用"这种正常结论）。
		if len(s.PropIDs) > 0 {
			need.propsKnown = true
			for _, id := range s.PropIDs {
				need.props[id] = true
			}
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

// wantsProp 道具是否在本次取镜范围内。
//
// 2026-09-27：技能侧补了 `shots[].prop_ids`（按道具名分词匹配，全书 43 镜命中），
// 于是这一位**真的能判**了 —— 有引用信息就按引用收窄；没有（老明细/手工方案）就**恒真**
// （宁可多带一张图，也不要因为"看不出它需要"而把道具图静默跳过）。
func (n *assetNeed) wantsProp(id string) bool {
	if n == nil || !n.propsKnown {
		return true
	}
	return n.props[id]
}

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
