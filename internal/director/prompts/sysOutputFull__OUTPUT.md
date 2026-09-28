## OUTPUT
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
  "core_beats": [
    "本集核心冲突：一句话",
    "爽点①/②/③：谁在何时逆袭/打脸/揭秘/升阶（2-3 个，宁少勿凑）"
  ],
  "props": [
    {"id":"prop_xxx","name":"中文名","en":"ENGLISH look phrase for the picture model","description":"中文外观/材质/配色/尺寸/记号","image_prompt":"ENGLISH 1:1 white-background single-object prompt (see PROP CARDS)"}
  ],
  "shots": [
    {"id":1,"scene":"scene_inn","characters":["char_xiaofan"],"prop_ids":["prop_xxx"],"positions":{"char_xxx":"画面左三分之一、前景、面朝右"},"offscreen":{"char_yyy":"上一镜在场、本镜已出画，走向门洞（本镜保留一行）"},"shot_size":"中景","camera":"slow push-in, small amplitude","view":"主视角,逐字取自官方 9 条词表(见 VIEWPOINT)","action":"中文一句话画面动作","dialogue":[{"speaker":"小凡","text":"你怎么也来这里了?"}],"narration":"","duration":6,"mode":"ref2va","hook_type":"官方钩子词表之一(见 SHOT TABLE)","end_state":"尾帧状态:这一镜最后一帧看得见的东西(位置/姿态/表情/视线/环境),禁写听觉与氛围词","h3_prompt":"FULL H3 prompt text as defined below"}
  ]
}