package qc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pitch_test.go 音色核对（pitch.go）的判据夹具。
//
// 【夹具照真机原文写】下面这些声线文本是**真机 plan.json 里逐字抄下来的**（《顺其自然》
// 第01集：5 张角色卡 + 平台三条默认声线 + 文档里那条 10 岁童声）。音高数字也是**真机实测值**
// （见文件末的对照表）—— 不许"两三句意思一下"，否则判据咬不到真问题。

// 真机角色卡 voice（逐字）
const (
	voiceZhou      = "Light, low-pitched young female voice, unhurried and clipped, with a soft Ningbo accent and very little upward inflection"
	voiceHanYan    = "Mature mid-range female voice, even and matter-of-fact, sentences ending flat as if she has said them a hundred times"
	voiceChairman  = "Warm elderly male voice, slow and settled, low volume, with the calm of a man who has stopped needing an answer"
	voiceIntern    = "Bright high young male voice, fast and a little breathy, sentences tilting up into questions"
	voiceVolunteer = "Soft young male voice, slightly hoarse from the cold, trailing off when he reads the signature aloud"
	// 无卡说话人「邮局同事」走 project.DerivedVoice 的池子：真机提交的中性声线
	voicePostClerk = "a neutral, androgynous voice, even-paced and calm"
	// 平台三条默认（voice_rebind.go 的常量，逐字）
	voiceNarratorDefault  = "an androgynous adult narrator voice, mid-range pitch, warm and even, steady pacing with clear Mandarin broadcast delivery"
	voiceSyntheticDefault = "a flat, sexless synthetic voice, even-paced and breathless"
	voiceGenericDefault   = "a natural, clearly articulated Mandarin voice, steady and even"
	// 童声（平台文档里的例子：10 岁顽童）
	voiceChild = "High cheeky child's voice, around 10, jeering"
)

// TestGenderOfVoiceOnRealCardVoices 声线文本 → 性别，必须与真机卡一致；
// 中性/童声/矛盾描述一律"判不了"（这是不制造假告警的前提）。
func TestGenderOfVoiceOnRealCardVoices(t *testing.T) {
	cases := []struct {
		name string
		text string
		want VoiceGender
	}{
		{"周（女卡）", voiceZhou, GenderFemale},
		{"韩沿（女卡）", voiceHanYan, GenderFemale},
		{"会长（男卡）", voiceChairman, GenderMale},
		{"实习生（男卡）", voiceIntern, GenderMale},
		{"志愿者（男卡）", voiceVolunteer, GenderMale},
		{"邮局同事（无卡·池·中性）", voicePostClerk, GenderUnknown},
		{"旁白默认（改中性之后）", voiceNarratorDefault, GenderUnknown},
		{"合成音默认", voiceSyntheticDefault, GenderUnknown},
		{"通用默认", voiceGenericDefault, GenderUnknown},
		{"童声（10 岁）", voiceChild, GenderUnknown},
		{"空文本", "", GenderUnknown},
		{"自相矛盾（男女词都有）", "a woman's forties voice answered in a steady male voice", GenderUnknown},
		// 反例：Mandarin 里有 "man" 的字母序列，但不构成单词 —— 不许被误判成男声。
		{"Mandarin 不误判", "a natural Mandarin voice", GenderUnknown},
		// 反向：韩沿卡上那句里同时有 female 与 she（同一个性别，不算矛盾）。
		{"同一性别的多个词", "young female voice; she speaks evenly", GenderFemale},
		// 【风险形态·不是真机卡】"中性词 + 性别词并存"在**平台真实语料里还没有**
		// （2026-10-08 扫过 5 份 plan.json、共 5 张有声线文本的卡：没有一条同时含两类词），
		// 但它是差一个词的事（作者写"androgynous voice, her tone flat"、模型给童声加个 "he"）。
		// 这两条**断言的是守卫本身**：两类线索冲突时一律判"不可判"，否则就会拿成人男女
		// 阈值去判一个本来就不分性别的嗓子（假告警）。
		{"中性+性别词并存（风险形态）", "a soft, breathy androgynous voice, quiet and thoughtful, a young woman's register", GenderUnknown},
		{"童声+代词并存（风险形态）", "a child's voice, around ten; he is jeering at the crowd", GenderUnknown},
	}
	for _, c := range cases {
		if got := GenderOfVoice(c.text); got != c.want {
			t.Errorf("%s：GenderOfVoice(%q) = %q，想要 %q", c.name, c.text, got, c.want)
		}
	}
}

// makeToneClip 造一段"有一轨单一纯音"的 mp4（判"念出来是高是低"的最小素材）。
//
// 为什么用纯音而不是 TTS 素材：真机音轨不进仓库，而这条判据要判的正是**音高**本身 ——
// 纯音的基频是已知量（就是那个频率），拿它当夹具反而比任何录像都更严格。
func makeToneClip(t *testing.T, dir string, hz, sec float64) string {
	t.Helper()
	out := filepath.Join(dir, "tone.mp4")
	args := []string{"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=320x240:d=" + ftoa(sec),
		"-f", "lavfi", "-i", "sine=frequency=" + ftoa(hz) + ":duration=" + ftoa(sec),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-r", "24",
		"-c:a", "aac", "-b:a", "128k", "-shortest", out}
	cmd := exec.Command("ffmpeg", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("造测试音频失败(%v):%s", err, string(b))
	}
	return out
}

