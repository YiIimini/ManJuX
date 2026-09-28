package director

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// sessions.go 五步专职会话（用户 2026-09-28 架构）。
//
// ## 用户原话
//
//	「我需要一套完整的功能全面、逻辑清晰明了、功能模块化，解耦性的架构级平台应用」
//	「小说转剧本完成后，后面的人物道具、场景、分镜都可以直接同时跑起来」
//	「每步新建会话先告诉 LLM 这一步只干什么」
//
// ## 五步与它们的关系
//
//	① 小说转剧本   —— 编剧会话（实现在 server 侧：`ensureEpisodeBrief`）
//	② 人物道具提取 —— `SessionCast`：**只**产出 characters + props
//	③ 角色多视图   —— `SessionViews`：为每个角色定多视角取景方案（板/转台图的上游）
//	④ 场景提取     —— `SessionScene`：**只**产出 scenes
//	⑤ 分镜脚本     —— `SessionStoryboard`：**只**产出 shots
//
// **②④⑤ 并行**（用户点名的"同时跑起来"）——它们**互不依赖**：
// ⑤ 引用角色/场景时写的是**名字**，卡 id 由装配侧对号（`project.HealPlan`），
// 所以它不需要等 ②④ 完成。这正是"先跑起来"成立的原因，也是为什么
// **平台从"一次调用出全部"改成五个会话是可行的**。
//
// ③ 依赖 ②（要知道有哪些角色），所以排在 ② 之后、与 ④⑤ 并行。
//
// ## 与单轮直出的关系
//
// `Generate`（单轮/分批）**保持不动**：它是"一次调用出全部"的老路，存量项目与
// 没开这个开关的调用点照旧走它。本文件是**新增的一条路**，由 `Options.Sessions` 打开。
// 两条路共用同一批规则段（`sysCastRules` / `sysSceneRules` / …），不复制文案 ——
// 复制一份的下场是"改了一处、另一处还是老口径"（本仓栽过多次）。

// SessionKind 五步里的第几步。
type SessionKind string

const (
	// SessionCast ②人物道具提取：只产出 characters + props。
	SessionCast SessionKind = "cast"
	// SessionViews ③角色多视图：为每个角色定多视角的取景方案（板 + 转台图的上游）。
	SessionViews SessionKind = "views"
	// SessionScene ④场景提取：只产出 scenes。
	SessionScene SessionKind = "scene"
	// SessionStoryboard ⑤分镜脚本：只产出 shots。
	SessionStoryboard SessionKind = "storyboard"
)

// ParallelKinds 用户点名的"可以同时跑起来"的那三步（②④⑤）。
func ParallelKinds() []SessionKind {
	return []SessionKind{SessionCast, SessionScene, SessionStoryboard}
}

// sessionOutputSchema 各会话**期望的输出形状**（写进 user 段，是"先告诉 LLM 干什么"的落点）。
var sessionOutputSchema = map[SessionKind]string{
	SessionCast: `{"characters":[{"id":"char_xxx","name":"中文名","kind":"human","description":"中文外貌/服装/气质",` +
		`"image_prompt":"英文定妆提示词","board_prompt":"英文三视图板提示词","voice":"英文声线一句话"}],` +
		`"props":[{"id":"prop_xxx","name":"中文名","en":"英文外观短语","description":"中文描述(含功能作用与独有特征)",` +
		`"image_prompt":"英文道具图提示词"}]}`,
	SessionScene: `{"scenes":[{"id":"scene_xxx","name":"场景名(四字以上)","description":"中文描述",` +
		`"image_prompt":"英文场景板提示词(含九视角之一)","landmarks":"连续性地标","light_state":"日/夜与主光方向"}]}`,
	SessionViews: `{"characters":[{"id":"char_xxx","name":"中文名","board_prompt":"英文三视图板提示词"}]}`,
	SessionStoryboard: `{"style":"画风一句话","shots":[{"id":1,"scene":"场景名","characters":["角色名"],` +
		`"prop_ids":["prop_xxx"],"shot_size":"中景","camera":"运镜(自然英文动作)","angle_zh":"平视",` +
		`"action":"中文动作","duration":5,"mode":"ref2va","h3_prompt":"官方六段式英文提示词"}]}`,
}

