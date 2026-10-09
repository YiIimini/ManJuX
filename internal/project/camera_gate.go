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
	// CamMonotone 整集运镜**种类太少**（2026-10-09 用户：「场景和分镜运镜很单一」）。
	CamMonotone CameraIssueKind = "monotone"
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
	Kinds []string // CamMonotone：整集认得出的运动类型（官方顺序）
	Shots int      // CamMonotone：认得出运动类型的镜数
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
	// ④ 整集运镜**种类太少**（2026-10-09 用户：「场景和分镜运镜很单一」）。
	//
	// 前三条只看「单镜有没有写」与「连续几镜一样」—— 于是「整集只有推与拉交替」这种
	// 一潭死水**可以全绿**：没有空值、没有术语、也没有连续 5 镜同值。
	// 而视角那边早就有同型的判据（plan_checks.go 的「整集只用了 N 种机位视角」），
	// 运镜没有 —— 用户看到的就是「镜头一直是推/拉」。
	if kinds, n := CameraKindSpread(shots); n >= PlanCameraVarietyMinShots && len(kinds) < PlanCameraVarietyMinKinds {
		out = append(out, CameraIssue{Kind: CamMonotone, Kinds: kinds, Shots: n})
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

// ── 整集运镜种类（2026-10-09 用户：「场景和分镜运镜很单一」）──────────────────
//
// 前三条是**逐镜**的（有没有写 / 是不是术语 / 连续几镜一样），这一条是**整集**的：
// 观众说的「运镜单调」不是单镜的问题，而是全集翻来覆去只有那两下子。

// PlanCameraKinds 官方 `base-en.txt` §4.3 的运动类型词表，**按官方顺序**。
//
// 顺序有意义：判据、播报与自纠指令都按它排序 —— 否则同一条门每次跑出来种类顺序都不一样，没法比对。
var PlanCameraKinds = []string{
	"zoom", "push", "pan", "truck", "tilt", "pedestal", "arc", "tracking", "static", "shake", "pov", "roll",
	// pull 不在官方表里（官方把「往外拉」也写成 push，只是方向不同），但模型极常写
	// `The camera pulls back` —— 判据把它算作**与 push 不同的另一种**：
	// 宁可少报一次单调，也不要把「推/拉交替」误报成「只有一种运镜」。
	"pull",
}

// PlanCameraVarietyMinKinds 整集至少要铺开的运镜种类数。
//
// 取 4（词表 13 条）：Static / Shake / POV / Roll 属于**特殊情形**（定格、抖动、主观、转体），
// 不是每集都用得上；常规镜头语言的主轴是 Zoom / Push / Pull / Pan / Truck / Tilt / Pedestal / Arc / Tracking。
// 要求 4 种 = 把「只有推与拉交替」这种一潭死水挡在门外，又不去逼每集凑冷门运镜（为凑数而凑数不是好事）。
const PlanCameraVarietyMinKinds = 4

// PlanCameraVarietyMinShots 判「运镜单调」所需的最少**认得出运动类型**的镜数。
//
// 与视角那条同源（`planViewVarietyMinShots = 10`）：样本太薄就不该判单调 ——
// 一集只有 6 镜的短包，要求它铺开 4 种运镜是无理要求。
const PlanCameraVarietyMinShots = 10

// cameraKindWords 自然英文动作 → 官方运动类型的**等价词**（只做等价归一，不猜语义）。
//
// 认不出来的一律**不算**（既不计种类也不计入样本）—— 保守：宁可少报一次单调，
// 也不要因为「没认出这个词」就把一条正常的运镜算成「没用过」。
var cameraKindWords = map[string][]string{
	"zoom":     {"zoom"},
	"push":     {"push"},
	"pull":     {"pull"},
	"pan":      {"pan"},
	"truck":    {"truck"},
	"tilt":     {"tilt"},
	"pedestal": {"pedestal"},
	"arc":      {"arc"},
	"tracking": {"track", "follow"},
	"static":   {"static", "fixed", "locked off"},
	"shake":    {"shake"},
	"pov":      {"pov", "point of view"},
	"roll":     {"roll"},
}

// CameraKindsIn 一条运镜描述里含有的**官方运动类型**（可能空，也可能多个 ——
// 模型会写 `panning right while pushing in` 这种复合运镜，那本来就该算两种）。
func CameraKindsIn(cam string) []string {
	low := strings.ToLower(strings.TrimSpace(cam))
	if low == "" {
		return nil
	}
	var out []string
	for _, k := range PlanCameraKinds {
		for _, w := range cameraKindWords[k] {
			if strings.Contains(low, w) {
				out = append(out, k)
				break
			}
		}
	}
	return out
}

// CameraKindSpread 整集用到的运镜种类（按官方顺序）+ 认得出运动类型的镜数。
func CameraKindSpread(shots []Shot) ([]string, int) {
	seen := map[string]bool{}
	n := 0
	for _, sh := range shots {
		ks := CameraKindsIn(sh.Camera)
		if len(ks) == 0 {
			continue
		}
		n++
		for _, k := range ks {
			seen[k] = true
		}
	}
	var kinds []string
	for _, k := range PlanCameraKinds {
		if seen[k] {
			kinds = append(kinds, k)
		}
	}
	return kinds, n
}

// CameraMonotoneKinds 整集运镜是否单调（种类数 < 上限，且样本够）；
// 返回用到的种类、**没用过**的种类（官方顺序，给自纠挑一个用）与样本镜数。
//
// 【谁在用】渲染前的门（plan_checks.go）与剧本阶段的有界自纠（director.repair.go）
// 用**同一个**判据 —— 一处阈值两处消费，免得「门说合规、修的时候又说违规」。
func CameraMonotoneKinds(shots []Shot) (used, unused []string, sample int, monotone bool) {
	used, n := CameraKindSpread(shots)
	if n < PlanCameraVarietyMinShots || len(used) >= PlanCameraVarietyMinKinds {
		return used, nil, n, false
	}
	in := map[string]bool{}
	for _, k := range used {
		in[k] = true
	}
	for _, k := range PlanCameraKinds {
		if !in[k] {
			unused = append(unused, k)
		}
	}
	return used, unused, n, true
}
