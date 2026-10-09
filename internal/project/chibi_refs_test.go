package project

import (
	"strings"
	"testing"
)

// realChibiCards 真机第01集《被开除一年后，我回公司送水》的**角色卡表原文**
// （2026-10-09 从盘上 plan.json 逐字抄：id 与显示名都照抄）。
//
// 夹具必须照真实形态写：这张表里 Q 版的 id 是 `char_jianning_q`（库卡 id），
// 而显示名是「简宁·Q版」——两条**互不相等**，正是本次事故的现场。
func realChibiCards() []CardRef {
	return []CardRef{
		{ID: "char_jianning", Name: "简宁"},
		{ID: "char_jianning_q", Name: "简宁·Q版"},
		{ID: "char_houwanlin", Name: "侯万林"},
		{ID: "char_xiaosu", Name: "小苏"},
		{ID: "char_zhengdayong", Name: "郑大勇"},
		{ID: "char_zhengdayong_q", Name: "郑大勇·Q版"},
	}
}

// 三套 id 空间都要认：库卡 id、技能侧前缀/后缀、显示名（真机写的就是显示名）。
func TestResolveChibiRefThreeSpaces(t *testing.T) {
	cards := realChibiCards()
	cases := []struct {
		ref  string
		want string
	}{
		{"简宁·Q版", "char_jianning_q"},           // 真机第01集 #8/#14/#15 写的原样
		{"简宁Q版", "char_jianning_q"},            // 少一个间隔号
		{"简宁 · Q版", "char_jianning_q"},         // 平台派生时的写法（名字里带空格）
		{"Q版简宁", "char_jianning_q"},            // 前缀写法
		{"char_jianning_q", "char_jianning_q"}, // 卡片 id 本身：一个字不动
		{"char:char_jianning_q", "char_jianning_q"},
		{"chibi_char_jianning", "char_jianning_q"},     // 技能侧前缀（平台顶替映射认的那套）
		{"char_jianning_chibi", "char_jianning_q"},     // 平台自己派生的后缀写法
		{"char_jianning_q（蹲在简宁左肩）", "char_jianning_q"}, // 名字带括注（技能侧常见）
		{"郑大勇·Q版", "char_zhengdayong_q"},               // 另一个角色不能串
	}
	for _, c := range cases {
		got, _, ok := ResolveChibiRef(c.ref, cards)
		if !ok {
			t.Errorf("ResolveChibiRef(%q) 没认出来 —— 这一镜的 Q 版参考图会绑不上", c.ref)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveChibiRef(%q) = %q，期望 %q", c.ref, got, c.want)
		}
	}
}

// 认不出来的一律**不改写**（不肯猜一张卡顶上去：猜错就是把别人的 Q 版画进这一镜）。
func TestResolveChibiRefRefusesToGuess(t *testing.T) {
	cards := realChibiCards()
	for _, ref := range []string{"", "  ", "简宁", "小灯的Q版", "char_nobody_q", "简宁·Q版2"} {
		if id, _, ok := ResolveChibiRef(ref, cards); ok {
			t.Errorf("ResolveChibiRef(%q) 猜出了 %q —— 认不出就该如实留给门去报", ref, id)
		}
	}
	// 本人卡（`简宁`）绝不能被当成 Q 版（否则内心镜反而附了本人照）。
	if id, _, ok := ResolveChibiRef("简宁", cards); ok {
		t.Errorf("「简宁」是本人卡，不该被解析成 Q 版（解析成了 %q）", id)
	}
}

