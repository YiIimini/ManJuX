// Package director 负责「LLM 直出分镜方案」:输入源文本,输出结构化
// 角色/场景/分镜 JSON,其中每镜包含完整的 H3 提示词(六段式/三段式)。
//
// 设计原则(对齐 MiniMax-H3 官方 Prompt Writing Guide 与知识库实践):
//   - 提示词 100% 由大模型直出,执行层不做拼接改写,只做提交与质检;
//   - 英文结构化描述 + 中文台词原样保留([Chinese] 标注);
//   - H3 直出音视频(对白/旁白/环境音/配乐 32kHz 立体声内嵌),无 TTS。
package director

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// SystemPrompt 直出系统提示(知识库 H3 官方规范全量内嵌)。
// SystemPrompt 拆段定义(拼接还原,内容与单轮路径完全一致):
const sysHeader = `You are an elite AI short-drama director and H3 prompt engineer.
You turn a Chinese story/idea/outline into a complete shot plan (Storyboard) with
production-ready MiniMax-H3 video prompts. All structural fields must be in the
required language as specified; NEVER translate dialogue.`

const sysOutputFull = `## OUTPUT
Return STRICT JSON only (no markdown fences, no commentary):
{
  "title": "剧名(中文,≤12字)",
  "logline": "一句话故事(中文,≤40字)",
  "style": "global visual style in ENGLISH, 1 main style + 1-2 texture traits, e.g. 'Chinese xianxia cinematic animation, soft volumetric lighting, rich color grading'",
  "characters": [
    {"id":"char_xiaofan","name":"小凡","description":"中文外貌/服装/气质,固定不变的关键特征","image_prompt":"ENGLISH half-body portrait prompt for a costume reference photo: gender, age, face, hair, costume, accessories, plain background, upper-body framing, front-facing","voice":"ENGLISH voice description, e.g. 'calm young male voice'"}
  ],
  "scenes": [
    {"id":"scene_inn","name":"悦来客栈","description":"中文场景描述","image_prompt":"ENGLISH establishing-shot image prompt for this location"}
  ],
  "shots": [
    {"id":1,"scene":"scene_inn","characters":["char_xiaofan"],"shot_size":"中景","camera":"slow push-in, small amplitude","action":"中文一句话画面动作","dialogue":[{"speaker":"小凡","text":"你怎么也来这里了?"}],"narration":"","duration":6,"mode":"ref2va","h3_prompt":"FULL H3 prompt text as defined below"}
  ]
}`

const sysShotRules = `## SHOT PLAN RULES
- 3-12 shots total. Each shot 4-10 seconds (integer). Hook in shot 1, payoff at the end.
- shot_size 景别用中文:远景/全景/中景/近景/特写。Emotional beats and dialogue MUST use 近景/特写 (H3 face tokens are scarce; closer framing beats wider framing).
- One camera move per shot, chosen from official motion vocabulary ONLY: Push In / Pull Out / Pan / Truck / Tilt / Pedestal / Arc / Tracking / Static / POV / Roll / Shake, plus amplitude (small/large amplitude) and speed (slow/fast) in natural English. NEVER use jargon like dolly, crane, whip pan.
- mode: "ref2va" when character/scene reference images will be provided (any shot with on-screen characters); "t2va" for pure text-to-video shots without references.
- dialogue speaker must match a character name, or use speaker "旁白" for narration. Keep spoken lines short (≤20 chars), punchy, ending with 。?! naturally. At most 2 speakers per shot.
- narration (旁白) is an off-screen voiceover; characters' lips stay closed. Use sparingly.`

