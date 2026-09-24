// Package compose 负责合成成片:镜头规范化 → 拼接(硬切/叠化)→
// 响度归一化 → 字幕烧录 → faststart。
// 关键纪律(知识库):H3 源视频仅首帧为 IDR,任何裁剪/拼接必须重编码,
// 禁止流复制;成片必须 +faststart(微信/播放器起播兼容)。
//
// 音频纪律(2026-09-24 用户报「配音也不完整 / 有杂音」后成文,改之前先读):
//   - **不许切在字上**:叠化转场必须让出与画面重叠等长的音频,让出哪一段是可选的
//     ——切成静音上(见 internal/audio 的 SolveCut),人声一个字都不丢;
//   - **每镜音频先补到与画面等长**(apad + -shortest):H3 偶有音轨比画面短的情况,
//     不补的话裁剪区间会越界,只能静默丢帧;
//   - **中间轨一律无损**(flac/mkv):拼接不是"再压一遍 AAC"的地方,否则每过一道
//     就多一层嘶声 —— 这正是"有杂音"的一半来源;
//   - **响度量两遍**(measure → linear 应用):单遍 loudnorm 是逐帧动态调增益,
//     会在对话与静音之间"抽气",听起来像底噪忽大忽小。
package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/audio"
	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// Params 合成参数。
type Params struct {
	Width, Height int
	Transition    string // cut / fade / dissolve
	BurnSubtitles bool
	SubtitleSize  int     // 字号(屏高百分比)
	Loudness      int     // LUFS
	FadeSec       float64 // 转场时长
	// Peak 真峰值上限 dBTP(loudnorm 的 TP;项目参数 assemble.peak,默认 0.92 线性
	// 即约 -0.72 dBTP)。
	//
	// 为什么必须由调用方给(审计 P1-1):`assemble.peak` 这个参数一直存在、界面能调、
	// Sanitize 也在校验,但真正生效的地方(两处 loudnorm 滤镜串)写死 `TP=-1.5` ——
	// 调它没有任何效果。0 表示"用默认值"(调用方不关心时的安全行为)。
	Peak float64
	// Denoise 音频净化:高通去隆隆声 + 频谱降噪去嘶声/底噪(默认开)。
	Denoise bool
}

// defaultPeakLinear peak 参数的默认值(0.92 线性 ≈ -0.72 dBTP)。
const defaultPeakLinear = 0.92

// peakDBTP 把项目的线性 peak 参数折算成 loudnorm 的 TP(dBTP)。
//
// 换算:dBTPS = 20·log10(线性峰值)。0.92 → -0.72 dBTP,比旧写死的 -1.5 略紧
// (更响一点),这正是设置页那个数字原本的意思。
func peakDBTP(linear float64) float64 {
	if linear <= 0 || linear > 1 {
		linear = defaultPeakLinear
	}
	return 20 * math.Log10(linear)
}

// Result 合成产物。
type Result struct {
	FinalPath string
	SRTPath   string
	Duration  float64
	// AudioNotes 音频处理实况(切点是否落在静音、降噪是否生效),供管线如实回显。
	AudioNotes []string
}

