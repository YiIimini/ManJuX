// Command preflight 渲染前预检：把"一集项目"从**方案到可渲染输入**整条链核一遍，
// 逐类报出（角色卡 / 场景卡 / 道具卡 / 分镜）到底哪一步没到位。
//
//	go run ./cmd/preflight -project "补碑人改命·守钟女逆时·第01集·碑上写我死了"
//	go run ./cmd/preflight -dir  "data/projects/补碑人改命·守钟女逆时/补碑人改命·守钟女逆时·第01集·碑上写我死了"
//
// 为什么要它（用户 2026-09-28：「复核一下是否到位，不要我渲染的时候又来各种各样的问题」）：
// 平台的门分散在各阶段（契约 / 装配 / 引擎），**渲染时才逐条冒出来**，用户看到的是"又出问题"。
// 这里把四类产物 + 渲染输入一次核完，输出一份**能先看完再决定要不要渲**的清单。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"encoding/json"

	"github.com/YiIimini/ManJuX/internal/project"
)

func main() {
	projFlag := flag.String("project", "", "项目 id（data/projects 下的目录名）")
	dirFlag := flag.String("dir", "", "项目目录（与 -project 二选一）")
	flag.Parse()

	root := "."
	projDir := strings.TrimSpace(*dirFlag)
	if projDir == "" {
		pid := strings.TrimSpace(*projFlag)
		if pid == "" {
			fmt.Println("用法：preflight -project <项目id> | -dir <项目目录>")
			os.Exit(2)
		}
		projDir = findProjectDir(filepath.Join(root, "data", "projects"), pid)
		if projDir == "" {
			fmt.Printf("✕ 找不到项目 %q\n", pid)
			os.Exit(1)
		}
	}
	planPath := filepath.Join(projDir, "plan", "plan.json")
	raw, err := os.ReadFile(planPath)
	if err != nil {
		fmt.Printf("✕ 读方案失败：%v\n", err)
		os.Exit(1)
	}
	var plan project.Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		fmt.Printf("✕ 解析方案失败：%v\n", err)
		os.Exit(1)
	}

	// 走平台**同一套自愈**（器物对号/补 prop_ids/补台词标签/补画外素材/回填小镜头结构），
	// 否则预检会报渲染器早已修好的假警。每个计数都要列出来 —— "自愈改了什么"必须看得见。
	healed := project.HealPlan(&plan)
	if healed.PropRefs+healed.PropRefsFilled+healed.Dialogue+healed.Voiceover+healed.Cuts+healed.Voices+healed.CodeShots > 0 {
		fmt.Printf("（已按平台自愈口径修过：器物对号 %d 处 / 补 prop_ids %d 处 / 补台词标签 %d 镜 / "+
			"补画外素材 %d 镜 / 回填小镜头结构 %d 片段 / 补角色声线 %d 张 / 编号定读音 %d 镜）\n",
			healed.PropRefs, healed.PropRefsFilled, healed.Dialogue, healed.Voiceover, healed.Cuts,
			healed.Voices, healed.CodeShots)
		if healed.Voices > 0 {
			fmt.Printf("  补了声线的卡：%s\n", strings.Join(healed.VoiceNames, "、"))
		}
		if healed.CodeShots > 0 {
			fmt.Printf("  %s\n", project.CodeSummary(healed.CodeIDs, healed.CodeCount))
		}
	}
	fmt.Printf("=== 渲染前预检：%s ===\n", filepath.Base(projDir))
	fmt.Printf("角色 %d 张（%d 个人） / 场景 %d / 道具 %d / 分镜 %d 镜\n\n",
		len(plan.Characters), project.CharFamilyCount(plan.Characters), len(plan.Scenes), len(plan.Props), len(plan.Shots))

	// ① 四类产物的卡片完备性
	fmt.Println("【角色卡】")
	for _, ch := range plan.Characters {
		problems := cardProblems(ch.Name, ch.ImagePrompt)
		board := "有板串"
		if strings.TrimSpace(ch.BoardPrompt) == "" {
			board = "（板串为空：会用平台兜底 15 格）"
		}
		fmt.Printf("  %-16s 外形锁=%v 提示词=%d 字 %s %s\n", ch.Name, hasIdentityLock(ch.ImagePrompt),
			len([]rune(ch.ImagePrompt)), board, problems)
	}
	fmt.Println("【场景卡】")
	for _, sc := range plan.Scenes {
		lack := project.ScenePlateThin(sc.ImagePrompt)
		state := "✓"
		if len(lack) > 0 {
			state = "⚠ 缺 " + strings.Join(lack, "＋")
		}
		fmt.Printf("  %-20s %d 字 %s\n", sc.Name, len([]rune(sc.ImagePrompt)), state)
	}
	fmt.Println("【道具卡】")
	if len(plan.Props) == 0 {
		fmt.Println("  ⚠ 一件都没有（招牌物件会在每一镜被重新发明 = 漂移）")
	}
	for _, pr := range plan.Props {
		fmt.Printf("  %-16s en=%d 字 desc=%d 字 prompt=%d 字\n", pr.Name,
			len([]rune(pr.EN)), len([]rune(pr.Description)), len([]rune(pr.ImagePrompt)))
	}
	fmt.Println("【分镜】")
	multi, noProp, words := 0, 0, 0
	for _, sh := range plan.Shots {
		if len(sh.Characters) > 0 && len(sh.PropIDs) == 0 && project.FirstPropWord(sh.Action) != "" {
			noProp++
		}
		words += project.H3DescriptionWords(sh.H3Prompt)
	}
	if len(plan.Shots) > 0 {
		fmt.Printf("  正文词数均值 %d（官方 350–500）/ 提到器物却没带 prop_ids 的镜 %d / 多视角镜 %d\n",
			words/len(plan.Shots), noProp, multi)
	}

	// ② 契约自检（平台全部门）
	fmt.Println("\n【契约自检】")
	issues := project.ValidatePlan(&plan)
	fails, warns, infos := 0, 0, 0
	for _, is := range issues {
		switch is.Level {
		case "fail":
			fails++
			fmt.Printf("  ✕ [fail] %s: %s\n", is.Where, oneLine(is.Msg))
		case "info":
			// info = 平台**已经自动处理**或仅供参考的提醒：如实说出来，但不计入"要你处理的问题"。
			// 为什么要有这一级（2026-09-28）：把"平台自己会补齐"的事报成 warn，会让真正的
			// 警告淹在噪音里（用户会整体忽略这一列）—— 平台自己的话就是"假告警会让人不再看告警"。
			infos++
		default:
			warns++
		}
	}
	for _, is := range issues {
		switch is.Level {
		case "fail":
		case "info":
			fmt.Printf("  ℹ [info] %s: %s\n", is.Where, oneLine(is.Msg))
		default:
			fmt.Printf("  ⚠ [warn] %s: %s\n", is.Where, oneLine(is.Msg))
		}
	}

	// ③ H3 官方格式（逐镜）
	bad := 0
	for _, sh := range plan.Shots {
		for _, v := range project.H3FormatViolations(sh.H3Prompt) {
			bad++
			fmt.Printf("  ⚠ 镜 %d：%s\n", sh.ID, oneLine(v))
		}
	}

	// ④ 结论
	fmt.Println("\n【结论】")
	fmt.Printf("  契约 fail=%d / warn=%d / info=%d（info = 平台已自动处理或仅供参考）；H3 格式违规=%d / %d 镜\n", fails, warns, infos, bad, len(plan.Shots))
	switch {
	case fails > 0:
		fmt.Println("  ✕ **先别渲**：有 fail（导入/建项目那一层就该拦住的问题），按上面逐条修。")
	case warns > 0 || bad > 0:
		fmt.Println("  ⚠ 可以渲，但先看一眼上面的 warn —— 它们正是成片里「看得出来」的那些毛病（漂移/手部/伪汉字/偏暗）。")
	default:
		fmt.Println("  ✔ 干净：四类产物齐、契约无 fail、H3 格式无违规。")
	}
}

func cardProblems(name, prompt string) string {
	var lack []string
	if strings.TrimSpace(prompt) == "" {
		lack = append(lack, "提示词为空")
	}
	if !hasIdentityLock(prompt) {
		lack = append(lack, "无 IDENTITY LOCK")
	}
	low := strings.ToLower(prompt)
	if !strings.Contains(low, "head") && !strings.Contains(low, "face") {
		lack = append(lack, "无面部/头身锚点")
	}
	if len(lack) == 0 {
		return "✓"
	}
	return "⚠ " + strings.Join(lack, "／")
}

func hasIdentityLock(p string) bool {
	return strings.Contains(strings.ToUpper(p), "IDENTITY LOCK")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) > 150 {
		return string(r[:150]) + "…"
	}
	return s
}

// findProjectDir 在 projects 根下按目录名找项目（书名/分集 两级）。
func findProjectDir(root, id string) string {
	if st, err := os.Stat(filepath.Join(root, id, "plan", "plan.json")); err == nil && !st.IsDir() {
		return filepath.Join(root, id)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(root, e.Name(), id, "plan", "plan.json")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return filepath.Join(root, e.Name(), id)
		}
	}
	return ""
}
