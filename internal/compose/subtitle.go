package compose

import (
	"fmt"
	"math"
	"strings"

	"github.com/YiIimini/ManJuX/internal/audio"
	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 字幕落点对齐(乙4)。此前字幕按"镜头时长均分"铺开(每镜头尾各留 10%,镜内均分),
// 只到**镜头级**:台词前 2 秒就说完了,字幕还挂在后面 5 秒;镜尾那句又提前压在画面上。
//
// 现在用 internal/audio 的人声包络(接缝静音求解已经在算的那一份)把每句字幕钉在
// **真实发声段落**上。对齐条件刻意保守:只有"人声段落数 == 字幕条数"才逐条对应;
// 对不上(包络缺失、解不出、条数不匹配)一律**回退到镜头级窗口**,并如实记一条 note
// —— 退化了要说出来,不能让人以为"这版字幕对上了"。

// minSpeechSpan 判定为"一句话"的最短人声段(秒)。短于此的多是换气/唇齿音,
// 把它们算作一段只会让"段落数 == 条数"这条对齐条件更容易落空。
const minSpeechSpan = 0.12

// 字幕相对人声段落的提前/延后量(秒):门限检测出的起点略晚、收尾略早,
// 贴太紧会"话都出来了字才跟上"。
const (
	cueLead  = 0.08
	cueTrail = 0.15
)

// minCueSpan 一条字幕的最短显示时长(秒)。
const minCueSpan = 0.4

// cueSpan 一条字幕的落点(相对成片起点,秒)。
type cueSpan struct {
	Start, End float64
}

// speechTrack 某镜的人声信息:包络 + 该镜在成片里**实际可听**的音频区间。
//
// 为什么要 From/To:叠化转场会让每镜让出一点音频(见 seamAudioWindows),被让出的
// 那一段在成片里根本听不到 —— 按它排字幕,等于给观众看一段没有声音的字。
// 硬切时 From/To 就是 0/镜头时长。
type speechTrack struct {
	Env  *audio.Envelope
	From float64
	To   float64
}

// cuePlan 全片字幕落点计划(逐镜、逐句),以及对不齐时的如实回显。
type cuePlan struct {
	byShot [][]cueSpan
	notes  []string
}

// speechSpans 取 [from,to] 内的人声段落(秒,相对镜头起点)。无包络返回 nil。
func speechSpans(env *audio.Envelope, from, to float64) []cueSpan {
	if env == nil || to <= from {
		return nil
	}
	on := env.Speech()
	if len(on) == 0 {
		return nil
	}
	hop := env.Hop
	if hop <= 0 {
		hop = audio.Hop
	}
	var out []cueSpan
	for i := 0; i < len(on); {
		if !on[i] {
			i++
			continue
		}
		j := i
		for j < len(on) && on[j] {
			j++
		}
		s, e := float64(i)*hop, float64(j)*hop
		i = j
		if s < from {
			s = from
		}
		if e > to {
			e = to
		}
		// 夹进可听区间后仍要够长才算一段
		if e-s >= minSpeechSpan {
			out = append(out, cueSpan{Start: s, End: e})
		}
	}
	return out
}

// fallbackCues 镜头级窗口:与 subtitleWindow 同源,镜内按时长均分 count 条。
// 这就是"包络不可用"时的既有行为(审计 M36 的 SRT/ASS 同窗口径),一个字不改。
func fallbackCues(filmStart, d float64, count int) []cueSpan {
	out := make([]cueSpan, count)
	if count <= 0 {
		return out
	}
	ws, we := subtitleWindow(d)
	seg := (we - ws) / float64(count)
	for j := range out {
		out[j] = cueSpan{
			Start: filmStart + ws + seg*float64(j),
			End:   filmStart + ws + seg*float64(j+1),
		}
	}
	return out
}

// alignCues 求某镜 count 条字幕的落点(相对成片起点,秒);第二个返回值表示是否真的
// 落在了真实人声上(false = 已回退镜头级窗口)。
//
// 为什么条数不符时不"按比例摊":把 3 句台词摊到 2 个人声段上,落点看着合理、实际每句
// 都错位,而且不会有人发现。宁可回退到与旧版一致的窗口,并把退化播报出来。
func alignCues(filmStart, d float64, tr speechTrack, count int) ([]cueSpan, bool) {
	if count <= 0 {
		return nil, false
	}
	spans := speechSpans(tr.Env, tr.From, tr.To)
	if len(spans) != count {
		return fallbackCues(filmStart, d, count), false
	}
	// 可听区间(相对成片):字幕不许越出这段 —— 越出去的那部分是静音或已被接缝切掉。
	lo, hi := filmStart+tr.From, filmStart+tr.To
	if hi <= lo {
		return fallbackCues(filmStart, d, count), false
	}
	if lim := filmStart + d; hi > lim {
		hi = lim
	}
	out := make([]cueSpan, count)
	for j, sp := range spans {
		s := filmStart + sp.Start - cueLead
		e := filmStart + sp.End + cueTrail
		if s < lo {
			s = lo
		}
		if e > hi {
			e = hi
		}
		if e-s < minCueSpan {
			e = math.Min(s+minCueSpan, hi)
			if e-s < minCueSpan && s > lo {
				s = math.Max(lo, e-minCueSpan)
			}
		}
		out[j] = cueSpan{Start: s, End: e}
	}
	// 前后留白把相邻两条推到重叠时,压回前一条的收尾(两句同屏叠字比留白难看)
	for j := 0; j+1 < len(out); j++ {
		if out[j].End > out[j+1].Start-0.02 {
			out[j].End = math.Max(out[j].Start+0.05, out[j+1].Start-0.02)
		}
	}
	return out, true
}

// shotSpans 取某镜的落点:有对齐计划且条数吻合就用计划,否则回退镜头级窗口。
// (SRT 与 ASS 的条数都来自 director.ShotSubtitleParts,理论上恒等;这里是防止两边
// 分裂的最后一道,分裂时退回旧行为而不是越界取下标。)
// 用指针接收者是为了容忍 nil(= 没有落点计划,全走镜头级窗口:BuildSRT/BuildASS 的公开入口)。
func (cp *cuePlan) shotSpans(i int, filmStart, d float64, count int) []cueSpan {
	if cp != nil && i >= 0 && i < len(cp.byShot) && len(cp.byShot[i]) == count {
		return cp.byShot[i]
	}
	return fallbackCues(filmStart, d, count)
}

// planCues 依人声落点排全片字幕。tracks 与 plan.Shots 同序;缺项按"无包络"处理。
func planCues(plan *project.Plan, starts, durations []float64, tracks []speechTrack) cuePlan {
	var cp cuePlan
	if plan == nil {
		return cp
	}
	cp.byShot = make([][]cueSpan, len(plan.Shots))
	var noEnv, mismatch []int
	total, aligned := 0, 0
	for i, shot := range plan.Shots {
		count := len(director.ShotSubtitleParts(shot))
		if count == 0 {
			continue // 该镜没有字幕,不需要落点
		}
		d := 7.0
		if i < len(durations) {
			d = durations[i]
		}
		t := 0.0
		if i < len(starts) {
			t = starts[i]
		}
		var tr speechTrack
		if i < len(tracks) {
			tr = tracks[i]
		}
		spans, ok := alignCues(t, d, tr, count)
		cp.byShot[i] = spans
		total++
		if ok {
			aligned++
			continue
		}
		if tr.Env == nil {
			noEnv = append(noEnv, i+1)
		} else {
			mismatch = append(mismatch, i+1)
		}
	}
	if aligned > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"字幕落点:%d/%d 镜按真实人声对齐(其余按镜头级窗口铺排)", aligned, total))
	}
	if len(mismatch) > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"⚠ 第 %s 镜的人声段落数与台词句数不符,字幕已回退镜头级窗口(镜内均分)", joinInts(mismatch)))
	}
	if len(noEnv) > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"⚠ 第 %s 镜没有可用的人声包络,字幕已回退镜头级窗口", joinInts(noEnv)))
	}
	return cp
}

// hasCueText 全片是否存在任何一条字幕。没有字幕时不必为对齐白解一遍全片音轨。
func hasCueText(plan *project.Plan) bool {
	if plan == nil {
		return false
	}
	for _, shot := range plan.Shots {
		if strings.TrimSpace(director.ShotSubtitle(shot)) != "" {
			return true
		}
	}
	return false
}
