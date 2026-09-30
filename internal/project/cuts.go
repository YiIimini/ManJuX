package project

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// cuts.go 片段内部的**小镜头**（用户 2026-09-28 的「片段 → 小镜头」两层结构）。
//
// 用户原话：「一个分镜里可以包含多个小分镜，小分镜里包含运镜」。
//
// ## 两层各是什么
//
//	片段（Shot）—— 一次生成，4–10 秒。**渲染与资产的单位**：参考图、种子、指纹、时长都挂在它上面。
//	小镜头（Cut）—— 片段内部的切点，1.5–4 秒。**镜头语言的单位**：景别、运镜、机位挂在它上面。
//
// 为什么必须有结构字段，而不是"反正正文里写着"：正文是字符串 —— 契约门只能拿正则去捞，
// 捞到了也**改不了、查不动、界面上摆不出来**。而"片段内到底切了几刀"恰恰是用户反复点的
// 那个毛病（「拖沓」「一镜到底」）：**用户文件里早写了"片段内切 1.5–4 秒小镜头"，平台读了却没用**。

// CutPlanMinSeconds / CutPlanMaxSeconds 单个小镜头的时长区间。
//
// 依据（用户文件，不是我的判断）：《【额外】仿真人10S分镜头指令》与《五、最新豆包十秒指令》
// 都写明「片段内要切 **2–4 个小镜头、每镜 1.5–4 秒**」。
const (
	CutPlanMinSeconds = 1.5
	CutPlanMaxSeconds = 4.0
	// CutPlanMinCuts / CutPlanMaxCuts 一个 4 秒以上的片段该切几刀（同一出处）。
	CutPlanMinCuts = 2
	CutPlanMaxCuts = 4
)

// Cut 片段内部的**小镜头**。
type Cut struct {
	// At 这一刀开始的时间点（秒，从片段开头算）。**第一刀必须为 0**，之后严格递增。
	At float64 `json:"at"`
	// Action 这一刀里发生的事（中文一句话）。
	Action string `json:"action,omitempty"`
	// Camera 这一刀的运镜（自然英文动作，官方运动词表；见 director 的运镜段）。
	Camera string `json:"camera,omitempty"`
	// ShotSize 这一刀的景别（中文，与 Shot.ShotSize 同一份词表）。
	ShotSize string `json:"shot_size,omitempty"`
	// View 这一刀的机位（官方 9 视角词，可空）。
	View string `json:"view,omitempty"`
}

// CutSeconds 这一刀的时长：到下一刀为止；最后一刀用片段时长补足。
//
// 越界（At 超出片段时长）时返回 0 —— 由 `CutIssues` 单独报"时间点越界"，
// 不在这里编一个负数出来（那会让"时长之和"的判据算出假象）。
func (s Shot) CutSeconds(i int) float64 {
	if i < 0 || i >= len(s.Cuts) {
		return 0
	}
	end := float64(s.Duration)
	if i+1 < len(s.Cuts) {
		end = s.Cuts[i+1].At
	}
	d := end - s.Cuts[i].At
	if d < 0 {
		return 0
	}
	return d
}

// CutIssues 两层结构的产出侧判据（返回人话，空 = 没问题）。
//
// 判据出处全部是**用户文件**（《【额外】仿真人10S分镜头指令》《五、最新豆包十秒指令》）：
//
//	① 4 秒以上的片段**必须切** 2–4 刀（一镜到底正是"拖沓"的成因）；
//	② 每刀 1.5–4 秒；
//	③ 第一刀为 0、时间点严格递增、不得超出片段时长；
//	④ 各刀时长之和 = 片段时长（误差 0.05 秒内，浮点容差）。
//
// 4 秒及以下的片段**允许只有一刀**（那就是它本身，切无可切）。
func CutIssues(p *Plan) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, sh := range p.Shots {
		if len(sh.Cuts) == 0 {
			// 没有结构数据时**不在这里报**：正文里可能已经有 `[Shot N]` 多镜形式，
			// 那是"还没解析回结构"而不是"没切"（解析入口见 ParseCutsFromPrompt）。
			// 真该报的是 `CutCoverageIssues`（它同时看正文）。
			continue
		}
		if sh.Cuts[0].At != 0 {
			out = append(out, fmt.Sprintf("片段 #%d 的第一刀不在 0 秒（%.2f）—— 第一刀必须是片段开头",
				sh.ID, sh.Cuts[0].At))
		}
		for i := 1; i < len(sh.Cuts); i++ {
			if sh.Cuts[i].At <= sh.Cuts[i-1].At {
				out = append(out, fmt.Sprintf("片段 #%d 的小镜头时间点没有严格递增（第 %d 刀 %.2f ≤ 上一刀 %.2f）",
					sh.ID, i+1, sh.Cuts[i].At, sh.Cuts[i-1].At))
			}
		}
		if last := sh.Cuts[len(sh.Cuts)-1].At; last >= float64(sh.Duration) {
			out = append(out, fmt.Sprintf("片段 #%d 有刀落在片段时长之外（%.2f ≥ %d 秒）",
				sh.ID, last, sh.Duration))
		}
		sum := 0.0
		for i := range sh.Cuts {
			d := sh.CutSeconds(i)
			sum += d
			if d > 0 && (d < CutPlanMinSeconds-0.01 || d > CutPlanMaxSeconds+0.01) {
				out = append(out, fmt.Sprintf("片段 #%d 第 %d 刀 %.2f 秒 —— 用户文件要求每刀 %.1f–%.1f 秒",
					sh.ID, i+1, d, CutPlanMinSeconds, CutPlanMaxSeconds))
			}
		}
		if math.Abs(sum-float64(sh.Duration)) > 0.05 {
			out = append(out, fmt.Sprintf("片段 #%d 的小镜头时长之和 %.2f ≠ 片段时长 %d 秒",
				sh.ID, sum, sh.Duration))
		}
		// 4 秒以上的片段必须切（用户口径：「片段内切 2–4 个小镜头」）
		if sh.Duration > int(CutPlanMaxSeconds) && len(sh.Cuts) < CutPlanMinCuts {
			out = append(out, fmt.Sprintf("片段 #%d 长 %d 秒却只切了 %d 刀 —— 用户文件要求 4 秒以上的片段切 %d–%d 刀"+
				"（一镜到底正是「拖沓」的成因）",
				sh.ID, sh.Duration, len(sh.Cuts), CutPlanMinCuts, CutPlanMaxCuts))
		}
		if len(sh.Cuts) > CutPlanMaxCuts {
			out = append(out, fmt.Sprintf("片段 #%d 切了 %d 刀，超过上限 %d —— 刀太碎观众跟不上",
				sh.ID, len(sh.Cuts), CutPlanMaxCuts))
		}
	}
	return out
}