// Compose 执行合成。clips 为有序镜头文件(绝对路径),plan 用于字幕。
func Compose(ctx context.Context, f *ff.FF, clips []string, plan *project.Plan, outDir string, p Params, onProgress func(pct int, note string)) (*Result, error) {
	if len(clips) == 0 {
		return nil, fmt.Errorf("没有可合成的镜头")
	}
	work, err := os.MkdirTemp(outDir, "compose-")
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if !failed {
			os.RemoveAll(work)
		} else {
			os.Rename(work, work+"-failed") // 保留排查现场
		}
	}()
	res := &Result{}

	// ---- 步骤一:逐镜规范化(重编码,统一分辨率/fps/采样)----
	if onProgress != nil {
		onProgress(5, "镜头规范化(重编码)")
	}
	parts := make([]string, 0, len(clips))
	durations := make([]float64, 0, len(clips))
	for i, clip := range clips {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		part := filepath.Join(work, fmt.Sprintf("part_%03d.mp4", i))
		vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=24,setsar=1",
			p.Width, p.Height, p.Width, p.Height)
		// apad + -shortest:音轨补齐到与画面等长。H3 产物偶有音轨略短于视频,
		// 不补的话后面按"画面时长"裁音频会越界(表现为末尾静音或整轨前移)。
		if _, err := f.Run(ctx, "-y", "-i", clip,
			"-vf", vf,
			"-af", "aresample=48000,apad",
			"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "192k", "-ac", "2",
			"-shortest", part); err != nil {
			return nil, fmt.Errorf("镜头 %d 规范化失败: %w", i+1, err)
		}
		info, err := f.Probe(part)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		durations = append(durations, info.Duration)
		if onProgress != nil {
			onProgress(5+(i+1)*50/len(clips), fmt.Sprintf("规范化 %d/%d", i+1, len(clips)))
		}
	}

	// 每镜在成片中的实际起始时间:转场(fade/dissolve)两镜重叠 FadeSec,
	// 成片总长比各镜累加短 (n-1)*FadeSec —— 字幕时间轴必须按此偏移,
	// 否则转场模式下字幕会逐步超前画面(2026-09-09 修正)。
	trans := p.Transition
	fd := p.FadeSec
	if fd <= 0 {
		fd = 0.5
	}
	starts := make([]float64, len(parts))
	{
		var acc float64
		for i, d := range durations {
			starts[i] = acc
			acc += d
			if trans != "cut" && i < len(parts)-1 && d > fd {
				acc -= fd
			}
		}
	}

	// ---- 步骤二:拼接 ----
	var master string
	// dissolve(xfade 像素溶解,颗粒噪点观感)按用户 2026-09-10 定调不再使用,
	// 存量项目参数里的 dissolve 一律按 fade(平滑交叉叠化)执行,不迁移数据。
	if p.Transition == "dissolve" {
		p.Transition = "fade"
	}
	switch p.Transition {
	case "fade":
		master, err = stitchWithTransitions(ctx, f, parts, durations, work, p, res, onProgress)
		if err != nil {
			return nil, err
		}
	default: // cut
		master, err = stitchCut(ctx, f, parts, work, onProgress)
		if err != nil {
			return nil, err
		}
	}

	// ---- 步骤三:字幕 + 响度 + faststart 终混 ----
	if onProgress != nil {
		onProgress(85, "字幕与响度终混")
	}
	srtPath := filepath.Join(outDir, "final.srt")
	if plan != nil {
		_ = os.WriteFile(srtPath, []byte(BuildSRT(plan, starts, durations)), 0o644)
	}
	total := sum(durations)
	if trans != "cut" && len(parts) > 1 {
		total -= float64(len(parts)-1) * fd // 转场重叠扣除后的真实成片时长
	}
	res.SRTPath = srtPath

	chain, _, note := prepareAudio(ctx, f, master, p)
	res.AudioNotes = append(res.AudioNotes, note)
	if onProgress != nil && note != "" {
		onProgress(88, note)
	}

	finalPath := filepath.Join(outDir, "final.mp4")
	args := []string{"-y", "-i", master, "-af", chain}
	if p.BurnSubtitles && plan != nil {
		ass := filepath.Join(outDir, "final.ass")
		if err := os.WriteFile(ass, []byte(BuildASS(plan, starts, durations, p.SubtitleSize, p.Width, p.Height)), 0o644); err != nil {
			return nil, err
		}
		// 相对文件名 + 工作目录,规避 Windows 盘符转义;libass 不接受带引号的文件名
		if err := runIn(ctx, f.Ffmpeg, outDir, append(args,
			"-vf", "subtitles=filename=final.ass",
			"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
			"-movflags", "+faststart", finalPath)...); err != nil {
			return nil, fmt.Errorf("终混失败: %w", err)
		}
	} else {
		if _, err := f.Run(ctx, append(args,
			"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
			"-movflags", "+faststart", finalPath)...); err != nil {
			return nil, fmt.Errorf("终混失败: %w", err)
		}
	}
	if onProgress != nil {
		onProgress(100, "合成完成")
	}
	failed = false // 成功:清理中间目录;失败保留 compose-*-failed 现场供排查
	res.FinalPath = finalPath
	res.Duration = total
	return res, nil
}

// ---------- 音频链 ----------

// denoiseChain 净化链:高通砍掉隆隆声/直流,afftdn 压嘶声与宽带底噪。
//
// 参数刻意保守(nr=12dB、噪声基准 -30dBFS):频谱降噪开大了会让语音发闷发"水",
// 宁可少降一点。用户报的"杂音"里,这一链能治的是嘶声/底噪/低频轰鸣;
// H3 自己写在音景里的持续环境声压不掉(那要靠分镜侧的音景纪律,见 director.go)。
func denoiseChain() string {
	return "highpass=f=70,afftdn=nr=12:nf=-30"
}

