// pitch.go 一镜的**音色核对**：把"卡上写的是男声还是女声"与"音轨里实际念出来的基频"对一遍。
//
// 【为什么必须有这一条】（2026-10-08 用户第二次报「女的配男音」）
//
// 平台此前每条配音闸门验的都是"说了什么"（QC 语音对账 = ASR 逐句比台词），而用户听到的
// 是"谁在说、什么嗓子"。真机取证（《顺其自然》第01集）把这件事说死了：
//
//	镜 5  卡上 `Mature mid-range female voice…`  提交的提示词逐字写着这条  实测中位 F0 130 Hz（男声带）
//	镜 6  卡上 `Soft young male voice…`          提交的提示词逐字写着这条  实测中位 F0 204 Hz（女声带）
//	镜 3/10/13 同一位女角色                                            176 / 198 / 181 Hz（女声带）
//
// 也就是说：**声线绑对了也没用** —— H3 把这段文字当"音频条件"，不是契约。能确定性做到的
// 只剩一件事：把音轨量出来，判"明显站错边"，判负就换种子重出（见 pipeline 的重出逻辑）。
//
// 【判据的边界（刻意保守）】
//   - 只判**明显**站错边：男声带 <140 Hz、女声带 ≥165 Hz，中间 140–165 Hz 是两者重叠区
//     ⇒ 一律"不判"（宁可漏判，也不制造假告警 —— 假告警会让真问题被淹没）。
//   - 声线文本里没有性别信息的（`a neutral, androgynous voice` / `sexless synthetic voice` /
//     `child's voice, around 10`）⇒ **不判**，并如实说"为什么没判"。
//   - 同镜既有男声又有女声（两个说话人）⇒ 整镜基频会混在一起 ⇒ **不判**（要判得先按 ASR
//     时间段归属，见文件末的"还没做"）。
//   - 人声太短（<0.6s 可靠基频）⇒ 不判：一两个音节的中位数没有统计意义。
//
// 【与"听感"的差别，别混为一谈】基频描述的是**音高**，不是音色本身（共振峰、气息、口音
// 都影响听感）。所以这条判据只能抓"男声/女声站错边"这一类 —— 用户报的正是这一类。
package qc

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/YiIimini/ManJuX/internal/audio"
	"github.com/YiIimini/ManJuX/internal/ff"
)

// 判据阈值（Hz）。数值来自成人语音基频的公认范围（男 ≈85–155、女 ≈165–255），
// 中间重叠区一律不判。改这两个数必须同时改 pitch_test.go 里的真机夹具。
const (
	// VoiceMaleMaxHz 低于它算"明显是男声带"。
	VoiceMaleMaxHz = 140.0
	// VoiceFemaleMinHz 高于等于它算"明显是女声带"。
	VoiceFemaleMinHz = 165.0
	// voicePitchMinSec 可靠基频至少要覆盖这么久才判（一两个音节不算）。
	voicePitchMinSec = 0.6
)

// VoiceGender 一段声线文本暗示的性别。
type VoiceGender string

const (
	// GenderUnknown 文本里没有可用信息（中性/系统音/童声/纯描述）。
	GenderUnknown VoiceGender = ""
	GenderFemale  VoiceGender = "female"
	GenderMale    VoiceGender = "male"
)

var (
	reVoiceFemale = regexp.MustCompile(`(?i)\b(female|feminine|woman|women|woman's|women's|girl|girls|girl's|ladies|lady|lady's|she|her|hers)\b`)
	reVoiceMale   = regexp.MustCompile(`(?i)\b(male|masculine|man|men|man's|men's|boy|boys|boy's|lad|guy|he|him|his)\b`)
	// 中性/不可判：写了这些词的声线**一律不判** —— androgynous/sexless 本来就是"不分男女"，
	// 童声（10 岁上下）的基频与成人女声重叠，用成人阈值判它只会误伤。
	reVoiceNeutral = regexp.MustCompile(`(?i)\b(neutral|androgynous|sexless|genderless|unisex|child|child's|kid|kid's|baby)\b`)
)

