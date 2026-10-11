package qc

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/audio"
	"github.com/YiIimini/ManJuX/internal/ff"
)

// pitch_lines.go 同镜里有**多个说话人**时的**逐句**音色判定。
//
// 【为什么要有（2026-10-11，用户「配音老是有问题」→「还没有做完的继续做」）】整镜判基频时，同镜
// 既有男声又有女声（或既有写性别的角色、也有中性声线）⇒ 整镜中位数把两个人的基频搅在一起 ⇒ 只能
// **不判**（`MeasureVoicePitch` 的 mixed / neutral 两个分支，那是 2026-10-08 定下的边界）。
// 真机第01集里正好有两镜落在缝里 —— 镜 9（星澜＋小狗）、镜 13（星澜＋星兽残魂）—— **一声都没验过**：
// 用户的痛点是「声不对人」，而这两镜恰恰是"人最多"的两镜。
//
// 现在按**每一句自己的时间窗**量：ASR 的逐词时间轴（`asr.AlignLines`，真机测过）把几句台词的时间
// 分开，逐句取中位数、逐句与那句的权威声线对。**一个字不猜**：对不上台词、窗太短、落在男女重叠带的
// 句子一律**不判**并如实写明（三态：通过 / 判负 / 不判）。
//
// 【只在 QC 时间接得上（有词流时）】渲染期的音色核对（`voice_check.go` 的 qualityRetryTargets）跑在
// **片段刚渲完、ASR 还没转写**的时候 —— 那时没有词流，逐句归属做不了，只能沿用整镜那套。
// 这不是偷懒：没有证据就不判；QC 阶段会把结论摆出来，返工由用户/重出决定。

// voiceLineMinSec 单句要判，可靠基频至少要覆盖这么久。
//
// 比整镜的 0.6 秒松（一句话本来就短），但**不能没有**：一两个音节的中位数没有统计意义。
const voiceLineMinSec = 0.3

// voiceLineMinRatio 这一句与 ASR 词流的对齐命中比例下限：低于它就不敢认这一句（不判）。
//
// 为什么要有它：逐句归属**认错句**比不判更糟 —— 会把另一个人的基频算到这个人头上，
// 变成"假告警"（而假告警会触发重出，白烧 GPU）。
const voiceLineMinRatio = 0.5

// genderVerdict 用**同一套带位与措辞**判一段人声（整镜与逐句都走它，改阈值只需改一处）。
//
// who 是给人看的主语前缀（整镜传空串；逐句传「星澜」这一句：）。
// 返回：判负的结论 / 不判的原因 —— 两者都为空 ＝ 这一段的音色与卡上相符。
func genderVerdict(expected VoiceGender, expectedVoice string, medianHz, voicedSec float64, who string) (*PitchFinding, string) {
	if medianHz >= VoiceMaleMaxHz && medianHz < VoiceFemaleMinHz {
		return nil, fmt.Sprintf("%s实测中位基频 %.0f Hz 落在男女重叠带（%.0f–%.0f Hz），不判音色",
			who, medianHz, VoiceMaleMaxHz, VoiceFemaleMinHz)
	}
	switch {
	case expected == GenderFemale && medianHz < VoiceMaleMaxHz:
		return &PitchFinding{Kind: "voice-gender", Detail: fmt.Sprintf(
			"%s卡上写的是**女声**（%s），念出来却是男声带（中位基频 %.0f Hz < %.0f Hz，可靠基频 %.1fs）—— "+
				"提示词里这条声线是逐字写对的，H3 没照做；换种子重出这一镜",
			who, expectedVoice, medianHz, VoiceMaleMaxHz, voicedSec)}, ""
	case expected == GenderMale && medianHz >= VoiceFemaleMinHz:
		return &PitchFinding{Kind: "voice-gender", Detail: fmt.Sprintf(
			"%s卡上写的是**男声**（%s），念出来却是女声带（中位基频 %.0f Hz ≥ %.0f Hz，可靠基频 %.1fs）—— "+
				"提示词里这条声线是逐字写对的，H3 没照做；换种子重出这一镜",
			who, expectedVoice, medianHz, VoiceFemaleMinHz, voicedSec)}, ""
	}
	return nil, ""
}

// voiceExpectation 从这几句声线里抽出"整镜口径"的期望：该是什么性别、那句权威声线原文，
// 以及这一镜是不是"整镜基频分不开"（男女混合 / 混着中性声线）。
//
// 抽成一处是因为它有**两个消费方**：整镜判据（MeasureVoicePitch 决定要不要判）与调用方
// （决定要不要退到**逐句**那条路，见 VoiceLinesNeedPerLine）。
func voiceExpectation(lines []VoiceLine) (expected VoiceGender, expectedVoice string, mixed, neutral bool) {
	for _, l := range lines {
		g := GenderOfVoice(l.Voice)
		if g == GenderUnknown {
			neutral = true
			continue
		}
		if expected == GenderUnknown {
			expected, expectedVoice = g, strings.TrimSpace(l.Voice)
			continue
		}
		if expected != g {
			mixed = true
		}
	}
	return expected, expectedVoice, mixed, neutral
}

// VoiceLinesNeedPerLine 这一镜的声线是不是"整镜基频分不开"那一类（男女混合 / 混着中性声线）。
//
// VoiceLinesNeedPerLine 这一镜的声线是不是"整镜基频分不开"那一类（男女混合 / 混着中性声线）
// **且**至少有一句写得清性别（否则逐句也只是逐句地"不判"，白解一次码）。
//
// 调用方（QC 阶段）据此决定要不要退到**逐句**那条路：`MeasureVoicePitchMixed`（要 ASR 词流）。
func VoiceLinesNeedPerLine(lines []VoiceLine) bool {
	expected, _, mixed, neutral := voiceExpectation(lines)
	return expected != GenderUnknown && (mixed || neutral)
}

