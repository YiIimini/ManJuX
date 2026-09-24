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
  "style": "global visual style in ENGLISH, 1 main style + 1-2 texture traits. IMPORTANT: if the source text contains 【全局风格】, COPY that style EXACTLY — do NOT invent a different one. The style must be IDENTICAL across all episodes of the same series. It must be an ANIMATION style (donghua / anime / cel-shaded illustration), never photorealistic live-action.",
  "characters": [
    {"id":"char_xiaofan","name":"小凡","description":"中文外貌/服装/气质,固定不变的关键特征(含至少一个独特记号)","image_prompt":"ENGLISH portrait prompt, IDENTITY FIRST per CAST DISTINCTNESS: [distinctive physical identity: age, face shape, build, hair, eyes, skin, signature marker] then [costume] then 'in [COPY the global style field above EXACTLY — same art style as the series]' then 'plain background, upper-body framing, front-facing'. NEVER open with style words; NEVER use a different art style from the global style.","voice":"ENGLISH voice description, e.g. 'calm young male voice'"}
  ],
  "scenes": [
    {"id":"scene_inn","name":"悦来客栈","description":"中文场景描述","image_prompt":"ENGLISH establishing-shot image prompt for this location"}
  ],
  "shots": [
    {"id":1,"scene":"scene_inn","characters":["char_xiaofan"],"shot_size":"中景","camera":"slow push-in, small amplitude","action":"中文一句话画面动作","dialogue":[{"speaker":"小凡","text":"你怎么也来这里了?"}],"narration":"","duration":6,"mode":"ref2va","h3_prompt":"FULL H3 prompt text as defined below"}
  ]
}`

const sysShotRules = `## SHOT PLAN RULES
- Shot count is driven by the CONTENT, with NO upper limit: cover this episode's story completely and clearly. If the source has many beats, use more shots — a dense chapter may legitimately need 15-30 shots, and that is fine. Always prefer MORE, smaller shots over fewer overloaded ones; never compress several actions into one shot to save shot count. Each shot 4-8 seconds (integer): local H3 rendering costs grow faster than linear with frame count (a 10s shot is ~2.8x the load of a 6s one on the same card), so split a long beat into two shots instead of stretching one. Hook in shot 1, payoff at the end.
- CONTINUITY (critical, 2026-09-10): consecutive shots must read as ONE continuous flow. Each shot's action must BEGIN where the previous shot ENDED — same location (unless a deliberate scene change), carried-over posture/emotion/props, coherent time-of-day and lighting. NEVER skip a narrative beat the audience needs; if a beat needs two camera moments, split it into two shots with one clear action each. On scene changes, bridge with narration or a matching visual anchor, never jump-cut blind.
- shot_size 景别用中文:远景/全景/中景/近景/特写。Emotional beats and dialogue MUST use 近景/特写 (H3 face tokens are scarce; closer framing beats wider framing).
- One camera move per shot, chosen from official motion vocabulary ONLY: Push In / Pull Out / Pan / Truck / Tilt / Pedestal / Arc / Tracking / Static / POV / Roll / Shake, plus amplitude (small/large amplitude) and speed (slow/fast) in natural English. NEVER use jargon like dolly, crane, whip pan.
- mode: "ref2va" when character/scene reference images will be provided (any shot with on-screen characters); "t2va" for pure text-to-video shots without references.
- dialogue speaker must match a character name, or use speaker "旁白" for narration. Keep spoken lines short (≤20 chars), punchy, ending with 。?! naturally. At most 2 speakers per shot.
- DIALOGUE MUST FIT THE SHOT (critical): Mandarin delivery runs about 4.5 characters per second, and H3 needs roughly one extra second of headroom to start and settle. The shot's duration MUST be at least ceil(total_dialogue_chars / 4.5) + 1 (rounded up to a whole second, min 4, max 8). A 20-character line therefore needs 6 seconds — never park a 20-character line in a 4-second shot: the speech gets cut off and the finished video loses the end of the line. If a line cannot fit 8 seconds, split the beat into two shots, each carrying part of the line.
- narration (旁白) is an off-screen voiceover; characters' lips stay closed. Use sparingly.`

