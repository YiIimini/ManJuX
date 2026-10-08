// pitch.go 音轨**基频（F0）**估计：给"念出来的音色对不对"提供机器证据。
//
// 【为什么需要它】（2026-10-08 用户第二次报「女的配男音」）
//
// 平台此前能验的只有两件事：① 音轨里有没有人声、有多长（qc.SpeechSec）；
// ② 念的是不是剧本里那几句（qc.SpeechReport，ASR 对账）。**音色本身从来没有判据** ——
// 于是"卡上写着女声、出来是男声"这种缺陷一路穿过所有闸门，只有用户听得出来。
//
// 真机取证（《顺其自然》第01集，卡上 voice 文本 vs 实测）：同一位女角色的四镜里
// 三镜落在女声带（176 / 198 / 181 Hz），**镜 5 落在男声带（130 Hz）**；而同集一位
// 男卡角色在镜 6 却落在女声带（204 Hz）。两镜提交的提示词都**逐字写着权威声线**
// （`Mature mid-range female voice` / `Soft young male voice`）——
// 也就是说 **H3 把这段文字当"音频提示"而不是"契约"**：写对了也可能不照做。
// 结论：光有"绑声线"不够，必须**量出来再判**（这就是本文件存在的理由）。
//
// 判据的物理基础：成人基频男声约 85–155 Hz、女声约 165–255 Hz，两者中间有一段
// 重叠区（本平台把 140–165 Hz 当作**判不了**，见 qc.GenderOfVoice 的说明）——
// 只判"明显站错边"的，不判边界上的（不制造假告警是这条判据能上线的前提）。
//
// 估计方法：**归一化互相关（NCC，分母取重叠区各自能量）+ 两级八度纠正**：
//
//	① 取周期最短的那个"足够强的局部峰"（自相关在整数倍周期处同样是大峰，直接取最大峰
//	   会把 245 Hz 的女声读成 122 Hz 的男声，真机调试时确实出现过）；
//	② 再用频域谐波和（HPS）往下查 f/2、f/4 —— 二次谐波比基频强时，上面那一步反而会
//	   把读数顶高一个八度（实测 110 Hz 被读成 220.9 Hz），只能在频域纠正。
//
// 判据本身要能过合成信号自检：见 pitch_test.go（95/110/130/190/210/245 Hz 六档误差 ≤6%）
// 与 pitch_octave_test.go（强谐波帧不许读高八度、搜索下界端点 400 Hz）。
//
// 依赖方向与包内其它文件一致：只依赖 internal/ff。
package audio

