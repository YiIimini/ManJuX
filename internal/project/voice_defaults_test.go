package project

import (
	"strings"
	"testing"
)

// realAnchorCast 真机《名册之外，八万零一号》锚表里 14 条角色的**名字 + 锚表原文的性别行**
// （照 `internal/server/testdata/char_anchor_real.md` 逐条抄；`未定` 那条也照抄 ——
// 毕申的锚表原文是「性别：未定（正文刻意不给代词）」，那是**刻意**不写代词的，不许当成女）。
//
// 这几张卡就是 `pipeline.EnsureAnchorCast` 补出来的样子：只写了 description（锚表原文）
// 与 image_prompt（英文兜底串），**voice 是空的** —— 2026-10-07 真机复核查出的撞声通路。
var realAnchorCast = []struct{ name, genderLine string }{
	{"祁七", "女"},
	{"霜见", "女"},
	{"墨洄", "女"},
	{"邵舷", "男"},
	{"尉迟彻", "男"},
	{"断桅", "女"},
	{"苔生", "女"},
	{"拾叁", "女"},
	{"回波", "女（与祁七同型机体）"},
	{"关棠", "女"},
	{"剥线刀", "男"},
	{"毕申", "未定（正文刻意不给代词）"},
	{"白手套", "女"},
	{"议长", "女"},
}

func anchorCard(name, genderLine string) Character {
	return Character{
		ID:          "char_" + name,
		Name:        name,
		Kind:        "human",
		Description: "- 性别：" + genderLine + "\n- 年龄：中年，42–50 岁\n- 身形：结实，个子不高，肩宽",
		ImagePrompt: "[ANCHOR-STUB] a Chinese period-film character, aged 42 to 50",
	}
}

// TestFillMissingVoicesNoTwoShareOneVoice 真机 14 条角色卡：**一张不落有声线，且两两不同**。
//
// 【为什么这条是门】此前这些卡 voice 全空，渲染侧 `defaultVoiceFor` 一律返回同一个常数
// `genericVoiceDefault` ⇒ 苔生/拾叁/回波/关棠/剥线刀… 在成片里是**同一个嗓子**
// （用户报的"好几个人一个声音"）。判据写成结构性的：补完之后池内不许有两张卡拿到同一条。
func TestFillMissingVoicesNoTwoShareOneVoice(t *testing.T) {
	plan := &Plan{}
	for _, r := range realAnchorCast {
		plan.Characters = append(plan.Characters, anchorCard(r.name, r.genderLine))
	}
	n, names := FillMissingVoices(plan)
	if n != len(realAnchorCast) {
		t.Fatalf("14 张缺声线的卡都该补上，实得 %d（%s）", n, strings.Join(names, "、"))
	}
	seen := map[string]string{}
	for _, c := range plan.Characters {
		v := strings.TrimSpace(c.Voice)
		if v == "" {
			t.Errorf("角色「%s」还是没有声线 —— 渲染时会掉进平台常数默认，与别人撞声", c.Name)
			continue
		}
		if other, dup := seen[v]; dup {
			t.Errorf("「%s」与「%s」拿到同一条声线（%q）—— 成片里是同一个嗓子", c.Name, other, v)
		}
		seen[v] = c.Name
	}
	if len(seen) != len(realAnchorCast) {
		t.Errorf("14 张卡应得到 14 条不同声线，实得 %d 条", len(seen))
	}
	// 性别要跟着锚表走：毕申那条写「未定」⇒ 中性池，不许当成女声池里的某一条。
	if got := plan.Characters[11].Voice; !containsStr(platformVoicePaletteNeutral, got) {
		t.Errorf("锚表写「性别：未定」的毕申该落中性池，实得 %q", got)
	}
	if got := plan.Characters[3].Voice; !containsStr(platformVoicePaletteMale, got) {
		t.Errorf("锚表写「性别：男」的邵舷该落男声池，实得 %q", got)
	}
}

