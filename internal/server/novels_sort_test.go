package server

import (
	"net/http/httptest"
	"testing"
)

// 小说库排序契约(用户 2026-09-24 定调:**默认倒序**)。
//
// 为什么值得单测:排序口径原来直接写在 handler 里的一行 sort.Slice,
// 谁都能顺手改回升序而没人发现 —— 而"新加的书排在列表最后"恰恰是最容易被
// 用户当成 bug 来回报的那类小回归。
// 注意:中文书名走的是**码点序**(阿 U+963F / 造 U+9020 / 明 U+660E),
// 不是拼音序 —— 这不是本次引入的,而是原来那行 `Name < Name` 的既有口径;
// 这次只把方向翻成倒序。要改成拼音/笔画序得引入排序表,另作一次改动。
func TestSortNovelsDefaultIsDescending(t *testing.T) {
	items := []novelItem{
		{Name: "阿"},
		{Name: "造"},
		{Name: "明"},
	}
	sortNovels(items, true) // 默认
	got := []string{items[0].Name, items[1].Name, items[2].Name}
	want := []string{"阿", "造", "明"} // 码点倒序
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("默认倒序不对:得到 %v,期望 %v", got, want)
		}
	}
}

func TestSortNovelsAsc(t *testing.T) {
	items := []novelItem{{Name: "阿"}, {Name: "造"}, {Name: "明"}}
	sortNovels(items, false)
	got := []string{items[0].Name, items[1].Name, items[2].Name}
	want := []string{"明", "造", "阿"} // 码点升序
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("升序不对:得到 %v,期望 %v", got, want)
		}
	}
}

// TestSortNovelsReverseOfAsc 倒序必须是升序的严格反转(用户要的就是"反过来")。
func TestSortNovelsReverseOfAsc(t *testing.T) {
	base := []novelItem{{Name: "beta"}, {Name: "Alpha"}, {Name: "03"}, {Name: "11"}}
	asc := append([]novelItem(nil), base...)
	desc := append([]novelItem(nil), base...)
	sortNovels(asc, false)
	sortNovels(desc, true)
	for i := range asc {
		if asc[i].Name != desc[len(desc)-1-i].Name {
			t.Fatalf("倒序不是升序的反转:asc=%v desc=%v", names(asc), names(desc))
		}
	}
}

func names(items []novelItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

// TestNovelSortQueryDefault 参数缺省即倒序:只有显式 ?sort=asc 才升序。
func TestNovelSortQueryDefault(t *testing.T) {
	cases := []struct {
		url  string
		desc bool
	}{
		{"/api/novels", true},
		{"/api/novels?", true},
		{"/api/novels?sort=", true},
		{"/api/novels?sort=asc", false},
		{"/api/novels?sort=ASC", false},
		{"/api/novels?sort=%20asc%20", false}, // 前后空白要容错
		{"/api/novels?sort=desc", true},
		{"/api/novels?sort=whatever", true},
		{"/api/novels?t=1&sort=asc", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", c.url, nil)
		if got := novelSortDesc(r); got != c.desc {
			t.Errorf("%s → desc=%v,期望 %v", c.url, got, c.desc)
		}
	}
}
