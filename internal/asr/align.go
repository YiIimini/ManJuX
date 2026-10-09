package asr

import (
	"strings"
	"unicode"
)

// align.go —— 「已知台词 ↔ 词流」的**强制对齐**（forced alignment 的可用档）。
//
// 【为什么是这条路（2026-10-09 用户：「看看全网上有没有最优的解决方案？」）】
//
//	成片语音由 H3 音画同生生出 ⇒ **没有任何现成时间戳**（不是 TTS，没有 word boundary），
//	所以字幕要贴在人声上只能事后对齐。业界标准做法就是 forced alignment：
//	WhisperX 用 wav2vec2 做音素级对齐、stable-ts 提供 align()（arXiv 2303.00747）。
//	我们这里取**最省的一档**：whisper 自带的词级时间戳（同一遍解码 + 一趟 DTW）
//	+ 平台侧的**字符级对齐**（已知台词 ↔ 词流）。判据纯函数、可测、无新依赖。
//
// 【它解决的是段级判据判不了的那件事（真机第01集）】
//
//	镜 10：台词 2 句（「工牌呢。」「被收了。」），ASR **也是 2 段** —— 但段的边界
//	（4.18-6.30 / 6.30-7.84）与句的边界并不重合：段 1 末尾那个 `呢?` 属于第一句没问题，
//	可第一句只说 2.12 秒、字幕却按"整段/整镜"铺开 ⇒ 观众先看到字、后听到话（实测差 0.94 秒）。
//	词级信息到手后，"这一句到底从哪个词开始、说到哪个词"就变成**可算**的。
//
// 【为什么要容忍错字】ASR 会把同音字听错：真机镜 10 的「工牌」被听成「**公**牌」
//	（3 个字里错 1 个）。所以对齐必须允许少量不匹配，而不是要求逐字相等 ——
//	阈值与记分写在 alignTolRatio / 下面的记分表里。

// alignMinRatio 一句台词至少要有这个比例的字符被对齐上，才算"这一句真的念了"。
//
// 取值依据（真机形态）：镜 10 第一句 3 个字错 1 个 = 0.67；镜 12 两句逐字全中 = 1.0；
// 而"这一句根本没念"（真机镜 2/5 那种）最多只能蹭到零星字符 ⇒ 远低于 0.6。
// 阈值取 0.6 让"同音错 1 个字"过关、"没念"不过关。
const alignMinRatio = 0.6

// alignChar 一个参与对齐的字符：它属于哪个词（词带起止时间）。
type alignChar struct {
	r     rune
	start float64
	end   float64
	// word 它属于第几个词（用来把落点收在**词边界**上：见 alignSpan 的注释）。
	word int
}

// normAlignText 归一化：只留**听得出来的字符**（汉字/字母/数字），大小写统一。
//
// 标点、空格、换行都不参与对齐 —— ASR 那边会带 `。`、`?` 之类，而标点在时间轴上是
// **没有"发音"**的，拿它当字符会让"错一个标点"也算错。
func normAlignText(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			out = append(out, r)
		case unicode.IsLetter(r), unicode.IsDigit(r):
			out = append(out, unicode.ToLower(r))
		}
	}
	return out
}

// alignChars 把词流摊成字符流（每个字符继承它所在词的时间）。
//
// 词内多字符按**时间等分**：whisper 的词级时间戳是词边界（真机形态：一个"词"常是
// 2~3 个汉字，如「现在」0.5 秒），句内按字符等分后，同一句里相邻字符的相对顺序
func alignChars(words []Word) []alignChar {
	out := make([]alignChar, 0, len(words)*2)
	for wi, w := range words {
		rs := normAlignText(w.Text)
		if len(rs) == 0 {
			continue
		}
		step := (w.End - w.Start) / float64(len(rs))
		for i, r := range rs {
			out = append(out, alignChar{
				r:     r,
				start: w.Start + step*float64(i),
				end:   w.Start + step*float64(i+1),
				word:  wi,
			})
		}
	}
	return out
}

// Span 一行的落点（秒）。
type Span struct {
	Start, End float64
	// Ratio 这一行被对齐上的字符比例（1.0 = 逐字全中）。调用方播报时可以据此说清
	// "这句是逐字对齐的"还是"靠同音字蹭上的"。
	Ratio float64
}

