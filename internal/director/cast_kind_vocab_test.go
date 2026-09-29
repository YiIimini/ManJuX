package director

import (
	"strings"
	"testing"
)

// ②人物道具会话必须**被告知 kind 的取值清单**（2026-09-29）。
//
// 【现场】schema 里原来写死了 `"kind":"human"`，模型于是把 6 个角色全填 human ——
// 包括设定写着「非人类机体，旧舰队义体维护单元，机型钢印 U-80001」的女主角，
// 她因此按人的身份锁出图（锁肤色/年龄感/毛孔红晕）⇒ 画成一个普通古装女子
// （用户报「机器人也按照人来生成？」）。
//
// 平台这一侧已经补了"强信号自动升级成 android"的安全网，但**模型侧也要能正确表达**：
// 它知道有 android 这一档、知道"有脸但不是人"该填 android，才是从源头对齐。
//
// 判据落在**实际下发的那两段文本**上（schema + 规则），不是"我改过文件"。
func TestCastSessionIsToldTheKindVocabulary(t *testing.T) {
	schema := sessionOutputSchema[SessionCast]
	if schema == "" {
		t.Fatal("②人物道具会话没有输出 schema")
	}
	for _, want := range []struct{ needle, why string }{
		{"android", "schema 必须给出 android 这一档（只写 human 等于告诉模型「所有角色都是人」）"},
		{"object", "schema 要给出 object（器物/机关/傀儡）"},
		{"incorporeal", "schema 要给出 incorporeal（无实体）"},
	} {
		if !strings.Contains(schema, want.needle) {
			t.Errorf("schema 缺少 %q —— %s", want.needle, want.why)
		}
	}

	// 规则段（go:embed 进来的 sysCastRules）要说清"有脸但不是人 ⇒ android"，
	// 否则模型还是会按"有脸=人"把它填成 human。
	rules := sysCastRules
	if rules == "" {
		t.Fatal("sysCastRules 是空的 —— 规则文件没被 embed 进来")
	}
	if !strings.Contains(rules, "android") {
		t.Error("角色卡规则段没提 android 这一档 —— 模型不知道有它可用")
	}
	if !strings.Contains(rules, "义体") && !strings.Contains(rules, "非人类机体") {
		t.Error("规则段没给「有脸但不是人」的判据（义体/非人类机体/机型钢印），模型只会按脸判成人")
	}
}
