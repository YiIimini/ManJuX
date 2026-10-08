package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// voice_pitch_test.go 音色核对（voice_check.go）的接线与解析判据。
//
// 【夹具照真机原文写】下面这几段是**真机提交版提示词里逐字抄下来的**（《顺其自然》第01集；
// 用 zz_probe_voice_live_test.go 从真实装配里打出来的，见 docs/配音-女配男音与重复念-2026-10-08.md）。
// 判"音色对不对"的第一步就是**从提交版提示词里读出"这一镜被要求的是什么声线"**，
// 读错这一层，后面量得再准也是白搭。

// 真机镜 5：卡上女声（韩沿），提交版引述里逐字写着卡上原文。
const vchkShot5 = `[Shot 3] At 00:06.500 cut to medium close-up of 韩沿, face right, eyes down. ` +
	`韩沿 (S1) says in Mature mid-range female voice, even and matter-of-fact, sentences ending flat as if she has said them a hundred times: ` +
	`<d>[Chinese]他就拆开,一张都不超过一万。</d> She squares the left edges of both rows with her right palm. Then, without lifting her head: ` +
	`<d>[Chinese]二十七年,一次都没写。</d>`

// 真机镜 6：卡上男声（志愿者）——注意模型自己还留了半句 "a young man's clear, plain,"（平台只管权威声线，画面/引述其余部分不动）。
const vchkShot6 = `Off-screen above the lamp, a young man's clear, plain, 志愿者 (S1) says in Soft young male voice, ` +
	`slightly hoarse from the cold, trailing off when he reads the signature aloud: <d>[Chinese]落款,顺其自然?</d>`

// 真机镜 11：内心音（旁白通道）——说话人靠在引述里点名的角色，平台绑成卡上声线并标 off-screen。
const vchkShot11 = `韩沿's off-screen inner voice, 韩沿 (S3) says off-screen in Mature mid-range female voice, ` +
	`even and matter-of-fact, sentences ending flat as if she has said them a hundred times: <d>[Chinese]桌子换了,窗上有了玻璃。</d>`

// 真机镜 1：同镜两个说话人（无卡的中性声线 + 女卡）。
const vchkShot1 = `Off-screen right, 邮局同事 (S1) says in a neutral, androgynous voice, even-paced and calm: ` +
	`<d>[Chinese]又是那个人?</d> ` +
	`周 (S2) says in Light, low-pitched young female voice, unhurried and clipped, with a soft Ningbo accent and very little upward inflection: ` +
	`<d>[Chinese]十一月底,还能是谁。</d>`

// TestVoiceLinesFromPromptReadsTheSubmittedPrompt 从提交版提示词里读"这一镜被要求什么声线"。
func TestVoiceLinesFromPromptReadsTheSubmittedPrompt(t *testing.T) {
	cases := []struct {
		name      string
		prompt    string
		wantLines int
		speaker   string
		voiceHas  string
		textHas   string
		gender    qc.VoiceGender
	}{
		{"镜 5 女卡", vchkShot5, 2, "韩沿", "Mature mid-range female voice", "他就拆开", qc.GenderFemale},
		{"镜 6 男卡", vchkShot6, 1, "志愿者", "Soft young male voice", "落款", qc.GenderMale},
		{"镜 11 内心音（画外）", vchkShot11, 1, "韩沿", "Mature mid-range female voice", "桌子换了", qc.GenderFemale},
	}
	for _, c := range cases {
		got := VoiceLinesFromPrompt(c.prompt)
		if len(got) != c.wantLines {
			t.Fatalf("%s：读到 %d 句，想要 %d 句（%+v）", c.name, len(got), c.wantLines, got)
		}
		first := got[0]
		if first.Speaker != c.speaker {
			t.Errorf("%s：说话人 = %q，想要 %q", c.name, first.Speaker, c.speaker)
		}
		if !strings.Contains(first.Voice, c.voiceHas) {
			t.Errorf("%s：声线 = %q，想要含 %q", c.name, first.Voice, c.voiceHas)
		}
		// 声线里**不许**带尾随的冒号/空格（否则期望值与卡上文本逐字比对会失配）。
		if strings.HasSuffix(first.Voice, ":") || strings.HasSuffix(first.Voice, " ") {
			t.Errorf("%s：声线尾部没清干净：%q", c.name, first.Voice)
		}
		if !strings.Contains(first.Text, c.textHas) || strings.Contains(first.Text, "[Chinese]") {
			t.Errorf("%s：台词 = %q（应当只留台词本身）", c.name, first.Text)
		}
		if g := qc.GenderOfVoice(first.Voice); g != c.gender {
			t.Errorf("%s：从声线读出的性别 = %q，想要 %q", c.name, g, c.gender)
		}
	}
}

