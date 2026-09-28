package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// 资产图方向/形状核对（2026-09-28 全面审计）。
//
// 官方 model-selection.md 的 per-shot 规则："Maintain the approved screen size / aspect ratio"。
// 平台对**成片**有画幅核对（qc 的 ExpectWidth/ExpectHeight），对**参考图产物**此前只在
// "复用场景图"那条路上核对过 —— **新生成的图没人看**。参考图与成片画幅不一致时 H3 只能
// 自行裁切/拉伸，构图与信息量错位而界面上毫无异常（本项目反复修的那类"报成功其实坏了"）。
func TestAssetShapeMismatch(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		iw, ih    int
		pw, ph    int
		wantBad   bool
		wantInWhy string
	}{
		{"竖版项目 + 竖版定妆照 = 对", "characters", 768, 1344, 768, 1344, false, ""},
		{"竖版项目 + 横版定妆照 = 错", "characters", 1344, 768, 768, 1344, true, "竖版"},
		{"横版项目 + 横版场景图 = 对", "scenes", 1344, 768, 1344, 768, false, ""},
		{"横版项目 + 竖版场景图 = 错", "scenes", 768, 1344, 1344, 768, true, "横版"},
		{"道具卡方形 = 对", "props", 1024, 1024, 768, 1344, false, ""},
		{"道具卡竖长 = 错", "props", 768, 1344, 768, 1344, true, "1:1"},
		{"小幅偏移(12% 内)不算错", "characters", 760, 1344, 768, 1344, false, ""},
		{"尺寸读不到时不报(不制造假告警)", "characters", 0, 0, 768, 1344, false, ""},
	}
	for _, c := range cases {
		bad, why := assetShapeMismatch(c.kind, c.iw, c.ih, c.pw, c.ph)
		if bad != c.wantBad {
			t.Errorf("%s：期望 bad=%v，实际 %v(理由 %q)", c.name, c.wantBad, bad, why)
			continue
		}
		if c.wantBad && !strings.Contains(why, c.wantInWhy) {
			t.Errorf("%s：理由里要说清 %q，实际 %q", c.name, c.wantInWhy, why)
		}
	}
}

// TestImagePixelSizeReadsRealFile 尺寸读取必须真能读出（整条核对都建立在它上面：
// 读不出来就当"没验过"，不当作通过 —— 这正是本项目反复修的那类"报成功其实没做"）。
func TestImagePixelSizeReadsRealFile(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("没有 ffmpeg:%v", err)
	}
	dir := t.TempDir()
	// 造一张横版真图（用 ffmpeg，避免手写 PNG 字节）
	p := filepath.Join(dir, "land.png")
	if _, err := f.Run(context.Background(), "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=gray:s=640x360", "-frames:v", "1", p); err != nil {
		t.Skipf("造图失败:%v", err)
	}
	w, h, err := imagePixelSize(p)
	if err != nil {
		t.Fatalf("应能读出尺寸:%v", err)
	}
	if w != 640 || h != 360 {
		t.Errorf("应读出 640×360，实际 %d×%d", w, h)
	}
	// 不存在的文件必须报错（不能返回 0×0 当"通过"）
	if _, _, err := imagePixelSize(filepath.Join(dir, "nope.png")); err == nil {
		t.Error("读不到文件时应报错")
	}
}
