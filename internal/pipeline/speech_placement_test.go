package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// realShot1Prompt 真机第01集镜头 1 的 h3_prompt（2026-10-08 08:24 落盘的计划，**原文照抄**：
// 只去掉与分刀/台词无关的中段，保留 subject_definitions 头部、三刀正文、soundscape 与音乐段）。
//
// 这一镜就是"配音第一个分镜就不对"的现场：三刀、只有后两刀有台词，而实际音轨是三段人声
// （第 1 句被提前讲进**没有台词**的开场刀里，中间那刀自编了一句假中文）。
const realShot1Prompt = `subject_definitions:
<Subject 1> is 祁七, female machine-frame unit U-80001: pale oval face, blue-grey almond eyes, small upturned nose, orange-red M-shaped lip, porcelain-white skin divided from her white armour by thin black seams, waist-length straight centre-parted hair, side strands over the ears, a floating brass halo lit at four of six points.
<Subject 2> is the bay 归墟环维护舱主舱: a floor of palm-sized mirror cells, four walls of a thousand metal tool drawers; identified by picture content, never by counting indexes.
The scene is the LAST attached picture.

summary:
[reference generation] Cold 3D CG inside 归墟环维护舱主舱: 祁七 rises from the mirror-tile floor while the bay speaker declares her steel stamp non-existent.

detailed_description:
Cinematic, 3D CG, guofeng sci-fi realism: a low-saturation cold-white and cyan-grey palette.

[Shot 1] Ultra-wide establishing shot, eye-level deep perspective down the bay 归墟环维护舱主舱: mirror cells to the far wall, three rows of cold-white work lamps with only the right-hand row alive, the rest dim blue-grey half-light. 祁七 lies curled at the exact centre of the grid, hair fanned over the tiles, halo turning. Static Shot; low hum, drawer walls ticking.

[Shot 2] At 00:02.500 the shot cuts to a medium shot over her shoulder as she drives up onto one knee, left palm flat on a grid seam, right hand shut around something dark; hair slides off the tiles, armour plates knock once, the halo pulses. The camera Push In with small amplitude at slow speed. 舱内系统 (S1) says off-screen in a flat, sexless synthetic voice, even-paced and breathless: <d>[Chinese]硬件钢印 U-80001。</d>

[Shot 3] At 00:05.000 the shot cuts to a medium close on 祁七 standing straight at the grid centre, arms at her sides, chin level, gaze crossing the dark lamp rows, expression unchanged, the halo steadies. The camera Push In with small amplitude at slow speed and stops. 舱内系统 (S1) says off-screen in a flat, sexless synthetic voice, even-paced and breathless: <d>[Chinese]该编号不存在，予以静默。</d>

overall_soundscape:
A low electrical hum fills the bay, cut by cooling ticks from the drawer walls and the scrape of her armour over the mirror tiles; the speaker's chime hangs above it.

non_diegetic_music:
N/A
`

// TestSpeechPlacementClauseMarksTheSilentCut 真机那一镜：**没有台词的开场刀必须被点名**，
// 两句台词必须各自绑在它自己那一刀上（时间串与原文逐字一致）。
func TestSpeechPlacementClauseMarksTheSilentCut(t *testing.T) {
	// Dialogue 也要照真机填上：这一镜计划里就是两句（`plan.json` 的 shots[0].dialogue），
	// 装配时逐刀清单要拿它当对账的权威（见 EnsureSpeechPlacementClause 的安全阀）。
	shot := &project.Shot{ID: 1, Duration: 9, H3Prompt: realShot1Prompt, Dialogue: []project.Line{
		{Speaker: "舱内系统", Text: "硬件钢印 U-80001。"},
		{Speaker: "舱内系统", Text: "该编号不存在，予以静默。"},
	}}
	got := EnsureSpeechPlacementClause(realShot1Prompt, shot)
	if !strings.Contains(got, speechPlacementMarker) {
		t.Fatal("逐刀的人声位置条款没进提示词")
	}
	if n := strings.Count(got, speechPlacementMarker); n != 1 {
		t.Fatalf("条款出现 %d 次（应恰好 1 次）", n)
	}
	// 三刀都要点到名，时间串照抄原文。
	for _, want := range []string{"cut into 3 pieces", "heard in exactly 2", "the opening of the shot",
		"the cut at At 00:02.500", "the cut at At 00:05.000"} {
		if !strings.Contains(got, want) {
			t.Errorf("逐刀清单里缺 %q\n%s", want, tailOf(got, 1400))
		}
	}
	// 开场那一刀必须被标成"一点人声都没有"—— 这正是真机被塞进第一句的那一刀。
	if n := strings.Count(got, "NO speech of any kind"); n != 1 {
		t.Errorf("「NO speech」的刀数应为 1（开场刀），实得 %d", n)
	}
	// 两句台词各自出现在自己那一刀的清单行里，且原文逐字。
	i2 := strings.Index(got, "the cut at At 00:02.500")
	i3 := strings.Index(got, "the cut at At 00:05.000")
	line1, line2 := "「硬件钢印 U-80001。」", "「该编号不存在，予以静默。」"
	if !strings.Contains(got[i2:i3], line1) {
		t.Errorf("第 1 句没绑在 00:02.500 那一刀上\n%s", got[i2:i3])
	}
	if !strings.Contains(got[i3:], line2) {
		t.Errorf("第 2 句没绑在 00:05.000 那一刀上\n%s", got[i3:])
	}
	if strings.Contains(got[i2:i3], line2) || strings.Contains(got[i3:], line1) {
		t.Error("两句台词被写到同一刀里了")
	}
	// 幂等：装配点会被调用两次（渲染 + 2K/重绘/音频驱动）。
	twice := EnsureSpeechPlacementClause(got, shot)
	if twice != got {
		t.Error("已含条款的提示词应原样返回（幂等）")
	}
}

