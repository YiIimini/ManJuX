package director

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// shot_views.go 给**机位留空**的镜头补上 view / view_note（2026-09-29 用户：
// 「前后镜漂移，压不住场」）。
//
// ## 为什么要单独一趟（而不是只写在分镜提示词里）
//
// 新规则与 schema 都加上了（`view` 用官方九条之一；对不上就留空并写 `view_note` 说明原因），
// 但**重跑实测模型一条 view_note 都没写**（11 镜里 8 镜两样都缺）—— 这正是本仓反复记着的
// "散文规则管不住模型"。`RepairPlan` 那条自纠环修的是**提示词正文**，而 view/view_note 是
// **计划字段**，它管不到。
//
// 所以照 `TranslateCardPrompts`（卡片英文提示词）那一套：**确定性判定触发 + 一次专职会话 +
// 只回填缺的那几个字段 + 幂等**。判据与契约门同源（留空且没 note 才算缺）。
//
// 口径不是我自己编的：官方那 9 条是**大机位表**（鸟瞰/平视深远/纵深压缩/条件性仰视…），
// 平视中近景、俯拍特写在里面没有对应项 —— 硬填就是写错词，而写错词比留空更糟
// （plan_checks 的裁定 A）。所以这一趟允许它回答"留空，理由是 X"。

// ShotViewPatch 一趟会话回填的字段。
type ShotViewPatch struct {
	ID       int    `json:"id"`
	View     string `json:"view"`
	ViewNote string `json:"view_note"`
}

// shotsMissingView 哪些镜需要补（留空且没写原因）。
func shotsMissingView(plan *project.Plan) []project.Shot {
	if plan == nil {
		return nil
	}
	var out []project.Shot
	for _, sh := range plan.Shots {
		if strings.TrimSpace(sh.H3Prompt) == "" {
			continue
		}
		if strings.TrimSpace(sh.View) == "" && strings.TrimSpace(sh.ViewNote) == "" {
			out = append(out, sh)
		}
	}
	return out
}

// applyViewPatches 把回填结果写回方案，返回补了几镜。
//
// 幂等与边界：只补**仍然缺**的那几镜；模型给空值的、给了不认识的 id 的一律跳过；
// view 与 view_note **至少要有一样**才算补上（两样都空等于没补）。
func applyViewPatches(plan *project.Plan, patches []ShotViewPatch) int {
	if plan == nil {
		return 0
	}
	byID := map[int]ShotViewPatch{}
	for _, p := range patches {
		byID[p.ID] = p
	}
	n := 0
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if strings.TrimSpace(sh.View) != "" || strings.TrimSpace(sh.ViewNote) != "" {
			continue
		}
		p, ok := byID[sh.ID]
		if !ok {
			continue
		}
		v, note := strings.TrimSpace(p.View), strings.TrimSpace(p.ViewNote)
		if v == "" && note == "" {
			continue
		}
		sh.View, sh.ViewNote = v, note
		n++
	}
	return n
}

// FillShotViews 给留空的镜头补 view/view_note（一次会话）。返回补了几镜。
//
// client 为 nil、没有缺的、或调用失败时返回 0（不打断流水线；契约门仍会如实报出缺的那些）。
func FillShotViews(ctx context.Context, client *llm.Client, plan *project.Plan) (int, error) {
	if plan == nil || client == nil {
		return 0, nil
	}
	missing := shotsMissingView(plan)
	if len(missing) == 0 {
		return 0, nil
	}
	sys := "你是分镜的**机位校对**。给你一集里几个镜头的中文动作与英文提示词，你要为每个镜头定机位。\n" +
		"\n" +
		"规则（官方口径，别自创）：\n" +
		"1. 官方只有这 9 个视角词，**逐字**使用:ultra-wide 45-degree aerial view / ultra-wide aerial view / " +
		"low-altitude flying aerial view / ultra-wide eye-level deep perspective / compressed distant depth / " +
		"low-angle upward view / low-angle peeking upward view / low-angle strong perspective / telephoto compression。\n" +
		"2. 只有**真的对得上**才填 view。平视中近景、俯拍特写这类在这张表里**没有对应项** —— " +
		"硬填一个不相干的词比留空更糟（会把画面带偏）。对不上就把 view 留空，改在 view_note 里用中文写清为什么" +
		"（如「平视中近景，官方表无对应项」「主观镜头」「越轴反打」）。\n" +
		"3. 同一场戏的相邻镜头**不要无故越轴**（机位保持在那条关系线的同一侧）；真要越轴，在 view_note 里说明。\n" +
		"4. 每个镜头**必须**至少给出一样：view 或 view_note。\n" +
		"\n" +
		"输出:严格的 JSON 数组,每项 {\"id\":镜头号,\"view\":\"官方九词之一或空串\",\"view_note\":\"中文原因或空串\"}。" +
		"不要解释、不要 markdown 围栏。"
	var sb strings.Builder
	for _, sh := range missing {
		fmt.Fprintf(&sb, "### 镜头 %d（%d 秒，景别 %s，运镜 %s，场景 %s）\n动作：%s\n提示词节选：%s\n\n",
			sh.ID, sh.Duration, orNone(sh.ShotSize), orNone(sh.Camera), orNone(sh.Scene),
			orNone(sh.Action), clampRunes(sh.H3Prompt, 400))
	}
	out, err := client.Chat(ctx, []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: sb.String()}}, false)
	if err != nil {
		return 0, err
	}
	var patches []ShotViewPatch
	if j := strings.Index(out, "["); j >= 0 {
		if k := strings.LastIndex(out, "]"); k > j {
			out = out[j : k+1]
		}
	}
	if err := json.Unmarshal([]byte(out), &patches); err != nil {
		return 0, fmt.Errorf("机位回填结果不是合法 JSON：%w", err)
	}
	return applyViewPatches(plan, patches), nil
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
