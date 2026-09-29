package director

import (
	"fmt"

	"github.com/YiIimini/ManJuX/internal/project"
)

// dialogue.go 台词时长预算:让「镜头放得下这句台词」变成代码保证,而不是一句提示词。
//
// 为什么必须有(2026-09-24 用户报「配音也不完整」):H3 是**音视频一起生成**的,
// 视频长度由镜头时长决定;台词说多久由文字长度决定 —— 两者对不上时,模型只会把话说
// 到被截断为止,成片里就是"最后半句没说完"。LLM 侧写了"台词 ≤20 字"的规则,但
// 20 字普通话约需 4.4 秒,落在 4 秒镜头里必然被切;实测一批分镜里这种"刚好装不下"
// 的镜头相当普遍。
//
// 所以这里在**存盘前**逐镜核算:装不下就按时长抬(最多到 MaxShotSeconds),抬不动
// 就如实报出来让用户拆镜 —— 不静默放行。

// CharsPerSecond 普通话口播速率(字/秒)。取偏保守的中速值:偏慢估比偏快估安全,
// 宁可镜头长一点也不要截断台词。
// 【2026-09-28 校正 4.5 → 4.1】与 project.PlanCharsPerSecond **必须同源**（有测试钉着这一点）。
// 依据：H3 公开输入契约文档（`drama-skills` 的 `references/minimax-h3.md`）写明"中文约 4.1 可发声字/秒"；
// 用户《【额外】仿真人10S分镜头指令》给的区间是 3.5~4.5（4.1 在区间内）。取偏小值更安全：
// 高估语速会让时长预算偏少，台词塞不进镜头（成片里表现为赶词、吞字）。
const CharsPerSecond = 4.1

// MinShotSeconds / MaxShotSeconds 单镜时长的**渲染窗口**(与计划契约同源的合法区间)。
//
// 下限 4 秒是 H3 帧网格与叙事节拍的实际需要;上限 12 秒是**合法硬上限**
// (2026-09-29 用户:「分镜渲染时间不超过12S,最高建议10S左右最合适」),
// 而**自动抬时长的天花板**仍是建议值 10 秒(project.PlanRecommendedShotSeconds):
// 台词排不开时才允许到 11/12,再长就该拆镜了(本地单镜渲染成本随帧数超线性增长,
// 见 sysShotRules),所以到顶后只报告不继续加。
//
// ⚠ 它不是生成侧的下限 —— 那一个是 GenMinShotSeconds(5)。两个数各管一件事:
// 4 是"能渲",5 是"该生成",关系由 plan_contract_ext_test.go 钉住(那里写清了为什么
// 窗口不能跟着抬)。
const (
	// 与计划契约**同源**（此前是两处字面量 + 一条测试钉相等；现在直接引用契约，
	// 少一处会漂的常量）。单镜合法区间：4–12 秒。
	MinShotSeconds = project.PlanMinShotSeconds
	MaxShotSeconds = project.PlanMaxShotSeconds
)

// GenMinShotSeconds LLM 直出路径的单镜**生成下限**(唯一一处;提示词由它渲染)。
//
// 为什么生成下限是 5、而渲染窗口仍从 4 起(2026-09-26 平台侧整改清单 §1.3):
//   - 云端引擎会把 <5 秒**静默**抬到 5(下限见 h3api.MinDurationSeconds,
//     行为由 h3api_test.go 的 clampDuration 表钉住)。LLM 直出路径此前按 4 秒出镜,
//     于是成片比计划长 —— 计划与成片时长不一致;
//   - 本地引擎也不是真的"吃 4 秒":comfyengine 按 17k+5 网格**向上**对齐,
//     4 秒(96 帧)提交成 107 帧 = 4.46 秒。所以没有"只让云端避开"的必要,生成侧统一取 5;
//   - 渲染窗口(MinShotSeconds=4)不能跟着抬:技能端旧包还在窗口内(仓库里那份真产物夹具
//     internal/project/testdata/skill-produced-plan-ep01.json 有 35 个 4 秒镜),
//     抬上去等于**整本拒收**技能端产物 —— 那是契约版本级的改动,要两侧同改同提交。
//     技能端 v2.2 起 `最短镜秒` 默认已是 5,新包不受影响。
//
// 与云端 ≥5 秒对齐,避免计划与成片时长不一致。
const GenMinShotSeconds = 5

// SpeechSeconds 估算一段中文台词的纯口播时长(秒)。
//
// 标点不单独计时(它已经包含在语速里),空白与换行不计;末尾留 0.3 秒收尾,
// 免得"刚好卡在最后一个字"。
func SpeechSeconds(text string) float64 {
	n := 0
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u3000' {
			continue
		}
		n++
	}
	if n == 0 {
		return 0
	}
	return float64(n)/CharsPerSecond + 0.3
}

// dialogueChars 该镜台词 + 旁白的总字数(空白不计)。
func dialogueChars(sh project.Shot) int {
	n := 0
	count := func(s string) {
		for _, r := range s {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u3000' {
				continue
			}
			n++
		}
	}
	for _, l := range sh.Dialogue {
		count(l.Text)
	}
	count(sh.Narration)
	return n
}

