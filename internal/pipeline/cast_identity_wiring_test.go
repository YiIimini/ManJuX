package pipeline

import (
	"strings"
	"testing"
)

// cast_identity_wiring_test.go 身份真源回填**真的接在两条链上**，而且顺序对。
//
// 背景（2026-10-07 用户问「非主角的角色都随机生成的吗」）：真机第01集五张卡描述为空 +
// 提示词只有年龄 ⇒ 定妆照现编脸 ⇒ 撞脸门当场报警。修法是"锚表原文 → 专职翻译 → 出图"三步，
// 而这三步一旦只接在 script 阶段，存量项目就必须重跑剧本（会重排分镜、废掉已渲镜头）。
//
// runAssets 需要真项目 + 真 ComfyUI 才能跑起来，所以这里做**源码级钉住**：接线被人删掉、
// 或者顺序被挪到出图之后，这条就红 —— 这类"接了但没生效/接晚了"的缺口不会报错。
func TestCastIdentityBackfillIsWired(t *testing.T) {
	assets := readSource(t, "stages_assets.go")
	for _, want := range []string{
		"c.applyCardPrompts(proj, plan)",          // 技能侧卡片仍是第一真源（回填要排在它之后）
		"c.backfillCastIdentity(ctx, proj, plan)", // 回填 + 专职翻译 + 点名缺口
	} {
		if !strings.Contains(assets, want) {
			t.Errorf("资产阶段缺少接线 %q —— 存量项目只能靠重跑剧本才能修，重跑剧本会重排分镜", want)
		}
	}
	// **顺序**：回填必须在**出第一张图之前**（下面每条出图路径都从 plan 取提示词）。
	if i, j := strings.Index(assets, "c.backfillCastIdentity("), strings.Index(assets, "genAsset := assetGenerator("); i < 0 || j < 0 || i > j {
		t.Errorf("身份回填必须在装配出图器之前（backfill@%d, genAsset@%d）—— 否则这次出图仍用旧提示词", i, j)
	}

	impl := readSource(t, "asset_cards.go")
	for _, want := range []string{
		"c.store.LoadSource(proj.ID)",             // 锚表在源文本里
		"FillAnchorDescriptions(plan, src)",       // ① 锚表原文补进 description
		"TranslateCardPrompts(ctx, client, plan)", // ② 专职会话译成英文定妆提示词
		"IdentityPromptGaps(plan)",                // ③ 出图前点名缺口
		"TruncatedAnchorSections(src)",            // 源文本被截断的节要如实点名
		"c.store.SavePlan(proj.ID, plan)",         // 补完要落盘，否则下次又从头来
		"cardTranslateClient()",                   // 没有 LLM 时如实播报，不静默跳过
		`"warn"`,                                  // 三条降级路径都是 warn（不拦产线）
	} {
		if !strings.Contains(impl, want) {
			t.Errorf("回填实现缺少 %q —— 会变成「接了但没生效」或「静默跳过」", want)
		}
	}

	script := readSource(t, "stages_script.go")
	for _, want := range []string{
		"FillAnchorDescriptions(plan, text)",      // 新项目在存盘前就补齐
		"TranslateCardPrompts(ctx, client, plan)", // 翻译照旧挂在脚本阶段
		"IdentityPromptGaps(plan)",                // 缺口一样要点名
		"TruncatedAnchorSections(text)",           // 断节一样要点名
	} {
		if !strings.Contains(script, want) {
			t.Errorf("脚本阶段缺少 %q —— 新方案落盘时这些卡还是不完整的", want)
		}
	}
}
