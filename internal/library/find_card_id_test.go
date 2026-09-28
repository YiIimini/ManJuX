package library

import (
	"testing"
)

// 库侧模糊匹配必须**排除 id 明确不同的条目**。
//
// 【2026-09-28 审计 B11】`FindForCard` 在 `matchByID` 未命中时会落到名字/描述的**模糊匹配**
// （`sameVariantCard` ＋ 双向子串 ＋ 描述相似度 ≥0.6）。而 `sameVariantCard` 是
// `CharVariantLabel(a) == CharVariantLabel(b)` —— **两边都没有档名时恒为真**，
// 于是"同名不同人"这条路完全没有防护：
//
//	库里已有「山贼」(char_shanzei_a)，本集新角色叫「山贼甲」(char_shanzei_b)，
//	两者名字互为子串、描述又都是"粗布短打的中年汉子" → **认领别人的条目**，
//	这个角色被锚到别人的脸上，还会把"山贼甲"写进那条目的别名表。
//
// 有 id 的场合这条根本不该发生：**id 不同就是两张卡**。
func TestFindForCardRefusesDifferentCardID(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	const desc = "粗布短打的中年汉子，腰间别一把柴刀"
	if _, err := l.PutCard("characters", "char_shanzei_a", "山贼", desc, "P",
		mkImg(t, src, "a.png"), "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}

	// ① **id 不同**的新角色：名字是子串、描述一模一样 —— 也不许认领
	if e, _, ok := l.FindForCard("characters", "char_shanzei_b", "山贼甲", desc); ok {
		t.Errorf("id 明确不同（char_shanzei_a vs char_shanzei_b）就不该认领，实际认到了 %q", e.Name)
	}

	// ② **同一张卡换叫法**（id 相同）：名字对不上也要能认到 —— 这条是原有的正确行为，别改坏
	if e, _, ok := l.FindForCard("characters", "char_shanzei_a", "山贼甲", desc); !ok {
		t.Error("同一张卡（id 相同）换叫法时必须认到，退回老行为才是回归")
	} else if e.Name != "山贼" {
		t.Errorf("该认到库里那条「山贼」，实际 %q", e.Name)
	}

	// ③ 对方**没有 id**（存量条目/手工建的）：没有判据，退回按名字的模糊匹配
	if _, err := l.PutCard("characters", "", "老猎户", desc, "P",
		mkImg(t, src, "b.png"), "ep01", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.FindForCard("characters", "char_newbie", "老猎户", desc); !ok {
		t.Error("对方条目没有 id 时只能按名字判，应能认到（不退这一步会破坏存量库）")
	}
}

// sameCardIDOrUnknown 的三态（它是上面那条门背后的唯一判据）。
func TestSameCardIDOrUnknown(t *testing.T) {
	cases := []struct {
		cand, self string
		want       bool
	}{
		{"char_a", "char_a", true},
		{"char_a", "char_b", false}, // 都有 id 且不同 ⇒ 两张卡
		{"", "char_b", true},        // 对方没 id ⇒ 只能按名字判
		{"char_a", "", true},        // 自己没 id ⇒ 没有判据
		{"  ", "char_b", true},
		{"char_a", "  ", true},
	}
	for _, c := range cases {
		if got := sameCardIDOrUnknown(c.cand, c.self); got != c.want {
			t.Errorf("sameCardIDOrUnknown(%q,%q) = %v,期望 %v", c.cand, c.self, got, c.want)
		}
	}
}