// TestVoiceLinesFromPromptMixedSpeakers 同镜两个说话人（真机镜 1）都要读到 ——
// 这是"同镜男女混合 ⇒ 不判音色"那条判据的输入。
func TestVoiceLinesFromPromptMixedSpeakers(t *testing.T) {
	got := VoiceLinesFromPrompt(vchkShot1)
	if len(got) != 2 {
		t.Fatalf("读到 %d 句，想要 2 句：%+v", len(got), got)
	}
	if got[0].Speaker != "邮局同事" || got[1].Speaker != "周" {
		t.Errorf("说话人不对：%q / %q", got[0].Speaker, got[1].Speaker)
	}
	if g := qc.GenderOfVoice(got[0].Voice); g != qc.GenderUnknown {
		t.Errorf("邮局同事是中性的，不该读出性别（读到 %q）", g)
	}
	if g := qc.GenderOfVoice(got[1].Voice); g != qc.GenderFemale {
		t.Errorf("周是女卡，应读出 female（读到 %q）", g)
	}
}

// TestVoiceLinesFromPromptSkipsNonSpeech 空标签/占位/规则正文里的示例标签都不算台词。
func TestVoiceLinesFromPromptSkipsNonSpeech(t *testing.T) {
	prompt := "规则：`<d>[Chinese]…</d>` 里写台词。\n" +
		"旁白 (S1) says in a natural, clearly articulated Mandarin voice, steady and even: <d>[Chinese]…</d>\n" +
		"旁白 (S1) says in a natural, clearly articulated Mandarin voice, steady and even: <d>[Chinese]无台词</d>\n"
	if got := VoiceLinesFromPrompt(prompt); len(got) != 0 {
		t.Errorf("占位标签不该被当成台词：%+v", got)
	}
}

// TestVoiceLinesFromPromptKeepsModelVoiceWhenUnresolved 认不出说话人的句子保留模型原话 ——
// 那时"模型自己写的声线"就是音频条件，拿它当期望值才是对的（不许猜成卡上某个人）。
func TestVoiceLinesFromPromptKeepsModelVoiceWhenUnresolved(t *testing.T) {
	// 真机镜 9 提交版：平台没能加上逐刀清单、说话人也没被重绑 ⇒ 引述仍是模型写的。
	prompt := `[Shot 2] At 00:02.500 the shot cuts closer, medium close-up on 会长's face. ` +
		`In an old man's slow, warm, certain voice (S1): <d>[Chinese]别找了。</d>`
	// 这条句式**不是平台写的**（没有 " says in "），解析不到 ⇒ 跳过这一句（不猜）。
	if got := VoiceLinesFromPrompt(prompt); len(got) != 0 {
		t.Errorf("平台句式之外的引述不该被解析成权威声线：%+v", got)
	}
}

// TestVoiceRetrySeedsDifferPerRound 每轮重出必须换种子：同一颗种子重出等于念同一遍。
//
// 【这条判据是被真机咬出来的】第一版写的是"固定基数 + 轮次"，而真机那一轮命令行恰好也用了
// 同一个值（20261008）⇒ 重出轮的指纹与上一轮逐字一致 ⇒ 平台判"产物有效,跳过(断点续跑)"，
// **日志写着"已重出 1 轮"，其实一次都没渲**。所以判据必须钉在"与上一轮不同"上，而不是"有值"。
func TestVoiceRetrySeedsDifferPerRound(t *testing.T) {
	// 界面/命令行填了种子（含"恰好等于平台基数"这种真机踩过的巧合）。
	for _, prev := range []int64{1, 20261008, 99999999} {
		next := voiceRetryNextSeed(prev, 0)
		if next == prev {
			t.Errorf("上一轮种子 %d 的重出种子还是它自己 —— 重出会变成空转", prev)
		}
		if next2 := voiceRetryNextSeed(next, 1); next2 == next {
			t.Errorf("第二轮重出种子 %d 与第一轮相同", next)
		}
	}
	// 界面没填（0）时也必须换成非零种子，且两轮不同。
	s0, s1 := voiceRetryNextSeed(0, 0), voiceRetryNextSeed(0, 1)
	if s0 <= 0 || s1 <= 0 || s0 == s1 {
		t.Errorf("没填 seed 时的重出种子不对：%d / %d", s0, s1)
	}
	// ShotSeed 会再加 shot.ID*101 ⇒ 同一轮内不同镜头不同种子。
	if ShotSeed(0, s0, 5, nil) == ShotSeed(0, s0, 6, nil) {
		t.Error("同一轮里两镜的种子相同 —— 抽卡就变成一起抽同一张")
	}
}

