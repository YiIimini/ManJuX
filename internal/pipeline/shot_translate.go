package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// shot_translate.go 分镜「动作 / 人物走位」的**英文版**（2026-10-08）。
//
// ## 为什么要有它（用户：「还没做/边界，给我留着吗？」）
//
// 平台的兜底正文（`project/empty_body.go`）只能放**英文材料** —— 汉字出现在 `<d>` 外会被入口的
// 汉字卫生门拒收（>200 即 fail，见 `project/prompt_cjk.go`）。而分镜的 `action` / `positions`
// 原本**只有中文**，于是"模型没写出正文"的那几镜，画面调度就只能靠参考图与机位兜 ——
// **那等于我把缺口留给了用户**。
//
// 平台其实已经有专职翻译会话（`anchor_translate.go`，给角色卡写英文定妆提示词），
// 这里用**同一套做法**给这几镜的动作/走位补一份英文：只在"正文为空 + 材料是中文 + 还没翻过"
// 的镜上花这次调用（正常情况下一镜都不会有 —— 那是模型没写出来的那几镜）。
//
// ## 与卡片翻译同一条纪律
//
//   - **带汉字的结果一律不采用**（宁可少一份材料，也不把中文塞进正文 —— 那是拒收级问题）；
//   - 翻不出来的镜**逐条点名**（调用方如实播报），不静默当成功；
//   - 幂等：已经有 `action_en` / `positions_en` 的镜不再翻。

// stagingEN 一镜的英文动作/走位（翻译会话的输出）。
//
// 【为什么 `positions` 是**数组**而不是对象（2026-10-08 真机当场咬出来的）】
// 第一版要的是对象 `{"姜晚舟": "..."}`，而模型**把键也翻成了英文/拼音**（回的是
// `{"Jiang Wanzhou": "left third of the frame…"}`）⇒ 兜底正文按方案里的 id/名字查不到这些键
// ⇒ 英文走位**翻出来了却没用上**（真机实测：正文里一条走位都没有）。
// 改成数组后**顺序是有信息的**（`who` 也对不上时还能按输入顺序配），而且能把"键必须照抄"
// 写成硬要求 —— 猜错人的风险归零：对不上**不猜**，如实说这一镜没翻出来。
type stagingEN struct {
	Action    string          `json:"action"`
	Positions json.RawMessage `json:"positions"`
}

// stagingPos 一条英文走位（`who` 要求逐字照抄输入里的键）。
type stagingPos struct {
	Who   string `json:"who"`
	Where string `json:"where"`
}

// NeedsStagingTranslation 这一镜要不要让专职翻译会话翻英文动作/走位。
//
// 两条都满足才值得花这次 LLM 调用：
//  1. **正文是空的** —— 平台马上要替它拼兜底正文，而兜底正文只能放英文；
//  2. 动作或走位**是中文**，而且还没有英文版（`action_en` / `positions_en`）。
func NeedsStagingTranslation(sh project.Shot) bool {
	if strings.TrimSpace(sh.H3Prompt) != "" {
		return false // 正文在：模型自己把它写成英文了，不必翻
	}
	return needsStagingMaterial(sh)
}

// stagingTodo 待翻的镜（按镜号升序，判据与 NeedsStagingTranslation 同一套）。
func stagingTodo(plan *project.Plan) []project.Shot {
	if plan == nil {
		return nil
	}
	var out []project.Shot
	for _, sh := range plan.Shots {
		if NeedsStagingTranslation(sh) {
			out = append(out, sh)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TranslateShotStaging 给"正文为空、动作/走位是中文"的镜补英文版（专职翻译会话）。
//
// 返回 (翻了几镜, err)。`client` 为 nil（没配 LLM）或没有待翻的镜时直接返回 —— 不打断流水线。
// 翻不出来的镜（模型没给、或给回来还带汉字）**逐条点名**进 err，调用方据此如实播报。
func TranslateShotStaging(ctx context.Context, client *llm.Client, plan *project.Plan) (int, error) {
	if plan == nil || client == nil {
		return 0, nil
	}
	todo := stagingTodo(plan)
	if len(todo) == 0 {
		return 0, nil
	}
	out, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: stagingTranslateSys},
		{Role: "user", Content: stagingRequest(todo)},
	}, false)
	if err != nil {
		return 0, err
	}
	m := parseStagingMap(out)
	if len(m) == 0 {
		return 0, fmt.Errorf("翻译结果里没有可用的 JSON")
	}
	n, missing := applyTranslatedStaging(plan, m, todo)
	if len(missing) > 0 {
		return n, fmt.Errorf("%d 镜的动作/走位没翻出来：%s", len(missing), shotIDsText(missing))
	}
	return n, nil
}

