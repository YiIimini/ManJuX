// Package qc 机械质检:时长/音轨/分辨率探测 + 抽帧亮度统计(黑屏防线)。
// 不做内容理解(那属于 LLM/VLM 审片,后续版本扩展)。
package qc

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// Report 单镜质检报告。
type Report struct {
	ShotID    int     `json:"shot_id"`
	File      string  `json:"file"`
	OK        bool    `json:"ok"`
	Problems  []string `json:"problems"`
	Warnings  []string `json:"warnings"`
	Duration  float64 `json:"duration"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	HasAudio  bool    `json:"has_audio"`
	DarkRatio float64 `json:"dark_ratio"` // 暗像素占比(0-1)
}

// Params 质检阈值。
type Params struct {
	DarkRatioFail float64
	DarkRatioWarn float64
	MinDuration   float64
	MaxDuration   float64
}

// Check 检查单个视频文件。
func Check(ctx context.Context, f *ff.FF, shotID int, path string, p Params) *Report {
	r := &Report{ShotID: shotID, File: path, Problems: []string{}, Warnings: []string{}}
	info, err := f.Probe(path)
	if err != nil {
		r.Problems = append(r.Problems, "无法探测:"+err.Error())
		return r
	}
	r.Duration, r.Width, r.Height, r.HasAudio = info.Duration, info.Width, info.Height, info.HasAudio
	if !info.HasAudio {
		r.Problems = append(r.Problems, "无音轨(H3 产物应自带 32kHz 立体声)")
	}
	if info.Duration < p.MinDuration {
		r.Problems = append(r.Problems, fmt.Sprintf("时长过短 %.1fs(应≥%.0fs)", info.Duration, p.MinDuration))
	}
	if info.Duration > p.MaxDuration {
		r.Warnings = append(r.Warnings, fmt.Sprintf("时长偏长 %.1fs", info.Duration))
	}

	dark, err := darkRatio(ctx, f, path)
	if err == nil {
		r.DarkRatio = dark
		if dark >= p.DarkRatioFail {
			r.Problems = append(r.Problems, fmt.Sprintf("疑似黑屏:暗像素占比 %.0f%%(阈值 %.0f%%)", dark*100, p.DarkRatioFail*100))
		} else if dark >= p.DarkRatioWarn {
			r.Warnings = append(r.Warnings, fmt.Sprintf("画面偏暗:暗像素占比 %.0f%%", dark*100))
		}
	} else {
		r.Warnings = append(r.Warnings, "亮度抽帧失败:"+err.Error())
	}
	r.OK = len(r.Problems) == 0
	return r
}

// darkRatio 抽 3 帧计算暗像素(<40/255)占比。
func darkRatio(ctx context.Context, f *ff.FF, video string) (float64, error) {
	tmpDir, err := os.MkdirTemp("", "manjux-qc-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmpDir)

	// 在 25% / 50% / 75% 处各抽一帧(依赖 ffmpeg 的 select+fps 简化:直接 3 个时间点)
	totalDark, totalPix := 0, 0
	for _, pos := range []string{"0.25", "0.5", "0.75"} {
		out := filepath.Join(tmpDir, "f_"+strings.ReplaceAll(pos, ".", "_")+".png")
		if _, err := f.Run(ctx, "-y", "-ss", posOf(pos), "-i", video,
			"-frames:v", "1", "-vf", "scale=160:-2", out); err != nil {
			continue
		}
		d, n, err := pngDark(out)
		if err != nil {
			continue
		}
		totalDark += d
		totalPix += n
	}
	if totalPix == 0 {
		return 0, fmt.Errorf("未成功抽帧")
	}
	return float64(totalDark) / float64(totalPix), nil
}

// posOf 以比例近似(拿不到总时长时按秒)。此处直接用秒位置(短视频 4-15s)。
func posOf(ratio string) string {
	switch ratio {
	case "0.25":
		return "1"
	case "0.5":
		return "3"
	default:
		return "5"
	}
}

// pngDark 解码 PNG 统计暗像素。
func pngDark(path string) (dark, total int, err error) {
	fh, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer fh.Close()
	img, err := png.Decode(fh)
	if err != nil {
		return 0, 0, err
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			// RGBA() 返回 16 位,转 8 位灰度(Rec.601 近似)
			gray := (r>>8*299 + g>>8*587 + bl>>8*114) / 1000
			total++
			if gray < 40 {
				dark++
			}
		}
	}
	return dark, total, nil
}
