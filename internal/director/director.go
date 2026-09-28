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
  "style": "global visual style in ENGLISH, 1 main style + 1-2 texture traits. IMPORTANT: if the source text contains 【全局风格】, COPY that style EXACTLY — do NOT invent a different one. The style must be IDENTICAL across all episodes of the same series. It must be a CINEMATIC GUOFENG (Chinese period) style with a photoreal 3D surface and IDEALISED, SEMI-STYLISED faces — e.g. 'GUOFENG ANIMATED-FEATURE ART DIRECTION … a photoreal 3D surface with IDEALISED, SEMI-STYLISED faces; muted celadon-grey and ink-wash palette; real materials with visible craft; soft motivated key light; matte finish, natural film grain, shallow depth of field; a Chinese period film, not a western fantasy, not cyberpunk neon, not japanese anime'. Write it as ENGLISH prose, never as a 2D cartoon / cel-shaded / hand-drawn-anime style, and never as photorealistic live-action with real actors.",
  "characters": [
    {"id":"char_xiaofan","name":"小凡","kind":"human","description":"中文外貌/服装/气质,固定不变的关键特征(含至少一个独特记号)","image_prompt":"ENGLISH portrait prompt. It MUST contain the literal block "IDENTITY LOCK (do not change): " followed by the distinctive physical identity (age, face shape, brows/eyes, nose/lips, skin, hair, signature mark, costume) AND **the body-proportion anchor "eight and a half heads tall"** — the renderer reads THAT block to anchor identity across shots and to tell characters apart; a card without it silently loses both. Then:  shape, build, hair, eyes, skin, signature marker] then [costume] then 'in [COPY the global style field above EXACTLY — same art style as the series]' then 'plain background' ONLY (never write framing, crop or camera angle — the renderer adds the framing that matches the kind and the project's aspect ratio). NEVER open with style words; NEVER use a different art style from the global style.","voice":"ENGLISH voice description, e.g. 'calm young male voice'"}
  ],
  "scenes": [
    {"id":"scene_inn","name":"悦来客栈","description":"中文场景描述","image_prompt":"ENGLISH establishing-shot image prompt for this location, pre-event and unpeopled (see SCENE CARDS)","landmarks":"连续性地标:同场景跨镜屏幕位置必须不变的固定物(门框/中岛/系船桩/树)","light_state":"光态:日/夜 + 主光方向(如 黄昏,主光从西侧门洞斜射)"}
  ],
  "props": [
    {"id":"prop_xxx","name":"中文名","en":"ENGLISH look phrase for the picture model","description":"中文外观/材质/配色/尺寸/记号","image_prompt":"ENGLISH 1:1 white-background single-object prompt (see PROP CARDS)"}
  ],
  "shots": [
    {"id":1,"scene":"scene_inn","characters":["char_xiaofan"],"prop_ids":["prop_xxx"],"shot_size":"中景","camera":"slow push-in, small amplitude","view":"主视角,逐字取自官方 9 条词表(见 VIEWPOINT)","action":"中文一句话画面动作","dialogue":[{"speaker":"小凡","text":"你怎么也来这里了?"}],"narration":"","duration":6,"mode":"ref2va","hook_type":"官方钩子词表之一(见 SHOT TABLE)","end_state":"尾帧状态:这一镜最后一帧看得见的东西(位置/姿态/表情/视线/环境),禁写听觉与氛围词","h3_prompt":"FULL H3 prompt text as defined below"}
  ]
}`

// sysPropRules 关键道具卡（用户口径：**关键物品必须生成道具**，且跨镜不许漂）。
//
// 【2026-09-28 实测病根】某个真实项目的第一集 plan **连 props 字段都没有**：
// 25 个镜头提到碑刀/烙铁/名册，`prop_ids` 全空 —— 于是每一镜都由模型**自己发明**那把刀，
// 长短、缠柄、朱砂线一镜一个样，观众看到的就是"前后镜漂移"。而根因在平台这一侧：
// **直出 schema 里从来没有 props**，LLM 不可能凭空提取它。
//
// 这一段把"什么该收成道具、每件要写什么、怎么保证跨镜一致"讲清楚；跨镜一致靠的是
// 装配层（`pipeline/prop_anchor.go`：把卡片原文逐字锚进引用它的每一镜），不靠 LLM 每镜自觉复述。
const sysPropRules = `## PROP CARDS (the series' signature objects — required, not optional)

- **Extract props**: any object that RECURS, is HANDLED in key beats, or is the SIGNATURE of a character or of the series (a weapon, a tool, a talisman, a letter, a token, a book, a lamp). Typical episodic drama has 2-6 of them. An episode with NONE is almost always a miss: a blade that a man carves his own stele with IS a prop, and so is the iron brand in the fire, the ledger the antagonist reads from, the cloth wound round a wrist.
- **Not props**: one-off clutter, scenery and fixed furnishings (those belong to the scene card), and anything that is part of a costume (that belongs to the character card).
- One card per prop, ` + "`id: \"prop_xxx\"`" + `, with: **name** (Chinese), **en** (one ENGLISH look phrase — form + material + colour), **description** (Chinese: 外观/材质/配色/尺寸/记号), **image_prompt** (ENGLISH, 1:1, single object, centred, plain pure white background, no hands, no text). Give the object ONE canonical look here — marks, wear, knots, inlay, its exact size against a hand.
- **Every shot that shows a prop must list it in ` + "`prop_ids`" + `** (by id). A shot that mentions the object in its action but leaves prop_ids empty is the drift bug: the model then invents a fresh object for that shot.
- **Consistency is by TEXT**: props never get a picture slot in the video reference set, so the SAME object must be described with the SAME words wherever it appears. Do not restyle, re-colour, resize or re-decorate it between shots, and never let one shot's object become a different one.
- A prop that is really an interface/screen (a holographic panel) has its interior left BLANK — never letter anything into it.
- The object's own story wear is fine (chipped edge, scorch mark, ink stain) as long as the same wear is written every time.`

// sysShotRulesTpl 分镜规则段**模板**:时长数字全部用 {{MIN}}/{{MAX}}/{{CPS}} 占位,
// 由 renderShotRules() 从常量渲染 —— 这段文案里**不许**再出现写死的时长数字。
//
// 为什么改成模板(2026-09-26 平台侧整改清单 §1.3):此前这里写死 "Each shot 4-8 seconds"
// 与 "min 4, max 8",而计划契约是 4-10、云端引擎的下限是 5 秒 —— 三处口径各自漂:
// LLM 直出路径稳定产出 4 秒镜(云端再静默抬成 5 秒,成片比计划长),而 4-8 又比契约的
// 4-10 更窄,长台词镜被迫拆得比需要的更碎。现在文案里的时长与语速都由常量渲染,
// 并有断言盯着(internal/project/plan_contract_ext_test.go 与本包 prompt_duration_test.go)。
//
// 区间取 GenMinShotSeconds-MaxShotSeconds(=5-10)而不是更窄的 5-8:上限就是契约上限,
// 不再用一个比契约更窄的硬区间逼 LLM 多拆镜;"宁可拆短"这个**负载偏好**由下面那句
// "costs grow faster than linear … split a long beat" 表达(它是实测结论,见 CHANGELOG
// 「单镜负载引导」),不靠一个跟契约打架的数字。
const sysShotRulesTpl = `## SHOT PLAN RULES
- Shot count is driven by the CONTENT, with NO upper limit: cover this episode's story completely and clearly. If the source has many beats, use more shots — a dense chapter may legitimately need 15-30 shots, and that is fine. Always prefer MORE, smaller shots over fewer overloaded ones; never compress several actions into one shot to save shot count. Each shot {{MIN}}-{{MAX}} seconds (integer): local H3 rendering costs grow faster than linear with frame count (a 10s shot is ~2.8x the load of a 6s one on the same card), so split a long beat into two shots instead of stretching one. Hook in shot 1, payoff at the end.
- CONTINUITY (critical, 2026-09-10): consecutive shots must read as ONE continuous flow. Each shot's action must BEGIN where the previous shot ENDED — same location (unless a deliberate scene change), carried-over posture/emotion/props, coherent time-of-day and lighting. NEVER skip a narrative beat the audience needs; if a beat needs two camera moments, split it into two shots with one clear action each. On scene changes, bridge with narration or a matching visual anchor, never jump-cut blind.
- shot_size 景别用中文:远景/全景/中景/近景/特写。Emotional beats and dialogue MUST use 近景/特写 (H3 face tokens are scarce; closer framing beats wider framing).
- NO CARD, NO FACE (critical, 2026-09-27): 特写/近景 puts a face on screen, and a face can only come from a reference image — those are attached from THIS SHOT's characters list, nothing else. So before writing 特写/近景, make sure every person whose face is visible is listed in characters (bound to their approved card). If a person has no card (or you deliberately do not want to fix their identity), then do NOT frame them close: use a back view, a partial body, or an anonymous crowd — AND DO NOT describe their face at all. When a shot genuinely needs a person on screen with no card, keep the framing off their face AND mark it explicitly with "unbound": true plus "unbound_reason": "no_card" (the person is not in this episode's cast) or "faceless_by_design" (you chose to keep them faceless) — that flag is what tells the contract check this is deliberate, not a forgotten binding. Never pair a close framing with an anchor that denies the person ("empty frame, no people") while the action describes a face: that contradiction makes the model paint a brand-new stranger's face on every take, and the audience sees a different person each time.
- One camera move per shot, written as a **natural English action inside the shot** (never stacked as labels at the end of a sentence), from the official motion vocabulary ONLY: Zoom In / Zoom Out (focal length changes, camera body still), Push In / Pull Out (camera moves forward / backward), Pan Left / Pan Right (lens pivots horizontally), Truck Left / Truck Right (camera translates sideways), Tilt Up / Tilt Down (lens pivots vertically), Pedestal Up / Pedestal Down (whole camera rises / descends), Arc Shot (arcs around the subject), Tracking Shot (follows a moving subject), Static Shot (camera and lens still), Shake Slightly / Shake Strongly, POV, Roll Clockwise / Roll Counterclockwise. Add amplitude ("with small amplitude" / "with large amplitude") and speed ("at slow speed" / "at fast speed") ONLY when they carry meaning — the official guide omits medium amplitude and normal speed. NEVER use jargon like dolly, crane, whip pan.
- CUTS have their own vocabulary (official §4.2): "the camera cuts to" / "the shot cuts to" / "the shot transitions to" / "the shot changes to" / "the shot switches to". A cut must introduce NEW information — subject, space, state, viewpoint or time; if only the distance or a slight angle needs to change, prefer CAMERA MOTION over a cut. Cross-dissolve / fade / wipe only where they are deliberately asked for (our between-scene rule asks for a fade — see the continuity section).
- mode: "ref2va" when character/scene reference images will be provided (any shot with on-screen characters); "t2va" for pure text-to-video shots without references.
- dialogue speaker must match a character name, or use speaker "旁白" for narration. Keep spoken lines short (≤20 chars), punchy, ending with 。?! naturally. At most 2 speakers per shot.
- DIALOGUE MUST FIT THE SHOT (critical): Mandarin delivery runs about {{CPS}} characters per second, and H3 needs roughly one extra second of headroom to start and settle. The shot's duration MUST be at least ceil(total_dialogue_chars / {{CPS}}) + 1 (rounded up to a whole second, min {{MIN}}, max {{MAX}}). A 20-character line therefore needs 6 seconds — never park a 20-character line in a {{MIN}}-second shot: the speech gets cut off and the finished video loses the end of the line. If a line cannot fit {{MAX}} seconds, split the beat into two shots, each carrying part of the line.
- NO NARRATOR LANE (口径调整 2026-09-28，用户指定): do NOT create a standalone narrator/旁白 voice. Convert it instead:
  · subjective material (what a character feels, remembers or judges) becomes that character's **inner monologue** — an off-screen line in the SAME character's own voice, written inside <d> with that character''s (Sx), and the shot states that their lips stay closed (see ` + "`vo_kind=inner`" + ` and the INNER MONOLOGUE rule);
  · objective, world-level information becomes either a **diegetic caption** (a location/time card the shot itself carries) or a line spoken by an on-screen character.
  A separate narrator timbre is never used: it flattens every episode into the same voice and the audience stops hearing whose head they are in.
- SELF-CONTAINED (critical — one shot = one generation, the model has NO memory of the previous shot): every field must be written out in full. NEVER write "同上" / "如上" / "same as above" / "as before" / "…" and NEVER leave a field empty or fill it with a placeholder like "N/A" or "-" when the field asks for content. Location, people, light, time of day and prop state must all be stated in THIS shot.
- SHOT LENGTH = MAX(dialogue time, action time) (official rule, applies at every duration): first compute the spoken line (total characters ÷ the character's emotional speaking rate — 低沉/哽咽 2.5-3.5, 威严/宣告 3-4, 正常叙事 3.5-4.5, 轻松/调侃 4-5, 愤怒/争吵 5-6.5 chars per second), then the time the on-screen action needs, and take the LARGER. If it exceeds {{MAX}} seconds, split at a sentence boundary — NEVER speed up dialogue and never cut a line in half.
- PACING QUOTAS (official, scaled to our shot length): the first MEANINGFUL line of dialogue must arrive within 12 seconds of the episode start; never leave a stretch longer than 12 seconds with no dialogue at all (insert a line, a caption or a reaction beat); the emotional core (戏眼 — a held close-up, a silent stand-off, tears, a clenched fist) lasts AT LEAST 5 seconds, never a quick cut.
- When the action is shorter than the dialogue, DO NOT pad with talk: add reaction shots (a listener's face, an environment detail, a close-up of a prop), slow the movement (a turn, a raised hand), use a slow camera move, or hold on a micro-expression.  When the dialogue is short, the action sets the length and the line sits in the gap.
- Keep the framing varied: never the SAME shot_size (景别) more than 6 shots in a row.
- Write only actions a real actor performs easily. Exact physical interactions and micro-movements (睫毛颤, 手指细小动作, 精密的持物角度) always break: externalise the emotion as body language instead (tension = glancing at a timepiece, eyes darting; anger = fists clenched, jaw tight; grief = head down, shoulders trembling), and give amplitude + speed + what it continues from.`

