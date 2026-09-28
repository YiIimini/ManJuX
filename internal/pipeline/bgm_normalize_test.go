package pipeline

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// 配乐响度归一（2026-09-28 实测驱动）。
//
// 病根记在这里免得被删：第一次真跑 ACE-Step 出来的 60 秒曲子文件一切正常
// （2.03 MB / stereo 48 kHz / 时长准确），**但 mean_volume -63.2 dB、max -53.5 dB** ——
// 正常混音在 -30～-10。也就是说"文件是好的、听起来等于没有"，铺进成片会被已 loudnorm 的
// 母轨彻底压住。只有真跑 + 测量才发现得了，看代码永远看不出来。
func TestNormalizeBGMRaisesQuietTrack(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("没有 ffmpeg:%v", err)
	}
	dir := t.TempDir()
	// 造一段**极轻**的正弦（-60 dB 量级），模拟实测到的直出状态
	// 用 **wav**：mp3 编码极轻信号会丢掉内容（实测 -63 dB 的源测出来 -81），
	// 那是编码问题，会掩盖我们要测的东西
	quiet := filepath.Join(dir, "quiet.wav")
	if _, err := f.Run(context.Background(), "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-af", "volume=-45dB", "-ar", "48000", quiet); err != nil {
		t.Skipf("造测试音频失败:%v", err)
	}
	before, err := measureMeanVolume(context.Background(), f, quiet)
	if err != nil {
		t.Fatalf("测量失败:%v", err)
	}
	if before > -40 {
		t.Skipf("造的测试音频不够轻（%.1f dB），跳过", before)
	}
	if BGMLevelUsable(before) {
		t.Errorf("极轻的曲子不该判为可用（%.1f dB）", before)
	}
	b2, after, err := normalizeBGM(context.Background(), f, quiet)
	if err != nil {
		t.Fatalf("归一失败:%v", err)
	}
	if math.Abs(b2-before) > 1 {
		t.Errorf("归一前测量应稳定：一次 %.1f、二次 %.1f", before, b2)
	}
	if after <= before+10 {
		t.Errorf("归一后应显著变响：%.1f → %.1f", before, after)
	}
	// 目标是 -16 LUFS，容差给宽一点（3 秒的短音频 loudnorm 本来就不精确）
	if !BGMLevelUsable(after) {
		t.Errorf("归一后应判为可用，实际 %.1f dB", after)
	}
}

// TestNormalizeBGMLeavesLoudTrackAlone 已经够响的曲子不该被动（重编码一次没意义还掉质量）。
func TestNormalizeBGMLeavesLoudTrackAlone(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("没有 ffmpeg:%v", err)
	}
	dir := t.TempDir()
	loud := filepath.Join(dir, "loud.wav")
	if _, err := f.Run(context.Background(), "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-af", "volume=6dB", "-ar", "48000", loud); err != nil {
		t.Skipf("造测试音频失败:%v", err)
	}
	before, err := measureMeanVolume(context.Background(), f, loud)
	if err != nil {
		t.Fatalf("测量失败:%v", err)
	}
	// 前提检查：这份素材必须**确实够响**（高于"不必动"的那条线），否则测的就不是这条规则
	if before < bgmTargetLUFS-6 {
		t.Skipf("夹具不够响（%.1f dB，线在 %.1f），跳过", before, float64(bgmTargetLUFS-6))
	}
	b2, after, err := normalizeBGM(context.Background(), f, loud)
	if err != nil {
		t.Fatalf("归一失败:%v", err)
	}
	if math.Abs(b2-before) > 0.5 || math.Abs(after-before) > 0.5 {
		t.Errorf("够响的曲子应原样返回（前后测量都略过处理）：%.2f → %.2f（二次测量 %.2f）", before, after, b2)
	}
}

// TestMeasureMeanVolumeRejectsBrokenInput 测不到响度时必须**报错**，不能返回一个"看起来正常"的默认值。
func TestMeasureMeanVolumeRejectsBrokenInput(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("没有 ffmpeg:%v", err)
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "notaudio.mp3")
	if err := os.WriteFile(bad, []byte("this is not audio at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := measureMeanVolume(context.Background(), f, bad); err == nil {
		t.Error("损坏/非音频输入必须报错（返回默认值会让「极轻的曲子」看起来正常）")
	}
}