const sysH3Format = `## H3 PROMPT FORMAT (critical - follow EXACTLY)
mode=ref2va => SIX sections in this exact order, separated by one blank line:

subject_definitions:
<Subject 1> is [full character description in English] in <Picture 1>.
<Subject 2> is [full scene description in English] in <Picture 2>.
(Add <Subject 3> for a key prop in <Picture 3> if essential. Scenes and props are also Subjects; Picture tags appear ONLY here as the source of a Subject, never standalone.)

summary:
[reference generation]
One English paragraph: what the video contains and how the reference materials are used.

retention_analysis:
- <Picture 1> : fully_preserved - character identity (face, hairstyle, costume) must stay identical.
- <Picture 2> : weak_reference - scene layout and mood referenced, camera free.
(Use official markers: fully_preserved / partially_preserved / attribute_transfer / weak_reference)

detailed_description:
[Style sentence(s) first: use the global style]. Then for every shot:
[Shot 1] with NO timestamp for the first shot; subsequent shots "[Shot 2] At 00:06.000" with strictly increasing timestamps.
Each shot paragraph (350-500 English words TOTAL across all shots): shot size and composition -> subject positions -> restate key appearance traits on first appearance -> ONE main action per shot (split complex actions into sub-moves) -> visible emotional change (show, don't label) -> camera motion with amplitude and speed -> sound events.
Dialogue lines: <Subject 1> (S1) says in a [voice description]: <d>[Chinese]台词原文</d>. Assign (S1)(S2) by order of first utterance and NEVER renumber within the video. Keep Chinese dialogue VERBATIM inside <d> tags with original punctuation; do not rewrite or translate it.
Off-screen narration: The narrator (S1) says in an off-screen voiceover: <d>[Chinese]旁白原文</d> while the on-screen characters' lips remain completely closed.
End the whole section with an exclusions sentence in prose: e.g. "No on-screen text, no subtitles, no watermarks, no logos."
MANDATORY brightness guard as the final sentence: "Subjects must remain clearly visible and adequately lit throughout the video; avoid rendering any frame nearly black or overly dark."

overall_soundscape:
1-4 English sentences of diegetic ambient sound. Do NOT repeat dialogue here.

non_diegetic_music:
Instruments + tempo + rhythm + dynamics in English, e.g. "Guzheng and low strings, slow tempo, sparse rhythm, gradually building dynamics." Or "N/A" if silence fits.

mode=t2va => THREE sections in this exact order, separated by one blank line:

integrated_multimodal_description:
[Style sentence(s) first]. Then [Shot 1] / [Shot 2] At MM:SS.mmm ... Same rules as detailed_description above (word count 350-500 total, dialogue tags, camera vocabulary, exclusions sentence, brightness guard at the end).

overall_soundscape:
(1-4 sentences)

non_diegetic_music:
(instruments, tempo, rhythm, dynamics, or N/A)`

const sysHard = `## HARD CONSTRAINTS
- English structural prose, Chinese dialogue verbatim in <d>[Chinese]...</d>.
- h3_prompt total length 2500-6000 characters; detailed_description body 350-500 English words.
- No negative-prompt syntax, no CFG hints, no technical parameters - exclusions go as prose at the end.
- Consistency: same character keeps identical appearance words every shot; same scene name reused.
- Sound: dialogue only via <d> tags; ambience in overall_soundscape; music in non_diegetic_music; never duplicate.
- Screen-visible text must be quoted exactly and kept in original language.`

// SystemPrompt 单轮直出(完整 schema + 全部规则)。
const SystemPrompt = sysHeader + `

` + sysOutputFull + `

` + sysShotRules + `

` + sysH3Format + `

` + sysHard

// UserPromptFmt 用户提示模板。
const UserPromptFmt = `Source text (Chinese):
"""
%s
"""

Additional requirements:
- Target: short video, total duration about %d seconds (%d shots).
%s
Produce the STRICT JSON plan now.`

// Options 直出选项。
type Options struct {
	MaxShots     int    // 期望镜头数(3-12)
	TotalSeconds int    // 期望总时长
	ExtraHint    string // 用户附加要求(风格/题材)
	Chibi        bool   // 生成 Q 版形象(内心活动用;分镜提示词指引 Q 版 cameo)
	// OnBatch 分批生成进度(批 i/n,负责镜号区间);仅分批路径触发。
	OnBatch func(batch, total, fromShot, toShot int)
}

// segment 分批切分计划(轮1 产物,不落盘)。
type segment struct {
	From  int    `json:"from"`
	To    int    `json:"to"`
	Brief string `json:"brief"`
}

