package compose

import (
	"context"
	"fmt"
	"math"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// xfade.go —— xfade 视频链的**帧对齐偏移**与**拼接后校验**。
//
// 【为什么单列这两个函数（2026-10-09 真机 P0：成片只有前 3 镜的画面）】
//
//	真机《被开除一年后，我回公司送水·第01集》合出来的 final.mp4：
//	**画面 23.708 秒（569 帧）、音轨 122.499 秒**（方案 15 镜、约 121 秒）。
//	569 帧正好是前 3 镜（192+175+204）减去 2 帧同场硬切 ⇒ **第 4 镜之后一帧画面都没有**，
//	观众从第 24 秒起看到的就是一直定格的第一帧、而声音照常往下走。
//
//	复现与根因（`scratch/chain4.mp4` 的实测，4 镜版本）：
//	  · 原先 xfade 的 `offset` 直接用 `starts[i]`（按**秒**连续累加的量）；
//	  · 而 xfade 每步的实际输出长度会被落到**整数帧**上（366 帧 = 15.25s，而标称 15.258s）；
//	  · 于是下一接缝的过渡窗口 [15.217, 15.259] 的末端**超出了 A 的真实末帧**（15.25）；
//	  · ffmpeg 的 xfade 遇到"过渡还没做完 A 就没了"时，**不再输出 B 的任何一帧**，
//	    而且**退出码是 0** ⇒ 整条链从此静止，合成照常往下走、日志照报"成片就绪 122 秒"。
//
//	两个层面都要修：
//	  ① `xfadeOffsets` —— 偏移按**整数帧**累计，保证每一步的过渡窗口都落在前一镜头之内；
//	  ② `verifyStitchedLength` —— 拼完之后**实测**画面时长，短了就直接拒收（宁可失败也不交付半部片子）。

// videoFPS 成片帧率。H3 渲染即 24fps（`ClipManifest.Length = duration×24` 是同一口径）。
const videoFPS = 24.0

// xfadeOffsets 算每个接缝上 xfade 的 offset（秒），**全程按整数帧累计**。
//
// 为什么不能用按秒的连续量：xfade 的输入是"帧"，它的每一步输出长度都被截到整帧
// （实测：标称 15.258s 出来 366 帧 = 15.25s，差 0.008s）。这点误差在 15 镜里会攒起来，
// 只要某一步的过渡窗口末端越过了前一镜的末帧，xfade 就**静默丢弃后面所有输入**。
//
// 口径：第 i 个接缝的过渡窗口 = 前一镜链的末尾 fades[i-1] 秒；累计量始终用整帧表示，
// 于是 `offset + duration` 恰好等于前一镜链的**整数帧长度**，永不越界。
func xfadeOffsets(durations, fades []float64) []float64 {
	n := len(durations)
	if n < 2 {
		return nil
	}
	out := make([]float64, n-1)
	// frames：链的当前长度（帧）。第一镜的帧长按它的时长折算。
	frames := int64(math.Round(durations[0] * videoFPS))
	if frames < 1 {
		frames = 1
	}
	for i := 1; i < n; i++ {
		df := int64(math.Round(safeIndex(fades, i-1) * videoFPS))
		if df < 1 {
			df = 1 // xfade 不接受 0 时长（见 minSeamFade）
		}
		if df > frames {
			df = frames // 极端：前一镜只有几帧（占位/极短镜）
		}
		off := frames - df
		out[i-1] = float64(off) / videoFPS
		next := int64(math.Round(safeIndex(durations, i) * videoFPS))
		if next < 1 {
			next = 1
		}
		frames = off + next
	}
	return out
}

// safeIndex 越界取零（fades 短一条时按"无重叠"处理，不 panic）。
func safeIndex(xs []float64, i int) float64 {
	if i >= 0 && i < len(xs) {
		return xs[i]
	}
	return 0
}

// verifyStitchedLength 拼完之后**实测**画面时长，短于标称就报错。
//
// 为什么必须这么做（而不是相信 ffmpeg 的退出码）：上面那种链中断的退出码是 **0**，
// 日志照报"成片就绪 122 秒"，而画面只有前 3 镜 —— 盘上、日志里、质检里**都没有一句话**
// 提到画面短了（`format.duration` 取的是两条流里最长的那个，所以连时长都看着正常）。
// 宁可在这里失败并说清原因，也不许交付一部"声音在走、画面定格"的片子。
func verifyStitchedLength(ctx context.Context, f *ff.FF, path string, want float64) error {
	if f == nil || want <= 0 {
		return nil
	}
	info, err := f.Probe(path)
	if err != nil || info == nil {
		return nil // 量不到就不拦（这里是兜底，不是唯一判据）
	}
	got := info.VideoDuration
	if got <= 0 {
		return nil
	}
	if got < want-0.5 {
		return fmt.Errorf("拼接后的**画面**只有 %.2f 秒，而方案要求 %.2f 秒（少 %.2f 秒）—— "+
			"画面会在半途定格、声音继续往下走（xfade 链中断的典型形态）。已拒绝交付：请重跑「合片」；"+
			"若反复出现，把这次的分镜/时长发我（这是平台侧要修的，不是你的操作问题）",
			got, want, want-got)
	}
	return nil
}
