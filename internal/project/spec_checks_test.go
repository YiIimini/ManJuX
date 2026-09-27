package project

import (
	"strings"
	"testing"
)

// spec_checks_test.go 词表类判据（技能侧《平台侧生成规范》§6.2 / §2.4 / §4.1 / §6.9）。
//
// 这四条都是"照词表判"的：词表本身就是口径，所以测试同时钉**判据**与**词表内容**
// （有人把词表改窄了，这里要红）。

// ── §6.2 机位角度词 ────────────────────────────────────────────────────────
func TestAngleWord(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"平视", true},
		{"仰拍", true},
		{"俯拍", true},
		{"鸟瞰", true},
		{"慢动作，中景，低角度仰拍", true}, // §6.2 的组合写法：含其一即算命中
		{"", true}, // 空值合法（存量/平台直出方案不写这个字段）
		{"眼平线", false},
		{"特写", false}, // 景别不是角度词
	}
	for _, tc := range cases {
		if got := AngleWordOK(tc.in); got != tc.want {
			t.Errorf("AngleWordOK(%q) = %v,期望 %v", tc.in, got, tc.want)
		}
	}
	if len(PlanAngleWords) != 4 {
		t.Errorf("角度词表就四个：平视/仰拍/俯拍/鸟瞰，实际 %v", PlanAngleWords)
	}
}

// ── §2.4 敏感词与安全替代 ──────────────────────────────────────────────────
func TestBannedWording(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantWord string
	}{
		{"赤足", "she walks the road 赤足", "赤足"},
		{"深V", "a 深V neckline dress", "深V"},
		{"超模身材", "his 超模身材 draws every eye", "超模身材"},
		{"毛孔可见", "skin with 毛孔可见", "毛孔可见"},
		{"英文写法", "he is barefoot on the stones", "barefoot"},
		{"英文 see-through", "a see-through sleeve", "see-through"},
		{"干净文本", "a lean young man in a grey linen robe", ""},
		{"空文本", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, hint := bannedWordingIn(tc.in)
			if w != tc.wantWord {
				t.Fatalf("bannedWordingIn(%q) 命中 %q,期望 %q", tc.in, w, tc.wantWord)
			}
			if w != "" && strings.TrimSpace(hint) == "" {
				t.Error("命中时必须给安全替代（规范给了写法，不该让人自己想办法）")
			}
		})
	}
}

// TestBannedWordingNegation 否定语境**不算违规**：`no bare feet` 正是规范要的写法，
// 报了就是误伤（误拦比漏拦更糟 —— 会逼着人把正确的写法删掉）。
func TestBannedWordingNegation(t *testing.T) {
	for _, s := range []string{
		"no bare feet",
		"not barefoot",
		"禁止赤足",
		"不得露乳",
		"avoid low-cut collars",
	} {
		if w, _ := bannedWordingIn(s); w != "" {
			t.Errorf("否定语境 %q 被误判成违规（命中 %q）", s, w)
		}
	}
}

// ── §4.1 场景命名四字以上 ──────────────────────────────────────────────────
func TestSceneNameLength(t *testing.T) {
	if PlanMinSceneNameRunes != 4 {
		t.Fatalf("规范口径是四字以上，实际 %d", PlanMinSceneNameRunes)
	}
	p := &Plan{
		Scenes: []Scene{{ID: "scene_01", Name: "城门", Description: "x", ImagePrompt: "x"}},
	}
	var hit bool
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "场景名短于") {
			hit = true
		}
	}
	if !hit {
		t.Error("「城门」这种单一名词该报（规范：严禁单一名词）")
	}
	// 真实数据那种写法不该报
	p2 := &Plan{Scenes: []Scene{{ID: "scene_01", Name: "临水城·城门·雨夜", Description: "x", ImagePrompt: "x"}}}
	for _, is := range ValidatePlan(p2) {
		if strings.Contains(is.Msg, "场景名短于") {
			t.Errorf("带修饰词的命名不该报：%s", is.Msg)
		}
	}
}

// ── §6.9 三人同框要写前后关系 ──────────────────────────────────────────────
func TestCrowdOrderMarkers(t *testing.T) {
	p := &Plan{
		Characters: []Character{{ID: "c1", Name: "甲", ImagePrompt: "x"}, {ID: "c2", Name: "乙", ImagePrompt: "x"}, {ID: "c3", Name: "丙", ImagePrompt: "x"}},
		Shots: []Shot{{
			ID: 1, Scene: "s", ShotSize: "中景", Duration: 6, Mode: "ref2va",
			Characters: []string{"c1", "c2", "c3"},
			Action:     "三个人一起走过来", H3Prompt: "detailed_description:\n[Shot 1] three men walk in together.",
		}},
	}
	var hit bool
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "三人以上同框") {
			hit = true
		}
	}
	if !hit {
		t.Error("三人同框没写前后关系该报")
	}
	// 写明关系（中英文任一写法）就不该报
	p.Shots[0].Action = "甲在前景，乙被甲挡住半步，丙在画左后方"
	hit = false
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "三人以上同框") {
			hit = true
		}
	}
	if hit {
		t.Error("写了前后关系不该报")
	}
}

// TestShotAngleZHCarriesThrough 角度词是导入字段，不能只停在明细层。
func TestShotAngleZHCarriesThrough(t *testing.T) {
	var sh Shot
	sh.AngleZH = "低角度仰拍"
	if !AngleWordOK(sh.AngleZH) {
		t.Error("组合写法要能命中")
	}
	sh.AngleZH = "蚂蚁视角"
	if AngleWordOK(sh.AngleZH) {
		t.Error("词表外的角度词要报")
	}
}
