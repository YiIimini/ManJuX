package director

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// repair.go 方案的**自纠一轮**：把机器能判定的违规**连同原文**回给模型，只重写被点名的镜。
//
// 【为什么要它】2026-09-28 实测：真章节那一跑 35 镜，**35 镜全是一镜到底**（正文里只有一个
// `[Shot 1]`，一个 6-8 秒片段从头到尾演一个连续动作）—— 而提示词里**已经写了**"4 秒以上必须
// 内部切 2-4 个小镜头"这条硬规则。也就是说：**散文规则对模型不起作用**，写进提示词 ≠ 会照做。
// 用户的原话是"拖沓、没抓重点"，光加规则等于没做。
//
// 机制：契约门已经能把违规一条条点出来（而且是**逐镜**点名的，不是笼统的"格式不对"），
// 于是把它当**反馈信号**用一次 —— 像人审稿那样说"这一镜这里不行，改掉，别的地方一个字别动"。
// 只做**一轮**、只重写被点名的镜（有预算上限），并且**改坏了就退回原文**（见 adoptRepaired）。
const (
	// repairMaxShots 一轮最多重写几镜。超出的留到下一轮/人工 —— 预算失控比留着缺陷更糟
	// （用户会为了修一集反复重跑，每次几十镜的额外调用既慢又容易触发限流）。
	repairMaxShots = 12
	// repairMaxWords 正文词数上限（官方 350-500，留余量到 600；超了就是"注意力被摊薄"）。
	repairMaxWords = 600
)

// repairableIssue 一镜需要修的地方（给人看的一句话 + 给模型看的指令）。
type repairableIssue struct {
	shotID int
	note   string // 播报用（中文，说清危害）
	fix    string // 给模型的英文指令
}

// collectRepairable 逐镜找出**能靠改写修好**的问题。判据直接算，不去解析告警文案
// （文案会改，判据不会）。
func collectRepairable(plan *project.Plan) []repairableIssue {
	if plan == nil {
		return nil
	}
	var out []repairableIssue
	for _, sh := range plan.Shots {
		if strings.TrimSpace(sh.H3Prompt) == "" {
			continue
		}
		// ① 一镜到底：时长够长却只有一个内部镜头 —— 拖沓的机制（按优先级排最前）
		if sh.Duration >= project.PlanOneTakeSeconds && project.H3ClipCuts(sh.H3Prompt) < 2 {
			out = append(out, repairableIssue{
				shotID: sh.ID,
				note:   fmt.Sprintf("#%d(%ds 一镜到底)", sh.ID, sh.Duration),
				fix: fmt.Sprintf("This %d-second clip contains only ONE shot. Split it into 2-4 internal "+
					"shots of 1.5-4 seconds each, cutting on natural stops (a person settles, a turn "+
					"completes, a gaze lands, a line ends). Write them in the official multi-shot form: "+
					"keep `[Shot 1]` and add `[Shot 2] At 00:03.500, ...` etc. The timestamps must be "+
					"strictly increasing and the internal shot durations must add up to %d seconds. "+
					"Change NOTHING else about the content — same action, same dialogue, same wording.",
					sh.Duration, sh.Duration),
			})
			continue
		}
		// ② 词数超标：注意力被摊薄
		if n := project.H3DescriptionWords(sh.H3Prompt); n > repairMaxWords {
			out = append(out, repairableIssue{
				shotID: sh.ID,
				note:   fmt.Sprintf("#%d(%d 词，官方 350-500)", sh.ID, n),
				fix: fmt.Sprintf("This description is %d English words; the official range for a generation "+
					"task is 350-500. Cut it to that range by removing repeated costume/prop restatement "+
					"and generic filler sentences — keep every beat, every line and every technical "+
					"requirement (six sections, brightness guard, exclusions sentence, retention markers, "+
					"dialogue tags). Do not shorten by deleting content that matters.", n),
			})
		}
	}
	return out
}