// sysShotRules 渲染后的分镜规则段(单轮 SystemPrompt 与分批 batchSystemPrompt **共用同一段**,
// 不各写一份 —— 写两份就又是一处会漂的口径)。
var sysShotRules = renderShotRules()

// renderShotRules 把时长/语速常量渲染进规则文案(纯函数,便于直接断言)。
//
// 用占位符替换而不是 fmt.Sprintf 的位置参数:这一段有 7 处数字,位置参数一错位
// 就会把"下限"印成"上限",而字符串本身看不出错 —— 名字替换不会错位。
func renderShotRules() string {
	return strings.NewReplacer(
		"{{MIN}}", strconv.Itoa(GenMinShotSeconds),
		"{{MAX}}", strconv.Itoa(MaxShotSeconds),
		"{{CPS}}", strconv.FormatFloat(CharsPerSecond, 'g', -1, 64),
	).Replace(sysShotRulesTpl)
}

// sysViewRules 视角/机位段（2026-09-27 技能侧整改单 §1.1）。
//
// 【为什么单列一段】复核证据：`camera` 100% 由 `shot_size` 决定、只有 2 个取值、**零机位角度**，
// 最长连续 21 镜同景别 —— 观众看到的是"用同一台机器拍完整集"。根因是产出侧从没被要求过写
// 机位角度（只有运镜），而运镜词表里根本没有"从哪看、看多高"。
//
// 9 条词表逐字来自官方 `skills/h3-prompt-writing`，经技能侧
// `references/分镜脚本-多引擎输出规范.md` §二 翻译；**逐字**是硬要求 ——
// 这些是模型侧被训练过的描述短语，改写等于换词表，而且平台侧的契约门按字符串判。
//
// `view` 字段与提示词里的串必须**逐字一致**：字段是自检/审计的入口，提示词是渲染读的东西，
// 两者不一致时自检面板就在骗人（契约里有一条专门判这个）。
const sysViewRules = `## VIEWPOINT (camera angle — write it where it applies, OMIT it where it does not)

The official list of nine view strings is a **large-viewpoint / camera-setup table**. Write ONE of them, word-for-word, into the shot's h3_prompt AND into the shot's "view" field (byte-identical) — but ONLY when the shot is actually one of those setups:
  ultra-wide 45-degree aerial view | ultra-wide aerial view | low-altitude flying aerial view | ultra-wide eye-level deep perspective | compressed distant depth | low-angle upward view | low-angle peeking upward view | low-angle strong perspective | telephoto compression

- **MANDATORY** on the shots that carry the space: the ESTABLISHING SHOT (first shot of a scene) and every 远景/全景 (long shot / full shot). Leaving those empty removes the one thing that tells the audience where they are.
- **OPTIONAL — leave the field EMPTY** on the framings the official table has no entry for: 平视中景 / 平视中近景 / 俯拍特写 and their kind. The table contains no "eye-level medium shot" and no "high-angle looking down", so picking something anyway means writing a WRONG word (a close-up labelled "ultra-wide 45-degree aerial view") — and a wrong word is worse than an empty field: empty just means one less camera cue, wrong actively misdirects the shot.
- Pick by WHAT THE SHOT MUST SHOW, never by habit:
  · terrain / building layout / roads / courtyards -> "ultra-wide 45-degree aerial view" (preferred opener for a place)
  · city / sect / battlefield / wasteland / large building complex -> "ultra-wide aerial view"
  · grassland / forest / mountains / canyon (a sense of flying over) -> "low-altitude flying aerial view"
  · hall / long corridor / street / canyon / bridge (linear depth into the frame) -> "ultra-wide eye-level deep perspective"
  · distant mountains / ruins / stacked fog layers -> "compressed distant depth"
- The last four — "low-angle upward view", "low-angle peeking upward view", "low-angle strong perspective", "telephoto compression" — are CONDITIONAL. Use them only when the content genuinely asks for that look (a tower, a mountain gate, a colossus, ruins, a deliberate near/far contrast). They turn an ordinary beat into a spectacle shot, so never make them your default.
- VARY IT. Among the shots that DO carry a view, use at least six DIFFERENT viewpoints across an episode (two or three if the episode is short), and never run more than six consecutive shots at the same shot_size — a long run of identical framing is what makes an episode feel like one unbroken take.
- ESTABLISHING SHOT — the FIRST shot of every scene, and the first shot after any scene change. The audience is seeing this place for the first time, so establish it: add this string VERBATIM to that shot's description: "ultra-wide angle, grand scale, epic wide shot, vast space, distant horizon, cinematic establishing shot". Never open a location on 特写 or another small framing — a face or a single prop tells the audience nothing about where they are.

Do not confuse viewpoint with camera movement: "view" is where the camera IS and what it looks at; "camera" is how it moves (push in / pan / truck …). A shot needs both.`

// sysContinuityRules 尾帧状态与跨镜连贯段（2026-09-27 技能侧整改单 §1.2）。
//
// 【为什么单列一段】复核证据：平台侧只有 seam 开关（把上一镜尾帧当下一镜首帧喂进模型），
// 却**没有任何"尾帧长什么样"的写法约束** —— 于是"接上一镜"全靠模型自己猜，接不上时
// 观众看到的是"人突然换了姿势/道具消失/光线换了"。
//
// 三档写法对应三种真实的剪辑关系（同片段内 / 同场景跨片段 / 跨场景）。跨场景**明确不要求**
// 动作连贯：硬接动作只会让新场景的第一镜莫名其妙地接着上一个地点的姿势。
// sysBlockingRules 占位一致性：用户 2026-09-28 指定的口径（源自《【额外】仿真人10S分镜头指令》的
// 「占位一致性规则」），但**按 H3 的格式重写** —— 那套文件是即梦/Seedance 的字段式写法
// （【关键道具】【人物】独立成行），H3 只有一段散文，所以这三样必须写进 detailed_description 的句子里。
//
// 为什么值得单列一段：H3 逐镜独立生成，没有"上一镜记忆"，跨镜一致**全靠每一镜自己写全**。
// 缺了它，道具的持有手、角色的相对站位、特效的落点每一镜都会漂。
const sysBlockingRules = `## BLOCKING & PROP HANDLING (write these INTO each shot's description)

H3 renders every shot independently and keeps no memory of the previous one, so each shot must state, in prose:
- **Who holds what, in which hand, facing where**: e.g. "in his right hand he holds the thin stone-cutting blade, its edge turned toward the stele". A prop mentioned without a holder/hand/orientation is a prop the model re-invents.
- **Relative staging when two or more people share the frame**: who is in the foreground, who is behind whom, how far apart, which side of frame each occupies: e.g. "he stands at the centre of the lane, the old woman three paces in front of him, the boy half a step to her right rear".
- **Special effects: origin, shape, colour**: e.g. "a thin gold light leaves his knuckles in a spray and lights the right side of his face".
- On a return to the same location within the same scene, restate that staging rather than saying "as before" (there is no "before" for this generation).
- One main action per shot; the action is performed with the hands and body at ARM level, never at finger level (see THE SHOT MUST MOVE).`

const sysContinuityRules = `## SHOT END STATE & CROSS-SHOT CONTINUITY

- EVERY shot carries an "end_state" (尾帧状态): what the LAST FRAME of this shot shows. Write only what a camera can see — where each character stands in frame, their pose, facial expression, gaze direction, and the state of the environment (door standing open, water still dripping, lantern lit, dust settled).
- NEVER put hearing, mood or atmosphere words in end_state (no 寂静 / 安静 / 沉默 / 压抑 / 气氛 / 氛围 …). The next shot is written FROM this text: a downstream model cannot draw "silence", so those words only dilute the visual facts the next shot needs.
- WITHIN ONE SCENE: the next shot's description begins where the previous shot ended — read the previous shot's end_state and continue from it ("然后 …" / "紧接着 …"), carrying over position, pose and props.
- SAME SCENE, ACROSS SEGMENTS OR BATCHES: the first shot of the new batch starts with "接上一片段结尾，" and RESTATES the previous end_state before continuing with the new action.
- ACROSS SCENES: do NOT force action continuity. Cut with a fade-out/fade-in and a location-and-time caption ([地点·时间]) so the audience knows the place and time changed; pick up the new scene's own first action instead of the previous scene's pose.`

// sysShotTableRules 官方「标准镜头信息表」段（六列 + STEP 5.5 自检）。
//
// 依据 MiniMax-AI/MiniMax-H3 `skills/3d-animation-short-generator/references/shot-table-spec.md`。
// 为什么并进来：平台的 plan.json **就是**那张镜头表（六列 ↔ 字段对照见
// internal/project/shot_table.go），官方把这张表定为强制步骤、且配了六条自检。
// 其中三条（钩子密度、单镜角色数、空间锚点继承）是模型侧自己能保证的，写进提示词；
// 另三条（时长上限、每秒钟指令覆盖、跨镜连贯链）由契约的硬区间与尾帧状态判据承担。
//
// 最容易被忽略的是**钩子类型**：没有它，"这一集是不是平的"只能靠感觉说；
// 有了它，密度（每三镜至少一个 reveal/reversal/callback）与两端强钩子都是可判的。
const sysShotTableRules = `## SHOT TABLE (the official six columns — every shot must fill them)

The per-shot JSON below IS the official standardized shot table. Each field maps to one of its six columns, and the official self-check has to pass before anything is rendered:
  · Shot ID & Duration      → id + duration
  · Continuity Handoff      → end_state (see SHOT END STATE & CROSS-SHOT CONTINUITY)
  · Reference Anchors       → scene (its landmarks + light_state) + characters (approved card names) + the reference pictures attached to this shot
  · Hook Type               → hook_type: ONE label, from this controlled vocabulary ONLY: setup / visual-joke / reversal / reveal / callback / suspense / tender / chase / expression-beat / climax
  · Shot Description        → action + h3_prompt (keep the detail inside the shot paragraph, second by second where the beat needs it)
  · Audio & Dialogue Track  → dialogue / narration, and the <d> blocks inside h3_prompt

Self-check (official STEP 5.5 — the three checks a table can fail on):
  1. HOOK DENSITY: every shot carries a hook_type; every THREE consecutive shots include at least one reveal / reversal / callback; the opening shot and the closing shot each carry a strong hook (visual-joke / reversal / reveal / suspense / tender). A stretch of shots with no strong hook reads as flat, no matter how good the pictures are.
  2. CAST PER SHOT: no shot has more than THREE important characters (characters with on-screen action or dialogue). More than that dilutes attention — everyone renders worse and faces start swapping. Split the beat, or leave the extras as environment instead of cast.
  3. SPATIAL ANCHORS: every shot inherits its scene card's landmarks and its light state. If a shot deliberately moves a landmark on screen or changes the light, say so inside that shot's description (e.g. "the door frame moves from the right third to centre as the camera arcs left").`

