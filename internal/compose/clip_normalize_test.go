package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// 片段级音频净化（2026-09-30 用户：「分镜视频的配音电流音太强了…另外配音的声音有点大声，吓人」）。
//
// 这一条的存在理由是**位置**：净化与响度归一原来只在合成整片时做，而工作台试听的是
// `clips/shot_0NN.mp4`（H3 原始音轨）⇒ 用户听到的就是没处理过的那个。
//
// 判据（不依赖真的跑 ffmpeg）：
//   - **幂等**：已有 `.audnorm` 标记时直接返回，不重复处理（反复 loudnorm 会把动态压没）；
//   - 片段不存在时报错（调用方只 warn，不当致命）；
//   - 参数为 nil / 空路径时安全返回。
func TestNormalizeClipAudioIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "shot_001.mp4")
	if err := os.WriteFile(clip, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 标记存在 ⇒ 跳过（拿不到 ffmpeg 也不该报错：根本不会去调它）
	if err := os.WriteFile(clip+".audnorm", []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NormalizeClipAudio(t.Context(), &ff.FF{Ffmpeg: "definitely-not-ffmpeg"}, clip, Params{Loudness: -16}); err != nil {
		t.Fatalf("已有标记时应当直接跳过，实得 %v", err)
	}
	// 片段不存在 ⇒ 报错（让调用方如实播报）
	missing := filepath.Join(dir, "nope.mp4")
	if err := NormalizeClipAudio(t.Context(), &ff.FF{Ffmpeg: "definitely-not-ffmpeg"}, missing, Params{}); err == nil {
		t.Error("片段不存在时应当报错")
	}
	// nil wrapper / 空路径 ⇒ 安全返回
	if err := NormalizeClipAudio(t.Context(), nil, clip, Params{}); err != nil {
		t.Errorf("nil wrapper 不该报错：%v", err)
	}
	if err := NormalizeClipAudio(t.Context(), &ff.FF{}, "  ", Params{}); err != nil {
		t.Errorf("空路径不该报错：%v", err)
	}
}

// 片段与整片必须用**同一套**净化链与响度口径（否则片段听着好、成片又变）。
func TestClipAudioUsesSameChainAsMaster(t *testing.T) {
	chain := denoiseChain()
	for _, want := range []string{"highpass", "afftdn", "lowpass"} {
		if !strings.Contains(chain, want) {
			t.Errorf("净化链缺 %s（电流音就是靠这三样去掉的）", want)
		}
	}
	loud := peakBaseFilter(-16, peakDBTP(0.92))
	if !strings.Contains(loud, "loudnorm=I=-16") {
		t.Errorf("响度口径不是母轨那一套：%s", loud)
	}
}