// TestSpeechPlacementClauseSkipsSingleCut 只有一刀的镜头不加条款（整镜就是一个时间窗，
// SPOKEN LINES ONLY 已经覆盖）—— 免得每镜尾巴都多背一段没信息量的清单。
func TestSpeechPlacementClauseSkipsSingleCut(t *testing.T) {
	single := "detailed_description:\nCold 3D CG.\n\n[Shot 1] At 00:00.000 the shot opens. 舱内系统 says: <d>[Chinese]读我的钢印。</d>\n"
	shot := &project.Shot{ID: 1, Duration: 5, H3Prompt: single}
	if got := EnsureSpeechPlacementClause(single, shot); got != single {
		t.Errorf("单刀镜不该加逐刀清单，实得：\n%s", tailOf(got, 1400))
	}
}

// TestSpeechPlacementClauseAllSilent 没有台词的镜（多刀）：每一刀都标 NO speech，不写任何台词。
func TestSpeechPlacementClauseAllSilent(t *testing.T) {
	silent := "detailed_description:\nCold 3D CG.\n\n[Shot 1] At 00:00.000 the bay stands empty. Static Shot.\n\n" +
		"[Shot 2] At 00:03.000 the camera drifts left. Slow Pan.\n"
	shot := &project.Shot{ID: 2, Duration: 7, H3Prompt: silent}
	got := EnsureSpeechPlacementClause(silent, shot)
	if !strings.Contains(got, "heard in exactly 0") {
		t.Errorf("没有台词的镜应写明 0 段有人声：\n%s", tailOf(got, 1400))
	}
	if n := strings.Count(got, "NO speech of any kind"); n != 2 {
		t.Errorf("两刀都该标 NO speech，实得 %d\n%s", n, tailOf(got, 1400))
	}
	if strings.Contains(got, "「") {
		t.Error("没有台词时不该在清单里编出台词")
	}
}

// TestSpeechPlacementClauseReachesTheRenderedPrompt 装配链必须真的调用它 ——
// 条款再好，不在发给模型的提示词里等于没有（本仓对"看着接上了、其实没到"的老账）。
func TestSpeechPlacementClauseReachesTheRenderedPrompt(t *testing.T) {
	proj, plan, shot := specFixture()
	shot.Dialogue = []project.Line{{Speaker: "沈砚", Text: "读我的钢印。"}}
	shot.H3Prompt = "[Shot 1] At 00:00.000 establishing. Static Shot.\n\n" +
		"[Shot 2] At 00:02.500 he lifts his head. 沈砚 (S1) says: <d>[Chinese]读我的钢印。</d>\n"
	got := ShotRenderPrompt(proj, plan, &shot)
	if !strings.Contains(got, speechPlacementMarker) {
		t.Fatal("装配链没有调用 EnsureSpeechPlacementClause —— 逐刀人声位置又丢了")
	}
	if !strings.Contains(got, "the cut at At 00:00.000 — NO speech of any kind") {
		t.Errorf("开场那一刀（无台词）没被点名：\n%s", tailOf(got, 1400))
	}
}