// TestClipPathOfSkipsPlaceholders 占位/空路径都不该进音色核对（那没有音轨可量）。
func TestClipPathOfSkipsPlaceholders(t *testing.T) {
	dir := `D:\proj`
	if got := clipPathOf(dir, project.ClipManifest{}); got != "" {
		t.Errorf("空路径应跳过，得到 %q", got)
	}
	// 真机形态：失败镜的清单项长这样（File 指着占位片段、Status=failed、Error 带占位标记）。
	ph := project.ClipManifest{Status: project.ShotFailed, Error: project.PlaceholderMarker + " —— 渲染失败",
		File: "clips/shot_001_placeholder.mp4"}
	if got := clipPathOf(dir, ph); got != "" {
		t.Errorf("占位片段应跳过，得到 %q", got)
	}
	ok := project.ClipManifest{File: "clips/shot_001.mp4"}
	if got := clipPathOf(dir, ok); !strings.HasSuffix(got, "shot_001.mp4") {
		t.Errorf("正常片段应给出绝对路径，得到 %q", got)
	}
}

// TestVoiceCheckIsWiredAtBothEnds 接线门：渲染侧（有界重出）与质检侧（报告）都必须挂着。
//
// 为什么用"读源码断言"这种形式：这两处是**跨阶段接线**，单元测不到；而这类缺陷
// （函数写好了但没人调用）不会报错、也不会让任何既有测试变红 —— 只有显式断言能钉住。
func TestVoiceCheckIsWiredAtBothEnds(t *testing.T) {
	// 渲染侧的接线分在两个文件：阶段循环在 stages_render.go、判据与文案在 voice_check.go。
	render := readSrc(t, "stages_render.go") + readSrc(t, "voice_check.go")
	for _, want := range []string{
		"qualityRetryTargets(ctx, proj, plan, shots, manifest)",
		"语音核对未跑成(不当作通过,也不据此重出)",                                                             // ASR 挂了必须留痕（不静默当成通过）
		"qc.CheckSpeech(sctx, asrTool, p.ASRModelOr(), clip, project.PlanSpokenTexts(&sh))", // 语音判据接进重出
		"maxVoiceRetryPasses",
		"voiceRetryNextSeed(seedOverride, pass)",
		"c.runRenderPass(ctx, proj, only, engineOverride, seedOverride, 0)",
	} {
		if !strings.Contains(render, want) {
			t.Errorf("渲染侧缺少接线：%s", want)
		}
	}
	qcsrc := readSrc(t, "stages_qc_assemble.go")
	for _, want := range []string{
		"voicePitchForQC(ctx, c, proj, plan, &shot, full)",
		"voiceLabel(r.Voice)",
		"voiceFlagged",
	} {
		if !strings.Contains(qcsrc, want) {
			t.Errorf("质检侧缺少接线：%s", want)
		}
	}
	// 报告字段必须落进 qc_report.json（前端与用户都会读它）。
	wantTag := "Voice *VoicePitchReport `json:\"voice,omitempty\"`"
	if !strings.Contains(readSrc(t, "../qc/qc.go"), wantTag) {
		t.Error("qc.Report 里没有 voice 字段 —— 结论只在日志里，报告查不到")
	}
}

// readSrc 读本包（或给定相对路径）的源码，用于接线断言。
func readSrc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读不到 %s：%v", rel, err)
	}
	return string(b)
}
