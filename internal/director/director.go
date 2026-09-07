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
const SystemPrompt = `You are an elite AI short-drama director and H3 prompt engineer.
You turn a Chinese story/idea/outline into a complete shot plan (Storyboard) with
production-ready MiniMax-H3 video prompts. All structural fields must be in the
required language as specified; NEVER translate dialogue.

## OUTPUT
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
}

## SHOT PLAN RULES
- 3-12 shots total. Each shot 4-10 seconds (integer). Hook in shot 1, payoff at the end.
- shot_size 景别用中文:远景/全景/中景/近景/特写。Emotional beats and dialogue MUST use 近景/特写 (H3 face tokens are scarce; closer framing beats wider framing).
- One camera move per shot, chosen from official motion vocabulary ONLY: Push In / Pull Out / Pan / Truck / Tilt / Pedestal / Arc / Tracking / Static / POV / Roll / Shake, plus amplitude (small/large amplitude) and speed (slow/fast) in natural English. NEVER use jargon like dolly, crane, whip pan.
- mode: "ref2va" when character/scene reference images will be provided (any shot with on-screen characters); "t2va" for pure text-to-video shots without references.
- dialogue speaker must match a character name, or use speaker "旁白" for narration. Keep spoken lines short (≤20 chars), punchy, ending with 。?! naturally. At most 2 speakers per shot.
- narration (旁白) is an off-screen voiceover; characters' lips stay closed. Use sparingly.

## H3 PROMPT FORMAT (critical - follow EXACTLY)
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
(instruments, tempo, rhythm, dynamics, or N/A)

## HARD CONSTRAINTS
- English structural prose, Chinese dialogue verbatim in <d>[Chinese]...</d>.
- h3_prompt total length 2500-6000 characters; detailed_description body 350-500 English words.
- No negative-prompt syntax, no CFG hints, no technical parameters - exclusions go as prose at the end.
- Consistency: same character keeps identical appearance words every shot; same scene name reused.
- Sound: dialogue only via <d> tags; ambience in overall_soundscape; music in non_diegetic_music; never duplicate.
- Screen-visible text must be quoted exactly and kept in original language.`

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
}

// Generate 调用 LLM 直出分镜方案并做结构校验与修复。
func Generate(ctx context.Context, client *llm.Client, sourceText string, opt Options) (*project.Plan, error) {
	if opt.MaxShots <= 0 {
		opt.MaxShots = 6
	}
	if opt.TotalSeconds <= 0 {
		opt.TotalSeconds = opt.MaxShots * 7
	}
	hint := ""
	if strings.TrimSpace(opt.ExtraHint) != "" {
		hint = "- User style note: " + strings.TrimSpace(opt.ExtraHint)
	}
	user := fmt.Sprintf(UserPromptFmt, sourceText, opt.TotalSeconds, opt.MaxShots, hint)

	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: SystemPrompt},
		{Role: "user", Content: user},
	}, true)
	if err != nil {
		return nil, err
	}
	plan, err := parsePlan(raw)
	if err != nil {
		// 二次修复:把原始输出连同错误交回 LLM 修正一次
		fix, ferr := client.Chat(ctx, []llm.Message{
			{Role: "system", Content: SystemPrompt},
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
	if err := validate(plan); err != nil {
		return nil, err
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
