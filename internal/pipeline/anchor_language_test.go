package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 锚定块的**内容**必须是英文（2026-09-29 真机实测）。
//
// 现场：真跑那一集的第 01 镜渲染出来，画面里那块金属铭牌上清清楚楚写着「钢印铭牌」四个字 ——
// 而它们正是锚定块里**中文角色描述**的两个词（「机型**钢印** U-80001」＋「腰侧挂一串金属**铭牌**
// 记名链」）被 H3 当成"要画的字"照搬。平台自己的口径是「正向提示词必须全英文，中文只进元数据」
// （docs/平台侧生成规范-角色场景道具分镜-2026-09-27.md §2 的 ⛔ 反向执行），锚定块属于正向提示词。
//
// 判据因此是：有英文外观串的卡，锚定条目里**不得出现中文描述**；
// 而**没有英文的存量卡要回落中文**（宁可留汉字，也不能让这一镜没有身份信息）。
func TestAnchorContentIsEnglishWhenCardHasEnglish(t *testing.T) {
	plan := &project.Plan{
		Style: "guofeng_cinematic",
		Characters: []project.Character{{
			ID: "char_qiqi", Name: "祁七",
			// 真实描述（真机那一集原文的一小段，含把字画上画面的那两个词）
			Description: "外观二十岁（非人类机体，旧舰队义体维护单元，机型钢印 U-80001）。腰侧挂一串金属铭牌记名链。",
			ImagePrompt: "Android girl with apparent age twenty, slender build with eight-heads-tall doll " +
				"proportions; a golden brass mechanical halo ornament; white ceramic armour plates over a " +
				"black metal inner frame, a chain of titanium nameplates at her left hip" +
				"RENDER STYLE (mandatory): 3D CG — next-generation render" +
				" FRAMING (mandatory): a front-facing chest-up portrait, shot STRAIGHT-ON from the front",
		}},
		Scenes: []project.Scene{{
			ID: "scene_bay", Name: "归墟环断口维护舱",
			Description: "废弃维护舱内景：四壁是上千个金属工具抽屉。",
			ImagePrompt: "Dead still air in an abandoned orbital maintenance bay; thousands of brushed-metal " +
				"tool drawers climb all four walls" + "SCENE FIDELITY (mandatory): the place must match the brief",
		}},
		Shots: []project.Shot{{ID: 1, Scene: "scene_bay", Characters: []string{"char_qiqi"}, H3Prompt: "shot text"}},
	}
	got := ShotRenderPrompt(nil, plan, &plan.Shots[0])

	// ① 中文描述**不得**出现在锚定条目里（那两个词正是被画上画面的）
	for _, w := range []string{"钢印", "铭牌记名链", "外观二十岁"} {
		if strings.Contains(got, w) {
			t.Errorf("锚定块里还有中文描述 %q —— H3 会把它画到画面上（实测那块铭牌上写着「钢印铭牌」）", w)
		}
	}
	// ② 英文外观串要在场（换成英文不等于把身份信息删了）
	if !strings.Contains(got, "Android girl with apparent age twenty") {
		t.Error("锚定条目丢了英文外观串：身份信息不能因为换语言而消失")
	}
	if !strings.Contains(got, "Dead still air in an abandoned orbital maintenance bay") {
		t.Error("场景条目同样要用英文外观串（场景卡里写着中文描述时更要如此）")
	}
	// ③ 卡里那份**定妆照专用**的取景条款不许跟着进来：这一镜不是"正脸胸像"
	if strings.Contains(got, "chest-up portrait") || strings.Contains(got, "RENDER STYLE (mandatory)") {
		t.Error("把定妆照的取景/画风条款塞进了镜头提示词 —— 这一镜会被拍成正脸胸像")
	}
	// ④ 兜底：没有英文的老卡仍要带上中文描述（宁可留汉字也要有身份信息）
	plan2 := &project.Plan{Shots: []project.Shot{{ID: 1, Characters: []string{"char_old"}, H3Prompt: "x"}},
		Characters: []project.Character{{ID: "char_old", Name: "老卡", Description: "瘦长脸，左眉尾一道短疤"}}}
	if got2 := ShotRenderPrompt(nil, plan2, &plan2.Shots[0]); !strings.Contains(got2, "左眉尾一道短疤") {
		t.Error("没有英文提示词的存量卡必须回落中文描述（否则这一镜没有任何身份信息）")
	}
}