// AlignLines 把**若干句已知台词**按说话顺序对齐到词流，返回每一句的实际起止。
//
// 逐句顺序推进（第二句只在前一句结束之后找词），因为分镜的台词本来就有先后；
// 任何一句对齐不上（字符命中比例 < alignMinRatio）就整体返回 ok=false ——
// **不猜**：上层会退回段级/镜头级的其余档，并在播报里说清用了哪一档。
func AlignLines(lines []string, words []Word) ([]Span, bool) {
	if len(lines) == 0 {
		return nil, false
	}
	chars := alignChars(words)
	if len(chars) == 0 {
		return nil, false
	}
	spans := make([]Span, 0, len(lines))
	cursor := 0 // 下一句只能从这个词开始找（顺序推进）
	for _, ln := range lines {
		rs := normAlignText(ln)
		if len(rs) == 0 {
			return nil, false
		}
		cols, next, ratio, ok := alignOne(rs, chars, cursor)
		if !ok {
			return nil, false
		}
		// 落点收在**词边界**上，而且范围取"对齐到的整段列"（含错字那一列），不是只取命中列：
		// 真机镜 10 第一句「工牌呢。」被听成「公牌呢」—— 首字是同音错字，若只按命中列取起点，
		// 就会把这 0.6 秒（公 4.18-4.78）丢掉，字幕又变成"比声音晚出"。
		// 例：cols 首列是那个错字、命中只有后两个字 ⇒ 起点仍是 4.18；而**比例**只算命中（2/3）。
		w0, w1 := chars[cols[0]].word, chars[cols[len(cols)-1]].word
		spans = append(spans, Span{
			Start: words[w0].Start,
			End:   words[w1].End,
			Ratio: ratio,
		})
		cursor = next
	}
	// 句子顺序：起点必须随后倒退（词流不回头，这条出错说明对齐选错了词）
	for i := 1; i < len(spans); i++ {
		if spans[i].Start+1e-9 < spans[i-1].End {
			return nil, false
		}
	}
	return spans, true
}

// alignOne 单句对齐：字符级全局对齐（Needleman–Wunsch），返回**对齐到的列**（cols，含错字那一列；
// 落点范围按它取）、下一句的起点（最后一个命中字符之后）、命中比例。
//
// 记分：命中 +2 / 不匹配（错字，同音场景在这里被容忍）-1 / 空位 -1。
// 命中比例 = 命中数 / 台词字符数（**分母只算台词**：ASR 多念出来的字不惩罚这一句的落点，
// 它属于"该念没念/多念"那条判据的活）。
func alignOne(line []rune, chars []alignChar, from int) (cols []int, next int, ratio float64, ok bool) {
	n, m := len(line), len(chars)-from
	if from < 0 || from > len(chars) || m <= 0 {
		return nil, 0, 0, false
	}
	// DP 表：dp[i][j] = 前 i 个台词字符与从 from 起前 j 个已知字符的最优得分。
	const gapScore, hitScore, missScore = -1, 2, -1
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		dp[i][0] = dp[i-1][0] + gapScore
	}
	for j := 1; j <= m; j++ {
		dp[0][j] = dp[0][j-1] + gapScore
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			sc := missScore
			if line[i-1] == chars[from+j-1].r {
				sc = hitScore
			}
			best := dp[i-1][j-1] + sc
			if v := dp[i-1][j] + gapScore; v > best {
				best = v
			}
			if v := dp[i][j-1] + gapScore; v > best {
				best = v
			}
			dp[i][j] = best
		}
	}
	// 回溯：pairs 只收**命中**的位置（比例按它算）；cols 收**所有对角列**（含错字那一列），
	// 落点范围按 cols 取 —— 同音错字那一截时间也算这一句（真机镜 10 的「工」/「公」）。
	pairs := make([]int, 0, n)
	cols = make([]int, 0, n)
	i, j := n, m
	for i > 0 || j > 0 {
		if i > 0 && j > 0 {
			sc, match := missScore, false
			if line[i-1] == chars[from+j-1].r {
				sc, match = hitScore, true
			}
			if dp[i][j] == dp[i-1][j-1]+sc {
				if match {
					pairs = append(pairs, from+j-1)
				}
				cols = append(cols, from+j-1)
				i, j = i-1, j-1
				continue
			}
		}
		if i > 0 && dp[i][j] == dp[i-1][j]+gapScore {
			i--
			continue
		}
		if j > 0 && dp[i][j] == dp[i][j-1]+gapScore {
			j--
			continue
		}
		break
	}
	if len(pairs) == 0 {
		return nil, 0, 0, false
	}
	// pairs / cols 都是回溯出来的倒序：反过来，让"起止"取首尾。
	for l, r := 0, len(pairs)-1; l < r; l, r = l+1, r-1 {
		pairs[l], pairs[r] = pairs[r], pairs[l]
	}
	for l, r := 0, len(cols)-1; l < r; l, r = l+1, r-1 {
		cols[l], cols[r] = cols[r], cols[l]
	}
	ratio = float64(len(pairs)) / float64(n)
	if ratio < alignMinRatio {
		return nil, 0, ratio, false
	}
	next = pairs[len(pairs)-1] + 1
	return cols, next, ratio, true
}

// JoinWords 词流拼回文本（诊断/播报用：出问题时把 ASR 到底听到什么打出来）。
func JoinWords(words []Word) string {
	var sb strings.Builder
	for _, w := range words {
		sb.WriteString(strings.TrimSpace(w.Text))
	}
	return sb.String()
}
