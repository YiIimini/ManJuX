package compose

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/asr"
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

// ── 字幕落点的四档来源（2026-10-09 用户：「对话的字幕显示存在延迟」）──────────────
//
// 【真机取证】《我捡的团子·第01集》的 final.srt：镜 1 的真实发声是 ASR 量到的
// `[3.50-4.50] 活的。`（1 秒），而字幕挂的是 `00:00,837 → 00:07,037` —— **6.2 秒**，
// 整镜铺满。原因是只用"人声包络"这一档，包络不可用（或段数不匹配）时**整镜**当窗口。
// 而平台**本来就有**逐镜 ASR 转写（质检那一步跑的就是它，文件就在剪辑旁 `<clip>.asr.txt`），
// 里面是**实测的发声时间**。于是补上第二档：
//
//	① 人声包络段落 == 句数 ⇒ 逐句精确对应（原有的一档，最准）
//	② ASR 段落 == 句数     ⇒ 逐句精确对应（新增；ASR 就是量出来的发声时间）
//	③ ASR 有段落但条数不符 ⇒ 字幕**只压在 ASR 的人声区间内**、句内按字数推（新增）
//	④ 两条都没有证据       ⇒ 镜头级窗口（与旧版一致）+ 如实说是哪几镜
//
// ③ 是刻意保守后的中间档：**不说"对齐了"**（条数不符就是没对齐），但至少不让字幕铺满整个镜头
// ——观众看到的是"话在人声区间里"，而不是"字幕挂了一整镜、声音早就没了"。

// cueSource 一条字幕落点的来源（播报要如实说清用了哪一档）。
type cueSource int

const (
	// cueAlign ⓪ **词级对齐**（2026-10-09 新增，最高一档）：把**已知台词**逐句对到 whisper
	// 给出的词级时间戳上 —— 这是业界标准 forced alignment 的最省一档（WhisperX / stable-ts
	// 走的是同一件事，见 internal/asr/align.go 的文件头）。它比②强的正是真机那种形态：
	// **段数与句数不符**（镜 10/11）时，②只能把窗口收进人声区间，而词级能把每一句钉到它
	// 自己的那几个词上。
	cueAlign      cueSource = iota
	cueEnvelope             // ① 人声包络（逐句精确）
	cueASR                  // ② ASR 段落（逐句精确）
	cueASRWindow            // ③ 只用 ASR 的人声区间、句内按字数推
	cueShotWindow           // ④ 镜头级窗口（没有可用的发声证据）
)

// asrSpansOf 读某镜的 ASR 转写（`<clip>.asr.txt`），把段落换算成**相对镜头起点**的秒区间。
//
// 读不到 / 解析失败一律返回 nil —— 那是"没有证据"，不是"这一镜没台词"（后者由 QC 的
// 语音对账负责播报）；没有证据就退回④，不许用空结果去猜。
func asrSpansOf(clipPath string) ([]cueSpan, []asr.Word) {
	if strings.TrimSpace(clipPath) == "" {
		return nil, nil
	}
	base := strings.TrimSuffix(clipPath, filepath.Ext(clipPath))
	r, err := asr.Parse(base + ".asr.txt")
	if err != nil || r == nil {
		return nil, nil
	}
	var out []cueSpan
	for _, s := range r.Segments {
		if s.End-s.Start < minSpeechSpan {
			continue
		}
		out = append(out, cueSpan{Start: s.Start, End: s.End})
	}
	// 词级时间戳是**升级版 ASR 工具**（`word_timestamps=True`）才写的；老结果文件里没有
	// ⇒ 老项目这里就是 nil，自动退回②③④。这一条刻意**不**播报"缺失"：那是工具版本，
	// 不是这一集的错（与"该念没念"那种必须点名的东西分得开）。
	return out, r.Words
}

// cueWeights 每句字幕的**字数**（用于③里"句内按时长推"）。没有字数信息时一律给 1（等分）。
func cueWeights(parts []string) []int {
	w := make([]int, len(parts))
	for i, p := range parts {
		n := len([]rune(strings.TrimSpace(p)))
		if n < 1 {
			n = 1
		}
		w[i] = n
	}
	return w
}

