package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// clip_normalize.go **片段级**音频净化 + 响度归一（2026-09-30 用户：
// 「分镜视频的配音电流音太强了，需要优化处理掉，另外配音的声音有点大声，吓人」）。
//
// ## 为什么要有它
//
// 净化与响度归一原来**只在合成整片时**做（`prepareAudio`），而工作台里试听的是
// `clips/shot_0NN.mp4` —— 那是 **H3 的原始输出**：电流音没去、响度接近满刻度，
// 用户听到的就是那个（实测日志里"音频净化 + 两遍响度归一"只出现在合成那一步）。
//
// 片段与整片的口径必须一致：这边用**同一套净化链**（`denoiseChain`）与
// **同一套 loudnorm 目标**（`peakBaseFilter`：I/TP/LRA），只是**单遍**——
// 片段只要"听着正常"，不需要整片那种两遍精确对表（那要多跑一遍 ffmpeg，乘上每集几十镜不划算）。
//
// ## 幂等
//
// 处理完写一个**同名 `.audnorm` 标记**；已有标记就跳过。这样断点续跑、重复调用都不会
// 把已经归一过的片段再压一遍（反复 loudnorm 会把动态压没）。
//
// ## 视频流不动
//
// `-c:v copy` —— 只重编码音频（AAC 192k）。片段是渲染产物，重编码视频既慢又会掉画质。
type clipNormParams struct {
	Loudness int
	Peak     float64
}

// NormalizeClipAudio 就地净化一个片段（幂等；视频流原样复制）。
//
// 失败**不致命**：调用方只报一条 warn（片段仍是原始音轨，合成整片时还有一次净化兜底）。
func NormalizeClipAudio(ctx context.Context, f *ff.FF, path string, p Params) error {
	if f == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	marker := path + ".audnorm"
	if _, err := os.Stat(marker); err == nil {
		return nil // 已经处理过
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".audnorm.tmp.mp4")
	af := denoiseChain() + "," + peakBaseFilter(p.Loudness, peakDBTP(p.Peak))
	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-i", path,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
		"-af", af,
		"-movflags", "+faststart",
		tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	// 原子替换：先换内容再落标记，避免"标记在、内容还是旧的"。
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.WriteFile(marker, []byte("clip audio normalised\n"), 0o644)
}