const sysH3Format = `## H3 PROMPT FORMAT (critical - follow EXACTLY)
mode=ref2va => SIX sections in this exact order, separated by one blank line:

subject_definitions:
REFERENCE ORDER (strict, but a character may occupy ONE OR TWO pictures, so the scene index is NOT fixed): pictures are attached in this order — for each character listed in the shot's "characters" array, in array order, FIRST that character's front portrait (the sharp face reference), and THEN, if that character has a multi-view turnaround, ONE extra turnaround picture of the SAME character (a horizontal strip: its LEFTMOST cell is that same front portrait and the following cells show the same person turned to other angles). After all character pictures comes exactly ONE scene picture, always LAST. Props never get a <Picture> — describe props in text only.
Locate every subject by PICTURE CONTENT, not by index: a portrait and its turnaround are the SAME subject and the same individual — never treat a turnaround as a second character. NEVER reason "N characters, therefore the scene is <Picture N+1>": with N characters the scene is the LAST picture, which is <Picture N+1> only when no character has a turnaround.
<Subject 1> is [full description of the FIRST character listed in "characters"]; its front portrait is the first attached picture, and the character is the SAME person in every other picture that shows this individual.
<Subject 2> is [full description of the SECOND listed character, or the scene description when the shot has only one character] — identify it by its picture content, never by counting indexes.
The scene is the LAST attached picture.
Every <Subject> must map to a picture that exists in the order above; never invent <Picture> tags beyond that count. Picture tags appear ONLY here as a Subject's source, never standalone. A turnaround picture is never a separate subject: the character and its turnaround share one description (the character's) — the turnaround only supplies the other viewing angles, and the FRONT PORTRAIT is the authority on the face.

summary:
[reference generation]
One English paragraph: what the video contains and how the reference materials are used.

retention_analysis:
- One line per reference picture, numbered EXACTLY as in subject_definitions. A character may contribute TWO pictures (front portrait + turnaround): give BOTH that character's fully_preserved line and never describe the turnaround as a different or additional character. For the face, the FRONT PORTRAIT is the authority; the turnaround only supplies the other viewing angles.
- The scene index is not fixed: count the pictures actually attached (a character with a turnaround contributes two), and confirm by CONTENT rather than assuming an index.
- Every character picture: fully_preserved - character identity (face shape, hairstyle, costume) must stay identical to the reference image throughout every frame. FACE LOCK on that character's own <Picture>: the facial features, bone structure, and skin details must be copied from it exactly — do NOT blend, average, or substitute another face.
- The scene picture: weak_reference - scene layout and mood referenced, camera free.
(Use official markers: fully_preserved / partially_preserved / attribute_transfer / weak_reference)
(For multi-character shots: EVERY character gets its own fully_preserved line with FACE LOCK. Characters WITHOUT a reference picture must NOT borrow facial features from any reference image — generate them from the text description only.)
The attached character sheets are the AUTHORITY on how each character looks: when a shot uses reference pictures, the face, hair, skin tone and age markers come from the sheet, not from your prose — describe wardrobe, props, pose and emotion, and never add facial marks (moles, scars, blush, red noses) that the sheet does not show. Characters without a sheet are the only ones whose face may be described in prose.

detailed_description:
[Style sentence(s) first: use the global style]. Then for every shot:
[Shot 1] with NO timestamp for the first shot; subsequent shots "[Shot 2] At 00:06.000" with strictly increasing timestamps.
Each shot paragraph (350-500 English words TOTAL across all shots): shot size and composition -> subject positions -> restate the subject's wardrobe, props, pose and state on first appearance in this shot -> ONE main action per shot (split complex actions into sub-moves) -> visible emotional change (show, don't label) -> camera motion with amplitude and speed -> sound events.
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
- The [Chinese] tag inside <d>...</d> is MANDATORY on every spoken line and every voiceover: it is what makes the voice speak Mandarin. A missing tag makes the model improvise a random language and the final video ends up with unintelligible gibberish speech. Write <d>[Chinese]台词原文</d> literally, every single time.
- Appearance facts must come from the source story ONLY. Never invent distinguishing marks the source does not mention (moles, scars, birthmarks, red noses, tattoos, unusual hair or eye colours). Invented marks show up in the video and clash with the fixed character sheets.
- Cast members must be easy to tell apart on screen: when creating character cards, avoid two leads that share the same age band, build and palette (e.g. two grey-bearded old men in brown robes). Differentiate by age, build, hair, silhouette and clothing colour; give each one a memorable marker taken from the source text. This is a design rule, not an invention licence: still describe only what the source supports.
- ONE character = ONE card. The same person may be referred to both by a name and by a code or serial number (e.g. 机关人 / 阿枢 / 一三七四一号): that is ONE person. Keep a single character card, pick the most informative name, and reuse it verbatim in every shot; never create a second card for another appellation of the same person.
- h3_prompt total length 2500-6000 characters; detailed_description body 350-500 English words.
- No negative-prompt syntax, no CFG hints, no technical parameters - exclusions go as prose at the end.
- Consistency: same character keeps identical appearance words every shot; same scene name reused.
- Sound: dialogue only via <d> tags; ambience in overall_soundscape; music in non_diegetic_music; never duplicate.
- Soundscape discipline (the finished video must not sound noisy): keep overall_soundscape to 1-2 short sentences describing a QUIET, low-level background bed (e.g. "distant street hum, faint wind"). Do not stack many simultaneous sound sources, do not ask for loud machinery, crowd chatter, alarms or continuous heavy noise: constant loud ambience buries the dialogue and reads as hiss in the final mix. Silence or near-silence is a perfectly good choice where it fits.
- Screen-visible text must be quoted exactly and kept in original language.`

