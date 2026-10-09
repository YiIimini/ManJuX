package project

// plan_checks.go `ValidatePlan` 的判据分组（2026-09-29 代码健康审计 P2-11 拆分）。
//
// ## 为什么要拆
//
// `ValidatePlan` 原本是**一个 1545 行的函数**，107 道门挤在一条直线过程里：
// 改一处要滚半天、没法按门隔离测试、也没法按域补反例。审计的结论是这不该是
// "一个函数"，而拆它正好能让"某一道门的反例"成组地补（见 G057 那条教训：
// 17 道 fail 级门里唯一一道从没被反例跑过的，就藏在这种"整文件 94.5% 覆盖率"里）。
//
// ## 怎么拆的（本仓纪律，见 AGENTS.md）
//
// ① **先写行为锁** —— `validate_behavior_lock_test.go` 锁住 5 组夹具的现有输出；
// ② **机械搬运** —— 按原文里已有的 `// ──` 分组注释整块移动，连注释一起、**一个字符不改**；
// ③ **跑锁** —— 锁绿说明行为没变。
//
// **没有顺手改任何判据**：这次提交只搬位置。想改逻辑另开一次（混在一起出 bug，
// 分不清是"搬错"还是"改错"——这是本仓栽过的坑）。
//
// 每组的局部变量都在组内，唯一的跨组共享是"镜头 id 集合"：原实现用前半段建的 `seen`
// 判"镜头号不连续"，拆分后那一组自己算（只多 3 行，比额外传一个 map 更不容易接错）。

import (
	"fmt"
	"strings"
)

// checkPlanBasics ……（搬运自原 `ValidatePlan`，判据与注释均未改动）
//
// 这一组含"建索引 + 用索引做基础判据"：`charIDs` / `sceneIDs` / `subjectNames` 都在组内，
// 不做跨组共享（拆分前它们是函数级变量，拆分后只在这一组用到 —— 已逐处核对使用行）。
func checkPlanBasics(p *Plan, add func(level, where, format string, a ...any)) {
	// 角色/场景 id 集合(悬空引用是最常见的"技能端写自己的、平台端认不得")
	charIDs := map[string]bool{}
	for i, c := range p.Characters {
		if strings.TrimSpace(c.ID) == "" {
			add("fail", fmt.Sprintf("characters[%d]", i), "角色 id 为空(镜头无法引用)")
		}
		if strings.TrimSpace(c.Name) == "" {
			add("warn", fmt.Sprintf("characters[%d]", i), "角色名为空(id=%s)", c.ID)
		}
		if strings.TrimSpace(c.ImagePrompt) == "" {
			add("warn", fmt.Sprintf("characters[%d]", i),
				"角色 %s 缺 image_prompt —— 定妆照只能靠描述现编,跨镜一致性会掉", c.Name)
		}
		charIDs[c.ID] = true
	}
	sceneIDs := map[string]bool{}
	for i, s := range p.Scenes {
		if strings.TrimSpace(s.ID) == "" {
			add("fail", fmt.Sprintf("scenes[%d]", i), "场景 id 为空(镜头无法引用)")
		}
		if strings.TrimSpace(s.ImagePrompt) == "" {
			add("warn", fmt.Sprintf("scenes[%d]", i), "场景 %s 缺 image_prompt —— 环境参考图只能现编", s.Name)
		}
		// ── 场景板(T2I)的汉字门（2026-09-27 技能侧整改单 §1.4 / 平台侧整改清单 §6.2）──────
		//
		// 场景板是文生图出的**环境参考图**，会被 H3 当成"这个地方本来就长这样"带进**每一镜** ——
		// 板上糊一个乱码字形，那一集每一镜都带着它（与"场景板里画了人 → 每镜都出现同一个陌生人"
		// 是同一条机理，见 pipeline.StripSceneCrowd）。
		//
		// 与 `<d>` 外汉字门**不同**的是这里的口径更严：**出现任何汉字即提示，不扣主体名**。
		// 扣减集合是给 h3_prompt 用的（官方要求用文字点名主体，名字里的汉字不可避免），
		// 而场景卡的 image_prompt 里没有"必须写中文名"的理由 —— 渲染期点名靠英文结构与
		// PICTURE MANIFEST。实测《求你们骂我》12 张场景卡每张都以中文名开头（合计 178 字），
		// 就是因为原先只有总阈值门、且把主体名算成合法。
		if n := CountCJK(s.ImagePrompt); n > 0 {
			add("warn", fmt.Sprintf("scenes[%d]", i),
				"场景 %s 的 image_prompt 里有 %d 个汉字 —— 场景板是 T2I 出的，汉字会被画成"+
					"乱码字形糊在环境板上，而这块板会被带进用到该场景的每一镜；"+
					"画面提示词一律写英文（平台装配时会自动清掉，但产出侧就该是干净的）", s.Name, n)
		}
		sceneIDs[s.ID] = true
	}

	// 汉字卫生门的**扣减集合**：主体名（角色 + 场景 + 道具）。
	//
	// 官方要求「用文字点名主体」（渲染期 PICTURE MANIFEST 靠它对编号），9 个角色同镜时
	// 名字本身就近百字，而且一个名字在提示词里会被反复提到 —— 那是**不可避免**的汉字。
	// 不扣会天天误报，真出问题时反而被淹掉（误拦的规则比没有规则更糟）。
	// 判据与阈值见 prompt_cjk.go（与技能侧 manju_plan.py 同源）。
	subjectNames := make([]string, 0, len(p.Characters)+len(p.Scenes)+len(p.Props))
	for _, c := range p.Characters {
		subjectNames = append(subjectNames, c.Name)
	}
	for _, s := range p.Scenes {
		subjectNames = append(subjectNames, s.Name)
	}
	for _, pr := range p.Props {
		subjectNames = append(subjectNames, pr.Name)
	}

	// **头身比锚自检**（2026-09-29 用户：「碑生（丙十七）、钟离、碑生·Q版、崔无咎 这几个
	// 定妆照的比例就不对」）。取景尺度已经在装配点补齐，但**锚本身**没人检查：
	// 「约七头半身」配「矮小驼背」这类自相矛盾的组合，取景怎么改都画不出对的东西。
	// 锚来自技能侧交出的【人物外形锚表】，所以门报出来之后要改的是**锚表原文**。
	// 级别是 warn（老包大量没写头身比，只报写了的且写矛盾的，避免刷屏）。
	// **性别没写就要报**（2026-09-29 用户：「钟离渲染成男的？」）：卡面/锚表里没有性别时，
	// 出图模型自己猜 —— 实测守钟女「钟离」被画成男性。技能侧已补 `性别：女|男` 字段，
	// 这条门保证"没补的卡"在出图前就看得见（锚表原文要改，所以是 warn）。
	for _, msg := range GenderIssues(p) {
		// 级别 info：存量包**每张卡**都没写性别，报 warn 会直接刷屏（本仓纪律：刷屏的规则
		// 比没有规则更糟）。技能侧补上字段后这条会自然消失；平台侧能做的（有性别就写进
		// 提示词）已经做了。
		add("info", "plan.characters", "%s", msg)
	}
	for _, msg := range ProportionAnchorIssues(p) {
		add("warn", "plan.characters", "%s", msg)
	}

	seen := map[int]bool{}
	for i, sh := range p.Shots {
		where := fmt.Sprintf("shots[%d](id=%d)", i, sh.ID)
		if sh.ID <= 0 {
			add("fail", where, "镜头 id 必须为正整数(它同时是产物文件名 NNN.mp4)")
		} else if seen[sh.ID] {
			add("fail", where, "镜头 id 重复 —— 后渲的会覆盖前一个的产物")
		}
		seen[sh.ID] = true

		if strings.TrimSpace(sh.Scene) == "" {
			add("warn", where, "未指定 scene —— 该镜拿不到环境参考图(空镜请确认是有意的)")
		} else if !sceneIDs[sh.Scene] {
			add("fail", where, "引用了不存在的场景 %q(参考图会挂空)", sh.Scene)
		}
		for _, cid := range sh.Characters {
			if !charIDs[cid] {
				add("fail", where, "引用了不存在的角色 %q(定妆照会挂空,跨镜换脸)", cid)
			}
		}

		if sh.Duration < PlanMinShotSeconds || sh.Duration > PlanMaxShotSeconds {
			add("fail", where, "镜头时长 %d 秒超出 H3 合法区间 %d–%d 秒",
				sh.Duration, PlanMinShotSeconds, PlanMaxShotSeconds)
		} else if sh.Duration > PlanRecommendedShotSeconds {
			// 合法但超过**建议值**：2026-09-29 用户「分镜渲染时间不超过12S，最高建议10S左右最合适」——
			// 11/12 秒是留给"台词确实排不开"的少数镜的，不是常态。本地 H3 单镜成本随帧数超线性增长
			// （10 秒 ≈ 6 秒的 2.8 倍），且长片段尾部是崩坏高发区。
			add("warn", where, "镜头时长 %d 秒超过建议值 %d 秒（上限 %d 秒合法，但建议 10 秒左右；"+
				"台词排不开才用 11/12 秒，否则拆成两镜更稳）",
				sh.Duration, PlanRecommendedShotSeconds, PlanMaxShotSeconds)
		}
		if strings.TrimSpace(sh.H3Prompt) == "" {
			// 【为什么要把"缺什么"写出来（2026-10-08 用户：「还没做/边界，给我留着吗？」）】
			// 平台对空正文会**先自己兜一手**（按这一镜的场次/人物/景别/机位/台词拼一版最小合规正文，
			// 见 empty_body.go）；能走到这条 fatal 的，是**连场次/人物/台词都没有**的镜 ——
			// 那时平台拼不出正文（拼出来等于替编剧新编一镜）。旧文案只说"重跑剧本分镜补上这一镜"，
			// 用户不知道该补什么；这里把缺的那几项点名。
			miss := MissingBodyMaterial(p, &sh)
			extra := ""
			if len(miss) > 0 {
				extra = fmt.Sprintf("（平台也拼不出它：这一镜连 %s 都没有 —— 补上任意一项就有材料可拼）",
					strings.Join(miss, "、"))
			}
			add("fatal", where, "h3_prompt 为空 —— 该镜没有可提交的提示词（引擎收到空提示词会出一帧近全黑，"+
				"实测第 02 集镜 15 达 94%%）%s；重跑剧本分镜补上这一镜，或在故事板里手改", extra)
		}
		switch sh.Mode {
		case "ref2va", "t2va", "":
		default:
			add("warn", where, "mode=%q 不是 ref2va/t2va（空值平台会自行推断）", sh.Mode)
		}
		if s := strings.TrimSpace(sh.ShotSize); s != "" {
			ok := false
			for _, v := range PlanShotSizes {
				if strings.Contains(s, v) {
					ok = true
					break
				}
			}
			if !ok {
				add("warn", where, "景别 %q 不在白名单(%s)—— 参考图拼合会按「保留其它角度」处理",
					s, strings.Join(PlanShotSizes, "/"))
			}
		}
		// ── 主视角（2026-09-27 技能侧整改单 §1.1）──────────────────────────
		//
		// 渲染真正读的是 h3_prompt，所以覆盖率判据以提示词为准（聚合在循环外报一次）；
		// 这里只查两件"精确且少见"的事：字段不在官方词表里、字段与提示词对不上。
		// 后者等于自检面板在骗人 —— 审计时看到 view 写了鸟瞰，成片却是平视。
		if v := strings.TrimSpace(sh.View); v != "" {
			known := false
			for _, want := range PlanViews {
				if v == want {
					known = true
					break
				}
			}
			if !known {
				add("warn", where, "view=%q 不是官方 9 视角词表里的串（必须逐字写）—— %s", v, PlanViewHint())
			} else if got, ok := PlanViewIn(sh.H3Prompt); ok && got != v {
				add("warn", where,
					"view 字段写的是 %q，而 h3_prompt 里出现的是 %q —— 两者必须一致（渲染读的是提示词）",
					v, got)
			}
		}
		// ── 尾帧状态里的听觉/氛围词（§1.2 只写看得见的）──────────────────────
		if hit := EndStateBannedIn(sh.EndState); len(hit) > 0 {
			add("warn", where,
				"end_state 里有听觉/氛围/情绪词 %s —— 尾帧状态只写看得见的：人物位置、姿态、"+
					"表情、视线方向、环境状态（下游画不出「寂静」「压抑」这类词）", strings.Join(hit, "、"))
		}
		// ── 语音层(v2)────────────────────────────────────────────────────
		//
		// 这三条是"配音错"的可判形态。用户报的是「系统旁白,主角动嘴?」——
		// 提示词措辞约束不住它(模型只看见一句 <d>),所以把机器判得出来的部分
		// 写成 fail:嘴巴开在谁身上,由"这句话是谁说的、他在不在画面里"决定。
		charInShot := map[string]bool{}
		for _, cid := range sh.Characters {
			charInShot[cid] = true
		}
		// **一镜台词条数上限**（2026-09-29 用户：「配音存在重复的」）。实测：一个 9 秒的镜里塞了 4 条台词，
		// 渲出来 ASR 转成「…碑是你自己刻的, 碑是你自己刻的, 碑号是营里烙的。」—— **同一句念了两遍**。
		// 机制：模型把每句放在一个内切点上，句子比节拍多时它会重复其中一句。
		// 判据取 3 条起报（2 条是常见的正反打，合法）。
		if n := len(sh.Dialogue); n >= 3 {
			add("warn", where,
				"这一镜有 %d 条台词 —— 实测 4 条挤在 9 秒里会让模型**把其中一句念两遍**（配音重复）；"+
					"建议把这一镜拆成两镜（每镜 ≤2 句），或删掉可省的短句", n)
		}
		for j, l := range sh.Dialogue {
			where := fmt.Sprintf("%s.dialogue[%d]", where, j)
			if strings.TrimSpace(l.Text) == "" {
				add("fatal", where, "台词文本为空 —— 渲染时模型没有台词可念，会自己瞎念"+
					"（实测 ASR 转出来不是话）；要么把这句话写全（逐字中文），要么把这条删掉")
				continue
			}
			name := strings.TrimSpace(l.Speaker)
			if name == "" {
				add("warn", where, "台词没有说话人")
				continue
			}
			if IsVoiceoverSpeaker(name) {
				// 画外音标签(旁白/系统/画外音/内心):必须是 os=true。
				// 否则平台按画内台词处理 → 模型在画面里找个角色张嘴说旁白。
				if !l.OS {
					add("fail", where,
						"说话人是画外音标签 %q 却没标 os=true —— 渲染会当画内台词处理,"+
							"让画面里的人张嘴替旁白说话(用户报的「系统旁白,主角动嘴」)", name)
				}
				continue
			}
			// 说话人是具体角色:必须在画面里(os=false 时)。
			if !l.OS {
				found := false
				for _, ch := range p.Characters {
					if ch.ID == name || ch.Name == name {
						found = true
						if !charInShot[ch.ID] {
							add("fail", where,
								"说话人「%s」不在本镜 characters 里 —— 画面里没有他,"+
									"模型会随便找个人动嘴(要么把他加进本镜,要么标 os=true 当画外音)", ch.Name)
						}
						break
					}
				}
				if !found {
					// 完全一致匹配是**有意保留**的口径（2026-09-27 技能侧整改单 §2.5 / 平台侧整改清单 §6.1）：
					// 技能侧 v2.9 起落盘即写卡名（`沈砚` → 该章生效的 `沈砚·七品`），实测两本
					// "说话人查不到卡 = 0 句"。平台若在这里**自动改写** speaker，字幕与台词归属
					// 就会与产出侧不一致 —— 所以只做诊断：家族基名命中时把"认得出是同一个人"
					// 这件事说清楚（`沈砚` vs `沈砚·七品`），让人一眼知道该改哪里，绝不动数据。
					if hit := familyMatchName(p, name); hit != "" {
						add("warn", where,
							"说话人 %q 与卡名「%s」不是完全一致，但家族基名一致（按 %s 判定为同一人）——"+
								"音色绑定靠这个名字连线，产出侧落盘时写卡名即可（技能侧 v2.9 已改）；"+
								"平台不改写 dialogue[].speaker（改写会让字幕/台词归属与产出侧不一致）",
							name, hit, CharFamilyBaseName(name))
					} else {
						add("warn", where, "说话人 %q 不在角色表里(只有画外音标签可以这么写)", name)
					}
				}
			}
		}
		// 有台词/旁白却连 <d> 都没有 → fail:平台后处理补不了(它不知道该往哪插 <d>),
		// H3 会即兴挑语言,成片是听不懂的鸟语。
		// 只是**缺 [Chinese] 标记**则归 warn —— 后处理 EnsureChineseDialogue 会把每个 <d>
		// 都补成 <d>[Chinese],那是修复得了的事,不该拒收(误拦比漏拦更糟)。
		if (len(sh.Dialogue) > 0 || strings.TrimSpace(sh.Narration) != "") &&
			!strings.Contains(sh.H3Prompt, "<d>") {
			add("fail", where,
				"本镜有台词/旁白,h3_prompt 里却没有任何 <d> 标记 —— 成片会说成听不懂的话")
		}
		// 画外音镜:提示词里必须有"嘴唇闭合"子句。
		// 级别是 **info**：`HealPlan → FillVoiceoverAssets` 会**确定性补齐**这一句
		// ⇒ 用户没有任何可做的事（2026-09-28 降噪：把"平台自己会补"的事报成 warn，
		// 会让真正的警告淹在噪音里 —— 平台自己的话就是"假告警会让人不再看告警"）。
		if HasVoiceover(sh) && !strings.Contains(sh.H3Prompt, VoiceoverClauseMarker) {
			add("info", where,
				"本镜有画外音但 h3_prompt 没写「%s」—— 平台会**自动补齐**（这一步不需要你做什么）；产出侧写对更稳",
				VoiceoverClauseMarker)
		}
		// **画面外说话人**（2026-09-29 用户：「镜头外的说话声音不应该镜头内人动嘴唇」）：
		// 说话人有角色卡、但这一镜没让他出场 —— 此时画面里的人**不许**对口型。
		// 与上一条同源（都靠 VoiceoverClauseMarker 那句"闭嘴"），级别同样 info（平台会自动补齐）；
		// 单独报是为了让"这一镜的台词是别处的人说的"这件事在预检里看得见。
		if speakers := OffScreenSpeakers(p, sh); len(speakers) > 0 && !strings.Contains(sh.H3Prompt, VoiceoverClauseMarker) {
			add("info", where,
				"本镜的台词是**画面外的人**说的（%s 不在本镜出场名单里）但 h3_prompt 没写「%s」—— "+
					"平台会**自动补齐**（画面内的人保持闭嘴）；产出侧写对更稳",
				strings.Join(speakers, "、"), VoiceoverClauseMarker)
		}
		// ── 汉字卫生（2026-09-26 用户报障「成片出现无名字幕」）──────────────────
		//
		// 官方 ON-SCREEN TEXT 规则的判据是：**没被英文引号括起来的文字，就是模型会画成
		// 字形的来源**；而汉字合法的位置只有 `<d>[Chinese]…</d>`（官方「说」的通道，
		// 同时供 H3 出原生中文配音）与**主体名**（官方要求点名主体）。所以 `<d>` 外的
		// 可避免汉字 = 往画面上写字。实测《翻篇》第 01 集 `<d>` 外 7083 字：根因不是台词，
		// 是场景卡的中文美术卡整段被拼进了 subject_definitions。
		//
		// 阈值与扣减集合**与技能侧 manju_plan.py 同源**（>200 fail / >60 warn，扣主体名），
		// 见 prompt_cjk.go。技能侧在产出前跑同一条，平台侧在这里守门 —— 这样**别的书**与
		// 平台 LLM 直出路径也能被拦住，而不是只有技能侧新出的包干净。
		//
		// 为什么不顺手升 PlanContractVersion：技能侧 v2.3 是在**同一契约版本**下加的这条门，
		// 单侧升版本会把现网所有 v2 包判成「版本不符」而整批拒收 —— 那与本条的意图无关。
		if n := CJKOutsideDialogue(sh.H3Prompt, subjectNames); n > PlanCJKFailOutsideDialogue {
			add("fail", where,
				"h3_prompt 里 <d> 外有 %d 个**可避免的**汉字（主体名已扣除）—— 这就是成片画面上"+
					"冒出无名字幕/乱码字形的直接来源：英文结构散文里不许有汉字，中文美术卡别塞进提示词",
				n)
		} else if n > PlanCJKWarnOutsideDialogue {
			add("warn", where, "h3_prompt 里 <d> 外有 %d 个可避免的汉字（主体名已扣除）", n)
		}
		// keyframe_prompt 是**首帧 T2I 静帧**提示词：它没有 `<d>` 这条合法通道，而图生图
		// 模型画中文几乎必然成乱码 —— 这一镜的第一帧就带字。所以「出现任何汉字」都提示。
		if n := CountCJK(sh.KeyframePrompt); n > 0 {
			add("warn", where,
				"keyframe_prompt 里有 %d 个汉字 —— 首帧是 T2I 出的，汉字会被画成乱码字形"+
					"（这一镜的第一帧就带字）", n)
		}
		// 台词装不下:平台侧会抬时长(最多到上限),但抬到顶仍不够时成片会**截断**,
		// 这是"配音不完整"的老病根,所以在入口就如实报出来。
		if need := NeededShotSeconds(sh); need > PlanMaxShotSeconds {
			add("warn", where,
				"台词+旁白需约 %d 秒、超过单镜上限 %d 秒 —— 会被抬到上限并可能截断,建议拆镜",
				need, PlanMaxShotSeconds)
		}
	}
}