// splitWindowCues 把 count 条字幕铺在 [lo,hi] 里，按 weights 比例分（缺权重就等分）。
// 这是③那一档的实现：位置是**推**的，所以调用方必须把它标成 cueASRWindow 并播报。
func splitWindowCues(lo, hi float64, weights []int, count int) []cueSpan {
	out := make([]cueSpan, count)
	if count <= 0 || hi <= lo {
		return out
	}
	den := 0
	for j := 0; j < count; j++ {
		if j < len(weights) && weights[j] > 0 {
			den += weights[j]
		} else {
			den++
		}
	}
	if den <= 0 {
		den = count
	}
	cur := lo
	for j := 0; j < count; j++ {
		w := 1
		if j < len(weights) && weights[j] > 0 {
			w = weights[j]
		}
		span := (hi - lo) * float64(w) / float64(den)
		out[j] = cueSpan{Start: cur, End: math.Min(cur+span, hi)}
		cur += span
	}
	return out
}

// alignCues 求某镜 count 条字幕的落点(相对成片起点,秒),并说明用的是**哪一档来源**。
//
// 为什么条数不符时不"按比例摊"到人声段上:把 3 句台词摊到 2 个人声段上,落点看着合理、
// 实际每句都错位,而且不会有人发现。所以③那一档只做一件事:**把窗口收进人声区间**
// （句内位置是推的,会标成 cueASRWindow 如实说明),而不是假装逐句对齐。
// ⓪（词级对齐，2026-10-09）之后这一条**不那么常触发了**：段数与句数不符的镜靠词级就能
// 逐句落点（真机镜 10/11），③留给"连词级也对不上"的镜（那通常意味着台词真的没念全）。
func alignCues(filmStart, d float64, tr speechTrack, asrSpans []cueSpan, words []asr.Word,
	parts []string, weights []int, count int) ([]cueSpan, cueSource) {
	if count <= 0 {
		return nil, cueShotWindow
	}
	lo, hi := filmStart+tr.From, filmStart+tr.To
	if hi <= lo {
		lo, hi = filmStart, filmStart+d
	}
	if lim := filmStart + d; hi > lim {
		hi = lim
	}
	// ⓪ 词级对齐（2026-10-09，业界标准 forced alignment 的可用档）：把**已知台词**逐句对到
	// 词流上。放在最前面是因为它是**唯一**能处理"段数与句数不符"的证据源（真机镜 10/11），
	// 而包络那一档在真机整集上都退化了（逐镜都判成"段落数不符"）。
	// 认不出来（比例不够）就往下走 —— 不许硬凑一个落点：那会把"这句根本没念"盖住。
	if spans, ok := asr.AlignLines(stripSpeakers(parts), words); ok && len(spans) == count {
		rel := make([]cueSpan, 0, len(spans))
		for _, sp := range spans {
			rel = append(rel, cueSpan{Start: sp.Start, End: sp.End})
		}
		return clampCues(filmStart, lo, hi, rel), cueAlign
	}
	// ① 人声包络：逐句精确（原有那一档，最准）
	if spans := speechSpans(tr.Env, tr.From, tr.To); len(spans) == count {
		return clampCues(filmStart, lo, hi, spans), cueEnvelope
	}
	// ②③ ASR：实测的发声时间。段数与句数相等 ⇒ 逐句精确；不等 ⇒ 只收窗口、句内按时长推。
	if len(asrSpans) > 0 {
		if len(asrSpans) == count {
			return clampCues(filmStart, lo, hi, asrSpans), cueASR
		}
		from := math.Max(lo, filmStart+asrSpans[0].Start)
		to := math.Min(hi, filmStart+asrSpans[len(asrSpans)-1].End)
		if to-from >= minCueSpan {
			return splitWindowCues(from, to, weights, count), cueASRWindow
		}
	}
	// ④ 没有可用的发声证据：与旧版一致的镜头级窗口（调用方会如实播报是哪几镜）。
	return fallbackCues(filmStart, d, count), cueShotWindow
}

