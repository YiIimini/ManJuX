// Package audio 分析单镜音轨的能量包络,并据此为成片拼接挑出**落在静音里**的切点。
//
// 为什么需要它(2026-09-24 用户报「配音也不完整」):叠化转场下,两镜的音频必须
// 让出与画面重叠等长的一段时间(否则整条音轨会比画面长 (n-1)×转场时长,越到后面
// 越不同步)。旧实现把这段时间平均地砍在重叠区中点 —— 也就是**每一镜的头尾各砍掉
// 0.25 秒**。台词往往正好压在镜头头尾(旁白起手、末句收尾),于是每镜都被切掉一点,
// 成片听起来就是"话没说完"。
//
// 正解不是"别砍"(必须砍,总时长是硬约束),而是**砍在静音上**:接缝窗口里两镜各有
// 一段可裁区间,把要丢掉的时长尽量落在没有人声的地方,人声一个字都不丢。本包只做
// 这件事:包络计算 + 切点求解,纯函数、可单独测(见 audio_test.go)。
//
// 依赖方向:audio → ff(底层基础设施),不反向。
package audio

import (
	"context"
	"fmt"
	"math"
	"os"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// Hop 包络的每格时长(秒)。20ms 与一帧视频同量级:足够定位音节边界,
// 又不至于让 10 秒的镜头产生过多格子。
const Hop = 0.02

// sampleRate 解码采样率。语音能量分析不需要高采样率,16k 单声道即可,
// 数据量约 32KB/秒 —— 一次解码一整集也不过几 MB。
const sampleRate = 16000

// Envelope 单条音轨的能量包络。
type Envelope struct {
	Hop float64   // 每格时长(秒)
	RMS []float64 // 每格 RMS(0-1 满量程)
}

// Duration 包络覆盖的时长(秒)。
func (e *Envelope) Duration() float64 {
	if e == nil {
		return 0
	}
	return float64(len(e.RMS)) * e.Hop
}

// Decode 用 ffmpeg 把音轨解码成 16kHz 单声道 PCM 并计算包络。
// 无音轨/解码失败都会返回错误,调用方据此回退到旧行为(中点切)。
func Decode(ctx context.Context, f *ff.FF, path string) (*Envelope, error) {
	if f == nil {
		return nil, fmt.Errorf("未配置 ffmpeg")
	}
	tmp, err := os.CreateTemp("", "mx-audio-*.pcm")
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
	return envelopeFromPCM(raw, sampleRate, Hop), nil
}

// envelopeFromPCM 由 s16le 单声道 PCM 计算 RMS 包络(单独成函数便于无 ffmpeg 单测)。
func envelopeFromPCM(raw []byte, rate int, hop float64) *Envelope {
	per := int(float64(rate) * hop)
	if per < 1 {
		per = 1
	}
	n := len(raw) / 2
	e := &Envelope{Hop: hop}
	e.RMS = make([]float64, 0, n/per+1)
	for start := 0; start+per <= n; start += per {
		var sum float64
		for i := start; i < start+per; i++ {
			v := float64(int16(uint16(raw[i*2])|uint16(raw[i*2+1])<<8)) / 32768.0
			sum += v * v
		}
		e.RMS = append(e.RMS, math.Sqrt(sum/float64(per)))
	}
	return e
}

// Speech 返回每格是否"有人声"。
//
// 自适应门限:先取能量的 20 分位作为底噪估计,门限 = 底噪 + 9dB,再夹到
// [-55dB, -30dB]。为什么不用固定阈值:H3 每个镜头的整体响度、环境底噪都不一样,
// 固定阈值会在安静镜头里把底噪全判成人声、在响的镜头里漏掉轻声台词。
// 为什么还要夹上限:整镜都是台词、一个静音格都没有时,20 分位也落在人声上,
// "底噪+9dB"会算出一个谁也够不着的高门限 —— 那时用 -30dB 兜底,避免把满是人声的
// 镜头误判成全程无人声(那会让台词完整性检查凭空报警)。
//
// 再做两遍清理:去抖(连续 3 格以下的有声段视为爆音/click 丢弃)与补洞(100ms 以内的
// 无声缝隙填回有声 —— 汉字之间的停顿不该把人声切碎)。
func (e *Envelope) Speech() []bool {
	if e == nil || len(e.RMS) == 0 {
		return nil
	}
	db := make([]float64, len(e.RMS))
	for i, r := range e.RMS {
		if r <= 1e-9 {
			db[i] = -120
			continue
		}
		db[i] = 20 * math.Log10(r)
	}
	sorted := append([]float64(nil), db...)
	sortFloats(sorted)
	floor := sorted[len(sorted)/5]
	thr := floor + 9
	if thr < -55 {
		thr = -55
	}
	if thr > -30 {
		thr = -30
	}
	on := make([]bool, len(db))
	for i := range db {
		on[i] = db[i] > thr
	}
	fillShortGaps(on, int(0.10/e.Hop)) // 人声内部的短停顿 → 算人声
	dropShortRuns(on, int(0.06/e.Hop)) // 眨眼即逝的爆音 → 不算人声
	return on
}

// SpeechSeconds 统计 [from,to) 区间内被判为人声的时长(秒);区间按需夹到有效范围。
//
// 两端都用**四舍五入**取格号(而不是下取整/上取整):相邻的两个区间共享同一个边界
// 格号,统计不会重叠也不会漏格。用 floor/ceil 的话边界那一格会被两侧各算一次,
// 于是"让人声最少"的求解会因为凑整误差偏向某个方向(实测会让切点系统性地偏 0.05 秒)。
func (e *Envelope) SpeechSeconds(on []bool, from, to float64) float64 {
	if e == nil || len(on) == 0 {
		return 0
	}
	i0 := int(math.Round(from / e.Hop))
	i1 := int(math.Round(to / e.Hop))
	if i0 < 0 {
		i0 = 0
	}
	if i1 > len(on) {
		i1 = len(on)
	}
	cnt := 0
	for i := i0; i < i1; i++ {
		if on[i] {
			cnt++
		}
	}
	return float64(cnt) * e.Hop
}

// SolveCut 求接缝切点(相对窗口起点,秒 ∈ [0,width])。
//
// 约定(与 compose 的音频链一一对应):转场重叠区长度 width 必须从两镜里让出来。
// 切点在窗口内的位置 t 决定这段"丢失量"怎么分摊 ——
//
//	上一镜丢 [outFrom+t, outFrom+width],下一镜丢 [0, t]
//
// 于是 t 可以自由滑动:把丢失量尽量推到没有人声的那一侧,人声就一个字不丢。
// 评价函数 = 被丢掉的人声总时长,取最小;并列时取最接近 width/2 的解(与旧行为一致,
// 保证"全是人声"的极端情况下不引入新的不均衡)。
//
// 任一包络缺失时返回 width/2 —— 宁可回退也不猜。
func SolveCut(out *Envelope, outFrom float64, in *Envelope, inTo float64, width float64) float64 {
	if width <= 0 {
		return 0
	}
	if out == nil || in == nil || len(out.RMS) == 0 || len(in.RMS) == 0 {
		return width / 2
	}
	outOn, inOn := out.Speech(), in.Speech()
	step := Hop
	if out.Hop > 0 {
		step = out.Hop
	}
	best, bestCost := width/2, math.Inf(1)
	for t := 0.0; t <= width+1e-9; t += step {
		if t > width {
			t = width
		}
		cost := out.SpeechSeconds(outOn, outFrom+t, outFrom+width) + in.SpeechSeconds(inOn, 0, t)
		if cost < bestCost-1e-9 || (math.Abs(cost-bestCost) <= 1e-9 && math.Abs(t-width/2) < math.Abs(best-width/2)) {
			best, bestCost = t, cost
		}
		if t == width {
			break
		}
	}
	return best
}

// sortFloats 升序排序(只用于分位数取值,规模是几百~几千,插入排序足够且不引依赖)。
func sortFloats(a []float64) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && a[j] > v {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}

// fillShortGaps 把长度不超过 maxGap 的 false 段填成 true。
func fillShortGaps(on []bool, maxGap int) {
	for i := 0; i < len(on); i++ {
		if on[i] {
			continue
		}
		j := i
		for j < len(on) && !on[j] {
			j++
		}
		if i > 0 && j < len(on) && j-i <= maxGap {
			for k := i; k < j; k++ {
				on[k] = true
			}
		}
		i = j
	}
}

// dropShortRuns 把长度不超过 maxRun 的 true 段判回 false。
func dropShortRuns(on []bool, maxRun int) {
	for i := 0; i < len(on); i++ {
		if !on[i] {
			continue
		}
		j := i
		for j < len(on) && on[j] {
			j++
		}
		if j-i <= maxRun && i > 0 && j < len(on) {
			for k := i; k < j; k++ {
				on[k] = false
			}
		}
		i = j
	}
}