// checkCoverage ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkCoverage(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 覆盖率类判据（整改单 §1.1/§1.2/§1.5）────────────────────────────────
	//
	// 这三组都是"每镜都可能命中"的规则，**按计划聚合报一次**，不逐镜刷屏：
	// 一条 56 镜的方案若逐镜报"缺尾帧状态"，报告里 50 条噪音会把真正的那几条淹掉
	// （本包的老规矩：误拦/刷屏的规则比没有规则更糟，因为报告没人看）。
	// 每条都带上"命中几镜 / 共几镜"与示例镜号，够定位也不吵。
	if len(p.Shots) > 0 {
		// 2026-09-27 晚**裁定 A**（技能侧追补单订正版 N5，平台侧为裁定方）：
		// **允许留空**，只对"官方 9 条语义站得住"的镜要求必须有 —— 建立镜头与远景/全景。
		//
		// 为什么不再"每镜必填"：官方那 9 条是**大视角/机位表**（鸟瞰、平视深远、纵深压缩、
		// 条件性的仰视/窥视/强透视/长焦），里面**没有**"平视中近景"，也没有"俯拍"。
		// 一条"平视中景"或"俯拍特写"在表里找不到对应项 —— 硬填就是写错词（把特写写成
		// "超广角斜 45° 鸟瞰"），而**写错词比留空更糟**（留空只是少一句镜头语言，写错是把画面带偏）。
		// 技能侧的实测口径：2462 镜里 1693 镜有官方视角串、769 镜留空（都带 `view_note` 说明原因）。
		var noView, noNote []string
		viewSet := map[string]bool{}
		viewShots, optional := 0, 0
		for i, sh := range p.Shots {
			if v, ok := PlanViewIn(sh.H3Prompt); ok {
				viewSet[v] = true
				viewShots++
				continue
			}
			if strings.TrimSpace(sh.H3Prompt) == "" {
				continue
			}
			if ViewRequiredForShot(p.Shots, i) {
				noView = append(noView, fmt.Sprintf("#%d", sh.ID))
			} else {
				optional++
				// 留空**必须写明原因**（2026-09-29 用户：「前后镜漂移，压不住场」）。
				//
				// 这里**不**改成"每镜必填 view"：官方那 9 条是大机位表，平视中近景/俯拍特写
				// 没有对应项，硬填就是写错词，而写错词比留空更糟（上面那段注释已论证；
				// 技能侧 2462 镜里 769 镜留空，都带 `view_note`）。缺的是**留空时的纪律**：
				// 留空又不说明，成片就只表现为"机位随机跳"——实测那一集 11 镜里 8 镜既没 view 也没 note。
				if strings.TrimSpace(sh.ViewNote) == "" {
					noNote = append(noNote, fmt.Sprintf("#%d", sh.ID))
				}
			}
		}
		if len(noNote) > 0 {
			add("warn", "plan.shots",
				"%d 镜的机位视角**留空又没写原因**（view_note）—— 留空本身合法（官方表里没有「平视中近景」这类），"+
					"但要说清为什么留空，否则整集看起来就是「机位随机跳」：%s",
				len(noNote), strings.Join(firstN(noNote, 6), " "))
		}
		if len(noView) > 0 {
			add("warn", "plan.shots",
				"%d 镜**该有**机位视角却没写（建立镜头与远景/全景必须写；共 %d 镜留空属正常）—— "+
					"这些镜是交代空间的主力，缺了整集会像用同一台机器拍完（%s；%s）",
				len(noView), optional, PlanViewHint(), strings.Join(firstN(noView, 6), " "))
		}
		// 视角单调：只有一两种视角同样是"同一台机器"的另一种形态。
		// 阈值取 6：整改单的验收是"camera 取值从 2 个涨到 ≥6 个"。
		// **只在"带视角的镜够多"时才判**（裁定 A 之后大量小景别合法留空，样本太薄就不该判单调 ——
		// 否则一条 60 镜里只有 5 镜带视角的方案也会被要求"铺开到 6 种"，那是无理要求）。
		if len(viewSet) < 6 && viewShots >= planViewVarietyMinShots {
			add("warn", "plan.shots",
				"整集只用了 %d 种机位视角（%s）—— 官方词表有 9 条，建议至少铺开到 6 种，"+
					"否则景别变化也救不回「镜头感」", len(viewSet), strings.Join(viewKeys(viewSet), " / "))
		}

		var establishingBad []string
		var establishingClose []string
		for i, sh := range p.Shots {
			if !IsEstablishingShot(p.Shots, i) || strings.TrimSpace(sh.H3Prompt) == "" {
				continue
			}
			if !strings.Contains(sh.H3Prompt, PlanEstablishingExtra) {
				establishingBad = append(establishingBad, fmt.Sprintf("场景 %s 的首镜 #%d", sh.Scene, sh.ID))
			}
			for _, bad := range PlanEstablishingBannedSizes {
				if strings.Contains(sh.ShotSize, bad) {
					establishingClose = append(establishingClose, fmt.Sprintf("场景 %s 的首镜 #%d 是「%s」", sh.Scene, sh.ID, sh.ShotSize))
				}
			}
		}
		if len(establishingBad) > 0 {
			// info 而不是 warn：装配时 `EnsureEstablishingExtra`（pipeline/prompts.go）会**确定性追加**
			// 这段串 ⇒ 用户没有可做的事。与"画外音闭嘴句"同一条政策：平台已处理的事只做告知，
			// 不冒充需要处理的问题（否则真正的警告会淹在噪音里）。
			add("info", "plan.shots",
				"%d 个建立镜头没写建立镜头串 —— 平台会**自动追加**（这一步不需要你做什么）；"+
					"产出侧逐字写上更稳：%q（%s）", len(establishingBad), PlanEstablishingExtra, strings.Join(establishingBad, "、"))
		}
		if len(establishingClose) > 0 {
			add("warn", "plan.shots",
				"建立镜头用了特写/小景别 —— %s；小景别当建立镜头等于没交代地点，换远景/全景/中景",
				strings.Join(establishingClose, "、"))
		}

		// ── 特写/近景却"没有参考卡的人"（2026-09-27 技能侧追补单 N1）──────────────
		//
		// 判据：`shot_size ∈ {特写, 近景}` 且 `characters` 为空。为什么要一条门：
		// 这类镜里**画面几乎只有一张脸**，而参考图集合是从 `characters` 派生的
		// （见 pipeline.RefPlan）—— 没有人就没有参考图，每次生成**换一张新脸**。
		// 这不是理论风险：技能侧全书 2462 镜里就有 3 镜这样，其中**第一条是全书第一镜、主角的脸**
		// （`第01集 S1 特写 :: 一筐烂菜叶砸在他脸上`，同镜锚点还写着 `empty frame, no people`，
		// 与正文"人群里一张脸被打"自相矛盾）。技能侧提这条时的原话很准：
		// **根因不是笔误，而是这类缺陷没有门** —— 谁写分镜都可能犯，LLM 直出更可能（它每集现写）。
		//
		// 与上面「建立镜头不许特写」形状一致（同样聚合报一条），但两者**不是一回事**：
		// 那条管"小景别当建立镜头"，这条管"有人脸却没卡"。
		var closeNoChar []string
		unbound, unboundNoCard, unboundByDesign := 0, 0, 0
		for _, sh := range p.Shots {
			if !IsCloseUpShot(sh.ShotSize) {
				continue
			}
			if len(sh.Characters) == 0 {
				// 技能侧**显式**写了"这个人故意不绑卡、只给背影/局部/人群"时不报 ——
				// 那种镜是"刻意不露脸"，与"忘了绑卡"是两回事，后者才是每次换一张脸的缺陷。
				// 判据优先用**字段**（`unbound` + `unbound_reason`，技能侧 09-27 §9 给的），
				// 再退回早先的 prose 写法（PlanUnboundFaceMarker）—— 字段是稳的，prose 会随措辞漂。
				//
				// **按 reason 分支**（技能侧 §9 的明确要求，两个 token 已进他们的契约）：
				//   `no_card`           画面里的人不在本集角色表（群演/一次性角色）→ 要露脸就得先建卡；
				//   `faceless_by_design` 刻意不给脸（背影/局部/群像）→ 无需建卡。
				// 分支不是为了放行（两者都不算漏绑），而是为了让**读日志的人知道自己在看哪种情况**：
				// 前者是"可以考虑建卡"的信息，后者是"本来就该这样"。
				if sh.Unbound || strings.Contains(sh.H3Prompt, PlanUnboundFaceMarker) {
					unbound++
					switch sh.UnboundReason {
					case UnboundNoCard:
						unboundNoCard++
					case UnboundFaceless:
						unboundByDesign++
					}
					continue
				}
				closeNoChar = append(closeNoChar,
					fmt.Sprintf("#%d「%s」%s", sh.ID, strings.TrimSpace(sh.ShotSize), firstRunes(sh.Action, 18)))
			}
		}
		if len(closeNoChar) > 0 {
			extra := ""
			if unbound > 0 {
				// 别让例外静默：按 reason 分支说明"另有哪些镜是刻意不露脸的，已排除"。
				// 分支口径见技能侧契约（tokens 写进他们的 §2.3）：no_card = 要露脸就得先建卡；
				// faceless_by_design = 刻意不给脸、无需建卡。
				// 没给 reason 的（老明细/handmade 方案）如实说"未标原因"，别把它算进任一分支。
				nor := unbound - unboundNoCard - unboundByDesign
				tail := ""
				if nor > 0 {
					tail = fmt.Sprintf("；另有 %d 镜标了 unbound 但没给 reason", nor)
				}
				extra = fmt.Sprintf("（另有 %d 镜显式标了 unbound：no_card %d / faceless_by_design %d —— "+
					"前者是「画面里的人不在本集角色表」，要让他露脸就得先建卡；后者是「刻意不给脸」；两者都不算漏绑%s）",
					unbound, unboundNoCard, unboundByDesign, tail)
			}
			add("warn", "plan.shots",
				"%d 镜是特写/近景却没有出场角色（%s）—— 这一档画面里几乎只有一张脸，而没有角色就没有参考图，"+
					"**每次生成都是一张新脸**；要么给这些镜绑上角色卡，要么把景别改到不露脸（背影/局部/群演）"+
					"并把正文里的脸删掉（画面里有人时也不许写 `empty frame, no people` 这种自相矛盾的锚点）%s",
				len(closeNoChar), strings.Join(firstN(closeNoChar, 4), "、"), extra)
		}

		// 尾帧状态：只对"后面还有同一场景的镜头"的那些镜判 —— 跨场景本来就不要求动作连贯
		// （改用渐隐渐显 + 转场字幕），每段场景的末镜与全片末镜不写尾帧状态是对的。
		var noEnd []string
		for i, sh := range p.Shots {
			if i+1 >= len(p.Shots) {
				continue
			}
			next := p.Shots[i+1]
			if strings.TrimSpace(next.Scene) != strings.TrimSpace(sh.Scene) {
				continue // 跨场景：不要求接动作
			}
			if strings.TrimSpace(sh.EndState) == "" {
				noEnd = append(noEnd, fmt.Sprintf("#%d", sh.ID))
			}
		}
		if len(noEnd) > 0 {
			add("warn", "plan.shots",
				"%d 镜缺 end_state（尾帧状态）—— 它们后面紧跟着同一场景的下一镜，"+
					"没有尾帧状态时「接上一镜」只能靠模型猜（缺尾帧的镜：%s）",
				len(noEnd), strings.Join(firstN(noEnd, 6), " "))
		}

		// 连续同景别（整改单 §1.1 验收：连续同景别 ≤6 镜）
		if runSize, runStart, n := longestSameShotSizeRun(p.Shots); n > PlanMaxConsecutiveSameShotSize {
			add("warn", "plan.shots",
				"连续 %d 镜同一个景别「%s」（#%d 起）—— 上限 %d 镜；连续同景别是「画面单调」最直接的成因",
				n, runSize, runStart, PlanMaxConsecutiveSameShotSize)
		}

		// ── 核心情绪镜的时长下限（2026-09-29：用户三份文件都写了，平台一直没有判据）──
		if ids := EmotionShotIssues(p); len(ids) > 0 {
			add("warn", "plan.shots",
				"%d 个**戏眼镜头**短于 %d 秒（%s）—— 用户文件：「承载核心情绪反应的镜头"+
					"（特写面部微表情、沉默对峙、泪目、咬牙、握拳颤抖）不得少于 5 秒。这类镜头是「戏眼」，"+
					"快切会直接消解情感浓度，宁愿压缩前面的动作铺垫也要给足发酵时间」",
				len(ids), PlanEmotionMinSeconds, strings.Join(firstN(ids, 6), "、"))
		}

		// ── 必填字段（规范 §6.2，审计 #15「只查 action 一个字段」的收口）────────
		//
		// 判据在 `required_fields.go`。那里只列**还没有专门门**的字段（其余各项各有门，
		// 重复造门就是同一件事报两遍）；台词那一条只对 `vo_kind=speech` 的镜判。
		for _, msg := range RequiredFieldIssues(p) {
			add("warn", "plan.shots", "%s", msg)
		}

		// ── 「片段 → 小镜头」两层结构（2026-09-28 用户架构）────────────────────
		//
		// 判据在 cuts.go。用户原话：「一个分镜里可以包含多个小分镜，小分镜里包含运镜」——
		// 此前小镜头只是正文里的字符串，**没有任何结构化字段**，于是"这一片段切了几刀"
		// 这件事查不动、改不了、界面上也摆不出来，而它正是用户反复点的"拖沓"的成因。
		if ids := CutCoverageIssues(p); len(ids) > 0 {
			add("warn", "plan.shots",
				"%d 个片段超过 4 秒却**没切小镜头**（%s）—— 用户文件（《【额外】仿真人10S分镜头指令》"+
					"《五、最新豆包十秒指令》）要求片段内切 %d–%d 个小镜头、每镜 %.1f–%.1f 秒。"+
					"一镜到底正是「全程慢动作」体感的来源：一个 5 秒片段从头演一个连续动作，中间没有切点",
				len(ids), strings.Join(firstN(ids, 6), "、"), CutPlanMinCuts, CutPlanMaxCuts,
				CutPlanMinSeconds, CutPlanMaxSeconds)
		}
		for _, msg := range CutIssues(p) {
			add("warn", "plan.shots", "%s", msg)
		}

		// ── 契约门第二批（2026-09-28）────────────────────────────────────────
		//
		// 判据在 `contract_gates.go`，文案在这里（gatecat 扫字面格式串生成门目录）。		//
		// 判据在 `contract_gates.go`，文案在这里（gatecat 扫字面格式串生成门目录）。
		if ids := CutVerbIssues(p.Shots); len(ids) > 0 {
			add("warn", "plan.shots",
				"%d 镜用了自创的切镜写法（%s）—— 官方 §4.2 给 CUTS 单列了词表，"+
					"只认「%s」这五个（原句照抄，别改写）；自创写法 H3 不认，切镜会变成一句没用的形容",
				len(ids), strings.Join(firstN(ids, 6), "、"), strings.Join(PlanCutVerbs, " / "))
		}
		if ids := ScreenTextQuoteIssues(p.Shots); len(ids) > 0 {
			add("warn", "plan.shots",
				"%d 镜的画面里有招牌/牌匾/碑文这类**要读的字**，但正文里没有成对引号包住的原文（%s）—— "+
					"官方硬约束：屏幕可见文字必须**逐字加引号**并保持原语言。不引的实测后果是"+
					"模型自己编一段伪汉字（引号是给渲染器的信号：引号里是要逐字出现的文字，不是画面形容）",
				len(ids), strings.Join(firstN(ids, 6), "、"))
		}
		for _, iss := range SceneElementIssues(p.Scenes) {
			add("warn", "plan.scenes",
				"场景「%s」缺四大要素里的 %s —— 用户《四》§「四大要素：环境类型、具体时间、空间氛围、"+
					"视觉主要特征」；少一项，板子就只能由模型自己补，而它补出来的每一集都不一样",
				iss.Name, strings.Join(iss.Words, "、"))
		}
		if names := SceneViewIssues(p.Scenes); len(names) > 0 {
			add("warn", "plan.scenes",
				"%d 个场景卡没有任何九视角词（%s）—— 场景是**每一镜的空间底座**，建立镜头必须说清"+
					"「从哪看」；一条都没有时渲染只能自己猜一个（且每集猜得不一样）。词表：%s",
				len(names), strings.Join(firstN(names, 5), "、"), strings.Join(PlanViews, " / "))
		}

		//
		// 判据在 `asset_gate.go`（返回**数据**），文案在这里 —— 理由同运镜那三条：
		// `docs/门目录.md` 由 gatecat 用 AST 扫**字面格式串**生成，写成 `"%s", msg`
		// 那几条门在目录里就看不出来。
		for _, iss := range SceneIssues(p.Scenes) {
			switch iss.Kind {
			case SceneHasPeople:
				add("warn", "plan.scenes",
					"场景「%s」的板面文本里有 %d 个人物词（%s）—— 场景板必须是**空场**："+
						"板上的人会被 H3 当作「这个地方本来就有的陌生人」带进该地点的每一镜，"+
						"与角色的身份锁直接打架（用户《四》§4.1 严禁人影）",
					iss.Name, len(iss.Words), strings.Join(iss.Words, "/"))
			case SceneHasEvent:
				add("warn", "plan.scenes",
					"场景「%s」的板面文本里有 %d 个事件痕迹词（%s）—— 场景板只画"+
						"「事件发生前的地点固有状态」：板上带着血渍/尸体/破碎物/遗落道具，"+
						"那一集每一镜都像「事情已经发生过」（用户《四》§4.3 事件隔离）",
					iss.Name, len(iss.Words), strings.Join(iss.Words, "/"))
			}
		}
		if issues := PropFeatureIssues(p.Props); len(issues) > 0 {
			var parts []string
			for i, iss := range issues {
				if i >= 4 {
					break
				}
				parts = append(parts, PropIssueText(iss))
			}
			add("warn", "plan.props",
				"%d 件道具缺「功能作用」或「独有特征」（%s）—— 用户《二》要求八项、"+
					"此前只查了六项：功能边界常常是后面情节的钥匙（「托不起人」这种限制往往就是钥匙），"+
					"而独有特征是它在画面里被认出来的唯一依据",
				len(issues), strings.Join(parts, "；"))
		}
		// ── 运镜（camera）的四条门 ─────────────────────────────────────────────
		//
		// 判据在 `camera_gate.go`（返回**数据**），播报文案在这里 —— 因为 `docs/门目录.md`
		// 由 gatecat 扫**字面格式串**生成，写成 `add(..., "%s", msg)` 那条门在目录里就看不出来。
		// 口径出处（官方 / 平台规则 / 用户《五》的"慢动作"）都写在 camera_gate.go 的文件头。
		for _, iss := range CameraIssues(p.Shots) {
			switch iss.Kind {
			case CamMissing:
				add("warn", "plan.shots",
					"%d 镜没有写运镜（camera）—— 运镜是这一镜「看起来是不是活的」的直接来源；"+
						"平台要求每镜给一个（官方 §4.3 只规定写法与词表，这条是平台规则）：%s",
					len(iss.IDs), strings.Join(firstN(iss.IDs, 6), " "))
			case CamJargon:
				add("warn", "plan.shots",
					"%d 镜用了摄影术语「%s」—— H3 认的是自然英文动作（官方 §4.3 的三个示例都是 "+
						"「The camera pushes in …」这种写法），术语进正文只是噪音：%s",
					len(iss.IDs), iss.Term, strings.Join(firstN(iss.IDs, 6), " "))
			case CamRepeat:
				add("warn", "plan.shots",
					"连续 %d 镜同一个运镜「%s」（#%d 起）—— 上限 %d 镜；与「连续同景别」同一条机理："+
						"运镜一潭死水，整集看下来像没剪过",
					iss.Run, iss.Cam, iss.Start, PlanMaxConsecutiveSameCamera)
			case CamMonotone:
				add("warn", "plan.shots",
					"整集 %d 镜的运镜只有 %d 种（%s）—— 官方词表有 %d 条，建议至少铺开到 %d 种；"+
						"只有一两种运镜时，景别与视角再变也救不回「镜头感」（观众说的「镜头一直是推/拉」就是这一条）",
					iss.Shots, len(iss.Kinds), strings.Join(iss.Kinds, " / "), len(PlanCameraKinds), PlanCameraVarietyMinKinds)
			}
		}
		// ── 场次覆盖对账（2026-10-09 用户：「场景和分镜运镜很单一」）──────────────────
		//
		// 剧本（平台自己的 ① 会话产出）自带结构化场次表【场 N|地点|时间|内外】，剧本阶段
		// 已把它落进方案（Plan.BriefScenes）—— 这里在**渲染前门**再对一次账：此前这笔账只在
		// 剧本阶段播报，重渲 / 导入包 / 预检（cmd/preflight）根本看不到，用户要到成片才发现。
		for _, iss := range SceneCoveragePlanIssues(p) {
			switch iss.Kind {
			case SceneShareOverCap:
				add("warn", "plan.brief_scenes",
					"「%s」一场占了 %d/%d 镜（%d%%）—— 上限 %d%%：超过就是观众说的「原地打转」；"+
						"把多余的镜让给别的场次，或在同一场内部换机位与时段",
					iss.Scene, iss.Shots, iss.Total, iss.Percent, SceneShareCapPercent)
			case SceneMissing:
				add("warn", "plan.brief_scenes",
					"剧本列了 %d 场戏，分镜只用到 %d 个场景 —— 有场次整段没被拍；逐场核对剧本的"+
						"「【场 N|地点|时间|内外】」，缺哪场补哪场",
					iss.Brief, iss.Used)
			case SceneNightDropped:
				add("warn", "plan.brief_scenes",
					"剧本里有**夜场**，而全集一个夜戏场景都没用上 —— 色调与节奏会没有起伏"+
						"（实测第 02 集：唯一的夜戏整场丢掉，整集八成镜头挤在同一个户外白日场景里）")
			}
		}

		// H3 官方格式（§1.5 的可机检三条）：聚合报一次，附前几条人话说明
		var fmtHits []string
		badShots := 0
		for _, sh := range p.Shots {
			if v := H3FormatViolations(sh.H3Prompt); len(v) > 0 {
				badShots++
				for _, s := range v {
					if len(fmtHits) < 4 {
						fmtHits = append(fmtHits, fmt.Sprintf("#%d %s", sh.ID, s))
					}
				}
			}
		}
		if badShots > 0 {
			add("warn", "plan.shots",
				"%d 镜的 h3_prompt 不符合官方格式（写错了不报错，只是成片变差）：%s",
				badShots, strings.Join(fmtHits, "；"))
		}

		// 混进"写给生成器的话"（2026-09-27 晚，验收技能侧重出的脚本时实测）：
		// 技能侧全书 2462 镜的 h3_prompt 都以 `REFERENCE ORDER: … characters array …` 开头，
		// 还夹着 `the renderer appends an authoritative PICTURE MANIFEST…` —— 这些是**写给
		// 拼提示词的人的规则**，官方仓一句都没有、共享契约也没定义。平台读取边界已经会剔掉
		// （project/h3_clean.go，一处清理四条消费路径），但**必须在导入时就告诉人**：
		// 上游不改，每次导出都会再带一遍。聚合报一次，别刷 2462 条。
		dirShots := 0
		var dirSample []string
		for _, sh := range p.Shots {
			if HasH3WriterDirectives(sh.H3Prompt) {
				dirShots++
				if len(dirSample) < 3 {
					dirSample = append(dirSample, fmt.Sprintf("#%d", sh.ID))
				}
			}
		}
		if dirShots > 0 {
			add("warn", "plan.shots",
				"%d 镜的 h3_prompt 里混着**生成器指令**（如 REFERENCE ORDER / PICTURE MANIFEST，%s…）："+
					"渲染时会自动剔除，但根治要脚本侧重新导出 —— 这些行不属于官方提示词结构，官方仓里没有这种写法",
				dirShots, strings.Join(dirSample, "、"))
		}

		// ── 官方「标准镜头信息表」STEP 5.5 的三条可机检自检（2026-09-27）──────────────
		// 依据：`skills/3d-animation-short-generator/references/shot-table-spec.md`。
		// 平台 plan.json 就是那张表（六列 ↔ 我们的字段见 shot_table.go 的对照），
		// 所以官方的强制自检可以直接落在契约上 —— "这一集是不是平/有没有一镜塞太多人"
		// 从"靠感觉说"变成可判。
		//
		// ── 抓重点：《一、网文小说转剧本提示词指令》里**能量化的两条**（2026-09-28 用户：
		// 「分镜脚本 LLM 解析是否按这个来啊，抓重点啊，咋那么纯啊」）────────────────────
		//
		// 那份文件把改编纪律写死了：开篇 3 秒爆点（**无任何铺垫、无多余旁白**）、
		// 每 80 字 1 个小爽点 / 每 200 字 1 个大爽点、段段留钩、砍掉冗余铺垫支线。
		// 钩子类型与密度平台已有官方门，这里补它**特有**的两条：
		//   ① 开篇镜不许有旁白 —— "无多余旁白"是那份文件对首镜的硬要求（旁白＝铺垫）；
		//   ② 爽点密度按**台词字数累计**：连续超过 PlanPayoffMaxRunes 字没有强节拍 ⇒ 这一段是平的。
		//
		// 为什么机器门只卡 200 字、不卡 80 字：80 字那条是提示词层面的引导（每 80 字一个小爽点），
		// 而按台词字数硬卡 80 会在一句长台词上误报；200 字那条才是"这一段已经拖了"的可靠信号。
		if len(p.Shots) > 0 {
			if first := p.Shots[0]; strings.TrimSpace(first.Narration) != "" || first.VoKind == VoKindNarration {
				add("warn", "plan.shots",
					"开篇镜 #1 带了旁白（%q）—— 用户《一、网文小说转剧本提示词指令》第一条就是"+
						"「第一句话必须抛出极致冲突/绝境/反转/悬念，**无任何铺垫、无多余旁白**，直接切入高潮名场面」；"+
						"旁白是最典型的铺垫，首镜该用画面动作把人钉在屏幕上",
					firstN([]string{strings.TrimSpace(first.Narration)}, 1)[0])
			}
			// 爽点密度：强节拍 = reveal / reversal / climax（逆袭、打脸、揭秘、升阶）
			var flat []string
			acc, from := 0, 0
			for _, sh := range p.Shots {
				if acc == 0 {
					from = sh.ID
				}
				for _, ln := range sh.Dialogue {
					acc += len([]rune(strings.TrimSpace(ln.Text)))
				}
				if isPayoffHook(hookTypeOf(sh)) {
					acc = 0
					continue
				}
				if acc > PlanPayoffMaxRunes {
					flat = append(flat, fmt.Sprintf("#%d–#%d", from, sh.ID))
					acc = 0
				}
			}
			if len(flat) > 0 {
				add("warn", "plan.shots",
					"%d 段连续超过 %d 字没有强节拍（%s）—— 《一、网文小说转剧本提示词指令》要求"+
						"「每 80 字 1 个小爽点、每 200 字 1 个大爽点」，主角全程不憋屈不拖沓；"+
						"把这段里的逆袭/打脸/揭秘/升阶提前或加一个，别让台词堆成流水账",
					len(flat), PlanPayoffMaxRunes, strings.Join(firstN(flat, 5), "、"))
			}
		}

		// ── 片段内切点必须落在片长内（官方 `skills/h3-prompt-writing/SKILL.md` §Tips：
		// "描述总时长必须等于请求时长，且落在 4–15 秒"）────────────────────────────────
		//
		// 平台此前那条"一镜到底"门只数**切点数量**，从不核对切点**是否超出片长** ✗ ——
		// 于是"一个 6 秒片段里写着 At 00:09.000"这种自相矛盾的提示词能一路渲下去：
		// 模型拿到的时间轴和实际时长对不上，画面节奏必然乱（要么赶、要么拖）。
		// 两条判据：① 任何切点不得 ≥ 片长；② 最后一刀之后剩下的尾巴不能太长（等于又变成一镜到底）。
		if len(p.Shots) > 0 {
			var over, longTail []string
			for _, sh := range p.Shots {
				if sh.Duration <= 0 {
					continue
				}
				last := 0
				for _, tp := range shotTimestamps(sh.H3Prompt) {
					if tp.ms >= sh.Duration*1000 {
						over = append(over, fmt.Sprintf("#%d(片长 %ds，却有切点 %s)", sh.ID, sh.Duration, tp.raw))
						break
					}
					if tp.ms > last {
						last = tp.ms
					}
				}
				if last > 0 && sh.Duration-last/1000 > PlanOneTakeSeconds {
					longTail = append(longTail, fmt.Sprintf("#%d(最后一刀在 %ds，尾巴还有 %ds)", sh.ID, last/1000, sh.Duration-last/1000))
				}
			}
			if len(over) > 0 {
				add("fail", "plan.shots",
					"%d 镜的**内部切点超出了片段时长**（%s）—— 官方要求「描述总时长必须等于请求时长」；"+
						"切点超出片长时模型拿到的时间轴和实际时长对不上，节奏必然乱（要么赶词要么拖）",
					len(over), strings.Join(firstN(over, 5), "、"))
			}
			if len(longTail) > 0 {
				add("warn", "plan.shots",
					"%d 镜的**最后一刀之后留了太长的尾巴**（%s）—— 尾巴超过 %d 秒等于又变回一镜到底；"+
						"把最后一刀往后挪，或再补一刀",
					len(longTail), strings.Join(firstN(longTail, 5), "、"), PlanOneTakeSeconds)
			}
		}
		// ── 片段内切镜：治"拖沓"的机制门（2026-09-28 用户：「难怪渲染的视频总感觉拖沓，都没抓重点」）──
		//
		// 量出来的事实：平台的每镜正文里**只有一个 `[Shot 1]`** —— 一个 6–8 秒片段从头到尾演一个
		// 连续动作，观众感觉就是"全程慢动作"。用户自己的《【额外】仿真人10S分镜头指令》写着
		// 「每片段内拆成若干小镜头，每镜 1.5–4 秒」，H3 官方 Ref2VA 也支持一个片段写多个
		// `[Shot N] At MM:SS.mmm`。**切镜是节奏**：同样内容切三刀就是快，一镜到底就是拖。
		if len(p.Shots) > 0 {
			var oneTake []string
			for _, sh := range p.Shots {
				if sh.Duration < PlanOneTakeSeconds {
					continue // 短片段一镜到底没问题
				}
				if H3ClipCuts(sh.H3Prompt) < 2 {
					oneTake = append(oneTake, fmt.Sprintf("#%d(%ds)", sh.ID, sh.Duration))
				}
			}
			if len(oneTake) > 0 {
				add("warn", "plan.shots",
					"%d 镜是**一镜到底的长片段**（%s）—— 一个 %d 秒以上的片段里只有一个「[Shot 1]」，"+
						"从头到尾演一个连续动作，观众感觉就是「全程慢动作」（用户原话：拖沓、没抓重点）。"+
						"按《【额外】仿真人10S分镜头指令》：片段内要切 **2–4 个小镜头、每镜 1.5–4 秒**，"+
						"用官方多镜形式「[Shot 2] At 00:03.500, …」写，时间戳严格递增且各镜之和 = 该片段时长",
					len(oneTake), strings.Join(firstN(oneTake, 8), "、"), PlanOneTakeSeconds)
			}
		}

		// ── 单集节奏公式（行业实测，按**秒**卡；2026-09-28 用户要求我"上网去学"，不再闭门造车）──
		//
		// 来源：拆解 100+ 部播放破亿竖屏短剧的节奏拆解（[单集万能节奏公式](https://www.163.com/dy/article/L30RCS1005340TH8.html)），
		// 与用户《一、网文小说转剧本提示词指令》同向 —— 用户那份讲"每 80/200 字"，这份讲"每 20–30 秒"，
		// 按秒的口径对**成片节奏**更贴身，所以两条都留、这条当主判据：
		//
		//	0–3 秒  黄金钩子（无慢开场）
		//	0–10 秒 交代：核心矛盾 / 人物关系 / 主角目标
		//	前 30 秒 必须有一次反转或爽点（压抑—爆发的第一次释放）
		//	每 20–30 秒 一个情绪节点
		//	结尾 5–10 秒 强钩子，卡在情绪最高点
		//
		// 三大死坑里能机检的两条：**开场 10 秒内全是铺垫**、**中段长时间没有情绪节点**。
		if len(p.Shots) > 0 {
			// ① 开场 10 秒内不许全是铺垫（setup 是"交代"，连续两镜都在交代 = 慢开场）
			openSetup := 0
			elapsed := 0
			for _, sh := range p.Shots {
				if elapsed >= PlanOpeningSetupSeconds {
					break
				}
				elapsed += sh.Duration
				if hookTypeOf(sh) == "setup" {
					openSetup++
				}
			}
			if openSetup >= 2 && elapsed <= PlanOpeningSetupSeconds {
				add("warn", "plan.shots",
					"开场 %d 秒里连着 %d 镜都是 setup（铺垫）—— 行业实测：**短剧不存在慢开场**，"+
						"前 3 秒抓不住人后面再精彩也会被划走。开场镜要么直接冲突、要么强悬念、要么极致反差，"+
						"把「交代三件事」（核心矛盾/人物关系/主角目标）塞进**动作**里，别用铺垫讲",
					elapsed, openSetup)
			}
			// ② 前 30 秒必须有一次反转/爽点
			acc, first := 0, 0
			for _, sh := range p.Shots {
				acc += sh.Duration
				if isPayoffHook(hookTypeOf(sh)) {
					first = acc
					break
				}
			}
			if first == 0 || first > PlanFirstPayoffSeconds {
				add("warn", "plan.shots",
					"前 %d 秒内没有第一次反转/爽点（行业口径：**前 30 秒必须给一次情绪释放**，"+
						"形成「压抑→爆发」的起伏；一直压着不给反馈，观众直接弃剧）",
					PlanFirstPayoffSeconds)
			}
			// ③ 情绪节点间隔：任何 30 秒以上没有"非交代型"节拍 ⇒ 这一段是平的
			var flat []string
			gap, from := 0, p.Shots[0].ID
			for _, sh := range p.Shots {
				if gap == 0 {
					from = sh.ID
				}
				gap += sh.Duration
				if h := hookTypeOf(sh); h != "" && h != "setup" && h != "expression-beat" {
					gap = 0
					continue
				}
				if gap > PlanBeatGapSeconds {
					flat = append(flat, fmt.Sprintf("#%d–#%d（%d 秒）", from, sh.ID, gap))
					gap = 0
				}
			}
			if len(flat) > 0 {
				add("warn", "plan.shots",
					"%d 段连续超过 %d 秒没有情绪节点（%s）—— 行业实测「每 20–30 秒就要设置一处情绪节点，"+
						"不管是台词冲突、动作冲突还是隐藏信息曝光，不能长时间平淡无波澜」；"+
						"把这一段切开(内部小镜头)或提前放一个冲突",
					len(flat), PlanBeatGapSeconds, strings.Join(firstN(flat, 5), "、"))
			}
		}

		// 检查 1：钩子密度。每镜都要有 hook_type；每连续三镜至少一个 reveal/reversal/callback；
		// 开篇镜与收尾镜必须是强钩子。
		var noHook, badHook []string
		for _, sh := range p.Shots {
			h := hookTypeOf(sh)
			if h == "" {
				noHook = append(noHook, fmt.Sprintf("#%d", sh.ID))
			} else if !IsKnownHookType(h) {
				badHook = append(badHook, fmt.Sprintf("#%d=%s", sh.ID, h))
			}
		}
		if len(noHook) > 0 {
			add("warn", "plan.shots",
				"%d/%d 镜没有 hook_type —— 官方镜头表要求每镜一个钩子类型（词表：%s；缺钩子的镜：%s）",
				len(noHook), len(p.Shots), strings.Join(PlanHookTypes, "/"), strings.Join(firstN(noHook, 6), " "))
		}
		if len(badHook) > 0 {
			add("warn", "plan.shots", "hook_type 不在官方受控词表里（%s）：%s",
				strings.Join(PlanHookTypes, "/"), strings.Join(badHook, " "))
		}
		if len(p.Shots) >= PlanHookDensityWindow {
			var flat []string
			for i := 0; i+PlanHookDensityWindow <= len(p.Shots); i++ {
				hit := false
				for _, sh := range p.Shots[i : i+PlanHookDensityWindow] {
					if InList(hookTypeOf(sh), PlanHookDensityTypes) {
						hit = true
						break
					}
				}
				if !hit {
					flat = append(flat, fmt.Sprintf("#%d–#%d", p.Shots[i].ID, p.Shots[i+PlanHookDensityWindow-1].ID))
				}
			}
			if len(flat) > 0 {
				add("warn", "plan.shots",
					"%d 个「连续三镜」里一个 %s 都没有 —— 官方密度自检要求每三镜至少一个，否则这一段是平的（%s）",
					len(flat), strings.Join(PlanHookDensityTypes, "/"), strings.Join(firstN(flat, 5), "、"))
			}
		}
		first, last := p.Shots[0], p.Shots[len(p.Shots)-1]
		for _, edge := range []struct {
			label string
			sh    Shot
		}{{"开篇镜", first}, {"收尾镜", last}} {
			h := hookTypeOf(edge.sh)
			if h != "" && !InList(h, PlanHookStrongTypes) {
				add("warn", "plan.shots",
					"%s #%d 的钩子是 %q —— 官方要求开篇与收尾都落在强钩子上（%s）",
					edge.label, edge.sh.ID, h, strings.Join(PlanHookStrongTypes, "/"))
			}
		}

		// 检查 3：单镜主要角色数 ≤3（有画面动作或说台词的角色）。
		// 超了注意力会被摊薄，每个人都画不好，也更容易互相换脸。
		var crowded []string
		for _, sh := range p.Shots {
			if n := len(sh.Characters); n > PlanMaxImportantCharacters {
				crowded = append(crowded, fmt.Sprintf("#%d(%d 人)", sh.ID, n))
			}
		}
		if len(crowded) > 0 {
			add("warn", "plan.shots",
				"%d 镜的主要角色超过 %d 个（官方自检：一个镜头里带动作或台词的角色不超过 3 个）—— %s；"+
					"人多的场面请拆镜，或把不参与动作的人留成环境描述",
				len(crowded), PlanMaxImportantCharacters, strings.Join(crowded, " "))
		}
	}
}

