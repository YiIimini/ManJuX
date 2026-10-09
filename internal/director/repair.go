package director

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

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
	// repairConcurrency 自纠一轮的并发度。
	//
	// 各镜之间**互不影响**（每镜只改自己的提示词，指令里明写"别的地方一个字别动"），
	// 串行等纯属浪费：实测那一集 12 镜打满上限，光这一段就十几分钟。
	// 但也不能无限并发 —— 一次几十个请求会把接口压到限流，限流一开整轮都变慢。
	repairConcurrency = 4
	// repairMaxCameraRewrites 一轮里**因为整集运镜单调**而重写的镜数上限。
	//
	// 取 4：整集换掉三四镜就足以打破「一直是推/拉」（判据要求铺开到 4 种），
	// 再多就是替导演改分镜了 —— 运镜是叙事选择，平台只把"一潭死水"撬开一个口子。
	repairMaxCameraRewrites = 4
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
		// **空提示词要修，不能跳过**（2026-09-30 用户：「分镜脚本输出不审查？」）。
		//
		// 这里原来 `continue` 跳过空提示词的镜 —— 后果正是第 02 集镜 15 那次：分镜没写出正文
		// （0 词、无 `<d>`、无内切），自纠环**不看它**、预检的 fail 又拦不住渲染，
		// 于是引擎收到空提示词、出一帧 94% 黑，白烧一次 GPU 作业。
		// 空镜恰恰是**最该喂回去重写**的那一类：给它整镜的内容（动作 + 内切 + 台词 + 六段式）。
		if strings.TrimSpace(sh.H3Prompt) == "" {
			out = append(out, repairableIssue{
				shotID: sh.ID,
				note:   fmt.Sprintf("#%d(提示词是空的)", sh.ID),
				fix:    emptyShotFix(plan, sh),
			})
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
	// ④ 整集运镜单调 —— **有界**（2026-10-09 用户：「场景和分镜运镜很单一」）。
	//
	// 这是**整集级**的问题（判据见 project.CameraMonotoneKinds，与渲染前门同一处阈值），
	// 但修法能逐镜落地：把几镜的**运镜那一句**换成整集**没用过**的官方运动类型。
	// 三个约束保命：
	//   ① 上限 repairMaxCameraRewrites 镜 —— 整集换三四镜就足以打破"一直是推/拉"；
	//   ② 指令里明写「动作/台词/时长/六段式/亮度与排除句一个字不许动」，只换运镜那一句；
	//   ③ 这一段放在**逐镜循环之外**：循环里那三条（空提示词 / 一镜到底 / 词数超标）各自
	//      都 `continue` 了，写在里面就会被它们跳过（实测：12 镜全被"一镜到底"吃掉，
	//      运镜修复一条都收不到）。
	// 追加在最后也是这个道理：真缺陷先占住 repairMaxShots 的预算，运镜单调不该把它们挤掉
	// （运镜难看是一回事，那一镜出不来是另一回事）。
	if used, unused, _, monotone := project.CameraMonotoneKinds(plan.Shots); monotone {
		rewrites := 0
		for _, sh := range plan.Shots {
			if rewrites >= repairMaxCameraRewrites {
				break
			}
			// 空提示词的镜由上面那条单独修（它会整镜重写）；认不出运动类型的镜不动 ——
			// 不知道它是什么，就没有理由说它"和别的镜重复"。
			if strings.TrimSpace(sh.H3Prompt) == "" || len(project.CameraKindsIn(sh.Camera)) == 0 {
				continue
			}
			rewrites++
			out = append(out, repairableIssue{
				shotID: sh.ID,
				note: fmt.Sprintf("#%d(运镜与整集其它镜重复：整集只用过 %d 种运镜「%s」)",
					sh.ID, len(used), strings.Join(used, "/")),
				fix: fmt.Sprintf("Every clip in this episode uses only %d camera motions (%s), so the whole "+
					"episode feels like one long camera move however the shot sizes change. Rewrite ONLY the "+
					"camera-motion sentence of this shot: replace it with ONE of the official motions listed in "+
					"the `camera` field spec that this episode has NOT used yet (%s) — pick the one that fits "+
					"what actually happens in this shot, and write it as a natural English action "+
					"(`The camera … `). Change NOTHING else: same action, same dialogue lines verbatim, same "+
					"duration, same timestamps, the same internal-shot structure, the same brightness and "+
					"exclusion sentences.",
					len(used), strings.Join(used, "/"), strings.Join(unused, ", ")),
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
	idxOf := map[int]int{} // 镜号 → plan.Shots 下标
	for i, sh := range plan.Shots {
		before[sh.ID] = sh.H3Prompt
		idxOf[sh.ID] = i
	}
	// **并发修**（2026-09-28 用户问「解析一集为什么要那么久」）：原来一镜一次调用、串行等，
	// 12 镜就是 12 次串行调用 —— 实测那一集正好打满上限，光这一段就十几分钟。
	// goroutine 里只**读**自己那一镜，写回统一在主协程做（合并顺序也稳定，无数据竞争）。
	type repairOut struct {
		idx int
		got string
		ok  bool
	}
	outs := make([]repairOut, len(issues))
	var wg sync.WaitGroup
	sem := make(chan struct{}, repairConcurrency)
	for k, is := range issues {
		i, ok := idxOf[is.shotID]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(k, i int, fix string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got, err := repairOneShot(ctx, client, plan.Shots[i], fix)
			if err != nil {
				return // 单镜失败不影响其余（也不把方案弄坏）
			}
			outs[k] = repairOut{idx: i, got: got, ok: true}
		}(k, i, is.fix)
	}
	wg.Wait()
	// 合并按 issues 顺序串行做：adoptRepaired 会改方案与指纹，不能并发写
	fixed := 0
	for _, o := range outs {
		if !o.ok {
			continue
		}
		if adoptRepaired(&plan.Shots[o.idx], o.got) {
			fixed++
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
	// 台词条数：**原文有 `<d>` 时不许增删**（台词是内容，不是格式）。
	//
	// 【原文没有 `<d>` 是例外（2026-10-08 真机《外卖员写了七千封回信》第01集镜头 8 现场）】
	// 那一镜的 `h3_prompt` 是**空的**，修复指令写的是"把整镜写出来"—— 而写出来就**必然**要补台词
	// （0 → 计划里那两句）。原来这条闸门一律要求 `afterLines == beforeLines`（0），于是把
	// **正确的修复**判成"台词被增删"、退回原文（原文就是空的）⇒ 那一镜永远补不上 ⇒
	// 渲染阶段被致命门「h3_prompt 为空」拦住、整条产线中止（真机现场：15 件资产全生成完
	// 之后才拦，白跑一整段资产阶段；短篇那一本正是这么卡住的）。
	// 所以原文没有 `<d>` 时，允许补到**计划里那几句**（`PlanSpokenTexts`，含旁白），
	// 但仍不许凭空多出计划外的台词 —— 安全阀的本意（"台词是内容"）不丢。
	wantLines := beforeLines
	if beforeLines == 0 {
		wantLines = len(project.PlanSpokenTexts(sh))
	}
	if afterLines != wantLines {
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

// orDash 空串给一个占位符（拼给模型的指令里不能出现空值）。
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(the plan gives no action text for this shot — infer it from the episode brief)"
	}
	return s
}

// emptyShotFix 空提示词的修复指令：**必须把这一镜已有的材料全给模型**。
//
// 【2026-10-08 真机《我捡的团子，暖了三千人的船》第01集镜 12 —— 用户第二次报「还是报一样的错误」】
// 这条指令原来只写了「时长 + 动作」，而那一镜的 `action` **本身也是空的**（`orDash` 成了
// 「(the plan gives no action text — infer it from the episode brief)」）：模型手上没有任何**具体**
// 内容（哪几句要说、谁在说、在哪儿、什么景别都不知道），写出来的正文里自然没有计划里那两句
// `<d>` ⇒ `adoptRepaired` 的「台词条数必须等于计划里那两句」把它判成改坏了、退回原文
// （原文是空的）⇒ **这一镜永远补不上** ⇒ 渲染入口的致命门「h3_prompt 为空」不让启动
// ⇒ 48 集的批量产线停在第 1 集。**点名生效、修复不生效**就是这么来的。
//
// 台词、人物、场次、景别、机位本来就是**已知量**，交给模型就是了 —— 这也是「能确定性做到的事，
// 不要让模型自觉」。逐句清单同时给了模型一个**可机检的靶子**（`adoptRepaired` 就按它数台词）。
func emptyShotFix(plan *project.Plan, sh project.Shot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This %d-second shot has NO prompt text at all — write the whole thing now. ", sh.Duration)
	b.WriteString("Everything you need is listed below; do not invent new beats, new lines or new people.\n")
	if sc := strings.TrimSpace(sh.Scene); sc != "" {
		fmt.Fprintf(&b, "- Scene (the LAST attached picture): %s\n", sceneLabel(plan, sc))
	}
	if len(sh.Characters) > 0 {
		fmt.Fprintf(&b, "- People in this shot (their front sheets are attached, in this order): %s\n",
			charLabels(plan, sh, sh.Characters))
	}
	if sz := strings.TrimSpace(sh.ShotSize); sz != "" {
		fmt.Fprintf(&b, "- Shot size: %s\n", sz)
	}
	if cam := strings.TrimSpace(sh.Camera); cam != "" {
		fmt.Fprintf(&b, "- Camera: %s\n", cam)
	}
	if act := strings.TrimSpace(sh.Action); act != "" {
		fmt.Fprintf(&b, "- The beat of this shot: %s\n", act)
	} else {
		b.WriteString("- The beat of this shot is not written in the storyboard: keep the staging above " +
			"and let the lines below play. Do NOT invent new action, and do NOT add lines.\n")
	}
	lines := fixSpokenLines(sh)
	if len(lines) > 0 {
		fmt.Fprintf(&b, "- Spoken lines: this shot has EXACTLY %d line(s), each spoken once, word for word:\n",
			len(lines))
		for i, l := range lines {
			who := strings.TrimSpace(l.Speaker)
			if who == "" {
				who = "旁白"
			}
			osTag := ""
			if l.OS {
				osTag = " (off-screen: the lips of anyone in frame stay closed)"
			}
			fmt.Fprintf(&b, "  %d. %s%s: %s\n", i+1, who, osTag, l.Text)
		}
		b.WriteString("Put each of them inside its own <d>[Chinese]…</d> tag, copied verbatim, and put " +
			"NO other <d> line anywhere in the prompt.\n")
	} else {
		b.WriteString("- Spoken lines: none — this shot has no dialogue, so the clip carries no voice at all " +
			"(ambient sound only).\n")
	}
	b.WriteString("Use the same official six-section form as the other shots: subject_definitions " +
		"(one <Subject N> per attached front sheet, numbered from 1, plus the scene line), " +
		"summary (starting with [reference generation]), retention_analysis (official retention words " +
		"only — no (Sx) speaker tags in that section), detailed_description (the style sentence first, " +
		"then the shot list: one [Shot 1] with NO timestamp plus [Shot N] At MM:SS.mmm cuts so that every " +
		"cut lasts 1.5-4 s and the cuts add up to the full duration, then the brightness guard and the " +
		"exclusions sentence), overall_soundscape and non_diegetic_music. " +
		"Change NOTHING about the other shots — reply with this one shot's prompt text only.")
	return b.String()
}

// sceneLabel 场景的"id(名字)"写法，给模型看的材料里用它（两个都写，改过名字也不会错位）。
func sceneLabel(plan *project.Plan, id string) string {
	if plan != nil {
		for i := range plan.Scenes {
			if strings.EqualFold(plan.Scenes[i].ID, id) || plan.Scenes[i].Name == id {
				if n := strings.TrimSpace(plan.Scenes[i].Name); n != "" && n != id {
					return id + " (" + n + ")"
				}
				break
			}
		}
	}
	return id
}

// charLabels 人物清单（名字 + 本镜走位）；按 id/名字都能查到卡（存量方案两种写法都有）。
func charLabels(plan *project.Plan, sh project.Shot, ids []string) string {
	var parts []string
	for _, id := range ids {
		name := id
		if plan != nil {
			for i := range plan.Characters {
				c := plan.Characters[i]
				if strings.EqualFold(c.ID, id) || c.Name == id {
					if n := strings.TrimSpace(c.Name); n != "" {
						name = n
					}
					break
				}
			}
		}
		// 走位表的键**两种形态都有**：新方案按 id 写，存量/技能侧产物按**名字**写
		//（真机镜 12 就是名字：{"姜晚舟": "画面左三分之一…"}）—— 只按 id 查会静默丢掉走位，
		// 而"人物在画面哪儿"正是模型写这一镜最需要的信息。两个键都试。
		pos := strings.TrimSpace(sh.Positions[id])
		if pos == "" {
			pos = strings.TrimSpace(sh.Positions[name])
		}
		if pos != "" {
			name += " (" + pos + ")"
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ", ")
}

// fixSpokenLines 这一镜要说的话（台词在前、旁白最后，与 project.PlanSpokenTexts 同序）。
// 它与装配侧是**同一条判据**：模型照这份清单写，`adoptRepaired` 就数得对。
func fixSpokenLines(sh project.Shot) []project.Line {
	var out []project.Line
	for _, l := range sh.Dialogue {
		if strings.TrimSpace(l.Text) != "" {
			out = append(out, l)
		}
	}
	if n := strings.TrimSpace(sh.Narration); n != "" {
		out = append(out, project.Line{Speaker: "旁白", Text: n, OS: true})
	}
	return out
}
