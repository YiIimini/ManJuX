package audio

import (
	"math"
	"math/rand"
	"testing"
)

// synthVoice 合成"已知基频 + 有点共振峰味 + 抖动"的脉冲串。
//
// 为什么要合成信号：判据本身的正确性必须能在**没有真机素材**的机器上复现
// （真机素材不进仓库）。基频估计最容易错的地方是八度（自相关在周期整数倍处
// 同样是大峰），合成信号正是冲着这一点设计的：抖动 ±3% 模拟真人音高的颤动，
// 卷积核模拟声道的频谱倾斜。
func synthVoice(hz, sec float64, rate int, jitter float64, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	n := int(sec * float64(rate))
	pulse := make([]float64, n)
	period := float64(rate) / hz
	for t := 0.0; t < float64(n-1); {
		i := int(t)
		pulse[i] += 1.0
		t += period * (1 + jitter*rng.NormFloat64())
	}
	// 声道味道：FIR（与真机取证脚本同一套系数）
	kernel := []float64{1.0, -1.2, 0.6, -0.3, 0.2, -0.1}
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		var acc float64
		for k, c := range kernel {
			if i-k >= 0 {
				acc += c * pulse[i-k]
			}
		}
		x[i] = acc
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
		v = v/peak*0.35 + 0.01*rng.NormFloat64() // 底噪
		s := int16(math.MaxInt16 * math.Max(-1, math.Min(1, v)))
		raw[i*2] = byte(uint16(s) & 0xff)
		raw[i*2+1] = byte(uint16(s) >> 8)
	}
	return raw
}

// TestPitchEstimatorOnSyntheticVoices 基频估计必须在合成信号上过关 ——
// 六档覆盖真机实测到的范围（男带 95/110/130、女带 190/210/245）。
func TestPitchEstimatorOnSyntheticVoices(t *testing.T) {
	for _, hz := range []float64{95, 110, 130, 190, 210, 245} {
		raw := synthVoice(hz, 3, sampleRate, 0.03, 7)
		tr := PitchFromPCM(raw, sampleRate, PitchHopSec)
		got := tr.MedianHz()
		if got == 0 {
			t.Fatalf("合成 %v Hz 的信号一帧都没量出基频（门限太严）", hz)
		}
		if math.Abs(got-hz) > 0.06*hz {
			t.Errorf("合成 %v Hz 的信号估成 %.1f Hz（误差 >6%%：判据本身错了）", hz, got)
		}
		// 覆盖率不是 100%：帧长 40ms、末尾不足一帧的部分被丢掉，且脉冲串经过
		// 频谱倾斜后有少数帧的自相关峰掉到门限下。实测六档都在 2.0~2.5s / 3s，
		// 这里只要求"大部分时间量得到"（≥60%）。
		if sec := tr.VoicedSec(); sec < 1.8 {
			t.Errorf("合成 %v Hz：可靠帧只覆盖 %.2fs（应接近 3s 的多数）", hz, sec)
		}
	}
}

// TestPitchIgnoresSilenceAndNoise 静音与纯噪声都不该被量出基频 ——
// 否则"没人说话的镜头"会被判成音色不符。
func TestPitchIgnoresSilenceAndNoise(t *testing.T) {
	if tr := PitchFromPCM(make([]byte, sampleRate*2), sampleRate, PitchHopSec); tr.MedianHz() != 0 {
		t.Errorf("静音被量出基频 %.1f Hz", tr.MedianHz())
	}
	rng := rand.New(rand.NewSource(3))
	raw := make([]byte, sampleRate*2)
	for i := range raw {
		raw[i] = byte(int16(0.3 * math.MaxInt16 * rng.NormFloat64() / 3))
	}
	tr := PitchFromPCM(raw, sampleRate, PitchHopSec)
	// 白噪没有稳定基频：要么量不出来，要么量出来的值不该高度集中（这里只要求
	// 不出现"整段都是同一个稳定基频"的假象）。
	if tr.MedianHz() != 0 && len(tr.Voiced()) > len(tr.F0)*9/10 {
		t.Errorf("白噪被当成 %.1f Hz 的稳定人声（%d/%d 帧可靠）", tr.MedianHz(), len(tr.Voiced()), len(tr.F0))
	}
}

// TestPitchTooShortIsHonest 素材短于两帧时报"量不出来"，不猜。
func TestPitchTooShortIsHonest(t *testing.T) {
	raw := synthVoice(200, 0.02, sampleRate, 0, 1)
	if tr := PitchFromPCM(raw, sampleRate, PitchHopSec); len(tr.Voiced()) > 0 && tr.VoicedSec() > 0.06 {
		t.Errorf("20ms 素材不该给出 %.2fs 的可靠基频", tr.VoicedSec())
	}
}