// checkVoiceover ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkVoiceover(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 画外演出：内心活动 / 系统（技能侧 2026-09-27 新口径，用户定调）────────────────
	//
	// 口径：**内心活动由该角色的 Q 版形象演出（本人闭嘴、不演这段）；系统由全息面板＋Q 版机器人演出
	// （人闭嘴、不念系统台词）**。这两类镜的**参考图完全不同**（见 pipeline/refs.go），
	// 所以字段写错/引用悬空的后果不是"少一张图"，而是"该出 Q 版却出了本人""系统镜没有面板"。
	//
	// 门分三种：
	//   fail — 引用悬空（chibi_ids / system_assets 指向方案里不存在的卡）：渲染时那张图会被静默丢掉，
	//          玩家看到的是"Q 版没画进来"，而日志里一条错都没有。这是**数据错**，导入就该拦住。
	//   warn — 取值不认识 / 演出句没逐字写进正文 / 面板没进 prop_ids：前两者会让模型自己发挥，
	//          后者会让面板图绑不上（口径要求 system 镜的 prop_ids 必含面板）。
	if p.HasVoiceoverShots() {
		var badKind, badRefs, noClauseInner, noClauseSys, noPanel, monologue []string
		for _, sh := range p.Shots {
			k := strings.TrimSpace(sh.VoKind)
			if k == "" {
				continue
			}
			if k != VoKindInner && k != VoKindSystem && k != VoKindNarration && k != VoKindSpeech {
				badKind = append(badKind, fmt.Sprintf("#%d「%s」", sh.ID, k))
				continue
			}
			// 引用完整性：Q 版卡 / 机器人 / 面板都必须真的在方案里
			for _, id := range sh.ChibiIDs {
				if !p.HasCharacterID(id) {
					badRefs = append(badRefs, fmt.Sprintf("#%d chibi_ids→%s", sh.ID, id))
				}
			}
			if k == VoKindSystem {
				for _, key := range []string{"bot", "panel"} {
					id := strings.TrimSpace(sh.SystemAssets[key])
					if id == "" {
						badRefs = append(badRefs, fmt.Sprintf("#%d system_assets.%s 为空", sh.ID, key))
						continue
					}
					ok := false
					if key == "bot" {
						ok = p.HasCharacterID(id)
					} else {
						ok = p.HasPropID(id)
					}
					if !ok {
						badRefs = append(badRefs, fmt.Sprintf("#%d system_assets.%s→%s", sh.ID, key, id))
					}
				}
				if panel := strings.TrimSpace(sh.SystemAssets["panel"]); panel != "" && !hasString(sh.PropIDs, panel) {
					noPanel = append(noPanel, fmt.Sprintf("#%d(%s)", sh.ID, panel))
				}
			}
			// 演出句必须**逐字**写进正文（技能侧同一条纪律：字段填了不算、写进正文才算）
			switch k {
			case VoKindInner:
				if len(sh.ChibiIDs) == 0 {
					badRefs = append(badRefs, fmt.Sprintf("#%d vo_kind=inner 但 chibi_ids 为空", sh.ID))
				}
				if !PlanInnerPerformanceOK(sh.H3Prompt) {
					noClauseInner = append(noClauseInner, fmt.Sprintf("#%d", sh.ID))
				}
			case VoKindSystem:
				if !PlanSystemPerformanceOK(sh.H3Prompt) {
					noClauseSys = append(noClauseSys, fmt.Sprintf("#%d", sh.ID))
				}
			case VoKindSpeech:
				// **哑剧检查**（技能侧生成规范 §6.6 的精确口径）：画面里有人在说，
				// 却在正文里写了「嘴唇完全闭合」—— 渲出来口型不动，观众看到的是「人在演但没声」。
				// 反方向（真画外却没写闭嘴句）由 HasVoiceover 那条门管。
				//
				// 【2026-10-07 修：混合镜不算哑剧】一镜里**既有**画面里的人说话、**又有**画面外
				// 的人说话（真机第01集大量如此：祁七在画面里、舱内AI 在画面外），
				// 那句"闭着嘴"是给画外那句写的，合法且必要 —— 原来只看"正文里有没有那个串"
				// 会把这批镜全报成哑剧。判据补上"本镜有没有画面外的说话人"。
				if strings.Contains(sh.H3Prompt, VoiceoverClauseMarker) && len(OffScreenSpeakers(p, sh)) == 0 {
					monologue = append(monologue, fmt.Sprintf("#%d", sh.ID))
				}
			}
		}
		if len(badKind) > 0 {
			add("warn", "plan.shots",
				"%d 镜的 vo_kind 取值不认识（%s）——只认 inner（内心，Q 版演出）、system（系统，面板＋机器人）、narration（旁白）与 speech（画面里有人在说）；"+
					"认不出的值会被当成普通镜，观众看到的差别是「该出 Q 版的镜出了本人」",
				len(badKind), strings.Join(firstN(badKind, 4), "、"))
		}
		if len(badRefs) > 0 {
			add("fail", "plan.shots",
				"%d 处画外演出的资产引用**悬空**（%s）—— 渲染时那几张图会被静默丢掉："+
					"内心镜少了 Q 版就会把本人画进去（口径要求本人不演这段），系统镜少了面板模型每镜自己编一块屏",
				len(badRefs), strings.Join(firstN(badRefs, 4), "、"))
		}
		if len(noClauseInner) > 0 {
			add("warn", "plan.shots",
				"%d 个内心镜的正文没写「由该角色的 Q 版形象演出」那句（%s）—— 字段标了不算，"+
					"演出句必须逐字写进 h3_prompt 正文（技能侧 2026-09-27 口径）",
				len(noClauseInner), strings.Join(firstN(noClauseInner, 6), "、"))
		}
		if len(noClauseSys) > 0 {
			add("warn", "plan.shots",
				"%d 个系统镜的正文没写「全息面板＋Q 版机器人演出」那句（%s）—— 同上，演出句要逐字写进正文；"+
					"面板里**不许写任何文字**（画面文字禁止）",
				len(noClauseSys), strings.Join(firstN(noClauseSys, 6), "、"))
		}
		if len(monologue) > 0 {
			add("warn", "plan.shots",
				"%d 个 speech 镜（画面里有人在说）的正文里写了「%s」（%s）—— 渲出来是**哑剧**："+
					"口型不动，观众看到的是「人在演但没声」。只有真画外（内心/系统/旁白）才写那句",
				len(monologue), VoiceoverClauseMarker, strings.Join(firstN(monologue, 6), "、"))
		}
		if len(noPanel) > 0 {
			add("warn", "plan.shots",
				"%d 个系统镜的 prop_ids 里没有那块全息面板（%s）—— 口径要求 system 镜的 prop_ids 必含面板，"+
					"否则面板图绑不上这一镜、参考图里也就没有它",
				len(noPanel), strings.Join(firstN(noPanel, 4), "、"))
		}
	}
}