// loudnormMeasured 一遍测量得到的响度参数(两遍法的第一遍产物)。
type loudnormMeasured struct {
	InputI       string
	InputTP      string
	InputLRA     string
	InputThresh  string
	TargetOffset string
}

// loudnormReport ffmpeg loudnorm 打在 stderr 里的 JSON 报告(数字用 json.Number
// 保留原始文本,直接交给滤镜串,避免浮点格式化引入偏差)。
type loudnormReport struct {
	InputI       json.Number `json:"input_i"`
	InputTP      json.Number `json:"input_tp"`
	InputLRA     json.Number `json:"input_lra"`
	InputThresh  json.Number `json:"input_thresh"`
	TargetOffset json.Number `json:"target_offset"`
}

// prepareAudio 依次尝试:净化 + 两遍响度 → 净化 + 单遍 → 仅两遍 → 仅单遍,
// 返回最终可用的音频链与说明。为什么要逐级回退:净化滤镜要较新的 ffmpeg,
// 而用户机器上的 ffmpeg 来自系统 PATH —— 缺滤镜时就该降级而不是整片合成失败。
func prepareAudio(ctx context.Context, f *ff.FF, master string, p Params) (string, *loudnormMeasured, string) {
	tp := peakDBTP(p.Peak)
	base := peakBaseFilter(p.Loudness, tp)
	cands := []struct{ chain, label string }{}
	if p.Denoise {
		cands = append(cands, struct{ chain, label string }{denoiseChain() + ",", "音频净化"})
	}
	cands = append(cands, struct{ chain, label string }{"", "原声"})

	for _, c := range cands {
		if meas := measureLoudness(ctx, f, master, c.chain+base); meas != nil {
			return c.chain + loudnormApplied(meas, p.Loudness, tp), meas,
				c.label + " + 两遍响度归一(线性增益,不抽气)"
		}
		// 测量失败多为滤镜不可用:同一条链退回单遍写法再试
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error", "-i", master,
			"-af", c.chain+base, "-f", "null", "-"); err == nil {
			if c.chain == "" {
				return base, nil, "原声 + 单遍响度归一(两遍法不可用,已降级)"
			}
			return c.chain + base, nil, c.label + " + 单遍响度归一(两遍法不可用,已降级)"
		}
	}
	return base, nil, "⚠ 音频链全部回退失败,按单遍响度归一继续(音质可能不稳)"
}

// peakBaseFilter loudnorm 的基础滤镜串(含目标响度与真峰值上限)。
//
// 抽成函数是为了让"项目的 peak 参数到底有没有进滤镜串"可被断言 ——
// 这正是审计 P1-1 里 `assemble.peak` 那个假开关的根因:参数存在、界面能调,
// 而真正生效的 filture 串里写死 TP=-1.5。
func peakBaseFilter(loudness int, tp float64) string {
	return fmt.Sprintf("loudnorm=I=%d:TP=%.2f:LRA=11", loudness, tp)
}

// loudnormApplied 用测量值构造第二遍(linear=true)的 loudnorm 滤镜串。
func loudnormApplied(m *loudnormMeasured, target int, tp float64) string {
	if m == nil || m.InputI == "" {
		return fmt.Sprintf("loudnorm=I=%d:TP=%.2f:LRA=11", target, tp)
	}
	return fmt.Sprintf("loudnorm=I=%d:TP=%.2f:LRA=11:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true",
		target, tp, m.InputI, m.InputTP, m.InputLRA, m.InputThresh, m.TargetOffset)
}

// measureLoudness 第一遍:只分析不输出,从 stderr 里抠出 loudnorm 的 JSON 报告。
func measureLoudness(ctx context.Context, f *ff.FF, input, chain string) *loudnormMeasured {
	out, _ := f.Run(ctx, "-hide_banner", "-i", input,
		"-af", chain+":print_format=json", "-f", "null", "-")
	a := strings.LastIndex(out, "{")
	b := strings.LastIndex(out, "}")
	if a < 0 || b <= a {
		return nil
	}
	// loudnorm 的报告是 stderr 里最后一个 JSON 对象
	var probe loudnormReport
	if json.Unmarshal([]byte(out[a:b+1]), &probe) != nil {
		return nil
	}
	if probe.InputI == "" {
		return nil
	}
	return &loudnormMeasured{
		InputI: probe.InputI.String(), InputTP: probe.InputTP.String(),
		InputLRA: probe.InputLRA.String(), InputThresh: probe.InputThresh.String(),
		TargetOffset: probe.TargetOffset.String(),
	}
}

