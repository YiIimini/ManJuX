package qc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// 质检包的第一批测试(审计 P1-3:此前 0 测试,而它管的是"质检")。
//
// 起因(审计 P0-3):抽帧时间点是**写死的 1s/3s/5s**,与镜头时长无关 ——
//   - 10 秒镜头只看得到前 5 秒,后 5 秒全黑照样判"通过"(假通过);
//   - 4 秒镜头第三个点越界,ffmpeg exit=0 但无产物,被 `continue` 吞掉,
//     函数仍返回一个数字,结论建立在"只看了一两帧"上。
//
// 下面用**真实 ffmpeg** 造片验证:前半亮、后半黑的 10 秒片必须被判黑屏。

// newFF 找本机 ffmpeg(测试依赖真实二进制:抽帧是外部进程行为,
// 用假实现测不出"越界不报错"这类真实失败模式)。
func newFF(t *testing.T) *ff.FF {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("本机没有 ffmpeg,跳过(抽帧逻辑无法用假进程验证)")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("本机没有 ffprobe,跳过")
	}
	f, err := ff.New(ffmpeg, ffprobe)
	if err != nil {
		t.Fatalf("初始化 ff 失败: %v", err)
	}
	return f
}

// makeClip 生成一段指定时长、前后两段颜色的片子。
func makeClip(t *testing.T, dir string, total int, firstColorSec int, first, second string) string {
	t.Helper()
	out := filepath.Join(dir, "clip.mp4")
	rest := total - firstColorSec
	args := []string{"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=" + first + ":s=320x240:d=" + itoa(firstColorSec),
		"-f", "lavfi", "-i", "color=c=" + second + ":s=320x240:d=" + itoa(rest),
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]",
		"-map", "[v]", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-r", "24", out}
	cmd := exec.Command("ffmpeg", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("造测试片失败(%v):%s", err, string(b))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestDarkRatioSeesSecondHalf 回归 P0-3:后半段全黑的长镜必须被判黑屏。
//
// 旧实现抽 1s/3s/5s,这三个点全落在亮的那一半 → darkRatio≈0 → "质检通过"。
func TestDarkRatioSeesSecondHalf(t *testing.T) {
	f := newFF(t)
	dir := t.TempDir()
	clip := makeClip(t, dir, 10, 5, "white", "black")

	rep := Check(context.Background(), f, 1, clip, Params{
		DarkRatioFail: 0.60, DarkRatioWarn: 0.45,
		MinDuration: 1, MaxDuration: 60,
	})
	if rep.DarkRatio < 0.45 {
		t.Fatalf("后半段全黑的 10s 片,totalDarkRatio=%.2f 太低 —— 采样点大概率又只看前 5 秒了", rep.DarkRatio)
	}
	if !hasProblem(rep, "疑似黑屏") {
		t.Fatalf("应判黑屏,实际 problems=%v", rep.Problems)
	}
	if rep.OK {
		t.Error("判出问题后 OK 必须为 false")
	}
}

// TestSamplePositionsInsideClip 采样点必须落在片内(旧实现 4 秒片的第三个点是 5s)。
func TestSamplePositionsInsideClip(t *testing.T) {
	for _, dur := range []float64{2, 4, 5, 7, 10, 15} {
		for i, at := range samplePositions(dur) {
			if at < 0 || at >= dur {
				t.Errorf("时长 %.0fs 的第 %d 个采样点 %.2fs 落在片外", dur, i+1, at)
			}
		}
	}
	// 比例语义:短片的三个点必须都 <= 时长 - 安全边距
	ps := samplePositions(4)
	if ps[2] > 3.9 {
		t.Errorf("4 秒片的末点应留出安全边距,实际 %.2f", ps[2])
	}
}

// TestDarkRatioFailsInsteadOfGuessing 抽不出帧时必须**报错**,而不是返回一个
// 看着像结论的数字(调用方据此计入 Problems)。
func TestDarkRatioFailsInsteadOfGuessing(t *testing.T) {
	f := newFF(t)
	dir := t.TempDir()
	bad := filepath.Join(dir, "not_a_video.mp4")
	if err := os.WriteFile(bad, []byte("这不是视频"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := darkRatio(context.Background(), f, bad, 4); err == nil {
		t.Fatal("对非视频文件抽帧应返回错误,不能静默返回 0")
	}
	if _, err := darkRatio(context.Background(), f, bad, 0); err == nil {
		t.Fatal("时长未知且探测失败时应返回错误")
	}
}

// TestCheckReportsUnmeasurableAsProblem 量不到亮度 = 不能宣称通过。
func TestCheckReportsUnmeasurableAsProblem(t *testing.T) {
	f := newFF(t)
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.mp4")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := Check(context.Background(), f, 1, bad, Params{MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.6})
	if rep.OK {
		t.Fatal("无法探测的文件不应判通过")
	}
	if len(rep.Problems) == 0 {
		t.Fatal("应有 problem 说明为什么")
	}
}

func hasProblem(r *Report, kw string) bool {
	for _, p := range r.Problems {
		if strings.Contains(p, kw) {
			return true
		}
	}
	return false
}
