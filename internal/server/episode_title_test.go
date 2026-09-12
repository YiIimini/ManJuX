package server

import "testing"

// TestNormalizeEpisodeTitles 集名兜底:章名即集名。
//
// 回归 2026-09-12:提示词已要求 LLM「TITLE 直接取章标题原文」,但 LLM 仍会自起
// 文艺标题(第001章《第一万三千七百四十一号》被起成《七文一斤》),集名脱离章名
// 后「章→集」失去可对账性,章节覆盖核对随之误报缺章。
func TestNormalizeEpisodeTitles(t *testing.T) {
	eps := []epItem{
		{Ep: 1, Title: "七文一斤", Chapters: "第001章 第一万三千七百四十一号"}, // LLM 自起名 → 应被章名覆盖
		{Ep: 2, Title: "送去北境", Chapters: "第1章~第2章"},           // 区间无标题 → 保留原集名
		{Ep: 3, Title: "", Chapters: "第002章 雨夜不点火"},           // 空 → 用章名填
		{Ep: 4, Title: "器不可以成道", Chapters: "第003章 器不可以成道"},    // 本来就一致 → 不变
	}
	normalizeEpisodeTitles(eps)

	want := []string{"第一万三千七百四十一号", "送去北境", "雨夜不点火", "器不可以成道"}
	for i, w := range want {
		if eps[i].Title != w {
			t.Errorf("第 %d 集标题 = %q,期望 %q", i+1, eps[i].Title, w)
		}
	}
}

// TestCheckOneChapterPerEpisode 检测「一章被拆成多集」。
func TestCheckOneChapterPerEpisode(t *testing.T) {
	eps := []epItem{
		{Ep: 1, Chapters: "第1章"},
		{Ep: 2, Chapters: "第1章"}, // 第 1 章被拆成两集 → 应检出
		{Ep: 3, Chapters: "第2章"},
		{Ep: 4, Chapters: "第3章~第4章"}, // 跨章区间不参与判定
	}
	dup := checkOneChapterPerEpisode(eps)
	if len(dup) != 1 || dup[0] != 1 {
		t.Errorf("应检出第 1 章被拆成多集,实际 %v", dup)
	}

	// 正常的一章一集不应误报
	ok := []epItem{
		{Ep: 1, Chapters: "第1章"}, {Ep: 2, Chapters: "第2章"}, {Ep: 3, Chapters: "第3章"},
	}
	if got := checkOneChapterPerEpisode(ok); len(got) != 0 {
		t.Errorf("一章一集不应报拆分,实际 %v", got)
	}
}