// ftoa 把频率写成 ffmpeg 认的小数字符串（避免 strconv 的指数写法）。
func ftoa(v float64) string {
	n := int(v * 1000)
	whole, frac := n/1000, n%1000
	s := itoa(whole)
	if frac > 0 {
		f := itoa(frac)
		for len(f) < 3 {
			f = "0" + f
		}
		s += "." + strings.TrimRight(f, "0")
	}
	return s
}

func hanYanLine() []VoiceLine {
	return []VoiceLine{{Speaker: "韩沿", Voice: voiceHanYan, Text: "他就拆开,一张都不超过一万。"}}
}

func chairmanLine() []VoiceLine {
	return []VoiceLine{{Speaker: "会长", Voice: voiceChairman, Text: "三页记录,凑不成一个人。"}}
}

// TestVoicePitchFlagsFemaleCardShotThatCameOutMale 真机镜 5：卡上女声、念出来 130 Hz。
func TestVoicePitchFlagsFemaleCardShotThatCameOutMale(t *testing.T) {
	f := newFF(t)
	clip := makeToneClip(t, t.TempDir(), 130, 3)
	rep := MeasureVoicePitch(context.Background(), f, clip, hanYanLine())
	if rep == nil {
		t.Fatal("没返回报告")
	}
	if !rep.Checked {
		t.Fatalf("130 Hz 的女卡镜头应该被判（Skipped=%q Error=%q）", rep.Skipped, rep.Error)
	}
	if rep.Expected != GenderFemale {
		t.Errorf("期望性别 = %q，想要 female", rep.Expected)
	}
	if !rep.Bad() {
		t.Fatalf("女卡角色念成 %.0f Hz 没被判负", rep.MedianHz)
	}
	if !strings.Contains(rep.Findings[0].Detail, "女声") || !strings.Contains(rep.Findings[0].Detail, "男声带") {
		t.Errorf("判词没说清哪边到哪边：%s", rep.Findings[0].Detail)
	}
	// 判词必须把**卡上那条声线原文**带上，用户才知道该比什么。
	if !strings.Contains(rep.Findings[0].Detail, "Mature mid-range female voice") {
		t.Errorf("判词里没有卡上的声线原文：%s", rep.Findings[0].Detail)
	}
	if s := VoicePitchSummary(rep); !strings.Contains(s, "Hz") || !strings.Contains(s, "女声") {
		t.Errorf("摘要里没有实测数字/方向：%s", s)
	}
	if w := VoicePitchWarnText(rep.Findings[0]); !strings.HasPrefix(w, "🎙") {
		t.Errorf("告警文案该带麦克风前缀：%s", w)
	}
}

// TestVoicePitchFlagsMaleCardShotThatCameOutFemale 真机镜 6 的反方向：卡上男声、念出来 204 Hz。
func TestVoicePitchFlagsMaleCardShotThatCameOutFemale(t *testing.T) {
	f := newFF(t)
	clip := makeToneClip(t, t.TempDir(), 204, 3)
	rep := MeasureVoicePitch(context.Background(), f, clip,
		[]VoiceLine{{Speaker: "志愿者", Voice: voiceVolunteer, Text: "落款,顺其自然?"}})
	if rep == nil || !rep.Checked || !rep.Bad() {
		t.Fatalf("男卡角色念成 204 Hz 没被判负：%+v", rep)
	}
	if rep.Expected != GenderMale || !strings.Contains(rep.Findings[0].Detail, "女声带") {
		t.Errorf("判词不对：expected=%q detail=%s", rep.Expected, rep.Findings[0].Detail)
	}
}

// TestVoicePitchPassesMatchingGenders 同一角色在真机里的正常档不许被判负（否则每次重出都白烧 GPU）。
func TestVoicePitchPassesMatchingGenders(t *testing.T) {
	f := newFF(t)
	// 真机镜 3/10/13：韩沿 176/198/181 Hz（女卡 + 女声带）
	clip := makeToneClip(t, t.TempDir(), 190, 3)
	rep := MeasureVoicePitch(context.Background(), f, clip, hanYanLine())
	if !rep.Checked || rep.Bad() {
		t.Fatalf("女卡 190 Hz 不该判负：%+v", rep)
	}
	if rep.MedianHz < 175 || rep.MedianHz > 205 {
		t.Errorf("实测中位基频 %.1f Hz 与夹具的 190 Hz 差太多（估计器漂了）", rep.MedianHz)
	}
	// 真机镜 8：会长 105 Hz（男卡 + 男声带）
	rep2 := MeasureVoicePitch(context.Background(), f, clip, chairmanLine())
	if !rep2.Bad() {
		t.Errorf("会长（男卡）念成 190 Hz 应该判负：%+v", rep2)
	}
	clip2 := makeToneClip(t, t.TempDir(), 105, 3)
	rep3 := MeasureVoicePitch(context.Background(), f, clip2, chairmanLine())
	if !rep3.Checked || rep3.Bad() {
		t.Fatalf("男卡 105 Hz 不该判负：%+v", rep3)
	}
}

