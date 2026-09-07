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

// Run 执行 ffmpeg(实时收集 stderr 摘要)。
func (f *FF) Run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.Ffmpeg, args...)
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
	Duration  float64 `json:"duration"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	FPS       float64 `json:"fps"`
	HasAudio  bool    `json:"has_audio"`
	AudioRate int     `json:"audio_sample_rate"` // Hz
	VideoCodec string `json:"video_codec"`
	AudioCodec string `json:"audio_codec"`
}

type probeOut struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		SampleRate string `json:"sample_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe 探测媒体信息。
func (f *FF) Probe(path string) (*ProbeInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, f.Ffprobe,
		"-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path).Output()
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
		switch st.CodecType {
		case "video":
			info.Width, info.Height = st.Width, st.Height
			info.VideoCodec = st.CodecName
			info.FPS = parseRate(st.AvgFrameRate)
		case "audio":
			info.HasAudio = true
			info.AudioCodec = st.CodecName
			info.AudioRate, _ = strconv.Atoi(st.SampleRate)
		}
	}
	return info, nil
}

func parseRate(s string) float64 {
	// "24/1" 形式
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