// TestFillMissingVoicesIsIdempotentAndStable 幂等 + 稳定 + 不覆盖作者给的声线。
//
//	· 第二次跑：0 张（幂等，不会每读一次方案就换一次嗓子）；
//	· 同一套卡片**另建一份 plan**：值必须逐条相同（跨集稳定 —— 观众靠这个认人）；
//	· 作者/技能侧写了 voice 的卡：一个字不动，而且派生出来的卡**不会撞上**它。
func TestFillMissingVoicesIsIdempotentAndStable(t *testing.T) {
	build := func() *Plan {
		p := &Plan{}
		for _, r := range realAnchorCast {
			p.Characters = append(p.Characters, anchorCard(r.name, r.genderLine))
		}
		// 技能侧给了声线的卡（真机 ②会话 schema 里有 voice 字段，给了就必须听它的）。
		p.Characters = append(p.Characters, Character{
			ID: "char_kept", Name: "技能侧给了声的角", Kind: "human",
			Description: "- 性别：女", Voice: "the author's own voice, unchanged",
		})
		return p
	}
	p1 := build()
	if _, _ = FillMissingVoices(p1); true {
	}
	if n2, _ := FillMissingVoices(p1); n2 != 0 {
		t.Errorf("幂等：第二次不该再补，实得 %d 张", n2)
	}
	p2 := build()
	FillMissingVoices(p2)
	for i := range p1.Characters {
		if p1.Characters[i].Voice != p2.Characters[i].Voice {
			t.Errorf("跨集不稳定：「%s」两份 plan 拿到不同声线（%q / %q）",
				p1.Characters[i].Name, p1.Characters[i].Voice, p2.Characters[i].Voice)
		}
	}
	if got := p1.Characters[len(p1.Characters)-1].Voice; got != "the author's own voice, unchanged" {
		t.Errorf("作者给的声线不许被改写，实得 %q", got)
	}
	for _, c := range p1.Characters[:len(realAnchorCast)] {
		if c.Voice == "the author's own voice, unchanged" {
			t.Errorf("派生声线撞上了作者指定的那条（%s）", c.Name)
		}
	}
}

// TestDerivedVoiceQVersionSharesBaseVoice Q 版卡与本体同一个嗓子。
//
// 内心戏里说话的是**本人**（Q 版只是"卡通化的同一个人"）：查表键要归一掉 `Q版` 前缀与括注，
// 否则同一句心里话在本体镜与 Q 版镜里是两把嗓子。
func TestDerivedVoiceQVersionSharesBaseVoice(t *testing.T) {
	base := DerivedVoice("祁七", "female")
	for _, n := range []string{"Q版祁七", "Q 版祁七", "祁七（女）"} {
		if got := DerivedVoice(n, "female"); got != base {
			t.Errorf("「%s」该与本体同一个嗓子，实得 %q，期望 %q", n, got, base)
		}
	}
	if base == "" {
		t.Fatal("不该为空")
	}
	if DerivedVoice("苔生", "female") == base {
		t.Error("不同角色不该拿到同一条声线（散列位置撞了就该由 PickVoice 顺延）")
	}
	// 空名字不许编：宁可不给，也不要给"无名"配一条嗓子
	if got := DerivedVoice("  ", "female"); got != "" {
		t.Errorf("空名字该返回空串，实得 %q", got)
	}
}

// TestVoiceGapsPointsAtCardsWithoutVoice 绕过读取边界的方案要能被点名（渲染前播报用）。
func TestVoiceGapsPointsAtCardsWithoutVoice(t *testing.T) {
	p := &Plan{Characters: []Character{
		{Name: "苔生", Description: "- 性别：女"},
		{Name: "祁七", Description: "- 性别：女", Voice: "given"},
		{Name: "", Description: ""},
	}}
	gaps := VoiceGaps(p)
	if len(gaps) != 1 || gaps[0] != "苔生" {
		t.Errorf("该点名「苔生」，实得 %v", gaps)
	}
	if VoiceSummary(gaps) == "" {
		t.Error("点名要有给日志用的话")
	}
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