const sysH3Format = `## H3 PROMPT FORMAT (critical - follow EXACTLY)
mode=ref2va => SIX sections in this exact order, separated by one blank line:

subject_definitions:
REFERENCE ORDER (strict, but a character may occupy ONE OR TWO pictures, so the scene index is NOT fixed): pictures are attached in this order — for each character listed in the shot's "characters" array, in array order, FIRST that character's front sheet (the sharp reference for its face, body form, or material and structure — whichever its kind calls for), and THEN, if that character has a multi-view strip, ONE extra picture of the SAME subject (a horizontal strip whose LEFTMOST cell is that same front sheet; for a person or a creature the remaining cells show it turned to other angles, for an artefact it is a turntable strip, for a bodiless entity the same form from other angles). It is always the SAME subject, never a second character. After all character pictures comes exactly ONE scene picture, always LAST. Props never get a <Picture> — describe props in text only.
Locate every subject by PICTURE CONTENT, not by index: a front sheet and its multi-view strip are the SAME subject — never treat a strip as a second character. NEVER reason "N characters, therefore the scene is <Picture N+1>": with N characters the scene is the LAST picture, which is <Picture N+1> only when no character has a turnaround. You CANNOT see how many pictures are actually attached (whether a character has a multi-view strip is decided by the asset pipeline, not by you), so NEVER write a sentence about the total number of pictures and NEVER assert which <Picture> number the scene is — the renderer appends an authoritative PICTURE MANIFEST at render time that fixes the numbering. Always name the subject in words too (character name, scene name), so the manifest can correct any number you used.
<Subject 1> is [full description of the FIRST character listed in "characters"]; its front sheet is the first attached picture, and it is the SAME subject in every other picture that shows it.
<Subject 2> is [full description of the SECOND listed character, or the scene description when the shot has only one character] — identify it by its picture content, never by counting indexes.
The scene is the LAST attached picture.
Official label discipline (§2.1–§2.5): give every referenced item that must be tracked later its OWN line — what the label denotes, its reference role, and the main features to follow. One subject may be defined by SEVERAL reference assets and one asset may supply several subjects (a front sheet plus its multi-view strip is ONE subject). If a <Picture N> only identifies the source of some other referenced item and will never be analysed on its own, cite it INSIDE that item's definition instead of giving it its own line. A <Subject N> that physically speaks keeps both labels — <Subject N> (Sx). A reference audio definition states the audio's role and reuses the target speaker's global ID, e.g. <Audio 1> is the voice-timbre reference for <Subject 1> (S1).
Every <Subject> must map to a picture that exists in the order above; never invent <Picture> tags beyond that count. Picture tags appear ONLY here as a Subject's source, never standalone — with ONE exception: a FRAME ANCHOR. When a picture is used as the first frame, a keyframe, the last frame or a composition anchor, reference it on its own line inside detailed_description as <Picture N>; an anchor line says WHEN that frame is used, and is never a way to define a character, a scene or a style (those definitions belong here in subject_definitions). A multi-view strip is never a separate subject: the subject and its strip share one description — the strip only supplies the other viewing angles, and the FRONT SHEET is the authority.

summary:
[<task-type prefix>] One English paragraph summarising the target video and how the reference materials are used. Never introduce a reference label here that subject_definitions did not define.
Task-type prefix (official §3; combine several with " + ", never repeat one — and the mere presence of a video or audio file does NOT create a type): keyframe completion (an image serves as the first frame, a keyframe, the last frame or a composition anchor) · reference generation (an image / video / audio only GUIDES character, scene, style, action, camera movement or storyboard — it is not a concrete frame and not a source video being edited) · video editing · video continuation · audio reuse (the same audio signal is reused in full or in part) · audio reference (only timbre, music style, dialogue content, sound texture, beat or continuity is referenced; the signal is not copied). Our shots are normally driven by character + scene reference images → "reference generation"; when a reference audio is attached, append " + audio reuse" if that track is reused as the shot's own final audio, or " + audio reference" if only the voice timbre and delivery are referenced.

retention_analysis:
- One line per reference picture, numbered EXACTLY as in subject_definitions. Name the subject in words in every line (character name / scene name) so the render-time PICTURE MANIFEST can correct the numbering if it differs. A character may contribute TWO pictures (front sheet + multi-view strip): give BOTH that character's fully_preserved line and never describe the strip as a different or additional character. The FRONT SHEET is the authority; the strip only supplies the other viewing angles.
- The scene index is not fixed: count the pictures actually attached (a character with a strip contributes two), and confirm by CONTENT rather than assuming an index.
- Every character picture: fully_preserved - identity must stay identical to the reference image throughout every frame, WHERE IDENTITY MEANS: for a person or a Q-version figure the face shape, hairstyle and costume; for a creature its species, body form, coat or scale pattern; for an artefact its silhouette, every part, material and finish; for a bodiless entity its form, transparency and glow. Copy that exactly — do NOT blend, average, restyle or substitute anything.
- The scene picture: weak_reference - scene layout and mood referenced, camera free.
(Use official markers: fully_preserved / partially_preserved / attribute_transfer / weak_reference)
(Entry format, official §4.1 — name the label, then where it appears, then the marker: "<Subject 1> (appears in [Shot 1], [Shot 3]): fully_preserved - …" · "<Picture 2> ([Shot 1] first frame): fully_preserved - …" · "<Video 1> (cut and pacing structure): weak_reference - …". For a reference AUDIO the markers are different (official §4.2): fully_copy - the complete source audio becomes the target video's complete final audio track · partially_copy - only part of the timeline or some layers are copied, or other sounds are added/removed/replaced · reference - the signal is not copied, only timbre, rhythm, music style, dialogue content or sound texture · weak_reference - only broad category or atmosphere similarity. Choose a marker only within the role that subject_definitions already gave that label, and do NOT treat newly added actions, backgrounds or plot events in the target video as losses of reference fidelity — they are new content, not damage.)
- NEVER write speaker tags — (S1), (S2), (S1,S2) — or any custom line in THIS section: retention_analysis only says what each reference picture preserves (identity, layout, material). The speaker and entity locking sentences belong at the START of detailed_description, not here.
(For multi-character shots: EVERY character gets its own fully_preserved line. Characters WITHOUT a reference picture must NOT borrow features from any reference image — generate them from the text description only.)
The attached character sheets are the AUTHORITY on how each character looks: with reference pictures, everything the sheet shows comes from the sheet and not from your prose — describe wardrobe, props, pose and emotion, and never add marks (moles, scars, blush, red noses, extra parts or ornaments) that the sheet does not show. **Never describe a non-human subject in human terms** (no face, no skin, no age, no hair for an artefact; no solid body for a bodiless entity) — that is what makes a bronze sparrow come out as a person. Characters without a sheet are the only ones whose look may be described in prose.

detailed_description:
[Style sentence(s) first: use the global style]. Then for every shot:
[Shot 1] with NO timestamp for the first shot; subsequent shots "[Shot 2] At 00:06.000" with strictly increasing timestamps.
**Style opening (official §5.2)**: in full-reference mode the style IS established in one or two English sentences **before [Shot 1]** (this is a difference from T2VA, where the style is written after [Shot 1] — see the mode=t2va section below). Lead those sentences with the official style token that matches this series' medium — "Cinematic, 3D CG" — and then the GUOFENG specifics (the official style vocabulary is Cinematic / live-action / 2D-animated / 3D CG / claymation / watercolor / vintage film; never invent a medium outside it).
Every detail you write must correspond to something VISIBLE or AUDIBLE (official §4.1): visual style, composition, subject appearance and position, scene and key props, actions and reactions, shot changes, spoken language, synchronised diegetic sound.
Each shot paragraph — **350-500 English words (the official range for a generation task)**. Official §5.2: "A single shot does not automatically justify a shorter description" — one shot still gets a full description, distributed by information load. Dialogue-dense content prioritises fitting the complete spoken timeline over the word count, and you never pad to reach it; but do NOT run long either: measured on a real episode, our 5-10 s shot prompts ran **633-1407 words (median ~990)**, roughly double the official range, and every extra sentence dilutes attention on the beats that matter. Land in the range; describe: shot size and composition -> the shot's viewpoint (the verbatim string from VIEWPOINT) -> subject positions -> restate the subject's wardrobe, props, pose and state on first appearance in this shot -> ONE main action per shot (split complex actions into sub-moves) -> visible emotional change (show, don't label) -> camera motion with amplitude and speed -> sound events. The description must COVER THE WHOLE REQUESTED DURATION of the video: with dense dialogue, making every spoken line fit takes priority over the word count — never pad mechanically to reach 350-500 words, and never leave the tail of the requested time undescribed.
Dialogue lines: <Subject 1> (S1) says in a [voice description]: <d>[Chinese]台词原文</d>. Assign (S1)(S2) by order of first utterance and NEVER renumber within the video.
  **Speakers (official §4.4)**: give every speaking/singing character a stable ID (S1), (S2) — a speaker keeps the SAME id across shots, characters who never vocalise get no id, and simultaneous speakers use a compound id like (S1,S2). Establish the voice once, at that speaker's first appearance, as a short identifying phrase placed BEFORE <d> (character type, age, gender, on/off-screen, pitch, timbre, speaking rate, accent) — e.g. "The young woman with a quiet, breathy voice (S1) says: <d>[Chinese]…</d>". That phrase MUST be copied verbatim from that character's "voice" field in the cast list: never reword it, and never give two characters the same voice description (H3 uses this text as audio conditioning — rewording it changes the timbre). Inside <d> put only the language tag and the user's spoken words, verbatim. When a line continues across a cut, mark the joint with <scenetrans> in both parts and say the audio continues across the cut; use <cutoff> when speech is truncated by the end of the video. Keep every SPOKEN WORD inside <d> verbatim (official §4.4): never rewrite, translate, summarise or paraphrase it. Punctuation is the one exception (official §5.4): normalise it to the basic written marks , . ? ! so the line is delivered cleanly — write an unintelligible span as [unclear] instead of guessing at it, strip repeated tildes, emoji, bullets and decorative punctuation, and end every complete statement, question or exclamation with . ? or ! respectively before </d>.
  **NO EMPTY DIALOGUE TAGS (learned from a real 48-shot episode, 2026-09-28)**: a ` + "`<d>`" + ` tag is an instruction to VOICE that text. Never write a placeholder tag — ` + "`<d>[Chinese]…</d>`" + `, ` + "`<d></d>`" + `, ` + "`<d>（无）</d>`" + ` — because the model reads it as "say something here" and improvises a line, a sigh or a filler syllable; the audience hears unscripted, repeated dubbing. If the shot has no line, write NO ` + "`<d>`" + ` tag at all and let the ambience carry it. The tags you do write must be exactly the shot's dialogue lines plus its narration — verbatim, no more and no fewer.
  **Reference numbering (official)**: <Video k> and <Audio j> are numbered INDEPENDENTLY — k counts reference VIDEOS only, j counts reference AUDIO only; a reference video that happens to carry a soundtrack does NOT automatically create an <Audio j>. Speech that lives only inside the background music or a whole-track voiceover does not go inside <d> and gets no (Sx): describe it as reference audio (<Audio j>) instead.
  **Reference audio (official §5.4)**: when only timbre, rhythm, emotion or delivery is referenced, do NOT carry the original dialogue from that reference audio into the target video — write the target video's own lines. When dialogue from the reference audio IS directly reused (or the request explicitly asks for a reperformance), keep those words verbatim inside <d> in their original language. A speaking referenced subject keeps BOTH labels — "<Subject N> (Sx) says …" — and the same form with "off-screen" when it speaks from outside the frame; a speaker that has no subject definition uses a stable voice description followed by (Sx). (Sx) is assigned once, by the order of actual vocal events, and every later vocal event reuses that ID.
Off-screen narration: The narrator (S1) says in an off-screen voiceover: <d>[Chinese]旁白原文</d> while the on-screen characters' lips remain completely closed.
ON-SCREEN TEXT (official rule): any text actually visible in frame - signage, banners, plaques, labels, subtitles, neon - MUST be written out inside ENGLISH DOUBLE QUOTATION MARKS, verbatim, one quoted string per piece of text, original characters and punctuation preserved, never translated (e.g. a red neon sign reading "营业中" glows above the doorway). Text you do not quote this way must not appear anywhere in the frame: unquoted text is what the model turns into garbled glyphs. Then end the section with an exclusions sentence in prose covering everything else (no watermarks, no logos, no captions, no added lettering on clothing or props).
BRIGHTNESS GUARD (a PLATFORM rule, not an official H3 requirement — kept because it measurably helps) as the final sentence: "Subjects must remain clearly visible and adequately lit throughout the video; avoid rendering any frame nearly black or overly dark."

overall_soundscape:
1-4 English sentences in ONE continuous paragraph (official §4.6) summarising diegetic ambience plus physical and non-verbal human sound across the whole video (wind, rain, traffic, footsteps, fabric movement, impacts, breathing, laughter). Dialogue, singing and diegetic music already live in the description above — never repeat them here. Write "N/A" only when the video is explicitly silent throughout.

non_diegetic_music:
1-3 English sentences (official §4.7). Write "N/A" when there is no non-diegetic music; when there is, describe instruments + tempo + rhythm + dynamic changes and do NOT use abstract mood words or explain the score's emotional function. Do not score every shot: a short drama reads better with music only where it is genuinely wanted, and the same cue must not run across unrelated shots.

mode=t2va => THREE sections in this exact order, separated by one blank line:

integrated_multimodal_description:
[Shot 1] opens with the same style sentence(s) — **in T2VA the style is written AFTER [Shot 1], not before it** (official §4.1/§5.2: "at the beginning of [Shot 1], state the overall style and initial composition"), e.g. "[Shot 1] Cinematic, 3D CG, a medium-wide shot frames…". Then [Shot 2] At MM:SS.mmm … Same rules as detailed_description above (word count 350-500 total, dialogue tags, camera vocabulary, exclusions sentence, brightness guard at the end).

overall_soundscape:
(1-4 sentences in ONE continuous paragraph; ambience + physical / non-verbal human sound; never repeat dialogue, singing or diegetic music)

non_diegetic_music:
(1-3 sentences: instruments, tempo, rhythm, dynamics — or N/A when there is none)`

