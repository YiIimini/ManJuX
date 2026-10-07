package director

import (
	"os"
	"strings"
	"testing"
)

// 日志是给**用户**看的进度，不是开发者调用栈（2026-09-29 用户指着两条日志问：
// 「这种调用函数方法的是有问题的日志？」——原文是
// `五步专职会话：②人物道具 / ⑤分镜 **同时跑**（④场景已先跑完，场景名拿到手）`
// 与 `✔ ④场景提取 会话完成（27.6s）`）。
//
// 判据：会话进度那几句只能用"有结果了"的说法（已确定/已排出/开工），
// 不许出现内部编号与实现词（②③④⑤、"会话"、"同时跑"、"已先跑完"）。
// 为什么写在日志里：这一类话用户看一眼就懂；"谁先谁后、为什么"写在代码注释里就够。
func TestSessionProgressLogsAreUserFacing(t *testing.T) {
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", rel, err)
		}
		return string(raw)
	}
	// ① 完成的说法：四种步骤各自一句用户话
	for _, k := range []SessionKind{SessionCast, SessionViews, SessionScene, SessionStoryboard} {
		done := sessionDone(k)
		if done == "" || strings.ContainsAny(done, "②③④⑤") || strings.Contains(done, "会话") {
			t.Errorf("%s 的完成话术不面向用户：%q", k, done)
		}
		label := sessionLabel(k)
		if strings.ContainsAny(label, "②③④⑤") || strings.Contains(label, "会话") {
			t.Errorf("%s 的名称带内部编号/说法：%q", k, label)
		}
	}
	// ② 源码里那几句不许再出现实现口径的措辞（**只看代码行**：注释里引旧措辞当反面教材是允许的）
	src := stripCommentLines(read("sessions.go"))
	for _, bad := range []string{"会话完成", "同时跑", "已先跑完", "五步专职会话："} {
		if strings.Contains(src, bad) {
			t.Errorf("会话日志里还有实现口径的措辞 %q —— 日志会被用户当成内部调用栈", bad)
		}
	}
	for _, want := range []string{"同时开工", "sessionDone("} {
		if !strings.Contains(src, want) {
			t.Errorf("会话进度少了用户口径的写法 %q", want)
		}
	}
	// ③ 脚本阶段的入口那句同样只能是生产语言
	stage := stripCommentLines(read("../pipeline/stages_script.go"))
	if strings.Contains(stage, "五步专职会话") || strings.Contains(stage, "同时跑") {
		t.Error("脚本阶段的入口日志还是实现口径（🧩 五步专职会话…同时跑）")
	}
}

// TestHealReportTellsTheUserAboutFilledVoices 自愈改了什么必须**看得见**（盯新增的"补声线"那条）。
//
// 【2026-10-07】走锚表补出来的角色卡从来没有 voice（`pipeline.EnsureAnchorCast` 只写
// Description/ImagePrompt），渲染侧于是所有这类卡共用一个常数默认声线 —— 成片里几个角色同一个嗓子。
// 修法在 `project.HealPlan`（唯一读取边界）里补声线，但**补了必须播报**：否则用户只会觉得
// "怎么好几个声音一样"，而平台一声不吭（本仓对静默降级的铁律）。这条门盯住那段播报不被删。
func TestHealReportTellsTheUserAboutFilledVoices(t *testing.T) {
	raw, err := os.ReadFile("director.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripCommentLines(string(raw))
	for _, want := range []string{"hr.Voices", "project.VoiceSummary(hr.VoiceNames)"} {
		if !strings.Contains(src, want) {
			t.Errorf("自愈结果里的声线播报少了 %q —— 补了声线用户却看不到（静默降级）", want)
		}
	}
}

// stripCommentLines 去掉整行注释（本仓其它源码扫描门同一手法：注释里的反面教材不算违规）。
func stripCommentLines(src string) string {
	var out []string
	for _, ln := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}