// checkNarrativeQuota ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkNarrativeQuota(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 叙事配额与自包含（技能侧《平台侧生成规范》§6.5 / §2 / §14）──────────────────
	//
	// 三条配额都是"节奏"的可机检代理（规范原话：首句台词 ≤12 s、连续无台词段 ≤12 s、戏眼 ≥5 s）：
	// 观众游离、纯视觉流拖长、情感浓度被快切消解 —— 这三种毛病此前只能靠感觉说。
	// 自包含那条（禁"同上/如上"、禁留空）针对的是**一镜一次生成**：模型没有上一镜的记忆，
	// 字段里写"同上"等于这一镜什么都没写。
	if len(p.Shots) > 0 {
		// ① 首句台词出现的时间（本方案 = 本集；平台一集一项目）
		firstLineAt, elapsed, noLineRun, worstNoLine := -1, 0, 0, 0
		for _, sh := range p.Shots {
			hasLine := len(sh.Dialogue) > 0 || strings.TrimSpace(sh.Narration) != ""
			if hasLine && firstLineAt < 0 {
				firstLineAt = elapsed
			}
			if hasLine {
				if noLineRun > worstNoLine {
					worstNoLine = noLineRun
				}
				noLineRun = 0
			} else {
				noLineRun += sh.Duration
			}
			elapsed += sh.Duration
		}
		if noLineRun > worstNoLine {
			worstNoLine = noLineRun
		}
		if firstLineAt > PlanFirstLineMaxSeconds {
			add("warn", "plan.shots",
				"本集第一句台词出现在第 %d 秒（上限 %d 秒）—— 开场太久没人在说话，观众会先游离；"+
					"把一句台词或一处人声提前到前 %d 秒内",
				firstLineAt, PlanFirstLineMaxSeconds, PlanFirstLineMaxSeconds)
		}
		if worstNoLine > PlanNoLineRunMaxSeconds {
			add("warn", "plan.shots",
				"最长连续无台词段 %d 秒（上限 %d 秒）—— 纯视觉流拖太久；"+
					"在中间插一句台词、一条字幕或一个反应镜打断它",
				worstNoLine, PlanNoLineRunMaxSeconds)
		}
		// ② 戏眼：情绪核心镜（特写/近景 ＋ climax/tender/expression-beat）必须留够时间，快切会消解情感浓度
		var quick []string
		for _, sh := range p.Shots {
			if !hasString(PlanFaceCloseSizes, sh.ShotSize) {
				continue
			}
			if !hasString([]string{"climax", "tender", "expression-beat"}, sh.HookType) {
				continue
			}
			if sh.Duration < PlanKeyBeatMinSeconds {
				quick = append(quick, fmt.Sprintf("#%d(%ds/%s)", sh.ID, sh.Duration, sh.HookType))
			}
		}
		if len(quick) > 0 {
			add("warn", "plan.shots",
				"%d 个情绪核心镜短于 %d 秒（%s）—— 特写/近景上的 climax/tender/expression-beat 是**戏眼**，"+
					"快切会让情感浓度归零（规范 §6.5：戏眼 ≥5 秒）",
				len(quick), PlanKeyBeatMinSeconds, strings.Join(firstN(quick, 6), "、"))
		}
		// ③ 自包含：禁"同上/如上"，必填的文本字段不许留空
		var sameAs, blank []string
		for _, sh := range p.Shots {
			for _, f := range []struct{ name, val string }{
				{"action", sh.Action}, {"end_state", sh.EndState}, {"camera", sh.Camera},
			} {
				if hit := sameAsAboveIn(f.val); hit != "" {
					sameAs = append(sameAs, fmt.Sprintf("#%d.%s「%s」", sh.ID, f.name, hit))
				}
			}
			// 留空：action 必填；end_state 对"每段场景的首镜"合法留空（见 Shot.EndState 的说明）
			if strings.TrimSpace(sh.Action) == "" {
				blank = append(blank, fmt.Sprintf("#%d.action", sh.ID))
			}
		}
		if len(sameAs) > 0 {
			add("fail", "plan.shots",
				"%d 处字段里写了「同上/如上」这类指代（%s）—— **一镜一次生成**，模型没有上一镜的记忆，"+
					"写指代等于这一镜什么都没写；场地/人物/光/时间/道具状态每一镜都要写全",
				len(sameAs), strings.Join(firstN(sameAs, 6), "、"))
		}
		if len(blank) > 0 {
			add("warn", "plan.shots",
				"%d 个镜头的「画面动作」是空的（%s）—— 空字段到了提示词那一层就是「这一镜没有动作」，"+
					"模型只会自己编",
				len(blank), strings.Join(firstN(blank, 6), "、"))
		}
	}
}