// stitchCut 硬切拼接:视频流复制(零损失),音频用 concat 滤镜重建。
//
// 为什么音频不能跟着视频一起走 demuxer 的 -c copy(旧实现):AAC 每段自带
// ~1024 采样(21ms)的编码器 priming,concat demuxer 按包拼接时这些 priming 会变成
// 接缝处的缝隙/错位,听感就是"每切一刀咔一下"。重建音频(aresample=async=1 把时间轴
// 拉直)+ 一次无损编码,接缝才真正干净。
// 中间轨用 flac/mkv:无损,最终终混那一道才编码 AAC,不为拼接多付一次有损压缩。
func stitchCut(ctx context.Context, f *ff.FF, parts []string, work string, onProgress func(int, string)) (string, error) {
	if onProgress != nil {
		onProgress(60, "拼接(硬切)")
	}
	list := filepath.Join(work, "concat.txt")
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString("file '" + strings.ReplaceAll(p, "\\", "/") + "'\n")
	}
	if err := os.WriteFile(list, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	out := filepath.Join(work, "master.mkv")
	if _, err := f.Run(ctx, "-y", "-f", "concat", "-safe", "0", "-i", list,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c:v", "copy",
		"-af", "aresample=48000:async=1:first_pts=0",
		"-c:a", "flac", out); err != nil {
		return "", fmt.Errorf("拼接失败: %w", err)
	}
	return out, nil
}

// stitchWithTransitions xfade 视频链 + **按静音落刀**的音频链拼接。
//
// 视频:xfade 叠化,链式推进(旧实现,不动)。
// 音频:每个接缝要交出 fd 秒(与画面重叠等长),旧实现固定砍在重叠区中点 ——
// 等于每镜头尾各砍 fd/2,台词压在头尾就被切掉半句(用户报「配音不完整」的根因)。
// 现在改为:用 internal/audio 的能量包络求解切点,让"让出去的这段"尽量落在没人声的
// 地方;两镜都可裁区间内人人声时,自然退回中点(与旧行为一致,不留新问题)。
func stitchWithTransitions(ctx context.Context, f *ff.FF, parts []string, durations []float64, work string, p Params, res *Result, onProgress func(int, string)) (string, error) {
	if onProgress != nil {
		onProgress(60, "拼接(转场 "+p.Transition+")")
	}
	d := p.FadeSec
	if d <= 0 {
		d = 0.5
	}
	n := len(parts)
	args := []string{"-y"}
	for _, part := range parts {
		args = append(args, "-i", part)
	}
	var filters []string
	// 视频链(pad 名自带方括号,直接拼接;再包一层会变 [[0:v]] 导致解析失败)
	lastV := "[0:v]"
	acc := durations[0]
	for i := 1; i < n; i++ {
		outV := fmt.Sprintf("[v%d]", i)
		filters = append(filters, fmt.Sprintf("%s[%d:v]xfade=transition=%s:duration=%.2f:offset=%.2f%s",
			lastV, i, p.Transition, d, acc-d, outV))
		lastV = outV
		acc += durations[i] - d
	}

	// ---- 音频链:逐接缝求解切点 ----
	envs := make([]*audio.Envelope, n)
	for i, part := range parts {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		e, err := audio.Decode(ctx, f, part)
		if err != nil {
			envs[i] = nil // 解不出来就按"无包络"处理(该接缝回退中点切)
			continue
		}
		envs[i] = e
	}
	// head[i]/tail[i] 为该镜保留的音频区间(秒)。窗口内切点 t 的含义见 audio.SolveCut。
	heads := make([]float64, n)
	tails := make([]float64, n)
	silentCut := 0
	for i := 0; i < n; i++ {
		heads[i], tails[i] = 0, durations[i]
	}
	for i := 0; i+1 < n; i++ {
		if durations[i] <= d || durations[i+1] <= d {
			continue // 超短镜:整段保留(与旧实现的兜底一致)
		}
		t := audio.SolveCut(envs[i], durations[i]-d, envs[i+1], d, d)
		// 判定"这一刀落在静音里":让出的两段都没有人声
		if envs[i] != nil && envs[i+1] != nil {
			on := envs[i].Speech()
			onIn := envs[i+1].Speech()
			lost := envs[i].SpeechSeconds(on, durations[i]-d+t, durations[i]) +
				envs[i+1].SpeechSeconds(onIn, 0, t)
			if lost <= audio.Hop {
				silentCut++
			}
		}
		tails[i] = durations[i] - (d - t)
		heads[i+1] = t
	}
	if res != nil {
		res.AudioNotes = append(res.AudioNotes,
			fmt.Sprintf("接缝切点:%d/%d 处完全落在静音(其余按最接近静音的时点切,人声损失已最小化)", silentCut, n-1))
	}

	const afade = 0.02
	aParts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		head, tail := heads[i], tails[i]
		if tail <= head {
			head, tail = 0, durations[i]
		}
		f := fmt.Sprintf("[%d:a]apad=whole_dur=%.3f,atrim=%.3f:%.3f,asetpts=PTS-STARTPTS", i, durations[i], head, tail)
		seg := tail - head
		if seg > 8*afade {
			if head > 0 {
				f += fmt.Sprintf(",afade=t=in:st=0:d=%.3f", afade)
			}
			if tail < durations[i] {
				f += fmt.Sprintf(",afade=t=out:st=%.3f:d=%.3f", seg-afade, afade)
			}
		}
		filters = append(filters, f+fmt.Sprintf("[ap%d]", i))
		aParts = append(aParts, fmt.Sprintf("[ap%d]", i))
	}
	filters = append(filters, fmt.Sprintf("%sconcat=a=1:v=0:n=%d[aout]", strings.Join(aParts, ""), n))
	lastA := "[aout]"
	// 不加头尾强制淡入淡出:曾对成片首尾各加 0.5s 黑场渐变,用户观感为
	// 「开头/结尾闪黑窗」(2026-09-09 反馈)——镜间叠化已足够,成片直接
	// 从首镜画面开始、末镜画面结束。null=视频直通滤镜(nop 不存在);
	// 兼容单镜场景:无转场可链时 filter_complex 仍有内容。
	filters = append(filters, fmt.Sprintf("%snull[vout]", lastV))
	// 中间轨走 flac/mkv:视频已经因 xfade 重编码过一次,音频没必要再压一遍 AAC
	// (每多一道有损编码就多一层嘶声,而终混那一道还要编码一次)。
	out := filepath.Join(work, "master.mkv")
	args = append(args,
		"-filter_complex", strings.Join(filters, ";"),
		"-map", "[vout]", "-map", lastA,
		"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
		"-c:a", "flac",
		out)
	if _, err := f.Run(ctx, args...); err != nil {
		return "", err
	}
	return out, nil
}

