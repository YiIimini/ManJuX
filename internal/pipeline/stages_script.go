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
		// 原文在场:分镜必须沿原文节拍逐段改编,覆盖全部关键情节——
		// 跳段/断章取义的根治指令(2026-09-10 用户定调)
		hint += "ADAPTATION SOURCE PRIORITY: 本集附有小说原文(【本集原文】)。分镜必须沿原文叙事节拍逐段改编:每个关键情节转折都必须有对应镜头,不得跳段、不得省略因果链、不得凭空压缩时空;原文对白优先原句使用。"
		c.emit(proj.ID, PhaseScript, 0, "info", 10, "📖 本集附小说原文,分镜沿原文节拍逐段改编(不跳段)")
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
		OnBatch: func(batch, total, fromShot, toShot int) {
			c.emit(proj.ID, PhaseScript, 0, "info", 15, "▸ 分批分镜 %d/%d:镜 %d-%d 生成中…", batch, total, fromShot, toShot)
		},
	})
	hbStop()
	<-hbDone
	if err != nil {
		return err
	}
	if err := c.store.SavePlan(proj.ID, plan); err != nil {
		return err
	}
	c.emit(proj.ID, PhaseScript, 0, "success", 100, "✔ 方案就绪:《%s》%d 角色 / %d 场景 / %d 镜", plan.Title, len(plan.Characters), len(plan.Scenes), len(plan.Shots))
	return nil
}

// ---------- 阶段二:assets ----------

// krea2Negative Krea-2 蒸馏(CFG 1.0)下负面词无引导作用,留空。
const krea2Negative = ""
