package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 锚表 → **英文画图提示词**：年龄与头身比必须译出来。
//
// 【2026-09-28 用户报「中年人渲染出来跟个小年轻一样」】根因：上一版用锚表补齐角色卡时
// 只搬了中文 `description`，`image_prompt` 是空的 —— 而出图装配只吃英文提示词，
// 模型拿不到任何年龄依据，于是自由发挥把 58–66 岁的老拾荒人画成了小年轻。
//
// 夹具逐条照**真实锚表**写（data/小说/名册之外，八万零一号/设定集/人物外形锚表.md）。
func TestAnchorImagePromptCarriesAgeAndProportions(t *testing.T) {
	cases := []struct {
		name    string
		age     string
		ratio   string
		wantAge string
		wantHd  string
	}{
		{"断桅", "老年，58–66 岁", "7.5 头身", "aged 58 to 66", "seven and a half heads tall"},
		{"墨洄", "青年，25–30 岁", "8 头身", "aged 25 to 30", "eight heads tall"},
		{"核验官", "中年偏青，35 岁上下", "7.5 头身", "aged about 35", "seven and a half heads tall"},
		{"尉迟彻", "青年，30–36 岁", "8 头身", "aged 30 to 36", "eight heads tall"},
	}
	for _, c := range cases {
		got := anchorImagePrompt(project.SettingChar{
			Name: c.name,
			Fields: []project.SettingField{
				{Key: "年龄", Value: c.age},
				{Key: "比例锚_机器", Value: c.ratio},
			},
		})
		if !strings.Contains(got, c.wantAge) {
			t.Errorf("[%s] 英文提示词缺年龄「%s」—— 出图会按模型自己的审美发挥:\n%s", c.name, c.wantAge, got)
		}
		if !strings.Contains(got, c.wantHd) {
			t.Errorf("[%s] 英文提示词缺头身比「%s」:\n%s", c.name, c.wantHd, got)
		}
		if project.CountCJK(got) != 0 {
			t.Errorf("[%s] 英文画图提示词里不许有汉字:\n%s", c.name, got)
		}
	}

	// 没有数字的年龄档位也要**给出明确的年龄尺度** —— 否则模型按"年轻好看"回落，
	// 那正是用户报的那个毛病（"老年"只写中文，等于没写）。
	for _, c := range []struct{ age, want string }{
		{"老年", "past sixty"},
		{"中年偏青", "middle-aged"},
		{"青年", "twenties"},
		{"少年", "teenager"},
	} {
		got := anchorAgePhrase(c.age)
		if !strings.Contains(got, c.want) {
			t.Errorf("年龄档「%s」应译出明确尺度（含 %q），实际 %q", c.age, c.want, got)
		}
	}
	if anchorAgePhrase("") != "" {
		t.Error("空年龄应返回空串")
	}
}

// 补齐出来的卡**必须带英文画图提示词**（这是"出图有依据"的最低要求）。
func TestEnsureAnchorCastFillsEnglishImagePrompt(t *testing.T) {
	plan := &project.Plan{}
	added := EnsureAnchorCast(plan, realAnchorSrc)
	if len(added) == 0 {
		t.Fatal("夹具应补出若干角色")
	}
	for _, ch := range plan.Characters {
		if strings.TrimSpace(ch.ImagePrompt) == "" {
			t.Errorf("%s 补出来的卡没有英文画图提示词 —— 出图时模型没有外貌依据", ch.Name)
		}
		if project.CountCJK(ch.ImagePrompt) != 0 {
			t.Errorf("%s 的英文提示词里混进了汉字:%q", ch.Name, ch.ImagePrompt)
		}
	}
}
