package pipeline

import "github.com/YiIimini/ManJuX/internal/project"

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

// isEmpty 本次范围内没有任何资产需求(例如单镜重渲一个 t2va 镜头)。
func (n *assetNeed) isEmpty() bool {
	return n != nil && len(n.chars) == 0 && len(n.scenes) == 0
}
