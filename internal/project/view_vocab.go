package project

import (
	"fmt"
	"strings"
)

// view_vocab.go 镜头语言的两套词表（视角/机位 与 尾帧状态）＋ 场景卡的事件隔离串。
//
// ## 为什么单独一个文件
//
// 2026-09-27 技能侧整改单（`docs/技能侧交付-平台侧整改单-2026-09-27.md` §1.1/§1.2/§1.3）
// 指出三件"直接决定成片对不对"的缺口，全部落在**词表有没有被逐字写进提示词**上：
//
//	1. 镜头**没有视角/机位角度** —— 复核 `docs/技能侧调整方案-脚本产物复核.md` §2.1：
//	   `camera` 100% 由 `shot_size` 决定、只有 2 个取值、零机位角度，最长 21 镜同景别；
//	2. 镜头**没有尾帧状态** —— 衔接只靠平台的 seam 开关，没有"这一镜最后一帧长什么样"的写法；
//	3. 场景卡**没有事件隔离** —— 场景里出现剧情道具/战斗痕迹，或堆成道具清单。
//
// 词表来自官方 `skills/h3-prompt-writing` 与 `skills/3d-animation-short-generator`，
// 经技能侧 `references/分镜脚本-多引擎输出规范.md` §二 与 `references/资产提示词-三卡规范.md` §二
// 逐条翻译（外部指令库原文见 `references/漫剧指令库-外部对标.md`）。
//
// **为什么逐字而不是"意思对了就行"**：这些串是模型侧被训练过的描述短语，改写等于换词表；
// 而且平台侧的契约门只能按字符串判 —— 词表一改，门与产出就一起走散。
// 所以本文件是**唯一真源**：director 的提示词文案、契约门、测试全部引用这里的常量。

// 官方 9 视角词表（英文，逐字进 prompt）。顺序即优先级建议：前 5 条是常规首选，
// 后 4 条是条件性用法（见 PlanViewsConditional）。
const (
	ViewAerial45     = "ultra-wide 45-degree aerial view"      // 超广角斜 45° 鸟瞰（地形/院落首选）
	ViewAerial       = "ultra-wide aerial view"                // 超广角鸟瞰（城市/宗门/战场/建筑群）
	ViewFlyingAerial = "low-altitude flying aerial view"       // 低空飞行鸟瞰（草原/森林/山脉，有掠过感）
	ViewDeepEyeLevel = "ultra-wide eye-level deep perspective" // 超广角平视深远（殿内/长廊/街道，线性纵深）
	ViewCompressed   = "compressed distant depth"              // 远景纵深压缩（远山/遗迹群/雾层叠压）
	ViewLowUp        = "low-angle upward view"                 // 低角度仰视（高塔/山门/巨像）
	ViewLowPeek      = "low-angle peeking upward view"         // 低角度窥视仰视（废墟/巨骸/战争遗迹）
	ViewLowStrong    = "low-angle strong perspective"          // 仰拍强透视（夸张近大远小）
	ViewTelephoto    = "telephoto compression"                 // 长焦压缩（远方巨物 vs 前景）
)

// PlanViews 9 视角词表（判据顺序：先匹配到的算数，前缀重叠的按长串优先，见 PlanViewIn）。
var PlanViews = []string{
	ViewAerial45, ViewAerial, ViewFlyingAerial, ViewDeepEyeLevel, ViewCompressed,
	ViewLowUp, ViewLowPeek, ViewLowStrong, ViewTelephoto,
}

// PlanViewsConditional 条件性视角：只在内容真的需要时才用（技能侧整改单 §1.1 标注为"条件性"）。
// 平台不禁止，但自检会提示"非条件场景不建议用"，因为它最容易把普通对话镜写成奇观镜。
var PlanViewsConditional = []string{ViewLowUp, ViewLowPeek, ViewLowStrong, ViewTelephoto}

// PlanEstablishingExtra 建立镜头**额外必含**的串（官方建立镜头写法）。
//
// 建立镜头 = 每个场景的**第一镜**（把地点交代清楚的那一镜）。整改单明确：
// **禁微距/特写/小景别当建立镜头** —— 观众第一次看见一个地方时，给一张脸或一件道具
// 等于没交代地点；这也是"最长连续 21 镜同景别"那类问题的反面写法。
const PlanEstablishingExtra = "ultra-wide angle, grand scale, epic wide shot, vast space, distant horizon, cinematic establishing shot"

// PlanEstablishingBannedSizes 不许当建立镜头的景别（微距/特写类）。
var PlanEstablishingBannedSizes = []string{"特写"}