type planHead struct {
	Title      string              `json:"title"`
	Logline    string              `json:"logline"`
	Style      string              `json:"style"`
	Characters []project.Character `json:"characters"`
	Scenes     []project.Scene     `json:"scenes"`
	Segments   []segment           `json:"segments"`
}

// batchShotOut 分镜轮产物。
type batchShotOut struct {
	Shots []project.Shot `json:"shots"`
}

const batchThresholdShots = 6    // 超过 → 分批
const batchThresholdRunes = 1500 // 源文本超长(字)→ 分批
const batchMaxShots = 5          // 每批最多镜数

// Generate 调用 LLM 直出分镜方案并做结构校验与修复。
//
// 双路径:小任务(≤6 镜且文本 ≤1500 字)单轮直出;大任务走「规划轮 + 分批
// 出镜轮」——单次生成量随批拆分,推理模型单轮压回 1-3 分钟,远离超时与
// max_tokens 截断(2026-09-08 长文超时治理)。
func Generate(ctx context.Context, client *llm.Client, sourceText string, opt Options) (*project.Plan, error) {
	if opt.MaxShots <= 0 {
		opt.MaxShots = 6
	}
	if opt.TotalSeconds <= 0 {
		opt.TotalSeconds = opt.MaxShots * 7
	}
	if opt.MaxShots > batchThresholdShots || len([]rune(sourceText)) > batchThresholdRunes {
		return generateBatched(ctx, client, sourceText, opt)
	}
	return generateSingle(ctx, client, sourceText, opt)
}

// styleHint 组装附加要求(单轮/分批共用)。
func styleHint(opt Options) string {
	hint := ""
	if strings.TrimSpace(opt.ExtraHint) != "" {
		hint = "- User style note: " + strings.TrimSpace(opt.ExtraHint)
	}
	// Q 版形象开启时,分镜中角色内心活动可用 Q 版形象插入画面(chibi cameo)
	if opt.Chibi {
		hint += "\n- Each main character has a chibi (Q-version) avatar generated alongside its portrait. " +
			"When a shot depicts a character's INNER monologue / mental reaction, you may describe a chibi cameo " +
			"of that character popping up in frame (cute miniature version, expressive) to visualize the inner activity."
	}
	return hint
}

func generateSingle(ctx context.Context, client *llm.Client, sourceText string, opt Options) (*project.Plan, error) {
	user := fmt.Sprintf(UserPromptFmt, sourceText, opt.TotalSeconds, opt.MaxShots, styleHint(opt))
	plan, err := chatPlan(ctx, client, SystemPrompt, user)
	if err != nil {
		return nil, err
	}
	if err := validate(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// chatPlan 单次方案请求 + 一次 JSON 修复重试。
func chatPlan(ctx context.Context, client *llm.Client, sys, user string) (*project.Plan, error) {
	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, true)
	if err != nil {
		return nil, err
	}
	plan, err := parsePlan(raw)
	if err != nil {
		// 二次修复:把原始输出连同错误交回 LLM 修正一次
		fix, ferr := client.Chat(ctx, []llm.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
			{Role: "assistant", Content: raw},
			{Role: "user", Content: "Your previous output is invalid JSON or violates the schema. Error: " +
				err.Error() + "\nReturn the corrected STRICT JSON only."},
		}, true)
		if ferr != nil {
			return nil, fmt.Errorf("方案解析失败: %w", err)
		}
		plan, err = parsePlan(fix)
		if err != nil {
			return nil, fmt.Errorf("方案二次解析仍失败: %w", err)
		}
	}
	return plan, nil
}

// parsePlan 从 LLM 输出提取并解析 JSON(容错:剥围栏/前后杂文)。
func parsePlan(raw string) (*project.Plan, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	// 截取首个 { 到最后一个 }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("输出中未找到 JSON 对象")
	}
	s = s[start : end+1]
	var plan project.Plan
	if err := json.Unmarshal([]byte(s), &plan); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %v", err)
	}
	return &plan, nil
}

