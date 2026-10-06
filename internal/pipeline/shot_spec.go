package pipeline

import (
	"fmt"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// shot_spec.go 把镜头表里那些「模型必须照做、正文却常常没写」的结构字段，在**装配点**写进提示词
// （2026-10-06 审计 §八-13）。
//
// 【为什么要这一层】官方镜头表的几列平台**都有字段、也都有门**，但没有一个进入渲染输入：
//
//	· `end_state`（Continuity Handoff）、`positions`（Character Positions）、`offscreen`
//	  （Exited Character Status）：只有契约门读（`plan_checks.go` 的必填与对号），渲染端**零消费**
//	  ⇒ 用户在工作台把它们改掉，提示词一个字不变、指纹也不变，**改了等于没改**；
//	· `view` / `camera`（机位 / 运镜）：契约要求**模型自己**把官方视角词逐字写进正文
//	  （`plan_view_contract_test.go` 还有门对账），但模型漏写时只会多一条 warn，画面照旧。
//
// 这些全是**已知量**（计划里写着），属于本仓那条老口径：「能确定性做到的事，不要让模型自觉」。
// 于是统一在**唯一装配入口**（`assembleShotPrompt`）里补一小段 —— 而且**缺什么补什么**：
// 正文里已经有那个串就不再写（提示词词数预算本来就紧，官方 350-500 词、实测常超）。
//
// 幂等由 marker 守（与 EnsurePropAnchors / EnsureTransitionCaption 同一套纪律）。
const shotSpecMarker = "SHOT SPEC (written by the renderer from the shot table)"

// EnsureShotSpecClause 按"缺什么补什么"把镜头表的结构字段补进提示词（幂等）。
//
// 逐行的判据（短、可测）：
//
//	viewpoint     —— `View` 非空、且正文里没有这个串
//	camera        —— `Camera` 非空、且正文里没有这个串
//	end state     —— `EndState` 非空、且正文里没有这个串（这一条几乎总会补上：模型很少把自己的尾帧状态抄进正文）
//	positions     —— 每个出场角色的位置：非空、且正文里没有这个串
//	off-screen    —— 每个离屏角色：非空（**必须**写进去 —— 不写模型就会让人凭空出现/消失）
//
// 一行都补不出来时原样返回（不改动、不加空白）。
func EnsureShotSpecClause(prompt string, plan *project.Plan, shot project.Shot) string {
	if strings.TrimSpace(prompt) == "" || strings.Contains(prompt, shotSpecMarker) {
		return prompt
	}
	var lines []string
	if v := strings.TrimSpace(shot.View); v != "" && !strings.Contains(prompt, v) {
		lines = append(lines, "- viewpoint (the official viewphrase for this shot): "+v)
	}
	if c := strings.TrimSpace(shot.Camera); c != "" && !strings.Contains(prompt, c) {
		lines = append(lines, "- camera movement: "+c)
	}
	if es := strings.TrimSpace(shot.EndState); es != "" && !strings.Contains(prompt, es) {
		lines = append(lines,
			"- the shot must END in exactly this state (it is the continuity handoff to the next shot, "+
				"so the last frame has to match it): "+es)
	}
	for _, id := range sortedMapKeys(shot.Positions) {
		pos := strings.TrimSpace(shot.Positions[id])
		if pos == "" || strings.Contains(prompt, pos) {
			continue
		}
		lines = append(lines, "- on-screen position of "+charLabel(plan, id)+": "+pos)
	}
	for _, id := range sortedMapKeys(shot.Offscreen) {
		off := strings.TrimSpace(shot.Offscreen[id])
		if off == "" {
			continue
		}
		lines = append(lines, "- OFF-SCREEN in this shot — "+charLabel(plan, id)+
			" must NOT appear in frame (stay outside it, no silhouette, no reflection): "+off)
	}
	if len(lines) == 0 {
		return prompt
	}
	return strings.TrimRight(prompt, "\n ") + "\n\n" + shotSpecMarker +
		" (authoritative for this shot; it corrects anything above that disagrees):\n" +
		strings.Join(lines, "\n") + "\n"
}

// sortedMapKeys 取 map 的键并排序（装配输出必须**稳定** —— 同样的计划每次要拼出同样的串，
// 否则渲染指纹会无谓地失配、白重渲）。
func sortedMapKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// charLabel 角色 id → 给模型看的标签（中文名 + id；查不到就用 id，不编）。
func charLabel(plan *project.Plan, id string) string {
	id = strings.TrimSpace(id)
	if plan == nil || id == "" {
		return id
	}
	for _, c := range plan.Characters {
		if c.ID == id || strings.TrimPrefix(c.ID, "char:") == strings.TrimPrefix(id, "char:") {
			if n := strings.TrimSpace(c.Name); n != "" {
				return fmt.Sprintf("%s (%s)", n, id)
			}
			break
		}
	}
	return id
}
