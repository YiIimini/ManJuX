package pipeline

import "strings"

// entity_prompts.go 六类实体各自的渲染方法**内容**(纯文案,与判定逻辑分开放:
// entity.go 是分类与分发,本文件是每一类具体怎么渲染)。
//
// 改这里的任何一句都要先想清楚"它会不会被模型当成画面元素画出来":
//   - 定妆照走 Krea-2(蒸馏模型,CFG 1.0)——**只许写它是什么**,否定词会把名词画出来;
//   - 转身/渲染走 H3 —— 官方指南允许散文式排除句,才写 NEGATIVE。

// entityProfile 一套完整的类型渲染方法。
type entityProfile struct {
	Label string
	Hint  string

	// ---- ① 定妆照(Krea-2)----
	SheetFraming  string // 取景/构图尾巴(会**替换** LLM 写的人物取景)
	SheetIdentity string // 身份忠实度(要抄什么)
	SheetLook     string // 画风/质感正向块
	// SheetProportions 平台侧**自带的比例措辞**(目前只有 Q 版的"二到三头身")。
	//
	// 为什么单独一个字段而不是混在 SheetLook 里(技能侧清单 §5.1):卡里已经带技能侧的
	// 人体比例句时(七个半头),平台侧这句必须**让位** —— 同一段提示词里两套比例,
	// 模型只会取中值,画出来既不是 Q 版也不是真人比例。混在介质块里就没法单独让位,
	// 而整块丢掉会连介质(线稿/平涂/哑光)一起丢。判据见 proportion_clause.go。
	SheetProportions string
	// SheetMarker 该类型忠实度块的唯一标记串(用于"已经带过就不再叠")。
	SheetMarker string
	// RewriteSheetFraming 是否重写取景尾巴。
	// 人**不重写**:LLM 写的 "plain background, upper-body framing, front-facing" 本来就对,
	// 重写只会白白改动提示词;其余类型必须重写 —— 那正是"铜雀被画成半身人像"的根因。
	RewriteSheetFraming bool

	// ---- ② 多视角(H3 转身视频)----
	TurnSubject   string   // <Subject 1> 的身份表述
	TurnSummary   string   // summary 段
	TurnRetention []string // retention_analysis 逐行
	TurnDetail    string   // detailed_description 首段
	TurnShots     [4]string
	TurnNegative  string
	TurnSound     string // overall_soundscape

	// ---- ③ 镜头锚定与画风(H3 渲染)----
	AnchorKind    string // 称呼:人物 / 器物 / 灵宠 / 妖兽 / 灵体 / Q 版形象
	AnchorRefNote string // "参考图里出现的就是它本体"的说明
	AnchorLock    string // 身份锁
	AnchorUseFor  string // 文字描述只许用于哪些方面
	RenderLook    string // 渲染侧质感条款(接在通用介质块后)
	AntiForm      string // 反错误形态条款(不得拟人化/不得实体化/不得缩水)

	// ---- ④ 多视角身份校验(VLM)----
	JudgePoints string
}

// entityProfiles 六类渲染方法总表。
//
// 人类那条是 2026-09-12 那轮重写的定稿(用户报「定妆照与多位图角色不一致」
// 「多位图有小红鼻小红脸」),三条不能回退的结论:
//
//	① 身份完全交给参考图:旧版把定妆照的文字描述塞进 subject_definitions,描述里的细节
//	   (如"圆钝的小红鼻子")与画面冲突时模型按文字补细节 —— 转台图就长了定妆照没有的红鼻子;
//	② "character turnaround reference"这个说法在训练数据里大量对应**角色设定图**,模型会
//	   自作主张换成 Q 版贴纸体型(实测:成年胖脸差役第二帧起变成红脸蛋大头娃娃),所以
//	   必须显式禁止换体型/换画风/加腮红;
//	③ 四帧锁同一景别与构图(旧版只对第 1 帧说了上半身中景,后三帧模型就自由拉到全身)。
//
// 其余五类是 2026-09-24 按用户「不同类型的角色要用不同的渲染函数」新增的:同一套人形
// 转身提示词套在铜雀/妖兽/灵体上都是错的(器物没有人脸可锁,灵体没有脚可以转身)。
// init 把"人形机械"这一档挂上去 —— **在"人"那套渲染方法上换掉措辞**,而不是另抄一份。
//
// 为什么用 init 派生而不是在 map 字面量里再写一大块:
//
//	① 人形机械与"人"的**动作/取景是同一套**(站着、原地转身、半身正脸)—— 那部分逐字相同,
//	   抄一份必然漂移(本仓的老毛病:同一件事两处实现,改一处忘一处);
//	② 真正不同的只有**身份锁与反错误形态**:锁的是装甲分缝/关节环/发光标记/材质/机型钢印,
//	   禁的是"退化成普通人"(人类皮肤毛孔、红晕、汗渍、布料古装)。
//
// map 字面量在构建时读不到自己,所以这里必须在 init 里做(那时 entityProfiles 已经建好)。
func init() {
	p := entityProfiles[KindHuman]
	p.Label = "人形机械"
	p.Hint = KindHint(KindAndroid)
	// 标记换了 ⇒ 以前按"人"渲染出来的定妆照会被判成"与当前类型不符"(界面提示重出),这正是我们要的。
	p.SheetMarker = "ANDROID / HUMANOID-MACHINE FIDELITY"
	p.SheetIdentity = androidIdentityBlock()
	// 取景尾巴**必须重写**(与器物/灵宠同一档):人那条走 LLM 原句,而机体的定妆照我们要自己
	// 收口成"上半身正脸 + 纯白背景 + 合成面甲",否则 LLM 可能写 full-body 或带场景的取景。
	p.RewriteSheetFraming = true
	p.SheetFraming = androidSheetFraming()
	p.AnchorKind = "人形机械"
	p.AnchorRefNote = "参考图里的这台机体就是它本体(同一角色可能有两张参考图:" +
		"**锐利的正面定妆照(机型与面甲的唯一权威)** + 横向拼合的转身图;转身图左起第一格就是同一张正面照、" +
		"其余格是同一台机体的其它角度,**它不是第二个角色、也不是一个穿着仿古服装的人**):"
	p.AnchorLock = "身份锁:画面中这台机体的人形轮廓/装甲分缝与板件排布/关节结构/发光标记的位置与颜色/" +
		"材质(陶瓷装甲、金属骨架、线缆)/机身编号刻印必须**照抄参考图里对应的那台机体**,逐项一致 —— " +
		"参考图拍的就是它本体,不是风格参考;面甲(脸)的形状与五官位置以正面定妆照为准,那是**合成面甲**;" +
		"不得按文字另画一个人类:面甲保持光洁的陶瓷质感,头发按定妆照照抄,服装按装甲板件照抄。"
	p.AnchorUseFor = "下面的文字描述只用于「动作/姿态/镜头/台词/所在场景」," +
		"不得用来改写机体结构,也不得用来把它改成人类:"
	p.RenderLook = androidRenderLookBlock()
	p.AntiForm = "KEEP IT A MACHINE (mandatory, every frame): the visible machine structure stays — armour panel seams, " +
		"joint rings, cable runs, glowing markers, and the material contrast between ceramic shell plates and the darker metal frame. " +
		"The faceplate surface is synthetic and matte: seamless, non-porous, with crisp specular highlights and no fabric weave; " +
		"the costume stays the armour plates shown in the reference, never period cloth robes."
	p.JudgePoints = "人形机械判据:① 人形轮廓与装甲板件排布是否与参考图一致 ② 关节环/线缆/发光标记是否还在 " +
		"③ 面甲形状是否与参考图同形 ④ **有没有退化成普通人**(面甲变成有毛孔与红晕的人类面孔、装甲被换成布料古装、换了一张人脸模板)"
	// 多视角:动作与人相同(原地转身),补一条"结构不得被演成人"的保留项与负面项。
	p.TurnRetention = append(p.TurnRetention,
		"<Picture 1> : the MACHINE STRUCTURE is the identity — keep the same armour panel seams, joint rings, cable runs, "+
			"glowing marker positions and colours, and the same ceramic-vs-metal material contrast in every frame. "+
			"This is a humanoid-machine body, not a costume: do NOT swap it for flesh, do NOT smooth the seams away.")
	p.TurnNegative += " no human skin texture (no pores, blush, sweat, freckles), no cloth period costume over the frame, " +
		"no replacement of the faceplate with a different human face template."
	entityProfiles[KindAndroid] = p
}

