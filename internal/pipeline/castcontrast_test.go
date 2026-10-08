// castcontrast_test.go 回归:同剧班底对比块(2026-09-11 用户定调「不同
// 角色脸不能相似」)——对比块必须列出其它角色、不列自己、有标识头。
//
// 【2026-10-08 用户「角色渲染的很丑，你看一下资产库」】夹具改成**真机形态**：
// 小说线/平台直出的卡 description 是中文、英文外形在 `image_prompt` 里
// （真机那张姜晚舟的 image_prompt 是整段英文定妆提示词）。
// 旧夹具是"只有中文 description、没有 image_prompt"的老形态 ⇒ 摘要被清汉字清空后
// 对比块**整段消失**也照样绿 —— 夹具不真，这道门就白设（AGENTS.md：夹具照真实形态写）。
package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

func TestCastContrastBlock(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "a", Name: "纪野", Description: "19岁清瘦少年,右眉尾旧疤",
			ImagePrompt: "A lean nineteen-year-old young man with a pale complexion, a small old scar at the right eyebrow end"},
		{ID: "b", Name: "魏启明", Description: "肥胖壮硕圆脸少年",
			ImagePrompt: "A stocky overweight young man with a round full-cheeked face and heavy sloping shoulders"},
		{ID: "c", Name: "裴修远", Description: "23岁窄长脸,白西装",
			ImagePrompt: "A twenty-three-year-old man with a narrow long face and hollow cheeks, in a white suit"},
	}}
	b := castContrastBlock(plan, "", "魏启明", KindHuman)
	if !strings.Contains(b, "纪野") || !strings.Contains(b, "裴修远") {
		t.Fatalf("对比块应列出其它角色: %q", b)
	}
	// 真机那批卡的英文外形**只在 image_prompt 里**（description 是中文）：
	// 少了这一档，对比块对小说线项目就永远是空的。
	if !strings.Contains(b, "narrow long face") || !strings.Contains(b, "old scar at the right eyebrow") {
		t.Fatalf("对比块必须带上别人的英文外形摘要(真机卡的英文外形在 image_prompt 里): %q", b)
	}
	if strings.Contains(b, "魏启明(肥胖") {
		t.Fatalf("对比块不应把自己列进班底列表: %q", b)
	}
	if !strings.Contains(b, "CAST DISTINCTNESS") || !strings.Contains(b, "「魏启明」") {
		t.Fatalf("缺标识头/自称: %q", b)
	}
	if castContrastBlock(nil, "", "x", KindHuman) != "" || castContrastBlock(&project.Plan{}, "", "x", KindHuman) != "" {
		t.Fatalf("无 plan/无其它角色时应返回空")
	}
	// 只有一个角色（就是自己）时对比块为空 —— 这条同时钉住"id 缺失时退回按名字判自己"
	if castContrastBlock(&project.Plan{Characters: []project.Character{{Name: "独角"}}}, "", "独角", KindHuman) != "" {
		t.Fatalf("单角色剧无对比对象应返回空")
	}
}