// windowMedian 取 [start,end] 这一段时间窗里可靠基频的中位数与覆盖时长。
//
// 时间窗来自 ASR 逐词对齐：帧号 = 秒 / Hop（Hop 是 10ms，见 audio.PitchHopSec）。
func windowMedian(tr *audio.PitchTrack, start, end float64) (median, voicedSec float64) {
	if tr == nil || len(tr.F0) == 0 || tr.Hop <= 0 || end <= start {
		return 0, 0
	}
	i0 := int(start / tr.Hop)
	i1 := int(end / tr.Hop)
	if i0 < 0 {
		i0 = 0
	}
	if i1 > len(tr.F0) {
		i1 = len(tr.F0)
	}
	if i1 <= i0 {
		return 0, 0
	}
	var vs []float64
	for i := i0; i < i1; i++ {
		if tr.F0[i] > 0 {
			vs = append(vs, tr.F0[i])
		}
	}
	if len(vs) == 0 {
		return 0, 0
	}
	sort.Float64s(vs)
	return vs[len(vs)/2], float64(len(vs)) * tr.Hop
}

// PerLineVoiceFindings 逐句判音色：只判**这一句自己的时间窗**里的基频。
//
// spans 与 lines 同序（spans[i] 是第 i 句的实测时间窗，来自 asr.AlignLines）。
// 返回：判负的结论 / 真的比过几句 / 每一句"没判"的原因（与 lines 同序，判过的为空串）。
func PerLineVoiceFindings(tr *audio.PitchTrack, lines []VoiceLine, spans []asr.Span) (findings []PitchFinding, judged int, skips []string) {
	skips = make([]string, len(lines))
	for i, l := range lines {
		who := "这一句："
		if sp := strings.TrimSpace(l.Speaker); sp != "" {
			who = fmt.Sprintf("「%s」这一句：", sp)
		}
		g := GenderOfVoice(l.Voice)
		switch {
		case g == GenderUnknown:
			skips[i] = who + "声线没写性别（中性/童声/系统音），不判"
			continue
		case i >= len(spans):
			skips[i] = who + "ASR 词流里没对齐到这一句（不猜是哪句），不判"
			continue
		}
		sp := spans[i]
		if sp.End <= sp.Start {
			skips[i] = who + "对齐出来的时间窗是空的，不判"
			continue
		}
		if sp.Ratio > 0 && sp.Ratio < voiceLineMinRatio {
			skips[i] = fmt.Sprintf("%s与 ASR 词流只对齐上 %.0f%%（低于 %.0f%%），不敢认这一句，不判",
				who, sp.Ratio*100, voiceLineMinRatio*100)
			continue
		}
		median, voiced := windowMedian(tr, sp.Start, sp.End)
		if voiced < voiceLineMinSec {
			skips[i] = fmt.Sprintf("%s这一句里可靠基频只覆盖 %.2f 秒（不足 %.1f 秒），不判", who, voiced, voiceLineMinSec)
			continue
		}
		f, why := genderVerdict(g, l.Voice, median, voiced, who)
		if f != nil {
			findings = append(findings, *f)
			judged++
			continue
		}
		if why != "" {
			skips[i] = why
			continue
		}
		judged++ // 比过、且与卡上相符
	}
	return findings, judged, skips
}

// MeasureVoicePitchMixed 同镜多说话人时的**逐句**音色判定（需要这一镜的 ASR 词流）。
//
// 只判**能认出来的那几句**；全都判不了时返回 Checked=false + Skipped（把"没验"与"验过相符"分开）。
func MeasureVoicePitchMixed(ctx context.Context, f *ff.FF, clip, asrPath string, lines []VoiceLine) *VoicePitchReport {
	if len(lines) == 0 {
		return VoicePitchSkipped("本镜没有需要核对音色的台词")
	}
	const head = "同镜多个说话人：整镜基频分不开"
	if strings.TrimSpace(asrPath) == "" {
		return VoicePitchSkipped(head + "，而逐句归属要 ASR 词流（没给路径），不判音色")
	}
	res, err := asr.Parse(asrPath)
	if err != nil || res == nil || len(res.Words) == 0 {
		return VoicePitchSkipped(head + "，而这一镜没有可用的 ASR 逐词时间轴（逐句归属要它），不判音色")
	}
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = l.Text
	}
	spans, ok := asr.AlignLines(texts, res.Words)
	if !ok {
		return VoicePitchSkipped(head + "，而 ASR 词流与这几句台词对不上（逐句归属不敢猜），不判音色")
	}
	tr, derr := audio.DecodePitch(ctx, f, clip)
	if derr != nil {
		return VoicePitchFailed(derr)
	}
	findings, judged, skips := PerLineVoiceFindings(tr, lines, spans)
	rep := &VoicePitchReport{Findings: findings, PerLine: judged, MedianHz: tr.MedianHz(), VoicedSec: tr.VoicedSec()}
	if judged == 0 {
		var why []string
		for _, s := range skips {
			if s != "" {
				why = append(why, s)
			}
		}
		rep.PerLine = 0
		rep.Skipped = head + "，逐句也都没判成：" + strings.Join(why, "；")
		return rep
	}
	rep.Checked = true
	return rep
}
