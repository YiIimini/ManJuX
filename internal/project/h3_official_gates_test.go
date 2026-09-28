package project

import (
	"strings"
	"testing"
)

// h3_official_gates_test.go 官方 §2/§5.4 里**有要求、此前没有门**的五条（2026-09-28 全面审计补齐）。
//
// 这五条都属于"写错了不报错、只是成片变差/听感变怪"的那一类，所以必须机器判：
//
//	· <d> 必须带语言标签；
//	· <d> 标点归一化（去波浪号/重复装饰标点，整句以 . ? ! 收尾）；
//	· <Picture N> / <Subject N> 编号连续不跳号；
//	· 分配了说话人 (Sx) 就必须在正文里真的说过话。
func TestDialogueTagLanguageAndPunctuation(t *testing.T) {
	base := "summary:\nx\n\nretention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved\n\n" +
		"detailed_description:\nThe style is cinematic.\n[Shot 1] he speaks. %s\n\n" +
		"overall_soundscape:\nwind.\n\nnon_diegetic_music:\nN/A."
	cases := []struct {
		name, line, want string
	}{
		{"正常", "<Subject 1> (S1) says: <d>[Chinese]我准了。</d>", ""},
		{"缺语言标签", "<Subject 1> (S1) says: <d>我准了。</d>", "没有**语言标签**"},
		{"波浪号", "<Subject 1> (S1) says: <d>[Chinese]我准了~</d>", "标点不规范"},
		{"重复感叹号", "<Subject 1> (S1) says: <d>[Chinese]我准了！！</d>", "标点不规范"},
		{"结尾没句末标点", "<Subject 1> (S1) says: <d>[Chinese]我准了</d>", "标点不规范"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := H3FormatViolations(strings.Replace(base, "%s", tc.line, 1))
			if tc.want == "" {
				for _, v := range got {
					if strings.Contains(v, "语言标签") || strings.Contains(v, "标点不规范") {
						t.Errorf("这条不该报：%s", v)
					}
				}
				return
			}
			found := false
			for _, v := range got {
				if strings.Contains(v, tc.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("应报 %q，实际 %v", tc.want, got)
			}
		})
	}
}

func TestLabelNumberingGaps(t *testing.T) {
	// 用 <Picture 1> 与 <Picture 3>（跳了 2）⇒ 报
	h3 := "summary:\nx\n\nretention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved\n\n" +
		"detailed_description:\nThe style is cinematic. <Picture 1> is his face, <Picture 3> is the stele.\n" +
		"[Shot 1] he stands.\n\noverall_soundscape:\nwind.\n\nnon_diegetic_music:\nN/A."
	got := H3FormatViolations(h3)
	found := false
	for _, v := range got {
		if strings.Contains(v, "编号跳号") {
			found = true
		}
	}
	if !found {
		t.Errorf("跳号要报，实际 %v", got)
	}
	// 连续编号不报
	ok := strings.Replace(h3, "<Picture 3>", "<Picture 2>", 1)
	for _, v := range H3FormatViolations(ok) {
		if strings.Contains(v, "编号跳号") {
			t.Errorf("连续编号不该报：%s", v)
		}
	}
}

func TestUnusedSpeakerID(t *testing.T) {
	// subject_definitions 里分配了 (S2)，正文里只有 (S1) 在说话 ⇒ 报"定义了却从未说话"
	h3 := "subject_definitions:\n- <Subject 1> (S1): he is the stone-cutter.\n- <Subject 2> (S2): she is the timekeeper.\n\n" +
		"summary:\nx\n\nretention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved\n\n" +
		"detailed_description:\nThe style is cinematic.\n[Shot 1] <Subject 1> (S1) says: <d>[Chinese]我准了。</d>\n\n" +
		"overall_soundscape:\nwind.\n\nnon_diegetic_music:\nN/A."
	got := H3FormatViolations(h3)
	found := false
	for _, v := range got {
		if strings.Contains(v, "从未在正文里说话") {
			found = true
		}
	}
	if !found {
		t.Errorf("分配了没用过的说话人号要报，实际 %v", got)
	}
	// 两个都说了话就不报
	ok := strings.Replace(h3, "<Subject 1> (S1) says: <d>[Chinese]我准了。</d>",
		"<Subject 1> (S1) says: <d>[Chinese]我准了。</d> <Subject 2> (S2) says: <d>[Chinese]你准不了。</d>", 1)
	for _, v := range H3FormatViolations(ok) {
		if strings.Contains(v, "从未在正文里说话") {
			t.Errorf("都用过就不该报：%s", v)
		}
	}
}

// TestStoryboardLabelsAllBanned 官方 model-selection.md 的 per-shot 规则点名五个双向绑定标签：
// "[char:…] [scene:…] [shot:…] [dur:…] [hook:…]" —— 一个都不许进成片提示词。
//
// 平台此前的词表只有前三个（[dur: 与 [hook: 漏了）——"抄清单只抄一半"的典型。
func TestStoryboardLabelsAllBanned(t *testing.T) {
	official := []string{"[char:", "[scene:", "[shot:", "[dur:", "[hook:"}
	for _, tag := range official {
		if !containsAny(strings.ToLower(strings.Join(h3TraceTags, " ")), tag) {
			t.Errorf("官方点名的标签 %s 必须在禁用词表里", tag)
		}
		// 真造一条正文，确认门真的会响
		h3 := "detailed_description:\nThe style is cinematic.\n[Shot 1] he stands " + tag + "x]."
		found := false
		for _, v := range H3FormatViolations(h3) {
			if strings.Contains(v, "内部标签") {
				found = true
			}
		}
		if !found {
			t.Errorf("正文里出现 %s 时要报"+"内部标签", tag)
		}
	}
}