// checkVocabGates ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkVocabGates(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 词表类判据（技能侧《平台侧生成规范》§6.2 角度词 / §2.4 敏感词 / §4.1 场景命名 / §6.9 三人同框）
	//
	// 四条都是"照词表判"的：角度词是必填项、敏感词云端会拦、场景名禁用单一名词、
	// 三人同框不写前后关系则模型自己排（排出来的关系每一版都不一样）。
	// 词表与判据在 spec_checks.go，这里只负责把它们接到契约上。
	if len(p.Shots) > 0 {
		var badAngle, noAngle, banned, crowd []string
		for _, sh := range p.Shots {
			// 角度词**必填**（用户《五》：「运镜：[运动方式] + [角度词]（**角度必填**），
			// 例如"慢动作，低角度仰拍，镜头跟随"」）。
			//
			// 【2026-09-28 补】`AngleWordOK` 此前对**空值直接放行**（"空值合法"），
			// 于是"角度词必填"这条**从来没有机检** —— 实测分镜里大量镜头这一栏是空的，
			// 而契约一句话都不说。规范里那句"按 WARN，不拒收"说的是级别（不判 fail），
			// 不是"不报"，所以这里按 warn 报，与规范不冲突。
			if strings.TrimSpace(sh.AngleZH) == "" {
				noAngle = append(noAngle, fmt.Sprintf("#%d", sh.ID))
			} else if !AngleWordOK(sh.AngleZH) {
				badAngle = append(badAngle, fmt.Sprintf("#%d「%s」", sh.ID, sh.AngleZH))
			}
			// 敏感词：查**会进渲染的**文本（正文 + 动作），卡片侧在下面单独查
			for _, f := range []struct{ name, val string }{
				{"h3_prompt", sh.H3Prompt}, {"action", sh.Action},
			} {
				if w, hint := bannedWordingIn(f.val); w != "" {
					banned = append(banned, fmt.Sprintf("#%d.%s「%s」→ %s", sh.ID, f.name, w, hint))
				}
			}
			// 三人以上同框：正文里必须写明前后/遮挡关系
			if len(sh.Characters) >= 3 && !hasAnyMarker(sh.H3Prompt+" "+sh.Action, PlanCrowdOrderMarkers) {
				crowd = append(crowd, fmt.Sprintf("#%d(%d 人)", sh.ID, len(sh.Characters)))
			}
		}
		if len(noAngle) > 0 {
			add("warn", "plan.shots",
				"%d 镜没写机位角度词（%s）—— 用户《五》写明「角度**必填**」：不是靠模型自己猜从哪看，"+
					"而是每一镜都要点名一个（%s，组合写法如「慢动作，中景，低角度仰拍」含其一即可）",
				len(noAngle), strings.Join(firstN(noAngle, 6), "、"), strings.Join(PlanAngleWords, "/"))
		}
		if len(badAngle) > 0 {
			add("warn", "plan.shots",
				"%d 镜的机位角度词不在词表里（%s）—— 只认 %s（组合写法如「慢动作，中景，低角度仰拍」含其一即可）；"+
					"角度词是必填项，写错等于这一镜没说「从哪看」",
				len(badAngle), strings.Join(firstN(badAngle, 6), "、"), strings.Join(PlanAngleWords, "/"))
		}
		if len(banned) > 0 {
			add("warn", "plan.shots",
				"%d 处命中平台敏感词（%s）—— 这些词**云端会拦**（换后端才发现就晚了）；"+
					"按规范给的安全替代改写即可（替代写法随命中项一起列出）",
				len(banned), strings.Join(firstN(banned, 4), "；"))
		}
		if len(crowd) > 0 {
			add("warn", "plan.shots",
				"%d 个镜头三人以上同框却没写前后关系（%s）—— 规范要求「谁在前景、谁被挡、镜头怎么切」，"+
					"不写就是让模型自己排：每一版站位都不一样，观众看到的是一团人",
				len(crowd), strings.Join(firstN(crowd, 6), "、"))
		}
	}
}

