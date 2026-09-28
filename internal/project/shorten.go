package project

import (
	"fmt"
	"strconv"
	"strings"
)

// shorten.go 官方降级阶梯第二级：**把镜头缩短到 ≤6 秒**再出一次。
//
// 来源（官方 `skills/3d-animation-short-generator/references/fallback-policy.md`）：
//
//	"Second retry: shorten the shot to ≤6s and split the dropped seconds into a new adjacent row
//	 in Step 5; re-run Step 5.5 self-check, then re-render."
//
// 为什么这一级最有效（实测）：崩坏多数发生在长片段的**尾巴**上（动作越到后面越容易散、
// 手/道具越容易糊）。把片段砍短，等于把最容易崩的那几秒直接扔掉。
//
// 平台化的处理（与原版的一点差异，如实记下）：
//   - 原版要求把砍掉的秒数"拆成相邻的新镜"（内容不丢）。平台这里**不自动建新镜** ——
//     自动拆镜要凭空判断"这几秒该拍什么"，那是创作决定，不该由重试逻辑替用户做。
//     平台的做法：把砍掉的秒数**如实报出来**（阶段日志 + 该镜状态），内容要不要补由人决定。
//
// ⚠ **有意分歧 DEV-01（勿当 bug 修）**：见 docs/与官方的有意分歧.md。
//   - 缩短发生在**片段内部小镜头**层面：按 1.5-4 秒的切点从后往前砍，保证剩下的仍满足
//     "≥1 个小镜头、总时长 = 指定秒数、时间戳严格递增"这三条官方格式要求。
//
// 返回 (新提示词, 新时长秒, 是否成功)。砍不动（本来就 ≤maxSeconds，或切不出合法结果）返回 false。
func ShortenShotPrompt(h3 string, duration int, maxSeconds int) (string, int, bool) {
	if maxSeconds <= 0 || duration <= maxSeconds {
		return h3, duration, false
	}
	seg, ok := sectionOf(h3, "detailed_description")
	if !ok {
		return h3, duration, false
	}
	// 正文段内的行首小镜头标题（同 H3ClipCuts 的口径）
	locs := h3ShotHeadRe.FindAllStringIndex(seg, -1)
	if len(locs) < 2 {
		// 只有一个镜头：砍不出"内部切点"，只能整段缩短 —— 那就把时长直接改成 maxSeconds，
		// 正文不动（模型按新时长自己分配节奏）。这是官方"缩短片段"的最直白形式。
		return h3, maxSeconds, true
	}
	// 每个小镜头的时长：由它自己的时间戳差算出；第一个镜头从 0 起算。
	times := shotTimestamps(seg)
	startMS := func(i int) int {
		if i == 0 {
			return 0
		}
		if i-1 < len(times) {
			return times[i-1].ms
		}
		return -1
	}
	// 从后往前砍整块小镜头，直到总长 ≤ maxSeconds
	keep := len(locs)
	total := duration * 1000
	for keep > 1 {
		// 最后一个镜头的时间跨度
		last := keep - 1
		s := startMS(last)
		if s < 0 {
			break
		}
		span := total - s
		if total-span <= maxSeconds*1000 {
			// 砍掉这一个正好够
			keep = last
			total -= span
			break
		}
		keep = last
		total = s
	}
	if keep == len(locs) {
		return h3, duration, false
	}
	newDur := (total + 999) / 1000
	if newDur > maxSeconds {
		newDur = maxSeconds
	}
	if newDur < 1 {
		newDur = 1
	}
	// 重建正文：保留前 keep 个小镜头（连同它们的标题行与内容），删掉其余
	out := rebuildWithHeadings(seg, locs, keep)
	if out == "" {
		return h3, duration, false
	}
	return strings.Replace(h3, seg, out, 1), newDur, true
}

// （2026-09-29 审计 #11：这里曾有一个 `h3ShotHeadReLocal`，与 h3_format_extra.go 的
// `h3ShotHeadRe` 逐字重复且全仓无使用点 —— 死代码已删。要用请直接用那个唯一的一份。

// rebuildWithHeadings 把正文段截到前 keep 个小镜头（保留每段的正文内容与标点）。
func rebuildWithHeadings(seg string, locs [][]int, keep int) string {
	if keep <= 0 || keep > len(locs) {
		return ""
	}
	end := len(seg)
	if keep < len(locs) {
		end = locs[keep][0]
	}
	out := seg[:end]
	// 去掉尾部悬空的空白/换行，保证格式整齐
	return strings.TrimRight(out, " \t\r\n")
}

// ShortenNote 给用户看的说明（说清砍了多少秒、以及"原版要求拆成新镜、平台不擅自拆"）。
func ShortenNote(shotID, fromSec, toSec int) string {
	return fmt.Sprintf("镜 %d 按官方降级阶梯缩短:%d 秒 → %d 秒（砍掉 %d 秒；"+
		"官方原版要求把砍掉的秒数拆成相邻新镜，平台**不擅自替你拆** —— 那是创作决定，"+
		"要不要补这几秒由你定）", shotID, fromSec, toSec, fromSec-toSec)
}

var _ = strconv.Itoa // 保持 strconv 在文件中被引用（时间戳解析后续如需可直接用）
