package project

import (
	"strings"
	"testing"
)

// voiceover_test.go **画外演出**的契约（技能侧 2026-09-27 口径 · 用户定调）。
//
// 三类检查各自的后果不一样，测试要能分辨：
//   fail — 引用悬空：渲染时那张图被静默丢掉（"该出 Q 版却出了本人"而日志无错）；
//   warn — 演出句没写进正文 / 取值不认识 / 面板没进 prop_ids：模型会自己发挥。

// voPlan 一份最小的画外演出方案：一个角色（含 Q 版卡）＋ 一件面板道具 ＋ 一集两镜。
func voPlan() *Plan {
	return &Plan{
		Characters: []Character{
			{ID: "char_01", Name: "沈砚", ImagePrompt: "IDENTITY LOCK (do not change): nineteen years old"},
			{ID: "chibi_char_01", Name: "沈砚·Q版", Kind: "chibi", ImagePrompt: "chibi proportions, 2.5 heads"},
			{ID: "chibi_sys_01", Name: "系统小机器人", Kind: "chibi", ImagePrompt: "capsule robot, single-eye visor"},
		},
		Props:  []Prop{{ID: "prop_holo_01", Name: "全息面板", ImagePrompt: "floating holographic panel"}},
		Scenes: []Scene{{ID: "scene_01", Name: "临水城", Description: "城门", ImagePrompt: "gate plate"}},
		Shots: []Shot{
			{
				ID: 1, Scene: "scene_01", ShotSize: "中景", Duration: 5, Mode: "ref2va",
				Characters: []string{"char_01"}, ChibiIDs: []string{"chibi_char_01"}, VoKind: VoKindInner,
				Action: "沈砚心里盘算", H3Prompt: "detailed_description:\nGUOFENG. [Shot 1] The inner monologue is performed by the CHIBI (Q-version) figure of Shen Yan. lips remain completely closed.",
			},
			{
				ID: 2, Scene: "scene_01", ShotSize: "中景", Duration: 5, Mode: "ref2va",
				Characters: []string{"char_01"}, VoKind: VoKindSystem,
				PropIDs:      []string{"prop_holo_01"},
				SystemAssets: map[string]string{"bot": "chibi_sys_01", "panel": "prop_holo_01"},
				Action:       "系统提示", H3Prompt: "detailed_description:\nGUOFENG. [Shot 1] The system interface is performed by a floating translucent HOLOGRAPHIC PANEL (prop) accompanied by a cute CHIBI ROBOT companion. lips remain completely closed.",
			},
		},
	}
}

func voIssues(t *testing.T, p *Plan) (fails, warns []string) {
	t.Helper()
	for _, is := range ValidatePlan(p) {
		if is.Level == "fail" {
			fails = append(fails, is.Msg)
		} else {
			warns = append(warns, is.Msg)
		}
	}
	return
}

func TestVoiceoverShotsCleanPlanPasses(t *testing.T) {
	fails, _ := voIssues(t, voPlan())
	if len(fails) != 0 {
		t.Fatalf("写对的方案不该有 fail：%v", fails)
	}
}

// TestVoiceoverDanglingRefsAreFatal 悬空引用 = fail（渲染时静默丢图，日志里一条错都没有）。
func TestVoiceoverDanglingRefsAreFatal(t *testing.T) {
	cases := []struct {
		name string
		mut  func(p *Plan)
		want string
	}{
		{"chibi_ids 指向不存在的卡", func(p *Plan) { p.Shots[0].ChibiIDs = []string{"chibi_nope"} }, "chibi_ids→chibi_nope"},
		{"inner 但 chibi_ids 为空", func(p *Plan) { p.Shots[0].ChibiIDs = nil }, "chibi_ids 为空"},
		{"system 的 bot 不存在", func(p *Plan) { p.Shots[1].SystemAssets["bot"] = "chibi_nope" }, "system_assets.bot→chibi_nope"},
		{"system 的 panel 不存在", func(p *Plan) { p.Shots[1].SystemAssets["panel"] = "prop_nope" }, "system_assets.panel→prop_nope"},
		{"system 的 panel 为空", func(p *Plan) { delete(p.Shots[1].SystemAssets, "panel") }, "system_assets.panel 为空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := voPlan()
			tc.mut(p)
			fails, _ := voIssues(t, p)
			if !hasMsg(fails, tc.want) {
				t.Errorf("应判 fail 且提到 %q，实际 fails=%v", tc.want, fails)
			}
		})
	}
}