const sysHard = `## HARD CONSTRAINTS
- English structural prose. Spoken words stay verbatim inside <d>[Chinese]...</d>; only their punctuation is normalised to , . ? ! (write an unintelligible span as [unclear] rather than guessing).
- EXCLUSIONS (also a PLATFORM rule, not official: H3 has no separate negative channel, so defects are excluded in PROSE at the end of the description): the closing exclusions sentence must name the artefacts this renderer actually produces — every shot ends with a sentence covering that, so the list is the same everywhere: no deformed or fused hands, no extra or missing fingers, no melted or shapeless props, no duplicated limbs, no flicker or strobing, no colour banding, no frame jumps, no warped or drifting faces, no plastic or over-glossy surfaces, no visible lettering other than the text quoted above, no watermarks, no logos, no captions, no UI or social-media chrome.
- The [Chinese] tag inside <d>...</d> is MANDATORY on every spoken line and every voiceover: it is what makes the voice speak Mandarin. A missing tag makes the model improvise a random language and the final video ends up with unintelligible gibberish speech. Write <d>[Chinese]台词原文</d> literally, every single time.
- Appearance facts must come from the source story ONLY. Never invent distinguishing marks the source does not mention (moles, scars, birthmarks, red noses, tattoos, unusual hair or eye colours). Invented marks show up in the video and clash with the fixed character sheets.
- Cast members must be easy to tell apart on screen: when creating character cards, avoid two leads that share the same age band, build and palette (e.g. two grey-bearded old men in brown robes). Differentiate by age, build, hair, silhouette and clothing colour; give each one a memorable marker taken from the source text. This is a design rule, not an invention licence: still describe only what the source supports.
- ONE character = ONE card. The same person may be referred to both by a name and by a code or serial number (e.g. 机关人 / 阿枢 / 一三七四一号): that is ONE person. Keep a single character card, pick the most informative name, and reuse it verbatim in every shot; never create a second card for another appellation of the same person.
- KIND (mandatory, drives the renderer): every character card MUST carry a "kind" field, exactly one of:
  "human" | "object" | "spirit_pet" | "beast" | "incorporeal" | "chibi".
  Classify by WHAT THE THING PHYSICALLY IS, never by whether it is named, speaks, or has a personality:
  · human — an ordinary living person (any age).
  · object — an artefact or made thing that is not alive: a machine, puppet, marionette, statue, lantern, talisman, coin, tool, or a constructed body (一只铜雀 = object; 木偶 / 机关人 / 傀儡 = object; a walking statue = object).
  · spirit_pet — a SMALL companion creature, animal or spirit (a spirit fox cub, a pet bird, a little moon rabbit).
  · beast — a LARGE or monstrous non-human creature (a dragon, a demon wolf, a mountain serpent).
  · incorporeal — a ghost, soul, spirit, voice, memory or projection with NO solid body.
  · chibi — the character is deliberately drawn in Q / SD proportions.
  Getting this wrong is not cosmetic: the renderer gives each kind its own reference-sheet framing, its own multi-view method and its own identity lock, so a bronze sparrow marked "human" is drawn as a half-length human portrait instead of a sparrow. When in doubt about a named non-human "character", ask what it physically is and classify that.
- KIND AND FRAMING go together, but **you do not write the framing** — the renderer adds it, per kind and per the project's aspect ratio (a vertical frame gets a chest-up portrait with the whole head inside; a wide frame gets head-and-shoulders). Writing your own crop here is exactly what produced reference sheets with the top of the head cut off, so keep image_prompt framing-free:
  · human / chibi → a chest-up portrait with the whole head inside the frame (the renderer adds it; a Q-version figure gets its whole small body instead).
  · spirit_pet / beast → the WHOLE creature with head, body, limbs and tail inside the frame.
  · object → the whole object centred with margin, a three-quarter product view; and the prompt must lead with the object's material, structure and parts (never with a face, an upper body or an age).
  · incorporeal → the whole translucent form centred, edges dissolving into mist.
  End image_prompt with a background phrase ONLY: "plain background" (use "plain dark background" for incorporeal). No "front-facing", no "upper-body", no "full body", no crop words — those come from the renderer.
  Also pick the right "voice": a non-speaking entity gets a sound description (e.g. "tiny metallic chirp, no human words"), never a spoken voice, and such a character must NOT be given full spoken <d> lines — at most one short vocalisation, otherwise convey it as a sound event or in narration.
- h3_prompt total length 2500-6000 characters; detailed_description body 350-500 English words **as a target**: when the shot carries dense dialogue, covering the whole requested duration and finishing every spoken line comes FIRST — never pad to hit the count.
- No negative-prompt syntax, no CFG hints, no technical parameters - exclusions go as prose at the end.
- Consistency: same character keeps identical appearance words every shot; same scene name reused.
- Sound: dialogue only via <d> tags; ambience in overall_soundscape; music in non_diegetic_music; never duplicate.
- Music (official §4.7): non_diegetic_music describes music that only the audience can hear — instruments, tempo, rhythm, dynamic changes; write "N/A" when there is none. Do not use abstract mood words, do not explain the score's emotional function, and do not score every shot: continuous background music buries the dialogue and makes every episode sound the same. Singing/instruments/radio audible to the characters are DIEGETIC and belong in the multimodal description instead.
- Soundscape discipline (the finished video must not sound noisy): stay at the LOW end of the official 1-4 sentence range — 1-2 short sentences describing a QUIET, low-level background bed (e.g. "distant street hum, faint wind") is usually right. Do not stack many simultaneous sound sources, do not ask for loud machinery, crowd chatter, alarms or continuous heavy noise: constant loud ambience buries the dialogue and reads as hiss in the final mix. Silence or near-silence is a perfectly good choice where it fits.
- Screen-visible text must be quoted exactly and kept in original language.`

// sysCastRules 选角差异化(2026-09-11 用户定调「不同角色脸不能相似」):
// 实证 LLM 写了 fat/scar/narrow face 等差异化特征,但 image_prompt 以巨型
// 统一风格前缀开头、身份特征挤尾部,Krea-2 渲染时全部丢弃(肥胖→清瘦、
// 疤→无疤,三张脸收敛成同一款标准帅哥)。规则:身份特征前置主导渲染,
// 全剧角色脸型/年龄/体格/记号互不相同,原文写明的外貌禁止美化抹平。
const sysCastRules = `## CAST DISTINCTNESS (critical - characters of the same series must NEVER look alike)
- Every named character gets a UNIQUE physical identity. For people and Q-version figures: no two may share the same face template — vary face shape (round / square / long / narrow / heart), age (child / teen / young adult / middle-aged / elderly), build (slim / average / stocky / burly), skin tone, hairstyle & hair color, and give each at least ONE signature marker (scar, mole, freckles, glasses, beard, tooth gap, dyed streak, bandage, tattoo...). For non-human kinds differentiate the thing itself instead: an artefact by shape, material, colour and part count; a creature by species, size class and coat or scale pattern; a bodiless entity by form, colour and glow — two artefacts must never be interchangeable, and no artefact may be given a human face to make it recognisable.
- image_prompt STRUCTURE (identity first): open with this character's distinctive physical identity (age, face shape, build, hair, eyes, skin, signature marker), then costume, then "in [COPY the global style EXACTLY]", end with "plain background" ONLY (never a crop / camera-angle phrase — the renderer adds the framing). Do NOT open the portrait prompt with style words — identity traits must LEAD the prompt so they dominate the render. **Never describe where the character is looking or a head turn either** — a reference sheet is always a straight-on front view: identity traits only, never which way they face (the renderer fixes the framing; 2026-09-27 实测: 剥掉卡里的正面约束后模型画出了四分之三侧脸).
- HAIR MUST BE UNAMBIGUOUS — three things, every time (2026-09-25 事故): the **TIE** (low ponytail / high ponytail / bun / loose), the **LENGTH** (to the jaw / shoulder / waist), and the **FOREHEAD STATE** (combed back and clear, or exactly how many wisps fall where). Never write just "ponytail", "hair tied up" or "short hair": an unstated forehead is what the image model fills in with invented strands over the face, and the video model then amplifies those strands into long flowing hair — the character's hair visibly changes from one shot to the next. If the design does cover the forehead, say how; if it does not, write "combed back, forehead clear, no strands over the face". Put the same three facts in the character's "description" field so both the sheet and every shot prompt agree.
- If the source states a character's appearance (age, build, hair color, scars...), follow it EXACTLY; never beautify or average it away. A fat character stays fat, an old character stays old, a scarred character keeps the scar.
- **A character sheet may be supplied with the source** (人物外形锚表, under 设定集). When it is present, EVERY character's appearance must be copied from it field by field — face shape, brows and eyes, nose and lips, skin, the signature marker, and the three hair facts (tie / length / forehead state). Never invent a different look for a character the sheet already describes, never beautify it away, and never merge two characters' traits. If the source mentions a character the sheet does not cover, design them by the same rules and keep them clearly unlike every other character.
- Faces are ORIGINAL DESIGNS (avoid infringing anyone's likeness): never model a character on a real person, actor, singer, celebrity or public figure, never name a character after one, and never write "looks like <real person>" in any field.`

// sysSceneRules 场景卡 image_prompt 的写作规则。
//
// 【为什么要有(2026-09-27 补的缺口)】角色卡有一整套规则(sysCastRules:身份前置、
// 发型三要素、原创底线、取景交给渲染器),场景卡却只有 schema 里一句
// "ENGLISH establishing-shot image prompt for this location" —— 场景图长什么样全看
// LLM 当天怎么写。实测《求你们骂我》的场景卡里写着
// "a scattered crowd of villagers in grey and brown cloth",而平台出场景图时按 KindScene
// 的取景纪律追加 "completely deserted and unpeopled"(场景图必须是**空场**:画面里的人
// 一律由角色定妆照负责) —— 同一段提示词里两套要求,模型折中,场景板上就站了人。
//
// 三条:①只写地点本身;②**不写人**(空场是硬要求);③取景/画幅由渲染器加。
// sysDynamicShotRules 分镜必须**显式解除**角色卡上的静态约束（技能侧《平台侧生成规范》§6.9）。
//
// 【为什么单列一段】角色卡与三视图板按规范要求"**无飘动、无动态动作**"（卡是静态图，飘动的衣料
// 会让模型以为在拍视频），而**分镜恰恰相反**：衣料要飘、人物要动、卡上那句"静态微光/静态流光"
// 在视频里可以流动起来。两边的约束方向相反，而先前平台的分镜提示词**一句都没提** ——
// 模型会从参考图（静态卡）里继承那份"站定不动"的气质，成片就是一屋子人僵着说话。
// 这条影响的是**每一镜的画面**，属于"看不见但看得见"的那类默认值：不写出来就自动站错边。
const sysDynamicShotRules = `## THE SHOT MUST MOVE (the reference cards are intentionally static - lift that here)

- The character sheets and turnaround boards are deliberately INERT: arms hanging naturally, no flutter, no dynamic action. **A shot is the opposite** — the cards exist to fix identity, not to fix posture. In every shot: cloth must hang, sway or flutter with the movement, hair and hems react, and people carry real body motion with amplitude and speed.
- Any "static shimmer / static glow" wording on a card becomes ANIMATED here (light pulses, holograms flicker, runes drift) unless the shot says otherwise.
- Do NOT copy a card's neutral studio pose into a shot: if a shot's action is walking, turning, kneeling or fighting, write the pose the action needs. The only thing the cards lock is the FACE, hair and costume design.
- Keep the camera free: the shot's framing is decided by the shot (size, viewpoint, camera move), never inherited from the reference plate.

## TWO THINGS THAT ALWAYS BREAK (learned from a real 48-shot episode, 2026-09-28)

**① HANDS + TOOLS — never frame them close, never write finger-level action.**
Real damage report: of 48 rendered shots, the four lowest-scoring ones all failed the same way — "fingers fused into one mass, wrong finger count, the hand welded to the blade". Every one of them was a 特写/近景 of a hand working a tool. The model cannot resolve which finger does what, so it melts them together.
- Tool work (carving a stele, chiselling, whittling, writing, threading, tying a knot) must be framed at **中景 or wider** — the hands stay small in the frame. Never park a 特写/近景 on a hand holding a tool.
- Describe the motion at the **arm / wrist / shoulder** level ("he leans in and drives his right wrist down the stone"), NEVER at finger level ("he lifts the final stroke out of the groove with the tip of his index finger"). Do not name individual fingers, knuckles or degrees of grip, and do not describe how a tool is held.
- If the story needs the RESULT of the work (a stroke opened, a character cut), show it as a change in the STONE or the surface, held in a medium shot, not as the fingers doing it.

**② READABLE CHARACTERS ON SKIN, CLOTH OR PROPS — do not ask for them.**
The same episode produced AI pseudo-hanzi on a forehead brand and on stele faces ("丙十七" came out as invented glyphs), because the character sheet said the brand "reads 丙十七".
- A scar, brand, tattoo or embroidery is a **worn, illegible mark**: "a pale ragged brand mark, the characters long since worn smooth". Never write that such a mark "reads X", and never ask for specific strokes to be cut or changed.
- A surface the audience genuinely must read (one stele inscription the plot turns on) is quoted **once, verbatim, inside English double quotation marks** exactly as the ON-SCREEN TEXT rule requires. Every other stele face, plaque, book page and banner stays **blank, weathered or out of focus** — say so explicitly ("the other stele faces are wind-worn and blank").
- Never describe a tool "opening a stroke out of a character": that is finger-level action AND legible text at once — the single worst combination the model can be handed.

**③ LIGHT: name the source.** Every shot states where the light comes from (a lamp, a shaft, the moon, firelight) and keeps the subject's face readably lit. Do not write "night" and leave the frame to go black: a real 48-shot episode had 18 shots whose sampled frames were over 45% near-black. Darkness is fine as a MOOD, never as the absence of a visible source.`

