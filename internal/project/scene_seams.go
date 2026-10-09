package project

import "strings"

// scene_seams.go 逐接缝判"这两镜是不是**同一个场次**"（叠化只在换场处的唯一判据）。
//
// 【为什么收进 project（2026-10-09 用户：「问题好多啊」）】这条判据原来只写在 `internal/compose`
// 的合成链里（`sceneSeamFlags`），而"这份成片/这一集有几处同场连续动作"是**方案自己的事实** ——
// 成片体检（`internal/filmaudit`）也要用它。判据只留一处：compose 与体检都调这里，
// 免得"合成按一个口径、体检按另一个口径说"（同类分裂在 2026-09-26 的转场审计里栽过）。
//
// 缺场景标注（老包、空 scene）时一律 false —— 没有依据说"这是同场连续动作"，就不拿猜出来的东西
// 去改画面，也不拿来报账。
func SceneSeamFlags(plan *Plan) []bool {
	if plan == nil || len(plan.Shots) < 2 {
		return nil
	}
	out := make([]bool, 0, len(plan.Shots)-1)
	for i := 0; i+1 < len(plan.Shots); i++ {
		a := strings.TrimSpace(plan.Shots[i].Scene)
		b := strings.TrimSpace(plan.Shots[i+1].Scene)
		out = append(out, a != "" && strings.EqualFold(a, b))
	}
	return out
}

// SameSceneSeamCount 有几处接缝是"同场连续动作"。
func SameSceneSeamCount(plan *Plan) int {
	n := 0
	for _, f := range SceneSeamFlags(plan) {
		if f {
			n++
		}
	}
	return n
}

// SceneSeamCounts 返回 (同场接缝数, 总接缝数) —— 体检播报用。
func SceneSeamCounts(plan *Plan) [2]int {
	flags := SceneSeamFlags(plan)
	same := 0
	for _, f := range flags {
		if f {
			same++
		}
	}
	return [2]int{same, len(flags)}
}
