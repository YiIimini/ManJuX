package asr

import (
	"fmt"
	"strings"
)

// compare.go 把「音轨里实际说的」与「计划里该说的」对账 —— 纯函数、可测。
//
// 【判据只报能证明的三类】（2026-10-07，真机第01集 13 镜标定，见 compare_real_test.go）
//
//	repeat  音轨里出现**不止一段**与某句台词相像的话
//	        （镜 4「已读取。无对应档位。」出现 2 段、镜 6「该钢印不在名册内。」2 段、
//	          镜 7「自检窗口，七十二标准时。」3 段 —— 剧本里每句只该出现一次）
//	extra   音轨里说了**计划之外的话**（H3 自己编的台词）
//	        （镜 3「跟强我的对数还修戴眼影一下,也续点倾向」、镜 1「没禁条」、
//	          镜 11「图墙白头圣呢﹔裙尾便宜着人」）
//	missing 音轨里**找不到与这句相像的内容**
//	        （镜 3 的第一句「80000。」一次都没念）
//
// 【为什么**不**报「念得不像」】中文 ASR 的同音字误差是常态（真机实测：异动/易动、钢印/刚硬、
// 自检/字简、已读取/以读曲、格线/隔线），字级差异当判据会把这些全判成缺陷 ⇒ 满屏假告警，
// 与「暗调戏被判黑屏」那一课同类。所以本包只把每句的**相似度**当数据摆出来（LineCheck.Sim），
// 由人看着决定要不要听 —— 机器不替人下「念得像不像」的结论。
//
// 同理，`extra` 的文案里必须写清「ASR 听出来的」：Whisper 在环境声/音乐上会幻觉出句子，
// 它是**需要人去听一遍的线索**，不是定论。
//
// 【数字的写法两边会不一样，必须折算】真机镜头 4 的台词是「钢印U-80001。」，ASR 把同一段
// 音轨写成「刚印右八零零一」（字母与数字全变成汉字）。不折算的话，这句与音轨的相似度只有 0.11
// —— 会被判成「整句没念」，而它其实念了。所以比对前把汉字数字折成阿拉伯数字（零〇一二三四五六七八九、幺），
// 两边同一口径后再比。这是 ASR 的书写习惯，不是剧本的问题。
//
// 【但报告里引的必须是原文】折算只用于比对：告警要贴的是 **ASR 原文**（「刚印右八零零一」），
// 而不是折算过的串（「刚印右8001」）—— 用户是照着这句话去听音轨的。见 normed 与它的 display。
const (
	// simRepeat 判「这一窗是不是某句台词的重复」的相似度下限。
	// 标定依据：念两遍时 ASR 与原文常有 1~3 字差（镜 4「以读曲无读对应档位」vs「已读取。无对应档位。」
	// 实测 0.67；镜 6「该刚印不在名册内」vs「该钢印不在名册内」0.875），而无关句子之间 ≤0.3。
	simRepeat = 0.60
	// simHit 判「这句在音轨里到底出现了没有」的相似度下限。
	// 真机 13 镜里**确实念了**的台词，最像的一窗最低 0.67（镜 8「U-79996」被念成「优势79997」那一类），
	// 而**整句没念**的（镜 3 的「80000」）最像的一窗是 0.00 —— 中间留了足够宽的余量。
	simHit = 0.55
	// minRepeatLen 参与「重复」计数的台词最短长度：太短的句子（「收到。」→「收到」两个字）
	// 在滑动窗口里跟什么都能碰上一半，会误报。
	minRepeatLen = 4
	// minExtraLen 自造台词的最短长度：短于 3 字的碎片（同音字替换留下的「是」「啊」）不报。
	minExtraLen = 3
	// wSlack 窗口宽度容差：ASR 会在句子里多吃/少吃一两个字（镜 4 的第二遍多出一个「读」），
	// 窗口比台词长/短几个字都算比较过。
	wSlack = 3
	// minWin 参与比较的最小窗口宽度。
	minWin = 2
)

// Finding 一条对账结论。
type Finding struct {
	Kind   string `json:"kind"`   // repeat / extra / missing
	Line   string `json:"line"`   // 相关的那句计划台词（extra 为空）
	Detail string `json:"detail"` // 人话（含 ASR 原文片段）
}

// LineCheck 某句计划台词在音轨里的**数据**（不下结论，供人判断）。
type LineCheck struct {
	Line  string  `json:"line"`
	Sim   float64 `json:"sim"`   // 音轨里与这句最像的一窗有多像（0-1）
	Times int     `json:"times"` // 相像的话出现了几段（≥2 = 不止一次）
}

// Verdict 一次对账的全部产出。
type Verdict struct {
	Findings []Finding   `json:"findings,omitempty"`
	Lines    []LineCheck `json:"lines,omitempty"`
}