// sysSceneRules 场景卡规则。
const sysSceneRules = `## SCENE CARDS (the location reference plates)
- Every location that appears in any shot gets ONE scene card. Its image_prompt describes **the place itself and nothing else**: spatial layout and de
- **EXTRACT IMPLICIT LOCATIONS TOO (not just named places)**: a card is required whenever the text lets the audience sense "where this is / what surrounds them / what the weather and light are" — even when the place has no name. Treat any of these as a location: the subject stands or moves inside a specific environment ("he walks across the stone bridge"); the passage describes space, terrain, architecture, weather or atmosphere systematically ("mist crawling over the waste", "the last light on a broken wall"); an action, a gaze or a state implies the surroundings ("he looks up at the sky"); someone crosses or passes through a space ("she pushes the door open"); a memory or inner thought carries a place image ("she remembers the locust tree at home"). **Missing a place means every shot set there renders with no reference plate.**pth, architecture and building materials, ground surface, fixed props, the direction and quality of the light, time of day and weather.
- **Never put people in a scene card** — no crowd, no passers-by, no vendors, no silhouettes, no "a scattered crowd of villagers". The scene plate is an EMPTY stage: every person in the finished video comes from a character sheet, so anyone drawn into the scene plate would be reused as an anonymous stranger in every shot at that location and would fight the cast's identity lock.
- **EVENT ISOLATION (mandatory)**: the plate shows the location **as it is BEFORE anything happens there** — its timeless state. FORBIDDEN: blood, corpses, fresh fight damage, freshly broken objects, burning embers, footprints, dropped props, quest items, anything a character carries. ALLOWED: weathering, old rust, moss, long cracks, faded paint, settled dust — the marks of time, not of an event. Why it matters: the plate is fed to every shot at that location, so a scene rendered with the aftermath already in it makes the whole episode look like it happens after the event.
  Put this string VERBATIM in image_prompt: "pre-event timeless location, no blood, no corpses, no fresh aftermath, no dropped props, no event aftermath".
  **ALSO put the establishing string VERBATIM in the scene image_prompt** — "ultra-wide angle, grand scale, epic wide shot, vast space, distant horizon, cinematic establishing shot" (official §4.4: the scene plate IS the establishing view; a plate without it renders as a corner, not a location, and every shot set there inherits that poverty). Measured on a real episode: **0 of 4 scene cards carried it**, while the shots were required to — the requirement had never been stated for the card itself.
- **PROP BUDGET**: name **2-4 groups** of FIXED furnishings, architectural parts or natural attachments (foreground and midground combined) — a table, a railing, a well, a wall niche, a dead tree. Never list story props or character-held objects: they belong to the prop cards and to the shot description. Fewer than two groups reads as an empty modelling shell; more than four turns the plate into a prop inventory and the model draws every single one. Give **foreground 1-2 groups and midground 1-2 groups** (the far layer is not required), each with a short material + light note, and describe same-kind objects as a GROUP — never item by item.
- **FIVE STEPS (the difference between a crude plate and a film plate)**: build every scene prompt in this order, in English prose — ① key light and mood (not "daytime" but the quality of the light: a dead gold dusk, the cold blue hour before dawn, visible god rays); ② the core subject and background as precise nouns (glazed eaves, flying-apsara motifs, a blossoming peach tree, a rusted bronze altar); ③ **how the light touches the material** (this is what turns "pretty" into "alive"); ④ the render and quality suffix that matches the project style; ⑤ 2-4 groups of fixed furnishings. A plate that only does ① and ② is the "bare and monotonous scene" the reviewer keeps rejecting.
- **FOUR ELEMENTS (all four, never fewer)** environment type + specific time + spatial mood + main visual features. The main picture must carry all three depth layers (foreground / midground / background) and include **at least 2 of these five**: sky, horizon, distant mountains, large architecture, open terrain. A plate with only one of them reads as a corner, not a location.
- **WORTH LOOKING AT (the difference between "nice" and "alive")**: write the light as emotion, not as a timestamp — dawn's dead gold, the cold blue hour before sunrise, visible god rays through dust. State **how the light travels across the material** (light pooling on glazed tile, dusk running down rough stone, cold light talking to wet moss). Write colour poetically and specifically (faded vermilion + rusted celadon + cold grey-blue) and REQUIRED phrasing for the light-vs-shadow is an **A-versus-B** construction ("a warm golden lattice gently merging with warm grey shadow"). Add 2-4 strong mood words, one or two live atmosphere details (drifting leaves, rising mist, a guttering candle, suspended dust), and keep it clean and film-set tidy: designed space, no litter, no clutter, no broken junk. Prefer side light / back light / volumetric light; avoid flat top light.
- **ALLOWED decay (the reverse whitelist)**: weathering, old rust, moss, old cracks, fading, settled dust, worn edges — the timeless wear of a place is fine and welcome. What is forbidden is only the **event aftermath** (the previous bullet).
- **CONTINUITY LANDMARKS**: name the one or two fixed things whose screen position must stay the same every time we return to this location (a door frame, a central island, a mooring post, a specific tree). The audience re-identifies a place by them — if they move between shots, the space falls apart.
- **LIGHT STATE**: state the time of day and the main light direction for this location (which side the key light comes from). Adjacent shots at the same location must not jump from noon to dusk.
- Write it as a plain establishing view of the empty location, in the same art style as the series. Do not write framing, crop, camera angle or aspect ratio — the renderer adds those (per the project's aspect ratio), exactly as it does for character cards.`

// animeFaceLock 角色画风锁(注入到用户附加要求最前面)。
//
// 三个"必须"对应三件不同的事:①`style` 字段与每张 image_prompt、每镜 H3 提示词
// 用同一种画风(否则定妆照与视频又是两套);②脸必须是**理想化的半风格化电影脸**
// (结构与五官可读、皮肤有真实质感但不油不亮,不是 2D 平涂卡通、也不是真人照片);
// ③原创设计,不影射任何真实人物 —— 这一条是避免侵权的落点,必须写在分镜层,
// 不能只靠画图模型自觉。
//
// **口径迁移(2026-09-27 用户报障)**:原话是"必须是 2D/2.5D donghua 动画风格
// (clean line art, cel-shaded flat colour, matte powdery skin)" —— 这条**明文要求 LLM
// 写 2D 漫画风**,是"平台端小说 LLM 解析渲染出来是 2D 漫画风格"的直接原因;
// 而技能侧《立项.json》要的是 photoreal 3D + 国风电影,两套打架。用户 2026-09-26 规则
// 「一切创作产物的电影级画面风格必须是国风系」→ 统一到国风电影级 3D 写实 + 半风格化脸。
// 变量名保留(调用点与测试都在用),语义已从"动画脸锁"变成"电影脸锁"。
const animeFaceLock = `CHARACTER STYLE LOCK (MANDATORY, highest priority):
- The 'style' field, EVERY character image_prompt and EVERY shot h3_prompt must use the SAME cinematic GUOFENG (Chinese period) art style: a photoreal 3D surface with IDEALISED, SEMI-STYLISED faces, real materials with visible craft, soft motivated key light, matte finish, natural film grain and shallow depth of field. It is a Chinese period film — not a western fantasy, not cyberpunk neon, not japanese anime.
- Faces are idealised and semi-stylised: a clean graphic facial structure with simplified, readable features, rendered with real skin texture and fine grain. Never write a flat cel-shaded cartoon or hand-drawn manga/anime look, and never write a photorealistic live-action actor, a photographic face, glossy or oily skin, wet highlights, or a plastic/wax finish.
- Every face is an ORIGINAL design: never describe a character as looking like a real person, actor, singer, celebrity or public figure, and never use such a name as a character name.`

