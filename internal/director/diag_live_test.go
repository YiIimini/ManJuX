package director

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/llm"
)

// 线上复现诊断(默认跳过,会**真实调用 LLM**)。
//
//	go test ./internal/director/ -run TestDiagPlanRoundLive -v -timeout 900s
//	MANJUX_DIAG=1  [MANJUX_DIAG_SRC=<章节 .md>]  [MANJUX_DIAG_STYLE=<全局风格>]
//
// 为什么留这个工具:规划轮是 48 集产线的咽喉 —— 它一失败,整条线就停在第 1 集
// (2026-09-24 用户报障)。线上只留得下一句"解析失败",无从复盘;能在本地用
// **同一份提示词**复现之后,再调提示词/容错才有依据,也才能验证修复。
// 提示词刻意复用 buildPlanUserPrompt:诊断与线上同源,否则复现的是另一个问题。
func TestDiagPlanRoundLive(t *testing.T) {
	if os.Getenv("MANJUX_DIAG") == "" {
		t.Skip("诊断用:设 MANJUX_DIAG=1 才跑(会真实调用 LLM,可能数分钟)")
	}
	root := filepath.Join("..", "..")
	cfgMgr, err := config.NewManager(filepath.Join(root, "data", "settings.json"), root)
	if err != nil {
		t.Fatalf("加载设置失败: %v", err)
	}
	g := cfgMgr.Get()
	if g.LLM.APIKey == "" || g.LLM.BaseURL == "" || g.LLM.Model == "" {
		t.Skip("未配置 LLM,跳过")
	}
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)

	srcPath := os.Getenv("MANJUX_DIAG_SRC")
	if srcPath == "" {
		srcPath = `D:\Ai\小说\天宫是我家盖的\正文\卷一_落款\第001章_三万六千块砖.md`
	}
	b, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("读章节失败(%s): %v", srcPath, err)
	}
	style := os.Getenv("MANJUX_DIAG_STYLE")
	if style == "" {
		style = "国漫二维动画,赛璐璐上色,哑光质感,电影级构图"
	}
	// 与 server/novels_split.go 的拆集源文同构:标题行 + 全局风格 + 本集剧情 + 本集原文
	text := fmt.Sprintf(
		"《天宫是我家盖的》系列短视频 第01 集《三万六千块砖》\n全局风格:%s\n\n【本集剧情】%s\n\n【本集原文】(分镜必须沿此原文节拍逐段改编,覆盖全部关键情节)\n%s",
		style,
		"少年石匠在宫城工地砌下第一块砖,被工头刁难,却因一块会发光的灵纹砖被监工记住。",
		strings.TrimSpace(string(b)))

	opt := Options{
		AnimeFaces: true,
		ExtraHint: "ADAPTATION SOURCE PRIORITY: 本集附有小说原文(【本集原文】)。分镜必须沿原文叙事节拍逐段改编:" +
			"每个关键情节转折都必须有对应镜头,不得跳段、不得省略因果链、不得凭空压缩时空;原文对白优先原句使用。",
	}
	user := buildPlanUserPrompt(text, opt, true)
	t.Logf("模型=%s  提示词:system %d 字 / user %d 字", g.LLM.Model,
		len([]rune(planSystemPrompt)), len([]rune(user)))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	start := time.Now()
	res, err := client.ChatEx(ctx, []llm.Message{
		{Role: "system", Content: planSystemPrompt},
		{Role: "user", Content: user},
	}, true)
	if err != nil {
		t.Fatalf("调用失败(耗时 %s): %v", time.Since(start).Round(time.Second), err)
	}
	t.Logf("耗时 %s  finish=%s  prompt=%d tokens  completion=%d tokens  正文 %d 字",
		time.Since(start).Round(time.Second), res.Finish, res.Usage.PromptTokens,
		res.Usage.CompletionTokens, len([]rune(res.Content)))
	t.Logf("原文两端:\n%s", snippet(res.Content, 160))

	if head, trunc, perr := parsePlanHead(res.Content); perr != nil {
		t.Errorf("✕ 解析失败: %v", perr)
	} else {
		t.Logf("✔ 解析成功:title=%q segments=%d characters=%d scenes=%d(截断补全=%v)",
			head.Title, len(head.Segments), len(head.Characters), len(head.Scenes), trunc)
	}

	// MANJUX_DIAG=full:连分批出镜轮一起跑完(等于「剧本分镜」阶段的完整链路,
	// 只是不落盘)。这是"48 集产线能不能继续跑"的端到端验证,耗时数分钟。
	if os.Getenv("MANJUX_DIAG") != "full" {
		return
	}
	t.Log("—— 继续跑完整分批链路(规划轮 + 逐批出镜)…")
	e2eStart := time.Now()
	e2eCtx, e2eCancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer e2eCancel()
	plan, gerr := Generate(e2eCtx, client, text, Options{
		AnimeFaces: true,
		ExtraHint:  opt.ExtraHint,
		OnBatch: func(b, total, from, to int) {
			t.Logf("  ▸ 批次 %d/%d 镜 %d-%d", b, total, from, to)
		},
		OnNote: func(f string, a ...any) { t.Logf("  ⚠ "+f, a...) },
	})
	if gerr != nil {
		t.Fatalf("✕ 端到端失败(耗时 %s): %v", time.Since(e2eStart).Round(time.Second), gerr)
	}
	t.Logf("✔ 端到端成功:耗时 %s,方案《%s》%d 角色 / %d 场景 / %d 镜",
		time.Since(e2eStart).Round(time.Second), plan.Title, len(plan.Characters), len(plan.Scenes), len(plan.Shots))
}