// planViewVarietyMinShots 判"视角单调"所需的最少带视角镜数（裁定 A 之后大量小景别合法留空，
// 样本太薄就不该判单调）。
const planViewVarietyMinShots = 10

// ViewRequiredForShot 这一镜**必须**写官方视角串吗（裁定 A：只看"官方 9 条语义站得住"的那些镜）。
//
// 判据两条，都是"交代空间"的镜：
//   - **建立镜头**（某场景的首镜）—— 观众第一次看见一个地点，机位视角就是地点交代本身；
//   - **远景 / 全景**（含极远景）—— 大景别的价值全在纵深与空间关系，官方表里对应
//     `compressed distant depth` / `ultra-wide …`。
//
// 其余（中景/近景/特写/平视中近景）**允许留空**：官方 9 条是"大视角/机位表"，里面没有
// "平视中近景"也没有"俯拍"，硬填就是写错词 —— 写错词比留空更糟（把特写写成"超广角斜 45° 鸟瞰"）。
func ViewRequiredForShot(shots []Shot, i int) bool {
	if i < 0 || i >= len(shots) {
		return false
	}
	if IsEstablishingShot(shots, i) {
		return true
	}
	size := strings.TrimSpace(shots[i].ShotSize)
	for _, wide := range []string{"远景", "全景"} {
		if strings.Contains(size, wide) {
			return true
		}
	}
	return false
}

// PlanUnboundFaceMarker 技能侧对"这一镜的人**故意不绑卡、且不露脸**"的**旧写法**（prose）。
//
// 2026-09-27 §9 起技能侧给了**机器可读字段**（`unbound` + `unbound_reason`，见下面的
// UnboundNoCard / UnboundFaceless），所以字段是首选判据、这条 prose 只是**兜底**
// （存量明细/手工写的方案里可能只有这句话）。
const PlanUnboundFaceMarker = "kept off-face and unbound"

// 「刻意不绑卡」的两个 token（技能侧 `unbound_reason` 的取值，已写进他们的契约 §2.3）。
const (
	// UnboundNoCard 画面里的人**不在本集角色表**（群演/一次性角色）→ 要让他露脸就得先建卡。
	UnboundNoCard = "no_card"
	// UnboundFaceless 刻意不给脸（背影/局部/群像）→ 无需建卡。
	UnboundFaceless = "faceless_by_design"
)

// PlanFaceCloseSizes "脸上特写"那一档的景别（2026-09-27 技能侧追补单 N1）。//
// 判据用途：**这一档画面里几乎只有一张脸**，而参考图集合是从 `shots[].characters` 派生的
// （pipeline.RefPlan）—— 所以「特写/近景 + characters 为空」= 有人脸却没参考图 = 每次换一张新脸。
// 含 `中近景` 是**有意**的：它同样以脸为主（判据用 Contains，见 IsCloseUpShot）。
var PlanFaceCloseSizes = []string{"特写", "近景"}

