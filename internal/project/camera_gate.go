package project

import (
	"fmt"
	"strings"
)

// camera_gate.go **运镜（camera）的三条产出侧判据**。
//
// ## 为什么现在才加（2026-09-28 用户问「运镜是否合理」）
//
// 运镜此前**只写在提示词里**，产出侧一条判据都没有：`Camera` 允许为空、
// 连续十镜同一个运镜没人管、写成 dolly / crane 这类术语也照过。
// 而运镜是"这一镜看起来是不是活的"的直接来源 —— 与"连续同景别"同级，
// 后者早就有一道门，运镜却没有。
//
// ## 口径出处（三件事分开，别混）
//
//   - **官方**（`MiniMax-AI/MiniMax-H3` 的 `base-en.txt` §4.3）：相机运动是
//     **运动类型 + 幅度 + 速度**三件套，运动类型**恰好 12 种**（Zoom / Push / Pan / Truck /
//     Tilt / Pedestal / Arc / Tracking / Static / Shake / POV / Roll），幅度与速度"只在有意义时写"，
//     且**要写成镜头里的自然英文动作**（官方给了三个示例，都是 `The camera pushes in …` 这种）。
//   - **平台规则**（官方没有）：① 每镜都要有一个运镜；② 禁摄影术语。
//   - **用户《五》点名要的"慢动作"**：官方的 12 种里**没有它** —— 官方给的 `at slow speed`
//     是"**运镜**走得慢"，与"**动作**被放慢"不是一回事。要时间放慢写 `in slow motion`，
//     并把它标成平台规则（见 `sysShotRulesTpl__SHOT_PLAN_RULES.md` 的运镜段）。

// PlanMaxConsecutiveSameCamera 连续同一个运镜的上限。
//
// 取值与"连续同景别 ≤6"同一个思路，但更紧（4）：景别变了运镜不变还能看，
// 而运镜连着四镜一个样，观众体感就是"卡住了"。
const PlanMaxConsecutiveSameCamera = 4

// cameraJargon 摄影术语黑名单（**平台规则**）。
//
// 官方只给词表、没有明说禁术语；加这条是因为 H3 认的是自然英文动作 ——
// 术语进正文只是噪音（官方自己的三个示例里一个术语都没有）。
var cameraJargon = []string{"dolly", "crane shot", "crane in", "whip pan", "steadicam", "jib shot"}

// CameraIssueKind 运镜问题的种类。
type CameraIssueKind string

const (
	CamMissing CameraIssueKind = "missing" // 没写运镜（平台规则）
	CamJargon  CameraIssueKind = "jargon"  // 用了摄影术语（平台规则）
	CamRepeat  CameraIssueKind = "repeat"  // 连续同一个运镜超过上限
)

// CameraIssue 一条运镜问题（**只给数据**）。
//
// 【为什么是"数据"而不是"人话"】`docs/门目录.md` 由 `gatecat` 用 AST 扫源码里的
// `add(level, where, "字面格式串", …)` 生成 —— 判据若直接返回成句的中文，接线处只能写
// `add(..., "%s", msg)`，格式串没有字面量、门目录里就看不到这条门是什么。
// 所以分工是：**算与判在 camera_gate.go，怎么说在 plan_contract.go**。
type CameraIssue struct {
	Kind  CameraIssueKind
	IDs   []string // 涉及的镜号（"#12"）
	Term  string   // CamJargon：命中的术语
	Cam   string   // CamRepeat：重复的运镜
	Start int      // CamRepeat：起始镜号
	Run   int      // CamRepeat：连续长度
}

// CameraIssues 运镜的三条判据（返回数据，空 = 没问题）。
func CameraIssues(shots []Shot) []CameraIssue {
	var out []CameraIssue
	var noCam []string
	for _, sh := range shots {
		if strings.TrimSpace(sh.Camera) == "" {
			noCam = append(noCam, fmt.Sprintf("#%d", sh.ID))
		}
	}
	if len(noCam) > 0 {
		out = append(out, CameraIssue{Kind: CamMissing, IDs: noCam})
	}
	if term, ids := jargonCameraShots(shots); len(ids) > 0 {
		out = append(out, CameraIssue{Kind: CamJargon, IDs: ids, Term: term})
	}
	if cam, start, n := longestSameCameraRun(shots); n > PlanMaxConsecutiveSameCamera {
		out = append(out, CameraIssue{Kind: CamRepeat, Cam: cam, Start: start, Run: n})
	}
	return out
}

// jargonCameraShots 找出用了摄影术语的镜（返回首个命中的术语 + 镜号列表）。
func jargonCameraShots(shots []Shot) (string, []string) {
	var ids []string
	hit := ""
	for _, sh := range shots {
		low := strings.ToLower(sh.Camera)
		if low == "" {
			continue
		}
		for _, j := range cameraJargon {
			if strings.Contains(low, j) {
				if hit == "" {
					hit = j
				}
				ids = append(ids, fmt.Sprintf("#%d", sh.ID))
				break
			}
		}
	}
	return hit, ids
}

// longestSameCameraRun 最长"连续同一个运镜"的段（运镜名、起始镜号、长度）。
//
// 空运镜**不参与**这段统计（它由"没写运镜"那条单独报）——否则十镜连空会被报成
// "连续十镜同一个运镜「」"，用户看不出到底是哪种毛病。
func longestSameCameraRun(shots []Shot) (string, int, int) {
	best, bestStart, bestLen := "", 0, 0
	cur, curStart, curLen := "", 0, 0
	for _, sh := range shots {
		k := strings.ToLower(strings.TrimSpace(sh.Camera))
		if k == "" {
			cur, curLen = "", 0
			continue
		}
		if k == cur {
			curLen++
		} else {
			cur, curStart, curLen = k, sh.ID, 1
		}
		if curLen > bestLen {
			best, bestStart, bestLen = cur, curStart, curLen
		}
	}
	return best, bestStart, bestLen
}
