// Package compose 负责合成成片:镜头规范化 → 拼接(硬切/叠化)→
// 响度归一化 → 字幕烧录 → faststart。
// 关键纪律(知识库):H3 源视频仅首帧为 IDR,任何裁剪/拼接必须重编码,
// 禁止流复制;成片必须 +faststart(微信/播放器起播兼容)。
package compose

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/sysexec"
	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
)

// Params 合成参数。
type Params struct {
	Width, Height int
	Transition    string // cut / fade / dissolve
	BurnSubtitles bool
	SubtitleSize  int    // 字号(屏高百分比)
	Loudness      int    // LUFS
	FadeSec       float64 // 转场时长
}

// Result 合成产物。
type Result struct {
	FinalPath string
	SRTPath   string
	Duration  float64
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
		if _, err := f.Run(ctx, "-y", "-i", clip,
			"-vf", vf,
			"-af", "aresample=48000",
			"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "192k", "-ac", "2",
			part); err != nil {
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

	// ---- 步骤二:拼接 ----
	var master string
	switch p.Transition {
	case "fade", "dissolve":
		master, err = stitchWithTransitions(ctx, f, parts, durations, work, p, onProgress)
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
		_ = os.WriteFile(srtPath, []byte(BuildSRT(plan, durations)), 0o644)
	}
	total := sum(durations)

	finalPath := filepath.Join(outDir, "final.mp4")
	args := []string{"-y", "-i", master}
	if p.BurnSubtitles && plan != nil {
		ass := filepath.Join(outDir, "final.ass")
		if err := os.WriteFile(ass, []byte(BuildASS(plan, durations, p.SubtitleSize, p.Width, p.Height)), 0o644); err != nil {
			return nil, err
		}
		// 相对文件名 + 工作目录,规避 Windows 盘符转义;libass 不接受带引号的文件名
		if err := runIn(ctx, f.Ffmpeg, outDir, append(args,
			"-vf", "subtitles=filename=final.ass",
			"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
			"-af", fmt.Sprintf("loudnorm=I=%d:TP=-1.5:LRA=11", p.Loudness),
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
			"-movflags", "+faststart", finalPath)...); err != nil {
			return nil, fmt.Errorf("终混失败: %w", err)
		}
	} else {
		if _, err := f.Run(ctx, append(args,
			"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
			"-af", fmt.Sprintf("loudnorm=I=%d:TP=-1.5:LRA=11", p.Loudness),
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
			"-movflags", "+faststart", finalPath)...); err != nil {
			return nil, fmt.Errorf("终混失败: %w", err)
		}
	}
	if onProgress != nil {
		onProgress(100, "合成完成")
	}
	failed = false // 成功:清理中间目录;失败保留 compose-*-failed 现场供排查
	return &Result{FinalPath: finalPath, SRTPath: srtPath, Duration: total}, nil
}

// stitchCut 硬切拼接(demuxer,同参数段直接 copy)。
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
	out := filepath.Join(work, "master.mp4")
	if _, err := f.Run(ctx, "-y", "-f", "concat", "-safe", "0", "-i", list,
		"-c", "copy", out); err != nil {
		return "", fmt.Errorf("拼接失败: %w", err)
	}
	return out, nil
}

// stitchWithTransitions xfade/acrossfade 链拼接(fade=闪黑叠化,dissolve=交叉叠化)。
func stitchWithTransitions(ctx context.Context, f *ff.FF, parts []string, durations []float64, work string, p Params, onProgress func(int, string)) (string, error) {
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
	// 视频链
	lastV := "[0:v]"
	acc := durations[0]
	for i := 1; i < n; i++ {
		outV := fmt.Sprintf("[v%d]", i)
		filters = append(filters, fmt.Sprintf("[%s][%d:v]xfade=transition=%s:duration=%.2f:offset=%.2f%s",
			lastV, i, p.Transition, d, acc-d, outV))
		lastV = outV
		acc += durations[i] - d
	}
	// 音频链
	lastA := "[0:a]"
	for i := 1; i < n; i++ {
		outA := fmt.Sprintf("[a%d]", i)
		filters = append(filters, fmt.Sprintf("[%s][%d:a]acrossfade=d=%.2f%s", lastA, i, d, outA))
		lastA = outA
	}
	// 头尾淡入淡出
	filters = append(filters, fmt.Sprintf("%sfade=t=in:st=0:d=0.5,fade=t=out:st=%.2f:d=0.5[vout]", lastV, acc-0.5))
	args = append(args,
		"-filter_complex", strings.Join(filters, ";"),
		"-map", "[vout]", "-map", lastA,
		"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		filepath.Join(work, "master.mp4"))
	if _, err := f.Run(ctx, args...); err != nil {
		return "", err
	}
	return filepath.Join(work, "master.mp4"), nil
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

// BuildSRT 依据镜头实际时长生成 SRT 时间轴。
func BuildSRT(plan *project.Plan, durations []float64) string {
	var sb strings.Builder
	idx := 1
	var t float64
	for i, shot := range plan.Shots {
		d := 7.0
		if i < len(durations) {
			d = durations[i]
		}
		text := director.ShotSubtitle(shot)
		if strings.TrimSpace(text) == "" {
			t += d
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
		t += d
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
// playW/playH 为成片画幅,字幕网格与字号随画幅缩放。
func BuildASS(plan *project.Plan, durations []float64, sizePct int, playW, playH int) string {
	if sizePct <= 0 {
		sizePct = 5
	}
	if playW < 32 || playH < 32 {
		playW, playH = 1080, 1920
	}
	fontSize := playH * sizePct / 100
	marginV := playH * 8 / 100
	marginLR := playW * 5 / 100
	var sb strings.Builder
	fmt.Fprintf(&sb, `[Script Info]
ScriptType: v4.00+
PlayResX: %d
PlayResY: %d
WrapStyle: 0
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Dialogue,Microsoft YaHei,%d,&H0020D9FF,&H000000FF,&H00101010,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,%d,%d,%d,1
Style: Narration,Microsoft YaHei,%d,&H00FFFFFF,&H000000FF,&H00101010,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,%d,%d,%d,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`, playW, playH, fontSize, marginLR, marginLR, marginV, fontSize, marginLR, marginLR, marginV)

	var t float64
	for i, shot := range plan.Shots {
		d := 7.0
		if i < len(durations) {
			d = durations[i]
		}
		// 逐条拆分显示:旁白/每段对白各占一条,按镜头时长均分;
		// 整镜堆一条会三四个段落同时压屏,小画幅下叠到画面中央不可读。
		parts := director.ShotSubtitleParts(shot)
		if len(parts) == 0 {
			t += d
			continue
		}
		style := "Dialogue"
		if strings.TrimSpace(shot.Narration) != "" && len(shot.Dialogue) == 0 {
			style = "Narration"
		}
		span := d * 0.8                     // 字幕显示窗口:镜头头尾各留 10%
		seg := span / float64(len(parts))   // 每条均分
		start := t + d*0.1
		for _, p := range parts {
			p = strings.ReplaceAll(strings.TrimSpace(p), "\n", `\N`)
			if p == "" {
				continue
			}
			fmt.Fprintf(&sb, "Dialogue: 0,%s,%s,%s,,0,0,0,,%s\n",
				assTime(start), assTime(start+seg), style, p)
			start += seg
		}
		t += d
	}
	return sb.String()
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