// sysCastRules 选角差异化(2026-09-11 用户定调「不同角色脸不能相似」):
// 实证 LLM 写了 fat/scar/narrow face 等差异化特征,但 image_prompt 以巨型
// 统一风格前缀开头、身份特征挤尾部,Krea-2 渲染时全部丢弃(肥胖→清瘦、
// 疤→无疤,三张脸收敛成同一款标准帅哥)。规则:身份特征前置主导渲染,
// 全剧角色脸型/年龄/体格/记号互不相同,原文写明的外貌禁止美化抹平。
const sysCastRules = `## CAST DISTINCTNESS (critical - characters of the same series must NEVER look alike)
- Every named character gets a UNIQUE physical identity. No two characters may share the same face template: vary face shape (round / square / long / narrow / heart), age (child / teen / young adult / middle-aged / elderly), build (slim / average / stocky / burly), skin tone, hairstyle & hair color, and give each at least ONE signature marker (scar, mole, freckles, glasses, beard, tooth gap, dyed streak, bandage, tattoo...).
- image_prompt STRUCTURE (identity first): open with this character's distinctive physical identity (age, face shape, build, hair, eyes, skin, signature marker), then costume, then "in [COPY the global style EXACTLY]", end with "plain background, upper-body framing, front-facing". Do NOT open the portrait prompt with style words — identity traits must LEAD the prompt so they dominate the render.
- If the source states a character's appearance (age, build, hair color, scars...), follow it EXACTLY; never beautify or average it away. A fat character stays fat, an old character stays old, a scarred character keeps the scar.
- Faces are ORIGINAL ANIMATION DESIGNS (avoid infringing anyone's likeness): never model a character on a real person, actor, singer, celebrity or public figure, never name a character after one, and never write "looks like <real person>" in any field.`

