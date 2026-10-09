package pipeline

import (
	"strings"
	"testing"
)

// 合片一完成必须**自动**体检 + 逐条点名（2026-10-09 用户：「问题好多啊」）。
//
// 【为什么要有这道门】"问题好多"的根子是"用户自己看得出来、平台不说"。
// 判据盯**生效的那一行**：标识符出现在注释里也能满足宽串断言 —— 那种守卫是摆设，
// 所以这里连"这一行是不是注释"一起查（第 33 轮为此抓出过三处摆设守卫）。
func TestAssembleAutoAuditsTheFilm(t *testing.T) {
	src := readSrc(t, "stages_qc_assemble.go")
	const call = "c.auditProducedFilm(ctx, proj, plan, clips, res, outDir)"
	at := strings.Index(src, call)
	if at < 0 {
		t.Fatal("合片完成后没有自动体检 —— 用户只能自己打开成片才发现问题（本轮要修的正是这件事）")
	}
	line := lineOf(src, at)
	if strings.HasPrefix(strings.TrimSpace(line), "//") {
		t.Fatalf("那句调用只在注释里：%q", line)
	}
	// 顺序要命：体检的对象是**刚合出来的这一版**（含 res.SRTPath / 新的 durations）。
	ready := strings.Index(src, "✔ 成片就绪")
	if ready < 0 {
		t.Fatal("找不到「成片就绪」那句播报（判据的前提没了，得先核对这一段）")
	}
	if at < ready {
		t.Error("体检跑在「成片就绪」之前 —— 那时 SRT / 时长还没落定，算出来的是上一版")
	}
	// 报告得落盘：界面播报只留前 8 条，全表要看文件。
	// 报告得落盘：界面播报只留前 8 条，全表要看文件（落盘那行在 film_audit.go 里）。
	if !strings.Contains(readSrc(t, "film_audit.go"), "film_audit.md") {
		t.Error("体检只播报不落盘 —— 前 8 条之外的结论用户翻不到")
	}
	// 并且落盘必须用**本次合片的输出目录**（不是猜出来的路径），否则报告会写到别处或写不进去。
	if !strings.Contains(readSrc(t, "film_audit.go"), "filepath.Join(outDir, \"film_audit.md\")") {
		t.Error("体检报告没用本次合片的输出目录 —— 报告会落到别处/写不进去")
	}
}

// 命令行与 app 必须用**同一套判据**（一处判据两处消费，2026-10-09）。
//
// 否则就会出现"命令行说没问题、界面上没说"（或反过来）—— 那种分叉比没工具更糟：
// 用户不知道该信哪一个。
func TestCLIAndAppShareOneJudgement(t *testing.T) {
	cli := readSrc(t, "../../cmd/auditfilm/main.go")
	if !strings.Contains(cli, "internal/filmaudit") {
		t.Fatal("cmd/auditfilm 没有用 internal/filmaudit —— 命令行与 app 各算一套，结论会分叉")
	}
	if !strings.Contains(cli, "filmaudit.Audit(") {
		t.Error("cmd/auditfilm 引了包却没调 Audit —— 判据分叉（或调用被删了）")
	}
	app := readSrc(t, "film_audit.go")
	if !strings.Contains(app, "filmaudit.Audit(") {
		t.Error("app 内的体检没调同一套判据（自己另写一份判定）")
	}
	// 两边都必须用同一个级别口径（emoji / 计数），否则"⛔2 条"这种播报对不上。
	for _, want := range []string{"filmaudit.CountLevels(", "filmaudit.LevelEmoji("} {
		if !strings.Contains(app, want) {
			t.Errorf("app 内体检少了 %s —— 与命令行的级别口径不一致", want)
		}
	}
}
