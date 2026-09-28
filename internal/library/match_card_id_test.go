package library

import (
	"testing"
)

// 界面那条路（`{name}` 段）必须**同时**接受卡片 id（2026-09-28 审计 B13）。
//
// 此前只按名字找，而"按名字"在**同名不同人**时只能到第一条：
// 第二条条目在界面上访问不到，而且点删除/重抽会落到**第一条**身上 ——
// 七条路由（detail/image/DELETE/regen/views/describe/setMeta）都走这条路。
func TestMatchAcceptsCardID(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	// 两个**同名但不同卡**的角色（真实形态：同名不同人是合法数据）
	if _, err := l.PutCard("characters", "char_laozhang_a", "老张", "修表匠", "P",
		mkImg(t, src, "a.png"), "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PutCard("characters", "char_laozhang_b", "老张", "快递员", "P",
		mkImg(t, src, "b.png"), "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}

	// ① 按 id 访问：两条**各自可达**（这正是界面此前做不到的事）
	a, _, ok := l.Match("characters", "char_laozhang_a")
	if !ok || a == nil {
		t.Fatal("按卡片 id 必须能访问到条目")
	}
	b, _, ok2 := l.Match("characters", "char_laozhang_b")
	if !ok2 || b == nil {
		t.Fatal("同名两张卡里的**第二张**也必须能按 id 访问到（这就是 B13）")
	}
	if a.ID == b.ID {
		t.Errorf("按 id 访问拿到了同一条（%s）—— 第二条仍然不可达", a.ID)
	}
	if a.Description == b.Description {
		t.Errorf("两张卡该各自取到自己的内容，实际都是 %q", a.Description)
	}

	// ② 按名字访问：**行为不变**（落到其中一条，存量调用零影响）
	if _, _, ok := l.Match("characters", "老张"); !ok {
		t.Error("按名字访问必须照旧可用")
	}

	// ③ 中文名绝不会被误判成卡片 id
	for _, name := range []string{"老张", "沈砚·一品", "守钟女", "系统面板"} {
		if looksLikeCardID(name) {
			t.Errorf("%q 是显示名，不该被判成卡片 id", name)
		}
	}
	for _, id := range []string{"char_a", "char:char_a", "prop_knife", "scene_room"} {
		if !looksLikeCardID(id) {
			t.Errorf("%q 是平台发的卡片 id，该被认出来", id)
		}
	}
}
