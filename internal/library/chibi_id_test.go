package library

import "testing"

// Q 版派生的库条目也必须能按**卡片 id** 取到（2026-09-29 用户：
// 「资产库 Q版形象封面显示异常」）。
//
// 真机：条目 `id=chibi_char_qiqi / name=祁七·Q版`，界面按卡片 id 取图 ⇒
// `looksLikeCardID("chibi_char_qiqi")` 返回 false（前缀表里没有 chibi_）⇒ 退回按名字找
// ⇒ 名字是 id 不是名 ⇒ **404**，封面就空了（实测：id 404 / 名字 200）。
func TestChibiEntryResolvesByCardID(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	img := mkImg(t, src, "chibi.png")
	if _, err := l.PutCard("characters", "chibi_char_qiqi", "祁七·Q版", "Q版形象", "Q版提示词", img, "proj", "generated"); err != nil {
		t.Fatal(err)
	}
	if !looksLikeCardID("chibi_char_qiqi") {
		t.Fatal("chibi_ 前缀没被认成卡片 id —— 界面按 id 取图会 404")
	}
	byID, _, ok := l.MatchCard("characters", "chibi_char_qiqi", "")
	if !ok {
		t.Fatal("按卡片 id 找不到 Q 版条目（封面会空）")
	}
	byName, _, ok2 := l.MatchCard("characters", "", "祁七·Q版")
	if !ok2 {
		t.Fatal("按名字找不到 Q 版条目")
	}
	if byID.ID != byName.ID || byID.Name != byName.Name {
		t.Fatalf("按 id 与按名字拿到的不是同一条：%s/%s vs %s/%s", byID.ID, byID.Name, byName.ID, byName.Name)
	}
	// 取图那条路也必须通（封面用的就是它）
	if p, err := l.ImagePathByName("characters", "chibi_char_qiqi", ""); err != nil || p == "" {
		t.Fatalf("按 id 取图失败：%v（封面会空）", err)
	}
}