// GenderOfVoice 从**权威声线文本**（角色卡 voice / 平台默认）判这段声线该是什么性别。
//
// 判据只用确定性的词表（不含任何模型调用）：一条声线文本里
//   - 出现中性词（androgynous/sexless/child…）⇒ 不可判（哪怕同时写了 female）；
//   - 男女词同时出现（自相矛盾的描述）⇒ 不可判；
//   - 只有一种 ⇒ 用它；一种都没有 ⇒ 不可判。
func GenderOfVoice(text string) VoiceGender {
	if strings.TrimSpace(text) == "" {
		return GenderUnknown
	}
	if reVoiceNeutral.MatchString(text) {
		return GenderUnknown
	}
	f, m := reVoiceFemale.MatchString(text), reVoiceMale.MatchString(text)
	switch {
	case f && !m:
		return GenderFemale
	case m && !f:
		return GenderMale
	default:
		return GenderUnknown
	}
}

// VoiceLine 本镜一句台词"该用什么嗓子"。Voice 必须是**权威来源**（角色卡 voice /
// 平台默认声线），不能是模型自己写在引述里的那一版 —— 那正是被换掉的东西。
type VoiceLine struct {
	Speaker string `json:"speaker,omitempty"`
	Voice   string `json:"voice,omitempty"`
	Text    string `json:"text,omitempty"`
}

// PitchFinding 音色核对的一条结论。Kind 固定为 voice-gender（留着扩展位）。
type PitchFinding struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// VoicePitchReport 一镜的音色核对结果。
//
// Checked=false 时 Skipped 里写着**为什么没判**（中性声线/同镜男女混合/人声太短/没人说话）——
// "没验过"与"验过且相符"必须在报告里分得开（与 SpeechReport 同一条纪律）。
// 量到的数字照样填进 MedianHz/VoicedSec/各带占比，便于人工复核。
type VoicePitchReport struct {
	Checked   bool           `json:"checked"`
	Skipped   string         `json:"skipped,omitempty"`
	Error     string         `json:"error,omitempty"`
	Expected  VoiceGender    `json:"expected,omitempty"`
	MedianHz  float64        `json:"median_hz,omitempty"`
	VoicedSec float64        `json:"voiced_sec,omitempty"`
	MalePct   float64        `json:"male_pct,omitempty"`
	MidPct    float64        `json:"mid_pct,omitempty"`
	FemalePct float64        `json:"female_pct,omitempty"`
	Findings  []PitchFinding `json:"findings,omitempty"`
}

// VoicePitchSkipped 构造"没判"的结论。
func VoicePitchSkipped(reason string) *VoicePitchReport {
	return &VoicePitchReport{Skipped: reason}
}

// VoicePitchFailed 构造"想判但量不出来"的结论（解码失败等）—— 同样不当作通过。
func VoicePitchFailed(err error) *VoicePitchReport {
	msg := "未知错误"
	if err != nil {
		msg = err.Error()
	}
	return &VoicePitchReport{Error: msg}
}

// Bad 判负（有 finding）。
func (r *VoicePitchReport) Bad() bool { return r != nil && len(r.Findings) > 0 }

