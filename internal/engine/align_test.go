package engine

import "testing"

// 帧数与画布换算的表驱动断言(审计 P1-3)。
//
// 这两个函数是**所有引擎共用**的换算点(本地 ComfyUI 与云端 API 都按它们决定
// 提交多少帧、多宽多高),而此前一个断言都没有 —— 一旦算错,症状是"渲染出来的
// 时长/画幅与预期差一点":不报错、不崩溃,成片短了零点几秒或者宽了 20 像素,
// 只有人眼盯着成片才会发现。所以这里把边界逐个钉住。

// TestAlignLengthGrid H3 的帧数网格是 17k+5(5,22,39,...)。
func TestAlignLengthGrid(t *testing.T) {
	cases := []struct {
		in, want int
		why      string
	}{
		{0, 5, "非正数抬到网格最小值"},
		{-100, 5, "负数同样抬到最小值(Go 负数取模不归一就会算出离谱的值)"},
		{1, 5, "网格下界"},
		{5, 5, "已在网格上不动"},
		{6, 22, "略高于 5 格 → 抬到下一格"},
		{21, 22, "差一格到 22"},
		{22, 22, "已在网格上"},
		{23, 39, "再抬一格"},
		{124, 124, "5 秒@约24fps 的常规值(17*7+5)"},
		{125, 141, "超过就抬到下一格"},
	}
	for _, c := range cases {
		got := AlignLength(c.in)
		if got != c.want {
			t.Errorf("AlignLength(%d) = %d,期望 %d(%s)", c.in, got, c.want, c.why)
		}
		if (got-5)%17 != 0 {
			t.Errorf("AlignLength(%d) = %d 不落在 17k+5 上", c.in, got)
		}
		if got < c.in {
			t.Errorf("AlignLength(%d) = %d 不该变小(变短会切掉画面)", c.in, got)
		}
	}
}

// TestSnapCanvas32 画幅必须是 32 的倍数,且短边不低于 32。
func TestSnapCanvas32(t *testing.T) {
	cases := []struct {
		w, h, wantW, wantH int
	}{
		{768, 1344, 768, 1344}, // 常用竖幅,本身就是 32 的倍数
		{1344, 768, 1344, 768},
		{32, 32, 32, 32}, // 最小值
		{1, 1, 32, 32},   // 小于 32 → 抬到 32(不能是 0,否则工作流非法)
		{0, -5, 32, 32},  // 非法输入同样抬到 32,不返回 0/负数
		{800, 1280, 800, 1280},
		{769, 1345, 768, 1344}, // 向下取整到 32 的倍数
	}
	for _, c := range cases {
		gw, gh := SnapCanvas(c.w, c.h)
		if gw != c.wantW || gh != c.wantH {
			t.Errorf("SnapCanvas(%d,%d) = (%d,%d),期望 (%d,%d)", c.w, c.h, gw, gh, c.wantW, c.wantH)
		}
		if gw%32 != 0 || gh%32 != 0 {
			t.Errorf("SnapCanvas(%d,%d) = (%d,%d) 不是 32 的倍数", c.w, c.h, gw, gh)
		}
		if gw < 32 || gh < 32 {
			t.Errorf("SnapCanvas(%d,%d) = (%d,%d) 低于最小 32", c.w, c.h, gw, gh)
		}
	}
}

// TestAlignLengthAndSnapCanvasAreSingleSource 时长的两个换算点必须同源:
// 镜头时长 → 帧数(提交给引擎)与项目的 24fps 约定必须一致,否则"判据认为 4 秒、
// 实际渲 6 秒"这类错位会长期隐形(同 P0-8 的教训)。
func TestAlignLengthAndSnapCanvasAreSingleSource(t *testing.T) {
	const fps = 24
	for dur := 4; dur <= 10; dur++ {
		frames := AlignLength(dur * fps)
		got := float64(frames) / fps
		// 对齐只会往后抬,不会缩短;抬升幅度必须小于一格(17 帧 ≈ 0.7 秒)
		if got < float64(dur) || got >= float64(dur)+float64(17)/fps {
			t.Errorf("时长 %ds → %d 帧(%.2fs):对齐后应不短于原时长且不超过一格", dur, frames, got)
		}
	}
}