// TestCastContrastSkipsEmptySummary 摘要必须**英文可读**才入列（2026-10-08 真机取证）。
//
// 真机三张卡的提示词里都是 `other cast members of this series: ( …); ( …); ( …)` 与
// `NOT: …` —— 来源是"取中文 description、追加块统一清汉字"⇒ 清成一个省略号却仍入列。
// 那种条目既没信息，占位符还会被模型当成"画面上要写的字"。
//
// 判据：① 全中文（英文外形还没翻出来的状态）⇒ **整段不拼**，也不许拼占位符；
// ② 混着来 ⇒ 有英文的进列、没英文的不进列，且任何条目都不是省略号/空括号。
func TestCastContrastSkipsEmptySummary(t *testing.T) {
	cn := &project.Plan{Characters: []project.Character{
		{ID: "a", Name: "姜晚舟", Description: "二十出头,身形清瘦,右肩有一块旧疤"},
		{ID: "b", Name: "阿糖", Description: "巴掌大的团子,通体暖白,两只小手"},
	}}
	if b := castContrastBlock(cn, "", "姜晚舟", KindHuman); b != "" {
		t.Fatalf("全中文摘要时不许拼出占位符条目: %q", b)
	}
	if ids := castFaceIDs(cn, "", "姜晚舟"); len(ids) != 0 {
		t.Fatalf("全中文摘要时 FACE ID 名单必须为空: %v", ids)
	}

	mix := &project.Plan{Characters: []project.Character{
		{ID: "a", Name: "姜晚舟", Description: "二十出头,身形清瘦,右肩有一块旧疤"},
		{ID: "b", Name: "阿糖", Description: "巴掌大的团子,通体暖白,两只小手",
			ImagePrompt: "A palm-sized warm-white round dumpling spirit with two stubby arms and a soft matte surface"},
	}}
	b := castContrastBlock(mix, "", "姜晚舟", KindHuman)
	if !strings.Contains(b, "阿糖(A palm-sized warm-white") {
		t.Fatalf("有英文 image_prompt 的卡必须列出来: %q", b)
	}
	if strings.Contains(b, "…") || strings.Contains(b, "( )") || strings.Contains(b, "()") {
		t.Fatalf("对比块里不许出现省略号占位/空条目: %q", b)
	}
	ids := castFaceIDs(mix, "", "姜晚舟")
	if len(ids) != 1 || !strings.Contains(ids[0], "palm-sized warm-white") {
		t.Fatalf("FACE ID 名单应只有那条真摘要: %v", ids)
	}
	// FACE ID 收尾段**自己**也要挡住空条目（真机形态：`NOT: … NOT: … NOT: …`）。
	// 直接喂一份含空条目的名单 —— 拿"已经过滤过的干净名单"断言是**摆设守卫**：
	// 名单在上游被滤过了，这里再坏掉也照样绿（反向验证针6 当场咬出这一处）。
	junk := PortraitFaceIDClause("", []string{"…", "", "   ", "a real summary of one other person"})
	if n := strings.Count(junk, "NOT: "); n != 1 {
		t.Fatalf("空/占位条目一律不许写成 NOT 行，实际 NOT 行数 %d：%q", n, junk)
	}
	if strings.Contains(junk, "NOT: …") || strings.Contains(junk, "NOT: \n") {
		t.Fatalf("NOT 行里不许出现省略号占位或空内容：%q", junk)
	}
	// 全是占位条目时：既不许写 NOT 行，也不许写那段"Anyone listed under NOT…"的收尾纪律
	allJunk := PortraitFaceIDClause("", []string{"…", ""})
	if strings.Contains(allJunk, "NOT: ") || strings.Contains(allJunk, "Anyone listed under NOT") {
		t.Fatalf("全是占位条目时不该留下对不存在的名单的纪律：%q", allJunk)
	}
}

// TestEnglishSummaryGate 摘要闸门**本体**：清完汉字后至少要有几个英文字母才算"可读"。
//
// 真机形态（逐字取自盘上那份 asset.json 的 image_prompt）：
//
//	other cast members of this series: ( …); ( …); ( …)
//	FACE ID … NOT: … NOT: … NOT: …
//
// 那种条目既没信息，占位符还会被模型当成"画面上要写的字"。
// 这条测试**直接喂闸门本身**——拿上游已过滤的数据断言是摆设守卫（反向验证针2 咬出过）。
func TestEnglishSummaryGate(t *testing.T) {
	for _, s := range []string{"…", "… …", "。，；", "（…）", "id 7", "a, b", "3x"} {
		if got := englishSummary(s); got != "" {
			t.Errorf("%q 清完汉字后没剩几个英文字母，必须判**不可读**，实际 %q", s, got)
		}
	}
	for _, s := range []string{
		"A woman in her early twenties, medium height, lean-toned with a strong grip",
		"IDENTITY LOCK: 16-year-old boy, narrow long face, sun-baked dull bronze skin",
		"巴掌大的团子,通体暖白 —— warm-white round dumpling spirit with two stubby arms",
	} {
		if got := englishSummary(s); got == "" {
			t.Errorf("%q 里有真英文外形，不该被判掉", s)
		}
	}
}