// androidIdentityBlock 人形机械定妆照的**身份忠实度**块(对应"人"的 identityFidelityBlock)。
//
// 与人的差别只有落点:人锁"脸与肤色",它锁"结构、材质与面甲" —— 但**同样**要求面甲形状照抄
// (否则同一个角色每张图换一张脸,跨镜就接不上)。
//
// 写法是**正向描述**(图像侧是 CFG 1.0 的蒸馏模型:否定词不生效、还会把名词当指令画出来,
// 见 artstyle.go 顶部) —— 所以这里一个 not/never 都不写,禁项写成"它长什么样"。
func androidIdentityBlock() string {
	return "ANDROID / HUMANOID-MACHINE FIDELITY (mandatory): the unit in the reference picture IS this character — " +
		"copy it exactly, item by item: the human-shaped silhouette and overall proportions; the armour panel layout and " +
		"the seams between panels; the joint rings, servos and cable runs; the position, shape and colour of every glowing " +
		"marker; the material contrast between the pale ceramic shell plates and the darker bare-metal frame; and the " +
		"machine number etched on the unit. The face is a SYNTHETIC FACEPLATE: copy its shape, eye shape and brow line " +
		"from the reference; its surface is smooth matte ceramic — seamless, even, non-porous, with a soft specular sheen " +
		"and crisp panel edges. Hair and costume follow the reference exactly (same hair, same armour plates)."
}

// androidSheetFraming 人形机械的取景尾巴:取景口径与"人"相同(上半身正脸、纯白背景),
// 但把"面甲/陶瓷质感"写进去 —— 机体不该被画成一张有皮肤质感的人脸。
func androidSheetFraming() string {
	return " FRAMING (mandatory): a front-facing bust of the unit, shot STRAIGHT-ON from the front — the faceplate looks " +
		"directly into the camera, the head is upright and squared to the lens, the WHOLE head is inside the frame with " +
		"headroom above the hair; the picture is cropped at mid-chest; pure white seamless backdrop, soft even studio " +
		"lighting, no background props, no on-screen lettering."
}

// androidRenderLookBlock 渲染侧质感条款(接在通用介质块后)。
func androidRenderLookBlock() string {
	return "ANDROID SURFACE (mandatory): hard-surface ceramic and metal shading with visible panel seams and joint rings; " +
		"crisp specular highlights on the armour, scuffed bare metal on the frame; emissive glowing markers kept at the " +
		"positions shown in the reference. The surface reads synthetic: smooth matte ceramic with a soft sheen."
}

