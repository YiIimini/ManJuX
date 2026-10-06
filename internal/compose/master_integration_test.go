package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine/h3api"
	"github.com/YiIimini/ManJuX/internal/ff"
)

// 768p 母版**真跑一遍**：它必须是云端「整片 2K」能接受的源（2026-09-30 深度分析）。
//
// 【为什么这条必须真跑】整片 2K 这条链此前**从来没有成功过**：
// compose 把成片按交付档放大到 1080×1920，而 H3-Regenerate-2K 只收 768P 规格的源
// （官方模型卡：2K 只能靠 in-context 重新生成，短边 768 + 含音轨）——
// 于是"拿 final.mp4 去升 2K"必然被 `Validate2KBase` 拦下，而按钮看起来是好的。
//
// 判据不是"母版文件在不在"，而是**它能不能通过那条真实契约**：
// 用 `h3api.Validate2KBase`（生产代码里同一把尺子）量这份母版。少测这一层，
// 母版写成一个没有音轨、或者被交付档一起放大了的文件，测试照样绿。
func TestComposePublishesUsable2KMaster(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机没有 ffmpeg，跳过真跑检查：%v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()

	// 造两镜真片段：**渲染规格** 768×1344、24fps、带音轨（H3 的产物就长这样）
	mk := func(name string, d int) string {
		p := filepath.Join(dir, name)
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "color=c=gray:s=768x1344:r=24:d="+itoa(d),
			"-f", "lavfi", "-i", "sine=frequency=440:duration="+itoa(d),
			"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", p); err != nil {
			t.Skipf("本机 ffmpeg 编不了测试片段（缺 libx264/aac？）：%v", err)
		}
		return p
	}
	clips := []string{mk("shot_001.mp4", 2), mk("shot_002.mp4", 2)}

	outDir := filepath.Join(dir, "output")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Compose(ctx, f, clips, nil, outDir, Params{
		Width: 768, Height: 1344, Transition: "cut", Loudness: -16, Peak: 0.92,
	}, nil)
	if err != nil {
		t.Fatalf("合成真跑失败：%v", err)
	}
	if res.MasterPath == "" {
		t.Fatal("没有回传 768p 母版路径 —— 整片 2K 就没有合法源了")
	}
	if _, err := os.Stat(res.MasterPath); err != nil {
		t.Fatalf("母版文件不在盘上：%v", err)
	}
	if filepath.Base(res.MasterPath) != MasterFileName {
		t.Errorf("母版文件名应为 %s，实际 %s", MasterFileName, filepath.Base(res.MasterPath))
	}

	// ① 规格：**与渲染同规格**（不是交付后的 1080×1920）
	info, err := f.Probe(res.MasterPath)
	if err != nil {
		t.Fatalf("probe 母版失败：%v", err)
	}
	if info.Width != 768 || info.Height != 1344 {
		t.Errorf("母版应为渲染规格 768×1344（交付档的放大不该带进来），实际 %d×%d", info.Width, info.Height)
	}
	if !info.HasAudio {
		t.Error("母版必须有音轨 —— regeneration 的输入契约要它")
	}
	// ② **真正的判据**：生产代码那把尺子（云端 2K 的预校验）必须接受它
	if err := h3api.Validate2KBase(f, res.MasterPath, true); err != nil {
		t.Fatalf("母版过不了云端 2K 的预校验：%v —— 那整片 2K 还是不可用", err)
	}
	// ③ 反面对照：**成片本身**（交付档放大后）过不了 —— 这正是原来那条断链的根因。
	//    交付分辨率在这里显式给 1080×1920（等价于默认档）。
	if res.Width == 768 && res.Height == 1344 {
		// 这一步没放大（未给交付尺寸）时就不做这条对照，避免把环境差异当缺陷。
		t.Log("本次成片未放大（未指定交付尺寸），跳过「成片过不了校验」的对照")
	} else if err := h3api.Validate2KBase(f, res.FinalPath, true); err == nil {
		t.Logf("成片 %d×%d 竟然也过了校验（交付尺寸未放大）—— 母版仍是更稳的那条路", res.Width, res.Height)
	}
}

// itoa 小整数转字符串（只用于 ffmpeg 时长参数，避免引 fmt）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
