package filmaudit

import (
	"bytes"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"

	"github.com/YiIimini/ManJuX/internal/compose"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// luma.go 画面亮度的**确定性体检**：镜内闪烁 + 同场相邻镜的亮度跳 + 镜内的**黑场**。
//
// 【为什么要有（2026-10-11，用户：「渲染出来的视频很不如意」）】这三件事此前**平台一处都没量**，
// 而它们是「看着不舒服但说不出哪儿不对」的典型来源。真机《星纪山海：我在废土养神兽》第01集
// 16 镜的实测（逐帧 YAVG，本机 ffmpeg 9.0.1，帧区间 170~255 帧）：
//
//   - **镜内闪烁**：帧间 |ΔYAVG| 的 95 分位 0.32~8.93、≥3 的大跳 0~29 次。镜 1（p95 8.93、
//     大跳 22 次）与镜 11（p95 6.27、大跳 29 次）判「闪」—— 观众看到的就是「忽明忽暗」；
//     其余 14 镜 p95 ≤1.68、大跳 ≤4 次（判据在这批真机上没误报）。
//   - **同场相邻镜亮度跳**：同一场次里相邻两镜的平均亮度差 15.4 / 25.6 / 24.5 / 19.6 四处超阈；
//     跨场次的两处（差 29.2 与 24.2）**不报** —— 夜戏接日戏本来就该不一样。
//   - **镜内黑场**：blackdetect 在这 16 镜里报出两处**纯黑**，都在镜首（镜 11 开头 0.542s、
//     镜 15 开头 0.583s），且都比平台自动剪的短黑闪上限（0.5 秒）长 ⇒ 渲染期按设计不剪，
//     只在渲染日志里留一句 warn —— 成片里「开头半秒黑」照旧。这一条此前**没有任何一处报**。
//
// 三项都只**如实播报**、不改画面：平台既有纪律是「观感口味类改动必须先抽帧 A/B」
// （见 docs/与官方的有意分歧.md 的不做时域降噪），所以这里只把「用户自己看出来」变成「平台先报出来」。
//
// **三态纪律**：没量到（LumaFn 为 nil / 解码失败）⇒ **不报**，也**不算合格** ——
// 现有的 Track 时长那组判据就是这个写法（`TestFilmTrackUnknownIsSilent`）。

const (
	// lumaStepBig 帧间亮度差多大算「大跳」（YAVG 是 0–255 尺度）。
	lumaStepBig = 3.0
	// lumaFlickerP95 帧间亮度差的 95 分位超过它 ⇒ 判「闪」。
	lumaFlickerP95 = 3.0
	// lumaFlickerBigCount 大跳次数超过它 ⇒ 也判「闪」（两个判据取"或"：抖得频繁与抖得猛都算）。
	lumaFlickerBigCount = 10
	// lumaNeighborDiff 同场次内相邻两镜的平均亮度差超过它 ⇒ 判「忽明忽暗」。
	lumaNeighborDiff = 15.0
)

// ShotLuma 一镜的亮度画像（全部由逐帧平均亮度 YAVG 算出来）。
//
// 为什么用 YAVG（而不是自己算一遍像素）：ffmpeg 的 signalstats 是**确定性**的，
// 且与平台别处（scene_exposure 的亮度口径）同一把尺子 —— 少一个"两处各算一套"的口径。
type ShotLuma struct {
	Frames   int     `json:"frames"`    // 量到多少帧（0 = 没量到）
	Mean     float64 `json:"mean"`      // 整镜平均亮度
	Min      float64 `json:"min"`       // 最暗那一帧
	MinAt    int     `json:"min_at"`    // 最暗帧的帧号（0-based）
	P95Step  float64 `json:"p95_step"`  // 帧间 |ΔYAVG| 的 95 分位
	MaxStep  float64 `json:"max_step"`  // 帧间 |ΔYAVG| 的最大值
	BigSteps int     `json:"big_steps"` // |ΔYAVG| ≥ lumaStepBig 的次数
	// Blacks 这一镜里的**黑场**（blackdetect 报的纯黑段，判据与阈值都来自 compose，
	// 见 compose.BlackDetectFilter / compose.BlackRunVerdict —— 与渲染期黑闪切除同一条口径）。
	Blacks []compose.BlackRun `json:"blacks,omitempty"`
}

// yavgRe 从 ffmpeg `signalstats` 的 metadata 输出里取逐帧平均亮度。
//
// 夹具是**真机原话**（2026-10-11 在本机 ffmpeg 9.0.1 上跑出来的样子）：
//
//	frame:0    pts:0       pts_time:0
//	lavfi.signalstats.YAVG=50.5504
var yavgRe = regexp.MustCompile(`YAVG=([0-9.]+)`)

// ParseYAVG 从 ffmpeg metadata 输出里取出逐帧平均亮度（纯函数，便于用真机日志当夹具）。
func ParseYAVG(out string) []float64 {
	var ys []float64
	for _, m := range yavgRe.FindAllStringSubmatch(out, -1) {
		v, err := strconv.ParseFloat(m[1], 64)
		if err == nil {
			ys = append(ys, v)
		}
	}
	return ys
}

// SummarizeLuma 把逐帧亮度收成一镜的画像（纯函数）。
func SummarizeLuma(ys []float64) ShotLuma {
	L := ShotLuma{Frames: len(ys)}
	if len(ys) == 0 {
		return L
	}
	sum, min := 0.0, ys[0]
	minAt := 0
	for i, y := range ys {
		sum += y
		if y < min {
			min, minAt = y, i
		}
	}
	L.Mean, L.Min, L.MinAt = sum/float64(len(ys)), min, minAt
	if len(ys) < 2 {
		return L
	}
	steps := make([]float64, 0, len(ys)-1)
	for i := 1; i < len(ys); i++ {
		d := math.Abs(ys[i] - ys[i-1])
		steps = append(steps, d)
		if d > L.MaxStep {
			L.MaxStep = d
		}
		if d >= lumaStepBig {
			L.BigSteps++
		}
	}
	sort.Float64s(steps)
	L.P95Step = steps[(len(steps)-1)*95/100]
	return L
}

// LumaFlicker 这一镜算不算「在闪」；第二返回值是给人看的理由（不算闪时为空串）。
//
// 判据两条取"或"：抖得**频繁**（大跳次数多）与抖得**猛**（95 分位高）都算 ——
// 真机实测这两类都出现过（镜 1 是"猛"：dMax 47.19；镜 3 是"频繁"：21 次大跳）。
func LumaFlicker(L ShotLuma) (bool, string) {
	if L.Frames < 2 {
		return false, ""
	}
	if L.P95Step > lumaFlickerP95 || L.BigSteps > lumaFlickerBigCount {
		return true, fmt.Sprintf("帧间亮度跳的 95 分位 %.2f、≥%.0f 的大跳 %d 次（阈值：95 分位 %.1f / 次数 %d）",
			L.P95Step, lumaStepBig, L.BigSteps, lumaFlickerP95, lumaFlickerBigCount)
	}
	return false, ""
}

// LumaFn 量一镜的亮度画像；nil = 这台机器没有这把尺子（体检会跳过这一项，且**不当成合格**）。
type LumaFn func(path string) (ShotLuma, error)

// LumaFromFFmpeg 用 ffmpeg 量一镜的亮度画像：**一次解码**同时拿两样东西 ——
// 逐帧 YAVG（signalstats，走 stdout）与**黑场**（blackdetect，走 stderr 日志）。
//
// 为什么要单独走 exec 而不是 `ff.Run`：`ff.Run` 只收 stderr（`cmd.Stdout = nil`），
// 而 `metadata=print:file=-` 是往 **stdout** 写的（2026-10-11 实测踩过这一脚：
// 用 ff.Run 量出来永远是 0 行，然后被当成「这一项没问题」）。这里两条流都要：
// stdout 收 YAVG、stderr 收 blackdetect 的日志行（所以两个都自己接上）。
//
// 为什么要 `-v info` 而不是 `-v error`：blackdetect 是**在 info 级别写日志**的，
// `-v error` 会把它整段吞掉（2026-10-11 实测：同一条命令 `-v error` 拿到 0 行 black_start，
// `-v info` 拿到 1 行）—— 而 YAVG 那条是 metadata 滤镜往 stdout 打印的，与日志级别无关。
// 两个滤镜串在一条链上，**黑场用的就是 compose 那条**（唯一出处，避免"两处各判一套"）。
func LumaFromFFmpeg(ffmpegPath string) LumaFn {
	return func(path string) (ShotLuma, error) {
		if ffmpegPath == "" {
			return ShotLuma{}, fmt.Errorf("没有 ffmpeg 路径")
		}
		chain := "signalstats," + compose.BlackDetectFilter() + ",metadata=print:file=-:key=lavfi.signalstats.YAVG"
		// 子进程一律经 sysexec.Hide（平台是 windowsgui 程序：不加 CREATE_NO_WINDOW 的话，
		// 每量一镜都会在桌面上闪一个黑窗 —— 这道规矩有门盯着，漏一处就红）。
		cmd := sysexec.Hide(exec.Command(ffmpegPath, "-v", "info", "-i", path, "-an", "-vf", chain, "-f", "null", "-"))
		var log bytes.Buffer
		cmd.Stderr = &log
		out, err := cmd.Output()
		if err != nil {
			return ShotLuma{}, err
		}
		ys := ParseYAVG(string(out))
		if len(ys) == 0 {
			return ShotLuma{}, fmt.Errorf("没取到逐帧亮度（signalstats 没输出）")
		}
		L := SummarizeLuma(ys)
		L.Blacks = compose.ParseBlackRuns(log.String())
		return L, nil
	}
}

// lumaAt 取第 i 镜的画像（没量到/越界 ⇒ false）。
func lumaAt(ls []ShotLuma, i int) (ShotLuma, bool) {
	if i < 0 || i >= len(ls) || ls[i].Frames < 2 {
		return ShotLuma{}, false
	}
	return ls[i], true
}

// lumaFindings 三项亮度判据（镜内黑场 / 镜内闪 / 同场相邻镜亮度跳）。
// **没量到的镜一律跳过**（三态：没验过 ≠ 合格，但也不编一条）。
func lumaFindings(in Input) []Finding {
	p := in.Plan
	var out []Finding

	// ① 镜内的**黑场**（blackdetect 报的纯黑段，判据与阈值都问 compose —— 与渲染期黑闪切除同一口径）。
	// 为什么先判它：闪/暗的**根因**常常就是它（真机镜 11 开头 0.542s 纯黑 ⇒ 从黑里爬出来那十几帧
	// 逐帧亮度猛跳，于是②那条也会报）。先把根因说清，用户才知道该重出哪一镜、还是该掐掉开头。
	for i := range p.Shots {
		L, ok := lumaAt(in.Luma, i)
		if !ok {
			continue
		}
		for _, r := range L.Blacks {
			strippable, why := compose.BlackRunVerdict(r)
			// 建议要**可执行**：长黑段不是"自己想办法"，而是给一条能直接粘的命令。
			// 上限按这一段往上报一格（0.5417 ⇒ 0.6）；但超过**累计**上限的段平台任何档位都剪不了，
			// 那时不许给这条命令（给了也剪不掉 = 撒谎）。
			suggest := math.Ceil(r.Dur()*10) / 10
			advice := ""
			switch {
			case strippable:
				// 两种可能：这条片段是"剪之前"落盘的（存量），或那一次自检没做成
				// （真机 shot_015 连 .noblack 标记都没有）。两种情况都用同一条命令修 ——
				// 且**不许**给放宽上限的命令（那等于说平台默认坏了）。
				advice = "这一段够短，渲染期平台**本该**剪掉它（这条片段要么是剪之前就落盘的、" +
					"要么那一次自检没做成）—— 跑一次 `go run ./cmd/clipfix -project \"<项目 id>\" -apply` 就能修好"
			case suggest <= compose.MaxBlackTotalSec:
				advice = fmt.Sprintf("要么重出这一镜，要么**显式放宽**上限把它剪掉："+
					"`go run ./cmd/clipfix -project \"<项目 id>\" -max-black %.1f -apply`", suggest)
			default:
				advice = fmt.Sprintf("这一段比**累计上限 %.1f 秒**还长，平台任何档位都不会剪它 —— "+
					"要么重出这一镜，要么在别的软件里手工剪", compose.MaxBlackTotalSec)
			}
			out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("shot %d", p.Shots[i].ID),
				Msg: fmt.Sprintf("这一镜有 **%.2fs 的纯黑**（从 %.2fs 起）—— %s；%s", r.Dur(), r.Start, why, advice)})
		}
	}

	// ② 镜内闪
	for i := range p.Shots {
		L, ok := lumaAt(in.Luma, i)
		if !ok {
			continue
		}
		if flicker, why := LumaFlicker(L); flicker {
			out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("shot %d", p.Shots[i].ID),
				Msg: fmt.Sprintf("这一镜画面在**闪**（%s）—— 观众看到的是「忽明忽暗」；"+
					"换 seed 重出通常能好，也可以在后期加时域稳光（后者会改画面，要先抽帧对比）", why)})
		}
	}

	// ③ 同场相邻镜的平均亮度跳（跨场次的明暗差是**内容**，不报：夜戏接日戏本来就该不一样）
	for i := 1; i < len(p.Shots); i++ {
		prev, cur := p.Shots[i-1], p.Shots[i]
		if prev.Scene == "" || prev.Scene != cur.Scene {
			continue
		}
		a, ok1 := lumaAt(in.Luma, i-1)
		b, ok2 := lumaAt(in.Luma, i)
		if !ok1 || !ok2 {
			continue
		}
		if d := math.Abs(b.Mean - a.Mean); d > lumaNeighborDiff {
			out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("shot %d,%d", prev.ID, cur.ID),
				Msg: fmt.Sprintf("同一场戏里镜 %d→%d 的**平均亮度差 %.1f**（%.1f → %.1f，阈值 %.1f）"+
					"—— 剪辑点上像「灯被人拧了一把」；对着这两镜比一眼就知道要不要重出其中一镜",
					prev.ID, cur.ID, d, a.Mean, b.Mean, lumaNeighborDiff)})
		}
	}
	return out
}

// MeasureLuma 逐镜量亮度画像（与 clips 同序；量不到的镜留零值）。
//
// 第二返回值 = **量不到的镜下标**（调用方据此如实点名：没量到 ≠ 合格）。
// 路径为空（占位镜/越界）也计入"量不到"。
func MeasureLuma(luma LumaFn, clips []string) ([]ShotLuma, []int) {
	out := make([]ShotLuma, len(clips))
	var missing []int
	for i, clip := range clips {
		if luma == nil || clip == "" {
			missing = append(missing, i)
			continue
		}
		L, err := luma(clip)
		if err != nil || L.Frames < 2 {
			missing = append(missing, i)
			continue
		}
		out[i] = L
	}
	if len(missing) == len(clips) {
		return nil, missing
	}
	return out, missing
}
