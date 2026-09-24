package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

// pcmNoise 生成 n 个采样的 s16le 单声道数据(振幅 amp,0-1)。
func pcmNoise(n int, amp float64, seed int) []byte {
	out := make([]byte, n*2)
	lfsr := uint32(seed*2654435761 + 1)
	for i := 0; i < n; i++ {
		lfsr ^= lfsr << 13
		lfsr ^= lfsr >> 17
		lfsr ^= lfsr << 5
		v := (float64(lfsr%2001)/1000.0 - 1.0) * amp // ±amp
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(v*32767)))
	}
	return out
}

// pcmSilence 生成静音(极低底噪)。
func pcmSilence(n int) []byte { return pcmNoise(n, 0.0005, 7) }

// buildPCM 按 [(采样数, 是否人声)] 拼一段 PCM。
func buildPCM(rate int, parts []struct {
	n      int
	speech bool
}) []byte {
	var out []byte
	for i, p := range parts {
		if p.speech {
			out = append(out, pcmNoise(p.n, 0.35, i+1)...)
		} else {
			out = append(out, pcmSilence(p.n)...)
		}
	}
	return out
}

// TestEnvelopeSpeechDetection 包络与自适应门限:有声段必须被判成人声,静音段不能。
func TestEnvelopeSpeechDetection(t *testing.T) {
	const rate = 16000
	sec := func(s float64) int { return int(s * rate) }
	raw := buildPCM(rate, []struct {
		n      int
		speech bool
	}{
		{sec(1), false}, {sec(1), true}, {sec(1), false}, {sec(1), true},
	})
	e := envelopeFromPCM(raw, rate, Hop)
	if got := e.Duration(); math.Abs(got-4) > 0.05 {
		t.Fatalf("包络时长应约 4 秒,实际 %.2f", got)
	}
	on := e.Speech()
	if got := e.SpeechSeconds(on, 0, 1); got > 0.2 {
		t.Errorf("第 0-1 秒是静音,不该判成人声(%.2f 秒)", got)
	}
	if got := e.SpeechSeconds(on, 1, 2); got < 0.7 {
		t.Errorf("第 1-2 秒是语音,应几乎全判成人声(%.2f 秒)", got)
	}
	if got := e.SpeechSeconds(on, 2, 3); got > 0.2 {
		t.Errorf("第 2-3 秒是静音,不该判成人声(%.2f 秒)", got)
	}
	if got := e.SpeechSeconds(on, 3, 4); got < 0.7 {
		t.Errorf("第 3-4 秒是语音,应几乎全判成人声(%.2f 秒)", got)
	}
}

// TestSolveCutPrefersSilence 接缝切点必须落在静音里 —— 这是「配音不完整」的根治判据。
//
// 场景一:上一镜尾部是人声、下一镜开头是静音 → 切点应贴到窗口末端(t≈width),
// 也就是"丢失量全从下一镜的静音开头里出",上一镜的人声一个字不丢。
func TestSolveCutPrefersSilence(t *testing.T) {
	const rate = 16000
	sec := func(s float64) int { return int(s * rate) }
	// 上一镜:2 秒人声(尾部也是人声)
	out := envelopeFromPCM(buildPCM(rate, []struct {
		n      int
		speech bool
	}{{sec(2), true}}), rate, Hop)
	// 下一镜:开头 1 秒静音,然后人声
	in := envelopeFromPCM(buildPCM(rate, []struct {
		n      int
		speech bool
	}{{sec(1), false}, {sec(2), true}}), rate, Hop)

	const d = 0.5
	t1 := SolveCut(out, 1.5, in, d, d) // 上一镜最后 0.5 秒(全是人声)
	if t1 < d-0.05 {
		t.Errorf("切点应尽量贴到窗口末端(从下一镜的静音开头让出时长),实际 %.2f", t1)
	}
}

// TestSolveCutPrefersOutgoingSilence 反向场景:上一镜尾部静音、下一镜开口就是人声,
// 切点应贴到窗口起点(t≈0)—— 从上一镜的静音尾巴里让出时长,不切下一镜的话头。
func TestSolveCutPrefersOutgoingSilence(t *testing.T) {
	const rate = 16000
	sec := func(s float64) int { return int(s * rate) }
	out := envelopeFromPCM(buildPCM(rate, []struct {
		n      int
		speech bool
	}{{sec(1), true}, {sec(1), false}}), rate, Hop)
	in := envelopeFromPCM(buildPCM(rate, []struct {
		n      int
		speech bool
	}{{sec(2), true}}), rate, Hop)

	const d = 0.5
	t1 := SolveCut(out, 1.5, in, d, d)
	if t1 > 0.05 {
		t.Errorf("切点应贴到窗口起点(从上一镜的静音尾巴让出时长),实际 %.2f", t1)
	}
}

// TestSolveCutFallsBackToMidpoint 包络缺失时必须回退中点切,而不是猜一个位置。
func TestSolveCutFallsBackToMidpoint(t *testing.T) {
	if got := SolveCut(nil, 0, nil, 0.5, 0.5); math.Abs(got-0.25) > 1e-9 {
		t.Errorf("无包络应回退 width/2=0.25,实际 %.3f", got)
	}
	e := &Envelope{Hop: Hop}
	if got := SolveCut(e, 0, e, 0.5, 0.5); math.Abs(got-0.25) > 1e-9 {
		t.Errorf("空包络应回退 width/2=0.25,实际 %.3f", got)
	}
	if got := SolveCut(nil, 0, nil, 0, 0); got != 0 {
		t.Errorf("零宽窗口应返回 0,实际 %.3f", got)
	}
}

// TestSolveCutBalancedWhenAllSpeech 全是人声时没有更优解,应保持中点(与旧行为一致,
// 不引入新的响度不均)。
func TestSolveCutBalancedWhenAllSpeech(t *testing.T) {
	const rate = 16000
	raw := pcmNoise(rate*3, 0.35, 3)
	e := envelopeFromPCM(raw, rate, Hop)
	got := SolveCut(e, 2.0, e, 0.5, 0.5)
	if math.Abs(got-0.25) > Hop*2 {
		t.Errorf("全是人声时应保持中点切(≈0.25),实际 %.3f", got)
	}
}
