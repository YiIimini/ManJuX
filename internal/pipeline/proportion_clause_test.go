package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 技能侧「人体比例锚点」的平台侧处理(清单 §5.1 "别覆盖它")。
//
// 这一条链的价值全在判据上,而它的失效方式是**静默**的:平台多加一句比例,提示词里就
// 有两套比例,模型取中值 —— 画出来既不 Q 版也不真人比例,没有人会报错,只有画面对不上。
// 所以这里把三件事钉死:
//  1. 判据认得技能侧的比例句(英文现行口径 + 中文记述形态),**不误认**平台自己的
//     "照抄参考图的比例"这类身份条款(误认会让平台侧的比例措辞在不该让位时让位);
//  2. 技能侧比例句同现时,平台侧不再追加比例措辞;
//  3. 技能侧比例句不在场时,**行为与现在完全一致**(存量兼容),而且介质/取景条款一条不少。

// skillProportionClause 技能侧现行口径(摘自本机 data/projects/求你们骂我 的真实角色卡
// image_prompt —— 技能侧交付说明里的 HUMAN_PROPORTION_CLAUSE)。
const skillProportionClause = "Proportions must be balanced and idealised: the eye line sits at the vertical middle of the head; " +
	"the eyes are one eye-width apart; the nose is about one third of the face length and the mouth about one fifth of it; " +
	"the chin is about two thirds as wide as the cheekbones; the neck is slim and long; the shoulders are about two head-widths wide; " +
	"the figure is seven and a half heads tall with a slim waist and long legs. " +
	"Never distort these ratios, never lengthen the face and never enlarge the eyes."

// TestHumanProportionClausePresent 判据:认得技能侧比例句,且**不**误认平台自己的身份条款。
//
// 误认的后果不比漏认轻:平台侧的比例措辞会在不该让位时消失(Q 版卡失去"二到三头身"),
// 于是"丑"从一种形态换成另一种形态。
func TestHumanProportionClausePresent(t *testing.T) {
	yes := []struct{ name, prompt string }{
		{"英文现行口径(本机真实角色卡)", "Clean anime-style face, clear-cut jawline, large bright lively eyes. " + skillProportionClause + ". plain pure white seamless background, upper-body framing"},
		{"英文特征短语单条", "the eye line sits at the vertical middle of the head"},
		{"英文特征短语:眼距", "the eyes are one eye-width apart"},
		{"英文特征短语:七个半头", "the figure is seven and a half heads tall"},
		{"中文记述形态(清单 §5.1)", "人体比例：眼线在头部垂直中线；两眼间距＝一个眼宽；鼻长约脸长三分之一；身高七个半头。"},
		{"大小写不敏感", "THE EYE LINE SITS AT THE VERTICAL MIDDLE OF THE HEAD"},
	}
	for _, tc := range yes {
		if !HumanProportionClausePresent(tc.prompt) {
			t.Errorf("%s:应判为「已有技能侧比例句」:%q", tc.name, tc.prompt)
		}
	}
	no := []struct{ name, prompt string }{
		{"空提示词", ""},
		{"纯空白", "   \n\t "},
		{"只有风格词(技能侧旧口径,本条的起因)", "Clean anime-style face, clear-cut jawline, large bright lively eyes, nose and mouth simplified to a few lines"},
		{"平台多视角的身份条款(照抄参考图的比例,不含任何比值)", "body proportions must stay exactly the same as <Picture 1> in every frame"},
		{"平台白底文件的身份条款", "same colours, same pose, same expression and the same body proportions as image 1"},
		{"Q 版类型条款(平台自己的比例措辞,不是技能侧锚点)", "Two-to-three-head-tall proportions, oversized rounded head"},
		{"普通角色卡", "Square jaw, high cheekbones, black hair in a tight martial topknot, plain background, upper-body framing"},
	}
	for _, tc := range no {
		if HumanProportionClausePresent(tc.prompt) {
			t.Errorf("%s:不该判为「已有技能侧比例句」:%q", tc.name, tc.prompt)
		}
	}
}

// humanProj 一个开着的项目(动漫化默认开)+ 给一个风格的方案。
func humanProj(style string) (*project.Project, *project.Plan) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	return proj, &project.Plan{Style: style}
}

