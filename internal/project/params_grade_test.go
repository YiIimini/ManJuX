package project

import "testing"

// TestSanitizeNormalizesGrade 调色档(assemble.grade)的归一(甲1 + 技能侧清单 §2.1):
// 未表态(空串 / auto)归成 GradeAuto,四个规范档位原样保留,认不出的脏值落回 film。
//
// 为什么"认不出也是 film"而不是 off:关掉调色是显式表态,写错一个词就静默关掉整片
// 调色,比默认开着更难发现(用户看到的是"这版怎么没调色",而日志里什么都没有)。
func TestSanitizeNormalizesGrade(t *testing.T) {
	cases := map[string]string{
		"":       GradeAuto, // 缺键(存量项目)= 未表态 = 自动档
		"auto":   GradeAuto, // 界面上显式选"自动"
		" AUTO ": GradeAuto, // 大小写/空白不敏感
		"film":   "film",
		"off":    "off",
		"soft":   "soft",
		"heavy":  "heavy",
		// 大小写/空白不敏感:与 compose.NormalizeGrade 的宽容度对齐
		" OFF ": "off",
		"Heavy": "heavy",
		// 认不出的脏值落回 film(显式档位,不放行自动降档)
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

// TestSanitizeKeepsUnsetGradeAuto Guard(§2.1 的关键前提):归一**不许**把"未表态"定死成 film。
//
// 这个函数是"保存项目参数"那一处调用的 —— 一开参数抽屉再保存,项目就过一次归一。
// 旧实现把空串就地改成 film,于是自动降档会在用户什么都没改的情况下静默失效
// (而这正是技能侧 §2.1 要解决的"指望人不漏不现实")。
func TestSanitizeKeepsUnsetGradeAuto(t *testing.T) {
	var p StageParams // 存量项目:json 里没有 grade 这个键
	p.Sanitize()
	if p.Assemble.Grade != GradeAuto {
		t.Fatalf("未表态的调色档应保住「自动」语义(=%q),实际 %q —— 定死成 film 会让 §2.1 的自动降档失效",
			GradeAuto, p.Assemble.Grade)
	}
}

// animatedStyleFixture 技能侧《翻篇》的真实风格串(实测 data/小说/翻篇/*/plan/plan.json),
// 也就是 §2.1 点名的那一条 —— 它是"动画电影"类,默认 film 会偏重。
const animatedStyleFixture = "ANIMATED-FEATURE CINEMATIC LOOK: a photoreal 3D world (concrete, rain, " +
	"wet asphalt, puddled reflections) with IDEALISED, SEMI-STYLISED animal characters — big clear eyes, " +
	"clean simple muzzles, soft matte fur, high-end animated-feature hero look; matte finish with no oily " +
	"highlights, natural film grain, motivated practical light; no plastic CG sheen, no flat 2D cartoon " +
	"look, no ink outlines, no cel shading"

// realisticStyleFixture 写实类风格串(artstyle.go 记录过的本机真实项目措辞)。
const realisticStyleFixture = "gritty rural Chinese period realism"

// TestEffectiveGradeAutoFollowsStyle §2.1 的主判据:未表态时按 plan.Style 选档 ——
// 动画电影类 → soft(自动降一档),其余 → film;且"自动降档"这个标志**只在真降档时**为真
// (合成入口靠它决定要不要播报)。
func TestEffectiveGradeAutoFollowsStyle(t *testing.T) {
	cases := []struct {
		name  string
		style string
		want  string
		auto  bool
	}{
		{"《翻篇》的真实风格串(动画电影)→ soft", animatedStyleFixture, "soft", true},
		{"单关键词 donghua → soft", "stylized donghua look, hand-drawn line art", "soft", true},
		{"单关键词 anime → soft", "modern anime aesthetic, soft matte finish", "soft", true},
		{"单关键词 animated-feature → soft", "a high-end ANIMATED-FEATURE hero look", "soft", true},
		{"中文写法动画电影 → soft", "动画电影质感,柔和平涂", "soft", true},
		{"写实风格串 → film(不降档)", realisticStyleFixture, "film", false},
		{"风格串为空(老方案)→ film,且不算自动降档", "", "film", false},
		{"风格串缺失但否定式提到动画 → 不降档", "LIVE-ACTION REALISM, no anime look", "film", false},
		{"段内否定式也要挡住(别把「不要动画」读成「要动画」)",
			"cinematic realism, no animated-feature stylisation", "film", false},
		{"只有大写的否定式关键词不算", "no DONGHUA LOOK, photoreal", "film", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, auto := AssembleParams{Grade: GradeAuto}.EffectiveGrade(tc.style)
			if got != tc.want || auto != tc.auto {
				t.Fatalf("风格 %q 应得 (%q, auto=%v),实际 (%q, auto=%v)",
					tc.style, tc.want, tc.auto, got, auto)
			}
		})
	}
}

// TestEffectiveGradeExplicitWins 显式表态的档位**不许被覆盖**(§2.1 的硬要求):
// 即使风格串是动画电影,用户/技能侧选了什么就是什么。
//
// 特别注意显式 film:它是"我就要电影档"的明确选择,不能因为风格是动画就被悄悄改成 soft
// —— 那样用户将**没有任何办法**把动画项目调回 film(界面里选 film 也无效)。
func TestEffectiveGradeExplicitWins(t *testing.T) {
	for _, tier := range []string{"off", "soft", "film", "heavy"} {
		for _, style := range []string{animatedStyleFixture, realisticStyleFixture, ""} {
			got, auto := AssembleParams{Grade: tier}.EffectiveGrade(style)
			if got != tier || auto {
				t.Errorf("显式 %q(风格=%q)被改成了 (%q, auto=%v)—— 显式表态必须优先于风格",
					tier, style, got, auto)
			}
		}
	}
	// 大小写/空白:归一前也要认出来(项目参数可能被手改过,大小写不该改变语义)
	if got, auto := (AssembleParams{Grade: " Heavy "}).EffectiveGrade(animatedStyleFixture); got != "heavy" || auto {
		t.Errorf("显式 \" Heavy \" 应得 (heavy,auto=false),实际 (%q,%v)", got, auto)
	}
}

// TestEffectiveGradeAgreesWithSanitize 同一语义不许两处口径:
// 归一前 / 归一后喂进 EffectiveGrade,结果必须逐字一致(含 auto 标志)。
//
// 这条防的是"保存一次参数就换了档"这类只在特定路径上出现的漂移 ——
// 参数读盘路径**不过** Sanitize(只有保存参数那一处过),所以两条路径必须自洽。
func TestEffectiveGradeAgreesWithSanitize(t *testing.T) {
	for _, in := range []string{"", "auto", " AUTO ", "off", "soft", "film", "heavy", " OFF ", "cinematic", "不知道"} {
		raw := AssembleParams{Grade: in}
		p := StageParams{}
		p.Assemble.Grade = in
		p.Sanitize()
		for _, style := range []string{animatedStyleFixture, realisticStyleFixture} {
			g1, a1 := raw.EffectiveGrade(style)
			g2, a2 := p.Assemble.EffectiveGrade(style)
			if g1 != g2 || a1 != a2 {
				t.Errorf("grade=%q(风格=%s):归一前 (%q,auto=%v) 与归一后 (%q,auto=%v) 口径不一致",
					in, style, g1, a1, g2, a2)
			}
		}
	}
}

// TestDefaultParamsGradeIsAuto 平台默认参数必须把调色开着(用户要的是"别再是 H3 直出的
// 平淡数码味"),但**档位跟着风格走**:默认是 auto,由 EffectiveGrade 落成 soft / film。
//
// 判据分两半:①默认值本身是 auto;②在任何风格下解析出来的都不是 off
// ——"默认开着"这条产品口径不能被自动档悄悄破坏。
func TestDefaultParamsGradeIsAuto(t *testing.T) {
	a := DefaultStageParams().Assemble
	if a.Grade != GradeAuto {
		t.Errorf("默认调色档应为 %q(按风格自动),实际 %q", GradeAuto, a.Grade)
	}
	for _, style := range []string{animatedStyleFixture, realisticStyleFixture, ""} {
		got, _ := a.EffectiveGrade(style)
		if got == "off" || got == "" {
			t.Errorf("默认档在风格 %q 下解析成了 %q —— 调色被静默关掉了", style, got)
		}
	}
}

// TestImportedAnimatedPackLandsOnSoft 把 §2.1 的两半接起来验一遍技能端导入包的真实路径:
// project.json 里 `config.params` 是空的 `{}`(技能端只写 id/name/config)
// → 读盘补平台默认(ensureDefaultParams)→ 合成入口按 plan.Style 解析最终档位。
//
// 两半各自都有测试(TestImportedProjectGetsDefaultParams / TestEffectiveGradeAutoFollowsStyle),
// 但"《翻篇》这类包最终真的落在 soft"只有连起来才成立 —— 而这正是 §2.1 要的结果。
func TestImportedAnimatedPackLandsOnSoft(t *testing.T) {
	proj := &Project{} // 导入包:参数全空
	proj.ensureDefaultParams()
	grade, auto := proj.Config.Params.Assemble.EffectiveGrade(animatedStyleFixture)
	if grade != "soft" || !auto {
		t.Fatalf("《翻篇》式导入包(空参数 + 动画电影风格串)最终应落 (soft, 需播报),实际 (%q, auto=%v)",
			grade, auto)
	}
}