// checkCardSupplements ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkCardSupplements(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 角色卡 / 道具卡 / 三视图板的补充判据（2026-09-28 欠账小项）──────────────
	//
	// 判据在 `asset_card_gates.go`，文案在这里（gatecat 扫字面格式串生成门目录）。
	if names := AgeIssues(p.Characters); len(names) > 0 {
		add("warn", "plan.characters",
			"%d 个角色的卡片里**没有年龄锚**（%s）—— 模型的默认审美是年轻化，不把年龄写成硬锚，"+
				"它每次都往年轻里画（用户报过「中年人渲染出来跟个小年轻一样」）",
			len(names), strings.Join(firstN(names, 6), "、"))
	}
	if names := BoardWhiteBackdropIssues(p.Characters); len(names) > 0 {
		add("warn", "plan.characters",
			"%d 张卡的三视图板没写**纯白无缝底**（%s）—— 板子是**规格书**（3 全身＋6 头＋6 细节）："+
				"带上场景之后模型把精力用在画环境上，15 格的身份信息被摊薄，而且底不一致就没法当角度参考用",
			len(names), strings.Join(firstN(names, 6), "、"))
	}
	if names := MaterialIssues(p.Props); len(names) > 0 {
		add("warn", "plan.props",
			"%d 件道具的材质过于单一（%s）—— 《二》对服装要求「材质对撞至少 4 种高级材质」，"+
				"道具没有这条；而道具图是拿卡当提示词单独出的，材质单薄就是「一块塑料」的来源",
			len(names), strings.Join(firstN(names, 6), "、"))
	}
	if names := ScenePropGroupIssues(p.Scenes); len(names) > 0 {
		add("warn", "plan.scenes",
			"%d 个场景卡一处环境配套物件都没提（%s）—— 用户《四》§4.6：「每个场景总计控制在 2-4 组，"+
				"前景 1-2 组、中景 1-2 组」。这条只判**下限方向**（一处都没提必然错），"+
				"不猜够不够：平台数不出「组」——要数得准得让技能侧先给 `fixed_props` 字段",
			len(names), strings.Join(firstN(names, 6), "、"))
	}

	// 角色卡的敏感词与场景命名（卡片侧）
	var cardBanned, shortNames []string
	for _, ch := range p.Characters {
		if w, hint := bannedWordingIn(ch.ImagePrompt + " " + ch.BoardPrompt); w != "" {
			cardBanned = append(cardBanned, fmt.Sprintf("%s「%s」→ %s", ch.Name, w, hint))
		}
	}
	for _, sc := range p.Scenes {
		if n := len([]rune(strings.TrimSpace(sc.Name))); n > 0 && n < PlanMinSceneNameRunes {
			shortNames = append(shortNames, fmt.Sprintf("「%s」", sc.Name))
		}
		if w, hint := bannedWordingIn(sc.ImagePrompt); w != "" {
			cardBanned = append(cardBanned, fmt.Sprintf("场景%s「%s」→ %s", sc.Name, w, hint))
		}
	}
	for _, pr := range p.Props {
		if w, hint := bannedWordingIn(pr.ImagePrompt + " " + pr.Description); w != "" {
			cardBanned = append(cardBanned, fmt.Sprintf("道具%s「%s」→ %s", pr.Name, w, hint))
		}
	}
	if len(cardBanned) > 0 {
		add("warn", "plan.cards",
			"%d 处卡片文本命中平台敏感词（%s）—— 卡片是所有参考图的源头，命中一次会跟着**每一镜**",
			len(cardBanned), strings.Join(firstN(cardBanned, 3), "；"))
	}
	if len(shortNames) > 0 {
		add("warn", "plan.scenes",
			"%d 个场景名短于 %d 字（%s）—— 规范要求「四字以上、带修饰词」，严禁单一名词："+
				"「城门」是地名占位，「临水城·城门·雨夜」才交代得清地点与时间",
			len(shortNames), PlanMinSceneNameRunes, strings.Join(firstN(shortNames, 6), "、"))
	}
}