// NeededDuration 该镜要把台词(含旁白)完整说清所需的秒数(向上取整,且有下限)。
// 无台词镜头返回 MinShotSeconds。
func NeededDuration(sh project.Shot) int {
	sec := 0.0
	for _, l := range sh.Dialogue {
		sec += SpeechSeconds(l.Text)
	}
	sec += SpeechSeconds(sh.Narration)
	need := int(sec + 0.999) // 向上取整
	if need < MinShotSeconds {
		need = MinShotSeconds
	}
	return need
}

// DialogueOverflow 台词装不下时的人类可读说明(拆镜建议),空 = 无问题。
//
// 必须在 FitDialogueDurations 之后判断也能成立:那时时长已被抬到上限,而
// NeededDuration 仍返回真实需求(不被夹),所以"抬到顶还是不够"能被如实报出来。
func DialogueOverflow(sh project.Shot) string {
	need := NeededDuration(sh)
	if need <= MaxShotSeconds {
		return ""
	}
	chars := dialogueChars(sh)
	return fmt.Sprintf("镜 %d 的台词约 %d 字,需约 %.1f 秒,超过单镜上限 %d 秒 —— "+
		"本次已按上限 %d 秒渲染,建议手动拆成两镜、把台词分开说",
		sh.ID, chars, float64(chars)/CharsPerSecond+0.3,
		MaxShotSeconds, MaxShotSeconds)
}

// FitDialogueDurations 逐镜把时长抬到"装得下台词"。
//
// 返回被调整的镜头数与明细(供日志如实回显)。只**加长**、绝不缩短:缩短会引入新的
// 截断,而加长的代价只是一点渲染时间。已经够长的镜头原样不动(仅把短于下限的抬到下限)。
func FitDialogueDurations(plan *project.Plan) (adjusted int, details []string) {
	if plan == nil {
		return 0, nil
	}
	// cutsDroveIt 记下"这条时长是被切镜需求抬起来的"镜号 —— 播报里要说清是台词还是切镜抬的，
	// 否则用户看到"4→7 秒"不知道原因。
	var cutsDroveIt []int
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		want := NeededDuration(*sh)
		// **切镜需要的时间也要算**（2026-09-28 全面审计，来源：开源参考实现 shuohao-skills
		// Apache-2.0「每拍几秒是**算出来的**，不要让模型估」＋ 用户《【额外】仿真人10S分镜头指令》
		// 「每个片段内拆成若干小镜头，每个镜头 1.5–4 秒」）。
		//
		// 病根：平台此前只按**台词**算时长 ⇒ 一个 4 秒片段里被要求切 4 刀，每刀只有 1 秒，
		// 低于"每镜 1.5 秒"的下限 ⇒ 模型要么草草带过、要么根本不切（实测那一集 35/35 镜一镜到底）。
		// 有了这条下界，"切几刀"与"给多少秒"就对上了：刀数 × 1.5 秒是地板。
		if floors := cutsFloorSeconds(*sh); floors > want {
			want = floors
			cutsDroveIt = append(cutsDroveIt, sh.ID)
		}
		if want > MaxShotSeconds {
			want = MaxShotSeconds
		}
		if sh.Duration >= want {
			if sh.Duration < MinShotSeconds {
				details = append(details, fmt.Sprintf("镜 %d:%d→%d 秒(补足时长下限)", sh.ID, sh.Duration, MinShotSeconds))
				sh.Duration = MinShotSeconds
				adjusted++
			}
			continue
		}
		reason := "台词装得下"
		for _, id := range cutsDroveIt {
			if id == sh.ID {
				reason = fmt.Sprintf("切镜需要(片内 %d 刀 × %.1f 秒)", project.H3ClipCuts(sh.H3Prompt), MinCutSeconds)
			}
		}
		details = append(details, fmt.Sprintf("镜 %d:%d→%d 秒(%s)", sh.ID, sh.Duration, want, reason))
		sh.Duration = want
		adjusted++
	}
	return adjusted, details
}

// MinCutSeconds 片段内**每一刀**的最短时长（用户《【额外】仿真人10S分镜头指令》：
// 「每个镜头时长建议控制在 1.5～4 秒」）。切镜需要的时间下限就是"刀数 × 这个值"。
const MinCutSeconds = 1.5

// cutsFloorSeconds 这一镜"切了几刀"所需要的最短时长（刀数按**内部切点数**算，至少 1）。
//
// 来源：shuohao-skills 的「每拍几秒是算出来的，不要让模型估」（Apache-2.0）＋ 用户分镜文件的
// 「片段内每个小镜头 1.5–4 秒」。这条下界把"要求切 4 刀"和"只给 4 秒"这种自相矛盾挡在存盘前。
func cutsFloorSeconds(sh project.Shot) int {
	cuts := project.H3ClipCuts(sh.H3Prompt)
	if cuts < 1 {
		cuts = 1
	}
	return int(float64(cuts)*MinCutSeconds + 0.999)
}
