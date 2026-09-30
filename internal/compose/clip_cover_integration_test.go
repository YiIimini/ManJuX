package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// **真跑一遍**的集成检查（2026-09-30 用户：「让你好好检查好好检查」）。
//
// 前面的守卫都是"判据对不对"，这一个证明**在这台机器上真的能跑**：
//   - 音频链（highpass + afftdn + lowpass + loudnorm）在本地 ffmpeg 里能不能解析——
//     滤镜名写错、参数越界、`loudnorm` 版本差异，都只有真跑才知道；
//   - 集封面那条（scale + crop + drawtext + 中文字体 + Windows 路径转义）能不能出图 ——
//     字体缺失、textfile 编码、路径转义漏一个字符，也都只有真跑才知道。
//
// 没有 ffmpeg 时**如实跳过**（不假装验过）。
func TestClipAndCoverRunForReal(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机没有 ffmpeg，跳过真跑检查：%v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()

	// ---- ① 片段音频净化：合成一段 3 秒测试片段（无声视频 + 正弦音）并就地净化 ----
	clip := filepath.Join(dir, "shot_001.mp4")
	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=gray:s=320x568:r=24:d=3",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", clip); err != nil {
		t.Skipf("本机 ffmpeg 编不了测试片段（缺 libx264/aac？）：%v", err)
	}
	before, err := f.Probe(clip)
	if err != nil {
		t.Fatalf("净化前 probe 失败：%v", err)
	}
	if err := NormalizeClipAudio(ctx, f, clip, Params{Loudness: -16, Peak: 0.92}); err != nil {
		t.Fatalf("片段音频净化真跑失败（滤镜名/参数有问题？）：%v", err)
	}
	after, err := f.Probe(clip)
	if err != nil {
		t.Fatalf("净化后 probe 失败（说明文件被写坏了）：%v", err)
	}
	if !after.HasAudio {
		t.Fatal("净化后音轨没了")
	}
	if after.Duration < before.Duration-0.2 {
		t.Errorf("净化把时长改短了：%.2f → %.2f（视频流应当 copy）", before.Duration, after.Duration)
	}
	if _, err := os.Stat(clip + ".audnorm"); err != nil {
		t.Error("没有落幂等标记")
	}
	// 再跑一次：应当直接跳过（不重复处理）
	if err := NormalizeClipAudio(ctx, f, clip, Params{Loudness: -16, Peak: 0.92}); err != nil {
		t.Errorf("第二次净化应当跳过，实得 %v", err)
	}

	// ---- ② 集封面：拿同一段当"成片"，真出三张候选 + 默认封面 ----
	out := filepath.Join(dir, "output")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	cover, err := EpisodeCover(ctx, f, clip, out, "第01集·碑上写我死了", 540, 960, 0)
	if err != nil {
		t.Fatalf("集封面真跑失败（字体/路径转义/drawtext 有问题？）：%v", err)
	}
	if p, err := f.Probe(cover); err != nil || p.Width != 540 || p.Height != 960 {
		t.Fatalf("封面尺寸不对：%v %+v", err, p)
	}
	cands, _ := filepath.Glob(filepath.Join(out, ".cover-candidates", "cover_*.jpg"))
	if len(cands) != len(CoverCandidateRatios) {
		t.Errorf("候选张数不对：得到 %d 张 %v", len(cands), cands)
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err != nil || st.Size() == 0 {
			t.Errorf("候选是空文件：%s", c)
		}
	}
}