// TestVoicePitchRefusesToJudge 判不了的情形必须**如实说为什么**，而不是默默当成通过。
func TestVoicePitchRefusesToJudge(t *testing.T) {
	f := newFF(t)
	dir := t.TempDir()
	clip := makeToneClip(t, dir, 190, 3)
	ctx := context.Background()

	cases := []struct {
		name  string
		lines []VoiceLine
		want  string
	}{
		{"中性声线（邮局同事）", []VoiceLine{{Speaker: "邮局同事", Voice: voicePostClerk, Text: "又是那个人?"}}, "都没有写性别"},
		{"旁白默认（中性）", []VoiceLine{{Speaker: "旁白", Voice: voiceNarratorDefault, Text: "桌子换了。"}}, "都没有写性别"},
		{"合成音", []VoiceLine{{Speaker: "舱内AI", Voice: voiceSyntheticDefault, Text: "系统就绪。"}}, "都没有写性别"},
		{"童声（10 岁）", []VoiceLine{{Speaker: "顽童", Voice: voiceChild, Text: "看招!"}}, "都没有写性别"},
		{ // 真机镜 4：实习生（男）+ 韩沿（女）
			"同镜男女混合",
			[]VoiceLine{{Speaker: "实习生", Voice: voiceIntern, Text: "为什么不一次汇完?"},
				{Speaker: "韩沿", Voice: voiceHanYan, Text: "一次汇一万以上,要写名字。"}},
			"同时有男声和女声",
		},
		{ // 真机镜 1：邮局同事（中性）+ 周（女）
			"中性 + 写性别的角色混在",
			[]VoiceLine{{Speaker: "邮局同事", Voice: voicePostClerk, Text: "又是那个人?"},
				{Speaker: "周", Voice: voiceZhou, Text: "十一月底,还能是谁。"}},
			"既有写性别的角色",
		},
		{"没有台词", nil, "没有需要核对音色的台词"},
	}
	for _, c := range cases {
		rep := MeasureVoicePitch(ctx, f, clip, c.lines)
		if rep == nil || rep.Checked || rep.Skipped == "" {
			t.Errorf("%s：应当「不判」并说明原因，得到 %+v", c.name, rep)
			continue
		}
		if !strings.Contains(rep.Skipped, c.want) {
			t.Errorf("%s：跳过原因没说到点上：%q（想要含 %q）", c.name, rep.Skipped, c.want)
		}
	}

	// 中间重叠带（真机镜 9 的会长 140.6 Hz）：判了但不算负 —— 边界上宁可不判。
	mid := makeToneClip(t, t.TempDir(), 150, 3)
	if rep := MeasureVoicePitch(ctx, f, mid, chairmanLine()); rep.Bad() {
		t.Errorf("150 Hz 落在男女重叠带，不该判负：%+v", rep)
	}
	// 纯静音：量不到基频 ⇒ 不判（不许当成"通过"）。
	silent := makeToneClip(t, t.TempDir(), 0, 2)
	if rep := MeasureVoicePitch(ctx, f, silent, hanYanLine()); rep.Checked || rep.Skipped == "" {
		t.Errorf("静音镜头应当「不判」：%+v", rep)
	}
	// 人声太短：1 秒里只留 0.1 秒有声(用 0.1 秒素材) ⇒ 不判
	short := makeToneClip(t, t.TempDir(), 190, 0.12)
	if rep := MeasureVoicePitch(ctx, f, short, hanYanLine()); rep.Bad() {
		t.Errorf("素材过短不该判负：%+v", rep)
	}
}

// TestVoicePitchFailedIsNotPass 解码失败/没有 ffmpeg 时必须记成"没判定"，绝不当成通过。
func TestVoicePitchFailedIsNotPass(t *testing.T) {
	rep := MeasureVoicePitch(context.Background(), nil, "nope.mp4", hanYanLine())
	if rep == nil || rep.Error == "" {
		t.Fatalf("没有 ffmpeg 时应当记成失败：%+v", rep)
	}
	if rep.Checked || rep.Bad() {
		t.Errorf("失败不该被记成「验过」或「判负」：%+v", rep)
	}
	if s := VoicePitchSummary(rep); !strings.Contains(s, "失败") {
		t.Errorf("摘要没说失败：%s", s)
	}
	// 文件不存在同理。
	if _, err := os.Stat("nope.mp4"); err == nil {
		t.Fatal("夹具前提错了：nope.mp4 居然存在")
	}
}
