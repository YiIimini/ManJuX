package project

import (
	"strings"
	"testing"
)

// shorten_test.go 官方降级阶梯第二级：缩短到 ≤6 秒（references/fallback-policy.md）。
//
//	"Second retry: shorten the shot to ≤6s and split the dropped seconds into a new adjacent row."
//
// 平台化差异（如实记）：**不自动建新镜** —— 自动拆镜要凭空判断"这几秒该拍什么"，那是创作决定；
// 平台把砍掉的秒数如实报出来，补不补由人定。
func TestShortenShotPromptTrimsInternalCuts(t *testing.T) {
	h3 := "summary:\nx\n\nretention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved\n\n" +
		"detailed_description:\nThe style is cinematic.\n" +
		"[Shot 1] he walks in.\n" +
		"[Shot 2] At 00:03.000, he lifts the blade.\n" +
		"[Shot 3] At 00:06.000, he sets it down.\n" +
		"[Shot 4] At 00:09.000, he turns away.\n\n" +
		"overall_soundscape:\nwind.\n\nnon_diegetic_music:\nN/A."
	got, dur, ok := ShortenShotPrompt(h3, 12, 6)
	if !ok {
		t.Fatal("12 秒的片段应该能砍到 ≤6 秒")
	}
	if dur > 6 {
		t.Errorf("新时长应 ≤6 秒，实际 %d", dur)
	}
	if n := H3ClipCuts(got); n < 2 {
		t.Errorf("砍完仍要保留 ≥2 个小镜头（否则又变成一镜到底），实际 %d", n)
	}
	// 六段结构不能破坏
	for _, sec := range []string{"summary:", "retention_analysis:", "detailed_description:",
		"overall_soundscape:", "non_diegetic_music:"} {
		if !strings.Contains(got, sec) {
			t.Errorf("砍完丢了段落 %s", sec)
		}
	}
	// 被砍掉的尾巴不许还在
	if strings.Contains(got, "[Shot 4]") {
		t.Error("砍掉的第 4 个小镜头不该残留")
	}
}

func TestShortenShotPromptNoopCases(t *testing.T) {
	// 本来就 ≤6 秒：不动
	if _, _, ok := ShortenShotPrompt("detailed_description:\n[Shot 1] x.", 5, 6); ok {
		t.Error("时长已经不超限时不该改动")
	}
	// 只有一个小镜头：整段缩短（正文不动，时长改小）——这是官方"缩短片段"的最直白形式
	got, dur, ok := ShortenShotPrompt("detailed_description:\n[Shot 1] x.", 12, 6)
	if !ok || dur != 6 || !strings.Contains(got, "[Shot 1] x.") {
		t.Errorf("单镜头片段应只改时长，实际 (dur=%d, ok=%v)", dur, ok)
	}
}

func TestShortenNoteIsHonest(t *testing.T) {
	n := ShortenNote(7, 12, 6)
	for _, want := range []string{"镜 7", "12 秒 → 6 秒", "不擅自替你拆"} {
		if !strings.Contains(n, want) {
			t.Errorf("说明里要说清 %q，实际：%s", want, n)
		}
	}
}