// SessionSystem 各会话的 **system 段**：只含这一步需要的规则。
//
// 「先告诉 LLM 这一步只干什么」的落点就是这里 —— 单轮直出那份巨型 SystemPrompt
// 里同时装着角色/场景/道具/分镜四套规则，模型的实际行为是"每样都写一点、每样都不够细"
// （实测：角色卡 6 个角色只解析出 1 个、场景卡缺四要素）。
func SessionSystem(kind SessionKind, opt Options) string {
	head := "You are one specialist in a five-step short-drama pipeline. " +
		"This conversation does EXACTLY ONE step and outputs ONLY that step's JSON — " +
		"no explanations, no markdown fences, no other sections.\n\n"
	switch kind {
	case SessionCast:
		return head + castSessionRules(opt) +
			"\n\nOUTPUT (JSON only): " + sessionOutputSchema[SessionCast]
	case SessionViews:
		return head + "TASK: for EACH character given below, write the **three-view reference sheet** prompt " +
			"(three full-body views + six head angles + six detail tiles on a pure white seamless backdrop; " +
			"identity markers copied from the character's own image prompt).\n\n" +
			sysCastRules + "\n" + sysFaceVocabRules +
			"\n\nOUTPUT (JSON only): " + sessionOutputSchema[SessionViews]
	case SessionScene:
		return head + "TASK: extract the LOCATIONS of this episode as scene cards. " +
			"Write ONLY the place itself.\n\n" + sysSceneRules +
			"\n\nOUTPUT (JSON only): " + sessionOutputSchema[SessionScene]
	case SessionStoryboard:
		return head + storyboardSessionRules(opt) +
			"\n\nOUTPUT (JSON only): " + sessionOutputSchema[SessionStoryboard]
	}
	return head
}

// castSessionRules ② 的规则段（人物 + 道具）。
func castSessionRules(opt Options) string {
	var sb strings.Builder
	sb.WriteString("TASK: extract the CAST (characters) and the KEY PROPS of this episode as cards.\n")
	sb.WriteString("Characters: identity features first and dominant; every character must differ in face shape, " +
		"age read, build and signature marks; never beautify what the source text states.\n")
	sb.WriteString("Props: only things that recur or that the plot turns on; each card must state its " +
		"FUNCTION (including its limits — those limits are often the plot's key) and its UNIQUE MARK.\n")
	sb.WriteString("\n" + sysCastRules + "\n" + sysFaceVocabRules + "\n" + sysPropRules)
	if opt.AnimeFaces {
		sb.WriteString("\n" + animeFaceLock)
	}
	return sb.String()
}

// storyboardSessionRules ⑤ 的规则段（分镜）。
func storyboardSessionRules(opt Options) string {
	var sb strings.Builder
	sb.WriteString("TASK: turn this episode into a shot list. Every shot is ONE generation of 4–10 seconds; " +
		"a shot longer than 4 seconds MUST be cut into 2–4 inner shots (1.5–4 s each) inside its h3_prompt. " +
		"Reference characters and scenes BY NAME — the ids are resolved downstream.\n\n")
	sb.WriteString(sysShotRules + "\n" + sysViewRules + "\n" + sysContinuityRules + "\n" + sysShotTableRules + "\n" +
		sysDynamicShotRules + "\n" + sysHighlightRules + "\n" + sysBlockingRules + "\n" + sysH3Format)
	return sb.String()
}

// SessionJob 一次专职会话的输入。
type SessionJob struct {
	Kind SessionKind
	User string // 这一步的正文（原文片段 + 已确立的卡清单 + 设定集摘要）
}

// SessionResult 一次专职会话的结果。
//
// **错误是逐会话的**：一个会话失败不该拖垮同批的其它会话（它们互不依赖）——
// 失败的那一步由调用方决定是重试还是降级，其余结果照常可用。
type SessionResult struct {
	Kind    SessionKind
	Plan    *project.Plan
	Err     error
	Elapsed time.Duration
}