// stagingTranslateSys 翻译会话的系统提示（给模型看的，可以用中文解释）。
//
// 三条硬要求与卡片翻译同源：只写英文 / 逐字对应不增删 / 输出严格 JSON。
const stagingTranslateSys = "你是影视分镜台的英文文案（熟悉电影机位与走位术语）。\n" +
	"你会收到若干**镜头**的中文「画面动作」与「人物走位」。\n" +
	"任务：把它们写成**英文**，供视频生成模型直接使用。\n" +
	"\n" +
	"硬要求：\n" +
	"1. **只写英文，一个汉字都不许有**（模型会把汉字画成画面上的乱码字形）。\n" +
	"2. **逐字对应**：写清画面里有什么、谁在画面哪个位置、朝哪、手里拿着什么；\n" +
	"   **不许新增动作、不许删减信息、不许写解释或情绪评价**。\n" +
	"3. 走位用电影界常用说法（left third of frame / foreground / mid-ground / facing right 之类），\n" +
	"   一人一条，照原文的位置描述写。\n" +
	"4. 动作写成一到两句连贯的现在时英文，只描述画面动作；**我给的动作是空的时候就留空**，别编。\n" +
	"5. 走位里的 `who` **必须逐字照抄我给你的那个键**（例如「姜晚舟」就写「姜晚舟」）——\n" +
	"   **不许把它翻成拼音或英文**，那会让平台认不出这是谁。`where` 才是英文。\n" +
	"\n" +
	"输出：严格的 JSON 对象，键是**镜号**（纯数字字符串），值是\n" +
	"{\"action\": \"...\", \"positions\": [{\"who\": \"<逐字照抄的键>\", \"where\": \"...\"}]}\n" +
	"（positions 是**数组**，顺序与我给的一致）。不要任何解释、不要 markdown 围栏。"