// clampCues 把逐条人声段落夹进可听区间 [lo,hi]，带上前后留白与最短显示时长。
//
// 为什么夹：叠化转场会让每镜让出一点音频，被让出的那段在成片里**根本听不到** ——
// 按它排字幕，等于给观众看一段没有声音的字。
func clampCues(filmStart, lo, hi float64, spans []cueSpan) []cueSpan {
	out := make([]cueSpan, len(spans))
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
	return out
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

// planCues 依人声落点排全片字幕。tracks / asrSpans / asrWords 与 plan.Shots 同序;缺项按
// "没有证据"处理。
//
// 三个证据源（人声包络、逐镜 ASR 段落、**词级对齐**）都要用上：包络量的是**这一镜自己的
// 音轨**，ASR 量的是**实际念出来的话与时间** —— 真机那一集正是只有 ASR 可用（包络这一档
// 对每镜都退化了），而字幕因此按整镜铺开（镜 1 挂 6.2 秒、真实发声只有 1 秒），用户看到的
// 就是"字幕与声音对不上"。词级对齐（2026-10-09）补的是最后那一截：**段数与句数不符**的镜
// （真机镜 10/11）也能逐句落点，而不是只把窗口收进人声区间。
func planCues(plan *project.Plan, starts, durations []float64, tracks []speechTrack,
	asrSpans [][]cueSpan, asrWords [][]asr.Word) cuePlan {
	var cp cuePlan
	if plan == nil {
		return cp
	}
	cp.byShot = make([][]cueSpan, len(plan.Shots))
	var noEvidence, asrWindow, mismatch []int
	total, byAlign, byEnvelope, byASR := 0, 0, 0, 0
	for i, shot := range plan.Shots {
		parts := director.ShotSubtitleParts(shot)
		count := len(parts)
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
		// 注意：局部变量不叫 `asr` —— 那会**遮蔽包名** internal/asr，`asr.Word` 就解析不了了。
		var asrSeg []cueSpan
		if i < len(asrSpans) {
			asrSeg = asrSpans[i]
		}
		var words []asr.Word
		if i < len(asrWords) {
			words = asrWords[i]
		}
		spans, src := alignCues(t, d, tr, asrSeg, words, parts, cueWeights(parts), count)
		cp.byShot[i] = spans
		total++
		switch src {
		case cueAlign:
			byAlign++
		case cueEnvelope:
			byEnvelope++
		case cueASR:
			byASR++
		case cueASRWindow:
			asrWindow = append(asrWindow, i+1)
		default:
			if tr.Env != nil {
				mismatch = append(mismatch, i+1)
			} else {
				noEvidence = append(noEvidence, i+1)
			}
		}
	}
	if byAlign > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"字幕落点:%d/%d 镜按**词级对齐**逐句落点（已知台词对到 whisper 的词级时间戳，"+
				"段数与句数不符也逐句精确）", byAlign, total))
	}
	if byEnvelope > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"字幕落点:%d/%d 镜按**人声包络**逐句对齐", byEnvelope, total))
	}
	if byASR > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"字幕落点:%d/%d 镜按 **ASR 转写段落**逐句对齐（实测发声时间）", byASR, total))
	}
	if len(asrWindow) > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"⚠ 第 %s 镜的 ASR 段落数与台词句数不符,字幕**只压在人声区间内**、句内位置按字数推"+
				"（没假装对齐 —— 条数不符就是没对齐)", joinInts(asrWindow)))
	}
	if len(mismatch) > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"⚠ 第 %s 镜的人声段落数与台词句数不符,且没有 ASR 可依,字幕已回退镜头级窗口(镜内均分)",
			joinInts(mismatch)))
	}
	if len(noEvidence) > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf(
			"⚠ 第 %s 镜**既没有可用的人声包络、ASR 也没转出任何段落** —— 字幕按镜头级窗口铺,"+
				"这几句很可能**根本没被念出来**（语音对账那份报告里同样会点名）", joinInts(noEvidence)))
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

// stripSpeakers 把字幕文本里的**说话人前缀**剥掉（`姜晚舟:活的。` ⇒ `活的。`）。
//
// 【为什么必须剥（真机实测，2026-10-09）】词级对齐全靠"已知台词 ↔ 词流"的字符命中率，
// 而前缀里那个人名**不在音轨里**（H3 只念台词本身）⇒ 它被算成一个错字：真机那句
// 「姜晚舟:活的。」= 6 个字里错 1 个，命中比例掉到 0.83 —— 看着还够，但四字以上的人名
// （如「郑大勇:工牌呢。」）就会掉到 0.5 以下 ⇒ 整镜退回段级档，词级这一档白做。
//
// 前缀的拼装方式见 `director.ShotSubtitleParts`（`Speaker + ":" + Text`，半角冒号）。
// 只认"冒号在 12 字以内、前半段不含句读"这一种形状 —— 台词里正常出现的冒号（时间、
// 引语）不会被误剥。
func stripSpeakers(parts []string) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = stripSpeakerPrefix(p)
	}
	return out
}

func stripSpeakerPrefix(s string) string {
	i := strings.IndexAny(s, ":：") // 半角为主，全角容忍
	if i <= 0 || i > 12 {
		return s
	}
	if strings.ContainsAny(s[:i], "。！？，、!?") {
		return s
	}
	rest := strings.TrimSpace(s[i+1:])
	if rest == "" {
		return s
	}
	return rest
}
