package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 锚表兜底的英文提示词必须能被**专职翻译会话**接手（2026-09-29 真机实测）。
//
// 现场：第 01 集六张定妆照长得几乎一样，白瓷装甲的女主角被画成普通古装女子 ——
// 因为六张卡的英文提示词都只有锚表兜底的那一句
// `a Chinese period-film character, aged 18 to 20`（脸型/眉眼/发长/服装一个都没有）。
// 而兜底串一填进 `ImagePrompt`，"非空就不翻"的判据就让翻译永远不会发生。
//
// 判据三件事：① 兜底串带标记、能被识别成"待翻译"；② 标记**不许发给图像模型**；
// ③ 已经写好提示词的卡不受影响（不许无端重翻，那会白烧 LLM 且可能改掉作者口径）。
func TestAnchorStubIsTranslatableButNeverSent(t *testing.T) {
	anchor := project.SettingChar{
		Name: "霜见",
		Note: "定位：第二主角／通信手",
		// Raw = 锚表里那一节的原文（真机里就是它当 description）
		Raw: "### 霜见（定位：第二主角／通信手。拾荒船团的读数员）\n\n" +
			"- 年龄：少年，17–19 岁\n- 身形：清瘦，肩背不宽\n- 脸型：瘦长脸，下颌线清晰\n" +
			"- 眉眼：**灰蓝色瞳孔**；柳叶眼；眼尾略垂\n- 首套服装：黑色高领针织 ＋ 灰白围巾\n",
		Fields: []project.SettingField{
			{Key: "年龄", Value: "17–19 岁"},
			{Key: "比例锚_机器", Value: "7.5 头身"},
			{Key: "脸型", Value: "瘦长脸，下颌线清晰"},
			{Key: "首套服装", Value: "黑色高领针织 ＋ 灰白围巾"},
		},
	}
	stub := anchorImagePrompt(anchor)
	if !strings.HasPrefix(stub, AnchorStubTag) {
		t.Fatalf("兜底串必须带标记（否则翻译会话认不出它、永远不翻）：%q", stub)
	}

	// ① 兜底串 = 待翻译
	card := project.Character{Name: "霜见", Description: anchor.Raw, ImagePrompt: stub}
	if !NeedsPromptTranslation(card) {
		t.Error("带兜底串的卡必须交给翻译会话（这正是「六张脸一样」的根因）")
	}
	// 空提示词同样待翻译（老行为不变）
	if !NeedsPromptTranslation(project.Character{Name: "x", Description: "瘦长脸"}) {
		t.Error("没有提示词的卡仍要翻译")
	}
	// ③ 已经写好提示词的卡不动
	full := project.Character{Name: "祁七", Description: "外观二十岁", ImagePrompt: "Android girl, porcelain-white armour, brass halo"}
	if NeedsPromptTranslation(full) {
		t.Error("已有提示词的卡不该重翻（会白烧 LLM，还可能改掉作者口径）")
	}
	// 没描述的卡没什么可翻
	if NeedsPromptTranslation(project.Character{Name: "x"}) {
		t.Error("没有中文描述的卡不该进翻译队列")
	}

	// ② 标记绝不许出现在送往图像模型的装配结果里
	prompt := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "霜见", Description: anchor.Raw,
		ImagePrompt: stub, FacesOn: true, FrameW: 768, FrameH: 1344,
	}, &project.Plan{Style: "guofeng_cinematic"})
	if strings.Contains(prompt, AnchorStubTag) {
		t.Error("兜底标记被送进了图像模型 —— 它会变成画面上的字")
	}
	if !strings.Contains(prompt, "a Chinese period-film character") {
		t.Error("标记被剥掉之后，兜底内容本身要留着（没有它这张卡就完全没有英文依据）")
	}
}