// validate 做最小硬校验并规整(id 连续、引用存在、时长钳制)。
func validate(p *project.Plan) error {
	if p.Title == "" {
		return fmt.Errorf("缺少剧名")
	}
	if len(p.Shots) == 0 {
		return fmt.Errorf("分镜为空")
	}
	if len(p.Shots) > 20 {
		p.Shots = p.Shots[:20]
	}
	sceneIDs := map[string]bool{}
	for _, sc := range p.Scenes {
		if sc.ID == "" {
			sc.ID = "scene_" + strconv.Itoa(len(sceneIDs)+1)
		}
		sceneIDs[sc.ID] = true
	}
	charIDs := map[string]bool{}
	for _, c := range p.Characters {
		if c.ID == "" {
			c.ID = "char_" + c.Name
		}
		charIDs[c.ID] = true
	}
	var validShots []project.Shot
	for i := range p.Shots {
		sh := p.Shots[i]
		if sh.Duration < 4 {
			sh.Duration = 4
		}
		if sh.Duration > 10 {
			sh.Duration = 10
		}
		if sh.Mode == "" {
			sh.Mode = "t2va"
		}
		if len(strings.TrimSpace(sh.H3Prompt)) < 400 {
			return fmt.Errorf("第 %d 镜 H3 提示词过短(<400 字符),疑似截断", sh.ID)
		}
		sh.ID = i + 1
		validShots = append(validShots, sh)
	}
	p.Shots = validShots
	return nil
}

// ShotSubtitle 生成的镜头字幕文本(用于 SRT)。
func ShotSubtitle(sh project.Shot) string {
	return strings.Join(ShotSubtitleParts(sh), "\n")
}

// ShotSubtitleParts 镜头字幕逐条拆分(用于 ASS 分时显示):
// 旁白一段、每段对白一段;单段超长按句读再切,保证单条两行内可读。
func ShotSubtitleParts(sh project.Shot) []string {
	var parts []string
	push := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if len([]rune(s)) > 42 {
			parts = append(parts, splitLong(s)...)
			return
		}
		parts = append(parts, s)
	}
	if strings.TrimSpace(sh.Narration) != "" {
		push(sh.Narration)
	}
	for _, d := range sh.Dialogue {
		if strings.TrimSpace(d.Text) == "" {
			continue
		}
		if strings.TrimSpace(d.Speaker) != "" && d.Speaker != "旁白" {
			push(d.Speaker + ":" + d.Text)
		} else {
			push(d.Text)
		}
	}
	return parts
}

