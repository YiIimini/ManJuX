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
	if strings.Contains(text, "【本集原文】") {
		if hint != "" {
			hint += "\n"
		}
		// 【口径变更 2026-09-28】用户：「分镜脚本按照爆款爽剧的核心要点来，LLM 需要抓住文字内容中的
		// 重点总结输出核心分镜脚本，**而不是一味的照搬原文内容**」。
		//
		// 此前这里是"必须沿原文叙事节拍逐段改编、**不得跳段**、不得省略因果链"——那是 2026-09-10 为治
		// "断章取义"定的调，代价是**把改编逼成了抄写**：模型逐段覆盖原文，于是铺垫、支线、絮叨对白
		// 全进了分镜（实测：一章 2500 字出 35 镜，均 6.8 秒，观众觉得拖沓）。
		// 现在改成"原文是素材、不是剧本"：先提炼核心冲突与爽点，砍冗余、允许跳段与合并、可重排先后。
		hint += "ADAPTATION IS A REWRITE, NOT A COPY: 本集附有小说原文(【本集原文】)，它是**素材**而不是剧本。\n" +
			"① 先提炼：本集的核心冲突是什么？2-3 个大爽点（逆袭/打脸/揭秘/升阶）分别是哪几处？开场 3 秒砸什么？\n" +
			"② 再取舍：**砍掉**铺垫、无关支线、重复对白、纯氛围描写；允许**跳段**、允许把多段合并成一镜、\n" +
			"   允许调整先后顺序（只要人物关系与因果讲得通）。判断标准：这一镜不推进冲突、不抛信息、不给情绪，就删。\n" +
			"③ 保留原文的**关键台词原句**（那是爽点的载体），其余对白可以短化、锋利化。\n" +
			"④ 输出的是**核心分镜**：围绕上面那几个爽点组织镜头，而不是围着原文的段落顺序排镜头。\n" +
			"（对照：忠实但平的改编 = 观众划走；有取舍的改编 = 爆款。）"
		c.emit(proj.ID, PhaseScript, 0, "info", 10, "📖 本集附小说原文：**提炼核心爽点后重建分镜**（不逐段照搬）")
	}
	if roster, nChars := c.bookRoster(proj.ID); roster != "" {
		if hint != "" {
			hint += "\n"
		}
		hint += roster
		c.emit(proj.ID, PhaseScript, 0, "info", 10, "🔁 系列角色锚定:沿用本书已确立角色名 %d 个(跨集不换脸)", nChars)
	}
	plan, err := director.Generate(ctx, client, text, director.Options{
		MaxShots: p.MaxShots, TotalSeconds: p.TotalSeconds,
		ExtraHint: hint, Chibi: p.Chibi,
		AnimeFaces: p.FacesOn(),
		// 镜头数安全网(设置页 split.shot_limit):此前只在界面上存在、没有读取点,
		// 内容驱动模式下上限永远是代码里的常量(审计 P1-1)。
		ShotLimit: c.cfg.Get().Split.ShotLimit,
		OnBatch: func(batch, total, fromShot, toShot int) {
			c.emit(proj.ID, PhaseScript, 0, "info", 15, "▸ 分批分镜 %d/%d:镜 %d-%d 生成中…", batch, total, fromShot, toShot)
		},
		// 降级/兜底提示直接进前端事件流:静默降级等于骗人
		// (典型:规划轮输出被截断,补全后继续 —— 必须让用户知道并有机会重跑)
		OnNote: func(format string, args ...any) {
			c.emit(proj.ID, PhaseScript, 0, "warn", 15, format, args...)
		},
	})
	hbStop()
	<-hbDone
	if err != nil {
		return err
	}
	// 台词时长预算:LLM 常把 20 字台词塞进 4 秒镜头,H3 只能把话说到被截断为止
	// (2026-09-24 用户报「配音也不完整」)。存盘前逐镜核算并抬时长,抬不动的
	// 如实报出来让用户拆镜 —— 这层必须在存盘前做,方案、2K 重生成、局部重绘、
	// 音频驱动、续跑重渲读的都是这份文本。
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
