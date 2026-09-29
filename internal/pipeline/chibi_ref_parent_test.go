package pipeline

import "testing"

// Q 版找主卡：**锚表的 `Q版本体` 字段优先**，名字档名只是兜底
// （技能侧 2026-09-29 补好字段之后，平台侧要接上 —— 否则 Q 版一旦改名就找不到主卡，
// 悄悄退回文生图、另画一张脸）。
func TestChibiRefKeysPreferAnchorParentField(t *testing.T) {
	// ① 名字里没有档名（真实形态：「碑生·Q版（肩上巴掌大的自己）」的基名是「碑生」，
	// 但若卡名写成别的，比如「肩上巴掌大的自己」，靠名字就找不到主卡）——
	// 有字段时必须仍能找到本体。
	id, name := sheetRefKeys("chibi_char_bisheng", "肩上巴掌大的自己",
		"- Q版形象：有\n- Q版本体：碑生（丙十七）\n- Q版比例锚_机器：2.5 头身")
	if id != "" || name != "碑生" {
		t.Fatalf("有 Q版本体 时没按字段找主卡：id=%q name=%q（应 name=碑生）", id, name)
	}
	// ② 字段里写的是带括注的本体名 → 归一到基名（与资产表的家族键同一口径）
	_, name2 := sheetRefKeys("chibi_char_x", "某人的Q版", "- Q版本体：钟离（本集仅作冷开场锚点）")
	if name2 != "钟离" {
		t.Errorf("本体名没归一：%q（应 钟离）", name2)
	}
	// ③ 没有字段时退回名字档名（老包不许因此失能）
	_, name3 := sheetRefKeys("chibi_char_qiqi", "祁七·Q版", "碑生的 Q 版小人，2.5 头身")
	if name3 != "祁七" {
		t.Errorf("没有 Q版本体 时应按名字档名兜底：%q（应 祁七）", name3)
	}
	// ④ 非 Q 版卡走家族键，不受影响
	id4, _ := sheetRefKeys("char_01v4", "翻篇·收容所", "")
	if id4 == "" {
		t.Error("非 Q 版的家族键被改动")
	}
}
