package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 锚表角色带括号注解、正文里又混着普通括号 —— 只有"名字（定位："这种小节才算角色。
const realAnchorSrc = `《名册之外，八万零一号》系列短视频 第01 集
全局风格:

【人物外形锚表 · 硬约束】以下每个角色的外形必须逐项照抄(脸型 / 眉眼 / 鼻唇 / 肤色 / 独特记号 / 发型三要素 / 首套服装),不得改写、不得美化:
人物外形锚表 ·《名册之外，八万零一号》

祁七（定位：主角。机器人／机甲形态——旧舰队义体维护单元，机型钢印 U-80001）

- 年龄：外观 18–20 岁（非人类机体，实际运行年限不详）
- 脸型：鹅蛋脸，下巴微尖不过分
- 独特记号：头顶金色黄铜机械光环状发饰（最强记忆点）

霜见（定位：第二主角／通信手。拾荒船团的读数员）

- 年龄：少年，17–19 岁
- 脸型：瘦长脸，下颌线清晰
- 独特记号：颈侧一道义体接缝纹

墨洄（定位：反派。核验官）

- 年龄：三十余岁
- 脸型：方脸，颧骨高

邵舷（定位：配角。名册官）

- 年龄：中年
- 脸型：圆脸

尉迟彻（定位：配角。值星长）

- 年龄：四十余
- 脸型：长脸

断桅（定位：配角。拾荒船团船长）

- 年龄：五十余
- 脸型：瘦削

【本集原文】
祁七在回收站醒来，环上的编号闪着。
（正文里出现的括号（比如这句）不该被当成角色小节。）
`

// 【2026-09-28 用户报】源文本的【人物外形锚表】列了 6 个角色，方案里只出了 1 个。
//
// 这条照**真实数据形态**写：锚表在小节标题里用「名字（定位：…）」、正文里也有普通括号，
// 解析必须只认前者。
func TestParseCastAnchorReadsTheAnchorTable(t *testing.T) {
	got := ParseCastAnchor(realAnchorSrc)
	var names []string
	for _, a := range got {
		names = append(names, a.Name)
	}
	want := []string{"祁七", "霜见", "墨洄", "邵舷", "尉迟彻", "断桅"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("锚表应解析出 %v，实际 %v", want, names)
	}
	// 描述块要带上"逐项照抄"的原文（后面当角色卡描述用）
	if !strings.Contains(got[0].Desc, "金色黄铜机械光环状发饰") {
		t.Errorf("角色描述要保留锚表原文，实际 %q", got[0].Desc)
	}
	// 正文里的普通括号不许被当成角色小节
	for _, n := range names {
		if strings.Contains(n, "正文") || strings.Contains(n, "比如") {
			t.Errorf("正文括号被误判成角色：%q", n)
		}
	}
	// 没有锚表字样时一律不认（防止把普通文本里的括号当角色表）
	if len(ParseCastAnchor("某某（定位：主角）\n随便一段")) != 0 {
		t.Error("源文本里没有【人物外形锚表】时不该解析出任何角色")
	}
}

// 方案里缺的角色要按锚表补齐；已有的（含仅差标点的写法）不许重复补。
func TestEnsureAnchorCastFillsMissingCharacters(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "char_qiqi", Name: "祁七", Description: "模型直出的祁七描述"}, // 模型已经出了
		{ID: "char_x", Name: "霜 见", Description: "模型直出的霜见描述"},   // 只差一个空格 —— 同一个人，不该补第二张
	}}
	added := EnsureAnchorCast(plan, realAnchorSrc)
	if len(added) != 4 {
		t.Fatalf("该补 4 个（墨洄/邵舷/尉迟彻/断桅），实际 %v", added)
	}
	if len(plan.Characters) != 6 {
		t.Fatalf("补完应有 6 张卡，实际 %d", len(plan.Characters))
	}
	for _, ch := range plan.Characters {
		if ch.ID == "" || ch.Name == "" || strings.TrimSpace(ch.Description) == "" {
			t.Errorf("补出来的卡必须有 id/名字/描述（描述取锚表原文）：%+v", ch)
		}
	}
	// 幂等：再对一次账不该再补
	if again := EnsureAnchorCast(plan, realAnchorSrc); len(again) != 0 {
		t.Errorf("对账必须幂等，第二次又补了 %v", again)
	}
	// id 必须稳定（跨集跨项目复用靠它）
	first := AnchorCardID("墨洄")
	if second := AnchorCardID("墨洄"); first != second || !strings.HasPrefix(first, "char_") {
		t.Errorf("锚表补出来的 id 必须稳定可复现：%q / %q", first, second)
	}
}