import (
	"context"
	"fmt"
	"math"
	"os"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// 基频分析的帧长/帧移。40ms 至少覆盖 70 Hz 的三个周期；10ms 的帧移让"换气/换人"
// 这种几十毫秒的变化不至于被平均掉。
const (
	PitchFrameSec = 0.040
	PitchHopSec   = 0.010
	// PitchMinHz/PitchMaxHz 搜索范围：人声基频的正常边界（低于 70 Hz 是次声/隆隆声，
	// 高于 400 Hz 是儿童尖叫/金属噪声，都不是本平台要判的东西）。
	PitchMinHz = 70.0
	PitchMaxHz = 400.0
)

const (
	// pitchVoicedFloor 一帧算"有人在说话"的 RMS 门限（约 -46 dBFS）。
	pitchVoicedFloor = 0.005
	// pitchPeakFloor 自相关峰值门限（低于它视为没有可靠基频）。
	pitchPeakFloor = 0.30
)

// PitchTrack 逐帧基频轨迹。F0[i] == 0 表示该帧没有可靠基频（静音/清音/噪声）。
type PitchTrack struct {
	Hop float64
	F0  []float64
}

// PitchFromPCM 由 s16le 单声道 PCM 估计逐帧基频。
//
// 纯函数、不碰 ffmpeg：合成信号（已知基频的脉冲串）可以直接喂进来做自检，
// 见 pitch_test.go —— 判据本身的正确性必须能在没有真机素材的机器上复现。
func PitchFromPCM(raw []byte, rate int, hop float64) *PitchTrack {
	t := &PitchTrack{Hop: hop}
	if rate <= 0 {
		return t
	}
	if hop <= 0 {
		hop = PitchHopSec
	}
	n := len(raw) / 2
	frame := int(PitchFrameSec * float64(rate))
	if frame < 8 {
		frame = 8
	}
	step := int(hop * float64(rate))
	if step < 1 {
		step = 1
	}
	if n < frame {
		return t
	}
	s := make([]float64, n)
	for i := 0; i < n; i++ {
		s[i] = float64(int16(uint16(raw[i*2])|uint16(raw[i*2+1])<<8)) / 32768.0
	}
	win := hann(frame)
	lo := int(float64(rate) / PitchMaxHz)
	hi := int(float64(rate) / PitchMinHz)
	if lo < 1 {
		lo = 1
	}
	if hi > frame-2 {
		hi = frame - 2
	}
	buf := make([]float64, frame)
	for start := 0; start+frame <= n; start += step {
		for i := 0; i < frame; i++ {
			buf[i] = s[start+i]
		}
		t.F0 = append(t.F0, frameF0(buf, win, rate, lo, hi))
	}
	return t
}

// frameF0 单帧基频（Hz）；返回 0 表示这一帧没有可靠基频。
func frameF0(buf, win []float64, rate, lo, hi int) float64 {
	var sum float64
	for _, v := range buf {
		sum += v * v
	}
	if math.Sqrt(sum/float64(len(buf))) < pitchVoicedFloor {
		return 0
	}
	var mean float64
	for _, v := range buf {
		mean += v
	}
	mean /= float64(len(buf))
	x := make([]float64, len(buf))
	for i := range buf {
		x[i] = (buf[i] - mean) * win[i]
	}
	var e0 float64
	for _, v := range x {
		e0 += v * v
	}
	if e0 <= 1e-12 || hi <= lo {
		return 0
	}
	// 归一化互相关（NCC）：分母取**两段重叠区各自的能量开方**，而不是整帧能量。
	//
	// 【2026-10-08 审查发现的真缺陷】原来是"整帧能量做分母"：汉宁窗把长 lag 的重叠区
	// 压小，分母却不跟着变小 ⇒ 半周期的峰被系统性抬高。实测（合成信号，见
	// pitch_octave_test.go）：110 Hz 基频 + 幅度 4 倍的二次谐波，被读成 **220.9 Hz**
	// （真值 110）—— 男声卡于是被机器判成"念成女声"，闭环据此换种子重出、白烧 GPU。
	// **判据自己读错一个八度 = 拿假证据做决定**，这一处必须修。
	ac := make([]float64, hi+1)
	for lag := 0; lag <= hi; lag++ {
		var num, e1, e2 float64
		for i := lag; i < len(x); i++ {
			num += x[i] * x[i-lag]
			e1 += x[i] * x[i]
			e2 += x[i-lag] * x[i-lag]
		}
		if e1 > 1e-12 && e2 > 1e-12 {
			ac[lag] = num / math.Sqrt(e1*e2)
		}
	}
	peak := 0.0
	for lag := lo; lag <= hi; lag++ {
		if ac[lag] > peak {
			peak = ac[lag]
		}
	}
	if peak < pitchPeakFloor {
		return 0
	}
	// 八度纠正：取**周期最短**的那个"足够强的局部峰"。
	// 为什么：真正的周期在整数倍 lag 处都是同样高的峰，直接取最大的那一个会把高音读低
	// 一个八度（真机调试时 245 Hz 的女声被读成 122 Hz）。
	//
	// 光有"第一个强峰"还不够，必须再问一句"这个峰会不会是二次谐波造的假周期"：
	// 合成夹具（110 Hz 基频 + 幅度 4 倍的二次谐波）实测半周期峰高 0.882×主峰，
	// 离 0.90 的门只差 2% —— 门限卡在理论值旁边，窗的偏差就能把它推过去（实测确实读成
	// 220.9 Hz）。所以加一条**结构性**判据：候选 lag 若"两倍周期处明显更相关"
	// （ac[2L] > ac[L]×1.02），说明真正的周期是 2L、L 只是谐波假象，跳过它继续找。
	// 真人的周期信号在 2T 处只会等于或略低于 T 处（抖动让它更低），不会被这条误伤。
	//
	// 边界处理：扫到 lo（= rate/PitchMaxHz）**含端点**。原来从 lo+1 起扫，等于把恰好
	// 落在搜索下界的 400 Hz 纯音排除掉，实测被读成 200.3 Hz（低一个八度）。
	best := -1
	for lag := lo; lag <= hi; lag++ {
		if ac[lag] < 0.85*peak {
			continue
		}
		if lag > lo && ac[lag] < ac[lag-1] {
			continue
		}
		if lag < hi && ac[lag] < ac[lag+1] {
			continue
		}
		if 2*lag <= hi && ac[2*lag] > ac[lag]*1.02 {
			continue
		}
		best = lag
		break
	}
	if best < 0 {
		return 0
	}
	lag := float64(best)
	if best > 0 && best < hi {
		a, b, c := ac[best-1], ac[best], ac[best+1]
		den := a - 2*b + c
		if math.Abs(den) > 1e-12 {
			lag += 0.5 * (a - c) / den
		}
	}
	if lag <= 0 {
		return 0
	}
	f := float64(rate) / lag
	if f < PitchMinHz*0.9 || f > PitchMaxHz*1.1 {
		return 0
	}
	// 频域校验（HPS）：自相关在"基频比二次谐波弱"的帧上会读**高**一个八度 ——
	// 合成夹具实录：110 Hz 基频 + 幅度 4 倍的二次谐波，自相关剖面 lag=72（222 Hz）
	// 得 0.81、lag=145（110 Hz）只 0.72（强谐波把窗内相关主导了，实测见
	// zz_probe_octave_test.go 的剖面打印）。这一处只能在频域判：
	// 分别量 f、f/2、f/4 三个候选的**谐波和**（能量落在 k·f0 上的多少），
	// 只有当低八度候选的分数明显更高（>1.15×）才改判 ⇒ 不是"抽卡"，是有证据才纠正。
	//
	// **只往下查，不往上查**：往上查会把"二次谐波强的低音"整体抬高一个八度
	// （夹具实测：score(220)/score(110)=1.33 > 1.15，翻上去就错到底），
	// 而自相关本身的"取周期最短的强峰"已经是防低一个八度的那一半。
	f = correctOctaveUp(x, float64(rate), f)
	return f
}

// goertzelMag 单频幅度（帧内，已加窗）。只依赖 math，确定性、无依赖。
func goertzelMag(x []float64, rate, hz float64) float64 {
	if hz <= 0 || hz*2 >= rate {
		return 0
	}
	w := 2 * math.Pi * hz / rate
	c := 2 * math.Cos(w)
	var s1, s2 float64
	for _, v := range x {
		s0 := v + c*s1 - s2
		s2, s1 = s1, s0
	}
	re := s1 - s2*math.Cos(w)
	im := s2 * math.Sin(w)
	return math.Hypot(re, im) / float64(len(x))
}

// harmonicScore 候选基频的谐波和（k=1..4，等权；k·f0 超出奈奎斯特就停）。
// 等权而不是 1/k 加权：夹具里"110 候选"的分要能压住"220 候选"，
// 靠的正是把 220 这个**强二次谐波**按整权记进 110 的账上（0.25+1.0 > 1.0）。
func harmonicScore(x []float64, rate, f0 float64) float64 {
	var s float64
	for k := 1; k <= 4; k++ {
		s += goertzelMag(x, rate, f0*float64(k))
	}
	return s
}

// correctOctaveUp 把"被强谐波顶高了一个八度"的读数拉回真值；没有证据就原样返回。
func correctOctaveUp(x []float64, rate, f float64) float64 {
	best, bestScore := f, harmonicScore(x, rate, f)
	if bestScore <= 0 {
		return f
	}
	for _, div := range []float64{2, 4} {
		cand := f / div
		if cand < PitchMinHz {
			break
		}
		if sc := harmonicScore(x, rate, cand); sc > bestScore*1.15 {
			best, bestScore = cand, sc
		}
	}
	if best < PitchMinHz || best > PitchMaxHz*1.1 {
		return f
	}
	return best
}

// hann 汉宁窗（避免自相关被帧边界的突变带偏）。
func hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

// Voiced 取出可靠的基频帧（Hz，升序无关，按时间顺序）。
func (p *PitchTrack) Voiced() []float64 {
	if p == nil {
		return nil
	}
	out := make([]float64, 0, len(p.F0))
	for _, f := range p.F0 {
		if f > 0 {
			out = append(out, f)
		}
	}
	return out
}

// VoicedSec 可靠基频覆盖的时长（秒）—— 也是"这条判据有没有足够素材"的分母。
func (p *PitchTrack) VoicedSec() float64 {
	if p == nil {
		return 0
	}
	hop := p.Hop
	if hop <= 0 {
		hop = PitchHopSec
	}
	return float64(len(p.Voiced())) * hop
}

// MedianHz 可靠基频的中位数；没有可靠帧时返回 0。
//
// 用中位数而不是均值：一句话里有升调问句、有重音，均值会被少数几帧拉走，
// 而中位数描述的是"这个人的常态音高"，正是不该因人而异的那个量。
func (p *PitchTrack) MedianHz() float64 {
	v := p.Voiced()
	if len(v) == 0 {
		return 0
	}
	sorted := make([]float64, len(v))
	copy(sorted, v)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	m := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[m]
	}
	return (sorted[m-1] + sorted[m]) / 2
}

// DecodePitch 用 ffmpeg 把音轨解码成 16kHz 单声道 PCM 再估基频。
//
// 无音轨/解码失败都返回错误，调用方据此记成"没验过"（绝不当作通过）。
func DecodePitch(ctx context.Context, f *ff.FF, path string) (*PitchTrack, error) {
	if f == nil {
		return nil, ff.Missing("ffmpeg")
	}
	tmp, err := os.CreateTemp("", "mx-pitch-*.pcm")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error", "-i", path,
		"-vn", "-ac", "1", "-ar", fmt.Sprint(sampleRate), "-f", "s16le", tmpPath); err != nil {
		return nil, fmt.Errorf("解码音轨失败: %w", err)
	}
	raw, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, err
	}
	return PitchFromPCM(raw, sampleRate, PitchHopSec), nil
}