// reCutAt 匹配官方多镜形式的切点：**行首**的 `[Shot N] At MM:SS.mmm, …`。
//
// 官方格式（`ref-en.txt` §5.2）：第一个镜头不写时间戳（它从 0 开始），
// 之后的每一镜都带 `At MM:SS.mmm`。
//
// ⚠️ **必须锚在行首**：正文里还有另一种 `[Shot N]` —— `retention_analysis` 段的
// **引用**写法，例如 `- <Subject 1> (appears in [Shot 1]): fully_preserved`。
// 第一版没锚行首，于是那份正文读出"两刀都是 0 秒"→ 被判成"不是规范的多镜形式"→ 返回 nil，
// 表现为**所有夹具都读不出小镜头**（而真相是解析器把引用当成了切点）。
var reCutAt = regexp.MustCompile(`(?im)^[ \t]*\[shot\s+(\d+)\]\s*(?:at\s+(\d+):(\d+)\.(\d{1,3}))?`)

// ParseCutsFromPrompt 从 `h3_prompt` 里读回小镜头结构（权威字段缺失时的**回填**）。
//
// 用户口径是"权威在结构字段、正文是投影"，但**存量方案与新技能侧产物**里只有正文 ——
// 这个函数就是那道桥：解析不出来时返回 nil（调用方按"没有结构数据"处理，不编）。
func ParseCutsFromPrompt(h3 string) []Cut {
	ms := reCutAt.FindAllStringSubmatch(h3, -1)
	if len(ms) < 2 {
		return nil // 只有一镜（或没有多镜形式）= 单层，没有小镜头可读
	}
	out := make([]Cut, 0, len(ms))
	base := -1
	for _, m := range ms {
		idx, _ := strconv.Atoi(m[1])
		if base < 0 {
			base = idx
		}
		at := 0.0
		if m[2] != "" {
			mm, _ := strconv.Atoi(m[2])
			ss, _ := strconv.Atoi(m[3])
			frac := 0.0
			switch len(m[4]) {
			case 1:
				frac = float64(mustAtoi(m[4])) / 10
			case 2:
				frac = float64(mustAtoi(m[4])) / 100
			default:
				frac = float64(mustAtoi(m[4])) / 1000
			}
			at = float64(mm*60+ss) + frac
		}
		out = append(out, Cut{At: at})
	}
	// 时间点必须严格递增且首刀为 0，否则这份正文不是规范的多镜形式 —— 返回 nil 而不猜。
	if out[0].At != 0 {
		return nil
	}
	for i := 1; i < len(out); i++ {
		if out[i].At <= out[i-1].At {
			return nil
		}
	}
	sortCuts(out)
	return out
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func sortCuts(c []Cut) {
	// 时间点已校验递增；这里只保证"解析顺序 = 时间顺序"这一条读起来不会骗人。
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].At < c[j-1].At; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

// CutCoverage 一个片段的小镜头覆盖情况（正文兜底读完之后的结论）。
type CutCoverage struct {
	ShotID  int
	Count   int
	FromDoc bool // true = 结构字段为空、是从正文里读回来的（提示需要回写）
}

// CutCoverageIssues 覆盖面判据：4 秒以上的片段**必须切**，一刀都不切就报。
//
// 与 `CutIssues` 的分工：那条判"切了但切得对不对"，这条判"到底切没切"——
// 判据同时看结构字段与正文（存量方案只有正文）。
func CutCoverageIssues(p *Plan) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, sh := range p.Shots {
		n := len(sh.Cuts)
		if n == 0 {
			n = len(ParseCutsFromPrompt(sh.H3Prompt))
		}
		if sh.Duration > int(CutPlanMaxSeconds) && n < CutPlanMinCuts {
			out = append(out, fmt.Sprintf("#%d(%d 秒,%d 刀)", sh.ID, sh.Duration, n))
		}
		// **刀数上限**（2026-09-29 用户：「有问题就需要进行优化调整处理，不要给我遗留问题」）。
		//
		// 【这里原来错在哪】上限那条只在 `CutIssues` 里、且只数**结构字段** `sh.Cuts`；
		// 而分镜实际是**把内切写在正文里**（`[Shot N] At MM:SS.mmm`）⇒ 结构字段是空的 ⇒
		// 上限从来没被检查过。实测两版分镜每片段切了 **5–9 刀**（用户文件要求 2–4 刀）：
		// 11 镜那版 6/8/6/6/6/6/7/6/7/6/5；16 镜那版 4/5/8/5/9/3/5/3/5/6/5/4/6/4/3/7。
		// 节拍过密有两个后果：剪辑碎，而且模型要往比台词多的节拍里填东西 ⇒ **一句台词被念两遍**。
		if n > CutPlanMaxCuts {
			out = append(out, fmt.Sprintf("#%d(%d 秒切了 %d 刀,上限 %d)", sh.ID, sh.Duration, n, CutPlanMaxCuts))
		}
	}
	return out
}

