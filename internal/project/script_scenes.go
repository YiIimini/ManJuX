package project

import (
	"fmt"
	"regexp"
	"strings"
)

// script_scenes.go **剧本场次表**：从结构化剧本里读场次，并与分镜做覆盖对账。
//
// ## 为什么要有它（2026-09-30 用户：「分析是否场景过于单一」+「好好完善」）
//
// 实测第 02 集：场景卡 5 张、剧本 4 场（覆盖是够的），但镜头分配成这样 ——
//
//	第七坡碑场 7 镜/63s（**49%**）· 粥棚 5 镜/43s（33%）· 封碑台 2 镜（14%）
//	号棚通铺（＝剧本场3）**1 镜/5s** · 号棚门槛夜色（＝剧本场4·夜）**0 镜**
//
// 于是整集 82% 在同一个坡上、全是户外白日，**唯一的夜戏被整场丢掉** ⇒ 观众感觉
// "场景过于单一"。机制是：⑤ 分镜按**叙事节拍**铺镜头，**没有场次配额约束**，
// 而此前剧本里没有结构化场次表 ⇒ 平台**无从对照**，这件事也就没人检查。
//
// 结构化剧本（平台自己的 ① 会话产出）现在带 `【场 N|地点|时间|内外】` ✔，
// 所以这一条第一次可以机检：**剧本列了几场，分镜就得覆盖几场；单场不许过半。**

// ScriptScene 剧本里的一场戏。
type ScriptScene struct {
	No     int    // 场号
	Place  string // 地点
	Time   string // 时间（清晨/上午/夜…）
	InOut  string // 内 / 外 / 内外
	Header string // 原样表头（播报与对照用）
}

// scriptSceneRe 结构化剧本的场次头：`【场 1|碑奴粥棚|清晨|外】`（全/半角竖线、空格都容忍）。
var scriptSceneRe = regexp.MustCompile(`【\s*场\s*(\d+)\s*[|｜]\s*([^|｜】]*)[|｜]\s*([^|｜】]*)[|｜]\s*([^】]*)】`)

// ParseScriptScenes 读剧本里的场次表（没有结构化场次头时返回空 —— 老包不判）。
func ParseScriptScenes(brief string) []ScriptScene {
	var out []ScriptScene
	for _, m := range scriptSceneRe.FindAllStringSubmatch(brief, -1) {
		no := 0
		fmt.Sscanf(m[1], "%d", &no)
		out = append(out, ScriptScene{
			No:     no,
			Place:  strings.TrimSpace(m[2]),
			Time:   strings.TrimSpace(m[3]),
			InOut:  strings.TrimSpace(m[4]),
			Header: strings.TrimSpace(m[0]),
		})
	}
	return out
}

// SceneShareCapPercent 单场戏占全集镜数的上限（超过就报"场景过于单一"）。
//
// 取 40：一部短剧一集通常 3–6 场，一场过半必然显得"原地打转"；实测第 02 集 49% 就是这种感觉。
const SceneShareCapPercent = 40

// SceneCoverageIssues 剧本场次 ↔ 分镜覆盖对账（每条一句人话；顺序即优先级）。
//
// ## 判据刻意**不靠名字模糊匹配**
//
// 实测两边写法根本不同（剧本「碑奴粥棚」/ 场景卡「碑奴营粥棚」；剧本「号棚内外」/
// 卡片「碑奴号棚通铺」与「号棚门槛夜色」）—— 靠"包含/最长公共子串"去猜会**猜错、还会撞车**
// （两张号棚卡得分相同）。所以这里只用**能确定性判定**的三件事：
//
//  1. **单场占比**：按镜头自己的 `scene`（场景卡 id）分组，一场超过 SceneShareCapPercent 就报
//     —— 这是"场景单一"最直接的来源，且完全确定（不需要知道它对应剧本哪一场）；
//  2. **场次数不足**：剧本列了 N 场，分镜只用到 M 个场景（M < N）⇒ 有场次没被拍；
//  3. **时段缺失**：拿**用到的场景卡**的 `light_state` 看有没有夜戏 —— 剧本写了夜场、
//     而全集一个夜戏场景都没用上，就报出来（第 02 集正是把唯一的夜戏整场丢了）。
//
// 只在剧本有结构化场次表（≥2 场）时才判：老包没有表 ⇒ 无从对照，不报。
func SceneCoverageIssues(plan *Plan, briefScenes []ScriptScene) []string {
	if plan == nil || len(briefScenes) < 2 || len(plan.Shots) == 0 {
		return nil
	}
	// 按场景分组（镜头里存的是场景卡 id；退回按卡片名兜一次）
	cardByID := map[string]Scene{}
	cardByName := map[string]Scene{}
	for _, sc := range plan.Scenes {
		cardByID[sc.ID] = sc
		cardByName[sc.Name] = sc
	}
	perScene := map[string]int{}
	for _, sh := range plan.Shots {
		perScene[sh.Scene]++
	}
	total := len(plan.Shots)
	var out []string

	// ① 单场占比
	for key, n := range perScene {
		if pct := n * 100 / total; pct > SceneShareCapPercent {
			name := key
			if sc, ok := cardByID[key]; ok {
				name = sc.Name
			} else if sc, ok := cardByName[key]; ok {
				name = sc.Name
			}
			out = append(out, fmt.Sprintf("「%s」一场就占了 %d/%d 镜（%d%%）—— 超过 %d%% 观众会觉得「原地打转」；"+
				"把多余的镜分给其它场次，或在这一场内部换机位与时段", name, n, total, pct, SceneShareCapPercent))
		}
	}
	// ② 场次数不足
	if used := len(perScene); used < len(briefScenes) {
		out = append(out, fmt.Sprintf("剧本列了 %d 场戏，分镜只用到 %d 个场景 —— 有场次整段没被拍；"+
			"逐场核对剧本的【场 N|地点|时间|内外】，缺哪场补哪场", len(briefScenes), used))
	}
	// ③ 时段缺失（剧本有夜场，而全集没用到任何夜戏场景）
	if briefHasNight(briefScenes) && !planUsesNightScene(plan, perScene, cardByID, cardByName) {
		out = append(out, "剧本里有**夜场**，而全集一个夜戏场景都没用上 —— 色调与节奏会没有起伏"+
			"（实测第 02 集就是这样：唯一的夜戏整场丢掉，82% 的镜头挤在同一个户外白日场景里）")
	}
	return out
}

// briefHasNight 剧本场次表里有没有夜场。
func briefHasNight(scenes []ScriptScene) bool {
	for _, sc := range scenes {
		if isNightLight(sc.Time) || strings.Contains(sc.Time, "夜") {
			return true
		}
	}
	return false
}

// planUsesNightScene 分镜用到的场景里有没有夜戏（看场景卡的 light_state）。
func planUsesNightScene(plan *Plan, perScene map[string]int, byID, byName map[string]Scene) bool {
	for key := range perScene {
		sc, ok := byID[key]
		if !ok {
			sc, ok = byName[key]
		}
		if !ok {
			continue
		}
		if isNightLight(sc.LightState) {
			return true
		}
	}
	return false
}

// isNightLight 光态/时段里是不是夜（中英都认）。与 pipeline 的同名判据同一套词表 ——
// 那边判镜头提示词，这边判场景卡与剧本时段，两处必须一致（否则一处说"夜"、一处说"日"）。
func isNightLight(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	for _, w := range []string{"夜", "night", "月", "moon", "烛", "candle", "灯", "lamp", "lantern"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}