// splitLong 超长段落按句读切到 ~40 字的条。
func splitLong(s string) []string {
	runes := []rune(s)
	var out []string
	var cur []rune
	for _, r := range runes {
		cur = append(cur, r)
		switch r {
		case '。', '!', '?', ';', '~', '…':
			if len(cur) >= 18 {
				out = append(out, string(cur))
				cur = nil
			}
		}
		if len(cur) >= 40 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// SanitizeForCache 清洗缓存键(保中文/字母/数字/下划线/连字符)。
var cacheCleanRe = regexp.MustCompile(`[^\p{L}\p{N}_-]+`)

// SanitizeForCache 清洗任意字符串为安全缓存名片段。
func SanitizeForCache(s string) string {
	return strings.Trim(cacheCleanRe.ReplaceAllString(s, "_"), "_")
}

// ---------- 分批生成:规划轮 + 分批出镜轮 ----------

// planSystemPrompt 规划轮:通读全文,只产出轻量头部(剧名/角色/场景/切分计划)。
const planSystemPrompt = sysHeader + `

## OUTPUT (BATCH PLAN)
Return STRICT JSON only (no markdown fences, no commentary):
{
  "title": "剧名(中文,≤12字)",
  "logline": "一句话故事(中文,≤40字)",
  "style": "global visual style in ENGLISH, 1 main style + 1-2 texture traits",
  "characters": [
    {"id":"char_xxx","name":"中文名","description":"中文外貌/服装/气质,固定不变的关键特征","image_prompt":"ENGLISH half-body portrait prompt for a costume reference photo: gender, age, face, hair, costume, accessories, plain background, upper-body framing, front-facing","voice":"ENGLISH voice description"}
  ],
  "scenes": [
    {"id":"scene_xxx","name":"中文名","description":"中文场景描述","image_prompt":"ENGLISH establishing-shot image prompt for this location"}
  ],
  "segments": [
    {"from":1,"to":3,"brief":"本段剧情要点(中文,≤160字):发生什么、关键台词原文、情绪转折、结局钩子"}
  ]
}

## SEGMENT RULES
- segments 的 from/to 是分镜镜号区间:从 1 开始连续覆盖到总镜数,不重叠不留空。
- 每段 3-6 镜;段与段之间是清晰的故事节拍(开场钩子/升级/转折/高潮/收尾)。
- brief 必须自足:仅凭 brief(不看全文)就能写好本段每一镜。关键台词必须原文引用。
- characters/scenes 覆盖全片所有出场者;角色外貌描述具体可画,全片复用。`

// batchSystemPrompt 分镜轮:按已定头部与本段 brief,只产出指定区间的 shots。
const batchSystemPrompt = sysHeader + `

## OUTPUT (THIS BATCH)
Return STRICT JSON only (no markdown fences, no commentary):
{"shots":[
  {"id":N,"scene":"scene_xxx","characters":["char_xxx"],"shot_size":"中景","camera":"slow push-in, small amplitude","action":"中文一句话画面动作","dialogue":[{"speaker":"中文名","text":"台词"}],"narration":"","duration":6,"mode":"ref2va","h3_prompt":"FULL H3 prompt text"}
]}

` + sysShotRules + `

` + sysH3Format + `

` + sysHard + `

## BATCH SCOPE
- Produce ONLY the shots whose ids are inside the given range; do NOT invent shots outside it.
- ids must be integers inside the range, sequential, no gaps.
- Story continuity: respect the PREVIOUS SHOTS summary if provided (do not repeat events; keep character states consistent).`

// generateBatched 分批路径:轮1 规划(轻量)→ 轮 2..k 逐批出镜(每批 ≤5 镜,
// 单轮生成量小,推理模型压回 1-3 分钟)。
func generateBatched(ctx context.Context, client *llm.Client, sourceText string, opt Options) (*project.Plan, error) {
	hint := styleHint(opt)

	// ---- 轮 1:规划 ----
	planUser := fmt.Sprintf(`Source text (Chinese):
"""
%s
"""

Additional requirements:
- Target: short video, total %d shots, total duration about %d seconds.
- Split the story into segments of 3-%d shots each (segments must continuously cover shot ids 1..%d).
%s
Produce the STRICT BATCH PLAN JSON now.`, sourceText, opt.MaxShots, opt.TotalSeconds, batchMaxShots, opt.MaxShots, hint)

	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: planSystemPrompt},
		{Role: "user", Content: planUser},
	}, true)
	if err != nil {
		return nil, fmt.Errorf("规划轮失败: %w", err)
	}
	var head planHead
	if err := json.Unmarshal([]byte(trimJSONBlock(raw)), &head); err != nil || head.Title == "" || len(head.Segments) == 0 {
		return nil, fmt.Errorf("规划轮输出解析失败(缺少 title/segments)")
	}
	normalizeSegments(&head, opt.MaxShots)

	total := len(head.Segments)
	plan := &project.Plan{
		Title: head.Title, Logline: head.Logline, Style: head.Style,
		Characters: head.Characters, Scenes: head.Scenes,
	}

	// ---- 轮 2..k:逐批出镜 ----
	cards, _ := json.Marshal(map[string]any{"characters": head.Characters, "scenes": head.Scenes})
	for bi, seg := range head.Segments {
		if opt.OnBatch != nil {
			opt.OnBatch(bi+1, total, seg.From, seg.To)
		}
		segSeconds := opt.TotalSeconds * (seg.To - seg.From + 1) / opt.MaxShots
		prevSummary := previousShotsSummary(plan.Shots)
		user := fmt.Sprintf(`Established plan head (STRICT, reuse ids verbatim):
%s

Global style: %s
%s
This batch: shots %d-%d (%d shots), total duration about %d seconds.
Segment brief (the ONLY story source for this batch):
"""
%s
"""
%s
Produce ONLY this batch's shots as STRICT JSON now.`, cards, head.Style, hint, seg.From, seg.To,
			seg.To-seg.From+1, segSeconds, seg.Brief, prevSummary)

		raw, err := client.Chat(ctx, []llm.Message{
			{Role: "system", Content: batchSystemPrompt},
			{Role: "user", Content: user},
		}, true)
		if err != nil {
			return nil, fmt.Errorf("分镜批次 %d/%d(镜 %d-%d)失败: %w", bi+1, total, seg.From, seg.To, err)
		}
		shots, err := parseShotsBatch(raw, seg.From, seg.To)
		if err != nil {
			// 单批重试一次
			raw2, err2 := client.Chat(ctx, []llm.Message{
				{Role: "system", Content: batchSystemPrompt},
				{Role: "user", Content: user},
				{Role: "assistant", Content: raw},
				{Role: "user", Content: "Invalid JSON or shots outside range " +
					fmt.Sprintf("%d-%d", seg.From, seg.To) + ". Error: " + err.Error() + "\nReturn the corrected STRICT JSON only."},
			}, true)
			if err2 != nil {
				return nil, fmt.Errorf("分镜批次 %d/%d 解析失败: %w", bi+1, total, err)
			}
			shots, err = parseShotsBatch(raw2, seg.From, seg.To)
			if err != nil {
				return nil, fmt.Errorf("分镜批次 %d/%d 二次解析仍失败: %w", bi+1, total, err)
			}
		}
		plan.Shots = append(plan.Shots, shots...)
	}
	if err := validate(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// normalizeSegments 规范切分:钳制批大小/镜号连续覆盖 1..max。
func normalizeSegments(h *planHead, max int) {
	var out []segment
	want := 1
	for _, s := range h.Segments {
		from, to := s.From, s.To
		if from < want {
			from = want
		}
		if to > max {
			to = max
		}
		if to-from+1 > batchMaxShots {
			to = from + batchMaxShots - 1
		}
		if from > to || from > max {
			continue
		}
		if from != want {
			// 镜号有洞:把洞并进本段
			from = want
			if to-from+1 > batchMaxShots+2 {
				to = from + batchMaxShots + 1
			}
		}
		out = append(out, segment{From: from, To: to, Brief: s.Brief})
		want = to + 1
		if want > max {
			break
		}
	}
	// 补尾洞
	for want <= max {
		to := want + batchMaxShots - 1
		if to > max {
			to = max
		}
		out = append(out, segment{From: want, To: to, Brief: "承接前文推进剧情至收尾(规划轮未覆盖,请依全片风格补全)"})
		want = to + 1
	}
	h.Segments = out
}

// parseShotsBatch 解析一批 shots 并校验区间。
func parseShotsBatch(raw string, from, to int) ([]project.Shot, error) {
	s := trimJSONBlock(raw)
	var out batchShotOut
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %v", err)
	}
	if len(out.Shots) == 0 {
		return nil, fmt.Errorf("shots 为空")
	}
	var valid []project.Shot
	for _, sh := range out.Shots {
		if sh.ID >= from && sh.ID <= to && strings.TrimSpace(sh.H3Prompt) != "" {
			valid = append(valid, sh)
		}
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("批内无有效镜头(镜号需在 %d-%d 且含 h3_prompt)", from, to)
	}
	return valid, nil
}

// previousShotsSummary 前批衔接摘要(每镜一行)。
func previousShotsSummary(shots []project.Shot) string {
	if len(shots) == 0 {
		return "Previous shots summary: (this is the first batch)"
	}
	var b strings.Builder
	b.WriteString("Previous shots summary (for continuity, do NOT repeat these events):\n")
	for _, s := range shots {
		line := fmt.Sprintf("- Shot %d [%s] %s", s.ID, s.ShotSize, s.Action)
		if len(s.Dialogue) > 0 {
			line += fmt.Sprintf(" | last line: %s:%s", s.Dialogue[len(s.Dialogue)-1].Speaker, s.Dialogue[len(s.Dialogue)-1].Text)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// trimJSONBlock 剥围栏并截取最外层对象。
func trimJSONBlock(raw string) string {
	s := strings.TrimSpace(strings.Trim(raw, "`"))
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	return s
}