// Empty 没有任何结论、也没有可比对的台词。
func (v Verdict) Empty() bool { return len(v.Findings) == 0 && len(v.Lines) == 0 }

// Compare 对账。planned 是这一镜**计划里该说的每一句原文**（dialogue 按顺序 + 旁白，见
// project.PlanSpokenTexts）。
//
// 没有 ASR 结果（nil）时返回空 Verdict：**没验就不报** —— 调用方要如实说「没验过」，
// 不能把「没量到」当成「合格」。计划为空、音轨也为空（真机镜 12，计划里本就没台词）同样返回空。
func Compare(planned []string, r *Result) Verdict {
	var v Verdict
	if r == nil {
		return v
	}
	type refLine struct {
		text string
		key  string
	}
	var refs []refLine
	for _, p := range planned {
		if key := normalize(p); key != "" {
			refs = append(refs, refLine{text: strings.TrimSpace(p), key: key})
		}
	}
	got := normalizeIdx(r.Text())
	if len(refs) == 0 && got.s == "" {
		return v
	}
	// 音轨里一个字都没有：计划里的每一句都是漏念。
	if got.s == "" {
		for _, rl := range refs {
			v.Findings = append(v.Findings, Finding{Kind: "missing", Line: rl.text,
				Detail: "音轨里没有这句话（ASR 在整条音轨上没转出任何语音）"})
		}
		return v
	}
	keys := make([]string, 0, len(refs))
	for _, rl := range refs {
		keys = append(keys, rl.key)
		line := LineCheck{Line: rl.text, Sim: bestWindowSim(got.s, rl.key), Times: repeatCount(got.s, rl.key)}
		v.Lines = append(v.Lines, line)
		switch {
		case line.Times >= 2 && utf8Len(rl.key) >= minRepeatLen:
			v.Findings = append(v.Findings, Finding{Kind: "repeat", Line: rl.text,
				Detail: fmt.Sprintf("音轨里出现了 %d 段与这句相像的话（剧本里只该出现一次）—— 疑似同句被念了多遍",
					line.Times)})
		case line.Sim < simHit:
			v.Findings = append(v.Findings, Finding{Kind: "missing", Line: rl.text,
				Detail: fmt.Sprintf("音轨里找不到与这句相像的内容（最像的一窗只有 %.2f）—— 疑似整句没念；ASR 全文「%s」",
					line.Sim, clip(got.rawText(), 60))})
		}
	}
	// 计划外的话：对齐后「转写多出来」的连续片段（够长、且不是某句台词的残余）。
	for _, sp := range extraSpans(align(joinKeys(keys), got.s)) {
		// 把片段里「其实就是某句台词」的部分切掉再报：ASR 常把一个自造句和一句真台词连在一起念
		//（镜 1 的一整段里塞了 5 个小句、镜 4 的第二遍接在第一遍后面）。切的时候窗口要在
		// **完整转写**上取 —— 只看片段内部会因为片段被对齐边界截断而认不出那句台词（实测过）。
		for _, pc := range unmatchedPieces(got.s, sp.start, sp.end, keys) {
			if pc.end-pc.start < minExtraLen {
				continue
			}
			v.Findings = append(v.Findings, Finding{Kind: "extra",
				Detail: fmt.Sprintf("音轨里说了计划之外的话：「%s」—— 剧本里没有这句（H3 自己编的，也可能是 ASR 听岔，请听一遍确认）",
					clip(got.display(pc.start, pc.end), 40))})
		}
	}
	return v
}

// span 归一化串上的一个区间 [start,end)。
type span struct{ start, end int }

// joinKeys 把各句的归一化串接成一串（全局对齐的参考）。
func joinKeys(keys []string) string { return strings.Join(keys, "") }

// windowMatches 这一窗算不算「认领了某句台词」。
//
// 光看相似度不够（真机镜 1）：台词「异动来源，硬件钢印U-80001。」在音轨里被写成「…条易动来源硬件刚硬u美001」，
// 从「条」起取窗与台词仍有 0.64 的相似度（窗口比台词多一个字），于是**相邻的自造句「没禁条」被判成
// 「有一段是台词」，只剩「没禁」两个字** ⇒ 短于告警下限、这条真缺陷会被静默丢掉。
// 所以再加一条：这一窗与台词的编辑对齐，**首尾那两个操作必须落在台词的字上**（'=' 或 'x'）——
// 开头不能多吃/少掉台词的首字，结尾不能多吃/少掉台词的末字。同音字替换允许，错位一个字不允许。
// 反过来它也堵住了镜 4 那条假告警：对齐片段被边界截断时，光看片段内部认不出那句台词。
func windowMatches(key, win string) bool {
	if similarity(key, win) < simHit {
		return false
	}
	ops := align(key, win)
	if len(ops) == 0 {
		return false
	}
	return isPair(ops[0].kind) && isPair(ops[len(ops)-1].kind)
}

