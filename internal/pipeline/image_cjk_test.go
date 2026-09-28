package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 进模型的**画面提示词不许残留汉字**。
//
// 【2026-09-28 用户报「定妆照也有莫名的文字」「一张定妆照还出现相同角色多人物」】
// 根因：平台追加的提示词块**全都带中英对照**（`(中文对照:……)`），而图片模型会把中文当成
// 「要画的字」**整段照抄进画面** —— 实测那张定妆照左右两侧与底部全是乱码中文段落，连
// "中文对照:"四个字都清晰可见；成片的文字块又把构图冲乱，同一个角色被画成了两个人。
//
// 这条门钉住「**产物进模型前没有汉字**」。它比「某个块里别写中文」更结实：块是给人读的、
// 中文对照要留（维护者靠它看懂英文口径），所以对策只能是**出口处清**，而不是让人别写。
func TestImagePromptsCarryNoCJK(t *testing.T) {
	plan := &project.Plan{
		Style:      "国风电影级",
		Characters: []project.Character{{ID: "c1", Name: "小凡"}},
	}
	base := PortraitSubject{
		Kind: KindHuman, Name: "小凡", Description: "十七岁少年，灰布短褂",
		ImagePrompt: "a slim young man in a grey robe", FacesOn: true,
	}
	portrait := PortraitPrompt(base, plan)
	cases := map[string]string{
		"项目侧定妆照": portrait,
		"库侧定妆照":  LibraryPortraitPrompt("小凡", "十七岁少年", "a slim young man in a grey robe"),
		"三视图板":   BoardImagePrompt(portrait),
	}
	for name, got := range cases {
		if n := project.CountCJK(got); n != 0 {
			t.Errorf("%s 进模型前残留 %d 个汉字（会被整段抄成画面上的字）:\n%s", name, n, got)
		}
	}
	// 反向：卡原文本身**不许**被清（存量包与技能侧导入的数据里可能就是中文，清素材会丢信息）
	raw := "a slim young man in a grey robe with a brass tag, plain background"
	got := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "小凡", Description: "十七岁少年",
		ImagePrompt: raw, FacesOn: true,
	}, plan)
	if !strings.Contains(got, "a brass tag") {
		t.Error("卡原文不得被清掉（那是素材，不是平台追加的对照块）")
	}
}
