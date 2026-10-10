package pipeline

import (
	"strings"
	"testing"
)

// 真机那份风格串（2026-10-10《废柴被逐那天，我捡到一只吞吞·第01集》plan.json 的 style 原文）。
//
// 它同时含「半写实绘画」与「3D CG 质感」两个互斥介质 —— 那是另一条待办；
// 这条测试只盯**同一句话在"物/人"两类资产上必须落成两套介质词**。
const realDramaStyle = "中式梦幻磅礴的半写实绘画风，3D CG 质感：眉骨有阴影、眼尾有一线光、皮肤带暗部；" +
	"执法堂青砖高窗的晌午硬光、山门万丈石阶铺进云海。"

// 借自 chatfire-AI/huobao-drama 的「按资产种类分口径」：他们把提示词拆成
// character-prompt / scene-prompt / prop-prompt 三套 SKILL，各写各的介质与取景。
//
// 【我们缺的那一格 + 真机证据】盘上 `prop:prop_key`（一把铜钥匙）的 image_prompt 里写着
// "next-generation 3D CG **character render, realistic skin shading** with visible skin texture
// and natural facial highlights" —— 给一把钥匙要求「皮肤着色 / 面部高光」。
func TestStyleExpandObjectKindGetsObjectMedium(t *testing.T) {
	obj := styleExpandFor("", realDramaStyle, true, KindObject)
	if !strings.Contains(obj, "prop render") {
		t.Errorf("物件该拿「prop render」那一套介质词，实际：%s", obj)
	}
	for _, bad := range []string{"character render", "skin shading", "facial highlights"} {
		if strings.Contains(obj, bad) {
			t.Errorf("物件提示词里不该出现 %q（那是人像那一套的介质词）：%s", bad, obj)
		}
	}
	// 反向针：同一条风格串给**人**时，必须仍然是人像那一套 —— 否则这就不是「按种类分派」，
	// 而是把那段词整块删了（摆设守卫的典型形态）。
	human := styleExpandFor("", realDramaStyle, true, KindHuman)
	if !strings.Contains(human, "character render") || !strings.Contains(human, "skin") {
		t.Errorf("人物提示词必须仍然拿人像那一套（character render / skin），实际：%s", human)
	}
}

// 场景口径：无人物空镜必须把「任何形态的人」都排掉（借自 huobao 的 scene-prompt SKILL 原文
// 「场景图中不能出现任何的人（含背影、剪影、倒影、照片里的人）」）。
func TestSceneFramingExcludesEveryKindOfPerson(t *testing.T) {
	fr := EntityProfileOf(KindScene).SheetFraming
	for _, want := range []string{"unpeopled", "no silhouette", "no reflection", "no crowd"} {
		if !strings.Contains(fr, want) {
			t.Errorf("场景取景口径缺 %q —— 场景图里混进「像人但不是本剧角色」的东西，"+
				"H3 会把它当身份参考（实测过同型故障）。实际：%s", want, fr)
		}
	}
}