// isPair 这一步是「把台词的字对上」（'=' 相同 / 'x' 不同）。
func isPair(k rune) bool { return k == '=' || k == 'x' }

// matchedKeyAt 从 s 的 from 处开始，按各句台词的长度（±wSlack）取窗比一遍；
// 够像就返回窗口宽度（= 该处被认领的字数）。
//
// 窗口在**完整转写**上取（不是片段内部）：对齐给出的片段边界可能正好切在一句真台词中间。
func matchedKeyAt(s string, from int, keys []string) (int, bool) {
	g := []rune(s)
	if from < 0 || from >= len(g) {
		return 0, false
	}
	for _, k := range keys {
		w0 := utf8Len(k)
		if w0 < minWin {
			continue
		}
		for w := maxInt(minWin, w0-wSlack); w <= w0+wSlack && from+w <= len(g); w++ {
			if windowMatches(k, string(g[from:from+w])) {
				return w, true
			}
		}
	}
	return 0, false
}

// unmatchedPieces 在 [from,to) 里走一遍，把**确实是某句台词**的部分让开，返回没人认领的区间。
//
// 为什么要切（真机镜 1/4）：ASR 把一整段话写成一个 segment ——「…﹔ 没禁条﹔易动来源﹔…」，
// 全局对齐给出的「多出来的片段」可能是「没禁条易动来源」这种**自造句与真台词连在一起**的东西；
// 直接照抄进告警会让人以为「易动来源」是自造的。
func unmatchedPieces(s string, from, to int, keys []string) []span {
	g := []rune(s)
	var out []span
	start := -1
	flush := func(end int) {
		if start >= 0 {
			out = append(out, span{start: start, end: end})
			start = -1
		}
	}
	for i := from; i < to; {
		if w, ok := matchedKeyAt(s, i, keys); ok {
			flush(i)
			i += w
			continue
		}
		if start < 0 {
			start = i
		}
		i++
	}
	flush(minInt(to, len(g)))
	return out
}

// normed 归一化串 + 它在**原文**里的位置。
//
// 比对用折算后的串（汉字数字→阿拉伯数字），但报告里要贴原文：告警是给人拿去听音轨的，
// 贴「眼影1下」「右8001」这种折算产物只会让人听不懂自己在找什么。
type normed struct {
	s   string // 折算+去标点后的串（比对用）
	raw []rune // 原文
	idx []int  // s 的第 k 个字对应 raw 的第 idx[k] 个字
}

// normalizeIdx 归一化并记下每个保留字的原文位置。
func normalizeIdx(text string) normed {
	var n normed
	n.raw = []rune(text)
	for i, r := range n.raw {
		lr := toLowerRune(r)
		switch {
		case lr >= '0' && lr <= '9', lr >= 'a' && lr <= 'z':
			n.s += string(lr)
			n.idx = append(n.idx, i)
		case lr >= 0x4E00 && lr <= 0x9FFF:
			if d, ok := hanDigit[lr]; ok {
				n.s += string(d)
				n.idx = append(n.idx, i)
				continue
			}
			n.s += string(lr)
			n.idx = append(n.idx, i)
		}
	}
	return n
}

// rawText 原文（不带标点删减）。
func (n normed) rawText() string { return string(n.raw) }

// display [start,end) 这段归一化串对应的**原文**（含其间被丢掉的标点）。
func (n normed) display(start, end int) string {
	if start < 0 || end > len(n.idx) || start >= end {
		return ""
	}
	a, b := n.idx[start], n.idx[end-1]+1
	if a < 0 || b > len(n.raw) || a >= b {
		return ""
	}
	return string(n.raw[a:b])
}

// toLowerRune 单字符小写（只处理 ASCII 字母，避免 strings.ToLower 的分配）。
func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// normalize 只留汉字/字母/数字并小写（标点、空白、全角符号一律不比），并把**汉字数字折成阿拉伯
// 数字** —— ASR 会把音轨里的「U-80001」写成「右八零零一」，不折算就会把念过的台词判成漏念。
func normalize(s string) string { return normalizeIdx(s).s }

// hanDigit 汉字数字 → 阿拉伯数字（只收**单个字能唯一对应一位数**的那些）。
// 刻意不收「十/百/千/万」与「两」：它们在一个词里表示的是数量而不是某一位数
// （「七十二」折成 7、2、10、2 只会把两边都弄乱），宁可让它们原样参与比较。
var hanDigit = map[rune]rune{
	'零': '0', '〇': '0', '一': '1', '幺': '1', '二': '2', '三': '3', '四': '4',
	'五': '5', '六': '6', '七': '7', '八': '8', '九': '9',
}