// animeFaceLock 角色画风锁(注入到用户附加要求最前面)。
//
// 三个"必须"对应三件不同的事:①`style` 字段与每张 image_prompt、每镜 H3 提示词
// 用同一种动画画风(否则定妆照与视频又是两套);②脸必须是动画脸(平涂 + 线稿 +
// 哑光,不是照片里的皮脂高光);③原创设计,不影射任何真实人物 —— 这一条是避免侵权
// 的落点,必须写在分镜层,不能只靠画图模型自觉。
const animeFaceLock = `CHARACTER STYLE LOCK (MANDATORY, highest priority):
- The 'style' field, EVERY character image_prompt and EVERY shot h3_prompt must use the SAME stylized ANIMATION art style (2D/2.5D donghua look: clean line art, cel-shaded flat colour, simplified graphic facial features, matte powdery skin with a dull even finish).
- Never write or imply a photorealistic human face, live-action actor, photographic skin texture, glossy or oily skin, wet highlights, or a plastic/wax finish anywhere in the plan.
- Every face is an ORIGINAL animation design: never describe a character as looking like a real person, actor, singer, celebrity or public figure, and never use such a name as a character name.`

// SystemPrompt 单轮直出(完整 schema + 全部规则)。
const SystemPrompt = sysHeader + `

` + sysOutputFull + `

` + sysShotRules + `

` + sysH3Format + `

` + sysHard + `

` + sysCastRules

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
	MaxShots     int    // 期望镜头数;0=内容驱动(不预设,LLM 按原文节拍自行决定)
	TotalSeconds int    // 期望总时长(秒);0=由镜头数推算
	ExtraHint    string // 用户附加要求(风格/题材)
	Chibi        bool   // 生成 Q 版形象(内心活动用;分镜提示词指引 Q 版 cameo)
	// AnimeFaces 角色动漫化 + 非真人脸(项目 script.stylized_faces,未表态即开)。
	// 开启时在提示词最前面钉一条角色画风锁 —— 分镜这一层不定,后面两头(定妆照
	// 的 image 块、视频的 render 块)都是在给一个已经写成"写实电影感"的方案打补丁。
	AnimeFaces bool
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

// defaultContentDrivenShots 内容驱动(MaxShots=0)时的内部兜底镜头数。它仅用于
// 分批阈值判断与每段秒数换算,不会被写进提示词 —— 提示词走
// buildSingleUserPrompt 的内容驱动分支,不预设镜头数。
const defaultContentDrivenShots = 12

