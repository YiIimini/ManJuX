package project

import "testing"

// char_family_stage_test.go 「同角色不同阶段」怎么判 —— 用户 2026-09-28 问的那条。
//
// 技能侧的造型档约定有两种写法，两种都要认成**同一个人**：
//
//	id 侧：`char_01` 与 `char_01v2`（档位号后缀）
//	名字侧：`沈砚` 与 `沈砚·少年`（间隔号后接档名 —— 剧情阶段/换装都走这一种）
//
// 但**两边混用**时此前会漏：主卡 id 规范（`char_01`）、变体卡 id 不规范（`char_01_young`
// 之类没有档位号）⇒ 主卡按 id 得到家族键、变体按名字得到另一个键，**永远碰不上** ✗。
// 后果不只是计数不准：撞脸自检会把"同一个人的少年期与成年期"当成两个角色比，
// 然后报"这两个人长得像同一个人"（用户刚报过的那类假警的另一种形态）。
func TestSameCharacterAcrossStages(t *testing.T) {
	cases := []struct {
		name                   string
		aID, aName, bID, bName string
		want                   bool
	}{
		// ① id 规范：档位号
		{"id 档位号", "char_01", "沈砚", "char_01v2", "沈砚·少年", true},
		// ② 名字规范：间隔号后接档名（阶段/换装）
		{"名字档名", "c_a", "沈砚", "c_b", "沈砚·少年", true},
		// ③ 混合：一边 id 规范、一边 id 认不出，靠名字认出来 —— 这一条此前是漏的
		{"混合：主卡 + id 不规范的变体", "char_01", "沈砚", "char_01_young", "沈砚·少年", true},
		{"混合：变体在前也一样", "char_01_young", "沈砚·少年", "char_01", "沈砚", true},
		// ④ 两边 id 都能解析但不同族 ⇒ 仍不许并（既有取舍：宁可多生成一张卡）
		{"两边 id 都能解析但不同族", "char_01", "沈砚", "char_02", "沈砚", false},
		{"不同角色", "char_01", "沈砚", "char_02", "周廷", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SameCharacter(tc.aID, tc.aName, tc.bID, tc.bName)
			if got != tc.want {
				t.Fatalf("SameCharacter(%q,%q,%q,%q) = %v,期望 %v", tc.aID, tc.aName, tc.bID, tc.bName, got, tc.want)
			}
			// 判据必须**对称**（哪张卡先问都一样）
			if rev := SameCharacter(tc.bID, tc.bName, tc.aID, tc.aName); rev != got {
				t.Errorf("不对称：正向 %v / 反向 %v", got, rev)
			}
		})
	}
}

// TestCharFamilyIndexStages 分组口径与 SameCharacter 必须一致（两处各写一遍必然走散）。
func TestCharFamilyIndexStages(t *testing.T) {
	chars := []Character{
		{ID: "char_01", Name: "沈砚"},
		{ID: "char_01_young", Name: "沈砚·少年"}, // id 不规范，靠名字归族
		{ID: "char_01v2", Name: "沈砚·成年"},
		{ID: "char_02", Name: "周廷"},
		{ID: "chibi_char_01", Name: "沈砚·Q版"}, // Q 版独立成族，不是造型档
	}
	idx := CharFamilyIndex(chars)
	if idx["char_01"] != idx["char_01_young"] || idx["char_01"] != idx["char_01v2"] {
		t.Errorf("同一个人的三档应归一组：%v", idx)
	}
	if idx["char_01"] == idx["char_02"] {
		t.Error("不同角色不能并成一组")
	}
	if idx["chibi_char_01"] == idx["char_01"] {
		t.Error("Q 版不是造型档，应独立成族")
	}
	if n := CharFamilyCount(chars); n != 3 {
		t.Errorf("应算 3 个人（沈砚 / 周廷 / 沈砚Q版），实际 %d", n)
	}
}
