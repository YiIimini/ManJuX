// Package qc 机械质检:时长/音轨/分辨率探测 + 抽帧亮度统计(黑屏防线)。
// 不做内容理解(那属于 LLM/VLM 审片,后续版本扩展)。
package qc

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/YiIimini/ManJuX/internal/audio"
	"github.com/YiIimini/ManJuX/internal/ff"
)

// Report 单镜质检报告。
type Report struct {
	ShotID    int        `json:"shot_id"`
	File      string     `json:"file"`
	OK        bool       `json:"ok"`
	Problems  []string   `json:"problems"`
	Warnings  []string   `json:"warnings"`
	Duration  float64    `json:"duration"`
	Width     int        `json:"width"`
	Height    int        `json:"height"`
	HasAudio  bool       `json:"has_audio"`
	DarkRatio float64    `json:"dark_ratio"`    // 暗像素占比(0-1)
	VLM       *VLMReport `json:"vlm,omitempty"` // 智能审片(可选)
	// SpeechSec 实测人声时长(秒);ExpectedSpeechSec 本镜台词应有的口播时长。
	// 两者差距过大就是「配音不完整」——这是用户 2026-09-24 报障里唯二可以
	// **机械判定**的一条(另一条是杂音,靠频谱,不在本包范围内)。
	SpeechSec         float64 `json:"speech_sec"`
	ExpectedSpeechSec float64 `json:"expected_speech_sec,omitempty"`
}

// Params 质检阈值。
type Params struct {
	DarkRatioFail float64
	DarkRatioWarn float64
	MinDuration   float64
	MaxDuration   float64
	// ExpectedSpeechSec 本镜台词的应有口播时长(秒);>0 时启用人声完整性判定。
	ExpectedSpeechSec float64
}

// speechWarnRatio 实测/应有 人声时长低于此比例即告警。
//
// 0.6 是刻意留松的:同一句话的语速会有 20-30% 的正常浮动,H3 也可能把两句之间的
// 停顿吃掉;低于六成就不像"语速快"而像"话没说完/整句没出声"。只是告警不阻断 ——
// 重渲同一提示词同一 seed 大概率复现,真正要改的是台词长度或拆镜。
const speechWarnRatio = 0.6

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
	} else {
		// 人声完整性:实测语音时长 vs 台词应有口播时长(见 Params.ExpectedSpeechSec)
		if env, err := audio.Decode(ctx, f, path); err == nil && env != nil {
			r.SpeechSec = env.SpeechSeconds(env.Speech(), 0, env.Duration())
			r.ExpectedSpeechSec = p.ExpectedSpeechSec
			if p.ExpectedSpeechSec > 1.0 && r.SpeechSec < p.ExpectedSpeechSec*speechWarnRatio {
				r.Warnings = append(r.Warnings, fmt.Sprintf(
					"疑似配音不完整:实测人声 %.1fs,台词应有约 %.1fs(不足六成)——检查台词是否被截断或整句未出声",
					r.SpeechSec, p.ExpectedSpeechSec))
			}
		} else if err != nil {
			r.Warnings = append(r.Warnings, "人声时长分析失败:"+err.Error())
		}
	}
	if info.Duration < p.MinDuration {
		r.Problems = append(r.Problems, fmt.Sprintf("时长过短 %.1fs(应≥%.0fs)", info.Duration, p.MinDuration))
	}
	if info.Duration > p.MaxDuration {
		r.Warnings = append(r.Warnings, fmt.Sprintf("时长偏长 %.1fs", info.Duration))
	}

	dark, err := darkRatio(ctx, f, path, info.Duration)
	if err == nil {
		r.DarkRatio = dark
		if dark >= p.DarkRatioFail {
			r.Problems = append(r.Problems, fmt.Sprintf("疑似黑屏:暗像素占比 %.0f%%(阈值 %.0f%%)", dark*100, p.DarkRatioFail*100))
		} else if dark >= p.DarkRatioWarn {
			r.Warnings = append(r.Warnings, fmt.Sprintf("画面偏暗:暗像素占比 %.0f%%", dark*100))
		}
	} else {
		// 抽帧失败**必须是 Problem**:量不到就没资格说"通过"(旧实现把它降级成
		// Warning 后照常给出 darkRatio=0 的结论,于是"质检通过"是假的)。
		r.Problems = append(r.Problems, "黑屏检测不可信(抽帧失败):"+err.Error())
	}
	r.OK = len(r.Problems) == 0
	return r
}

// darkRatio 按**实测时长**取 25% / 50% / 75% 三个时间点抽帧,统计暗像素占比。
//
// 2026-09-24 修(审计 P0-3):旧实现固定抽 1s/3s/5s,与时长无关,于是
//   - 长镜只看得到前 5 秒:第 6 秒起全黑也判"通过";
//   - 4 秒镜头的第三个采样点越界,ffmpeg exit=0 但没有产物,被 `continue` 吞掉,
//     函数照样返回一个数字 —— 结论建立在"只看了一两帧"甚至"一帧都没看"之上。
//
// 现在:采样点按时长定位且保证落在片内;任何一帧抽不出来都直接返回错误,
// 由调用方计入 Problems(见 Check)。
func darkRatio(ctx context.Context, f *ff.FF, video string, duration float64) (float64, error) {
	if duration <= 0 { // 调用方没给时长(或探测失败)时自己再探一次
		if info, err := f.Probe(video); err == nil {
			duration = info.Duration
		}
	}
	if duration <= 0 {
		return 0, fmt.Errorf("读不到视频时长,无法定位抽帧点")
	}
	tmpDir, err := os.MkdirTemp("", "manjux-qc-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmpDir)

	totalDark, totalPix := 0, 0
	for i, at := range samplePositions(duration) {
		out := filepath.Join(tmpDir, fmt.Sprintf("f_%d.png", i))
		if _, err := f.Run(ctx, "-y", "-ss", fmt.Sprintf("%.3f", at), "-i", video,
			"-frames:v", "1", "-vf", "scale=160:-2", out); err != nil {
			return 0, fmt.Errorf("第 %d 帧(%.2fs)抽帧失败:%w", i+1, at, err)
		}
		d, n, err := pngDark(out)
		if err != nil {
			return 0, fmt.Errorf("第 %d 帧(%.2fs)不可读:%w", i+1, at, err)
		}
		totalDark += d
		totalPix += n
	}
	if totalPix == 0 {
		return 0, fmt.Errorf("未成功抽帧")
	}
	return float64(totalDark) / float64(totalPix), nil
}

// samplePositions 25% / 50% / 75% 三个时间点,并保证都落在片内:
// 末点回退 edge 秒 —— `-ss` 超过时长的常见后果是"exit 0 但没有输出文件",
// 那正是旧实现静默吞掉的那种失败。
func samplePositions(duration float64) []float64 {
	const edge = 0.15
	last := duration - edge
	if last < 0 {
		last = 0
	}
	ps := []float64{duration * 0.25, duration * 0.5, duration * 0.75}
	for i := range ps {
		if ps[i] > last {
			ps[i] = last
		}
	}
	return ps
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