// SystemPrompt 单轮直出(完整 schema + 全部规则)。
//
// 是 var 不是 const:分镜规则段由一个值 = renderShotRules()(见 sysShotRules),
// 时长数字全部从常量渲染 —— 常量对了、文案写死照样会漂,所以文案本身也得是算出来的。
// sysFaceVocabRules 五官词表 + 敏感词安全替代（技能侧《平台侧生成规范》§3.2/§2.4，**逐字**吸收）。
//
// 【为什么要单列一段】这两块都是"**改一个词就等于换一个词表**"的东西：
//
//	① 五官词表：模型侧被训练过的是**具体类型名**（鹅蛋脸/丹凤眼/远山眉…）。平台此前只要求
//	   "写清脸型眼型"，LLM 于是爱写 `large bright eyes`、`精致五官`、`绝美` 这类**尺寸词与抽象词**
//	   —— 那正是"全剧一张模板脸"的根源（同一天还报过"周廷和主角脸相似"）。词表把"挑一个具体类型"
//	   变成硬要求，且**同类角色不许都用同一种**。
//	② 敏感词禁止表：赤足/深V/低胸/超模身材/大眼长腿/半透/毛孔可见… 云端会拦，本地也照写
//	   —— 免得换后端才发现。规范给了**安全替代写法**（高领/小立领、立体剪裁、写成头身比而不是"大长腿"），
//	   照抄即可，不是"自己发明一套更保守的说法"。
const sysFaceVocabRules = `## FACE VOCABULARY & SAFE WORDING (mandatory — these are controlled lists)

### Face features: pick a SPECIFIC type name, never a size word or an abstract compliment
- FACE SHAPE: 鹅蛋脸 / 瓜子脸(下巴微尖不过分) / 圆润苹果脸 / 方中带圆脸(柔和方颌) / 长菱形脸(颧骨微宽下颌收窄) / 心形脸 / 瘦长脸. Bone structure must read: defined but not hard, moderate cheekbone and jaw presence — NEVER an inflated face or a cone chin (锥子下巴).
- BROWS: 远山眉 / 柳叶眉 (清冷疏离) · 剑眉 / 水弯眉 (英气干练) · 秋波眉 / 挑眉 (妩媚) · 野生平眉 (中性少年感). Hair-by-hair texture must be visible; never one identical brow for everyone.
- EYES: 丹凤眼 / 桃花眼 / 杏眼 / 瑞凤眼 / 狐狸眼 / 下垂眼 / 柳叶眼 / 细长单眼皮 — plus the gaze/expression that matches the character's temperament (清冷/凌厉/温柔/狡黠/迷茫/坚毅/空洞).
- NOSE: 高挺直鼻 / 微驼峰鼻 / 小巧翘鼻 / 鼻头圆润带肉感 / 希腊鼻 / 鼻梁低平. NEVER a cone or over-narrow nose.
- LIPS: 薄唇轻抿 / 厚唇微翘 / M 唇 / 嘴角平直或下垂 / 嘴角一边习惯性上挑.
- BANNED as feature words: "large eyes", "big bright eyes", "绝美", "精致五官", "眉目如画", "完美脸型", "supermodel face" and any pure size adjective. Saying "精致五官" tells the model NOTHING and pushes every character toward one template.
- DIFFERENTIATE (critical): face shape / brows / eyes / nose / lips must be chosen per character (age, temperament, role) — characters of the same kind must NEVER all get the same combination.

### Banned words and their safe replacements (verbatim from 人物道具提取指令)
- 赤足/光脚/barefoot → the character MUST wear shoes: "cloth shoes", mud-caked hem; never bare feet.
- 深V/低胸/露乳/low neckline → collar wording only: high collar / small stand collar / layered lapels / 一字领(非低胸). Never any chest-revealing design.
- 宽腰封/紧束/勒紧 → 立体剪裁贴合身形 / 衣料垂坠自然收窄 (tailoring, not tightness); if a belt is needed it is at most a palm-wide thin cord or chain — preferably no belt at all.
- 半透/透视装/sheer → opaque matte fabric with a woven texture (state explicitly that it is NOT see-through).
- 超模身材/大长腿/legs 60% of height → express as proportions instead: "**eight and a half heads tall**, lean,
  straight-backed, shoulders about two head-widths".
  【口径来源，2026-09-28 更正】用户《二、人物道具提取指令》写"九头身"、《【额外】人物道具场景提取》写"身材比例 8.5 头身"
  —— 两处都是**用户定的口径**。平台此前写 7.5 是**我自己**在用户报"比例太丑"时拍的，把"丑"误当成"头身数太高"：
  真正的病根是**比例失真**（Q 版比例混进真人卡、取景与全身比例打架），那两条现在各有专门的自检与锚点。
  头身数回到用户文件的口径；若哪天用户说"就是要 7.5"，改这里并注明是用户拍板。
- 毛孔可见/汗毛/visible pores → matte skin with a soft natural finish (never realistic pores).
- 血管可见/visible veins → drop it; if needed: faint blue veins at the temple, subtle.
- Also banned: 性感暴露, 沟, 翘臀/蜜桃臀, 巨乳, 网衣/链式衣饰/蛛网衣, 大面积镂空, 暗示性动作, 不雅姿态, 低俗图案.
- Costume quality (keeps the design rich without exposure): 立体剪裁(流畅衣身线条、侧身拼接、内藏式收省)、材质对撞至少 4 种高级材质、外层自然过渡到腰胯；女装必须有内搭。
- Make-up and pupils: make-up is style-adaptive — period xianxia/fantasy uses an upturned eyeliner tail, huadian forehead mark, bitten-lip colour and a brow ornament; modern/urban uses earth-tone eyeshadow, a very fine liner, natural lip colour and a clean base. Pupil colour is one of 深黑 / 浅褐 / 琥珀 / 灰蓝 / 血红, and the GAZE must match the temperament (清冷 / 凌厉 / 温柔 / 狡黠 / 迷茫 / 坚毅 / 空洞) — a character with the wrong gaze reads as the wrong person.

### Character quality bar (verbatim essentials; the style-related parts follow the project style string)
- SKIN: cold porcelain or healthy wheat, luminous like jade — **plastic or silicone skin is forbidden**. Use "matte skin with a soft natural finish".
- HAIR: layered, smooth and full, **individual strands readable** (毛流感清晰), and the parting plus strand direction must stay readable.
- COSTUME: sculpted tailoring (flowing body lines, side panel seams, hidden darts), shoulders flowing naturally into the waist and hips, a waistline that follows real human proportions, and the natural slimness of well-cut clothing. Women's wear MUST have an inner layer. Mix **at least 4 premium materials**. Collar wording stays safe: high collar / small stand collar / shirt collar / 一字领(非低胸). No belt wider than a palm (prefer none), and the character MUST wear shoes.
- POSE ON A CARD (static image): full-body standing, shoulders relaxed and level, hands folded in front or hanging at the sides, body turned a natural 3/4 toward the camera, chin slightly raised, robe hanging naturally — **no flutter, no dynamic action** (shots lift this, see THE SHOT MUST MOVE).
- LIGHT on a card: even soft frontal light plus a fine rim / side-back light — **no harsh shadows**.
- QUALITY WORDS: next-gen high-poly model, ray tracing, 8K, extreme detail (matched to the project style string).
- Mood words: 1-2 at most, and they must fit the style.

### Anchor sheet is the ONLY authority for a face (卡从表来)
- The character sheet under 设定集 (人物外形锚表) outranks everything else: copy its fields **item by item**, add no adjectives of your own, and never reword them. Its 11 fields are: 年龄 / 身形 / 脸型 / 眉眼 / 鼻唇 / 肤色肤质 / 独特记号 / 发型三要素(束法+长度+额前状态) / 首套服装 / 气质 (plus name and role). A purely background character may carry only four: 年龄 / 身形 / 脸型 / 首套服装.
- Changing the anchor sheet = changing the face: if you must differ, say so explicitly in the description; never silently drift.
- **SAME PERSON AT ANOTHER TIME (造型档 / 不同时期) — the split line is fixed** (用户 2026-09-28：「主角不同时期会存在差异，但相似度不能走样」):
  · **INVARIANT (must survive every period)**: face shape and bone structure, brow shape, eye shape and pupil colour, nose and lip shape, skin tone family, height/build family, AND every signature mark (scar, brand, mole, missing tooth). These are what tells the audience "this is still him" — if a later card loses them, the character has silently been re-cast.
  · **VARIABLE (must actually change with the period)**: age read (a boy's soft jaw vs a man's defined one — same bone structure, different maturity), hair length/tie/state (shaved, grown out, greying, wet), costume and its wear, posture, and the body's condition (wounds, gauntness, muscle).
  · Write BOTH halves into the card: the IDENTITY LOCK carries the invariants, and the description states the period ("thirty-one, three years in the stone camp") so the change is deliberate rather than accidental.
  · A variant card is NOT a copy: if the new period only differs by costume, say so and keep the invariant list identical word for word. Never let two periods of one person render as the same image, and never let them render as two different people.`

var SystemPrompt = sysHeader + `

` + sysOutputFull + `

` + sysShotRules + `

` + sysViewRules + `

` + sysContinuityRules + `

` + sysShotTableRules + `

` + sysDynamicShotRules + `

` + sysHighlightRules + `

` + sysBlockingRules + `

` + sysH3Format + `

` + sysHard + `

` + sysCastRules + `

` + sysFaceVocabRules + `

` + sysPropRules + `

` + sysSceneRules

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
	// ShotLimit 镜头数**安全网上限**(设置页 split.shot_limit;0 = 用 project.MaxShotsCeiling)。
	//
	// 用途只有一个:内容驱动(不指定镜头数)时,LLM 失控吐出几百个镜头要有个闸。
	// 此前这个设置项只有界面、没有读取点(审计 P1-1),而代码里写死 MaxShotsCeiling,
	// 于是"我把上限调到 30"这句话在系统里完全没有效果 —— 典型的假开关。
	ShotLimit int
	// OnBatch 分批生成进度(批 i/n,负责镜号区间);仅分批路径触发。
	OnBatch func(batch, total, fromShot, toShot int)
	// OnNote 非致命提示(降级/兜底时用)。用于把"方案不完整但我们继续了"这类
	// 事实说给用户听 —— 静默降级等于骗人,直接报错又会让整条产线停在一集上。
	OnNote func(format string, args ...any)
}

// segment 分批切分计划(轮1 产物,不落盘)。
type segment struct {
	From  int    `json:"from"`
	To    int    `json:"to"`
	Brief string `json:"brief"`
}

// planHead 规划轮产物（"轻量头部"：剧名/角色/场景/道具/切分计划）。
//
// 【2026-09-28 实测抓到的静默丢字段】这个结构体原先**没有 Props** —— 而规划轮是唯一
// 产出道具卡的地方（分镜轮 batchShotOut 只回 shots）。于是模型在分镜里老老实实写了
// `prop_ids`（实测 35 镜里 19 镜带 `prop_zaodao / prop_shidao / prop_taijia / prop_gangzi`
// 这种真道具 id），道具卡却**在解码这一步被无声丢掉**，落盘方案里连 `props` 键都没有。
// 后果：契约的"prop_ids 悬空 ⇒ fail"必然触发，道具一张图也不会出 —— 用户看到的正是
// 「关键物品生成道具啊，前后镜都漂移了」。
//
// 教训（写进注释免得再犯）：**给提示词加字段时，必须同时给解码结构体加字段** ——
// 只改骨架文字，模型写的东西会在解析处消失，而且不报错。
type planHead struct {
	Title      string              `json:"title"`
	Logline    string              `json:"logline"`
	Style      string              `json:"style"`
	Characters []project.Character `json:"characters"`
	Scenes     []project.Scene     `json:"scenes"`
	Props      []project.Prop      `json:"props"`
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
		normalizePlan(plan, opt)
		return plan, nil
	}
	plan, err := generateSingle(ctx, client, sourceText, opt, contentDriven, secondsGiven)
	if err != nil {
		return nil, err
	}
	enforceStyleLock(plan, sourceText)
	normalizePlan(plan, opt)
	// **自纠一轮**（2026-09-28 实测驱动）：散文规则模型不照做 —— 真章节那一跑 35 镜
	// 全都还是一镜到底（提示词里明明写着"5 秒以上必须内部切 2-4 个小镜头"）。
	// 所以把机器能判定的违规**连同原文**回给它改一次，只改被点名的镜，改坏了退回原文。
	if n, note := RepairPlan(ctx, client, plan, opt); n > 0 && opt.OnNote != nil {
		opt.OnNote("%s", note)
	}
	return plan, nil
}

// normalizePlan 直出结果进契约之前的**最后一手归一化**（两条产出路径共用，只此一处）。
//
// 目前只做一件事：把"说话人是本方案角色、但不在本镜画面里"的台词标成画外音
// （`os=true`）—— 真跑实测 40 镜里有 5 镜是这种情况，全部报 fail，而**语义上本来就该是
// 画外音**（人在画外喊的那句）。判据与边界见 project.MarkOffscreenSpeakers 的说明：
// 只改这一种，画外音标签与"名字写错了"都不动。
func normalizePlan(plan *project.Plan, opt Options) {
	if plan == nil {
		return
	}
	if n := project.MarkOffscreenSpeakers(plan); n > 0 && opt.OnNote != nil {
		opt.OnNote("把 %d 条「说话人不在本镜画面里」的台词标成画外音（os=true）—— 语义上本来就是画外音，"+
			"标上之后契约不再因此 fail", n)
	}
	// 画外演出的**资产引用**由归一化补全（2026-09-28 实测：一次真跑里 35 镜的 fail 就是
	// 「#11 vo_kind=inner 但 chibi_ids 为空」——模型标了"这是内心镜"，却忘了写演出素材 id，
	// 契约于是 fail，用户看到的是"又说不能渲"）。口径是"字段标了不算，素材必须真在"，
	// 所以这里按该镜出场角色**确定性地**补出 Q版 / 系统素材 id：素材本来就会按角色自动生成
	// （见资产阶段的 Q 版生成），补 id 只是把双方对上号，不改任何创作内容。
	// 分镜里的器物引用**对号**（2026-09-28 实测：14 处 prop_ids 与卡片的 id 拼法不一致，
	// 锚定落空 = 那几镜的刀又变成模型自己编的）。id 拼法是机械约定，平台有名字与英文串，
	// 对号这件事它是唯一能确定做对的角色；对不上就丢引用，宁可不锚也不锚错。
	// **自愈三件**（器物对号 / 补台词标签 / 补画外素材）走同一个入口，
	// 与 project.LoadPlan、cmd/preflight 共用一份实现 —— 三个消费方必须看到同一份方案。
	if hr := project.HealPlan(plan); opt.OnNote != nil {
		if hr.PropRefs > 0 {
			opt.OnNote("把 %d 处分镜里的器物引用**对上了卡片**（模型写的 id 与卡片 id 拼法不一致，"+
				"对不上号的话那几镜的器物又会被重新发明）", hr.PropRefs)
		}
		if hr.Dialogue > 0 {
			opt.OnNote("给 %d 镜补上了缺失的台词标签（计划里有台词、正文里却没有 <d>）——"+
				"不补的话成片会把那几句说成听不懂的话", hr.Dialogue)
		}
		if hr.Voiceover > 0 {
			opt.OnNote("补全 %d 镜的画外演出素材引用（内心镜→该角色的 Q 版、系统镜→机器人＋面板）—— "+
				"模型漏写 id 会让契约 fail，但素材本身是按角色自动生成的，补上即对上号", hr.Voiceover)
		}
	}
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
	if err := validateAndNote(plan, opt, contentDriven); err != nil {
		return nil, err
	}
	return plan, nil
}

