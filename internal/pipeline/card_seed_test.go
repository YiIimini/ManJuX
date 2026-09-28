package pipeline

import "testing"

// 出图种子的**锚定键**：有卡片 id 就用 id。
//
// 【2026-09-28 审计 B6】种子原先一律按**显示名**锚定，而名字最容易漂（同一角色换个集
// 多一个「（丙十七）」注解、繁简标点不同、技能侧改次名），一漂种子就变 ——
// "同名必同图"这道跨集一致性的保险**静默失效**，表现得跟"跨集换脸"一模一样，
// 而日志和契约门都看不出问题（名字确实还是"同一个名字"）。
func TestCardSeedKeyPrefersID(t *testing.T) {
	if got := cardSeedKey("char_qiqi", "祁七"); got != "char_qiqi" {
		t.Errorf("有卡 id 时应以 id 为锚定键，实际 %q", got)
	}
	if got := cardSeedKey("", "祁七"); got != "祁七" {
		t.Errorf("没有卡 id 时退回显示名（老包/人工上传），实际 %q", got)
	}
	if got := cardSeedKey("  ", " 祁七 "); got != "祁七" {
		t.Errorf("空白 id 等同没有 id，实际 %q", got)
	}

	// **换名字写法不该换种子**（这正是 B6 要防的：同一个人的两种写法拿到同一张图）
	a := nameSeed("characters", cardSeedKey("char_qiqi", "祁七"))
	b := nameSeed("characters", cardSeedKey("char_qiqi", "祁七（丙十七）"))
	if a != b {
		t.Error("同一张卡换个名字写法就换了种子 —— 跨集一致性的保险会静默失效")
	}
	// 反过来：**不同的人必须拿到不同种子**（不然两个角色共用一张脸）
	c := nameSeed("characters", cardSeedKey("char_bingsheng", "碑生"))
	if a == c {
		t.Error("不同卡片 id 不该撞种子")
	}
}
