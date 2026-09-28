package director

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
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
	//
	// MANJUX_DIAG_RAW=1：**把章节原文当整篇源**（标题取首行、剧情段留空）。
	// 为什么要有这个开关（2026-09-28）：默认那条内置样例故事是"石匠砌第一块砖"，本身没几件器物，
	// 于是"道具提取出没出"这类问题在样例上根本验不出来（道具 0 未必是 bug）。验道具必须用**真书**。
	text := ""
	if os.Getenv("MANJUX_DIAG_RAW") != "" {
		body := strings.TrimSpace(string(b))
		title := "第01集"
		if ls := strings.Split(body, "\n"); len(ls) > 0 && strings.TrimSpace(ls[0]) != "" {
			title = strings.TrimSpace(ls[0])
		}
		text = fmt.Sprintf("%s\n全局风格:%s\n\n【本集原文】(分镜必须沿此原文节拍逐段改编,覆盖全部关键情节)\n%s",
			title, style, body)
	} else {
		text = fmt.Sprintf(
			"《天宫是我家盖的》系列短视频 第01 集《三万六千块砖》\n全局风格:%s\n\n【本集剧情】%s\n\n【本集原文】(分镜必须沿此原文节拍逐段改编,覆盖全部关键情节)\n%s",
			style,
			"少年石匠在宫城工地砌下第一块砖,被工头刁难,却因一块会发光的灵纹砖被监工记住。",
			strings.TrimSpace(string(b)))
	}

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

	// 官方 H3 格式的输出门**在这里也跑一遍**：直出成功不等于满足官方要求，
	// 没有这一步，用户问"LLM 解析是否满足 H3 官方要求"时只能靠翻代码估。
	bad := 0
	for _, sh := range plan.Shots {
		for _, v := range project.H3FormatViolations(sh.H3Prompt) {
			bad++
			t.Logf("  [H3格式] 镜 %d：%s", sh.ID, v)
		}
	}
	t.Logf("H3 格式违规：%d 条 / %d 镜", bad, len(plan.Shots))
	fails, warns := 0, 0
	for _, is := range project.ValidatePlan(plan) {
		if is.Level == "fail" {
			fails++
		} else {
			warns++
		}
	}
	t.Logf("契约自检：fail=%d warn=%d", fails, warns)

	// 把方案**落盘**（MANJUX_DIAG_OUT）—— 2026-09-28 加：诊断不落盘时，跑完 30 分钟只剩几行日志，
	// "道具到底提取了没有"这种问题得再烧一次才答得上；落盘后可以直接
	//   go run ./cmd/preflight -dir <目录>
	// 逐类复核（角色卡/场景卡/道具卡/分镜），也能拿真方案做对比。
	if out := os.Getenv("MANJUX_DIAG_OUT"); out != "" {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatalf("建目录失败:%v", err)
		}
		b, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			t.Fatalf("序列化方案失败:%v", err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(out), "plan.json"), b, 0o644); err != nil {
			t.Fatalf("写方案失败:%v", err)
		}
		t.Logf("方案已落盘:%s（可 go run ./cmd/preflight -dir %s）", filepath.Join(filepath.Dir(out), "plan.json"), filepath.Dir(out))
	}
	// 四类产物的**一处汇总**：道具这一项此前只能靠肉眼翻 plan，落这里就一眼看见。
	t.Logf("四类：角色 %d（其中 Q 版 %d）/ 场景 %d / **道具 %d** / 分镜 %d",
		len(plan.Characters), func() int {
			n := 0
			for _, ch := range plan.Characters {
				if project.IsChibiCard(ch.Kind, ch.ID) {
					n++
				}
			}
			return n
		}(), len(plan.Scenes), len(plan.Props), len(plan.Shots))
	withProp := 0
	for _, sh := range plan.Shots {
		if len(sh.PropIDs) > 0 {
			withProp++
		}
	}
	t.Logf("带 prop_ids 的镜：%d / %d", withProp, len(plan.Shots))
}