// IsCloseUpShot 这一镜是否属于"脸上特写"那一档（含中近景，它同样以脸为主）。
func IsCloseUpShot(shotSize string) bool {
	s := strings.TrimSpace(shotSize)
	for _, k := range PlanFaceCloseSizes {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// PlanScenePreEventClause 场景卡的**事件隔离**串（逐字拼进场景图提示词，见 §1.3）。
//
// 场景板是 T2I 出的**环境参考图**，会被 H3 当成"这个地方本来就长这样"带进每一镜。
// 若场景板上带着血渍/尸体/刚破碎物/遗落道具，那些东西会在**所有**用到该场景的镜头里复现
// （与"场景板里画了人 → 每镜都出现同一个陌生人"是同一条机理，见 pipeline.StripSceneCrowd）。
// 所以场景卡只画**事件发生前**的地点固有状态：允许风化/旧锈/苔藓/旧裂纹/褪色/积尘（长期自然痕迹），
// 禁止任何"刚刚发生过什么"的痕迹。
const PlanScenePreEventClause = "pre-event timeless location, no blood, no corpses, no fresh aftermath, no dropped props, no event aftermath"

// PlanSceneFixedPropMin/Max 场景卡里"环境配套物件"的配额（前中景合计 2–4 组）。
//
// 只写**固定陈设/建筑构件/自然附着物**；禁止剧情道具与角色持有物（它们属于道具卡与角色卡）。
// 少于 2 组 → 空得像建模白模；多于 4 组 → 堆成"道具清单"，模型把每件都画一遍，画面失焦。
const (
	PlanSceneFixedPropMin = 2
	PlanSceneFixedPropMax = 4
)

// PlanMaxConsecutiveSameShotSize 连续同景别的上限（整改单 §1.1 验收：连续同景别 ≤6 镜）。
const PlanMaxConsecutiveSameShotSize = 6

// PlanEndStateBannedWords 尾帧状态里**不许出现**的词（听觉/氛围/情绪类）。
//
// 理由（整改单 §1.2）：尾帧状态是写给**下游模型**看的画面交接单 —— 它只认看得见的东西
// （人物位置、姿态、表情、视线方向、环境状态）。"寂静""压抑"这类词下游画不出来，
// 写进去只会稀释真正的视觉信息，让下一镜的模型自己猜"哪里该安静"。
var PlanEndStateBannedWords = []string{
	"寂静", "安静", "沉寂", "死寂", "静谧", "宁静", "沉默", "无声", "鸦雀无声",
	"压抑", "沉闷", "肃杀", "凄凉", "氛围", "气氛", "空气凝固", "情绪",
}

// PlanViewIn 文本里出现了 9 视角词表里的哪一条（判据：逐字包含，长串优先）。
//
// 长串优先是必须的：`ultra-wide aerial view` 是 `ultra-wide 45-degree aerial view` 的子串，
// 不先匹长串就会把"斜 45° 鸟瞰"误判成普通鸟瞰（两串在提示词里同时出现时更容易判错）。
func PlanViewIn(text string) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	best := ""
	for _, v := range PlanViews {
		if strings.Contains(text, v) && len(v) > len(best) {
			best = v
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// IsEstablishingShot 第 i 镜是不是**某个场景的第一镜**（= 该场景的建立镜头）。
//
// 判据用"场景发生变化"而不是"场景首次出现"：一集里回到同一地点多次时，每次都该重新交代
// （观众刚看完别处，需要重新建立空间），所以每一段连续的同场景镜头里的第一镜都算。
func IsEstablishingShot(shots []Shot, i int) bool {
	if i < 0 || i >= len(shots) {
		return false
	}
	if strings.TrimSpace(shots[i].Scene) == "" {
		return false // 没有场景的镜头（纯空镜）无从"建立"
	}
	if i == 0 {
		return true
	}
	return strings.TrimSpace(shots[i].Scene) != strings.TrimSpace(shots[i-1].Scene)
}

// EndStateBannedIn 尾帧状态里命中的禁用词（听觉/氛围/情绪）。返回去重后的词表顺序命中项。
func EndStateBannedIn(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var hit []string
	for _, w := range PlanEndStateBannedWords {
		if strings.Contains(s, w) {
			hit = append(hit, w)
		}
	}
	return hit
}

// firstRunes 取前 n 个字符（报错文案里贴动作摘要用；**按 rune 切**，别把中文切坏）。
func firstRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// PlanViewHint 自检提示用的一行词表（报错文案里贴出来，便于照着改）。
func PlanViewHint() string {
	return fmt.Sprintf("主视角必须是官方 9 条之一（逐字）:%s", strings.Join(PlanViews, " / "))
}

// firstN 取前 n 项（报错文案里只贴几条，别把 50 个镜号全倒进日志）。
func firstN(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	out := append([]string{}, list[:n]...)
	return append(out, fmt.Sprintf("…等 %d 处", len(list)))
}

// viewKeys 把视角集合按词表顺序摊平（map 迭代顺序随机，报告里必须稳定可 diff）。
func viewKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, v := range PlanViews {
		if set[v] {
			out = append(out, v)
		}
	}
	return out
}

// longestSameShotSizeRun 最长的一段"连续同景别"。返回(景别, 起始镜号, 长度)。
//
// 判据按**景别原文**比（"中景" vs "中近景"是两种），空景别不参与（未填的镜头不该被算成
// 一段连续，否则报告会把"没写景别"说成"同景别"）。
func longestSameShotSizeRun(shots []Shot) (string, int, int) {
	bestSize, bestStart, bestLen := "", 0, 0
	curSize, curStart, curLen := "", 0, 0
	for _, sh := range shots {
		size := strings.TrimSpace(sh.ShotSize)
		if size == "" {
			curSize, curLen = "", 0
			continue
		}
		if size == curSize {
			curLen++
		} else {
			curSize, curStart, curLen = size, sh.ID, 1
		}
		if curLen > bestLen {
			bestSize, bestStart, bestLen = curSize, curStart, curLen
		}
	}
	return bestSize, bestStart, bestLen
}