// ShotSpeechBudget 该镜"应该有"的人声秒数(台词 + 旁白)。
//
// 放在 compose 包是因为它和成片音频同源(同一套语速常量);质检用它和实测语音时长
// 对比 —— 差太多就是"配音不完整"。
func ShotSpeechBudget(sh project.Shot) float64 {
	sec := 0.0
	for _, l := range sh.Dialogue {
		sec += director.SpeechSeconds(l.Text)
	}
	sec += director.SpeechSeconds(sh.Narration)
	return sec
}

// runIn 在指定工作目录执行(字幕相对路径方案)。
func runIn(ctx context.Context, bin, dir string, args ...string) error {
	cmd := sysexec.Hide(exec.CommandContext(ctx, bin, args...))
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 800 {
			tail = tail[len(tail)-800:]
		}
		return fmt.Errorf("%v\n%s", err, tail)
	}
	return nil
}

func sum(d []float64) float64 {
	var s float64
	for _, v := range d {
		s += v
	}
	return s
}

// ---------- 字幕生成 ----------

// BuildSRT 依据镜头实际起始/时长生成 SRT 时间轴(starts 为转场扣除后的镜起始)。
func BuildSRT(plan *project.Plan, starts, durations []float64) string {
	var sb strings.Builder
	idx := 1
	for i, shot := range plan.Shots {
		d := 7.0
		if i < len(durations) {
			d = durations[i]
		}
		t := 0.0
		if i < len(starts) {
			t = starts[i]
		}
		text := director.ShotSubtitle(shot)
		if strings.TrimSpace(text) == "" {
			continue
		}
		// 台词区约 8%-88%,多行均分
		lines := strings.Split(text, "\n")
		start, end := t+d*0.08, t+d*0.88
		seg := (end - start) / float64(len(lines))
		for j, ln := range lines {
			ls := start + seg*float64(j) + 0.05
			le := start + seg*float64(j+1) - 0.05
			if le-ls < 0.5 {
				le = ls + 0.5
			}
			fmt.Fprintf(&sb, "%d\n%s --> %s\n%s\n\n", idx, srtTime(ls), srtTime(le), ln)
			idx++
		}
	}
	return sb.String()
}

