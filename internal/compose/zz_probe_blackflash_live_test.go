package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// TestZZProbeBlackFlashLive 真机取证：拿**真实片段**（env MANJUX_PROBE_CLIP）跑一遍黑闪切除，
// 打印处理前后的黑段、时长与"最暗那一帧"的亮度。
//
// 【为什么走探针、并且先拷贝】真机那一集的片段是用户数据，**单测绝不动它** —— 这里先拷到 t.TempDir()
// 再动副本。不设 env 就跳过（不是"通过"）。
//
// 用法：
//
//	$env:MANJUX_PROBE_CLIP="D:\...\clips\shot_001.mp4"; go test ./internal/compose -run TestZZProbeBlackFlashLive -v
func TestZZProbeBlackFlashLive(t *testing.T) {
	src := os.Getenv("MANJUX_PROBE_CLIP")
	if src == "" {
		t.Skip("未设 MANJUX_PROBE_CLIP（真机片段路径），跳过")
	}
	f := ffexe(t)
	ctx := context.Background()
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
	// 连同"已处理过"标记一起拷（真机形态：标记在、黑还在）
	if mb, err := os.ReadFile(src + ".noblack"); err == nil {
		_ = os.WriteFile(dst+".noblack", mb, 0o644)
	}

	info0, err := f.Probe(dst)
	if err != nil {
		t.Fatal(err)
	}
	runs0, err := DetectBlackRuns(ctx, f, dst)
	if err != nil {
		t.Fatal(err)
	}
	min0, at0 := minLuma(t, f, dst)
	t.Logf("改前：时长=%.6fs 有音轨=%v 黑段=%v 最暗帧=%.2f@f%d（源 %d 字节）",
		info0.Duration, info0.HasAudio, runs0, min0, at0, len(b))

	removed, n, longRuns, err := ForceStripBlackFlash(ctx, f, dst, 0)
	if err != nil {
		t.Fatal(err)
	}
	info1, err := f.Probe(dst)
	if err != nil {
		t.Fatal(err)
	}
	runs1, err := DetectBlackRuns(ctx, f, dst)
	if err != nil {
		t.Fatal(err)
	}
	min1, at1 := minLuma(t, f, dst)
	t.Logf("改后：时长=%.6fs 剪掉 %d 段/%.4fs 长黑段=%v 黑段=%v 最暗帧=%.2f@f%d",
		info1.Duration, n, removed, longRuns, runs1, min1, at1)

	if len(runs1) != 0 {
		t.Errorf("真机这处黑闪应当被剪干净，改后仍有黑段：%v", runs1)
	}
	if n == 0 {
		t.Errorf("真机这处黑闪没有被剪（n=0）—— 修复没生效？")
	}
}

// minLuma 逐帧量亮度（ffmpeg signalstats 的 YAVG），返回最小值与它的帧号。
//
// 为什么要绕一下写文件：`ff.Run` 只收 stderr（`cmd.Stdout = nil`），而 `metadata=print:file=-` 走 stdout
// ⇒ 只能让它写到临时文件再读回来。Windows 路径里盘符那个冒号必须转义（滤镜参数用 `:` 分隔选项）。
func minLuma(t *testing.T, f *ff.FF, path string) (float64, int) {
	t.Helper()
	// 用 exec 直接收 stdout：`ff.Run` 只收 stderr（`cmd.Stdout = nil`），而 `metadata=print:file=-` 走 stdout。
	out, err := exec.Command(f.Ffmpeg, "-v", "error", "-i", path, "-an",
		"-vf", "signalstats,metadata=print:file=-:key=lavfi.signalstats.YAVG",
		"-f", "null", "-").Output()
	if err != nil {
		t.Skipf("量逐帧亮度失败（%v）", err)
	}
	re := regexp.MustCompile(`YAVG=([0-9.]+)`)
	best, at, i := 1e9, -1, 0
	for _, line := range regexp.MustCompile("\n").Split(string(out), -1) {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v, perr := strconv.ParseFloat(m[1], 64)
		if perr == nil && v < best {
			best, at = v, i
		}
		i++
	}
	if at < 0 {
		t.Skip("没取到逐帧亮度")
	}
	return best, at
}