var entityProfiles = map[EntityKind]entityProfile{
	// ---------- 人 ----------
	KindHuman: {
		Label:         "人",
		Hint:          KindHint(KindHuman),
		SheetIdentity: identityFidelityBlock(),
		SheetMarker:   "IDENTITY FIDELITY",
		SheetLook:     animatedFaceImageBlock(),
		// 人的取景交给 LLM(它写的 upper-body framing 是对的),不重写
		TurnSubject: "the EXACT SAME individual as <Picture 1> in <Picture 1>",
		TurnSummary: "A character turnaround reference: the character stands still in the centre of frame and slowly rotates " +
			"in place, showing the front view first, then turning through three-quarter and full profile to the back view. " +
			"Plain seamless background, even studio lighting, the character never leaves the frame and never changes appearance.",
		TurnRetention: []string{
			"<Picture 1> : fully_preserved - this is an IDENTITY COPY, not a re-design. The subject is the SAME PERSON as <Picture 1>: " +
				"same ethnicity and facial type, same face, same age — never cast a different-looking or different-ethnicity person. " +
				"Face shape, facial features, eyebrows, nose shape and colour, skin tone, hairstyle, hair colour, costume, accessories " +
				"and body proportions must stay exactly the same as <Picture 1> in every frame. FACE LOCK: copy the facial features, " +
				"bone structure and skin details from <Picture 1> exactly; do NOT blend, average, beautify, slim, age or de-age the face, " +
				"and do NOT substitute any other face. Do NOT restyle the subject: same character, same art style, same age, same body proportion as <Picture 1>.",
			"Keep the SAME framing as <Picture 1> in all four shots (the same upper-body medium shot throughout). " +
				"Do NOT zoom out, do NOT switch to a full-body view, do NOT change the camera distance.",
			"Everything must be COPIED from <Picture 1>, never re-invented: hairstyle (same length and same colour), costume, accessories, " +
				"the objects the character is holding (same objects, same colours, no lettering added anywhere) and body proportions. " +
				"Do not restyle, do not recolour, do not change the subject's age or art style.",
		},
		TurnDetail: "Character turnaround on a plain seamless background, soft even studio lighting. Static camera, constant framing, " +
			"constant lighting and constant colour grading across the whole clip. Keep the SAME props and hand items as <Picture 1> " +
			"(do not add, remove or recolour them) and the same plain background; no on-screen text anywhere.",
		TurnShots: [4]string{
			"The character stands centred, facing the camera directly, arms relaxed at the sides, neutral expression; upper-body medium shot, identical to <Picture 1>.",
			"At 00:01.300 The character has rotated about forty-five degrees, now seen from a three-quarter angle; the face is still exactly the face of <Picture 1>, hair colour, hair length and costume unchanged; same upper-body medium shot.",
			"At 00:02.600 The character has rotated about ninety degrees, now in full profile; the silhouette of the hairstyle (same hair colour) and costume stays identical; same upper-body medium shot.",
			"At 00:03.900 The character has rotated about one hundred and eighty degrees, now seen from behind; the back of the hairstyle and the costume are visible, still identical; same upper-body medium shot.",
		},
		TurnNegative: "NEGATIVE (must not happen): no change of body proportion (an adult must stay an adult; a stylized or doll-like character " +
			"must keep exactly the stylization shown in <Picture 1>), no oversized head, no cartoon restyling, no rosy cheeks or blush, " +
			"no red nose unless <Picture 1> shows one, no change of skin tone, no added make-up, no added facial marks, no costume change, " +
			"no change of framing or background, no face close-up.",
		TurnSound:  "Quiet room tone, no speech, no music, no sound effects.",
		AnchorKind: "人物",
		AnchorRefNote: "参考图里出现的人就是他们本人(同一角色可能有两张参考图:" +
			"**锐利的正面定妆照(脸的唯一权威)** + 横向拼合的转身图;转身图的左起第一格就是同一张正面照、" +
			"其余格是同一个人的其它角度,**它不是第二个角色**):",
		AnchorLock: "身份锁:画面中每个人物的脸型/五官/发型发色/肤色/年龄感/体型必须**照抄参考图里对应的那个人**," +
			"逐项一致 —— 参考图拍的就是这个人物本人,不是风格参考;脸部细节(脸型/眼睛/眉毛/发型长度与形状)" +
			"**以正面定妆照为准**;不得按文字重画、不得美化/年轻化/换成更俊美的版本、" +
			"不得把多人的脸平均化成同一张脸。\n" +
			"禁止添加参考图里没有的面部标记:痣、疤、胎记、腮红红晕、红鼻头、夸张大眼睛、额外的胡须与首饰,一律不得无中生有;" +
			"反过来,参考图里有的特征一个都不能丢(发型/发饰/服饰细节同样照抄)。",
		AnchorUseFor: "下面的文字描述只用于「服装/道具/姿态/情绪/台词」,不得用来改写长相",
		RenderLook:   humanFaceRenderClause(),
		JudgePoints: "the same PERSON: face shape, facial features, brows, nose, eye shape, hairstyle and hair colour, " +
			"apparent age, ethnicity, skin tone, and costume",
	},

	// ---------- 物 ----------
	KindObject: {
		Label: "物",
		Hint:  KindHint(KindObject),
		SheetFraming: "plain seamless studio background, the WHOLE object shown complete and centred with generous margin on all sides, " +
			"a clean three-quarter product view of the object, even soft light, full depth of field so every part stays sharp",
		SheetIdentity: "\n\nOBJECT FIDELITY (mandatory): the object's silhouette, overall proportions, every structural part, the material of " + "each part (metal / wood / clay / paper / cloth / stone as the brief says), its colour and patina, seams, joints, rivets, " +
			"openings and surface ornament must match the written brief EXACTLY. Nothing may be added and nothing may be dropped: " +
			"a part the brief lists must be visible, and a part the brief does not list must not appear. " +
			"Do not redesign, simplify or prettify the object's shape.",
		SheetMarker: "OBJECT FIDELITY",
		SheetLook: animatedMediumImageBlock() + "\n- The object's own material dominates the render: the metal reads as metal, the wood as wood, " +
			"the paper as paper, with visible craft detail (plate seams, rivets, joints, patina, grain) under a dull even finish.",
		RewriteSheetFraming: true,
		TurnSubject:         "the EXACT SAME object as <Picture 1> in <Picture 1>, an artefact rather than a living being",
		TurnSummary: "An object turntable reference: the object rests on a plain turntable in the centre of frame and rotates slowly in place — " +
			"the front view first, then three-quarter, then full profile, then the back view. Plain seamless background, even studio lighting, " +
			"the object never leaves the frame, never changes shape and never comes alive.",
		TurnRetention: []string{
			"<Picture 1> : fully_preserved - this is an IDENTITY COPY of an OBJECT, not a re-design. Silhouette, proportions, every part, " +
				"material, colour, patina, ornament and wear must stay exactly as <Picture 1> in every frame. Copy the object, do not re-imagine it.",
			"Keep the SAME framing as <Picture 1> in all four shots: the whole object stays inside the frame with the same margin throughout. " +
				"Do NOT zoom in until parts are cut off, do NOT change the camera distance.",
			"Everything must be COPIED from <Picture 1>, never re-invented: the same number of parts, the same stacked plates / joints / rivets / " +
				"inlays / markings, the same colours, the same degree of wear. No lettering may be added anywhere.",
		},
		TurnDetail: "Object turntable reference on a plain seamless background, soft even studio lighting. Static camera, constant framing, " +
			"constant lighting and constant colour grading across the whole clip. The object simply rotates on the spot; no on-screen text anywhere.",
		TurnShots: [4]string{
			"The object sits centred on the turntable, presented in its canonical front view, everything visible and lit exactly as in <Picture 1>.",
			"At 00:01.300 The object has rotated about forty-five degrees; the same parts, seams, rivets and finish are visible from a three-quarter angle; the whole object still inside the frame.",
			"At 00:02.600 The object has rotated about ninety degrees and is now seen in full profile; the silhouette is unchanged and every part keeps its place.",
			"At 00:03.900 The object has rotated about one hundred and eighty degrees; the back of the object is shown with the same materials and details as <Picture 1>.",
		},
		TurnNegative: "NEGATIVE (must not happen): the object must NOT turn into a creature, an animal or a person; no human face, " +
			"no living eyes, no mouth, no limbs, no flesh, no fur or feathers may be added that <Picture 1> does not show; " +
			"no blob, no walking, no flying, no expression, no change of material or colour, no added ornament, no change of framing or background.",
		TurnSound:  "Quiet room tone with a faint dry mechanical tick as the object rotates, no speech, no music, no melody.",
		AnchorKind: "器物",
		AnchorRefNote: "参考图里出现的那件东西就是它的本体(同一器物可能有两张参考图:" +
			"**锐利的正面定妆照(外形与材质的唯一权威)** + 横向拼合的转台图;转台图左起第一格就是同一张正面照、" +
			"其余格是同一件东西的其它角度,**它不是第二件器物、更不是一个人**):",
		AnchorLock: "器物锁:这件东西的**外形轮廓/比例/结构部件/材质/颜色/做旧与纹样**必须照抄参考图里对应的那一件," +
			"逐项一致;参考图拍的就是这件东西本体,不是风格参考。部件一个都不能少、也一个都不能多:" +
			"参考图上有的(叠片/铆钉/接缝/嵌珠/尾羽/纹饰)必须在画面里找得到,参考图上没有的不许凭空长出来。",
		AnchorUseFor: "下面的文字描述只用于「材质/部件/动作/位置/受光」,不得用来改写外形,更不得用来把它变成人形物体",
		RenderLook: "\n- Every surface of the artefact reads MATTE: metal, wood, clay, paper or cloth with a dull even finish and " +
			"visible craft detail (plate seams, rivets, joints, patina, grain); no chrome shine and no wet sheen anywhere.\n" +
			"(中文对照:器物表面一律哑光——铜/木/泥/纸/布各自的材质感 + 叠片缝/铆钉/接缝/铜绿/木纹等工艺细节;不得出现镜面反光或湿亮。)",
		AntiForm: "ARTEFACT CLAUSE (mandatory): this is an ARTEFACT — a made thing, not a living being. It has no human face, no living eyes, " +
			"no flesh, no skin and no human body beyond exactly what the reference picture shows. It performs only by being moved, carried, " +
			"set down, spun, glowing, vibrating, swinging or casting light — never by walking, talking with a mouth or emoting with a face. " +
			"Its count of parts, its proportions and its material stay identical to the reference picture in every frame.",
		JudgePoints: "the same OBJECT: silhouette, size relative to its parts, every structural part, material, colour, patina and ornament — " +
			"a bronze sparrow must still be that same bronze sparrow and must not have become a person or an animal",
	},

	// ---------- 灵宠 ----------
	KindSpiritPet: {
		Label: "灵宠",
		Hint:  KindHint(KindSpiritPet),
		SheetFraming: "plain background, the WHOLE creature shown complete and centred, full body with head, body, limbs and tail all inside " +
			"the frame, three-quarter view, the face clearly visible, even soft light",
		SheetIdentity: "\n\nCREATURE FIDELITY (mandatory): the species and body plan, fur / feather / scale colour and pattern, eye colour " +
			"and shape, ear / horn / tail shape, limb count, size class and every marking must match the written brief EXACTLY. " +
			"Do not turn it into a different species, do not smooth its markings away, do not make it cuter than described.",
		SheetMarker: "CREATURE FIDELITY",
		SheetLook: animatedMediumImageBlock() + "\n- Fur, feathers or scales read soft and matte with a dull even finish — never wet, " +
			"never glossy, never a plastic toy surface.",
		RewriteSheetFraming: true,
		TurnSubject:         "the EXACT SAME creature as <Picture 1> in <Picture 1>",
		TurnSummary: "A creature turnaround reference: the creature stands on the spot in the centre of frame and slowly rotates, showing the " +
			"front view first, then three-quarter, then profile, then the back view — the same animal, the same pose, only the viewing angle changes. " +
			"Plain seamless background, even lighting, the creature never leaves the frame and never changes species.",
		TurnRetention: []string{
			"<Picture 1> : fully_preserved - IDENTITY COPY of the same creature, not a re-design. Species, body plan, fur / feather / scale colour " +
				"and pattern, eye colour, ear / horn / tail shape, limb count and size must stay exactly as <Picture 1> in every frame.",
			"Keep the SAME framing as <Picture 1> in all four shots: the whole creature stays inside the frame with the same camera distance.",
			"Everything must be COPIED from <Picture 1>: the same markings, the same accessories, the same glow or aura if any (as an effect on the body, never instead of a body). No lettering anywhere.",
		},
		TurnDetail: "Creature turnaround on a plain seamless background, soft even lighting. Static camera, constant framing and constant grading " +
			"across the whole clip; the creature only rotates on the spot, no on-screen text.",
		TurnShots: [4]string{
			"The creature stands centred facing the camera, whole body in frame, natural relaxed pose, identical to <Picture 1>.",
			"At 00:01.300 The creature has turned about forty-five degrees; the same fur / feather / scale pattern and colour are visible from the three-quarter angle.",
			"At 00:02.600 The creature has turned about ninety degrees into full profile; the body length, limb count and tail shape stay identical.",
			"At 00:03.900 The creature has turned about one hundred and eighty degrees; its back, tail and markings are shown with exactly the same pattern as <Picture 1>.",
		},
		TurnNegative: "NEGATIVE (must not happen): no change of species, no anthropomorphism (it must not stand upright like a person, wear human " +
			"clothes it does not have, or gain a human face), no human face or human limbs, no different fur / feather / scale pattern, " +
			"no change of size class, no added markings, no restyling, no change of framing or background.",
		TurnSound:  "Quiet room tone with a soft natural animal breath, no speech, no music.",
		AnchorKind: "灵宠",
		AnchorRefNote: "参考图里的那只生物就是它本体(可能有正面定妆照 + 横向拼合的转身图,转身图左起第一格就是同一张正面照," +
			"其余格是同一只的其它角度,**它不是第二只,也不是一个人**):",
		AnchorLock: "灵宠锁:物种与体态、毛/羽/鳞的颜色与花纹、眼睛颜色与形状、耳/角/尾的形状、四肢数量与体量,必须照抄参考图里的那一只;" +
			"参考图拍的就是它本体。它保留**自己的动物解剖结构**:四足就四足、有翼就有翼,不得因为「会说话/有戏份」就改成人形或拟人化的兽人。",
		AnchorUseFor: "下面的文字描述只用于「毛色细节/配饰/姿态/情绪/动作」,不得用来改写物种与外貌,也不得用来把它拟人化",
		RenderLook: "\n- The creature's coat reads soft and MATTE: fur, feathers or scales with a dull even finish, dry and powdery, " +
			"never wet and never glossy; its eyes are drawn as flat graphic shapes, never glinting.\n" +
			"(中文对照:灵宠的毛/羽/鳞一律哑光柔和、干燥不反光;眼睛是平涂图形化处理,不得反光发亮。)",
		AntiForm: "CREATURE CLAUSE (mandatory): it keeps its own animal anatomy and proportions from the reference picture — no human face, " +
			"no human limbs, no upright human stance, no human clothing unless the reference shows it. Its fur / feathers / scales stay matte " +
			"and dry with a dull even finish, never wet or glossy.",
		JudgePoints: "the same CREATURE: species, body plan, fur / feather / scale colour and pattern, eye colour, ear / horn / tail shape, " +
			"limb count and size class",
	},

	// ---------- 妖兽 ----------
	KindBeast: {
		Label: "妖兽",
		Hint:  KindHint(KindBeast),
		SheetFraming: "plain background, the WHOLE beast shown complete and centred, full body from horn to tail inside the frame, " +
			"three-quarter view that conveys its size and mass, even dramatic-but-soft light",
		SheetIdentity: "\n\nBEAST FIDELITY (mandatory): species and body plan, scale and mass, horn / fang / claw / wing / tail configuration, " +
			"hide, scale or feather colour and pattern, eye colour and shape, and every unnatural feature the brief names must match it EXACTLY. " +
			"Do not shrink it into a cute pet, do not remove its horns or fangs, do not soften its build.",
		SheetMarker: "BEAST FIDELITY",
		SheetLook: animatedMediumImageBlock() + "\n- Hide, scale or feather reads matte and dry with a dull even finish — a heavy, " +
			"weathered surface, never wet, never glossy, never a plastic toy finish.",
		RewriteSheetFraming: true,
		TurnSubject:         "the EXACT SAME beast as <Picture 1> in <Picture 1>",
		TurnSummary: "A beast turnaround reference: the beast stands on the spot in the centre of frame and slowly rotates, showing the front view " +
			"first, then three-quarter, then profile, then the back view — the same creature, only the viewing angle changes. " +
			"Plain seamless background, even lighting, it never leaves the frame and never changes form.",
		TurnRetention: []string{
			"<Picture 1> : fully_preserved - IDENTITY COPY of the same beast. Body plan, mass, horn / fang / claw / wing / tail configuration, " +
				"hide, scale or feather colour and pattern, eye colour and any unnatural feature must stay exactly as <Picture 1> in every frame.",
			"Keep the SAME framing as <Picture 1> in all four shots: the whole beast stays inside the frame with the same camera distance.",
			"Everything must be COPIED from <Picture 1>: the same markings, the same number of horns, wings, limbs and tails, the same wear. No lettering anywhere.",
		},
		TurnDetail: "Beast turnaround on a plain seamless background, soft even lighting with a low key to keep its mass readable. Static camera, " +
			"constant framing and constant grading across the whole clip; the beast only rotates on the spot, no on-screen text.",
		TurnShots: [4]string{
			"The beast stands centred facing the camera, whole body in frame, weight settled, identical to <Picture 1>.",
			"At 00:01.300 The beast has turned about forty-five degrees; horns, shoulders and hide pattern read clearly from the three-quarter angle.",
			"At 00:02.600 The beast has turned about ninety degrees into full profile; body length, limb count, wings and tail stay identical.",
			"At 00:03.900 The beast has turned about one hundred and eighty degrees; its back, tail and markings are shown with exactly the same pattern as <Picture 1>.",
		},
		TurnNegative: "NEGATIVE (must not happen): no shrinking into a cute pet, no loss of horns / fangs / claws / wings, no change of species " +
			"or body plan, no anthropomorphism (no upright human stance, no human face, no human clothing), no change of hide colour or pattern, " +
			"no restyling, no change of framing or background.",
		TurnSound:  "Quiet low room tone with a heavy animal breath, no speech, no music.",
		AnchorKind: "妖兽",
		AnchorRefNote: "参考图里的那头就是它本体(可能有正面定妆照 + 横向拼合的转身图,转身图左起第一格就是同一张正面照," +
			"其余格是同一头的其它角度,**它不是第二头,也不是一个人**):",
		AnchorLock: "妖兽锁:物种与体态、体量、角/牙/爪/翼/尾的配置、皮/鳞/羽的颜色与花纹、眼睛颜色、以及原文点名的非自然特征," +
			"必须照抄参考图里的那一头。**体量不许缩水**(该是庞然大物就得占满画面),兽形不许改人形," +
			"参考图有的角与牙一个都不能丢。",
		AnchorUseFor: "下面的文字描述只用于「姿态/动作/受光/情绪(以兽的方式表达)」,不得用来改写物种与体量",
		RenderLook: "\n- The beast's hide, scales or feathers read MATTE and dry with a dull even finish — a heavy weathered surface, " +
			"never wet, never glossy, never a plastic toy finish.\n(中文对照:妖兽的皮/鳞/羽一律哑光干燥、呈厚重做旧质感;不得湿亮、不得塑料感。)",
		AntiForm: "BEAST CLAUSE (mandatory): it keeps its own monstrous anatomy and its full mass from the reference picture — never a cute " +
			"shrunken pet, never an upright human stance, never a human face or human clothing. Its hide, scales or feathers stay matte and dry " +
			"with a dull even finish.",
		JudgePoints: "the same BEAST: species, body plan, mass, horn / fang / claw / wing / tail configuration, hide or scale colour and pattern, " +
			"eye colour",
	},

	// ---------- 无实体 ----------
	KindIncorporeal: {
		Label: "无实体",
		Hint:  KindHint(KindIncorporeal),
		SheetFraming: "plain dark background, the WHOLE apparition centred and fully visible, its whole form inside the frame with generous margin, " +
			"soft even light that reveals the shape without hardening it",
		SheetIdentity: "\n\nFORMLESS-FORM FIDELITY (mandatory): its silhouette, outline behaviour, colour and temperature of its glow, degree of " +
			"transparency, and whatever residual features the brief names (a remembered face, a remembered garment, a floating mask) must match " +
			"the written brief EXACTLY. The entity is NOT solid: keep its edges dissolving rather than cut clean.",
		SheetMarker: "FORMLESS-FORM FIDELITY",
		SheetLook: animatedMediumImageBlock() + "\n- The body is translucent and lit from within: a soft, diffuse glow with misty, dissolving edges; " +
			"the light spreads gently instead of forming a hard bloom.",
		RewriteSheetFraming: true,
		TurnSubject:         "the EXACT SAME apparition as <Picture 1> in <Picture 1>, a bodiless presence rather than a living person",
		TurnSummary: "An apparition reference: the presence hovers in the centre of frame and slowly turns in place, its translucent form drifting " +
			"and flickering as it rotates — front first, then three-quarter, then profile, then its back. Plain dark background, even dim lighting, " +
			"the form never leaves the frame and never becomes solid.",
		TurnRetention: []string{
			"<Picture 1> : fully_preserved - IDENTITY COPY of the same presence: the same silhouette, the same glow colour and intensity, " +
				"the same degree of transparency and the same residual features in every frame.",
			"Keep the SAME framing as <Picture 1> in all four shots: the whole form stays inside the frame with the same margin and the same scale.",
			"Everything must be COPIED from <Picture 1>: the same edge treatment (dissolving into mist or light), the same drifting particles or " +
				"wisps, the same residual garment or feature if the sheet shows one. No lettering anywhere.",
		},
		TurnDetail: "Apparition reference on a plain dark background, dim even lighting. Static camera, constant framing and constant grading across " +
			"the whole clip; the form only rotates in place while continuing to drift and flicker, no on-screen text.",
		TurnShots: [4]string{
			"The presence hovers centred, facing the camera, its translucent form and inner glow exactly as in <Picture 1>.",
			"At 00:01.300 The presence has turned about forty-five degrees; the same glow, transparency and drifting wisps are visible from the three-quarter angle.",
			"At 00:02.600 The presence has turned about ninety degrees into profile; the silhouette and its dissolving edge stay identical.",
			"At 00:03.900 The presence has turned about one hundred and eighty degrees; its far side is shown with the same translucency and glow as <Picture 1>.",
		},
		TurnNegative: "NEGATIVE (must not happen): the presence must NOT become solid; no opaque body, no flesh, no cast shadow on the ground, " +
			"no footprints, no weight, no footprints or dust kicked up, no full opacity, no change of glow colour, no added solid costume, " +
			"no change of framing or background.",
		TurnSound:  "Quiet airy room tone with a faint hollow resonance, no speech, no music.",
		AnchorKind: "灵体",
		AnchorRefNote: "参考图里的那团东西就是它本体(可能有定妆照 + 横向拼合的转身图,转身图左起第一格就是同一张定妆照," +
			"其余格是同一团形体的其它角度,**它不是第二个人物**):",
		AnchorLock: "无实体锁:形体轮廓、发光颜色与强弱、透明程度、以及原文点名的残留特征(记忆里的脸/衣袍/面具)必须照抄参考图," +
			"逐项一致。它是**非实体存在**:边缘是化开的、能透光,不是一个有血有肉的活人;" +
			"表演方式是漂移/悬停/拉长/闪动/穿墙,而不是走路的实体动作。",
		AnchorUseFor: "下面的文字描述只用于「漂移方式/情绪/受光/与环境的关系」,不得用来把它实体化或改写形体",
		RenderLook: "\n- Its glow is soft and diffuse: the light spreads gently through the translucent form instead of forming hard speculars " +
			"or a blown-out bloom, and its edges keep dissolving into mist.\n" +
			"(中文对照:灵体的光是柔和散射的、从形体内透出,不得出现硬高光或过曝光斑;边缘保持雾状化开。)",
		AntiForm: "INCORPOREAL CLAUSE (mandatory): it stays bodiless — translucent, glowing from within, edges dissolving into mist or light. " +
			"It casts no ground shadow, leaves no footprint, has no weight and is never touched by solid objects as if it were solid; " +
			"it moves by drifting, hovering, stretching and flickering. Its outline must never harden into a solid opaque body.",
		JudgePoints: "the same PRESENCE: silhouette, glow colour and intensity, degree of transparency, edge treatment, residual features — " +
			"judge that it is the same apparition and that it has NOT become a solid opaque person",
	},

	// ---------- Q 版 ----------
	KindChibi: {
		Label: "Q版",
		Hint:  KindHint(KindChibi),
		SheetFraming: "plain pastel background, the WHOLE chibi figure shown complete and centred, full body inside the frame, facing the camera, " +
			"big head and small body, thick clean outlines, even soft light",
		SheetIdentity: "\n\nCHIBI FIDELITY (mandatory): the figure keeps the EXACT identity markers of the character it depicts — the same face " +
			"design, hairstyle and hair colour, the same costume and colour scheme, the same signature marks (a mole, a scar, a hair ornament, " +
			"a tool). Only the PROPORTIONS become chibi. Do not redraw the identity into a generic cute mascot.",
		SheetMarker: "CHIBI FIDELITY",
		// 【2026-09-28 用户要求「Q 版形象需要娇小呆萌可爱」】
		// 介质块**必须用 Q 版自己那套**（`animatedChibiImageBlock`：可爱风格化 3D、玩具质感），
		// 不能用 `animatedMediumImageBlock`（电影级 3D 写实）—— 后者会把 Q 版画成
		// "写实的小人"，与"娇小呆萌"正好相反。旧文案里的 "flat cel colour" 也一并去掉：
		// 那是 2D 平涂的写法，与 3D 玩具质感打架（`artstyle_test.go` 有专条钉"不许出现 2D 卡通用词"）。
		SheetLook: animatedChibiImageBlock() + "\n- Rounded, chunky and huggable: simplified rounded " +
			"features, large bright eyes with a warm friendly expression, soft matte surfaces and a " +
			"cheerful silhouette that reads as a small cute character at a glance.",
		// Q 版的比例就是它这个类型本身(见 SheetProportions 的字段说明):卡里带技能侧的
		// 人体比例句时这句让位,介质块照旧保留。
		SheetProportions:    "\n- Two-to-three-head-tall proportions.",
		RewriteSheetFraming: true,
		TurnSubject:         "the EXACT SAME chibi figure as <Picture 1> in <Picture 1>",
		TurnSummary: "A chibi figure turnaround reference: the figure stands in the centre of frame and slowly rotates in place, showing the front " +
			"view first, then three-quarter, then profile, then the back view. Plain pastel background, even soft lighting, the figure never leaves " +
			"the frame and keeps the same chibi proportions and identity throughout.",
		TurnRetention: []string{
			"<Picture 1> : fully_preserved - IDENTITY COPY of the same chibi figure: same head-to-body ratio, same face design, same hairstyle and " +
				"hair colour, same costume and colour scheme, same signature marks in every frame.",
			"Keep the SAME framing as <Picture 1> in all four shots: the whole figure stays inside the frame, same camera distance, same head size.",
			"Everything must be COPIED from <Picture 1>: the same costume details, the same accessories and held items, the same colour scheme. No lettering anywhere.",
		},
		TurnDetail: "Chibi figure turnaround on a plain pastel background, even soft lighting. Static camera, constant framing and constant grading " +
			"across the whole clip; the figure only rotates on the spot, no on-screen text.",
		TurnShots: [4]string{
			"The chibi figure stands centred facing the camera, whole body in frame, big head and small body, identical to <Picture 1>.",
			"At 00:01.300 The figure has turned about forty-five degrees; the same hairstyle, face design and costume are visible from the three-quarter angle.",
			"At 00:02.600 The figure has turned about ninety degrees into full profile; the head-to-body ratio and costume silhouette stay identical.",
			"At 00:03.900 The figure has turned about one hundred and eighty degrees; the back of the hairstyle and the costume are shown, still identical.",
		},
		TurnNegative: "NEGATIVE (must not happen): no return to realistic human proportions, no change of head-to-body ratio, no loss of the " +
			"signature marks, no restyling into a different mascot, no change of costume or colour scheme, no change of framing or background.",
		TurnSound:  "Quiet room tone, no speech, no music, no sound effects.",
		AnchorKind: "Q 版形象",
		AnchorRefNote: "参考图里的这个 Q 版形象就是它本体(可能有正面定妆照 + 横向拼合的转身图,转身图左起第一格就是同一张正面照," +
			"**它不是额外多出来的一个小人**):",
		AnchorLock: "Q 版锁:头身比、脸型设计、发型发色、服装配色与标志性记号必须照抄参考图里的这个 Q 版形象;" +
			"不得把它拉回真人头身比,也不得换成一个通用的萌系吉祥物。",
		AnchorUseFor: "下面的文字描述只用于「姿态/表情/动作/服装细节」,不得用来改写头身比与身份记号",
		RenderLook:   humanFaceRenderClause(),
		JudgePoints: "the same chibi CHARACTER DESIGN: head-to-body ratio, face design, hairstyle and hair colour, costume and colour scheme, " +
			"signature marks",
	},

	// ---------- 场景(环境参考图;不是角色卡类型,见 entity.go 的 KindScene) ----------
	//
	// 场景与角色共用**同一套装配链**(取景重写 + styleExpand 的三道守卫),但取景与
	// 忠实度的落点完全不同:
	//   - 取景:场景要的是**环境图**——整块地方、建筑/地面/天光,画面里躺着一个空场;
	//     此前场景走裸提示词,LLM 没写取景时模型就自由发挥(有时塞人进画面,而那个
	//     人不是本剧任何角色的脸,反而成了 H3 的干扰参考)。
	//   - 忠实度:场景要抄的是**空间结构与材质**,不是脸。给它写"脸型/五官照抄"就是
	//     在逼模型画人(与 2026-09-24「铜雀被渲染成人物」同一条教训)。
	KindScene: {
		Label: "场景",
		Hint:  "地点/环境:整块地方的空场环境图(无人物),渲染方法为环境插图 + 场景忠实度",
		SheetFraming: "a wide establishing view of the whole location — the environment itself is the subject: " +
			"its architecture, ground plane, sky, light and props; the place stands completely deserted and unpeopled, " +
			"an empty stage waiting for the cast",
		SheetIdentity: "\n\nSCENE FIDELITY (mandatory): the place must match the written brief EXACTLY — the same spatial layout, " +
			"the same architecture and building materials, the same ground, vegetation and props, the same time of day, weather and " +
			"light direction. Art style changes the RENDERING SURFACE only (material, sheen, shading); it must NEVER change the layout " +
			"or the identity of the place. Every design stays ORIGINAL: never reproduce a real, identifiable location or landmark.",
		SheetMarker: "SCENE FIDELITY",
		SheetLook:   animatedMediumImageBlock(),
		// 与器物/灵宠/妖兽/无实体/Q 版同一条理由:LLM 写的 image_prompt 常常带着人像取景
		// (plain background / upper-body framing / portrait),场景必须重写取景尾巴,
		// 否则同一段提示词里同时要求"整块地方"和"半身正面"。
		RewriteSheetFraming: true,
	},
}