// utf8Len 字符数（不是字节数）。
func utf8Len(s string) int { return len([]rune(s)) }

// op 对齐的一步：kind = '=' 相同 / 'x' 不同 / '+' 转写多出 / '-' 计划里被吃掉。
type op struct {
	kind rune
	r    rune
	at   int // kind == '+' 时：这个字在转写归一化串里的位置
}

// align Levenshtein 对齐（带回溯，记下插入字的位置）。两边都很短（几十到几百字），O(n*m) 足够。
func align(ref, got string) []op {
	a, b := []rune(ref), []rune(got)
	n, m := len(a), len(b)
	d := make([][]int, n+1)
	for i := range d {
		d[i] = make([]int, m+1)
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			best := d[i-1][j-1] + cost
			if v := d[i-1][j] + 1; v < best {
				best = v
			}
			if v := d[i][j-1] + 1; v < best {
				best = v
			}
			d[i][j] = best
		}
	}
	var rev []op
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1] && d[i][j] == d[i-1][j-1]:
			rev = append(rev, op{'=', a[i-1], 0})
			i, j = i-1, j-1
		case i > 0 && j > 0 && d[i][j] == d[i-1][j-1]+1:
			rev = append(rev, op{'x', b[j-1], 0})
			i, j = i-1, j-1
		case i > 0 && d[i][j] == d[i-1][j]+1:
			rev = append(rev, op{'-', a[i-1], 0})
			i--
		default:
			rev = append(rev, op{'+', b[j-1], j - 1})
			j--
		}
	}
	out := make([]op, 0, len(rev))
	for k := len(rev) - 1; k >= 0; k-- {
		out = append(out, rev[k])
	}
	return out
}

// extraSpans 转写里「多出来」的连续片段（归并相邻的 '+'，含它们在归一化串上的位置）。
func extraSpans(ops []op) []span {
	var out []span
	cur := span{start: -1}
	flush := func() {
		if cur.start >= 0 {
			out = append(out, cur)
			cur = span{start: -1}
		}
	}
	for _, o := range ops {
		if o.kind == '+' {
			if cur.start < 0 {
				cur.start = o.at
			}
			cur.end = o.at + 1
			continue
		}
		flush()
	}
	flush()
	return out
}

// bestWindowSim 在 got 里滑一窗，取与 key 最像的那一窗的相似度。
//
// 为什么要滑窗口而不是拿整句比：ASR 的分段与剧本的分句不对应（镜 1 一整个 segment 里塞了 5 个小句），
// 拿整句比会把「这句在不在音轨里」算成 0。窗口宽度留 ±wSlack 是给 ASR 多吃/少吃一两个字留余量。
func bestWindowSim(got, key string) float64 {
	g := []rune(got)
	w0 := utf8Len(key)
	if w0 == 0 || len(g) < minWin {
		return 0
	}
	best := 0.0
	for w := maxInt(minWin, w0-wSlack); w <= w0+wSlack && w <= len(g); w++ {
		for i := 0; i+w <= len(g); i++ {
			if s := similarity(key, string(g[i:i+w])); s > best {
				best = s
			}
		}
	}
	return best
}

// repeatCount 音轨里出现了几段「近似这句台词」的话（非重叠，从前往后贪心取）。
func repeatCount(got, key string) int {
	g := []rune(got)
	w0 := utf8Len(key)
	if w0 < minWin || len(g) < minWin {
		return 0
	}
	cnt := 0
	for i := 0; i < len(g); {
		hit, width := false, 0
		for w := maxInt(minWin, w0-wSlack); w <= w0+wSlack && i+w <= len(g); w++ {
			if sim := similarity(key, string(g[i:i+w])); sim >= simRepeat {
				hit, width = true, w
				break
			}
		}
		if hit {
			cnt++
			i += width
			continue
		}
		i++
	}
	return cnt
}

// similarity 两个归一化串的相似度（0-1）= 1 - 编辑距离/较长长度。
func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	n, m := len(ra), len(rb)
	dp := make([]int, m+1)
	for j := 0; j <= m; j++ {
		dp[j] = j
	}
	for i := 1; i <= n; i++ {
		prev := dp[0]
		dp[0] = i
		for j := 1; j <= m; j++ {
			cur := dp[j]
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			v := prev + cost
			if x := dp[j] + 1; x < v {
				v = x
			}
			if x := dp[j-1] + 1; x < v {
				v = x
			}
			dp[j] = v
			prev = cur
		}
	}
	l := n
	if m > l {
		l = m
	}
	sim := 1 - float64(dp[m])/float64(l)
	if sim < 0 {
		sim = 0
	}
	return sim
}

// clip 截断（报告里贴片段，不贴整篇）。
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// maxInt / minInt 取大 / 取小（显式写一份，避免与其它包的同名实现分叉）。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
