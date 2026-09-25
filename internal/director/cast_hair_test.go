package director

import (
	"strings"
	"testing"
)

// TestCastRulesRequireUnambiguousHair 角色卡写法闸门(2026-09-25)。
//
// 定妆照会自己"补"简报里没写的东西:卡片只写"短低马尾",定妆照就给额前加了几缕碎发;
// 而参考图是全片身份的权威,H3 于是把那几缕复现并放大成"头发飘逸"。
// 所以**写卡侧**必须把三件事写死:束法、长度、额前状态 —— 缺一个,下游就会自己填。
func TestCastRulesRequireUnambiguousHair(t *testing.T) {
	for _, want := range []string{
		"HAIR MUST BE UNAMBIGUOUS",
		"the **TIE**",
		"the **LENGTH**",
		"the **FOREHEAD STATE**",
		"an unstated forehead",
		"combed back, forehead clear, no strands over the face",
	} {
		if !strings.Contains(SystemPrompt, want) {
			t.Errorf("选角/角色卡规则缺少 %q(下游会把没写的部分自己补上)", want)
		}
	}
	// 同一条事实必须同时进 description 字段,否则"卡面"与"镜头锚定"会各说一套
	if !strings.Contains(SystemPrompt, `put the same three facts in the character's "description" field`) &&
		!strings.Contains(SystemPrompt, "Put the same three facts in the character's \"description\" field") {
		t.Error("必须要求把同样三条写进 description 字段")
	}
}