// MeasureVoicePitch 量这一镜的音轨基频，与 lines 里的权威声线对一遍。
//
// lines 为空 ⇒ 不判（本镜没人说话时调用方也不该调它，这里兜一层）。
func MeasureVoicePitch(ctx context.Context, f *ff.FF, clip string, lines []VoiceLine) *VoicePitchReport {
	expected, neutral, mixed := GenderUnknown, false, false
	var expectedVoice string
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
	switch {
	case len(lines) == 0:
		return VoicePitchSkipped("本镜没有需要核对音色的台词")
	case expected == GenderUnknown:
		return VoicePitchSkipped("本镜说话人的声线都没有写性别（中性/系统音/童声），判不了")
	case mixed:
		return VoicePitchSkipped("本镜同时有男声和女声说话人，整镜基频分不开，不判音色")
	case neutral:
		return VoicePitchSkipped("本镜既有写性别的角色、也有中性声线的说话人，整镜基频分不开，不判音色")
	}

	tr, err := audio.DecodePitch(ctx, f, clip)
	if err != nil {
		return VoicePitchFailed(err)
	}
	v := tr.Voiced()
	rep := &VoicePitchReport{Expected: expected, MedianHz: tr.MedianHz(), VoicedSec: tr.VoicedSec()}
	if len(v) == 0 {
		return VoicePitchSkipped("音轨里量不到人声基频（没人说话/清音/噪声），不判音色")
	}
	ml, md := 0, 0
	for _, hz := range v {
		switch {
		case hz < VoiceMaleMaxHz:
			ml++
		case hz >= VoiceFemaleMinHz:
			// 女声带
		default:
			md++
		}
	}
	rep.MalePct = float64(ml) * 100 / float64(len(v))
	rep.MidPct = float64(md) * 100 / float64(len(v))
	rep.FemalePct = float64(len(v)-ml-md) * 100 / float64(len(v))
	if rep.VoicedSec < voicePitchMinSec {
		rep.Skipped = fmt.Sprintf("人声里的可靠基频只覆盖 %.2f 秒（不足 %.1f 秒），不判音色", rep.VoicedSec, voicePitchMinSec)
		return rep
	}
	// 落在男女重叠带（140–165 Hz）⇒ **不判**，而且必须落成 Checked=false + Skipped 说明。
	//
	// 原来这条只写在文件头的注释里：实测（2026-10-08 审查发现）"女卡 + 音轨 150 Hz"得到
	// Checked=true / Findings 空 / 摘要"音色核对通过：卡上是女声，实测中位基频 151 Hz" ——
	// **把"其实没判"说成了"验过且相符"**。三态（通过 / 判负 / 不判）在报告里必须分得开，
	// 否则用户看不出"这一条根本没验"（与 SpeechReport 同一条纪律）。
	if rep.MedianHz >= VoiceMaleMaxHz && rep.MedianHz < VoiceFemaleMinHz {
		rep.Skipped = fmt.Sprintf("实测中位基频 %.0f Hz 落在男女重叠带（%.0f–%.0f Hz），不判音色",
			rep.MedianHz, VoiceMaleMaxHz, VoiceFemaleMinHz)
		return rep
	}
	rep.Checked = true
	switch {
	case expected == GenderFemale && rep.MedianHz < VoiceMaleMaxHz:
		rep.Findings = append(rep.Findings, PitchFinding{Kind: "voice-gender", Detail: fmt.Sprintf(
			"卡上写的是**女声**（%s），念出来却是男声带（中位基频 %.0f Hz < %.0f Hz，可靠基频 %.1fs）—— "+
				"提示词里这条声线是逐字写对的，H3 没照做；换种子重出这一镜", expectedVoice, rep.MedianHz, VoiceMaleMaxHz, rep.VoicedSec)})
	case expected == GenderMale && rep.MedianHz >= VoiceFemaleMinHz:
		rep.Findings = append(rep.Findings, PitchFinding{Kind: "voice-gender", Detail: fmt.Sprintf(
			"卡上写的是**男声**（%s），念出来却是女声带（中位基频 %.0f Hz ≥ %.0f Hz，可靠基频 %.1fs）—— "+
				"提示词里这条声线是逐字写对的，H3 没照做；换种子重出这一镜", expectedVoice, rep.MedianHz, VoiceFemaleMinHz, rep.VoicedSec)})
	}
	return rep
}

// VoicePitchWarnText 把一条音色结论翻成给人看的告警文案。
func VoicePitchWarnText(f PitchFinding) string {
	return "🎙 " + f.Detail
}

// VoicePitchSummary 一镜音色核对结果的一行摘要（日志/阶段详情用）。没判就如实说没判。
func VoicePitchSummary(r *VoicePitchReport) string {
	if r == nil {
		return ""
	}
	switch {
	case r.Error != "":
		return "音色核对失败（未判定，不当作通过）：" + r.Error
	case r.Skipped != "":
		return "音色核对跳过：" + r.Skipped
	}
	if len(r.Findings) == 0 {
		want := "男声"
		if r.Expected == GenderFemale {
			want = "女声"
		}
		return fmt.Sprintf("音色核对通过：卡上是%s，实测中位基频 %.0f Hz（可靠基频 %.1fs）", want, r.MedianHz, r.VoicedSec)
	}
	return fmt.Sprintf("音色核对发现 %d 处问题：%s", len(r.Findings), r.Findings[0].Detail)
}
