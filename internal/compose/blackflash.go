package compose

import (
	"context"
	"fmt"
	"math"
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
// 剪辑是**确定性**的：ffmpeg `blackdetect` 找段，`select/aselect` 按**同一批帧**同步切视频与音频
// （切的是帧号窗口，不是日志里那个四舍五入过的秒值 —— 见 frameSpan），再重编码一次（x264 crf 与成片同档）。
// 不调任何模型。
//
// 【2026-10-10 真机修正：原来只剪掉一部分黑闪】真机镜 1 报 black_start:5.291667 black_end:5.458333
// （0.1667s ＝ 4 帧），而那一帧真实 pts 是 127/24＝5.2916666…、**比打印值小** ⇒ 原来按秒切的窗口把
// **最黑的那一帧**（YAVG 16.1，其余三帧 17.1/19.3/21.7）漏在外面：成品片段实测只少 3 帧、"无故一黑"
// 照旧，且音轨按整段多切了一帧。现在一律换整数帧号窗口。
const (
	maxBlackRunSec   = 0.5
	maxBlackTotalSec = 1.0
	// blackPixTh 像素判黑的亮度阈值（0-1，官方默认 0.10）；pic_th 要求整帧 98% 像素都黑才算。
	blackPixTh = 0.10
	blackPicTh = 0.98
	// blackMinDur 短于这个的黑段忽略（=1 帧级噪声，不动）。
	blackMinDur = 0.02
	// defaultClipFPS 片段帧率缺省值（调用方传 0 时用它；合成侧同样强制 24fps）。
	defaultClipFPS = 24.0
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

// frameSpan 把一段黑（秒）换成**整数帧窗口** [i0,i1]（含两端，0-based 输入帧序号）。
//
// 【为什么必须换成帧号（2026-10-10 真机，缺陷就在这）】黑段的秒值是 ffmpeg 用 `%.6f` 打印出来的
// **四舍五入**结果：真机《星纪山海：我在废土养神兽》第01集镜 1 报的是 `black_start:5.291667`，
// 而那一帧的真实 pts 是 127/24＝5.2916666…，**比打印值小**。拿打印值当窗口下界，最黑的那一帧
// 正好卡在窗口外 —— 0.1667s（4 帧）的黑只剪掉 3 帧，观众看到的"无故一黑"照旧留在成片里；
// 音频却按整段 0.1667s 切 ⇒ 音轨比画面**多切一帧**（2026-10-10 实测复现：视频 189 帧/7.875s、
// 音频 7.84s）。换成帧号窗口后，同样的 6 位小数误差只值 1e-5 帧，取整结果唯一、稳定。
//
// 第二返回值 false ＝ 这一段短到落在同一帧里（丢帧丢不出它），调用方跳过、也不许虚报剪了多少。
func frameSpan(r BlackRun, fps float64) (int, int, bool) {
	if fps <= 0 {
		fps = defaultClipFPS
	}
	i0 := int(math.Floor(r.Start*fps + 0.5))
	i1 := int(math.Floor(r.End*fps+0.5)) - 1
	if i0 < 0 || i1 < i0 {
		return 0, 0, false
	}
	return i0, i1, true
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
	// 视频按**帧号**切、音频按**同一批帧换出来的时间**切 —— 两者丢的是同一批帧，所以画面与音轨
	// 少掉的时长逐字相等（少切一帧就是"画面还在黑、音轨已经过去"；多切一帧就是音画错位）。
	// 为什么不能直接拿日志里的秒值去比：见 frameSpan 的注释（真机那一处就栽在这）。
	if fps <= 0 {
		fps = defaultClipFPS
	}
	var vTerms, aTerms []string
	cutFrames := 0
	for _, r := range cut {
		i0, i1, ok := frameSpan(r, fps)
		if !ok {
			continue // 短到落在同一帧里：丢帧丢不出这一段，不碰
		}
		vTerms = append(vTerms, fmt.Sprintf("between(n,%d,%d)", i0, i1))
		aTerms = append(aTerms, fmt.Sprintf("between(t,%.9f,%.9f)", float64(i0)/fps, float64(i1+1)/fps))
		cutFrames += i1 - i0 + 1
	}
	if len(vTerms) == 0 {
		return 0, 0, longRuns, nil
	}
	vExpr := strings.Join(vTerms, "+")
	aExpr := strings.Join(aTerms, "+")
	nRuns := len(vTerms)
	// 剪掉多少秒按**帧数**算，不按日志里那个四舍五入过的秒值算（否则会虚报）。
	removed = float64(cutFrames) / fps
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".noblack.tmp.mp4")
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", path,
		// `select` 只丢帧、不重排时间戳；必须再 `setpts=N/(fps*TB)` 把留下的帧压紧，
		// 否则 CFR 会把丢掉的时长用重复帧补回来（实测：时长一点没变 ⇒ 剪辑等于没做）。
		"-vf", fmt.Sprintf("select='not(%s)',setpts=N/(%.6f*TB)", vExpr, fps),
		"-fps_mode", "cfr", "-r", strconv.FormatFloat(fps, 'f', 6, 64),
		"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p"}
	if hasAudio, _ := hasAudioStream(ctx, f, path); hasAudio {
		args = append(args, "-af", fmt.Sprintf("aselect='not(%s)',asetpts=N/SR/TB", aExpr),
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
	return removed, nRuns, longRuns, writeClipMarker(marker, path,
		fmt.Sprintf("clip black flash stripped: %d run(s), %.3fs", nRuns, removed))
}

// ForceStripBlackFlash 与 StripBlackFlash 同一条链，只是**绕过"已处理过"标记**。
//
// 【为什么要有它（2026-10-10）】片段是按标记判幂等的（`clipMarkerValid`），而**存量片段**可能是在
// 旧判据下处理过的：标记有效、可窗口边缘那一帧黑还在（真机镜 1 就是"剪了 3 帧、留下最黑那帧"）。
// 走普通入口会直接 `return` 什么都不做 —— 而 `cmd/clipfix` 的全部意义正是修存量，所以它必须走这条。
// 剪完仍会重写标记（内容是新的，标记也跟着刷新）。
func ForceStripBlackFlash(ctx context.Context, f *ff.FF, path string, fps float64) (float64, int, []BlackRun, error) {
	if f == nil || strings.TrimSpace(path) == "" {
		return 0, 0, nil, nil
	}
	if err := os.Remove(path + ".noblack"); err != nil && !os.IsNotExist(err) {
		return 0, 0, nil, err
	}
	return StripBlackFlash(ctx, f, path, fps)
}

// hasAudioStream 这条片段有没有音轨。
func hasAudioStream(ctx context.Context, f *ff.FF, path string) (bool, error) {
	info, err := f.Probe(path)
	if err != nil {
		return false, err
	}
	return info.HasAudio, nil
}