func srtTime(sec float64) string {
	ms := int(sec * 1000)
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// BuildASS 生成 ASS 字幕(中文字体+描边;台词金色/旁白白色)。
// playW/playH 为成片画幅,字幕网格与字号随画幅缩放;
// 超宽台词自动断行(中文无空格,ASS 智能换行不生效,单行超出即出界截断)。
func BuildASS(plan *project.Plan, starts, durations []float64, sizePct, playW, playH int) string {
	if sizePct <= 0 {
		sizePct = 4
	}
	if playW < 32 || playH < 32 {
		playW, playH = 1080, 1920
	}
	fontSize := playH * sizePct / 100
	marginV := playH * 8 / 100
	marginLR := playW * 5 / 100
	// 单行可容字数 ≈ 可用宽度/字宽(全角字宽≈字号);不足下限兜底
	maxRunes := int(float64(playW-marginLR*2) / float64(fontSize) * 0.98)
	if maxRunes < 6 {
		maxRunes = 6
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `[Script Info]
ScriptType: v4.00+
PlayResX: %d
PlayResY: %d
WrapStyle: 2
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Dialogue,Microsoft YaHei,%d,&H0020D9FF,&H000000FF,&H00101010,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,%d,%d,%d,1
Style: Narration,Microsoft YaHei,%d,&H00FFFFFF,&H000000FF,&H00101010,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,%d,%d,%d,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`, playW, playH, fontSize, marginLR, marginLR, marginV, fontSize, marginLR, marginLR, marginV)

	for i, shot := range plan.Shots {
		d := 7.0
		if i < len(durations) {
			d = durations[i]
		}
		t := 0.0
		if i < len(starts) {
			t = starts[i]
		}
		// 逐条拆分显示:旁白/每段对白各占一条,按镜头时长均分;
		// 整镜堆一条会三四个段落同时压屏,小画幅下叠到画面中央不可读。
		parts := director.ShotSubtitleParts(shot)
		if len(parts) == 0 {
			continue
		}
		style := "Dialogue"
		if strings.TrimSpace(shot.Narration) != "" && len(shot.Dialogue) == 0 {
			style = "Narration"
		}
		span := d * 0.8                   // 字幕显示窗口:镜头头尾各留 10%
		seg := span / float64(len(parts)) // 每条均分
		start := t + d*0.1
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			// 超宽行按画面宽度断行(标点处优先,避免生切;多行共用一个时间窗)
			p = wrapAssText(strings.ReplaceAll(p, "\n", `\N`), maxRunes)
			fmt.Fprintf(&sb, "Dialogue: 0,%s,%s,%s,,0,0,0,,%s\n",
				assTime(start), assTime(start+seg), style, p)
			start += seg
		}
	}
	return sb.String()
}

// wrapAssText 把超宽字幕行按 maxRunes 断为多行(以 \N 相连);
// 中文断点优先落在标点后,不得已才按字符硬切。
func wrapAssText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 12
	}
	lines := strings.Split(text, `\N`)
	var out []string
	for _, ln := range lines {
		runes := []rune(ln)
		for len(runes) > maxRunes {
			cut := maxRunes
			// 从行宽末往前找最近标点(至少留 2/3 行宽),整句处断更顺眼
			for j := maxRunes - 1; j >= maxRunes*2/3; j-- {
				if isAssBreak(runes[j]) {
					cut = j + 1
					break
				}
			}
			out = append(out, string(runes[:cut]))
			runes = runes[cut:]
		}
		if len(runes) > 0 {
			out = append(out, string(runes))
		}
	}
	return strings.Join(out, `\N`)
}

// isAssBreak 断行标点(中文/英文句读与括号)。
func isAssBreak(r rune) bool {
	return strings.ContainsRune("，。、；：！？,.!?;:…—～·—…\"'「」『』()（）【】", r)
}

func assTime(sec float64) string {
	cs := int(sec * 100)
	h := cs / 360000
	cs %= 360000
	m := cs / 6000
	cs %= 6000
	s := cs / 100
	cs %= 100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}