// realShot1LastCutWithClauses 真机第 01 集**镜头 1 实际提交给模型**的那一段（2026-10-08 12:06
// 那一轮渲染，取自本机 ComfyUI `/history` 的工作流文本，逐字照抄；只把中段与音频段省掉）：
//
//	① 第四刀正文（`[Shot 4] At 00:06.800 …`）里只有**一句**真台词；
//	② 后面跟着装配点自己加的条款，条款正文里**引用 `<d>…</d>` 当例子** ——
//	   就是这三处引用把逐刀清单数成了「这一刀 4 行」。
const realShot1LastCutWithClauses = `[Shot 4] At 00:06.800 camera holds on 周 as she answers without turning, early-thirties ` +
	`woman's voice, 周 (S2) says in Light, low-pitched young female voice, unhurried and clipped, with a soft Ningbo ` +
	`accent and very little upward inflection: <d>[Chinese]十一月底,还能是谁。</d> Outside, fine sleet strikes bare ` +
	`plane-tree branches and melts on wet bark. No other text appears in frame; no watermarks, no logos, no subtitles.` + "\n\n" +
	`SCENE LIGHTING (scene-level, mandatory): every shot in "老城邮局营业厅" shares ONE light state — 清晨七点四十,内景.` + "\n\n" +
	`MANDARIN ONLY (mandatory language rule): every spoken line and voiceover MUST be Mandarin Chinese (普通话), ` +
	`matching the [Chinese] tag inside each <d>…</d>; no English, no Japanese, no invented or gibberish speech.` + "\n\n" +
	`SPOKEN LINES ONLY (MANDATORY):` + "\n" +
	`the ONLY human speech in this shot is the line or lines enclosed in <d>…</d> tags. Say each of them exactly once, ` +
	`verbatim, in the order written.` + "\n" +
	`(中文对照：本镜**唯一**的人声就是 <d>…</d> 里的那几句台词 —— 逐字、按顺序、**每句只念一遍**。)` + "\n"

// realShot1HeadWithCuts 同一镜的前三刀（真机原文：开场刀无台词，第二、三刀各一句）。
const realShot1HeadWithCuts = `[Shot 1] Ultra-wide deep establishing shot, 07:40, whole service hall: long counter near left to ` +
	`far right, green mail slots and round clock on back wall. 周 stands behind the counter. Static Shot.` + "\n" +
	`[Shot 2] At 00:02.500 cut to close-up: envelope corner flat on counter, red seal impression slightly smeared.` + "\n" +
	`[Shot 3] At 00:05.000 cut to medium close-up from counter's right. 邮局同事 (S1) says in a neutral, androgynous ` +
	`voice, even-paced and calm: <d>[Chinese]又是那个人?</d>` + "\n"

// TestSpeechPlacementCountsOnlyRealLines 逐刀清单**只数真台词**：条款正文里引用的 `<d>…</d>`（示例）不算。
//
// 【真机代价（2026-10-08）】这一镜实际提交的清单写着
// 「the cut at At 00:06.800 — 4 line(s), each spoken exactly once inside THIS piece:「十一月底,还能是谁。」/「…」/「…」/「…」」
// —— 真实只有 1 行。ASR 的实测结果就是模型把空出来的格子填满：镜 1「又是那个人?」念了两遍、
// 镜 5 凭空多出一句「或是血」（镜 5 的清单同样写着「5 line(s)」而真实只有 2 行）。
func TestSpeechPlacementCountsOnlyRealLines(t *testing.T) {
	shot := &project.Shot{ID: 1, Duration: 9, Dialogue: []project.Line{
		{Speaker: "邮局同事", Text: "又是那个人?"},
		{Speaker: "周", Text: "十一月底,还能是谁。"},
	}}
	got := EnsureSpeechPlacementClause(realShot1HeadWithCuts+realShot1LastCutWithClauses, shot)
	if !strings.Contains(got, speechPlacementMarker) {
		t.Fatal("逐刀清单没进提示词")
	}
	if strings.Contains(got, "「…」") {
		t.Errorf("清单里把 `<d>…</d>` 这种**示例标签**当成台词了 —— 模型会去填这些空格子：\n%s", tailOf(got, 1200))
	}
	last := got[strings.Index(got, "the cut at At 00:06.800"):]
	if !strings.Contains(last, "1 line(s)") {
		t.Errorf("最后一刀应写明 1 行（真实只有「十一月底,还能是谁。」一句）：\n%s", tailOf(last, 600))
	}
	if strings.Contains(last, "4 line(s)") || strings.Contains(last, "2 line(s)") {
		t.Errorf("最后一刀的行数被数多了（含示例标签）：\n%s", tailOf(last, 600))
	}
	if n := strings.Count(got, "heard in exactly 2"); n != 1 {
		t.Errorf("三刀里只有两刀有人声（开场刀安静）：实得 %d 处「heard in exactly 2」", n)
	}
}

