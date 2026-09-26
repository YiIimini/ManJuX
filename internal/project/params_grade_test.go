package project

import "testing"

// TestSanitizeNormalizesGrade 调色档(assemble.grade)的归一(甲1):
// 空串(老项目 json 里没有这个键)与任何认不出的值一律按 film —— **调色默认开**。
//
// 为什么"认不出也是 film"而不是 off:关掉调色是显式表态,写错一个词就静默关掉整片
// 调色,比默认开着更难发现(用户看到的是"这版怎么没调色",而日志里什么都没有)。
func TestSanitizeNormalizesGrade(t *testing.T) {
	cases := map[string]string{
		"":      "film", // 缺键(存量项目)
		"film":  "film",
		"off":   "off",
		"soft":  "soft",
		"heavy": "heavy",
		// 大小写/空白不敏感:与 compose.NormalizeGrade 的宽容度对齐
		" OFF ": "off",
		"Heavy": "heavy",
		// 认不出的值落回默认
		"cinematic": "film",
		"true":      "film",
		"不知道":       "film",
	}
	for in, want := range cases {
		p := StageParams{}
		p.Assemble.Grade = in
		p.Sanitize()
		if p.Assemble.Grade != want {
			t.Errorf("grade %q 应归一为 %q,实际 %q", in, want, p.Assemble.Grade)
		}
	}
}

// TestDefaultParamsGradeIsFilm 平台默认参数必须把调色开着(用户要的是"别再是 H3 直出
// 的平淡数码味"),而不是留空等运行期兜底 —— 默认值本身就是产品口径的一部分。
func TestDefaultParamsGradeIsFilm(t *testing.T) {
	if got := DefaultStageParams().Assemble.Grade; got != "film" {
		t.Errorf("默认调色档应为 film,实际 %q", got)
	}
}
