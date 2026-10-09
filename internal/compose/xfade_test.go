package compose

import (
	"math"
	"testing"
)

// xfade 偏移必须**按整帧**累计：每个过渡窗口的末端都不许越过前一镜链的末帧。
//
// 夹具是**真机那一集的前 4 镜**（时长与接缝叠化时长都取自盘上实测）：
// 8.0 / 7.3 / 8.5 / 9.3 秒，前两个接缝是同场硬切（1 帧 = 0.0417s）、第三个是换场叠化 0.5s。
//
// 【为什么这条判据要命（2026-10-09 真机 P0）】越界一次，ffmpeg 的 xfade 就**静默丢弃后面
// 所有输入**（退出码还是 0）—— 真机成片因此只有前 3 镜的画面（23.708 秒 /569 帧），
// 而音轨 122.499 秒。这里把那次的数字直接钉进判据。
func TestXfadeOffsetsStayInsidePreviousChain(t *testing.T) {
	durations := []float64{8.0, 7.3, 8.5, 9.3}
	fades := []float64{1.0 / videoFPS, 1.0 / videoFPS, 0.5}
	offs := xfadeOffsets(durations, fades)
	if len(offs) != len(durations)-1 {
		t.Fatalf("该给 %d 个接缝的 offset，实际 %v", len(durations)-1, offs)
	}
	// 链长（按整帧算，与 xfade 的实际输出口径一致）
	chain := math.Round(durations[0]*videoFPS) / videoFPS
	for i, off := range offs {
		if end := off + fades[i]; end > chain+1e-9 {
			t.Errorf("第 %d 个接缝的过渡窗口末端 %.6f 越过了前一链的末帧 %.6f —— "+
				"xfade 会从这里开始**静默丢弃后面所有输入**（真机就是死在第 4 镜上）",
				i+1, end, chain)
		}
		chain = math.Round((off+durations[i+1])*videoFPS) / videoFPS
	}
	// 前两个接缝是 1 帧硬切 ⇒ offset 必须正好落在"前一链末帧减 1 帧"
	if math.Abs(offs[0]-float64(int64(math.Round(8*videoFPS))-1)/videoFPS) > 1e-9 {
		t.Errorf("第 1 个接缝（1 帧硬切）的 offset 该是 %v，实际 %v",
			float64(int64(math.Round(8*videoFPS))-1)/videoFPS, offs[0])
	}
}

// **反向对照**：老的"按秒连续累加"口径在真机这组数字上确实越界（这正是那次事故的机制）。
// 这条不是给新代码用的，是把事故钉在判据里 —— 免得以后又有人"简化"成连续量。
func TestOldSecondBasedOffsetsWouldOvershoot(t *testing.T) {
	durations := []float64{8.0, 7.3, 8.5}
	fades := []float64{1.0 / videoFPS, 1.0 / videoFPS}
	acc := durations[0]
	var old []float64
	for i := 1; i < len(durations); i++ {
		acc -= fades[i-1]
		old = append(old, acc)
		acc += durations[i]
	}
	// 第 2 个接缝：老口径的窗口末端（按秒精确）比真实整帧链长**多出一点点**
	chainFrames := math.Round((old[0]+durations[1])*videoFPS) / videoFPS
	if old[1]+fades[1] <= chainFrames {
		t.Errorf("这一组数字本该复现老口径越界（老 %.6f vs 链 %.6f）—— 夹具要照真机重抄",
			old[1]+fades[1], chainFrames)
	}
}

// 帧对齐之后，成片总长 = Σ时长 − Σ(整帧叠化)：真机 4 镜该得 32.5 秒上下。
func TestXfadeChainLengthOnRealFourShots(t *testing.T) {
	durations := []float64{8.0, 7.3, 8.5, 9.3}
	fades := []float64{1.0 / videoFPS, 1.0 / videoFPS, 0.5}
	offs := xfadeOffsets(durations, fades)
	last := offs[len(offs)-1]
	got := last + durations[len(durations)-1]
	if math.Abs(got-32.5) > 0.05 {
		t.Errorf("4 镜链的标称总长该是 32.5 秒上下，实际 %.3f（修复前这里只有 15.25 秒）", got)
	}
}

// 单镜/空输入不炸（硬切路与占位镜都会走到这里）。
func TestXfadeOffsetsEdgeCases(t *testing.T) {
	if got := xfadeOffsets(nil, nil); got != nil {
		t.Errorf("空输入该返回 nil，实际 %v", got)
	}
	if got := xfadeOffsets([]float64{8}, nil); got != nil {
		t.Errorf("单镜没有接缝，该返回 nil，实际 %v", got)
	}
	// 前一镜只有 1 帧时（占位镜），叠化时长要被夹到链长以内，不许出现负 offset
	got := xfadeOffsets([]float64{1.0 / videoFPS, 5}, []float64{0.5})
	if len(got) != 1 || got[0] < 0 {
		t.Fatalf("极短镜不许算出负 offset，实际 %v", got)
	}
}