// validateAndNote 收口"截断必须说出来"这一条(两条生成路径共用)。
//
// 为什么要收口:镜头数上限截断会**丢掉方案结尾**(用户反复强调“不跳段、不省略”),
// 而旧实现是静默截断 —— 用户只看到成片短了,不知道原因,也没人告诉他上限在哪调。
// 收成一个函数,是为了让"新增生成路径"不可能再漏掉这句提示。
func validateAndNote(plan *project.Plan, opt Options, contentDriven bool) error {
	limit := shotLimitOf(opt, contentDriven)
	before := len(plan.Shots)
	if err := validate(plan, limit); err != nil {
		return err
	}
	if dropped := before - len(plan.Shots); dropped > 0 && opt.OnNote != nil {
		opt.OnNote("分镜被镜头数上限收窄:LLM 给出 %d 镜,按上限 %d 镜截断(丢了 %d 镜,方案结尾会短)。"+
			"上限在「设置 → 拆分」的「镜头数安全上限」里调;填 0 = 按内容自决(内置安全网 %d 镜)",
			before, limit, dropped, project.MaxShotsCeiling)
	}
	return nil
}

// shotLimitOf 本次生成的镜头数上限:内容驱动时是安全网(设置页 split.shot_limit,
// 未配置则 project.MaxShotsCeiling),指定镜头数时就是该值。
//
// 为什么上限要可配(审计 P1-1):这个开关此前只在设置页存在、没有任何读取点,
// 而"内容驱动"是默认模式 —— 也就是说绝大多数用户跑的分镜,上限永远是代码里那个
// 120,与界面上写的数字无关。现在它真的生效,并且**仍然**受 MaxShotsCeiling 保护:
// 设置里填一个更大的值不该让安全网失效(那正是安全网存在的意义)。
func shotLimitOf(opt Options, contentDriven bool) int {
	if contentDriven {
		return clampShotLimit(opt.ShotLimit)
	}
	return opt.MaxShots
}

