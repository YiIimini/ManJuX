package pipeline

import "testing"

// 资产生成日志的括号必须与资产**实际类型**一致(2026-09-26 真机实测暴露)。
//
// 事故形态:日志里出现「✔ 已生成:场景「临水城」(人)」。根因是场景没有实体类型,
// 而这条播报**无条件**挂了 EntityKindLabel(零值 → default → 「人」)。
// 用户看日志判断"平台到底在画什么",这类假话他一天里已经纠过两回
// (「资产库里的 Krea-2 字样写死了」、「Krea-2 出图这么多问题」),所以钉死在单元测试上:
// 角色挂类型、场景/器物类不挂。
func TestEntityKindSuffix(t *testing.T) {
	// 角色:必须挂实体类型,且随类型变
	cases := []struct {
		ek   EntityKind
		want string
	}{
		{KindHuman, "(人)"},
		{KindObject, "(物)"},
		{KindSpiritPet, "(灵宠)"},
		{KindBeast, "(妖兽)"},
		{KindIncorporeal, "(无实体)"},
		{KindChibi, "(Q版)"},
	}
	for _, c := range cases {
		if got := entityKindSuffix("characters", c.ek); got != c.want {
			t.Errorf("角色 %s 应播报 %q,实际 %q", c.ek, c.want, got)
		}
	}
	// 零值(未判定类型)按「人」走 —— 这个默认值本身是设计的一部分,不是这里要改的东西
	if got := entityKindSuffix("characters", ""); got != "(人)" {
		t.Errorf("角色未判定类型时应回落 (人),实际 %q", got)
	}
	// 场景**不许**挂实体类型:这是本次修的那条假话
	for _, kind := range []string{"scenes", "props", "others"} {
		if got := entityKindSuffix(kind, ""); got != "" {
			t.Errorf("%s 不该播报实体类型(它就是没有类型),实际 %q —— 会变成「场景「临水城」(人)」这种假话", kind, got)
		}
	}
}
