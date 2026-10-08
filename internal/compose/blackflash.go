package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// blackflash.go 片段里的**黑闪**：把"短到只是故障"的那几段全黑帧剪掉。
//
// 【为什么要有它（2026-10-08 真机《顺其自然（真实事迹短片）》第01集）】
// 用户报「视频看的莫名其妙的」。逐帧量下来，那条片子里有几处**几十到两百毫秒的纯黑**，
// 位置还各不相同（引擎自己吐的，不是后期加的 —— compose 早就明确"不加头尾淡入淡出"）：
//
//	镜 1   black_start:0        black_end:0.208333   （片头）
//	镜 6   black_start:4.458333 black_end:4.541667   （第 4.5 秒，83 毫秒）
//	镜 8   black_start:0        black_end:0.208333   （片头）
//	镜 13  black_start:2.416667 black_end:2.541667   （第 2.4 秒，125 毫秒）
//
// 后果有两层：① 观众看到"画面无故一黑"；② **质检被它带偏** —— 质检的暗度判据取"最暗采样帧"
// （设计如此，为的是抓尾部黑这类失效），而它的采样点恰好落在黑闪上时，整镜就被判"疑似黑屏"：
// 那一集 4 镜不合格（镜 6/8/12/13），其中镜 13 的 30% 采样点正好压在那 125 毫秒黑上（实测 1.00）。
// 把黑闪剪掉之后，同样的采样点落回画面本体（实测 0.47 一档）。
//
// 【边界：什么剪、什么不剪】只需要"短"这一个判据，且**宁可漏不可误剪**：
//   - 单段 ≤ maxBlackRunSec（0.5s）且累计 ≤ maxBlackTotalSec（1.0s）⇒ 剪（这是故障，不是内容）；
//   - 更长的黑段**不剪** —— 整段黑/黑尾是 H3 的真实失效，也是计划里就写明"沉在黑暗里"时该有的样子，
//     那属于质检该报、用户该看到的，不能在这里悄悄改掉。
//
// 剪辑是**确定性**的：ffmpeg `blackdetect` 找段、`select/aselect` 按同一批时间区间同步切视频与音频，
// 再重编码一次（x264 crf 与成片同档）。不调任何模型。
const (
	maxBlackRunSec   = 0.5
	maxBlackTotalSec = 1.0
	// blackPixTh 像素判黑的亮度阈值（0-1，官方默认 0.10）；pic_th 要求整帧 98% 像素都黑才算。
	blackPixTh = 0.10
	blackPicTh = 0.98
	// blackMinDur 短于这个的黑段忽略（=1 帧级噪声，不动）。
	blackMinDur = 0.02
)

// BlackRun 一段全黑（秒）。
type BlackRun struct {
	Start float64
	End   float64
}

// Dur 这段黑的时长。
func (r BlackRun) Dur() float64 { return r.End - r.Start }

// blackDetectRe 解析 ffmpeg `blackdetect` 的日志行。
var blackDetectRe = regexp.MustCompile(`black_start:([0-9.]+)\s+black_end:([0-9.]+)\s+black_duration:([0-9.]+)`)

// ParseBlackRuns 从 blackdetect 的输出里取出黑段（纯函数，便于用**真机日志原文**当夹具）。
func ParseBlackRuns(out string) []BlackRun {
	var runs []BlackRun
	for _, m := range blackDetectRe.FindAllStringSubmatch(out, -1) {
		s, err1 := strconv.ParseFloat(m[1], 64)
		e, err2 := strconv.ParseFloat(m[2], 64)
		if err1 != nil || err2 != nil || e <= s {
			continue
		}
		runs = append(runs, BlackRun{Start: s, End: e})
	}
	return runs
}

// strippableBlackRuns 哪些黑段该剪（判据只有两条：单段够短、累计够短）。
func strippableBlackRuns(runs []BlackRun, maxRun, maxTotal float64) []BlackRun {
	var out []BlackRun
	total := 0.0
	for _, r := range runs {
		if r.Dur() < blackMinDur {
			continue
		}
		if r.Dur() > maxRun {
			continue // 长黑段不碰（那是质检该报的）
		}
		if total+r.Dur() > maxTotal {
			break
		}
		total += r.Dur()
		out = append(out, r)
	}
	return out
}

