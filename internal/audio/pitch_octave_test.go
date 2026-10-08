package audio

import (
	"math"
	"math/rand"
	"testing"
)

// sineMix 合成"若干正弦叠加"的 s16le PCM（幅度比按传入值）。
//
// 为什么除了 synthVoice 还需要它：synthVoice 造的是**脉冲串**（谐波按 1/n 衰减），
// 而最容易把基频估计器带偏的形态是"基频比某个谐波还弱" —— 真人里也有
// （轻声/气声/录音高通之后），必须有一条能构造它的夹具。
func sineMix(rate int, sec float64, comps [][2]float64, noise float64, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	n := int(sec * float64(rate))
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(rate)
		var v float64
		for _, c := range comps {
			v += c[1] * math.Sin(2*math.Pi*c[0]*t)
		}
		x[i] = v + noise*rng.NormFloat64()
	}
	var peak float64
	for _, v := range x {
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	if peak <= 0 {
		peak = 1
	}
	raw := make([]byte, n*2)
	for i, v := range x {
		s := int16(math.MaxInt16 * math.Max(-1, math.Min(1, v/peak*0.35)))
		raw[i*2] = byte(uint16(s) & 0xff)
		raw[i*2+1] = byte(uint16(s) >> 8)
	}
	return raw
}

// TestPitchGivesNoOctaveUpOnHarmonicDominatedFrame 基频估计**不许因为二次谐波更强
// 就把读数顶高一个八度**。
//
// 【2026-10-08 审查发现的真缺陷】原实现是"归一化自相关 + 取第一个足够强的峰"，
// 门限 0.90×主峰。实测这一例：110 Hz 基频 + 幅度 4 倍的二次谐波，自相关剖面
// lag=72（222 Hz）得 0.81、lag=145（110 Hz）只 0.72 —— **半周期峰真的更高**
// （强谐波主导了窗内相关），于是被读成 220.9 Hz（+101%）。
// 后果不是"读数难看"：qc.VoicePitchReport 会据此判"男声卡念成了女声"，
// 闭环再换种子重出 1~2 轮 = **拿假证据白烧 GPU**。所以这一条必须是硬判据。
func TestPitchGivesNoOctaveUpOnHarmonicDominatedFrame(t *testing.T) {
	cases := []struct {
		name string
		rate int
		raw  []byte
		want float64
	}{
		{"110:1+220:4(二次谐波强 4 倍)", sampleRate, sineMix(sampleRate, 3, [][2]float64{{110, 1}, {220, 4}}, 0.01, 7), 110},
		{"110:1+220:4+330:2(多个强谐波)", sampleRate, sineMix(sampleRate, 3, [][2]float64{{110, 1}, {220, 4}, {330, 2}}, 0.01, 7), 110},
		{"130:1+260:4", sampleRate, sineMix(sampleRate, 3, [][2]float64{{130, 1}, {260, 4}}, 0.01, 7), 130},
		{"110:1+220:4 @44.1kHz", 44100, sineMix(44100, 3, [][2]float64{{110, 1}, {220, 4}}, 0.01, 7), 110},
		{"110:1+220:4 @8kHz", 8000, sineMix(8000, 3, [][2]float64{{110, 1}, {220, 4}}, 0.01, 7), 110},
	}
	for _, c := range cases {
		tr := PitchFromPCM(c.raw, c.rate, PitchHopSec)
		got := tr.MedianHz()
		if got == 0 {
			t.Errorf("%s：一帧都没量出基频", c.name)
			continue
		}
		if math.Abs(got-c.want) > 0.06*c.want {
			t.Errorf("%s：估成 %.1f Hz（真值 %.0f，差 %.0f%%）—— 八度又被读错了",
				c.name, got, c.want, 100*(got-c.want)/c.want)
		}
	}
}

// TestPitchKeepsTrueHighVoiceAndBoundary 两个"不许被纠偏过头"的形态：
// ① 真的 245 Hz 女声（往下查 f/2、f/4 不能把它拉低）；
// ② 恰好落在搜索下界的 400 Hz 纯音（原来从 lo+1 起扫，实测被读成 200.3 Hz）。
func TestPitchKeepsTrueHighVoiceAndBoundary(t *testing.T) {
	if got := PitchFromPCM(synthVoice(245, 3, sampleRate, 0.03, 7), sampleRate, PitchHopSec).MedianHz(); math.Abs(got-245) > 0.06*245 {
		t.Errorf("245 Hz 的脉冲串被读成 %.1f Hz（纠偏过头或读低一个八度）", got)
	}
	if got := PitchFromPCM(sineMix(sampleRate, 3, [][2]float64{{245, 1}}, 0.01, 7), sampleRate, PitchHopSec).MedianHz(); math.Abs(got-245) > 0.06*245 {
		t.Errorf("245 Hz 纯音被读成 %.1f Hz", got)
	}
	// 400 Hz 是 PitchMaxHz（搜索下界 lag = rate/PitchMaxHz）：必须含端点。
	// 容差刻意收到 2%（其余用例是 6%）：排除端点会把 400 Hz 读成 200.3 Hz、只差一个 lag 的
	// 写法会读成 390 Hz —— 6% 的容差把后者盖住了（反向验证时这条守卫"改坏了还是绿的"），
	// 所以按实测精度（400.5 Hz，误差 0.1%）收紧。
	if got := PitchFromPCM(sineMix(sampleRate, 3, [][2]float64{{400, 1}}, 0.01, 7), sampleRate, PitchHopSec).MedianHz(); math.Abs(got-400) > 0.02*400 {
		t.Errorf("400 Hz 纯音被读成 %.1f Hz（搜索下界把端点排除掉了？）", got)
	}
	// 220 Hz 纯音：往下查 110（f/2）时，110 处**没有能量**，不许把它拉低。
	if got := PitchFromPCM(sineMix(sampleRate, 3, [][2]float64{{220, 1}}, 0.01, 7), sampleRate, PitchHopSec).MedianHz(); math.Abs(got-220) > 0.06*220 {
		t.Errorf("220 Hz 纯音被读成 %.1f Hz（无证据的向下纠偏）", got)
	}
}
