package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// asset_fp.go 逐镜记录"这一镜用的是**哪一批资产**"，并在合成前核对。
//
// 官方出处（`skills/3d-animation-short-generator/references/qc-checklist.md` 的重生成纪律 ＋
// 同目录 `fallback-policy.md` 的 "After all clips are rendered"）：
//
//	"用户重做任何产物后，全部下游必须用最新批准版本，且**不得在最终拼接里静默混用新旧资产**；
//	 漂移未修正的片段也不得与修正片段静默混拼。"
//
// 【平台现状与缺口】manifest 里已经逐镜记了 Length / Model / SeamFrom 三列（各自治一类
// "改了东西旧产物还在"的静默故障），**但没有一列说明"这镜用的是哪批资产"**。于是会出现这种
// 情况：用户把某角色的定妆照重生成（换了脸），只重渲了前 5 镜，剩下 20 镜还是旧脸渲的 ——
// 合成照常跑、阶段照常报成功，成片里同一个人**前后两张脸**。
//
// 【做法】沿用既有纪律：**不往 InputFP 里加字段**（那会让存量项目全部重渲），而是单独记一列
// AssetFP = 这一镜所用资产（角色/场景/道具/系统卡片）的 id+kind+fingerprint 集合的哈希。
// 合成前用**当前**方案重算一遍，对不上就说明该片段是旧资产渲的 —— 如实播报，不静默混拼。
//
// 老清单没有这个键（空串）时不推翻结论（与 Model 的处理一致）：只提示"这些镜头是加这一列之前
// 渲的"，不据此拦人。
func shotAssetFP(shot project.Shot, assets map[string]project.Asset) string {
	ids := make([]string, 0, len(shot.Characters)+len(shot.PropIDs)+len(shot.SystemAssets)+1)
	if s := strings.TrimSpace(shot.Scene); s != "" {
		ids = append(ids, s)
	}
	ids = append(ids, shot.Characters...)
	ids = append(ids, shot.PropIDs...)
	for _, v := range shot.SystemAssets {
		if s := strings.TrimSpace(v); s != "" {
			ids = append(ids, s)
		}
	}
	sort.Strings(ids)

	var b strings.Builder
	for _, id := range ids {
		a, ok := assets[id]
		fp := "absent"
		kind := "?"
		if ok {
			kind = a.Kind
			if a.Fingerprint != "" {
				fp = a.Fingerprint
			} else {
				fp = "nofp:" + a.File
			}
		}
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(kind)
		b.WriteByte('|')
		b.WriteString(fp)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
}

// StaleAssetClips 合成前的**新旧资产混用**核对（官方重生成纪律）。
//
// 返回：(需要重渲的镜号, 旧清单里没有这一列的镜号)。两个都为空 = 全部片段都出自当前这批资产。
// 判据只在**双方都有值**时成立（老清单缺列不判，见 shotAssetFP 的说明）。
func StaleAssetClips(plan *project.Plan, manifest map[string]project.ClipManifest, assets map[string]project.Asset) (stale, unknown []int) {
	if plan == nil {
		return nil, nil
	}
	for _, shot := range plan.Shots {
		m, ok := manifest[itoa(shot.ID)]
		if !ok || m.Status != project.ShotDone {
			continue
		}
		if strings.TrimSpace(m.AssetFP) == "" {
			unknown = append(unknown, shot.ID)
			continue
		}
		if want := shotAssetFP(shot, assets); want != "" && want != m.AssetFP {
			stale = append(stale, shot.ID)
		}
	}
	return stale, unknown
}
