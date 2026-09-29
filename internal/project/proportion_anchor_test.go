package project

import "testing"

// 头身比锚自检（2026-09-29 用户：「碑生（丙十七）、钟离、碑生·Q版、崔无咎 这几个定妆照的
// 比例就不对」）。取景尺度平台侧已补；这道门管的是**锚本身**是否可信、是否自相矛盾。
//
// 夹具用真机卡面的写法（「约七头半身」「比例锚_机器：7.5 头身」「2.5–3 头身」）。
func TestProportionAnchorIssues(t *testing.T) {
	// ① 取值：阿拉伯、中文、带"半"
	for _, tc := range []struct {
		in   string
		want float64
		ok   bool
	}{
		{"瘦高，约七头半身，锁骨明显", 7.5, true},
		{"比例锚_机器：8 头身", 8, true},
		{"约六头身，矮壮", 6, true},
		{"碑生的 Q 版小人，2.5–3 头身", 3, true}, // 区间取"紧挨着头身"的那个数（上界）
		{"没有写头身比", 0, false},
	} {
		got, ok := HeadToBodyRatio(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("HeadToBodyRatio(%q) = %v,%v；期望 %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	// ② 自相矛盾要报出来（这两条正是用户点名那批卡里最可能的组合）
	bad := []struct {
		name, desc, why string
		chibi           bool
	}{
		{"崔无咎", "五十余，矮小驼背，约七头半身", "矮小驼背配七头半身", false},
		{"钟离", "清瘦挺直，高挑，约六头身", "高挑配六头身", false},
		{"某角色", "身形：高壮，肩极宽，约十二头身", "头身比超出人形区间", false},
		{"碑生·Q版", "碑生的 Q 版小人，3.5 头身，巴掌大", "Q 版头身比超出 2–3", true},
	}
	for _, b := range bad {
		if got := CharacterProportionIssue(b.name, b.desc, b.chibi); got == "" {
			t.Errorf("「%s」应当报出问题（%s），却没报", b.name, b.why)
		}
	}

	// ③ 正常卡不许误报（真机里这几张是合法的）
	good := []struct {
		name, desc string
		chibi      bool
	}{
		{"碑生（丙十七）", "二十出头，瘦高，肩薄，约七头半身，锁骨明显", false},
		{"邢五", "四十余岁，中等身量偏胖，约七头身", false},
		{"哑七", "七十余岁，矮瘦，背驼，约六头半身", false},
		{"碑生·Q版", "碑生的 Q 版小人，2.5 头身，巴掌大，总蹲在碑生左肩上", true},
		{"没写头身比的角色", "气质清冷，白衣", false},
	}
	for _, g := range good {
		if got := CharacterProportionIssue(g.name, g.desc, g.chibi); got != "" {
			t.Errorf("「%s」被误报：%s", g.name, got)
		}
	}

	// ④ 方案级入口：Q 版按名字/类型识别，别拿人形区间去量 Q 版
	plan := &Plan{Characters: []Character{
		{ID: "char_x", Name: "某角色", Description: "瘦高，约七头半身"},
		{ID: "chibi_char_x", Name: "某角色·Q版", Kind: "chibi", Description: "Q 版小人，2.5 头身"},
	}}
	if got := ProportionAnchorIssues(plan); len(got) != 0 {
		t.Errorf("合法方案不该报问题，实得：%v", got)
	}
}