// RepairPlan 自纠一轮：重写被点名的镜，返回(实际修好几镜, 说明)。改坏了退回原文。
//
// client 为 nil 时直接返回（没配 LLM 的部署不该因此失败）。
func RepairPlan(ctx context.Context, client *llm.Client, plan *project.Plan, opt Options) (int, string) {
	if plan == nil || client == nil {
		return 0, ""
	}
	issues := collectRepairable(plan)
	if len(issues) == 0 {
		return 0, ""
	}
	if len(issues) > repairMaxShots {
		issues = issues[:repairMaxShots]
	}
	before := map[int]string{}
	for _, sh := range plan.Shots {
		before[sh.ID] = sh.H3Prompt
	}
	fixed := 0
	for _, is := range issues {
		for i := range plan.Shots {
			sh := &plan.Shots[i]
			if sh.ID != is.shotID {
				continue
			}
			got, err := repairOneShot(ctx, client, *sh, is.fix)
			if err != nil {
				continue // 单镜失败不影响其余（也不把方案弄坏）
			}
			if adoptRepaired(sh, got) {
				fixed++
			}
		}
	}
	if fixed == 0 {
		return 0, ""
	}
	notes := make([]string, 0, len(issues))
	for _, is := range issues {
		notes = append(notes, is.note)
	}
	return fixed, fmt.Sprintf("自纠一轮:重写了 %d 镜（%s）—— 散文规则模型不照做，"+
		"所以把违规连同原文回给它改一次；改坏了的镜保留原文", fixed, strings.Join(firstN(notes, 4), "、"))
}

// repairOneShot 让模型重写这一镜。
func repairOneShot(ctx context.Context, client *llm.Client, sh project.Shot, fix string) (string, error) {
	sys := "You are fixing ONE shot prompt of an existing MiniMax H3 storyboard. " +
		"You will receive the current prompt and one specific defect. Rewrite the prompt so the defect is gone. " +
		"Hard rules: return ONLY the corrected prompt text (no JSON, no commentary, no markdown fences); " +
		"keep the same six sections and their order; keep every <d> dialogue line verbatim; " +
		"keep the style sentence, the brightness guard and the exclusions sentence; change nothing that was not named."
	user := "DEFECT TO FIX:\n" + fix + "\n\nCURRENT PROMPT:\n\"\"\"\n" + sh.H3Prompt + "\n\"\"\""
	out, err := client.Chat(ctx, []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: user}}, false)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// adoptRepaired 只在**改好了且没改坏**时才采纳：违规必须真的消失，格式检查不能变差，
// 台词条数不能变。这是"自纠"不会把方案弄坏的那道闸。
func adoptRepaired(sh *project.Shot, got string) bool {
	got = stripCodeFence(got)
	if got == "" {
		return false
	}
	beforeCuts := project.H3ClipCuts(sh.H3Prompt)
	beforeBad := len(project.H3FormatViolations(sh.H3Prompt))
	beforeLines := strings.Count(sh.H3Prompt, "<d>")
	afterCuts := project.H3ClipCuts(got)
	afterBad := len(project.H3FormatViolations(got))
	afterLines := strings.Count(got, "<d>")
	if afterBad > beforeBad {
		return false // 格式变差：退回原文
	}
	if afterLines != beforeLines {
		return false // 台词被增删：退回原文（台词是内容，不是格式）
	}
	if afterCuts <= beforeCuts && project.H3DescriptionWords(got) >= project.H3DescriptionWords(sh.H3Prompt) {
		return false // 两个毛病都没改善：不值得换
	}
	sh.H3Prompt = got
	return true
}

// stripCodeFence 去掉模型偶尔裹上的 ``` 围栏。
func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	if i := strings.Index(t, "\n"); i > 0 {
		t = t[i+1:]
	}
	if j := strings.LastIndex(t, "```"); j >= 0 {
		t = t[:j]
	}
	return strings.TrimSpace(t)
}

func firstN(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	return list[:n]
}

var _ = json.Marshal // 保持 encoding/json 在文件里有使用（后续如需结构化修复输出可直接用）
