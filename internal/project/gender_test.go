package project

import (
	"strings"
	"testing"
)

// 性别（2026-09-29 用户：「钟离渲染成男的？」）：
// 卡面/锚表没写性别时，出图模型自己猜 —— 名字偏男性先验，守钟女就被画成了男的。
//
// 判据：显式字段 `性别：X` 优先；其次**强**性别词；两者都没有 ⇒ 报"没写"（不许猜）。
func TestGenderOfAndIssues(t *testing.T) {
	// ① 显式字段（技能侧 2026-09-29 起补这一行）
	if g, ok := GenderOf("- 姓名：钟离\n- 性别：女\n- 身形：清瘦挺直", ""); !ok || g != "女" {
		t.Errorf("显式字段没读到：%q,%v", g, ok)
	}
	// ② 强性别词（真机卡面写法）
	if g, ok := GenderOf("- 年龄：三十出头\n- 身形：中等身量结实，圆润苹果脸，三十出头的**妇人**", ""); !ok || g != "女" {
		t.Errorf("强性别词没读到：%q,%v", g, ok)
	}
	if g, ok := GenderOf("五十余岁的老汉，背微驼", ""); !ok || g != "男" {
		t.Errorf("老汉没读成男：%q,%v", g, ok)
	}
	// ③ 弱词**不许**拿来猜（「少年」在不同文本里男女都用过）
	if g, ok := GenderOf("清瘦挺直，约八头身，指节细", ""); ok {
		t.Errorf("没有性别线索时不该猜出来：%q", g)
	}
	// ④ 门：没写的报出来，写了的（或用强词写清的）不报
	plan := &Plan{Characters: []Character{
		{ID: "char_ff31f70f", Name: "钟离", Kind: "human", Description: "- 年龄：外表 17–18\n- 身形：清瘦挺直，约八头身"},
		{ID: "char_eb99044b", Name: "阿枝", Kind: "human", Description: "三十出头的妇人，中等身量结实"},
		{ID: "prop_x", Name: "碑刀", Kind: "object", Description: "朱砂红刃，木柄缠布"},
	}}
	issues := GenderIssues(plan)
	if len(issues) != 1 || !strings.Contains(issues[0], "钟离") {
		t.Fatalf("应当只报「钟离」没写性别，实得 %v", issues)
	}
	if strings.Contains(issues[0], "碑刀") {
		t.Error("器物没有性别这回事，不该报")
	}
}