// checkMeasuredGates ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkMeasuredGates(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 2026-09-28 实测驱动的两条（真渲 48 镜的最低四镜失败方式完全相同）──────────
	//
	// ① 小景别 ＋ 手部工具精细动作 = 生成必崩（实测最低四镜全部如此：手指融合、多指、
	//    与刀柄粘成一团）。提示词里"手指细小动作不要写"那条**不够** —— 模型照写，所以要机检。
	// ② 把**可读文字**写到皮肤/衣物/器物上（`scar reading "丙十七"`）= 必出伪汉字
	//    （实测额头与碑面都糊成了无意义字形）。
	if len(p.Shots) > 0 {
		var handClose, bodyText []string
		for _, sh := range p.Shots {
			body := sh.Action + " " + sh.H3Prompt
			if handToolCloseUp(sh.ShotSize, body) {
				handClose = append(handClose, fmt.Sprintf("#%d(%s)", sh.ID, sh.ShotSize))
			}
			// 只扫**本镜自己的动作**：正文每镜都复述角色卡的身份锁，扫正文会得到同源误报一堆
			if w := bodyReadableText(sh.Action); w != "" {
				bodyText = append(bodyText, fmt.Sprintf("#%d「%s」", sh.ID, w))
			}
		}
		// 卡片侧各报一次（真正要改的是那张卡：疤写着 `reading "丙十七"`）
		for _, ch := range p.Characters {
			if w := bodyReadableText(ch.ImagePrompt + " " + ch.BoardPrompt + " " + ch.Description); w != "" {
				bodyText = append(bodyText, fmt.Sprintf("卡片「%s」命中「%s」", ch.Name, w))
			}
		}
		if len(handClose) > 0 {
			add("warn", "plan.shots",
				"%d 个镜头是**小景别 ＋ 手部工具精细动作**（%s）—— 实测这一类**生成必崩**"+
					"（第一集最低四镜全是它：手指融合、多指、手与刀柄粘成一团）。改法：工具活一律用**中景或更宽**，"+
					"动作写到**手腕/手臂**层，别写哪根手指做哪个动作；要展示成果就把镜头对着**石面/器物本身**的变化",
				len(handClose), strings.Join(firstN(handClose, 5), "、"))
		}
		if len(bodyText) > 0 {
			add("warn", "plan.shots",
				"%d 处把**可读文字**写到皮肤/衣物/器物上（%s）—— 文字进画面是 T2V 的老大难；"+
					"实测同一集额头印记与碑面都被画成了伪汉字（例如「丙十十」这种无意义重复）。改法："+
					"疤/烙印/纹样写成「磨损不可辨」；剧情非要读的**唯一**那处，按官方屏幕文字规则"+
					"**用英文双引号逐字引一次**，其余面一律写明「风化无字」",
				len(bodyText), strings.Join(firstN(bodyText, 5), "、"))
		}
	}
}

// checkCardCompleteness ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkCardCompleteness(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 三类卡片的**完备性**（2026-09-28 用户问「角色/场景/道具的提示词是完成了还是未完成」，
	// 拿真方案逐项量过：**基本完成**，但有三处小缺口会一直静默存在，所以各立一道门）──────
	//
	// ① 人形角色卡缺**比例锚点**（实测 5 张里 1 张没有）。比例是用户点名的口径
	//    （《二、人物道具提取指令》9 头身 /《【额外】人物道具场景提取》8.5 头身），缺了模型就自己定，
	//    同一集里两个角色的头身比会互相打架。
	// ② 道具卡不完整（缺 id/名/英文串/中文描述/出图提示词，或出图提示词没有 1:1 纯白底单件）：
	//    道具图是拿卡当提示词单独出的（《二》：纯白背景、单独呈现无遮挡），缺项会出一张带场景的道具图，
	//    之后跨镜锚定的外观就不干净。
	// ③ 分镜引用了方案里没有的器物（RemapPropRefs 会尽量对号，对不上就丢）—— 丢弃必须**如实报**，
	//    因为那件器物在这一镜又变成模型自己编的。
	if len(p.Characters) > 0 {
		var noProportion []string
		for _, c := range p.Characters {
			if IsChibiCard(c.Kind, c.ID) || strings.TrimSpace(c.ImagePrompt) == "" {
				continue
			}
			if !containsAny(c.ImagePrompt, "head-tall", "heads tall", "proportion", "头身") {
				noProportion = append(noProportion, c.Name)
			}
		}
		if len(noProportion) > 0 {
			add("warn", "plan.characters",
				"%d 张人形角色卡没有**比例锚点**（%s）—— 比例是用户定的口径（8.5 头身／九头身），"+
					"缺了模型就自己定，同一集里两个角色的头身比会互相打架；写进 image_prompt："+
					"「eight and a half heads tall, lean, straight-backed」",
				len(noProportion), strings.Join(firstN(noProportion, 6), "、"))
		}
	}
	if len(p.Props) > 0 {
		var badProp []string
		for _, pr := range p.Props {
			lack := ""
			switch {
			case strings.TrimSpace(pr.ID) == "":
				lack = "缺 id"
			case strings.TrimSpace(pr.Name) == "":
				lack = "缺中文名"
			case strings.TrimSpace(pr.EN) == "":
				lack = "缺英文外观串"
			case strings.TrimSpace(pr.Description) == "":
				lack = "缺中文描述（材质/配色/尺寸/记号）"
			case strings.TrimSpace(pr.ImagePrompt) == "":
				lack = "缺出图提示词"
			case !containsAny(pr.ImagePrompt, "1:1", "square", "white background", "纯白"):
				lack = "出图提示词里没有 1:1 纯白底单件"
			}
			if lack != "" {
				badProp = append(badProp, fmt.Sprintf("%s(%s)", pr.Name, lack))
			}
		}
		if len(badProp) > 0 {
			add("warn", "plan.props",
				"%d 张道具卡不完整（%s）—— 道具图是拿卡当提示词单独出的（《二》：纯白背景、单独呈现无遮挡），"+
					"缺项会出一张带场景的道具图，之后跨镜锚定的外观就不干净",
				len(badProp), strings.Join(firstN(badProp, 6), "、"))
		}
	}
	if n := countDanglingPropRefs(p); n > 0 {
		add("warn", "plan.props",
			"%d 处分镜的器物引用指向方案里**没有的道具卡**（对不上号会被丢弃）—— 那些镜头的器物又会变成"+

				"模型自己编的。修法：把这几件器物真正收成道具卡（《一、网文小说转剧本提示词指令》："+
				"关键场景、道具、伏笔细节要补齐）", n)
	}
}

// checkShotCount ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkShotCount(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 镜头数与片段时长（用户 2026-09-28：「为什么还是存在那么多的镜头？」）──────────────
	//
}

// checkNoBgmPerShot ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkNoBgmPerShot(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 片段里不许各带配乐（官方 qc-checklist STEP 8：「Do not generate BGM per shot」）────
	//
	// 平台此前每镜都要求写 non_diegetic_music —— 每个片段里 H3 自己生成一段曲子，30+ 个片段
	// 拼起来就是 30+ 段各弹各的，每个接缝都在换调（听感"不对"的一大来源）。
	// 现在口径：片段里一律 N/A，配乐由合片阶段铺**一条连续 BGM**（compose/bgm.go）。
	//
	// ⚠ **有意分歧 DEV-05（勿当 bug 修）**：官方说 N/A 只用于"确无画外配乐"——而平台**要求**每镜都写 N/A，
	// 因为官方同时禁止逐镜配乐（qc-checklist STEP 8）。片段里确实不该有画外配乐 ⇒ 写 N/A 是如实描述，
	// 不是打折扣。见 docs/与官方的有意分歧.md。
	// 环境声不在此列：overall_soundscape 本来就该逐镜写，那是画面里的声音。
	if len(p.Shots) > 0 {
		var withMusic []string
		for _, sh := range p.Shots {
			if sec, ok := musicSectionOf(sh.H3Prompt); ok && !isNA(sec) {
				withMusic = append(withMusic, fmt.Sprintf("#%d", sh.ID))
			}
		}
		if len(withMusic) > 0 {
			add("warn", "plan.music",
				"%d 镜在片段里写了配乐（%s）—— 官方终检要求「**Do not generate BGM per shot**」："+
					"每个片段是独立生成的，逐镜配乐会让成片在每个接缝处换调；配乐应由合片阶段铺**一条连续 BGM**"+
					"（项目参数 assemble.bgm 指向音乐文件）。片段里写 N/A，环境声照旧写 overall_soundscape",
				len(withMusic), strings.Join(firstN(withMusic, 6), "、"))
		}
	}
}

// checkPositionsAndExit ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkPositionsAndExit(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 人物位置与退场状态（官方 `shot-table-spec.md` STEP 5：Reference Anchors 四个子字段全必填）──
	//
	// 平台此前只有"地标"与"光态"两个子字段，`Character Positions (camera view)` 与
	// `Exited Character Status` **连字段都没有** ⇒ 位置只散在散文里，"人物凭空出现/凭空消失"
	// 没有任何判据。两条门：
	//   ① 出场角色必须在 positions 里有位置（缺了等于让模型自己摆人）；
	//   ② 上一镜在场、本镜不在场的角色必须写进 offscreen 且**至少保留一镜**（官方纪律：
	//      连续 2 镜明确不在场后才可移除）—— 这条让"人不见了"变成显式信息。
	if len(p.Shots) > 0 {
		var noPos, noExit []string
		for i, sh := range p.Shots {
			for _, cid := range sh.Characters {
				if strings.TrimSpace(sh.Positions[cid]) == "" {
					noPos = append(noPos, fmt.Sprintf("#%d/%s", sh.ID, cid))
				}
			}
			if i == 0 {
				continue
			}
			prev := p.Shots[i-1]
			for _, cid := range prev.Characters {
				if hasString(sh.Characters, cid) {
					continue // 本镜还在场
				}
				// 上一镜在场、本镜不在场 ⇒ 必须在 offscreen 里交代（官方：至少保留一镜）
				if strings.TrimSpace(sh.Offscreen[cid]) == "" {
					noExit = append(noExit, fmt.Sprintf("#%d→#%d/%s", prev.ID, sh.ID, cid))
				}
			}
		}
		if len(noPos) > 0 {
			add("warn", "plan.shots",
				"%d 处出场角色**没有写位置**（%s）—— 官方分镜表要求每行都有 `Character Positions (camera view)`"+
					"（画面左/中/右、前/中/后景、朝向与初始姿态）；缺了等于让模型自己摆人，同一场戏两镜的站位就会漂",
				len(noPos), strings.Join(firstN(noPos, 6), "、"))
		}
		if len(noExit) > 0 {
			add("warn", "plan.shots",
				"%d 处**人物凭空消失**（%s）—— 上一镜还在画面里的角色，这一镜既不在场、也没写离屏状态。"+
					"官方纪律：已离屏角色至少保留一镜（写清去哪了、为什么），连续 2 镜明确不在场后才可移除",
				len(noExit), strings.Join(firstN(noExit, 6), "、"))
		}
	}
}

