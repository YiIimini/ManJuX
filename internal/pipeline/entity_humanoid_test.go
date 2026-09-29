package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 人形机械必须是**独立一档**（2026-09-29 用户：「单独弄，避免又渲染错误」）。
//
// 【为什么】第 01 集的白瓷装甲女主角（设定原文：「非人类机体，旧舰队义体维护单元，
// 机型钢印 U-80001」）此前只有 human 一档可用 ⇒ 按人的身份锁渲染（锁肤色/年龄感/毛孔红晕）
// ⇒ 出图就是一个普通古装女子，用户报「机器人也按照人来生成？」。
//
// 【三条判据，缺一条就会再错一次】
//
//	① **判定**：人形机体要被认出来（哪怕 LLM 把 kind 填成 human），而真人（机械师/义肢）
//	   绝不能被卷进来；
//	② **取景**：走**人形**取景（半身正脸）—— 绝不能掉进器物那一档（整件居中 + 转台自转）；
//	③ **锁点**：身份锁与判据锁在**机械结构**（装甲分缝/关节/发光标记/材质/机身刻印）上，
//	   并且要有"别退化成普通人"的反错误形态条款。
func TestHumanoidMachineIsItsOwnKind(t *testing.T) {
	// ① 判定：真机那一集的原话（祁七），LLM 当时填的是 human
	qiqi := project.Character{
		ID: "char_qiqi", Name: "祁七", Kind: "human",
		Description: "外观二十岁（非人类机体，旧舰队义体维护单元，机型钢印 U-80001）。身形：纤细，站姿笔直；" +
			"瓷器白的装甲板覆盖胸腹，颈胸有三角与菱形发光标记。",
	}
	if got := ResolveEntityKind(qiqi); got != KindAndroid {
		t.Fatalf("人形机体被判成了 %q（%s）—— 按人渲染就会出现「机器人按人画」", got, EntityKindLabel(got))
	}
	// 没有 kind 字段（存量项目）同样要判出来
	qiqi.Kind = ""
	if got := InferEntityKind(qiqi.Name, qiqi.Description, ""); got != KindAndroid {
		t.Fatalf("存量卡推断错了：%q", got)
	}

	// 真人**不许**被卷进来：机械师是职业、义肢是假肢
	for _, human := range []project.Character{
		{Name: "老周", Kind: "human", Description: "舰上的机械师，四十来岁，方脸，右眉一道旧疤，常年一身油渍工装。"},
		{Name: "阿岩", Kind: "human", Description: "退役老兵，左腿是义肢，走路右肩低半寸；短发，国字脸。"},
		{Name: "尉迟彻", Kind: "human", Description: "深灰值勤装甲，方中带圆脸，剑眉，细长单眼皮，左手小指缺一节。"},
	} {
		if got := ResolveEntityKind(human); got != KindHuman {
			t.Errorf("真人「%s」被误判成 %s —— 这会把普通人按机械渲染", human.Name, EntityKindLabel(got))
		}
	}

	// ② 取景：走人形那一套（半身正脸 + 原地转身），不是器物那一套
	if PortraitLeadFraming(KindAndroid, 768, 1344) == "" {
		t.Error("人形机械没有前导取景句 —— 会退化成裸提示词（此前定妆照取景失效就是这个原因）")
	}
	if PortraitFramingClause(KindAndroid, 768, 1344) == "" {
		t.Error("人形机械缺少取景纪律（上半身正脸）")
	}
	if got := KindAndroid.TurnVerb(); got != "原地转身" {
		t.Errorf("人形机械的多视角动作应是原地转身（它不是转台产品），实得 %q", got)
	}
	for _, bad := range []string{"turn table", "turntable", "整件居中"} {
		p := EntityProfileOf(KindAndroid)
		if strings.Contains(p.SheetFraming+p.TurnSummary, bad) {
			t.Errorf("人形机械不该用器物那套「%s」取景", bad)
		}
	}

	// ③ 锁点：机械结构在场、反错误形态在场
	prof := EntityProfileOf(KindAndroid)
	for _, want := range []struct{ hay, needle, why string }{
		{prof.SheetIdentity, "armour panel", "定妆照身份锁要锁装甲板件（不是锁肤色）"},
		{prof.SheetIdentity, "SYNTHETIC FACEPLATE", "面甲要写清是合成面甲（形状照抄、不是人类皮肤）"},
		{prof.AnchorLock, "装甲分缝", "镜头身份锁要锁机械结构"},
		{prof.AntiForm, "KEEP IT A MACHINE", "必须有一条反错误形态条款（别退化成普通人）"},
		{prof.JudgePoints, "退化成普通人", "人审判据要专门查「有没有被画成普通人类」"},
		{string(prof.SheetMarker), "ANDROID", "类型标记要能区分「按人渲染的旧定妆照」（界面据此提示重出）"},
	} {
		if !strings.Contains(want.hay, want.needle) {
			t.Errorf("缺少 %q —— %s", want.needle, want.why)
		}
	}

	// ④ 与"人"的关系：动作/取景那套是**派生**来的（不许各写一份），但标记必须不同
	human := EntityProfileOf(KindHuman)
	if prof.SheetMarker == human.SheetMarker {
		t.Error("人形机械与人的定妆照标记相同 —— 「这张是按人渲染的旧图」就查不出来了")
	}
	if prof.TurnSubject != human.TurnSubject || prof.TurnSummary != human.TurnSummary {
		t.Error("人形机械的转身方法应当派生自人那一套（同一动作、同一取景），改了就要同步改，别各写一份")
	}

	// ⑤ 类型清单与标签（UI 选择器与日志都读它）
	found := false
	for _, k := range EntityKinds {
		if k == KindAndroid {
			found = true
		}
	}
	if !found {
		t.Error("EntityKinds 里没有 android —— UI 里选不到这个类型（后端 /api/meta 按它下发）")
	}
	if EntityKindLabel(KindAndroid) != "人形机械" || KindHint(KindAndroid) == "" {
		t.Error("人形机械缺中文标签/说明")
	}
	// 用户手改类型这条路要走得通（NormalizeEntityKind 认中英两种写法）
	for _, s := range []string{"android", "人形机械", "机器人", "仿生人", "义体"} {
		if NormalizeEntityKind(s) != KindAndroid {
			t.Errorf("NormalizeEntityKind(%q) 没认出来 —— 用户在界面上选/后端存的这种值会掉回推断", s)
		}
	}
}