// TestSpeechPlacementWritesEvenWhenPlanCountsDiffer 逐刀清单**不再因为「对不上账」而丢掉**。
//
// 【2026-10-10 语义反转（深度排查，用户「配音老是有问题」）】旧实现是"数出来的真句数与方案对不上
// 就不加清单"（当时叫安全阀）。真机实测证明它装反了：对不上恰恰说明数据坏掉（归档第01集 17 镜里
// 7 镜对不上），而那几镜正是最需要「台词只在这一刀念」这条约束的 —— 更糟的是同一个坏数据还会让
// 「整镜静音」条款被写上，于是提示词自相矛盾。现在：**清单一律按正文写**（正文才是模型读到的），
// 对账只用来**如实记账 + 播报**（记账在唯一装配点 ShotRenderPrompt，见下面两条）。
func TestSpeechPlacementWritesEvenWhenPlanCountsDiffer(t *testing.T) {
	// 提示词里有 2 句 <d>，而方案的 dialogue 只有 1 句 ⇒ 对不上。
	prompt := "[Shot 1] At 00:00.000 opening. Static Shot.\n" +
		"[Shot 2] At 00:02.500 a. 沈砚 (S1) says: <d>[Chinese]读我的钢印。</d>\n" +
		"[Shot 3] At 00:05.000 b. 沈砚 (S1) says: <d>[Chinese]这句不在方案里。</d>\n"
	shot := &project.Shot{ID: 1, Duration: 9, Dialogue: []project.Line{{Speaker: "沈砚", Text: "读我的钢印。"}}}
	if got := EnsureSpeechPlacementClause(prompt, shot); !strings.Contains(got, speechPlacementMarker) {
		t.Errorf("对不上账也必须写逐刀清单 —— 丢掉它等于把这镜「台词只在这一刀念」的约束拆了：\n%s", tailOf(got, 600))
	}
	// 对得上时当然也要写（正常路径不许被这个改动堵死）。
	shot.Dialogue = append(shot.Dialogue, project.Line{Speaker: "沈砚", Text: "这句不在方案里。"})
	if got := EnsureSpeechPlacementClause(prompt, shot); !strings.Contains(got, speechPlacementMarker) {
		t.Error("对得上时应该加清单")
	}
}

// TestSpeechTextMismatchIsAccounted 对不上的事件要**如实记账**（判据源是正文，结构字段只对账）。
//
// 夹具＝真机那份坏方案的形态：正文三句 `<d>`，而 `dialogue` 的词全是空串。
func TestSpeechTextMismatchIsAccounted(t *testing.T) {
	proj, plan, shot := specFixture()
	shot.H3Prompt = "detailed_description:\nGUOFENG. [Shot 1] a. 祁七 (S2) says: <d>[Chinese]别碰那条链。</d> " +
		"<Subject 1> (S2) says: <d>[Chinese]凭它还在响。</d>\n" +
		"[Shot 2] At 00:02.500 b. 白手套 (S1) says: <d>[Chinese]一台散件，凭什么说话。</d>\n"
	shot.Dialogue = []project.Line{{Speaker: "祁七"}, {Speaker: "白手套"}, {Speaker: "祁七"}} // 词全空 = 真机那份坏方案
	_ = ShotRenderPrompt(proj, plan, &shot)
	if plan.SpeechTextMismatch != 1 {
		t.Errorf("正文 3 句 vs 结构 0 句 ⇒ 必须记 1 次对不上，实得 %d", plan.SpeechTextMismatch)
	}
	if len(plan.SpeechTextMismatchShots) != 1 || plan.SpeechTextMismatchShots[0] != shot.ID {
		t.Errorf("要记下**是哪一镜**（播报时点名），实得 %v", plan.SpeechTextMismatchShots)
	}
	// 账对得上时不许误报（夹具照真实形态：正文里的 <d> 与 dialogue 逐字一致）。
	proj2, plan2, _ := specFixture()
	ok := project.Shot{ID: 9, H3Prompt: "detailed_description:\n[Shot 1] 沈砚 (S1) says: <d>[Chinese]读我的钢印。</d>\n",
		Dialogue: []project.Line{{Speaker: "沈砚", Text: "读我的钢印。"}}}
	_ = ShotRenderPrompt(proj2, plan2, &ok)
	if plan2.SpeechTextMismatch != 0 {
		t.Errorf("账对得上时不该报，实得 %d", plan2.SpeechTextMismatch)
	}
}

// TestSpeechTextMismatchIsReported 渲染收尾必须把这件事播出来（不许只记账、静默）。
func TestSpeechTextMismatchIsReported(t *testing.T) {
	src := readSrc(t, "stages_render.go")
	for _, want := range []string{"plan.SpeechTextMismatch > 0", "shotIDsText(plan.SpeechTextMismatchShots)", "台词账对不上"} {
		if !strings.Contains(src, want) {
			t.Errorf("渲染收尾少了播报 %q —— 对不上就等于数据坏了，用户有权知道", want)
		}
	}
	// 旧字段名必须已经不在了（免得两处各说一套）。
	if strings.Contains(src, "SpeechPlacementSkipped") {
		t.Errorf("渲染阶段还在用旧字段 SpeechPlacementSkipped（语义已换成 SpeechTextMismatch）")
	}
}