// SessionBrief 把"已确立的卡片清单 + 设定集摘要"拼成 user 段的前半部分。
//
// 各会话都要它：② 要避免与既有角色重名重设定、④ 要复用既有场景名、
// ⑤ 要**按名字**引用前两步的产出。设定集摘要是"这一步的硬锚"（数字锚尤其）。
func SessionBrief(kind SessionKind, plan *project.Plan, settingBrief string) string {
	var sb strings.Builder
	if s := strings.TrimSpace(settingBrief); s != "" {
		sb.WriteString(s + "\n\n")
	}
	if plan == nil {
		return sb.String()
	}
	if len(plan.Characters) > 0 && kind != SessionScene {
		sb.WriteString("【已确立的角色（**沿用原名与设定**，不要另起一套）】\n")
		for _, c := range plan.Characters {
			line := "- " + c.Name
			if d := strings.TrimSpace(c.Description); d != "" {
				line += "：" + clampRunes(d, 80)
			}
			sb.WriteString(line + "\n")
		}
	}
	if len(plan.Scenes) > 0 && kind != SessionCast {
		sb.WriteString("【已确立的场景名（再次出现时沿用原名）】\n")
		names := make([]string, 0, len(plan.Scenes))
		for _, sc := range plan.Scenes {
			names = append(names, sc.Name)
		}
		sb.WriteString("- " + strings.Join(names, "、") + "\n")
	}
	return sb.String()
}

// RunSessionsParallel 并行跑多个专职会话，返回**按 Kind 排序**的结果。
//
// 并行是用户点名的要求（"后面的人物道具、场景、分镜都可以直接同时跑起来"），
// 而它成立的前提写在文件头：三步互不依赖（⑤ 引用 ②④ 用的是名字，id 由装配侧对号）。
//
// 并发度**不设上限**：调用方给几个会话就跑几个 —— 这一步的开销全在 LLM 的往返上，
// 而它本来就是远端并发；限流是调用方（批量产线）的事，不在这里藏一个数字。
func RunSessionsParallel(ctx context.Context, client *llm.Client, jobs []SessionJob,
	opt Options, onNote func(format string, args ...any)) []SessionResult {

	if onNote == nil {
		onNote = func(string, ...any) {}
	}
	out := make([]SessionResult, len(jobs))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job SessionJob) {
			defer wg.Done()
			start := time.Now()
			res := SessionResult{Kind: job.Kind}
			// **每个会话独立超时与独立错误**：一个挂了不影响同批其它会话。
			plan, err := chatPlan(ctx, client, SessionSystem(job.Kind, opt), job.User)
			res.Plan, res.Err, res.Elapsed = plan, err, time.Since(start)
			if err != nil {
				onNote("⚠ %s 会话失败（其余会话不受影响）：%v", sessionLabel(job.Kind), err)
			} else {
				onNote("✔ %s 会话完成（%.1fs）", sessionLabel(job.Kind), res.Elapsed.Seconds())
			}
			out[i] = res
		}(i, job)
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// MergeSessions 把各会话的产出**归并成一份方案**。
//
// 纪律：**后到的会话不覆盖先到的同名字段**（彼此不依赖，所以谁先回来都可能）——
// 但**同一步内部的多次产出**以最后一次为准（重试语义）。
// 归并只做"填字段"，不在这里做任何对号与补齐（那是 `project.HealPlan` 的事）。
func MergeSessions(results []SessionResult) *project.Plan {
	plan := &project.Plan{}
	for _, r := range results {
		if r.Err != nil || r.Plan == nil {
			continue
		}
		p := r.Plan
		if len(p.Characters) > 0 {
			plan.Characters = mergeCharacters(plan.Characters, p.Characters)
		}
		if len(p.Props) > 0 {
			plan.Props = append(plan.Props, p.Props...)
		}
		if len(p.Scenes) > 0 {
			plan.Scenes = append(plan.Scenes, p.Scenes...)
		}
		if len(p.Shots) > 0 {
			plan.Shots = p.Shots
		}
		if strings.TrimSpace(plan.Style) == "" {
			plan.Style = strings.TrimSpace(p.Style)
		}
	}
	return plan
}

// mergeCharacters 按**卡片 id 优先、其次名字**合并角色（与平台的关联口径一致）。
func mergeCharacters(dst, src []project.Character) []project.Character {
	idx := map[string]int{}
	for i, c := range dst {
		idx[characterKey(c)] = i
	}
	for _, c := range src {
		k := characterKey(c)
		if i, ok := idx[k]; ok {
			// 后到的**补空字段**，不覆盖已有内容（两个会话可能都提了同一个角色）
			if dst[i].ImagePrompt == "" {
				dst[i].ImagePrompt = c.ImagePrompt
			}
			if dst[i].BoardPrompt == "" {
				dst[i].BoardPrompt = c.BoardPrompt
			}
			if dst[i].Description == "" {
				dst[i].Description = c.Description
			}
			continue
		}
		idx[k] = len(dst)
		dst = append(dst, c)
	}
	return dst
}

