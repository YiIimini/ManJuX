package director

import (
	"strings"
	"testing"
)

// prop_rules_test.go 关键道具必须被提取（用户 2026-09-28：「关键物品生成道具啊，前后镜都漂移了」）。
//
// 病根实测：那一集的 plan **连 `props` 字段都没有**，25 镜提到刀/烙铁/名册、`prop_ids` 全空 ——
// 于是每镜都由模型自己发明那把碑刀，形状缠柄一镜一个样 = 观众看到的漂移。
// 根因是**平台的直出 schema 里从来没有 props**（本包 grep 不到）⇒ LLM 不可能提取它。
//
// 这条测试钉三件事：schema 里有 props、分镜能引用 prop_ids、规则段落把"什么该收成道具"写清楚。
func TestSchemasCarryProps(t *testing.T) {
	// ① 单轮直出与计划轮**两个** schema 都要有 props（漏一个，走那条路的方案还是没有道具）
	if n := strings.Count(SystemPrompt, `"props": [`); n != 1 {
		t.Errorf("单轮直出 schema 里应有 props 数组，实际出现 %d 次", n)
	}
	if n := strings.Count(planSystemPrompt, `"props": [`); n != 1 {
		t.Errorf("计划轮 schema 里应有 props 数组，实际出现 %d 次", n)
	}
	// ② 字段名要与契约/装配那侧对齐（id/name/en/description/image_prompt）
	for _, want := range []string{`"id":"prop_xxx"`, `"en":`, `"image_prompt":`} {
		if !strings.Contains(planSystemPrompt, want) {
			t.Errorf("props 条目缺字段 %s —— 装配与契约按这几个键取（见 project.Prop）", want)
		}
	}
}

// TestShotSchemaCarriesPropIDs 分镜要能引用道具，否则道具卡出了也没人用。
func TestShotSchemaCarriesPropIDs(t *testing.T) {
	for name, p := range ShotPrompts() {
		if !strings.Contains(p, "prop_ids") {
			t.Errorf("%s 的 shots schema 里没有 prop_ids —— 道具出了图也不会进任何一镜", name)
		}
	}
}

// TestPropRulesExplainWhatCounts 规则段落必须说清"什么算高频道具"与"道具怎么跨镜一致"。
func TestPropRulesExplainWhatCounts(t *testing.T) {
	p := SystemPrompt
	for _, want := range []string{
		"PROP CARDS",   // 段落标题
		"RECURS",       // 高频/反复出现
		"key beats",    // 参与关键剧情（判据认概念，不认某个固定措辞）
		"never invent", // 每镜不许自己发明形状（漂移的根）
		"1:1",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("道具规则段缺 %q（用户口径：关键物品必须生成道具、且跨镜不许漂）", want)
		}
	}
}