// checkCoreBeats ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkCoreBeats(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 核心提炼（用户 2026-09-28：「LLM 需要抓住文字内容中的重点总结输出核心分镜脚本，
	// 而不是一味的照搬原文内容」）────────────────────────────────────────────────
	//
	// 平台此前给 LLM 的指令是反的（"必须沿原文叙事节拍逐段改编、不得跳段、不得省略因果链"），
	// 改编于是退化成抄写：铺垫、支线、絮叨对白全进分镜。现在要求**先提炼、再取舍**，
	// 并把提炼结果落成字段 —— 有了字段，这件事才可机检（不然"抓重点"永远只是一句劝告）。
	//
	// 判据：core_beats 少于 3 条（1 条核心冲突 + 2-3 个爽点）⇒ 报。宁少勿凑不等于可以不写。
	if len(p.CoreBeats) < 3 {
		add("warn", "plan.core",
			"本集**核心提炼只有 %d 条**（core_beats）—— 用户口径是「抓住文字内容中的重点，总结输出核心分镜脚本，"+
				"而不是一味照搬原文」。至少要写清：①本集核心冲突是什么 ②2-3 个大爽点（逆袭/打脸/揭秘/升阶）分别是哪几处。"+
				"有了这份提炼，分段与镜头才有取舍的依据；没有它，分镜只能是原文的段落表",
			len(p.CoreBeats))
	}

	// 对过口径：用户的分镜文件写的是「**非最后一个片段时长优先在 7、8、9、10 秒中选择**，
	// 最后一个按实际剩余」，而平台提示词里此前写的是"镜头数由内容决定、**没有上限**" ——
	// 于是模型给了 35 镜（均 6.8 秒）、上一集 55 镜。**每多一镜就是多一次 GPU 生成**：
	// 更慢、更贵、多一个接缝、多一次漂移机会，而且观感碎而不是紧。
	//
	// 判据（都在"有足够多镜"时才判，夹具那种 1-2 镜的方案不该被咬）：
	//   ① 非末镜里短于 7 秒的占比过高 ⇒ 镜头过碎；
	//   ② 镜数明显多于"总时长 ÷ 8.5" ⇒ 与本集时长不成比例（口径是每片段 7-10 秒）。
	// 时长要短、节奏要快，靠的是**片段内切 2-4 个小镜头**（见 plan.shots 的"一镜到底"门），
	// 不是靠把片段切碎 —— 这两件事必须分清楚。
	if len(p.Shots) >= 6 {
		total := 0
		short, nonFinal := 0, 0
		for i, sh := range p.Shots {
			total += sh.Duration
			if i == len(p.Shots)-1 {
				continue // 末镜按剩余时长，不套 7-10 秒
			}
			nonFinal++
			if sh.Duration < PlanPreferredShotSeconds {
				short++
			}
		}
		if nonFinal > 0 && short*100/nonFinal >= PlanShortClipSharePct {
			add("warn", "plan.shots",
				"%d/%d 个非末尾片段的时长短于 %d 秒 —— 用户的分镜口径是「非最后一个片段时长优先在 7、8、9、10 秒中选择」，"+
					"镜头数要由**本集时长**推出来（约 总秒数 ÷ 8.5），不是一镜一个节拍。镜头越碎：每多一镜就多一次 GPU 生成、"+
					"多一个接缝、多一次漂移机会，观感还更碎。**要快靠片段内切 2-4 个小镜头，不是靠切碎片段**",
				short, nonFinal, PlanPreferredShotSeconds)
		}
		if want := int(float64(total) / PlanShotSecondsRef); want > 0 && len(p.Shots) > want*6/5 {
			add("warn", "plan.shots",
				"本集 %d 秒却有 %d 镜（按时长口径约 %d 镜就够，每片段 7-10 秒）—— 多出的每一镜都是一次完整视频生成："+
					"同样的内容用更少的片段装完，既省钱又不碎",
				total, len(p.Shots), want)
		}
	}
}

// checkKeyProps ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkKeyProps(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 关键道具（2026-09-28 用户：「关键物品生成道具啊，前后镜都漂移了」）──────────
	//
	// 实测病根：某真实项目第一集的 plan **连 props 字段都没有**：25 个镜头提到碑刀/烙铁/名册，
	// `prop_ids` 全空 —— 每镜由模型自己发明那把刀，形状缠柄一镜一个样 = 观众看到的漂移。
	// 道具不进参考图集合（官方规则），跨镜一致**只能靠文字**，所以"没有卡"就等于"没有一致性"。
	if len(p.Shots) > 0 {
		if len(p.Props) == 0 {
			add("warn", "plan.props",
				"本集**一件道具都没提取** —— 招牌物件（兵器/法器/书信/信物/器具）不进卡片，就没有任何东西"+
					"能约束它跨镜一致：每一镜都由模型自己发明一件，观众看到的就是**前后镜漂移**"+
					"（实测那一集：25 镜提到刀/烙铁/名册、0 张道具卡）。收 2–6 件反复出现或参与关键剧情的物件成卡")
		} else {
			propIDs := map[string]bool{}
			for _, pr := range p.Props {
				propIDs[strings.TrimSpace(pr.ID)] = true
			}
			var dangling, unbound []string
			for _, sh := range p.Shots {
				for _, id := range sh.PropIDs {
					if !propIDs[strings.TrimSpace(id)] {
						dangling = append(dangling, fmt.Sprintf("#%d→%s", sh.ID, id))
					}
				}
				if len(sh.PropIDs) == 0 && hasAnyMarker(sh.Action, PlanPropWords) {
					unbound = append(unbound, fmt.Sprintf("#%d(%s)", sh.ID, firstPropWord(sh.Action)))
				}
			}
			if len(dangling) > 0 {
				add("fail", "plan.props",
					"%d 处 prop_ids 指向方案里不存在的道具（%s）—— 装配时那件器物的锚定文本会落空，"+
						"这一镜的刀/器物又变成模型自己编的",
					len(dangling), strings.Join(firstN(dangling, 5), "、"))
			}
			if len(unbound) > 0 {
				add("warn", "plan.props",
					"%d 个镜头的动作里出现了器物却没写 prop_ids（%s）—— 这类镜是**漂移的高发区**："+
						"不带锚定就等于让模型每镜重新设计一件同样的东西",
					len(unbound), strings.Join(firstN(unbound, 6), "、"))
			}
		}
	}
}

// checkSceneBoardRich ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkSceneBoardRich(p *Plan, add func(level, where, format string, a ...any)) {
	// ── 场景板贫瘠（用户 2026-09-28：「场景简陋单一导致分镜内容一点也不理想」）────────
	//
	// 场景板是**每一镜的空间底座**：板子简陋，分镜就只能在简陋的空间里拍（用户原话）。
	// 判据见 ScenePlateThin（建立镜头串 / 大景元素≥2 / 光的质感），按《四、场景提示词指令》
	// 的意图翻译成机检，而不是搬它的中文词表。
	if len(p.Scenes) > 0 {
		var thin []string
		for _, sc := range p.Scenes {
			if lack := ScenePlateThin(sc.ImagePrompt); len(lack) > 0 {
				thin = append(thin, fmt.Sprintf("「%s」缺 %s", sc.Name, strings.Join(lack, "＋")))
			}
		}
		if len(thin) > 0 {
			add("warn", "plan.scenes",
				"%d 个场景板偏**贫瘠**（%s）—— 场景板是每一镜的空间底座，板子简陋，分镜就只能在简陋的空间里拍。"+
					"按五步公式补全：① 光的质感与情绪 ② 核心主体与背景的精准名词 ③ **光在材质上怎么走** "+
					"④ 与项目风格匹配的画质后缀 ⑤ 2–4 组固定陈设（前景 1–2 组、中景 1–2 组）",
				len(thin), strings.Join(firstN(thin, 4), "；"))
		}
	}
}

// checkH3WordCount ……（搬运自原 ValidatePlan，判据与注释均未改动）
func checkH3WordCount(p *Plan, add func(level, where, format string, a ...any)) {
	// ── H3 正文词数（官方 ref-en.txt §5.2：生成任务 350–500 词，"单镜也不能因此写短"）──
	//
	// 2026-09-28 从官方仓库同步时实测：平台把这条当"目标而非配额"，真方案 48 镜落在
	// 633–1407 词（中位 ~990）≈ 官方上限两倍。太长会摊薄注意力，太短是描写不足 —— 两头都报。
	if len(p.Shots) > 0 {
		var short, long []string
		total := 0
		for _, sh := range p.Shots {
			n := H3DescriptionWords(sh.H3Prompt)
			if n == 0 {
				continue // 空正文由别的门报，这里不重复
			}
			total += n
			if n < H3WordsFloor {
				short = append(short, fmt.Sprintf("#%d(%d 词)", sh.ID, n))
			} else if n > H3WordsCeil {
				long = append(long, fmt.Sprintf("#%d(%d 词)", sh.ID, n))
			}
		}
		if len(short) > 0 {
			add("warn", "plan.shots",
				"%d 镜的正文偏短（%s，官方 350–500 词）—— 描写不足时模型只能自己补画面；"+
					"官方还特别说明「单镜也不能因此写短」，一镜也要按信息量写满",
				len(short), strings.Join(firstN(short, 6), "、"))
		}
		if len(long) > 0 {
			add("warn", "plan.shots",
				"%d 镜的正文偏长（%s，官方 350–500 词；本方案均值 %d 词）—— 超长会把注意力摊薄在无关句子上，"+
					"真正该重的动作反而糊过去；把重复的服装/道具复述压掉，只留这一镜自己发生的事",
				len(long), strings.Join(firstN(long, 6), "、"), total/len(p.Shots))
		}
	}

	// 检查 3'（场景卡侧）：官方 STEP 4 要求场景卡写"连续性地标"与"光态"。
	// 这两项是**跨镜空间一致**的锚点：地标换位置、光态跳时间，观众立刻觉得"不是同一个地方"。
	if len(p.Scenes) > 0 {
		var missing []string
		for _, s := range p.Scenes {
			var lack []string
			if strings.TrimSpace(s.Landmarks) == "" {
				lack = append(lack, "landmarks")
			}
			if strings.TrimSpace(s.LightState) == "" {
				lack = append(lack, "light_state")
			}
			if len(lack) > 0 {
				missing = append(missing, fmt.Sprintf("%s(%s)", s.Name, strings.Join(lack, "+")))
			}
		}
		if len(missing) > 0 {
			add("warn", "plan.scenes",
				"%d/%d 个场景卡缺「连续性地标 landmarks」或「光态 light_state」—— 官方场景卡规范要求两项都写；"+
					"它们是跨镜空间与时间一致的锚点（%s）",
				len(missing), len(p.Scenes), strings.Join(firstN(missing, 5), "、"))
		}
	}

	// 镜头号连续性:平台按 id 落产物文件名,断号不致命但会让"缺镜"看起来像故障。
	//
	// 【拆分说明】原来这里用的是函数级变量 `seen`（前半段"遍历镜头"时建的 id 集合）。
	// 拆成独立函数后不再跨组共享索引 —— 这里自己数一遍：判据只看"有几个不同的 id"，
	// 3 行就够，比多传一个 map 进 15 个函数更不容易接错（真正需要那些索引的只有第一组）。
	maxID := 0
	ids := map[int]bool{}
	for _, sh := range p.Shots {
		if sh.ID > maxID {
			maxID = sh.ID
		}
		ids[sh.ID] = true
	}
	if maxID > 0 && len(ids) != maxID {
		add("warn", "plan.shots", "镜头号不连续(1..%d 共 %d 个)—— 缺号会让产物清单看起来像丢镜",
			maxID, len(ids))
	}
}

// checkEntityRefs 分镜引用的**人物 / 场景**必须在卡片表里（2026-09-29 真机实测驱动）。
//
// 【这条门为什么存在】五步专职会话的 ⑤分镜 输出的是**名字**（「characters」里写角色名），
// 而上游字段语义是 id。归一（RemapEntityRefs）能把"名字写法"这一类对上号，但**对不上**的
// 那些是真没有卡：渲染装配取不到参考图 ⇒ 该镜降级 t2va（角色逐镜换脸、场景每镜重编），
// 而资产阶段照报"资产就绪"，界面上看不出任何异常。
//
// 判据取**归一之后**的悬空数：能自动修的（名字→id）不该报警，剩下的才需要人看一眼。
//
// **新门接在文件末尾**：门编号按源码顺序生成（tools/gatecat），插在中间会让后面所有门的
// 编号整体后移 —— 而口头定位（「G014 太严」）与文档引用用的正是编号。
func checkEntityRefs(p *Plan, add func(level, where, format string, a ...any)) {
	chars, scenes := DanglingEntityRefs(p)
	if chars > 0 {
		add("warn", "plan.shots",
			"%d 处镜头引用的**人物**对不上角色卡（渲染时这一镜拿不到定妆照参考，会降级成纯文生，"+
				"同一角色在成片里逐镜换脸）—— 修法：要么把这些人物补进角色卡，要么把这些镜头的人物"+
				"改成卡片里的写法（平台会把名字自动对号，这里报的是**对不上**的那些）", chars)
	}
	if scenes > 0 {
		add("warn", "plan.shots",
			"%d 处镜头引用的**场景**对不上场景卡（这一镜拿不到环境参考图，背景会每镜重编）——"+
				"修法：把场景补成场景卡，或把引用改成卡片里的写法", scenes)
	}
}

// EmptyPromptShotIDs 哪些镜的 `h3_prompt` 是空的（渲染会被致命门拦住的那几镜）。
//
// 【为什么要单独有个查询】这条缺陷此前只在**渲染阶段**才以 fatal 出现 —— 而那一集
// 15 件资产已经全部生成完（真机《外卖员写了七千封回信》第01集镜头 8，2026-10-08），
// 用户看到的是"跑了一大段然后整条产线中止"。剧本阶段在存盘前用它**点名**，
// 让用户在资产阶段之前就知道是哪几镜、该怎么办（脚本阶段自己会先尝试自纠一轮补写）。
func EmptyPromptShotIDs(p *Plan) []int {
	if p == nil {
		return nil
	}
	var out []int
	for i := range p.Shots {
		if strings.TrimSpace(p.Shots[i].H3Prompt) == "" {
			out = append(out, p.Shots[i].ID)
		}
	}
	return out
}