func characterKey(c project.Character) string {
	if id := strings.TrimSpace(c.ID); id != "" {
		return "id:" + id
	}
	return "name:" + strings.TrimSpace(c.Name)
}

// sessionLabel 给用户看的会话名（"②人物道具提取"这种）。
func sessionLabel(k SessionKind) string {
	switch k {
	case SessionCast:
		return "②人物道具提取"
	case SessionViews:
		return "③角色多视图"
	case SessionScene:
		return "④场景提取"
	case SessionStoryboard:
		return "⑤分镜脚本"
	}
	return string(k)
}

// SessionLabel 供包外播报用。
func SessionLabel(k SessionKind) string { return sessionLabel(k) }

// clampRunes 截到 n 个字符（提示词里的"已确立清单"只要够认人）。
func clampRunes(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// GenerateSessions 五步专职会话的**编排入口**（用户 2026-09-28 架构）。
//
// 与 `Generate` 的关系：那条是"一次调用出全部"的老路，这条是**新路**。两条都保留 ——
// 存量项目与没开开关的调用点照旧走老路（本仓纪律：改结构另开一次，别和逻辑改动混在一起）。
//
// 编排（**②④⑤ 并行**，③ 依赖 ②）：
//
//	②人物道具 ┐
//	④场景     ├─ 同时跑（互不依赖：⑤ 引用它们用的是**名字**，id 由装配侧对号）
//	⑤分镜     ┘
//	③多视图   ← ② 完成之后（它要知道有哪些角色）
//
// `settingBrief` 是设定集摘要（`project.SettingBook.SettingBrief`）——
// 它是"这一步的硬锚"：数字锚尤其（"现在是哪一年"不再集集不同）。
// 传空串就是"这部戏没有设定集"，照旧跑。
func GenerateSessions(ctx context.Context, client *llm.Client, sourceText, settingBrief string,
	opt Options) (*project.Plan, error) {

	onNote := opt.OnNote
	if onNote == nil {
		onNote = func(string, ...any) {}
	}
	// ②④⑤ 的 user 段：已确立清单（首轮为空，只有设定集）+ 本集原文
	base := SessionBrief(SessionCast, nil, settingBrief)
	user := base + "\n【本集原文】\n" + sourceText
	jobs := make([]SessionJob, 0, 3)
	for _, k := range ParallelKinds() {
		jobs = append(jobs, SessionJob{Kind: k, User: user})
	}
	onNote("五步专职会话：②人物道具 / ④场景 / ⑤分镜 **同时跑**")
	res := RunSessionsParallel(ctx, client, jobs, opt, onNote)
	if err := ErrAllSessionsFailed(res); err != nil {
		return nil, err
	}
	plan := MergeSessions(res)

	// ③ 角色多视图：依赖 ② 的产出（要知道有哪些角色），与上面三步**串行**。
	if len(plan.Characters) > 0 {
		vres := RunSessionsParallel(ctx, client,
			[]SessionJob{{Kind: SessionViews,
				User: SessionBrief(SessionViews, plan, "") + "\n【本集原文】\n" + sourceText}},
			opt, onNote)
		if len(vres) > 0 && vres[0].Err == nil {
			plan = MergeSessions(append(res, vres[0]))
		}
	}
	if planHasNothing(plan) {
		return nil, fmt.Errorf("专职会话跑完但没有任何产出")
	}
	return plan, nil
}

// planHasNothing 方案里一个字段都没有（合并后用来判"整批会话都白跑了"）。
func planHasNothing(p *project.Plan) bool {
	return p == nil || (len(p.Characters) == 0 && len(p.Props) == 0 && len(p.Scenes) == 0 && len(p.Shots) == 0)
}

// ErrAllSessionsFailed 所有会话都失败时给调用方的错误（别让"零产出"当成成功落盘）。
func ErrAllSessionsFailed(results []SessionResult) error {
	if len(results) == 0 {
		return fmt.Errorf("没有会话可跑")
	}
	for _, r := range results {
		if r.Err == nil && !planHasNothing(r.Plan) {
			return nil
		}
	}
	return fmt.Errorf("五个专职会话全部失败或全部空产出")
}