// clampShotLimit 归一化来自设置的镜头上限:>0 且不超过 MaxShotsCeiling 才采用。
func clampShotLimit(n int) int {
	if n <= 0 {
		return project.MaxShotsCeiling
	}
	if n > project.MaxShotsCeiling {
		return project.MaxShotsCeiling
	}
	return n
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

// parsePlan 从 LLM 输出提取并解析 JSON(容错:剥围栏/前后杂文/截断补全/拆一层包装)。
// 错误信息带原文摘录:方案解析失败时,日志里能直接看出是"截断"还是"格式跑偏"。
func parsePlan(raw string) (*project.Plan, error) {
	obj, ok := extractJSONObject(raw)
	if !ok {
		if fixed, closed := closeTruncatedJSON(obj); closed {
			obj, ok = fixed, true
		}
	}
	if !ok || obj == "" {
		return nil, fmt.Errorf("输出中未找到 JSON 对象(原文 %d 字):%s", len([]rune(raw)), snippet(raw, 120))
	}
	s := repairJSONStrings(obj)
	decode := func(x string) (*project.Plan, error) {
		var plan project.Plan
		if err := json.Unmarshal([]byte(x), &plan); err != nil {
			return nil, err
		}
		if strings.TrimSpace(plan.Title) == "" && len(plan.Shots) == 0 {
			return nil, fmt.Errorf("JSON 合法但既无 title 也无 shots")
		}
		return &plan, nil
	}
	plan, firstErr := decode(s)
	if firstErr == nil {
		return plan, nil
	}
	for _, inner := range allObjects(s) {
		if inner == s {
			continue
		}
		if p, err := decode(repairJSONStrings(inner)); err == nil {
			return p, nil
		}
	}
	return nil, fmt.Errorf("JSON 解析失败: %v;原文摘录:%s", firstErr, snippet(raw, 120))
}

// validate 做最小硬校验并规整(id 连续、引用存在、时长钳制)。
//
// shotLimit 是**本次生成的镜头数上限**:内容驱动时传 project.MaxShotsCeiling
// (安全网,不是预设值),指定镜头数时传该值。
// 旧实现在这里写死 `> 20 就截断` —— 与 params.go 的 MaxShotsCeiling=120 和
// 「0=内容驱动、该多少就多少」的定调直接冲突:实测一集按原文节拍规划出 32 镜,
// 分批也照 32 镜全跑完了,末了被这行**静默砍成 20 镜** —— 既白花 3 轮 LLM 调用,
// 又正好违反用户反复强调的「不跳段、不省略」。
func validate(p *project.Plan, shotLimit int) error {
	if p.Title == "" {
		return fmt.Errorf("缺少剧名")
	}
	if len(p.Shots) == 0 {
		return fmt.Errorf("分镜为空")
	}
	if shotLimit > 0 && len(p.Shots) > shotLimit {
		p.Shots = p.Shots[:shotLimit]
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
		// 时长钳到**生成区间**(不是渲染窗口):下限是 GenMinShotSeconds(5),
		// 与云端 ≥5 秒对齐,避免计划与成片时长不一致(清单 §1.3)。
		// 4 秒镜从这里开始不再被产出 —— 那是本次整改的预期结果。
		if sh.Duration < GenMinShotSeconds {
			sh.Duration = GenMinShotSeconds
		}
		if sh.Duration > MaxShotSeconds {
			sh.Duration = MaxShotSeconds
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
    {"id":"char_xxx","name":"中文名","description":"中文外貌/服装/气质,固定不变的关键特征(含至少一个独特记号)","image_prompt":"ENGLISH portrait prompt. It MUST contain the literal block "IDENTITY LOCK (do not change): " followed by the distinctive physical identity (age, face shape, brows/eyes, nose/lips, skin, hair, signature mark, costume) AND **the body-proportion anchor "eight and a half heads tall"** — the renderer reads THAT block to anchor identity across shots and to tell characters apart; a card without it silently loses both. Then:  shape, build, hair, eyes, skin, signature marker] then [costume] then 'in [COPY the global style EXACTLY]' then 'plain background' ONLY (no framing or crop words — the renderer adds them)","voice":"ENGLISH voice description"}
  ],
  "scenes": [
    {"id":"scene_xxx","name":"中文名","description":"中文场景描述","image_prompt":"ENGLISH establishing-shot image prompt for this location, pre-event and unpeopled (see SCENE CARDS)","landmarks":"连续性地标:同场景跨镜屏幕位置必须不变的固定物(门框/中岛/系船桩/树)","light_state":"光态:日/夜 + 主光方向"}
  ],
  "props": [
    {"id":"prop_xxx","name":"中文名","en":"ENGLISH look phrase for the picture model","description":"中文外观/材质/配色/尺寸/记号","image_prompt":"ENGLISH 1:1 white-background single-object prompt (see PROP CARDS)"}
  ],
  "segments": [
    {"from":1,"to":3,"brief":"本段剧情要点(中文,≤160字):发生什么、关键台词原文、情绪转折、结局钩子"}
  ]
}

## SEGMENT RULES
- segments 的 from/to 是分镜镜号区间:从 1 开始连续覆盖到总镜数,不重叠不留空。
- 每段 3-6 镜;段与段之间是清晰的故事节拍(开场钩子/升级/转折/高潮/收尾)。
- brief 必须自足:仅凭 brief(不看全文)就能写好本段每一镜。关键台词必须原文引用。
- characters/scenes/props 覆盖全片所有出场者与关键器物;角色外貌描述具体可画,全片复用。` + "\n\n" + sysCastRules + "\n\n" + sysFaceVocabRules + "\n\n" + sysPropRules + "\n\n" + sysHighlightRules + `

` + sysSceneRules

// batchSystemPrompt 分镜轮:按已定头部与本段 brief,只产出指定区间的 shots。
// var 的理由同 SystemPrompt:内嵌的 sysShotRules 由常量渲染。
var batchSystemPrompt = sysHeader + `

## OUTPUT (THIS BATCH)
Return STRICT JSON only (no markdown fences, no commentary):
{"shots":[
  {"id":N,"scene":"scene_xxx","characters":["char_xxx"],"prop_ids":["prop_xxx"],"shot_size":"中景","camera":"slow push-in, small amplitude","view":"主视角,逐字取自官方 9 条词表(见 VIEWPOINT)","action":"中文一句话画面动作","dialogue":[{"speaker":"中文名","text":"台词"}],"narration":"","duration":6,"mode":"ref2va","hook_type":"官方钩子词表之一(见 SHOT TABLE)","end_state":"尾帧状态:最后一帧看得见的东西(位置/姿态/表情/视线/环境),禁写听觉与氛围词","h3_prompt":"FULL H3 prompt text"}
]}

` + sysShotRules + `

` + sysViewRules + `

` + sysContinuityRules + `

` + sysShotTableRules + `

` + sysDynamicShotRules + `

` + sysHighlightRules + `

` + sysBlockingRules + `

` + sysH3Format + `

` + sysHard + `

` + sysFaceVocabRules + `

` + sysPropRules + `

` + sysHighlightRules + `

## BATCH SCOPE
- Produce ONLY the shots whose ids are inside the given range; do NOT invent shots outside it.
- ids must be integers inside the range, sequential, no gaps.
- Story continuity: respect the PREVIOUS SHOTS summary if provided (do not repeat events; keep character states consistent).`

// ShotPrompts 产出 **shots[]** 的两条直出路径（单轮整体直出 / 分批分镜），供双侧同源测试取用。
//
// 为什么按"产出什么"分组：视角词表与尾帧状态的纪律必须出现在**写 shots 的那两条**路径里，
// 而场景卡纪律必须出现在**写 scenes 的那两条**里（见 ScenePrompts）—— 分成两组，守卫才能
// 各盯各的；混成一个 map 时，测试只能笼统要求"每条提示词都有全部规则"，那不是事实
// （分批分镜轮根本不产出场景卡），也就守不住真正的缺口。
func ShotPrompts() map[string]string {
	return map[string]string{
		"SystemPrompt(单轮直出)":      SystemPrompt,
		"batchSystemPrompt(分批分镜)": batchSystemPrompt,
	}
}

// ScenePrompts 产出 **scenes[]** 的两条路径（单轮整体直出 / 规划轮），供双侧同源测试取用。
//
// 2026-09-27 这条守卫抓到的真实缺口：规划轮（`planSystemPrompt`）是**真正写 scenes[] 的地方**，
// 却只带了角色卡规则、漏了 `sysSceneRules` —— 场景卡的事件隔离与物件配额规则从没进过那条路径。
func ScenePrompts() map[string]string {
	return map[string]string{
		"SystemPrompt(单轮直出)":    SystemPrompt,
		"planSystemPrompt(规划轮)": planSystemPrompt,
	}
}

// buildPlanUserPrompt 组装规划轮的用户提示词。
// 单独成函数是为了让"线上跑的那段提示词"和诊断用的那段**完全同源** ——
// 复现失败时最怕提示词与线上不一致(改一处漏一处,复现出来的是另一个问题)。
func buildPlanUserPrompt(sourceText string, opt Options, contentDriven bool) string {
	hint := styleHint(opt)
	if contentDriven {
		// 内容驱动时不预设总镜头数:由规划轮自己决定拍多少镜才 cover 完整,
		// 只保留「每段 3-N 镜」这个分批粒度 —— 它是技术参数(保证单次输出不溢出)。
		return fmt.Sprintf(`Source text (Chinese):
"""
%s
"""

Additional requirements:
- Target: short video. Decide the TOTAL shot count YOURSELF from the source text's natural story beats — there is NO upper limit; cover the story completely and clearly.
- Then split it into consecutive segments of 3-%d shots each, so the segments continuously cover shot ids 1..N where N is the total you decided.
%s
Produce the STRICT BATCH PLAN JSON now.`, sourceText, batchMaxShots, hint)
	}
	return fmt.Sprintf(`Source text (Chinese):
"""
%s
"""

Additional requirements:
- Target: short video, total %d shots, total duration about %d seconds.
- Split the story into segments of 3-%d shots each (segments must continuously cover shot ids 1..%d).
%s
Produce the STRICT BATCH PLAN JSON now.`, sourceText, opt.MaxShots, opt.TotalSeconds, batchMaxShots, opt.MaxShots, hint)
}

// decodePlanHead 直接解析并校验(错误信息按**缺什么**分别说明)。
func decodePlanHead(s string) (planHead, error) {
	var head planHead
	if err := json.Unmarshal([]byte(s), &head); err != nil {
		return head, fmt.Errorf("JSON 解析失败: %v", err)
	}
	if strings.TrimSpace(head.Title) == "" {
		return head, fmt.Errorf("JSON 合法但缺少 title")
	}
	if len(head.Segments) == 0 {
		return head, fmt.Errorf("JSON 合法但缺少 segments")
	}
	return head, nil
}

// parsePlanHead 解析规划轮输出(标题/角色/场景/分段):
// 提取 → 直解 → 补全截断 → 拆一层包装,四步都失败才报错。
// 第二个返回值表示"这份结果是靠补全截断尾巴得到的"(方案可能不完整),
// 调用方据此决定要不要重问一轮。
// 规划轮是 48 集产线的咽喉,一句笼统的"解析失败"没法修,所以每步都留清楚原因。
func parsePlanHead(raw string) (head planHead, truncated bool, err error) {
	obj, ok := extractJSONObject(raw)
	if !ok {
		if fixed, closed := closeTruncatedJSON(obj); closed {
			obj, ok, truncated = fixed, true, true
		}
	}
	if !ok || obj == "" {
		return planHead{}, false, fmt.Errorf("未找到 JSON 对象(原文 %d 字)", len([]rune(raw)))
	}
	s := repairJSONStrings(obj)
	h, firstErr := decodePlanHead(s)
	if firstErr == nil {
		return h, truncated, nil
	}
	// 包了一层({"plan":{…}} / {"result":{…}} / {"data":{…}})是常见写法,拆一层再试
	for _, inner := range allObjects(s) {
		if inner == s {
			continue
		}
		if h2, err2 := decodePlanHead(repairJSONStrings(inner)); err2 == nil {
			return h2, truncated, nil
		}
	}
	return planHead{}, truncated, firstErr
}

// callPlanRound 规划轮:调用 + 一次修复重问。
//
// 为什么要专门做一层(2026-09-24 用户 48 集产线卡在第 1 集):
//   - 失败几乎都是**格式**问题(被 max_tokens 截断、或先写一段说明再给 JSON),
//     不是模型不会规划 —— 把原文回灌要求"只补发合法 JSON"通常一次就救回来;
//   - 旧实现只回一句"规划轮输出解析失败(缺少 title/segments)",finish_reason、
//     token 用量、原文摘录全丢了,现场无法复盘。这里把它们全部写进错误。
func callPlanRound(ctx context.Context, client *llm.Client, sys, user string, note func(string, ...any)) (planHead, error) {
	res, err := client.ChatEx(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, true)
	if err != nil {
		return planHead{}, fmt.Errorf("规划轮失败: %w", err)
	}
	head, truncated, perr := parsePlanHead(res.Content)
	if perr == nil && !truncated {
		return head, nil
	}
	// 走到这里两种情形:① 解析失败;② 解析成功但靠的是"补全截断的尾巴"
	// —— 后者那份方案大概率不完整(后面的段会落到占位 brief),
	// 所以先花一轮把它问全;真问不回来再用它兜底,不让产线停在第 1 集。
	var fallback *planHead
	if perr == nil {
		h := head
		fallback = &h
	}

	// ---- 修复/补全重问 ----
	prev := res.Content
	if len([]rune(prev)) > 40000 {
		prev = snippet(prev, 20000)
	}
	shrink := ""
	if truncated || res.Finish == "length" {
		shrink = "\nYour previous reply was CUT OFF by the output limit. This time make the plan SHORTER: fewer segments (each up to 5 shots) and terse briefs — still covering the whole story."
	}
	why := "output ended before the JSON object was complete"
	if perr != nil {
		why = perr.Error()
	}
	fixUser := fmt.Sprintf(`Your previous reply could not be parsed as a complete JSON object: %s.
Return ONLY the JSON object — no markdown fences, no commentary, no text before or after — with exactly these top-level keys:
{"title":"…","logline":"…","style":"…","characters":[…],"scenes":[…],"segments":[{"from":1,"to":3,"brief":"…"}]}%s

Keep every id/name you already decided. Previous reply:
"""
%s
"""`, why, shrink, prev)
	if fix, ferr := client.ChatEx(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: fixUser},
	}, true); ferr == nil {
		if h2, trunc2, e2 := parsePlanHead(fix.Content); e2 == nil && !trunc2 {
			return h2, nil
		} else if e2 == nil {
			perr, res = nil, fix
			fallback = &h2
		} else {
			perr, res = e2, fix
		}
	}
	if fallback != nil {
		if note != nil {
			note("⚠ 规划轮输出疑似被截断,已用补全后的方案继续(建议本集跑完后回看分镜;如明显缺段,重跑「剧本分镜」即可)")
		}
		return *fallback, nil
	}

	whyStr := perr.Error()
	if res.Finish == "length" {
		whyStr += ";输出被 max_tokens 截断"
	}
	return planHead{}, fmt.Errorf(
		"规划轮输出解析失败(%s;finish=%s,completion=%d tokens,原文 %d 字)\n原文摘录:%s",
		whyStr, res.Finish, res.Usage.CompletionTokens, len([]rune(res.Content)), snippet(res.Content, 240))
}

// generateBatched 分批路径:轮1 规划(轻量)→ 轮 2..k 逐批出镜(每批 ≤5 镜,
// 单轮生成量小,推理模型压回 1-3 分钟)。
func generateBatched(ctx context.Context, client *llm.Client, sourceText string, opt Options, contentDriven bool) (*project.Plan, error) {
	hint := styleHint(opt)

	// ---- 轮 1:规划 ----
	// 内容驱动时不预设总镜头数:由规划轮自己决定拍多少镜才 cover 完整,只保留
	// 「每段 3-N 镜」这个分批粒度 —— 它是技术参数(保证单次输出不溢出),不是数量上限。
	head, err := callPlanRound(ctx, client, planSystemPrompt, buildPlanUserPrompt(sourceText, opt, contentDriven), opt.OnNote)
	if err != nil {
		return nil, err
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
		Characters: head.Characters, Scenes: head.Scenes, Props: head.Props,
	}

	// ---- 轮 2..k:逐批出镜 ----
	cards, _ := json.Marshal(map[string]any{"characters": head.Characters, "scenes": head.Scenes})
	for bi, seg := range head.Segments {
		if opt.OnBatch != nil {
			opt.OnBatch(bi+1, total, seg.From, seg.To)
		}
		segSeconds := opt.TotalSeconds * (seg.To - seg.From + 1) / opt.MaxShots
		if contentDriven {
			// 内容驱动下 opt.TotalSeconds/MaxShots 只是内部兜底(12 镜×7 秒),
			// 拿它换算分段时长会把"32 镜的一集"每段写成 10 秒 —— 提示词自相矛盾。
			// 按本文件一贯的 7 秒/镜估,才和实际方案对得上。
			segSeconds = (seg.To - seg.From + 1) * 7
		}
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
	if err := validateAndNote(plan, opt, contentDriven); err != nil {
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

// parseShotsBatch 解析一批 shots 并校验区间(同样支持截断补全与拆一层包装)。
func parseShotsBatch(raw string, from, to int) ([]project.Shot, error) {
	obj, ok := extractJSONObject(raw)
	if !ok {
		if fixed, closed := closeTruncatedJSON(obj); closed {
			obj, ok = fixed, true
		}
	}
	if !ok || obj == "" {
		return nil, fmt.Errorf("未找到 JSON 对象(原文 %d 字):%s", len([]rune(raw)), snippet(raw, 120))
	}
	s := repairJSONStrings(obj)
	decode := func(x string) ([]project.Shot, error) {
		var out batchShotOut
		if err := json.Unmarshal([]byte(x), &out); err != nil {
			return nil, err
		}
		if len(out.Shots) == 0 {
			return nil, fmt.Errorf("shots 为空")
		}
		return out.Shots, nil
	}
	parsed, firstErr := decode(s)
	if firstErr != nil {
		for _, inner := range allObjects(s) {
			if inner == s {
				continue
			}
			if sh, err := decode(repairJSONStrings(inner)); err == nil {
				parsed, firstErr = sh, nil
				break
			}
		}
	}
	if firstErr != nil {
		return nil, fmt.Errorf("JSON 解析失败: %v;原文摘录:%s", firstErr, snippet(raw, 120))
	}
	out := batchShotOut{Shots: parsed}
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

// trimJSONBlock 已由 jsonx.go 的 extractJSONObject/closeTruncatedJSON 取代
// (旧实现是"首个 { 到最后一个 }",模型在 JSON 前写一句带花括号的说明、或输出被
//  截断时会截出非法片段,且无法区分这两种情形)。保留此别名只为兼容历史调用点。

// sysHighlightRules 抓重点纪律（来源：用户《一、网文小说转剧本提示词指令》，
// 2026-09-28 用户：「分镜脚本 LLM 解析是否按这个来啊，抓重点啊，咋那么纯啊」）。
//
// 【为什么单列一段】这份文件是用户定的**改编方法论**，平台此前只在钩子类型/片尾钩上偶然对上，
// 最要命的四条（3 秒爆点、爽点密度、砍冗余、放大反差）一个字都没进提示词 ——
// 于是解析出来的分镜"忠实但平"：该炸的地方不炸，该砍的支线照单全收。
//
// 覆盖关系（用户文件 → 平台的机器门）：
//
//	· 开篇 3 秒无铺垫无旁白  → contract「#1 必须是强钩，且不许有旁白/画外音」
//	· 每 80 字小爽点 / 200 字大爽点 → contract「爽点密度」（按台词字数累计，超阈报警）
//	· 段段留钩 / 片尾终极钩  → contract「连续三镜钩子密度」「收尾钩」
//	· 砍冗余铺垫支线冗长对话 → 本段规则 + 分镜预算（信息密度优先，宁短勿平）
//	· 放大情绪反差 / 反转密集 → sysDynamicShotRules 与钩子密度规则
//	· 合规零风险 → sysHard 既有合规段
const sysHighlightRules = `## 抓重点纪律 (改编方法论 — 用户定的口径, 违反即返工)

这份文件把"网文 → 漫剧"的改编规则写死了。**忠实不是优点,平才是缺点** —— 观众划走只需要 3 秒。

1. **开篇 3 秒爆点(硬性)**: [Shot 1] 的第一句画面/动作必须是极致冲突、绝境、反转或悬念 ——
   绝境受辱、生死危机、惊天揭秘、逆袭打脸、天降机缘。**无任何铺垫、无多余旁白**,
   直接切进高潮名场面。慢热开场 = 整集白做。
2. **爽点密度(硬性)**: 每 80 字内容要有 1 个小爽点,每 200 字要有 1 个大爽点。
   主角全程不憋屈、不拖沓: 逆袭、打脸、揭秘、升阶的节奏要拉满。
   (平台按台词字数累计做机器门: 连续超过 200 字没有强节拍就报警。)
3. **段段留钩(硬性)**: 每一段(segment)结尾必须留悬念/伏笔/反转预告,杜绝平淡收尾。
4. **砍冗余(硬性)**: 砍掉所有冗余铺垫、无关支线、冗长对话。保留核心主线,快速推进。
   **判断标准: 这一镜不推进冲突、不抛信息、不给情绪,就该并进相邻镜或直接删。**
   宁可少一镜,不要多一镜水戏 —— 短视频里"平稳过渡"就是流失点。
5. **放大反差**: 主角的绝境/屈辱与逆袭/碾压要形成尖锐对比;冲突更狠、情绪更极致。
6. **反转密集不套路**: 每次冲突都要有意外反转;规避俗套桥段。
7. **人物**: 主角气场拉满、智商在线、行动果决 —— 不写懦弱犹豫、不写反复纠结;
   对立角色**标签化**(一眼认出他是谁、要什么)。
8. **补充关键场景/道具/伏笔**: 原文里一带而过但剧情要用的,要补成卡(场景/道具),不冗余。
9. **语言**: 短句为主、力道足、画面感强;重点剧情突出。
10. **片段内必须切镜(治拖沓, 硬性)**: 一个片段 5 秒以上就必须在**内部切 2-4 个小镜头**,
    每个小镜头 1.5-4 秒, 切点落在动作的自然停顿处(人物站定、转身完成、视线定格、台词间隙)。
    写法用官方多镜形式: 「[Shot 1] ...」然后「[Shot 2] At 00:03.500, ...」(时间戳从 0 开始**严格递增**,
    且各小镜头时长之和 = 该片段时长)。**第一刀最快**: 开场那一刀 1.5-2.5 秒就把信息砸出去。
    为什么: 平台实测每镜正文只有一个「[Shot 1]」—— 一个 6-8 秒片段从头到尾演一个连续动作,
    观众感觉就是"全程慢动作"; 用户自己的《仿真人10S分镜头指令》也写着"每片段内拆成若干小镜头,
    每镜 1.5-4 秒"。**切镜是节奏, 不是特效**: 同样的内容切三刀就是快, 一镜到底就是拖。
11. **重点配重(抓重点)**: 一集里挑出 2-3 个**大爽点**(逆袭/打脸/揭秘/升阶), 给它们**更多镜与更长的
    停留**; 其余过渡内容压到最省 —— 不要把镜数平均分给每一段, 平均就是没有重点。`