// Generate 调用 LLM 直出分镜方案并做结构校验与修复。
//
// 双路径:小任务(≤6 镜且文本 ≤1500 字)单轮直出;大任务走「规划轮 + 分批
// 出镜轮」——单次生成量随批拆分,推理模型单轮压回 1-3 分钟,远离超时与
// max_tokens 截断(2026-09-08 长文超时治理)。
// Generate 分镜入口。sourceText 若含【全局风格】,提取并锁定——跨集风格
// 一致的代码级保障(不依赖 LLM 服从指令,直接覆写)。
func Generate(ctx context.Context, client *llm.Client, sourceText string, opt Options) (*project.Plan, error) {
	// 提取 sourceText 中的全局风格行,锁进 ExtraHint 强制 LLM 沿用
	if locked := extractGlobalStyle(sourceText); locked != "" {
		lock := "STYLE LOCK (MANDATORY): The global visual style for this series is: " + locked +
			". You MUST use this EXACT style in the 'style' field and in ALL character image_prompts. " +
			"Do NOT invent a different style. Character portraits must be in the same art style as the video."
		if strings.TrimSpace(opt.ExtraHint) != "" {
			opt.ExtraHint = lock + "\n" + opt.ExtraHint
		} else {
			opt.ExtraHint = lock
		}
	}
	// 角色画风锁(2026-09-24 用户定调「角色不要真人脸,动漫化,避免侵权」):
	// 放在 ExtraHint **最前面**,优先级高于用户自己写的风格附加要求 —— 分镜这一层
	// 定不下来,后面定妆照/视频两头都是在给"已写成写实电影感"的方案打补丁。
	if opt.AnimeFaces {
		lock := animeFaceLock
		if strings.TrimSpace(opt.ExtraHint) != "" {
			opt.ExtraHint = lock + "\n" + opt.ExtraHint
		} else {
			opt.ExtraHint = lock
		}
	}
	// 内容驱动语义:MaxShots=0 表示「镜头数由原文节拍决定」,不能简单换成
	// 一个具体数字了事 —— 否则提示词里那句 "(%d shots)" 又把数量写死了,与
	// 用户「该多少就多少」的定调相悖。这里先记下原始意图供提示词分支使用,
	// 下面的兜底值只服务分批阈值判断与每段秒数换算。
	contentDriven := opt.MaxShots <= 0
	secondsGiven := opt.TotalSeconds > 0
	if opt.MaxShots <= 0 {
		opt.MaxShots = defaultContentDrivenShots
	}
	if opt.TotalSeconds <= 0 {
		opt.TotalSeconds = opt.MaxShots * 7
	}
	if opt.MaxShots > batchThresholdShots || len([]rune(sourceText)) > batchThresholdRunes {
		plan, err := generateBatched(ctx, client, sourceText, opt, contentDriven)
		if err != nil {
			return nil, err
		}
		enforceStyleLock(plan, sourceText)
		return plan, nil
	}
	plan, err := generateSingle(ctx, client, sourceText, opt, contentDriven, secondsGiven)
	if err != nil {
		return nil, err
	}
	enforceStyleLock(plan, sourceText)
	return plan, nil
}

// extractGlobalStyle 从 sourceText 提取「全局风格:」行的值(截到换行)。
func extractGlobalStyle(sourceText string) string {
	for _, line := range strings.Split(sourceText, "\n") {
		if i := strings.Index(line, "全局风格:"); i >= 0 {
			v := strings.TrimSpace(line[i+len("全局风格:"):])
			if v != "" {
				return v
			}
		}
	}
	return ""
}

