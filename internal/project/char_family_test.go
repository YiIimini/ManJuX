package project

import "testing"

// 「同角色的多张卡当一个人」的**唯一判据**穷举(技能侧清单 §4.2)。
//
// 数据形态取自《翻篇》真机实测(4 档翻篇 / 3 档糖球 / 2 档老墙):
// 名字带档名、id 带 vK。判错的两个方向代价不对称 ——
//
//	· 漏判(把变体当新角色):跨集复用时会给同一个人重新定妆,脸会漂;
//	· 误判(把两个人并成一个):两个人共用一张脸。
//
// 后者更坏,所以"两列互相矛盾时不合并"这条必须被测试钉住。
func TestCharFamilyKeyExhaustive(t *testing.T) {
	cases := []struct {
		name          string
		id, nm        string
		wantKey       string
		wantLabel     string
		wantBase      string
		wantVariantNo int
	}{
		{"主卡:id 无档位", "char_01", "翻篇", "char_1", "", "翻篇", 0},
		{"变体卡:vK 与档名", "char_01v4", "翻篇·归零", "char_1", "归零", "翻篇", 4},
		{"变体卡:v1", "char_01v1", "翻篇·收容所", "char_1", "收容所", "翻篇", 1},
		{"id 前导零归一:char_01 与 char_1 同族", "char_1", "翻篇", "char_1", "", "翻篇", 0},
		{"id 前导零归一(变体)", "char_007v2", "糖球·雨夜", "char_7", "雨夜", "糖球", 2},
		{"id 带分隔符的档位写法", "char_01_v2", "翻篇·进城", "char_1", "进城", "翻篇", 2},
		{"id 大写 V", "char_01V3", "翻篇·有名", "char_1", "有名", "翻篇", 3},
		{"无分隔符的无关名 → 自成一族", "char_02", "小喇叭", "char_2", "", "小喇叭", 0},
		{"全角中点", "char_05v2", "糖球・雨夜", "char_5", "雨夜", "糖球", 2},
		{"连字点", "char_05v2", "糖球‧雨夜", "char_5", "雨夜", "糖球", 2},
		{"下划线写法(名字)", "char_05v2", "糖球_雨夜", "char_5", "雨夜", "糖球", 2},
		{"id 认不出来时退回名字口径", "", "翻篇·归零", "翻篇", "归零", "翻篇", 0},
		{"id 认不出来、名字也没有档名", "", "小喇叭", "小喇叭", "", "小喇叭", 0},
		{"空值 → 空键(不成族)", "", "", "", "", "", 0},
		{"分隔符在首尾不算档名", "char_09", "·归零", "char_9", "", "·归零", 0},
		{"分隔符在尾不算档名", "char_09", "翻篇·", "char_9", "", "翻篇·", 0},
		// Q 版形象**独立成族**（2026-09-27 技能侧新口径：id 约定 chibi_<原角色id>，kind=chibi）。
		// 它**不是造型档**：造型档是"同一个人按场换服装"，Q 版是"同一个人的另一种画法"
		// （观众靠它认"这段是心里话"）。并进主卡的家族会同时坏两件事：
		//   ① 界面显示成"青禾 2 个造型档"（多出来的那张是什么，看不懂）；
		//   ② 资产库按"同一个人"复用 —— Q 版会去复用真人那张定妆照（拿真人照派生 Q 版是错的，
		//      这正是"派生"要单独把 ek==chibi 挡在外面的原因）。
		{"Q 版独立成族（不是造型档）", "chibi_pet", "青禾 · Q版", "chibi:chibi_pet", "Q版", "青禾", 0},
		{"id 既无编号又无分隔符 → 用 id", "abc", "", "abc", "", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CharFamilyKey(tc.id, tc.nm); got != tc.wantKey {
				t.Errorf("CharFamilyKey(%q,%q) = %q,期望 %q", tc.id, tc.nm, got, tc.wantKey)
			}
			if got := CharVariantLabel(tc.nm); got != tc.wantLabel {
				t.Errorf("CharVariantLabel(%q) = %q,期望 %q", tc.nm, got, tc.wantLabel)
			}
			if got := CharFamilyBaseName(tc.nm); got != tc.wantBase {
				t.Errorf("CharFamilyBaseName(%q) = %q,期望 %q", tc.nm, got, tc.wantBase)
			}
			if got := CharVariantNumberOf(tc.id); got != tc.wantVariantNo {
				t.Errorf("CharVariantNumberOf(%q) = %d,期望 %d", tc.id, got, tc.wantVariantNo)
			}
		})
	}
}

