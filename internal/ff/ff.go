// Package ff 封装 ffmpeg / ffprobe 调用(合成与质检的基础设施)。
package ff

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// FF ffmpeg/ffprobe 执行器。
type FF struct {
	Ffmpeg  string
	Ffprobe string
}

// New 依显式路径或 PATH 查找构建。
func New(ffmpegPath, ffprobePath string) (*FF, error) {
	f := &FF{}
	var err error
	if f.Ffmpeg, err = resolve(ffmpegPath, "ffmpeg"); err != nil {
		return nil, err
	}
	if f.Ffprobe, err = resolve(ffprobePath, "ffprobe"); err != nil {
		return nil, err
	}
	return f, nil
}

func resolve(path, name string) (string, error) {
	if path != "" {
		if _, err := exec.LookPath(path); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("%s 不存在: %s", name, path)
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("未在 PATH 中找到 %s,请安装 ffmpeg 或在设置中指定路径", name)
	}
	return p, nil
}

// Missing 未配置某个工具时的**统一错误**（2026-09-29 代码健康审计 P1-4）。
//
// ## 为什么要有这个函数
//
// 平台上"没配 ffmpeg/ffprobe"这件事，此前在 8 个地方各报各的，一共四种说法：
//
//	「未配置 ffmpeg」/「未配置 ffprobe」/「没有 ffmpeg」（5 处）/「未配置 ffmpeg,无法抽帧」
//
// 而**没有一条**提到设置页有 `ffmpeg_path` / `ffprobe_path` 输入框。
// 用户拿到「没有 ffmpeg」时，唯一可行动的信息是零 —— 他既不知道去哪配，
// 也不知道这是"没装"还是"没告诉平台装在哪"。
//
// 现在所有调用点都走这里，文案里**带上下一步动作**（去哪配）。
// 注意与 `New`/`resolve` 的分工：那两个管"启动时找不到"，这里管"运行时发现字段为空"。
func Missing(tool string) error {
	if tool == "" {
		tool = "ffmpeg"
	}
	return fmt.Errorf("未找到 %s（当前从 PATH 查找）—— 请在 设置 → 工具路径 填写 %s 的完整路径，"+
		"或把它装进 PATH 后重启平台", tool, tool)
}

// Run 执行 ffmpeg(实时收集 stderr 摘要)。
func (f *FF) Run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := sysexec.Hide(exec.CommandContext(ctx, f.Ffmpeg, args...))
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	cmd.Stdout = nil
	if err := cmd.Run(); err != nil {
		tail := lastLines(errBuf.String(), 8)
		return tail, fmt.Errorf("ffmpeg 失败: %v\n%s", err, tail)
	}
	return errBuf.String(), nil
}

// ProbeInfo ffprobe 摘要信息。
type ProbeInfo struct {
	Duration   float64 `json:"duration"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	HasAudio   bool    `json:"has_audio"`
	AudioRate  int     `json:"audio_sample_rate"` // Hz
	VideoCodec string  `json:"video_codec"`
	AudioCodec string  `json:"audio_codec"`
	// VideoDuration / AudioDuration **各自那条流**的时长（秒）。
	//
	// 【2026-10-09 真机 P0：为什么必须分开量】`Duration` 取的是 `format.duration`，
	// 也就是**两条流里最长的那个** —— 于是"视频轨被截断、音轨完好"这种成片，
	// format.duration 照样报完整长度（真机第01集：画面 23.7 秒、音轨 122.5 秒，
	// 而 format.duration 报 122.5），从时长到质检**没有任何一处**看得出来。
	// 所以这里把两条流分开量，成片时长对不上时才有判据可用。
	VideoDuration float64 `json:"video_duration"`
	AudioDuration float64 `json:"audio_duration"`
}

type probeOut struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		SampleRate   string `json:"sample_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
		Duration     string `json:"duration"`
		NbFrames     string `json:"nb_frames"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe 探测媒体信息。
func (f *FF) Probe(path string) (*ProbeInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := sysexec.Hide(exec.CommandContext(ctx, f.Ffprobe,
		"-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path)).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe 失败: %v", err)
	}
	var po probeOut
	if err := json.Unmarshal(out, &po); err != nil {
		return nil, err
	}
	info := &ProbeInfo{}
	info.Duration, _ = strconvParse(po.Format.Duration)
	for _, st := range po.Streams {
		// 流时长：优先 `duration`；mp4 里有时只给 nb_frames（用帧数/帧率折算）。
		d, _ := strconvParse(st.Duration)
		if d <= 0 && st.NbFrames != "" {
			if nf, err := strconv.ParseFloat(st.NbFrames, 64); err == nil && nf > 0 {
				if rate := parseRate(st.AvgFrameRate); rate > 0 {
					d = nf / rate
				}
			}
		}
		switch st.CodecType {
		case "video":
			info.Width, info.Height = st.Width, st.Height
			info.VideoCodec = st.CodecName
			info.FPS = parseRate(st.AvgFrameRate)
			info.VideoDuration = d
		case "audio":
			info.HasAudio = true
			info.AudioCodec = st.CodecName
			info.AudioRate, _ = strconv.Atoi(st.SampleRate)
			info.AudioDuration = d
		}
	}
	// 流时长拿不到时（老容器/缺字段）退回容器时长 —— 宁可少一个判据，也不要报假的 0。
	if info.VideoDuration <= 0 {
		info.VideoDuration = info.Duration
	}
	if info.HasAudio && info.AudioDuration <= 0 {
		info.AudioDuration = info.Duration
	}
	return info, nil
}

// parseRate 把 ffprobe 的 `24/1` 这种帧率串折算成数字。
func parseRate(s string) float64 {
	if i := strings.IndexByte(s, '/'); i > 0 {
		num, _ := strconvParse(s[:i])
		den, _ := strconvParse(s[i+1:])
		if den != 0 {
			return num / den
		}
	}
	v, _ := strconvParse(s)
	return v
}

func strconvParse(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
