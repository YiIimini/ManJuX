package qc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/audio"
)

// pitch_lines_test.go 同镜多说话人时**逐句**判音色的门（2026-10-11）。
//
// 【夹具为什么这么写】声线与说话人取自真机《星纪山海》第01集：镜 9 是「苏棉（星澜）＋小狗」——
// 卡上苏棉是女声（`A clear low young female voice, a little roughened by dust, steady and warm`），
// 小狗是中性（`A bright, slightly hoarse telepathic puppy voice…`，`GenderOfVoice` 认不出性别）；
// 镜 13 是「星澜＋星兽残魂」（后者卡上写 `No voice — only a low hydraulic whine`）。
// 真机探针实测：镜 9 走逐句那条路时，苏棉那一句量到 **114 Hz（男声带）** 而卡上是女声 ⇒ 判负。
//
// 基频轨迹用**合成**（`PitchTrack{F0, Hop}` 是纯数据，判据本身与 ffmpeg 无关）—— 这正是
// `audio.PitchFromPCM` 那套自检的同一思路：没有真机素材的机器上也要能复现判据本身。

// trackOf 造一条基频轨迹：hop 是 10ms（与 audio.PitchHopSec 一致），[start,end) 秒填 hz、其余填 0。
func trackOf(hop float64, totalSec float64, segs ...[]float64) *audio.PitchTrack {
	_ = totalSec
	n := 0
	for _, s := range segs {
		if len(s) > n {
			n = len(s)
		}
	}
	f0 := make([]float64, 0, n)
	for _, s := range segs {
		f0 = append(f0, s...)
	}
	return &audio.PitchTrack{Hop: hop, F0: f0}
}

// fill 造 n 帧同值 hz（0 = 该帧没有可靠基频）。
func fill(n int, hz float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = hz
	}
	return out
}

// realShotNineLines 真机镜 9 的两句（说话人 + 卡上声线原文，逐字取自方案）。
func realShotNineLines() []VoiceLine {
	return []VoiceLine{
		{Speaker: "苏棉", Voice: "A clear low young female voice, a little roughened by dust, steady and warm with a stubborn edge", Text: "你……你说什么？"},
		{Speaker: "小狗（饕餮幼崽）", Voice: "A bright, slightly hoarse telepathic puppy voice speaking in the mind", Text: "爸爸，我一直跟着你。"},
	}
}