// 同角色的多张卡必须判成一个人;不同角色**任何情况下都不许**并成一个。
func TestSameCharacter(t *testing.T) {
	cases := []struct {
		name                   string
		aID, aName, bID, bName string
		want                   bool
	}{
		{"主卡与变体卡 → 同一个人", "char_01", "翻篇", "char_01v4", "翻篇·归零", true},
		{"两张变体卡 → 同一个人", "char_01v1", "翻篇·收容所", "char_01v2", "翻篇·进城", true},
		{"同一个人的另一种叫法(同名不同编号写法)", "char_01v4", "翻篇·归零", "char_1v4", "翻篇·归零", true},
		{"同前缀不同角色 → 不是一个人(不能并)", "char_01v1", "翻篇·收容所", "char_02", "小喇叭", false},
		{"名字相同但 id 分属两族 → 不合并(宁可多生成一张)", "char_01", "翻篇", "char_09", "翻篇", false},
		{"空值不与他人成族", "", "", "char_01", "翻篇", false},
		{"自己与自己", "char_04v2", "老墙·认错", "char_04v2", "老墙·认错", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameCharacter(tc.aID, tc.aName, tc.bID, tc.bName); got != tc.want {
				t.Fatalf("SameCharacter(%q,%q,%q,%q) = %v,期望 %v",
					tc.aID, tc.aName, tc.bID, tc.bName, got, tc.want)
			}
			// 对称性:判据不能只在一个方向上成立
			if got := SameCharacter(tc.bID, tc.bName, tc.aID, tc.aName); got != tc.want {
				t.Fatalf("判据必须对称:%v ≠ %v", got, tc.want)
			}
		})
	}
}

// 家族键两个口径都要能取到(id 口径 + 名字口径):找派生参考用宽松口径(任一命中)。
func TestCharFamilyKeys(t *testing.T) {
	cases := []struct {
		name             string
		id, nm           string
		wantID, wantName string
	}{
		{"两口径都取得到", "char_01v4", "翻篇·归零", "char_1", "翻篇"},
		{"id 认不出", "abc", "翻篇·归零", "", "翻篇"},
		{"名字没有档名", "char_02", "小喇叭", "char_2", "小喇叭"},
		{"全空", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idKey, nameKey := CharFamilyKeys(tc.id, tc.nm)
			if idKey != tc.wantID || nameKey != tc.wantName {
				t.Fatalf("CharFamilyKeys(%q,%q) = (%q,%q),期望 (%q,%q)",
					tc.id, tc.nm, idKey, nameKey, tc.wantID, tc.wantName)
			}
		})
	}
}

// 给人看的角色数:变体不额外计一个新角色(《翻篇》12 个角色 = 21 张造型卡)。
func TestCharFamilyCount(t *testing.T) {
	chars := []Character{
		{ID: "char_01v1", Name: "翻篇·收容所"},
		{ID: "char_01v2", Name: "翻篇·进城"},
		{ID: "char_05v2", Name: "糖球·雨夜"},
		{ID: "char_05v3", Name: "糖球·决裂"},
		{ID: "char_02", Name: "小喇叭"},
	}
	if got := CharFamilyCount(chars); got != 3 {
		t.Fatalf("5 张卡应算 3 个人,实际 %d", got)
	}
	if got := CharFamilyCount(nil); got != 0 {
		t.Errorf("空清单应为 0,实际 %d", got)
	}
	// 只有 id、没有名字(资产表里的形态)也要能归族
	onlyIDs := []Character{{ID: "char_04v1"}, {ID: "char_04v2"}}
	if got := CharFamilyCount(onlyIDs); got != 1 {
		t.Errorf("同一人的两张卡应算 1 个人,实际 %d", got)
	}
}
