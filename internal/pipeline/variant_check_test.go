package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// variant_check_test.go 跨期造型档的两向复核（用户 2026-09-28：
// 「主角不同时期的人物角色会存在差异，但是面板相似度不能走样」）。
//
// 判据一分为二，两半都要测：
//   - 该变的没变（SSIM 高）⇒ 报；
//   - 不该变的变了（VLM 判成不是同一个人）⇒ 报。
func TestParseSSIM(t *testing.T) {
	// ffmpeg 真实输出长这样（取 All 那一段）
	out := "[Parsed_ssim_1 @ 000001] SSIM Y:0.912 U:0.930 V:0.925 All:0.918 (15.482)\n"
	got, err := parseSSIM(out)
	if err != nil {
		t.Fatalf("解析失败:%v", err)
	}
	if got < 0.917 || got > 0.919 {
		t.Errorf("应解析出 0.918,实际 %v", got)
	}
	if _, err := parseSSIM("nothing here"); err == nil {
		t.Error("没有分数时要报错，不能返回 0 假装比过了")
	}
}

// TestImageSSIMRealFFmpeg 真跑 ffmpeg 比两张图：同一张图 ≈1，两张不同的图明显低。
func TestImageSSIMRealFFmpeg(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机没有 ffmpeg，跳过:%v", err)
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	c := filepath.Join(dir, "c.png")
	mk := func(args ...string) {
		cmd := exec.Command("ffmpeg", append([]string{"-v", "error", "-y"}, args...)...)
		if err := cmd.Run(); err != nil {
			t.Skipf("造图失败(跳过):%v", err)
		}
	}
	mk("-f", "lavfi", "-i", "color=c=gray:s=320x480", "-frames:v", "1", a)
	if err := os.WriteFile(b, mustRead(t, a), 0o644); err != nil {
		t.Fatal(err)
	}
	mk("-f", "lavfi", "-i", "testsrc=s=320x480", "-frames:v", "1", c)

	co := &Coordinator{ff: f}
	same, err := co.imageSSIM(context.Background(), a, b)
	if err != nil {
		t.Fatalf("比同一张图失败:%v", err)
	}
	if same < 0.99 {
		t.Errorf("同一张图 SSIM 应≈1,实际 %v", same)
	}
	diff, err := co.imageSSIM(context.Background(), a, c)
	if err != nil {
		t.Fatalf("比不同图失败:%v", err)
	}
	if diff >= variantSameSSIM {
		t.Errorf("两张明显不同的图 SSIM 应低于阈值 %.2f,实际 %v —— 否则「该变的没变」这条门永远不响", variantSameSSIM, diff)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