// ① 同镜"女声卡 + 中性声线"混在一起时**能逐句判**：把每一句按它自己的时间窗量，
// 中性那句不判（不猜），女声那句量到男声带就判负并**点名到说话人**。
func TestPerLineVoiceFindingsJudgesMixedShot(t *testing.T) {
	lines := realShotNineLines()
	// 苏棉那一句（前 1.5 秒）实测 114 Hz（= 真机量到的男声带）；小狗那句（后 1.5 秒）192 Hz。
	tr := trackOf(0.01, 3.0, fill(150, 114), fill(150, 192))
	spans := []asr.Span{{Start: 0, End: 1.5, Ratio: 1}, {Start: 1.5, End: 3.0, Ratio: 1}}
	findings, judged, skips := PerLineVoiceFindings(tr, lines, spans)
	if len(findings) != 1 {
		t.Fatalf("该只判负 1 句（苏棉那句量到男声带），实际 %d 条：%v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Detail, "苏棉") {
		t.Errorf("判负必须**点名到说话人**（用户要知道是哪一句念错了），实际：%s", findings[0].Detail)
	}
	if !strings.Contains(findings[0].Detail, "念出来却是男声带") {
		t.Errorf("措辞该与整镜那条**同一套**（念出来却是男声带），实际：%s", findings[0].Detail)
	}
	if judged != 1 {
		t.Errorf("该只判了 1 句（中性那句不判），实际判了 %d 句", judged)
	}
	if !strings.Contains(skips[1], "没写性别") {
		t.Errorf("中性声线那该句如实说明为什么不判，实际：%q", skips[1])
	}
	if skips[0] != "" {
		t.Errorf("判过的那句不该再留\"不判\"原因，实际：%q", skips[0])
	}
}

// ② 判不了就**不猜**：对不上词流 / 窗太短 / 命中率太低 / 空窗 —— 一律不判并说明（三态）。
func TestPerLineVoiceFindingsNeverGuesses(t *testing.T) {
	lines := realShotNineLines()[:1] // 只要苏棉那一句（女声卡）
	tr := trackOf(0.01, 3.0, fill(300, 192))
	cases := []struct {
		name  string
		spans []asr.Span
		want  string
	}{
		{"没有这一句的窗", nil, "没对齐到这一句"},
		{"空窗", []asr.Span{{Start: 1.0, End: 1.0, Ratio: 1}}, "时间窗是空的"},
		{"窗太短（0.2 秒）", []asr.Span{{Start: 0, End: 0.2, Ratio: 1}}, "不足"},
		{"命中率太低", []asr.Span{{Start: 0, End: 2.0, Ratio: 0.3}}, "不敢认这一句"},
	}
	for _, c := range cases {
		findings, judged, skips := PerLineVoiceFindings(tr, lines, c.spans)
		if len(findings) != 0 || judged != 0 {
			t.Errorf("%s：不许判（findings=%v judged=%d）—— 没证据就不判", c.name, findings, judged)
		}
		if len(skips) == 0 || !strings.Contains(skips[0], c.want) {
			t.Errorf("%s：该如实说明原因（含 %q），实际 %v", c.name, c.want, skips)
		}
	}
}

// ③ 落在男女重叠带 ⇒ 不判（与整镜那条同一口径，别把"没判"说成"过了"）。
func TestPerLineVoiceFindingsOverlapBandNotJudged(t *testing.T) {
	lines := realShotNineLines()[:1]
	tr := trackOf(0.01, 2.0, fill(200, 150)) // 150 Hz 落在 140–165 重叠带
	findings, judged, skips := PerLineVoiceFindings(tr, lines, []asr.Span{{Start: 0, End: 2.0, Ratio: 1}})
	if len(findings) != 0 || judged != 0 {
		t.Errorf("重叠带不许判（findings=%v judged=%d）", findings, judged)
	}
	if !strings.Contains(skips[0], "重叠带") {
		t.Errorf("重叠带该如实说\"不判\"，实际 %q", skips[0])
	}
}

// ④ 什么时候该退到逐句那条路：**有写性别的 + 混着中性/另一性别**才算。
func TestVoiceLinesNeedPerLine(t *testing.T) {
	female := "A clear low young female voice, steady and warm"
	neutral := "A bright, slightly hoarse telepathic puppy voice speaking in the mind"
	noVoice := "No voice — only a low hydraulic whine, rusted servo grinding"
	male := "A flat, low, mechanical male voice, clipped and emotionless"
	cases := []struct {
		name  string
		lines []VoiceLine
		want  bool
	}{
		{"真机镜 9（女声卡 + 中性）", realShotNineLines(), true},
		{"真机镜 13（中性 + 女声卡）", []VoiceLine{{Speaker: "星兽残魂", Voice: noVoice}, {Speaker: "苏棉", Voice: female}}, true},
		{"男女混合", []VoiceLine{{Voice: female}, {Voice: male}}, true},
		{"只有一种性别（整镜就能判）", []VoiceLine{{Voice: female}, {Voice: female}}, false},
		{"全是中性（逐句也只是逐句地不判）", []VoiceLine{{Voice: neutral}, {Voice: noVoice}}, false},
		{"没有台词", nil, false},
	}
	for _, c := range cases {
		if got := VoiceLinesNeedPerLine(c.lines); got != c.want {
			t.Errorf("%s：该退到逐句 = %v，实际 %v", c.name, c.want, got)
		}
	}
}

// ⑤ 逐句那条路**没有词流就不判**，而且绝不许把"没判"写成"通过"。
func TestMeasureVoicePitchMixedNeedsWordStream(t *testing.T) {
	lines := realShotNineLines()
	rep := MeasureVoicePitchMixed(context.Background(), nil, "no-such.mp4", "", lines)
	if rep == nil || rep.Checked || rep.Skipped == "" {
		t.Fatalf("没有词流路径时该返回\"不判\"（Checked=false + Skipped），实际 %+v", rep)
	}
	if !strings.Contains(rep.Skipped, "ASR") {
		t.Errorf("要如实说清为什么没法逐句判（缺 ASR 词流），实际：%s", rep.Skipped)
	}
	// 侧车文件不存在：同样是不判（而不是崩/当成通过）。
	missing := filepath.Join(t.TempDir(), "shot_009.mp4.asr.txt")
	rep = MeasureVoicePitchMixed(context.Background(), nil, "no-such.mp4", missing, lines)
	if rep == nil || rep.Checked || rep.Skipped == "" {
		t.Fatalf("侧车不存在时该返回\"不判\"，实际 %+v", rep)
	}
	// 没有台词/声线清单时也不判（兜一层）。
	if rep := MeasureVoicePitchMixed(context.Background(), nil, "x.mp4", "y.txt", nil); rep == nil || rep.Skipped == "" {
		t.Errorf("没有台词时该明确说不判，实际 %+v", rep)
	}
}

// ⑥ 逐句那条路的**摘要不许用整镜口径说话**（镜里还有别的说话人）。
func TestVoicePitchSummaryPerLine(t *testing.T) {
	rep := &VoicePitchReport{Checked: true, PerLine: 2}
	got := VoicePitchSummary(rep)
	if !strings.Contains(got, "逐句") {
		t.Errorf("逐句判过的镜，摘要该说\"逐句\"，实际：%s", got)
	}
	if strings.Contains(got, "卡上是") {
		t.Errorf("摘要不许说\"卡上是女声\"那种整镜口径的话（镜里还有别的说话人）：%s", got)
	}
}