// TestSkillProportionClauseSurvivesAssembly 端到端:**不许覆盖它** ——
// 装配后的定妆照提示词里,技能侧比例句逐字仍在,而平台自己那套比例措辞不进同一段。
//
// 风格特意用"Q版呆萌"(hasQ 分支):这是平台侧**唯一**会往提示词里塞比例词的地方
// ("cute chibi proportions"),也是"同一段里两套比例"最容易发生的组合。
func TestSkillProportionClauseSurvivesAssembly(t *testing.T) {
	proj, plan := humanProj("Q版呆萌, 国漫二维")
	ch := project.Character{
		ID: "c1", Name: "沈砚",
		ImagePrompt: "Clean anime-style face, clear-cut jawline. " + skillProportionClause +
			". plain pure white seamless background, upper-body framing, front-facing",
	}
	got := PortraitPromptForProject(proj, plan, ch)

	// ① 技能侧比例句里的**脸部/上半身锚点逐字保留**(证明不是被剥剩下的残句)
	for _, want := range []string{
		"the eye line sits at the vertical middle of the head",
		"the eyes are one eye-width apart",
		"about one third of the face length",
		"two thirds as wide as the cheekbones",
		"the neck is slim and long",
		"the shoulders are about two head-widths wide",
		"Never distort these ratios",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("技能侧比例句被改动了,缺少「%s」:\n%s", want, got)
		}
	}
	// ①b **整身**那一条是有意剥离的(2026-09-26 修正):这张卡自己写着 `upper-body framing`,
	//     半身幅里"七头半"既不可验证又与取景打架,实测出图就是全身立绘 —— 64 镜里 61 镜
	//     是近景/中景/特写,喂全身像必然漂。剥的是**取景一致性**,不是技能侧的脸部口径。
	if strings.Contains(strings.ToLower(got), "seven and a half heads") {
		t.Errorf("半身定妆照里不该留全身口径:\n%s", got)
	}
	// ①c 剥离不许留下**平台自造**的措辞:早期实现只删短语,把这一句剥成
	//     `the figure is tall with a slim waist and long legs`,等于往技能侧口径里塞了个 `tall`。
	if strings.Contains(got, "the figure is tall with") {
		t.Errorf("剥离整身口径不许留下自造残迹:\n%s", got)
	}
	// ② 平台自己那套比例措辞不得同现
	if strings.Contains(got, "cute chibi proportions") {
		t.Errorf("卡里已有技能侧比例句,平台不该再追加自己的比例措辞:\n%s", got)
	}
	// ③ 平台侧的**职责条款一条都不能少**(让位只让比例,不让介质/取景/忠实度)
	for _, want := range []string{
		MediumMarker,        // 介质块
		"IDENTITY FIDELITY", // 身份忠实度块
		// 取景(2026-09-27 起由平台**按画幅**算:竖幅 → 胸像 + 头顶留白;
		// 卡里那句写死的 `upper-body framing` 会被剥掉,见 PortraitFramingClause)
		"chest-up portrait",
		"front-facing",
		"plain pure white seamless background",
		"HAIR EXACTLY AS BRIEFED", // 发型纪律
	} {
		if !strings.Contains(got, want) {
			t.Errorf("让位不该牵连职责条款,提示词缺「%s」:\n%s", want, got)
		}
	}
	// ④ 人类**不**走取景重写:那是"别覆盖它"的结构性保证(重写会把取景短语剥掉再追加类型取景)
	if EntityProfileOf(KindHuman).RewriteSheetFraming {
		t.Error("人类定妆照不得走取景重写(会把 LLM 的人像取景剥掉,并可能牵连卡上的比例句)")
	}
}

