package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 声线锚定(2026-09-25 用户报「人物配音错误异常」)。
//
// 角色卡上的 `voice` 此前**从未注入镜头提示词**(anchorPrompt 只注入外貌与类型条款),
// 逐镜声线全靠 LLM 现写 —— 实测同一角色的措辞在镜头间漂移:
//
//	卡:  Young female, low and even, dry and unhurried, with a faint hard edge…
//	镜8: young female, low and even, dry and unhurried…            (大小写差)
//	镜11:young female **voice**, low and even…                      (多一个 voice)
//	镜15:… blunt and confrontational, **speaking** from the chest   (卡是 speaks)
//
// H3 把这段文字当**音频条件**用:措辞一变音色就跟变。所以平台必须把权威声线钉进
// 每条镜头提示词,而不是指望 LLM 每次抄得一模一样。
func TestAnchorInjectsCanonicalVoice(t *testing.T) {
	plan := &project.Plan{
		Style: "2D DONGHUA",
		Characters: []project.Character{
			{ID: "char_qinghe", Name: "顾青禾", Description: "二十四岁女性,短发马尾",
				Voice: "Young female, low and even, dry and unhurried, with a faint hard edge under the calm."},
			{ID: "char_haotong", Name: "郝通", Description: "四十八岁男性,站长",
				Voice: "Middle-aged male, wheedling and soft, quick apologetic laugh."},
		},
	}
	shot := &project.Shot{ID: 9, Characters: []string{"char_qinghe", "char_haotong"},
		H3Prompt: "detailed_description:\n[Shot 1] 对话…", Duration: 6, Mode: "ref2va"}
	got := ShotRenderPrompt(&project.Project{}, plan, shot)
	for _, want := range []string{
		"【声线(逐字照抄,不得改写)】",
		"Young female, low and even, dry and unhurried",
		"Middle-aged male, wheedling and soft",
		"不得相同",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("镜头提示词缺少声线锚定内容(%q):\n%s", want, got)
		}
	}
	// 没有 voice 的角色(手动上传/旧方案)不该出现空条目
	bare := &project.Plan{Characters: []project.Character{{ID: "c1", Name: "甲", Description: "路人"}}}
	got2 := ShotRenderPrompt(&project.Project{}, bare, &project.Shot{ID: 1, Characters: []string{"c1"}, H3Prompt: "x"})
	if strings.Contains(got2, "【声线") {
		t.Error("没有声线信息时不该输出声线段(空条目会让模型自己编)")
	}
}

// TestPlanChibiCardSuppressesAutoChibi Q 版重复(2026-09-25 用户报「Q版形象相同的角色
// 创建多个」):技能端把内心 Q 版写成独立角色卡之后,平台不该再给每个真人角色造一份
// <id>_chibi —— 否则同一个 Q 版形象会有两套资产、两个库条目(实测:
// 「迷你青禾」与「迷你青禾内心Q版」同时在库)。
func TestPlanChibiCardSuppressesAutoChibi(t *testing.T) {
	withChibi := &project.Plan{Characters: []project.Character{
		{ID: "char_qinghe", Name: "顾青禾", Description: "二十四岁女性"},
		{ID: "char_chibi", Name: "迷你青禾(内心Q版)", Description: "巴掌大 Q 版小人,三头身"},
	}}
	if !planHasChibiCard(withChibi) {
		t.Error("方案里有 Q 版角色卡时应识别出来(否则平台会再造一份)")
	}
	noChibi := &project.Plan{Characters: []project.Character{
		{ID: "char_qinghe", Name: "顾青禾", Description: "二十四岁女性"},
	}}
	if planHasChibiCard(noChibi) {
		t.Error("没有 Q 版卡时仍应自动生成(这是原有的贴心能力)")
	}
}