// TurnaroundPrompt 组装该类型的转身/转台视频提示词(六段式,与 director 的 sysH3Format 同构)。
//
// medium 是**介质锁**(见 artstyle.go 的 TurnaroundMediumBlock,空串 = 不加):
// 转身视频是独立的一次渲染,拿不到分镜那边的介质覆盖,不给它这一句,H3 就用自己的
// 默认介质重画 —— 实测赛璐璐角色被画成写实真人、插画铜雀被画成写实 3D 机械鸟,
// 逐帧身份校验于是必然不通过(2026-09-24 用户报「多视角老是生成错误」的根因)。
func (p entityProfile) TurnaroundPrompt(name, imagePrompt, medium string) string {
	desc := compactIdentity(imagePrompt)
	subject := p.TurnSubject
	if strings.TrimSpace(desc) != "" {
		// 把描述插进身份句里(参考图仍是唯一权威:描述只补"是哪一类东西")
		if i := strings.Index(subject, ", "); i > 0 && strings.HasPrefix(subject, "the EXACT SAME ") {
			// object/incorporeal 变体自带补充语,插在补充语之后
			if j := strings.Index(subject, " in <Picture 1>"); j > 0 {
				subject = subject[:j] + ", " + desc + subject[j:]
			}
		} else {
			subject += ", " + desc
		}
	}
	var sb strings.Builder
	sb.WriteString("subject_definitions:\n<Subject 1> is " + subject + ".\n")
	if medium != "" {
		// 介质锁紧贴身份句:它是"身份的一部分",不是风格偏好
		sb.WriteString("\nMEDIUM LOCK (highest priority): rendering medium is part of the identity. " +
			"<Picture 1>, every frame of this clip and every frame extracted from it must read as THE SAME ARTWORK — " +
			"same medium, same level of stylization, same finish. This is an identity copy, not a re-illustration.\n")
		sb.WriteString(medium + "\n")
	}
	sb.WriteString("\nsummary:\n[reference generation]\n")
	sb.WriteString(p.TurnSummary)
	sb.WriteString("\n\nretention_analysis:\n")
	for _, r := range p.TurnRetention {
		sb.WriteString("- " + r + "\n")
	}
	if medium != "" {
		sb.WriteString("- RENDERING MEDIUM IS PART OF THE IDENTITY: <Picture 1> is a cinematic photoreal 3D render " +
			"with IDEALISED, SEMI-STYLISED faces. Do NOT re-render the subject as a flat cel-shaded cartoon or a " +
			"hand-drawn anime/manga figure, and do NOT turn it into a photographed real person or a glossy plastic " +
			"model; the real material response, the matte finish and the fine film grain must stay identical in every " +
			"frame (see MEDIUM LOCK above).\n")
	}
	sb.WriteString("\ndetailed_description:\n")
	sb.WriteString(p.TurnDetail + "\n")
	for i, s := range p.TurnShots {
		sb.WriteString("[Shot " + itoa(i+1) + "] " + s + "\n")
	}
	sb.WriteString("NOTE FOR THE RENDERER: never mention a feature you do not want (saying \"no grey hair\" makes grey hair appear) — " +
		"copy everything from <Picture 1> instead of re-inventing it.\n")
	sb.WriteString(p.TurnNegative + "\n")
	if medium != "" {
		sb.WriteString("NEGATIVE (must not happen): a photographic or 3D-rendered version of the subject, " +
			"a live-action look, glossy or plastic surfaces, and any change of the rendering medium between frames.\n")
	}
	sb.WriteString("The whole clip is one continuous slow rotation with no cuts, no camera movement and no background change. " +
		"No on-screen text, no subtitles, no watermarks, no logos. Subjects must remain clearly visible and adequately lit throughout the video; " +
		"avoid rendering any frame nearly black or overly dark.\n\noverall_soundscape:\n" + p.TurnSound +
		"\n\nnon_diegetic_music:\nN/A")
	return sb.String()
}

// itoa 小整数转字符串(避免为两处循环引入 strconv)。
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
