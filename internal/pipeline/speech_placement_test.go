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
	shot := &project.Shot{ID: 1, Duration: 9, H3Prompt: realShot1Prompt}
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