// enforceStyleLock 代码级覆写:LLM 若无视 STYLE LOCK 指令自行发明风格,
// 在此处强制回写为全局风格(跨集一致的最终保障)。
func enforceStyleLock(plan *project.Plan, sourceText string) {
	locked := extractGlobalStyle(sourceText)
	if locked == "" || plan == nil {
		return
	}
	if plan.Style != locked {
		plan.Style = locked
	}
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

// contentDrivenPromptFmt 内容驱动模板(MaxShots=0)。与 UserPromptFmt 的关键
// 区别是不写死镜头数:只给总时长,或连时长也交给内容决定,镜头数交由
// sysShotRules 的「镜头数由内容决定、不设上限」
// 按原文节拍自行判定。
const contentDrivenPromptFmt = `Source text (Chinese):
"""
%s
"""

Additional requirements:
%s
%s
Produce the STRICT JSON plan now.`

// buildSingleUserPrompt 组装单批直出的用户提示词。contentDriven 时不预设镜头
// 数;secondsGiven 决定要不要给时长约束(用户没给时长则连时长也交给内容决定)。
func buildSingleUserPrompt(sourceText string, opt Options, contentDriven, secondsGiven bool) string {
	hint := styleHint(opt)
	if !contentDriven {
		return fmt.Sprintf(UserPromptFmt, sourceText, opt.TotalSeconds, opt.MaxShots, hint)
	}
	target := "- Target: short video. Decide BOTH the shot count and the total duration YOURSELF from the source text's natural story beats."
	if secondsGiven {
		target = fmt.Sprintf("- Target: short video, total duration about %d seconds. Decide the shot count YOURSELF from the source text's natural story beats.", opt.TotalSeconds)
	}
	return fmt.Sprintf(contentDrivenPromptFmt, sourceText, target, hint)
}

func generateSingle(ctx context.Context, client *llm.Client, sourceText string, opt Options, contentDriven, secondsGiven bool) (*project.Plan, error) {
	user := buildSingleUserPrompt(sourceText, opt, contentDriven, secondsGiven)
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
		// LLM 常在字符串值里写裸换行/制表符(标准 JSON 非法),先修再试一次
		if err2 := json.Unmarshal([]byte(repairJSONStrings(s)), &plan); err2 != nil {
			return nil, fmt.Errorf("JSON 解析失败: %v", err)
		}
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
    {"id":"char_xxx","name":"中文名","description":"中文外貌/服装/气质,固定不变的关键特征(含至少一个独特记号)","image_prompt":"ENGLISH portrait prompt, IDENTITY FIRST per CAST DISTINCTNESS: [distinctive physical identity: age, face shape, build, hair, eyes, skin, signature marker] then [costume] then 'in [COPY the global style EXACTLY]' then 'plain background, upper-body framing, front-facing'","voice":"ENGLISH voice description"}
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
- characters/scenes 覆盖全片所有出场者;角色外貌描述具体可画,全片复用。` + "\n\n" + sysCastRules

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
func generateBatched(ctx context.Context, client *llm.Client, sourceText string, opt Options, contentDriven bool) (*project.Plan, error) {
	hint := styleHint(opt)

	// ---- 轮 1:规划 ----
	// 内容驱动时不预设总镜头数:由规划轮自己决定拍多少镜才 cover 完整,只保留
	// 「每段 3-N 镜」这个分批粒度 —— 它是技术参数(保证单次输出不溢出),不是数量上限。
	var planUser string
	if contentDriven {
		planUser = fmt.Sprintf(`Source text (Chinese):
"""
%s
"""

Additional requirements:
- Target: short video. Decide the TOTAL shot count YOURSELF from the source text's natural story beats — there is NO upper limit; cover the story completely and clearly.
- Then split it into consecutive segments of 3-%d shots each, so the segments continuously cover shot ids 1..N where N is the total you decided.
%s
Produce the STRICT BATCH PLAN JSON now.`, sourceText, batchMaxShots, hint)
	} else {
		planUser = fmt.Sprintf(`Source text (Chinese):
"""
%s
"""

Additional requirements:
- Target: short video, total %d shots, total duration about %d seconds.
- Split the story into segments of 3-%d shots each (segments must continuously cover shot ids 1..%d).
%s
Produce the STRICT BATCH PLAN JSON now.`, sourceText, opt.MaxShots, opt.TotalSeconds, batchMaxShots, opt.MaxShots, hint)
	}

	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: planSystemPrompt},
		{Role: "user", Content: planUser},
	}, true)
	if err != nil {
		return nil, fmt.Errorf("规划轮失败: %w", err)
	}
	var head planHead
	if err := json.Unmarshal([]byte(repairJSONStrings(trimJSONBlock(raw))), &head); err != nil || head.Title == "" || len(head.Segments) == 0 {
		return nil, fmt.Errorf("规划轮输出解析失败(缺少 title/segments)")
	}
	// 段规范化上限:内容驱动时不能用人为此限 —— 否则 LLM 决定拍 20 镜会被截到
	// 内部兜底值,「不设上限」就成了空话。这里取规划轮自己给出的最大镜号。
	segMax := opt.MaxShots
	if contentDriven {
		segMax = 0
		for _, s := range head.Segments {
			if s.To > segMax {
				segMax = s.To
			}
		}
		if segMax <= 0 {
			segMax = opt.MaxShots
		}
	}
	normalizeSegments(&head, segMax)

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
	s := repairJSONStrings(trimJSONBlock(raw))
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
