

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
  "core_beats": [
    "本集核心冲突：一句话",
    "爽点①/②/③：谁在何时逆袭/打脸/揭秘/升阶（2-3 个，宁少勿凑）"
  ],
  "props": [
    {"id":"prop_xxx","name":"中文名","en":"ENGLISH look phrase for the picture model","description":"中文外观/材质/配色/尺寸/记号","image_prompt":"ENGLISH 1:1 white-background single-object prompt (see PROP CARDS)"}
  ],
  "core_beats": [
    "本集核心冲突：一句话（≤30字）",
    "爽点①：谁在什么时候逆袭/打脸/揭秘/升阶（≤30字，标类型）",
    "爽点②：同上",
    "爽点③：同上（2-3 个即可，宁少勿凑）"
  ],
  "segments": [
    {"from":1,"to":3,"brief":"本段剧情要点(中文,≤160字):发生什么、关键台词原文、情绪转折、结局钩子"}
  ]
}

## SEGMENT RULES
- segments 的 from/to 是分镜镜号区间:从 1 开始连续覆盖到总镜数,不重叠不留空。
- 每段 3-6 镜;段与段之间是清晰的故事节拍(开场钩子/升级/转折/高潮/收尾)。
- **segments 必须围绕 core_beats 组织**：每个大爽点要有对应段落，铺垫与过渡段能省则省（用户口径：抓重点，不照搬原文）。
- brief 必须自足:仅凭 brief(不看全文)就能写好本段每一镜。关键台词必须原文引用。
- characters/scenes/props 覆盖全片所有出场者与关键器物;角色外貌描述具体可画,全片复用。