// FillCutsFromPrompt 把正文里的多镜形式回填进结构字段（幂等；已有结构数据时不覆盖）。
//
// 返回回填了几个片段。**不静默**：调用方负责播报（这一项会改变界面上"这一片段切了几刀"）。
func (p *Plan) FillCutsFromPrompt() int {
	if p == nil {
		return 0
	}
	n := 0
	for i := range p.Shots {
		if len(p.Shots[i].Cuts) > 0 {
			continue
		}
		if cuts := ParseCutsFromPrompt(p.Shots[i].H3Prompt); len(cuts) > 0 {
			p.Shots[i].Cuts = cuts
			n++
		}
	}
	return n
}

// CutsFromPromptText 供测试与工具使用：从一段正文里读小镜头（导出薄包装）。
func CutsFromPromptText(h3 string) []Cut { return ParseCutsFromPrompt(h3) }

// NormalizeCuts 把结构字段归一到"首刀 0、严格递增、最后一刀不超过片段时长"。
//
// 模型直出的小镜头常有两个毛病：首刀不写 0（写成 0.5 之类）、以及最后一刀的 At 顶到时长。
// 这里修掉的是**表示**，不是内容 —— 修不动就原样返回，让门去报。
func NormalizeCuts(sh Shot) []Cut {
	cuts := append([]Cut(nil), sh.Cuts...)
	if len(cuts) == 0 {
		return cuts
	}
	if cuts[0].At != 0 {
		cuts[0].At = 0
	}
	prev := cuts[0].At
	for i := 1; i < len(cuts); i++ {
		if cuts[i].At <= prev {
			cuts[i].At = prev + CutPlanMinSeconds
		}
		prev = cuts[i].At
	}
	// 最后一刀压回片段内（留出最小刀长）
	if last := cuts[len(cuts)-1].At; last > 0 && last >= float64(sh.Duration) {
		cuts[len(cuts)-1].At = math.Max(0, float64(sh.Duration)-CutPlanMinSeconds)
	}
	return cuts
}

// CutsToPromptSection 把小镜头渲染回**官方多镜形式**（`[Shot N] At MM:SS.mmm, …`）。
//
// 第一个镜头不写时间戳（官方 §5.2：它从 0 开始）；其余逐刀带 `At`。
// 只渲染结构里有的字段，缺的不编 —— 编出来的运镜/动作会盖掉正文里那句。
func CutsToPromptSection(cuts []Cut) string {
	if len(cuts) < 2 {
		return ""
	}
	var sb strings.Builder
	for i, c := range cuts {
		sb.WriteString("[Shot " + strconv.Itoa(i+1) + "]")
		if i > 0 {
			total := c.At
			mm := int(total) / 60
			ss := int(total) % 60
			frac := int(math.Round((total - float64(int(total))) * 1000))
			sb.WriteString(fmt.Sprintf(" At %02d:%02d.%03d,", mm, ss, frac))
		}
		for _, part := range []string{c.Action, c.Camera, c.ShotSize, c.View} {
			if strings.TrimSpace(part) != "" {
				sb.WriteString(" " + strings.TrimSpace(part) + ";")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
