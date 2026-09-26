package server

import "testing"

// TestEpisodesOfScopesToOneBook 「脚本创作」导入后要按同一口径开产线,而这条口径的口子
// (episodesOf)必须**只认这本书**:精确匹配 `^前缀·第N集`、按集号升序、绝不把另一本书
// 或前缀相近的书卷进来(2026-09-26 用户报「我点的是《求你们骂我》,结果是另一部」——
// 口径收成一处,才好一眼查清)。
func TestEpisodesOfScopesToOneBook(t *testing.T) {
	s := newTestServer(t)
	for _, name := range []string{
		"甲书·第02集·第二",
		"甲书·第01集·第一",
		"甲书·第10集·第十", // 两位数集号:必须按数字排序,不是字符串排序
		"乙书·第01集·第一",
		"甲书续集·第01集·第一", // 前缀相近但不是同一本
	} {
		newProjectForTest(t, s, name)
	}

	got := s.episodesOf("甲书")
	want := []string{"甲书·第01集·第一", "甲书·第02集·第二", "甲书·第10集·第十"}
	if len(got) != len(want) {
		t.Fatalf("《甲书》应有 %d 集,实际 %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个应为 %s,实际 %s(口径:精确前缀 + 集号升序)", i+1, want[i], got[i])
		}
	}
	if n := len(s.episodesOf("乙书")); n != 1 {
		t.Errorf("《乙书》应只有 1 集,实际 %d", n)
	}
	if n := len(s.episodesOf("甲书续集")); n != 1 {
		t.Errorf("前缀相近的书必须各归各的(甲书续集 %d 集)", n)
	}
	if n := len(s.episodesOf("丙书")); n != 0 {
		t.Errorf("不存在的书不该返回任何集,实际 %d", n)
	}
}
