package director

import (
	"strings"
	"testing"
)

// source_role_test.go 「分镜必须以**剧本**为主源、原文只是素材」这条平台口径的判据。
//
// 【为什么要有这道门】（2026-10-08 用户：「文章内容转剧本，需要的是 LLM 提取内容分析总结后输出爽文
// 爆点剧本（根据剧本内容再完善输出分镜脚本），**而不是去要求原文内容怎样怎样**」）
//
// 真机结构：material = 抬头 + 【本集剧情】(剧本) + 【本集原文】(小说原文)。此前 `withSettingBrief`
// 会把**整份材料**改标成【本集原文】再交给模型，配合"从原文提炼"的提示 ⇒ 剧本等于没当成主源。
// 这道门钉住两件事：① 材料**原样**（两个节都在，不许改标）② 提示词里必须写明分工。

// realMaterialFixture 与 project/source_sections_test.go 同源的真机形态材料（抬头 + 剧本 + 原文）。
const realMaterialFixture = `《名册之外，八万零一号》系列短视频 第01 集《我是代号》
全局风格:国风写实

【本集剧情】【开场爆点】“重新检测到生命反应。”——灰纪元第八万年，编号 U-80001 的硬件钢印刚亮起。
【场 1|归墟环·维护舱|灰纪元第八万年·第三千四百六十二标准日|内】
  目的:主角刚睁眼就知道自己「不算人」。
  爽点:大爽点——当场把「不存在」的编号念出来。
  正文:祁七把铭牌按在扫描口上。“U-80001。”
【本集原文】(素材：小说原文；分镜只按【本集剧情】排)
第001章 编号之外
她睁开眼的时候，舱里的灯只亮着右边一排。
`

// TestSourceRoleClauseThreeCases 三种材料形态各自说清"哪一份是主源"。
func TestSourceRoleClauseThreeCases(t *testing.T) {
	both := sourceRoleClause(realMaterialFixture)
	for _, want := range []string{"【本集剧情】是**剧本**", "逐场", "【本集原文】只是**参考素材**", "不得"} {
		if !strings.Contains(both, want) {
			t.Errorf("剧本+原文的分工里少了 %q：%s", want, both)
		}
	}
	// 只有剧本（技能侧裁过 / 没有原文段）
	onlyBrief := "【本集剧情】【开场爆点】“重新检测到生命反应。”\n【场 1|维护舱|内】\n  正文:…\n"
	if got := sourceRoleClause(onlyBrief); !strings.Contains(got, "就是【本集剧情】") ||
		!strings.Contains(got, "逐场") {
		t.Errorf("只有剧本时的分工不对：%s", got)
	}
	// 没有剧本：必须**直说没有**，不许让模型猜
	noBrief := "《某剧》第01集\n\n【本集原文】\n第001章 开场\n她睁开眼。\n"
	got := sourceRoleClause(noBrief)
	if !strings.Contains(got, "没有剧本") || !strings.Contains(got, "【本集原文】") {
		t.Errorf("没有剧本时必须直说（并给出退路）：%s", got)
	}
	// 两个标记都没有（技能侧包）：不塞噪音
	if got := sourceRoleClause("随便一份没有节标记的材料"); got != "" {
		t.Errorf("没有节标记的材料不该被塞进分工说明：%q", got)
	}
}

// TestUserPromptsCarryTheScriptFirstClause 四条生成路径都必须带上这条分工 ——
// 它只写在 withSettingBrief 一处，这里用真实拼装出来的提示词验证它真的到了模型面前。
func TestUserPromptsCarryTheScriptFirstClause(t *testing.T) {
	opt := Options{MaxShots: defaultContentDrivenShots, TotalSeconds: 42}
	prompts := map[string]string{
		"单批·内容驱动":  buildSingleUserPrompt(realMaterialFixture, opt, true, true),
		"单批·指定镜数":  buildSingleUserPrompt(realMaterialFixture, opt, false, true),
		"规划轮·内容驱动": buildPlanUserPrompt(realMaterialFixture, opt, true),
		"规划轮·指定镜数": buildPlanUserPrompt(realMaterialFixture, opt, false),
	}
	for name, p := range prompts {
		if !strings.Contains(p, "【本集剧情】是**剧本**") || !strings.Contains(p, "参考素材") {
			t.Errorf("%s：提示词里没有「剧本为主源、原文只是素材」的分工：\n%s", name, p)
		}
		// 材料必须原样在提示词里（含两个节标记）——否则分工说了也白说。
		if !strings.Contains(p, "【本集剧情】【开场爆点】") || !strings.Contains(p, "【本集原文】(素材") {
			t.Errorf("%s：材料没被原样带上（节标记丢了）：\n%s", name, p)
		}
		// 反面：不许出现"整份材料被贴成【本集原文】"的那种拼法。
		if strings.Contains(p, "【本集原文】\n【本集剧情】") ||
			strings.Contains(p, `"""`+"\n\n【本集原文】") {
			t.Errorf("%s：材料被改标成【本集原文】了：\n%s", name, p)
		}
	}
}

// TestNoScriptMaterialSaysSoInPrompt 没有剧本时，提示词里必须**直说**（并把退路写清），
// 而不是让模型自己在材料里找 —— 这正是用户说的"别去要求原文内容"的反面。
func TestNoScriptMaterialSaysSoInPrompt(t *testing.T) {
	noBrief := "《某剧》第01集\n全局风格:国风\n\n【本集原文】\n第001章 开场\n她睁开眼。\n"
	opt := Options{MaxShots: defaultContentDrivenShots, TotalSeconds: 42}
	p := buildSingleUserPrompt(noBrief, opt, true, true)
	if !strings.Contains(p, "没有剧本") {
		t.Errorf("没有剧本时提示词必须直说：\n%s", p)
	}
}