// TestStyleExpandUnchangedWithoutSkillClause 存量兼容:卡里**没有**技能侧比例句时,
// 行为与改动前完全一致 —— Q 版的平台侧比例措辞照旧在,介质块照旧在。
func TestStyleExpandUnchangedWithoutSkillClause(t *testing.T) {
	// 无上下文入口(旧签名)与空提示词的新入口等价
	old := styleExpand("Q版呆萌, 国漫二维", true, KindHuman)
	same := styleExpandFor("", "Q版呆萌, 国漫二维", true, KindHuman)
	if old != same {
		t.Errorf("无上下文入口必须与新入口(空提示词)完全等价:\n%q\n%q", old, same)
	}
	if !strings.Contains(old, "cute chibi proportions") {
		t.Errorf("存量兼容:没有技能侧比例句时,hasQ 的比例词应照旧保留:\n%s", old)
	}
	// Q 版类型块:没有技能侧比例句 → 平台的"二到三头身"照旧在,介质块也在
	chibi := styleExpandFor("", "Q版呆萌", true, KindChibi)
	// 判据用 **Q 版自己的介质块标记**（2026-09-28：Q 版介质块从"电影级写实"换成
	// "可爱风格化 3D"，标记也跟着换了 —— 这条测试的意图是"介质条款必须留下"，
	// 不是"必须是写实那一块"）。
	for _, want := range []string{"Two-to-three-head-tall proportions", ChibiMediumMarker, "CHIBI FIDELITY"} {
		if !strings.Contains(chibi, want) {
			t.Errorf("Q 版类型块存量行为被改变,缺「%s」:\n%s", want, chibi)
		}
	}
}

// TestPlatformProportionStandsDownForChibiWithSkillAnchor Q 版卡若带技能侧人体比例句:
// 让位的是**比例**,介质与忠实度块照旧 —— 这是"整块丢掉"与"精确让位"的分界。
func TestPlatformProportionStandsDownForChibiWithSkillAnchor(t *testing.T) {
	withAnchor := styleExpandFor("chibi card. "+skillProportionClause, "Q版呆萌", true, KindChibi)
	if strings.Contains(withAnchor, "Two-to-three-head-tall proportions") {
		t.Errorf("已有技能侧人体比例句时,平台侧的比例措辞必须让位:\n%s", withAnchor)
	}
	// 「让位只让比例」：介质块与忠实度块必须留下。判据跟着 2026-09-28 的介质变更走
	// （写实块 → Q 版自己的可爱风格化 3D 块），意图不变。
	for _, want := range []string{ChibiMediumMarker, "CHIBI FIDELITY", "toy-like", "big head on a small body"} {
		if !strings.Contains(withAnchor, want) {
			t.Errorf("让位只让比例,介质/忠实度条款必须留下,缺「%s」:\n%s", want, withAnchor)
		}
	}
	// 端到端:Q 版装配点同样让位(取景仍是 Q 版的"大头小身")
	proj, plan := humanProj("Q版呆萌")
	got := PortraitPrompt(PortraitSubject{
		Kind: KindChibi, Name: "迷你沈砚",
		ImagePrompt: "a mini version. " + skillProportionClause + ". plain background",
		FacesOn:     true,
	}, plan)
	_ = proj
	if strings.Contains(got, "Two-to-three-head-tall proportions") {
		t.Errorf("端到端:Q 版装配也应让位:\n%s", got)
	}
	if !strings.Contains(got, "big head and small body") {
		t.Errorf("Q 版取景条款(大头小身)是取景职责,不该被牵连:\n%s", got)
	}
}

// TestPortraitProportionCheckable 比例自检的可测范围(接 qc 侧的自检):
// 人照测;器物/灵宠/妖兽/无实体没有人体比例可言;Q 版默认不测(它故意就是二到三头身),
// 除非那张卡自带技能侧的人体比例锚点。
func TestPortraitProportionCheckable(t *testing.T) {
	if !PortraitProportionCheckable(KindHuman, "") {
		t.Error("人必须做比例自检")
	}
	for _, k := range []EntityKind{KindObject, KindSpiritPet, KindBeast, KindIncorporeal} {
		if PortraitProportionCheckable(k, "") {
			t.Errorf("%s 没有人体比例可言,不该做比例自检", EntityKindLabel(k))
		}
	}
	if PortraitProportionCheckable(KindChibi, "") {
		t.Error("Q 版是故意的二到三头身,用人体的锚点去量只会得到假告警")
	}
	if !PortraitProportionCheckable(KindChibi, skillProportionClause) {
		t.Error("带着技能侧人体比例句的 Q 版卡,契约就是人体比例 —— 照测")
	}
}
