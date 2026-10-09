package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

func (c *Coordinator) runScript(ctx context.Context, proj *project.Project) error {
	c.emit(proj.ID, PhaseScript, 0, "info", 5, "读取源文本…")
	text, err := c.store.LoadSource(proj.ID)
	if err != nil {
		return fmt.Errorf("读取源文本失败: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("源文本为空:请先在「剧本」中输入故事/创意文本")
	}
	g := c.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	p := proj.Config.Params.Script
	c.emit(proj.ID, PhaseScript, 0, "info", 15, "LLM 直出分镜(%s,%d 镜约 %d 秒)…", g.LLM.Model, p.MaxShots, p.TotalSeconds)
	c.emit(proj.ID, PhaseScript, 0, "info", 15, "深度推理模型单次可思考数分钟属正常,期间每 30 秒报一次心跳,请勿提前停止")

	// 心跳:LLM 推理期间持续可见进度,避免「看起来卡死」被误停
	hbCtx, hbStop := context.WithCancel(ctx)
	defer hbStop()
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		start := time.Now()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-tick.C:
				c.emit(proj.ID, PhaseScript, 0, "info", 15, "⏳ LLM 仍在推理…已等待 %d 秒(推理模型深度思考,通常 2-6 分钟)", int(time.Since(start).Seconds()))
			}
		}
	}()

	// 书级角色锚定:系列前几集已确立的角色/场景名注入提示,强制沿用同名
	// ——配合资产库按名复用,杜绝同角色跨集换脸(漂移)。实证:貔貅第01集
	// 「貅满堂」到第02集被 LLM 写成「貅满仓」,一字之差即匹配失效重生成。
	hint := composeStyleHint(p.Styles, p.StyleHint)
	// 【2026-10-08 用户口径】剧本与原文**分工**：分镜以【本集剧情】(= 剧本) 为主源，小说原文只是素材。
	//
	// 这里不再让模型"从原文提炼/重建分镜" —— 那是**专职编剧会话**的活（见 server/novels_jobs.go：
	// 它会产出【开场爆点】+ 逐场「目的/冲突/转折/爽点/钩子」+ 正文，还有契约门与补写一轮）。
	// 分镜阶段再让模型"从原文提炼"一遍，等于把编剧的活重做、且两头都不作数（用户 2026-10-08：
	// 「需要的是 LLM 提取内容分析总结后输出爽文爆点剧本（根据剧本内容再完善输出分镜脚本），
	// 而不是去要求原文内容怎样怎样」）。剧本**缺失**时如实播报，不静默按原文硬跑。
	brief, _, hasBrief, hasOrig := project.EpisodeSourceParts(text)
	if hasBrief && strings.TrimSpace(brief) == "" {
		// 标记在、内容是空的：建集时写过壳、编剧会话还没轮到这一集（或被打断）。
		hasBrief = false
	}
	switch {
	case hasBrief:
		if hint != "" {
			hint += "\n"
		}
		hint += "SCRIPT FIRST: 本集已有**剧本**(【本集剧情】,专职编剧产出:开场爆点 + 逐场「目的/冲突/转折/爽点/钩子」+ 正文)。" +
			"分镜**逐场**按剧本组织:第 1 镜拍【开场爆点】那一下、台词逐字取自剧本、每个大爽点都要有对应镜头;" +
			"原文(若有)只用于补细节与核实,**不得**用它替换或重排剧本的场次/爽点/钩子。"
		if hasOrig {
			c.emit(proj.ID, PhaseScript, 0, "info", 10,
				"📖 本集以**剧本**为准排分镜（剧本 %d 字，另附小说原文作参考素材）—— 不逐段照搬原文",
				len([]rune(brief)))
		} else {
			c.emit(proj.ID, PhaseScript, 0, "info", 10,
				"📖 本集以**剧本**为准排分镜（剧本 %d 字）", len([]rune(brief)))
		}
	case hasOrig:
		// 剧本缺失：正常链路里它由专职编剧会话产出（批量产线轮到这一集时、或手动跑阶段时由
		// `s.ensureEpisodeBrief` 兜底）。走到这里是"还没有剧本"——必须说出来，不能假装有。
		c.emit(proj.ID, PhaseScript, 0, "warn", 10,
			"⚠ 本集**没有剧本**（材料里找不到【本集剧情】，或它是空的）—— 分镜只能按小说原文组织。"+
				"正常链路里剧本由专职编剧会话产出：批量产线轮到这一集时会先改编；手动跑请从「视频制作」那一侧"+
				"重跑本集（平台会先补剧本再出分镜）")
		if hint != "" {
			hint += "\n"
		}
		hint += "NO SCRIPT: 本集**没有剧本**，只能按小说原文组织分镜:先提炼核心冲突与 2-3 个大爽点," +
			"砍冗余、允许跳段与合并,不要围着原文段落顺序排镜头。"
	}
	if roster, nChars := c.bookRoster(proj.ID); roster != "" {
		if hint != "" {
			hint += "\n"
		}
		hint += roster
		c.emit(proj.ID, PhaseScript, 0, "info", 10, "🔁 系列角色锚定:沿用本书已确立角色名 %d 个(跨集不换脸)", nChars)
	}
	// 设定集（用户口径：`data/小说/<作品>/设定集/`）——**接进会话**。
	//
	// 【2026-09-28 用户架构】读取器早就有了，但没有任何会话消费它 —— 那些锚白白躺在磁盘上，
	// 模型每次自己编：「现在是哪一年」集集不同、角色的年龄与疤痕每集漂、
	// 道具的功能边界（"托不起人"这种情节钥匙）被忽略，而锚表里逐项写着。
	settingBrief := ""
	if sb := c.settingBookForProject(proj.ID); sb != nil && sb.HasContent() {
		settingBrief = sb.SettingBrief(false) // 风格卡不进这里：画风另有 styleHint 的专门通道
		c.emit(proj.ID, PhaseScript, 0, "info", 10,
			"📌 已接入设定集：%d 个外形锚 / %d 件道具 / %d 条数字锚（**硬锚**，模型不得另编）",
			len(sb.Characters), len(sb.Props), len(sb.Numbers))
	}
	if hint != "" && settingBrief != "" {
		// 设定集放**提示词最前**（在 hint 之前由 Options.SettingBrief 注入），
		// 这里只保证两者不互相吞掉。
		hint += "\n"
	}
	dopt := director.Options{
		MaxShots: p.MaxShots, TotalSeconds: p.TotalSeconds,
		ExtraHint: hint, Chibi: p.Chibi,
		AnimeFaces:   p.FacesOn(),
		SettingBrief: settingBrief,
		// 镜头数安全网(设置页 split.shot_limit):此前只在界面上存在、没有读取点,
		// 内容驱动模式下上限永远是代码里的常量(审计 P1-1)。
		ShotLimit: c.cfg.Get().Split.ShotLimit,
		OnBatch: func(batch, total, fromShot, toShot int) {
			c.emit(proj.ID, PhaseScript, 0, "info", 15, "▸ 分批分镜 %d/%d:镜 %d-%d 生成中…", batch, total, fromShot, toShot)
		},
		// 降级/兜底提示直接进前端事件流:静默降级等于骗人
		// (典型:规划轮输出被截断,补全后继续 —— 必须让用户知道并有机会重跑)
		OnNote: func(format string, args ...any) {
			// **级别按内容判**（2026-09-29 用户：「运行日志中的函数方法调用、已确认、已入库这种是
			// 完成的类型，为什么标红？请好好优化一下所有日志的类型，目前异常」）。
			//
			// 这里原来**一律发 warn** —— 于是分镜的自愈播报（「✔ 已把 12 处分镜里的角色/场景对上
			// 方案里的卡」「✔ 给 8 镜补上了机位」「✔ 把 2 张同名卡并成一张」）全被标成红的，
			// 用户看到的就是"完成的事被当成错误报"。级别本来就写在文案的首字符里（✔/⚠/💡），
			// 按它判即可 —— 别让"发布通道"决定语义。
			msg := fmt.Sprintf(format, args...)
			level := "info"
			switch {
			case strings.HasPrefix(strings.TrimSpace(msg), "✔"):
				level = "success"
			case strings.HasPrefix(strings.TrimSpace(msg), "⚠"):
				level = "warn"
			}
			c.emit(proj.ID, PhaseScript, 0, level, 15, "%s", msg)
		},
	}
	// **选路**：五步专职会话（用户 2026-09-28 架构）或单轮直出（老路）。
	//
	// 老路保留：它是经过验证的那条，新路要用户主动切（设置页 split.five_sessions）。
	var plan *project.Plan
	if c.cfg.Get().Split.FiveSessions {
		c.emit(proj.ID, PhaseScript, 0, "info", 12,
			"🎬 本集拆解：先定场景，再同时排人物道具与分镜")
		plan, err = director.GenerateSessions(ctx, client, text, settingBrief, dopt)
	} else {
		plan, err = director.Generate(ctx, client, text, dopt)
	}
	hbStop()
	<-hbDone
	if err != nil {
		return err
	}
	// 台词时长预算:LLM 常把 20 字台词塞进 4 秒镜头,H3 只能把话说到被截断为止
	// (2026-09-24 用户报「配音也不完整」)。存盘前逐镜核算并抬时长,抬不动的
	// 如实报出来让用户拆镜 —— 这层必须在存盘前做,方案、2K 重生成、局部重绘、
	// 音频驱动、续跑重渲读的都是这份文本。
	// **场次覆盖对账**（2026-09-30 用户：「分析是否场景过于单一」+「好好完善」）。
	//
	// 剧本（平台自己的 ① 会话产出）带结构化场次表 `【场 N|地点|时间|内外】`，
	// 而 ⑤ 分镜按**叙事节拍**铺镜头、**没有场次配额约束** ⇒ 实测第 02 集：一场戏占 49%、
	// 剧本第 3 场只剩 1 镜、**唯一的夜戏整场丢掉**，观众的评价就是「场景过于单一」。
	// 以前无处对照（剧本没有场次表），现在有 —— 就在**存盘前**把这笔账报出来。
	//
	// 2026-10-09（用户：「场景和分镜运镜很单一」）：场次表**落进方案**（Plan.BriefScenes）——
	// 渲染前的门（project.ValidatePlan / cmd/preflight）、重渲与导入包路径**拿不到剧本文本**，
	// 只能靠方案自己带上这一份，否则这笔账出了本阶段就再也看不见（解析一次，多处消费）。
	plan.BriefScenes = project.ParseScriptScenes(text)
	for _, msg := range project.SceneCoverageIssues(plan, plan.BriefScenes) {
		c.emit(proj.ID, PhaseScript, 0, "warn", 16, "🎬 %s", msg)
	}
	if n, details := director.FitDialogueDurations(plan); n > 0 {
		c.emit(proj.ID, PhaseScript, 0, "info", 15, "⏱ 台词时长预算:%d 镜时长已调整(%s)", n, strings.Join(details, ";"))
	}
	for _, sh := range plan.Shots {
		if msg := director.DialogueOverflow(sh); msg != "" {
			c.emit(proj.ID, PhaseScript, sh.ID, "warn", 15, "⚠ %s", msg)
		}
	}
	// **每集总时长对账**（2026-09-28 全面审计，来源：开源参考实现 shuohao-skills「每集总时长 ±15%」）。
	// 时长被台词预算与切镜需要抬过之后，这一集的真实总长可能已经偏离目标 —— 用户看到的是
	// "说要 4 分钟、结果成片 6 分钟"。超了如实报出差额与原因，让人决定是砍镜还是接受更长。
	if p.TotalSeconds > 0 {
		got := 0
		for _, sh := range plan.Shots {
			got += sh.Duration
		}
		if drift := float64(got-p.TotalSeconds) / float64(p.TotalSeconds); drift > 0.15 {
			c.emit(proj.ID, PhaseScript, 0, "warn", 15,
				"⚠ 本集实际总长 %d 秒，比目标 %d 秒多 %.0f%%（口径：超 ±15%% 就该看一眼）—— "+
					"原因多是台词时长预算与切镜需要把镜头抬高；要么接受更长，要么砍/并几镜（本集 %d 镜）",
				got, p.TotalSeconds, drift*100, len(plan.Shots))
		}
	}
	// 台词语言标记兜底:LLM 常把模板要求的 <d>[Chinese]…</d> 写成 <d>…</d>,
	// 标记一丢,H3 就用任意语言说话(成片出现听不懂的"鸟语",2026-09-12 用户报障)。
	// 在**存盘前**补齐:方案、2K 重生成、局部重绘、音频驱动、续跑重渲都读这份文本,
	// 一处修好全链受益。
	for i := range plan.Shots {
		plan.Shots[i].H3Prompt = EnsureChineseDialogue(plan.Shots[i].H3Prompt)
	}
	// **锚表对账**（2026-09-28 用户报「6 个角色，LLM 解析出来咋只有一个角色」）：
	// 源文本里的【人物外形锚表 · 硬约束】列了几个角色，方案里就该有几个 —— 缺的用锚表原文补上。
	// 放在**存盘前**：补出来的卡要跟着方案一起落盘，资产阶段才会给它们出定妆照。
	// 为什么由平台补而不是让模型自觉：锚表本身就是硬约束、每个角色的外形描述都是现成的，
	// 补一张卡是确定性的活；而模型实测会漏（那一集锚表 6 个、它只出了 1 个）。
	if added := EnsureAnchorCast(plan, text); len(added) > 0 {
		c.emit(proj.ID, PhaseScript, 0, "warn", 15,
			"⚠ 源文本的【人物外形锚表】列了 %d 个角色，方案里只出了 %d 个 —— 已按锚表原文补进：%s"+
				"（锚表是硬约束、逐项照抄即可；这些卡同样会出定妆照并入库）",
			len(ParseCastAnchor(text)), len(plan.Characters)-len(added), strings.Join(added, "、"))
	}
	// **锚表原文回填**（2026-10-07）：②人物道具会话照名字出了卡、却没写 `description` —— 只补兜底串
	// 的话，下面的专职翻译会话**永远不会**为它们启动（NeedsPromptTranslation 要求"有描述可翻"）。
	if names := FillAnchorDescriptions(plan, text); len(names) > 0 {
		c.emit(proj.ID, PhaseScript, 0, "info", 15,
			"📇 %d 张角色卡的身份真源从【人物外形锚表】补回（原先只有年龄、描述是空的）：%s",
			len(names), strings.Join(names, "、"))
	}
	// **锚表里也没有的人**：同上，用小说原文的"首次出场段"当外形材料。
	if names := FillCastDescriptionsFromSource(plan, text); len(names) > 0 {
		c.emit(proj.ID, PhaseScript, 0, "info", 15,
			"📖 %d 张卡的外形材料取自**本集原文的首次出场段**（锚表里没有这几个人）：%s",
			len(names), strings.Join(names, "、"))
	}
	// **专职翻译会话（用户架构第②步）**：锚表补齐的卡只有**中文**设定，而出图只吃英文 ——
	// 不翻译的话模型只知道"几岁、几头身"，服装/脸型/发型全靠想象。用户 2026-09-28 报的
	// 「角色三视图渲染异常」就是这么来的：设定写"深灰值勤装甲、方中带圆脸、剑眉、左手小指缺一节"，
	// 画出来却是"穿现代浅灰衬衫、挂工牌的年轻职员"。见 anchor_translate.go。
	if n, err := TranslateCardPrompts(ctx, client, plan); err != nil {
		c.emit(proj.ID, PhaseScript, 0, "warn", 16,
			"⚠ 角色卡的英文定妆提示词生成失败(%v) —— 这些角色出图时只有年龄与头身比，"+
				"服装/脸型/发型照不上设定；可重跑本阶段", err)
	} else if n > 0 {
		c.emit(proj.ID, PhaseScript, 0, "success", 16,
			"✔ 已为 %d 张只有中文设定的角色卡生成英文定妆提示词（逐项照抄锚表：脸型/眉眼/发型/服装/记号）", n)
	}
	// **身份提示词缺口点名**（2026-10-07）：这些卡的参考图提示词只有年龄（兜底串）或干脆是空的 ——
	// 出图时脸会被现编。此前这件事只在下游**撞脸门事后报警**时才看得见（那是症状），
	// 这里在存盘前把"是哪几张卡、为什么"一次说清。不拦人：锚表里真没有这个人时，也只能现编。
	if gaps := IdentityPromptGaps(plan); len(gaps) > 0 {
		c.emit(proj.ID, PhaseScript, 0, "warn", 16,
			"⚠ 这些角色卡的参考图提示词**只有年龄**（身份没锁：脸靠现编，跨镜/跨集很难一致）：%s —— "+
				"锚表里有他们的外形就补进 description（平台会自动补），锚表里没有就先补源文本再重跑本阶段",
			strings.Join(gaps, "、"))
	}
	if cut := TruncatedAnchorSections(text); len(cut) > 0 {
		c.emit(proj.ID, PhaseScript, 0, "warn", 16,
			"⚠ 【人物外形锚表】里这些小节被剧本正文截断了（源文本本身不全，只能照抄到断点）：%s —— "+
				"补全源文本后重跑本阶段，这些人的外形才能逐项照抄", strings.Join(cut, "、"))
	}
	// **空正文的两步处置**（2026-10-08 用户第二次报「还是报一样的错误」）。
	//
	// ① 平台按**这一镜自己的材料**（场次/人物/走位/景别/机位/台词原文/时长）拼一版最小合规正文。
	//    真机代价：镜 12 材料齐全、只有正文是空的，模型自纠一轮补不出来 ⇒ 渲染入口的致命门
	//    「h3_prompt 为空」不让启动 ⇒ **48 集的批量产线停在第 1 集**。材料是已知量，
	//    「能确定性做到的事不要让模型自觉」（见 project.empty_body.go）。
	// ② 拼完还有空的（连场次/人物/台词都没有）才点名 —— 那几镜渲染会被致命门拦住；
	//    方案照常存盘（用户可以在故事板里手改这几镜，资产还能复用）。
	// **兜底正文要用的英文材料**（2026-10-08 用户：「还没做/边界，给我留着吗？」）。
	//
	// 分镜的 `action` / `positions` 原本**只有中文**，而兜底正文只能放英文（汉字出现在 <d> 外
	// 会被入口拒收）⇒ 那几镜的画面调度就只剩参考图与机位可依 —— 那等于把缺口留给用户。
	// 这里用平台自己的专职翻译会话（与卡片翻译同一套做法）把它们翻成英文写进
	// `action_en` / `positions_en`；**只在"正文为空 + 材料是中文 + 还没翻过"的镜上花这次调用**。
	// 必须排在 FillEmptyShotBodies **之前**（兜底正文要消费这份英文）。
	if n, err := TranslateShotStaging(ctx, client, plan); err != nil {
		c.emit(proj.ID, PhaseScript, 0, "warn", 16,
			"⚠ 空正文那几镜的中文动作/走位翻英文失败(%v) —— 兜底正文只能按英文材料拼，"+
				"那几镜的画面调度会少一份（只能靠参考图与机位兜画）", err)
	} else if n > 0 {
		c.emit(proj.ID, PhaseScript, 0, "info", 16,
			"✔ 已把 %d 镜的中文动作/走位翻成英文（专职翻译会话）—— 兜底正文会用英文那一份，信息不丢", n)
	}
	if n, ids := project.FillEmptyShotBodies(plan); n > 0 {
		// 少了中文材料这件事必须说出来：兜底正文**只用英文材料**（汉字出现在 <d> 外会被入口的
		// 汉字卫生门拒收，见 project.empty_body.go），所以中文的走位/动作描述进不去 ——
		// 那几镜的画面调度只能靠参考图与机位兜，用户有权知道并决定要不要手写这一镜。
		cjk := ""
		if plan.FilledBodyCJK > 0 {
			cjk = fmt.Sprintf("；其中 %d 镜的**中文走位/动作描述没能进正文**（汉字出现在 <d> 外会被入口拒收），"+
				"那几镜只能靠参考图与机位兜画", plan.FilledBodyCJK)
		}
		c.emit(proj.ID, PhaseScript, 0, "warn", 16,
			"🩹 有 %d 镜的正文是空的、自纠一轮也没写出来（%s）—— 平台已按**这一镜自己的材料**"+
				"（场次/人物/景别/机位/台词原文 + 卡片的英文外形）拼了一版最小合规正文，**不是新编内容**；"+
				"渲染因此可以继续%s。不满意就在故事板里改这几镜的正文", n, joinShotIDs(ids, 8), cjk)
	}
	// 说话人误标纠正要如实说（2026-10-09 用户：「旁白说话角色未对齐（另一人说话，其它角色动嘴）」）：
	// 模型把"画面里的人在说"写成画外音时，那一镜的提示词里只剩"画内的人保持闭嘴"这一条约束。
	if plan.SpeakerOSFixed > 0 {
		c.emit(proj.ID, PhaseScript, 0, "warn", 16,
			"🎭 纠正了 %d 条**被误标成画外音**的台词（说话人就在本镜的出场名单里）—— "+
				"标成画外音时提示词里只剩\"画内的人保持闭嘴\"，画面里到底谁开口就全凭模型自己安排"+
				"（用户报的「另一人说话、其它角色动嘴」就是它）；已按 `characters` 出场名单改回**画内对口型**",
			plan.SpeakerOSFixed)
	}
	if ids := project.EmptyPromptShotIDs(plan); len(ids) > 0 {
		c.emit(proj.ID, PhaseScript, 0, "warn", 16,
			"⚠ 还有 %d 镜的正文是**空的**（%s）—— 这几镜连场次/人物/台词都没有，平台拼不出正文；"+
				"渲染会被致命门拦住不启动（空提示词只会出一帧近全黑，实测第 02 集镜 15 达 94%%）。"+
				"改法：在故事板里手写这几镜的正文，或补全源文本后重跑本阶段", len(ids), joinShotIDs(ids, 8))
	}
	if err := c.store.SavePlan(proj.ID, plan); err != nil {
		return err
	}
	// 角色数按**人**报(造型档算同一个人,§4.2):技能侧把每个造型档做成独立卡之后,
	// 计划里的 characters 长度会把《翻篇》的 12 个角色算成 21 个。
	people := project.CharFamilyCount(plan.Characters)
	tail := ""
	if people != len(plan.Characters) {
		tail = fmt.Sprintf("(造型卡 %d 张)", len(plan.Characters))
	}
	c.emit(proj.ID, PhaseScript, 0, "success", 100, "✔ 方案就绪:《%s》%d 角色%s / %d 场景 / %d 镜",
		plan.Title, people, tail, len(plan.Scenes), len(plan.Shots))
	return nil
}

// ---------- 阶段二:assets ----------

// imageNegative 图像生成的负面词。当前所有家族都是 CFG 1.0(蒸馏档),
// 负面词不参与引导,因此留空 —— 要表达"不要什么"必须写进正向提示词。
const imageNegative = ""
