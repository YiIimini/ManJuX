package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 「不是实体的」不许用人的渲染方式（2026-09-29 用户原话）。
//
// 真机现场：无实体角色「肩头虚影」的卡里写着
// 「巴掌大、约一百二十毫米、纤细的木偶关节人形剪影、冷蓝白的光、半透明全息」，
// 出来的图却是**一个真人大小的半透明女人站在黑底上** ——
//
//	① 背景黑（用户口径：角色卡一律**纯白无缝底**，与其它卡一致才能当参考图）；
//	② 尺度丢了（"巴掌大" → 等人高）；
//	③ 形态退化成通用"女鬼"，卡里写的木偶关节/冷蓝白光/全息质感全没了。
//
// 判据落在**实际下发的那段文本**上（profile 的取景与身份锁），不是"我改过文件"。
func TestIncorporealDoesNotUseHumanRendering(t *testing.T) {
	p := EntityProfileOf(KindIncorporeal)

	// ① 纯白无缝底（与其它角色卡同一口径）
	if !strings.Contains(p.SheetFraming, "pure white seamless backdrop") {
		t.Error("无实体的取景必须是纯白无缝底 —— 黑底那张与其它卡不是一套，没法当参考图")
	}
	if strings.Contains(p.SheetFraming, "plain dark background") {
		t.Error("无实体又退回黑底了")
	}
	// ② 尺度照卡里写的，不许自动放大成人
	for _, want := range []string{"SIZE and PROPORTION the brief states", "never scaled up to a life-size person"} {
		if !strings.Contains(p.SheetFraming, want) {
			t.Errorf("取景缺少 %q —— 「巴掌大」会被画成等人高", want)
		}
	}
	// ③ 身份锁要点明"尺度与形态来自简报，不是人体比例"
	for _, want := range []string{"SCALE AND FORM COME FROM THE BRIEF", "palm-sized", "generic life-size human"} {
		if !strings.Contains(p.SheetIdentity, want) {
			t.Errorf("身份锁缺少 %q —— 否则只会画出通用人形", want)
		}
	}
	// ④ 人像专属的取景句一个都不许出现在无实体上（上半身正脸那套）
	for _, bad := range []string{"chest-up", "upper-body framing", "front-facing"} {
		if strings.Contains(p.SheetFraming, bad) || strings.Contains(p.SheetIdentity, bad) {
			t.Errorf("无实体夹带了人像专属取景 %q", bad)
		}
	}
	// ⑤ 它仍然是无实体：半透明、边缘消散
	if !strings.Contains(p.SheetIdentity, "NOT solid") {
		t.Error("无实体的身份锁要写明它不是实体（边缘消散）")
	}
}

// 翻译结果必须真的**写回**兜底串的卡（2026-09-29 真机：钟离与石头两张卡出图像同一个人）。
//
// 事故形状是"同一判据两处实现"：收集待翻译卡时已改成"空串**或**兜底串"，
// 而写回那一侧还写着 `if ImagePrompt != "" { continue }` —— 模型翻了、结果也回来了，
// 却因为字段非空被跳过 ⇒ 锚表补出来的卡永远停在 `[ANCHOR-STUB] … aged X`
// ⇒ 出图只有年龄差异 ⇒ 用户问「石头 和 钟离 咋那么相似？」。
func TestTranslatedPromptIsWrittenBackToStubCards(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "char_ff31f70f", Name: "钟离", Description: "外表 17–18，清瘦挺直，八头身，指节细",
			ImagePrompt: AnchorStubTag + "a Chinese period-film character, aged 17 to 18, eight heads tall"},
		{ID: "char_452d1576", Name: "石头", Description: "11–12 岁，矮小结实，六头身，圆润苹果脸",
			ImagePrompt: AnchorStubTag + "a Chinese period-film character, aged 11 to 12, six heads tall"},
		{ID: "char_written", Name: "已有提示词的卡", Description: "作者写过的",
			ImagePrompt: "A lean 17-year-old timekeeper with a long braid"}, // 不许被覆盖
	}}
	m := map[string]string{
		"钟离":      "A slender seventeen-year-old timekeeper girl, eight heads tall, long braid to the waist",
		"石头":      "A stocky eleven-year-old village boy, six heads tall, round apple face",
		"已有提示词的卡": "SHOULD NOT WIN",
	}
	n, missing := applyTranslatedPrompts(plan, m)
	if n != 2 {
		t.Fatalf("兜底串的卡必须被写回（2 张），实得 %d；missing=%v", n, missing)
	}
	for _, c := range plan.Characters {
		if strings.Contains(c.ImagePrompt, AnchorStubTag) {
			t.Errorf("%s 还停在兜底串：%q —— 出图只会是「通用脸」", c.Name, c.ImagePrompt)
		}
	}
	if got := plan.Characters[0].ImagePrompt; !strings.Contains(got, "timekeeper girl") {
		t.Errorf("钟离没拿到翻译结果：%q", got)
	}
	if got := plan.Characters[2].ImagePrompt; got != "A lean 17-year-old timekeeper with a long braid" {
		t.Errorf("已有真提示词的卡被覆盖了：%q", got)
	}
	// 模型漏给某张卡时必须**报出来**（不能静默留着兜底串）
	plan2 := &project.Plan{Characters: []project.Character{
		{Name: "钟离", Description: "d", ImagePrompt: AnchorStubTag + "stub"},
		{Name: "石头", Description: "d", ImagePrompt: AnchorStubTag + "stub"},
	}}
	n2, missing2 := applyTranslatedPrompts(plan2, map[string]string{"钟离": "A slender girl"})
	if n2 != 1 || len(missing2) != 1 || missing2[0] != "石头" {
		t.Fatalf("漏翻的卡要如实报出来：n=%d missing=%v", n2, missing2)
	}
}

// 空卡（既无描述又无提示词）**不许出图**（同一现场的第二个成因）。
//
// 那一版方案里同一个「肩头虚影」有两张卡，第二张只有 board_prompt ——
// 平台照样给它出了一张图，而"从零"只能画出一个通用人形（与设定无关）。
// 接线判据：资产阶段必须先判空再出图，并把"跳过"如实说出来。
func TestEmptyCharacterCardIsSkippedNotInvented(t *testing.T) {
	raw, err := os.ReadFile("stages_assets.go")
	if err != nil {
		t.Fatalf("读 stages_assets.go 失败：%v", err)
	}
	src := string(raw)
	if !strings.Contains(src, "没有描述也没有定妆提示词 —— **跳过出图**") {
		t.Error("资产阶段没有「空卡不出图」的分支 —— 从零生成只会得到通用人形")
	}
	if !strings.Contains(src, "strings.TrimSpace(ch.Description) == \"\" && strings.TrimSpace(ch.ImagePrompt) == \"\"") {
		t.Error("空卡的判据必须是「描述与提示词都空」（有其一就能出图），别写成只要其一为空就跳")
	}
}