// 装配点归一：显示名 → 真实卡片 id，且**幂等**；认不出来的如实记账。
func TestNormalizeChibiIDsOnRealPlan(t *testing.T) {
	p := &Plan{
		Characters: []Character{
			{ID: "char_jianning", Name: "简宁"},
			{ID: "char_jianning_q", Name: "简宁·Q版"},
		},
		Shots: []Shot{
			{ID: 8, VoKind: VoKindInner, ChibiIDs: []string{"简宁·Q版"}},
			{ID: 14, VoKind: VoKindInner, ChibiIDs: []string{"简宁·Q版"}},
			{ID: 15, VoKind: VoKindInner, ChibiIDs: []string{"chibi_char_jianning"}},
			{ID: 16, VoKind: VoKindInner, ChibiIDs: []string{"简宁·Q版（蹲在左肩）"}},
			{ID: 17, VoKind: VoKindInner, ChibiIDs: []string{"小灯Q版"}}, // 没有这张卡
			{ID: 18, VoKind: VoKindInner, ChibiIDs: []string{"char_jianning_q"}},
		},
	}
	fixed, unknown := p.NormalizeChibiIDs()
	if fixed != 4 {
		t.Errorf("该归一 4 处（#8/#14/#15/#16），实际 %d", fixed)
	}
	for _, sh := range p.Shots[:4] {
		if len(sh.ChibiIDs) != 1 || sh.ChibiIDs[0] != "char_jianning_q" {
			t.Errorf("镜 #%d 的 chibi_ids 该变成 [char_jianning_q]，实际 %v", sh.ID, sh.ChibiIDs)
		}
	}
	if len(unknown) != 1 || unknown[0] != "小灯Q版" {
		t.Errorf("认不出来的该如实点名 [小灯Q版]，实际 %v", unknown)
	}
	if p.ChibiRefFixed != 4 {
		t.Errorf("方案上的记账 ChibiRefFixed = %d，期望 4（要播报这个数）", p.ChibiRefFixed)
	}
	// 幂等：第二次一处也不改（否则每读一次方案就重报一遍）。
	fixed2, unknown2 := p.NormalizeChibiIDs()
	if fixed2 != 0 || len(unknown2) != 1 {
		t.Errorf("归一不幂等：第二次仍报 改=%d 认不出=%v", fixed2, unknown2)
	}
	if p.ChibiRefFixed != 4 {
		t.Errorf("幂等时不该继续累加记账，实际 %d", p.ChibiRefFixed)
	}
}

// 门与装配点必须**对同一件事有一致的结论**：归一是为了过门，不是为了让门闭嘴。
//
// 判据：① 未归一时门必须 fail（这就是用户看到的「chibi_ids 引用悬空」）；
// ② 归一之后门不再 fail；③ 真认不出来的**归一后仍然 fail**。
func TestChibiRefGateAgreesWithHeal(t *testing.T) {
	base := func() *Plan {
		return &Plan{
			Title: "T",
			Characters: []Character{
				{ID: "char_jianning", Name: "简宁"},
				{ID: "char_jianning_q", Name: "简宁·Q版"},
			},
			Scenes: []Scene{{ID: "scene_a", Name: "走廊"}},
			Shots: []Shot{{
				ID: 1, Scene: "scene_a", Characters: []string{"char_jianning"},
				VoKind: VoKindInner, ChibiIDs: []string{"简宁·Q版"},
				H3Prompt: "[Shot 1] A girl stands at a window.",
			}},
		}
	}
	hasDangling := func(p *Plan) bool {
		for _, it := range ValidatePlan(p) {
			// 判据读**门报出来的正文**（`plan.shots` 那一条里点名了 `chibi_ids→…`），
			// 不是读 Where —— 读错字段会让整个断言永远不成立（摆设守卫）。
			if strings.Contains(it.Msg, "chibi_ids→") && strings.Contains(it.Msg, "悬空") {
				return true
			}
		}
		return false
	}
	// ① 显示名**能对号**：门不该报悬空（渲染侧走同一判据 resolve，图绑得上）。
	// 口径：门管的是"这条引用能不能绑到一张真卡"，不是"写法是不是 id"。
	before := base()
	ids := before.Shots[0].ChibiIDs
	if hasDangling(before) {
		t.Errorf("显示名 %v 能解析到真卡，不该被判悬空（装配点与渲染侧都认得它）", ids)
	}
	// ② 归一之后：门同样安静（而且这次 chibi_ids 是真正的卡片 id）。
	after := base()
	if fixed, _ := after.NormalizeChibiIDs(); fixed != 1 {
		t.Fatalf("该归一 1 处，实际 %d", fixed)
	}
	if after.Shots[0].ChibiIDs[0] != "char_jianning_q" {
		t.Errorf("归一后该是真实卡片 id，实际 %v", after.Shots[0].ChibiIDs)
	}
	if hasDangling(after) {
		t.Error("归一之后门还在报悬空 —— 装配点与门的判据没对上")
	}
	// ③ 真认不出来的：归一后仍要 fail（不许"归一一下就当没事了"）。
	bad := base()
	bad.Shots[0].ChibiIDs = []string{"小灯Q版"}
	if _, unk := bad.NormalizeChibiIDs(); len(unk) != 1 {
		t.Fatalf("认不出来的该点名，实际 %v", unk)
	}
	if !hasDangling(bad) {
		t.Error("认不出来的 Q 版引用必须仍然 fail —— 否则渲染时那张图被静默丢掉，谁也不知道")
	}
}