// stagingRequest 拼给模型的材料（纯函数，便于判据直接检查"材料有没有给全"）。
func stagingRequest(todo []project.Shot) string {
	var sb strings.Builder
	for _, sh := range todo {
		fmt.Fprintf(&sb, "### 镜 %d（%d 秒）\n", sh.ID, sh.Duration)
		if a := strings.TrimSpace(sh.Action); a != "" {
			fmt.Fprintf(&sb, "动作：%s\n", a)
		} else {
			sb.WriteString("动作：（分镜台没写动作，请只把走位翻出来，空着 action）\n")
		}
		if len(sh.Positions) > 0 {
			sb.WriteString("走位（`who` 请逐字照抄这里的键）：\n")
			for _, k := range positionKeys(sh) {
				fmt.Fprintf(&sb, "- %s：%s\n", k, strings.TrimSpace(sh.Positions[k]))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// parseStagingMap 解析翻译结果（键可以是 "12" / "镜 12" / "shot 12"；容忍围栏与前后废话）。
// 解析不出来返回 nil（调用方据此放弃，而不是写半截英文进方案）。
func parseStagingMap(raw string) map[int]stagingEN {
	body := extractJSONObject(raw)
	if body == "" {
		return nil
	}
	raw2 := map[string]stagingEN{}
	if err := json.Unmarshal([]byte(body), &raw2); err != nil {
		return nil
	}
	out := make(map[int]stagingEN, len(raw2))
	for k, v := range raw2 {
		id, ok := digitsOf(k)
		if !ok {
			continue
		}
		out[id] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyTranslatedStaging 把英文写回方案，返回（写回几镜, 没写成的镜号）。
//
// **带汉字的那些一律不采用**（与卡片翻译同一条纪律）：宁可这一镜少一份材料（由兜底正文如实播报），
// 也不把中文塞进正文 —— 那是入口拒收级问题。
func applyTranslatedStaging(plan *project.Plan, m map[int]stagingEN, todo []project.Shot) (int, []int) {
	if plan == nil {
		return 0, nil
	}
	n := 0
	var missing []int
	for _, want := range todo {
		got, ok := m[want.ID]
		if !ok {
			missing = append(missing, want.ID)
			continue
		}
		applied := false
		idx := shotIndexByID(plan, want.ID)
		if idx < 0 {
			missing = append(missing, want.ID)
			continue
		}
		sh := &plan.Shots[idx]
		// 判据与入口的汉字卫生门**同源**：值里可以出现**主体名**（「in 姜晚舟's arms」这种是合法的 ——
		// 门本身就把主体名扣掉），但**不许有别的汉字**（那会在画面上变成乱码字形，入口也会拒收）。
		// 【为什么不能"一个汉字都不许有"】第一版就是这么写的，真机当场把它咬成失败：
		// 模型回的英文走位里带了女主角的名字（`the white plush ball in 姜晚舟's arms`）⇒ 整镜判"没翻出来"。
		names := planSubjectNames(plan)
		if a := strings.TrimSpace(got.Action); a != "" && project.CJKOutsideDialogue(a, names) == 0 {
			sh.ActionEN = a
			applied = true
		}
		// 走位：模型的键**不可信**（真机实测它会顺手把中文名翻成拼音）⇒ 先做键归一（`who` 逐字、
		// 或对到名字/id），对不上的按**输入顺序**配（我发出去的顺序是确定的），
		// 数量对不上就整镜不算（**不猜人**）。
		if pn, ok := alignStagingPositions(positionKeys(want), got.Positions, names); ok && len(pn) > 0 {
			sh.PositionsEN = pn
			applied = true
		}
		if applied {
			n++
		} else {
			missing = append(missing, want.ID)
		}
	}
	sort.Ints(missing)
	return n, missing
}

// extractJSONObject 从模型输出里取第一段完整 JSON 对象（容忍 ``` 围栏与前后废话）。
func extractJSONObject(raw string) string {
	t := strings.TrimSpace(raw)
	t = strings.TrimPrefix(t, "```json")
	t = strings.TrimPrefix(t, "```")
	if i := strings.Index(t, "{"); i >= 0 {
		if j := strings.LastIndex(t, "}"); j > i {
			return t[i : j+1]
		}
	}
	return ""
}

// digitsOf 从 "12" / "镜 12" / "shot 12" 这类键里取出镜号。
func digitsOf(s string) (int, bool) {
	digits := strings.Builder{}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(digits.String())
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// shotIndexByID 镜号 → plan.Shots 下标（找不着返回 -1）。
func shotIndexByID(plan *project.Plan, id int) int {
	for i := range plan.Shots {
		if plan.Shots[i].ID == id {
			return i
		}
	}
	return -1
}

// shotIDsText 把镜号拼成 "#12、#15"（播报用）。
func shotIDsText(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, "#"+strconv.Itoa(id))
	}
	return strings.Join(parts, "、")
}

// needsStagingMaterial 这一镜的**材料**是中文、而且还没有英文版（与"正文空不空"无关）。
func needsStagingMaterial(sh project.Shot) bool {
	if strings.TrimSpace(sh.ActionEN) != "" || len(sh.PositionsEN) > 0 {
		return false
	}
	if project.CountCJK(sh.Action) > 0 {
		return true
	}
	for _, v := range sh.Positions {
		if project.CountCJK(v) > 0 {
			return true
		}
	}
	return false
}

// refillTargets 要"补翻 + 重拼"的镜（纯函数，便于判据直接验）。
//
// 来源是**这一轮读取时记下的账** `plan.FilledBodyShots`（平台自己拼过正文的镜）∩ 材料仍是中文的镜。
// 只看这两个集合：模型自己写的正文**一个字都不碰**。
func refillTargets(plan *project.Plan) []int {
	if plan == nil || len(plan.FilledBodyShots) == 0 {
		return nil
	}
	var out []int
	for _, id := range plan.FilledBodyShots {
		sh := shotPtrByID(plan, id)
		if sh == nil {
			continue
		}
		if needsStagingMaterial(*sh) {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// RefillPlatformBodiesWithStaging 给"平台自己拼过正文、但中文动作/走位没能进正文"的那几镜补一次英文，
// 然后**重拼**这几镜的正文。
//
// 【为什么渲染阶段还要做这一步（2026-10-08）】补写发生在**读取边界**（`LoadPlan → HealPlan`），
// 那一刻手上没有 LLM ⇒ 中文的走位/动作进不了正文，只能如实播报"这几镜少了材料"。
// 已经躺在盘上的方案（用户手上这一份就是）正好卡在这里：脚本阶段早已跑完，没人再给它翻。
// 渲染阶段有 LLM 客户端，于是在这里补齐：**先清空这几镜的正文 → 翻英文 → 重拼一次**。
//
// 为什么"清空再重拼"是安全的：兜底正文是**这一镜材料的确定性函数**，材料只会变多（多了英文那份），
// 翻不出来时重拼结果与原来逐字一致；万一某镜没拼回来（不该发生），把原来的正文原样还回去 ——
// **绝不许把方案弄坏**。
func RefillPlatformBodiesWithStaging(ctx context.Context, client *llm.Client, plan *project.Plan) (int, error) {
	if plan == nil || client == nil {
		return 0, nil
	}
	ids := refillTargets(plan)
	if len(ids) == 0 {
		return 0, nil
	}
	backup := map[int]string{}
	for _, id := range ids {
		if sh := shotPtrByID(plan, id); sh != nil {
			backup[id] = sh.H3Prompt
			sh.H3Prompt = ""
		}
	}
	// 翻译会话：无论它成没成，都要按"材料现在有多少"重拼一次（重拼是确定性的，失败也不比原来差）。
	_, trErr := TranslateShotStaging(ctx, client, plan)
	filled, _ := project.FillEmptyShotBodies(plan)
	restored := 0
	for _, id := range ids {
		sh := shotPtrByID(plan, id)
		if sh == nil {
			continue
		}
		if strings.TrimSpace(sh.H3Prompt) == "" {
			sh.H3Prompt = backup[id]
			restored++
		}
	}
	if restored > 0 {
		// 还原了就说出来（不该发生；静默还原会让"这镜为什么没有英文走位"无从追查）
		return filled, fmt.Errorf("%d 镜重拼失败，已还原原正文（%s）", restored, shotIDsText(ids))
	}
	if trErr != nil {
		return filled, trErr
	}
	return filled, nil
}

// shotPtrByID 镜号 → 方案里那个镜的指针（找不着返回 nil）。
func shotPtrByID(plan *project.Plan, id int) *project.Shot {
	if plan == nil {
		return nil
	}
	if i := shotIndexByID(plan, id); i >= 0 {
		return &plan.Shots[i]
	}
	return nil
}

// alignStagingPositions 把模型给的英文走位对回**方案里的键**（返回"键 → 英文走位"）。
//
// 对法（先准后宽，**对不上宁可不要）**：
//  1. `who` 与输入键**逐字**相同（去空白）→ 用它；
//  2. `who` 与输入键**忽略大小写/空格**相同 → 用它；
//  3. 都对不上时按**输入顺序**配（输入顺序由 `stagingRequest` 定：同一份材料每次都一样），
//     但只在**条数相等**时才这么配 —— 条数不等说明模型漏了或加了人，那时整镜不算。
//
// 为什么键必须回到方案里的键：兜底正文按角色卡的 **id / 名字**查走位（`posENOf`），
// 模型自己造的键（拼音/英文）查不到 ⇒ 翻了也白翻（真机就是这么白翻一次的）。
//
// 含汉字的值一律不要（与卡片翻译同一条纪律）；返回的第二个值表示"这一镜的走位是否给全"。
func alignStagingPositions(wantKeys []string, raw json.RawMessage, keepNames []string) (map[string]string, bool) {
	keys := make([]string, 0, len(wantKeys))
	for _, k := range wantKeys {
		if t := strings.TrimSpace(k); t != "" {
			keys = append(keys, t)
		}
	}
	if len(keys) == 0 {
		return nil, true // 这一镜本来就没有走位：不算"翻了"
	}
	got := parseStagingPositions(raw)
	if len(got) != len(keys) {
		return nil, false // 漏人或多人：不猜
	}
	out := map[string]string{}
	used := map[int]bool{}
	// ① / ② 先按 who 精确或宽松对上
	norm := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(s), ""))
	}
	for _, g := range got {
		w := strings.TrimSpace(g.Where)
		// 汉字判据与**入口那道门同源**：值里允许出现**主体名**（`in 姜晚舟's arms` 合法 ——
		// 门自己就把主体名扣掉），但不许有别的汉字（那会在画面上变成乱码字形）。
		if w == "" || project.CJKOutsideDialogue(w, keepNames) > 0 {
			return nil, false
		}
		hit := -1
		for i, k := range keys {
			if used[i] {
				continue
			}
			if strings.TrimSpace(g.Who) == k || norm(g.Who) == norm(k) {
				hit = i
				break
			}
		}
		if hit < 0 {
			// 留给第 ③ 步
			continue
		}
		used[hit] = true
		out[keys[hit]] = w
	}
	// ③ 剩下按输入顺序配（条数已相等，所以一一对应）
	if len(out) < len(keys) {
		unmatched := make([]stagingPos, 0, len(got))
		for _, g := range got {
			m := false
			for i, k := range keys {
				if used[i] && out[k] == strings.TrimSpace(g.Where) {
					m = true
					break
				}
			}
			if !m {
				unmatched = append(unmatched, g)
			}
		}
		for i, k := range keys {
			if used[i] {
				continue
			}
			if len(unmatched) == 0 {
				return nil, false
			}
			w := strings.TrimSpace(unmatched[0].Where)
			if w == "" || project.CJKOutsideDialogue(w, keepNames) > 0 {
				return nil, false
			}
			out[k] = w
			unmatched = unmatched[1:]
			used[i] = true
		}
	}
	for _, k := range keys {
		if strings.TrimSpace(out[k]) == "" {
			return nil, false // 有人没配上：整镜不算（不许静默少人）
		}
	}
	return out, true
}

// parseStagingPositions 解析 `positions`：**数组**（要的形态）或**对象**（旧形态/别的模型），
// 对象形态下顺序不可靠（Go 的 map 无序）⇒ 只信值，键留空交给顺序对齐。
func parseStagingPositions(raw json.RawMessage) []stagingPos {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return nil
	}
	if strings.HasPrefix(t, "[") {
		var arr []stagingPos
		if err := json.Unmarshal([]byte(t), &arr); err == nil {
			return arr
		}
		return nil
	}
	if strings.HasPrefix(t, "{") {
		var m map[string]string
		if err := json.Unmarshal([]byte(t), &m); err != nil {
			return nil
		}
		// 对象形态：键可能是模型自己译的 —— 仍然留着（`who` 那一层对不上还有顺序兜底），
		// 但**按值排序**让它与"输入顺序"无关地稳定下来（同一份输出每次解析一致）。
		out := make([]stagingPos, 0, len(m))
		for k, v := range m {
			out = append(out, stagingPos{Who: k, Where: v})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Where < out[j].Where })
		return out
	}
	return nil
}

// positionKeys 一镜走位表的键，**按固定顺序**（字典序）—— 请求与写回**同一条判据**：
// 顺序是"按输入顺序对齐"那一层兜底的依据，两边各排一次就会走散。
func positionKeys(sh project.Shot) []string {
	keys := make([]string, 0, len(sh.Positions))
	for k := range sh.Positions {
		if t := strings.TrimSpace(k); t != "" {
			keys = append(keys, t)
		}
	}
	sort.Strings(keys)
	return keys
}

// planSubjectNames 方案的**主体名**集合（角色 + 场景 + 道具）—— 与契约门 `ValidatePlan`
// 里用来扣减汉字的那一份**同源**（官方要求点名主体，所以主体名不算"可避免的汉字"）。
// 翻译结果的验收判据必须用它，否则会比门更严：真机实测模型回
// `the white plush ball in 姜晚舟's arms` —— 带名字的英文是**合法**的，被"一个汉字都不许有"误杀过。
func planSubjectNames(plan *project.Plan) []string {
	if plan == nil {
		return nil
	}
	out := make([]string, 0, len(plan.Characters)+len(plan.Scenes)+len(plan.Props))
	for _, c := range plan.Characters {
		if n := strings.TrimSpace(c.Name); n != "" {
			out = append(out, n)
		}
	}
	for _, s := range plan.Scenes {
		if n := strings.TrimSpace(s.Name); n != "" {
			out = append(out, n)
		}
	}
	for _, pr := range plan.Props {
		if n := strings.TrimSpace(pr.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}