// TestVoiceoverPerformanceClauseWarned 演出句必须逐字进正文（字段填了不算）。
func TestVoiceoverPerformanceClauseWarned(t *testing.T) {
	p := voPlan()
	p.Shots[0].H3Prompt = "detailed_description:\nGUOFENG. [Shot 1] 他想着心事。"
	p.Shots[1].H3Prompt = "detailed_description:\nGUOFENG. [Shot 1] 系统提示。"
	_, warns := voIssues(t, p)
	if !hasMsg(warns, "内心镜") || !hasMsg(warns, "系统镜") {
		t.Errorf("两处演出句缺失都要 warn，实际 %v", warns)
	}
	// 判据大小写不敏感（技能侧写的是 The inner monologue is performed by…）
	p2 := voPlan()
	p2.Shots[0].H3Prompt = "x performed by the chibi figure of Shen Yan"
	if _, warns := voIssues(t, p2); hasMsg(warns, "内心镜") {
		t.Error("大小写不同不该算缺失")
	}
}

// TestVoiceoverNarrationAccepted 旁白（narration）是**合法**取值，且不要求演出句。
//
// 实测依据：《求你们骂我》2462 镜里 739 镜标了 narration —— 不认它就会一次报 739 条假警。
func TestVoiceoverNarrationAccepted(t *testing.T) {
	p := voPlan()
	p.Shots[0].VoKind = VoKindNarration
	p.Shots[0].ChibiIDs = nil
	p.Shots[0].H3Prompt = "detailed_description:\nGUOFENG. [Shot 1] 画外音在说，画面里的人不说话。"
	p.Shots[1] = Shot{ID: 2, Scene: "scene_01", ShotSize: "中景", Duration: 5, Mode: "ref2va", Action: "普通镜", H3Prompt: "detailed_description:\nGUOFENG. [Shot 1] 普通镜。"}
	p.Shots = p.Shots[:2]
	fails, warns := voIssues(t, p)
	if len(fails) != 0 {
		t.Fatalf("旁白镜不该有 fail：%v", fails)
	}
	if hasMsg(warns, "取值不认识") || hasMsg(warns, "演出") {
		t.Errorf("旁白镜不该被要求演出句、也不该算未知取值：%v", warns)
	}
}

// TestVoiceoverUnknownKindWarned 取值不认识要提示（会被当成普通镜渲染）。
func TestVoiceoverUnknownKindWarned(t *testing.T) {
	p := voPlan()
	p.Shots[0].VoKind = "monologue"
	_, warns := voIssues(t, p)
	if !hasMsg(warns, "vo_kind 取值不认识") {
		t.Errorf("未知取值要 warn，实际 %v", warns)
	}
}

// TestVoiceoverPanelMustBeInPropIDs 口径要求 system 镜的 prop_ids 必含面板。
func TestVoiceoverPanelMustBeInPropIDs(t *testing.T) {
	p := voPlan()
	p.Shots[1].PropIDs = nil
	_, warns := voIssues(t, p)
	if !hasMsg(warns, "prop_ids") {
		t.Errorf("面板不在 prop_ids 时要 warn，实际 %v", warns)
	}
}

// TestNoVoiceoverShotsNoCheck 没有画外演出镜时整段不跑（不刷无意义的门）。
func TestNoVoiceoverShotsNoCheck(t *testing.T) {
	p := voPlan()
	p.Shots[0].VoKind, p.Shots[1].VoKind = "", ""
	p.Shots[0].ChibiIDs, p.Shots[1].SystemAssets = nil, nil
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "vo_kind") || strings.Contains(is.Msg, "演出") {
			t.Errorf("没有画外演出镜时不该报这类：%s", is.Msg)
		}
	}
	if p.HasVoiceoverShots() {
		t.Error("HasVoiceoverShots 应为 false")
	}
}

// TestIsChibiCard Q 版判定：kind 与 id 约定两条路都要认。
func TestIsChibiCard(t *testing.T) {
	cases := []struct {
		kind, id string
		want     bool
	}{
		{"chibi", "p01", true},
		{"Chibi", "p01", true},
		{"", "chibi_char_01", true},
		{"", "chibi_sys_01", true},
		{"human", "char_01", false},
		{"", "char_01v2", false},
	}
	for _, tc := range cases {
		if got := IsChibiCard(tc.kind, tc.id); got != tc.want {
			t.Errorf("IsChibiCard(%q,%q) = %v,期望 %v", tc.kind, tc.id, got, tc.want)
		}
	}
}

func hasMsg(list []string, sub string) bool {
	for _, m := range list {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}
