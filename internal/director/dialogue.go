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
const CharsPerSecond = 4.5

// MinShotSeconds / MaxShotSeconds 单镜时长的合法区间。
//
// 下限 4 秒是 H3 帧网格与叙事节拍的实际需要;上限 10 秒不是"不许更长",而是
// **自动抬时长的天花板**:再长就该拆镜了(本地单镜渲染成本随帧数超线性增长,
// 见 sysShotRules),所以到顶后只报告不继续加。
const (
	MinShotSeconds = 4
	MaxShotSeconds = 10
)

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
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		want := NeededDuration(*sh)
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
		details = append(details, fmt.Sprintf("镜 %d:%d→%d 秒(台词装得下)", sh.ID, sh.Duration, want))
		sh.Duration = want
		adjusted++
	}
	return adjusted, details
}