// DetectBlackRuns 找出片段里的黑段（确定性：ffmpeg blackdetect，不调模型）。
func DetectBlackRuns(ctx context.Context, f *ff.FF, path string) ([]BlackRun, error) {
	if f == nil {
		return nil, fmt.Errorf("ffmpeg 不可用")
	}
	out, err := f.Run(ctx, "-hide_banner", "-i", path,
		"-vf", fmt.Sprintf("blackdetect=d=%.3f:pix_th=%.2f:pic_th=%.2f", blackMinDur, blackPixTh, blackPicTh),
		"-an", "-f", "null", os.DevNull)
	if err != nil {
		// blackdetect 只写日志，退出码非 0 也可能已经给全了日志；解析得到就不算失败。
		if runs := ParseBlackRuns(out); len(runs) > 0 {
			return runs, nil
		}
		return nil, err
	}
	return ParseBlackRuns(out), nil
}

// StripBlackFlash 把**短黑闪**切掉（视频与音频同步切，原地替换）。
//
// 返回剪掉的秒数、段数与**没剪**的长黑段（后者由调用方如实播报，见 Stripped / Unstripped）。
// 已经处理过的片段用同名标记跳过（幂等，与 NormalizeClipAudio 同一套路）。
func StripBlackFlash(ctx context.Context, f *ff.FF, path string, fps float64) (removed float64, runs int, longRuns []BlackRun, err error) {
	if f == nil || strings.TrimSpace(path) == "" {
		return 0, 0, nil, nil
	}
	if _, statErr := os.Stat(path); statErr != nil {
		return 0, 0, nil, statErr
	}
	marker := path + ".noblack"
	if clipMarkerValid(marker, path) {
		return 0, 0, nil, nil // 已经处理过（且片段没被重渲覆盖）
	}
	found, err := DetectBlackRuns(ctx, f, path)
	if err != nil {
		return 0, 0, nil, err
	}
	if len(found) == 0 {
		_ = writeClipMarker(marker, path, "clip black-flash checked: none")
		return 0, 0, nil, nil
	}
	cut := strippableBlackRuns(found, maxBlackRunSec, maxBlackTotalSec)
	for _, r := range found {
		if r.Dur() < blackMinDur {
			continue
		}
		if r.Dur() > maxBlackRunSec {
			longRuns = append(longRuns, r)
		}
	}
	if len(cut) == 0 {
		return 0, 0, longRuns, nil
	}
	// 同一批时间区间同时作用在视频与音频上（不然会音画不同步）。
	var terms []string
	for _, r := range cut {
		terms = append(terms, fmt.Sprintf("between(t,%.6f,%.6f)", r.Start, r.End))
		removed += r.Dur()
	}
	expr := strings.Join(terms, "+")
	if fps <= 0 {
		fps = 24
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".noblack.tmp.mp4")
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", path,
		// `select` 只丢帧、不重排时间戳；必须再 `setpts=N/(fps*TB)` 把留下的帧压紧，
		// 否则 CFR 会把丢掉的时长用重复帧补回来（实测：时长一点没变 ⇒ 剪辑等于没做）。
		"-vf", fmt.Sprintf("select='not(%s)',setpts=N/(%.6f*TB)", expr, fps),
		"-fps_mode", "cfr", "-r", strconv.FormatFloat(fps, 'f', 6, 64),
		"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p"}
	if hasAudio, _ := hasAudioStream(ctx, f, path); hasAudio {
		args = append(args, "-af", fmt.Sprintf("aselect='not(%s)',asetpts=N/SR/TB", expr),
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2")
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-movflags", "+faststart", tmp)
	if _, err := f.Run(ctx, args...); err != nil {
		os.Remove(tmp)
		return 0, 0, longRuns, err
	}
	// 原子替换：先换内容再落标记（与 NormalizeClipAudio 同一顺序，避免"标记在、内容还是旧的"）。
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return 0, 0, longRuns, err
	}
	return removed, len(cut), longRuns, writeClipMarker(marker, path,
		fmt.Sprintf("clip black-flash stripped: %d run(s), %.3fs", len(cut), removed))
}

// hasAudioStream 这条片段有没有音轨。
func hasAudioStream(ctx context.Context, f *ff.FF, path string) (bool, error) {
	info, err := f.Probe(path)
	if err != nil {
		return false, err
	}
	return info.HasAudio, nil
}
