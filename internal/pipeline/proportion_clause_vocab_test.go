package pipeline

import "testing"

// 比例句识别的**口径覆盖**门。
//
// 【2026-09-28 审计实测的缺陷形态】`skillProportionSignatures` 原先只认技能侧写法的
// "seven and a half heads"，而**平台自己的提示词**（`sysFaceVocabRules`）要求模型写
// "eight and a half heads tall" —— 于是模型照平台要求**写对了**，识别却漏了，
// 平台随后又注入自己那套比例措辞 ⇒ 同一段提示词里两套比例、模型取中值。
// 这正是 proportion_clause.go 开头写的"丑"的来源，而且形态最隐蔽：
// **不是漏在"没写比例"，是漏在"写了却不认"** —— 所以单独钉一条。
//
// 四方口径不一致（技能侧锚表 7.5/8 两档、用户《二》九头身、《【额外】》8.5、平台自拍过 7.5）
// 是**用户的决定**，本门不替它选；这里只保证"无论哪一方写出来，识别都不许漏"。
func TestProportionClauseRecognizesEveryWording(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
	}{
		{"平台提示词要求的 8.5", "the figure is eight and a half heads tall with a slim waist and long legs"},
		{"技能侧的 7.5", "the figure is seven and a half heads tall with a slim waist and long legs"},
		{"锚表的机器可读写法", "比例锚_机器：8 头身；站姿笔直"},
		{"八头身（中文）", "身高八头身，四肢偏长"},
		{"七个半头（中文）", "身高七个半头，肩线平"},
		{"技能侧整句特征仍在", "the eye line sits at the vertical middle of the head"},
	}
	for _, c := range cases {
		if !HumanProportionClausePresent(c.prompt) {
			t.Errorf("[%s] 该被认成「已有比例句」却漏了 —— 漏认的后果是平台再注入一套比例：%q",
				c.name, c.prompt)
		}
	}
	// 反向：压根没写比例的提示词不许误判成"已有"（否则平台那套该注入的就不注入了）
	for _, s := range []string{
		"A girl stands in the rain, upper-body framing, plain background.",
		"clean anime-style face, clear-cut jawline, large bright eyes",
	} {
		if HumanProportionClausePresent(s) {
			t.Errorf("这段没有比例句，不该误判成「已有」：%q", s)
		}
	}
}
