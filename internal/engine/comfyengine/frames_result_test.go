package comfyengine

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// 本地引擎的"实际帧数"口径(乙6 的本地一半)。
//
// engine.Result 新增 ActualFrames / DurationAdjusted 之前,上层只有计划帧数可用 ——
// 而本地引擎**一定会**改写它:提交长度要对齐 17k+5 网格,接缝生效时还要扣掉被 Trim
// 裁掉的 22 帧 pinned 头。这两个差异只活在代码注释里,清单与界面无从得知。

// TestProducedFramesMatchesSubmittedGridAndTrim 实际产出帧数 = 提交长度(及其裁剪)。
//
// 期望值**写死**而不是照抄公式:照抄一遍公式等于没测(改错公式时两边一起错)。
// 4 个样例覆盖两条分支 × (落在网格上 / 不在网格上)。
func TestProducedFramesMatchesSubmittedGridAndTrim(t *testing.T) {
	cases := []struct {
		name       string
		jobLength  int
		seamActive bool
		want       int
		why        string
	}{
		{
			name:      "计划 96 帧、非接缝 → 网格量化到 107",
			jobLength: 96, seamActive: false, want: 107,
			why: "AlignLength(96)=107:提交长度必须落在 17k+5 上",
		},
		{
			name:      "计划 107 帧、非接缝 → 已在网格上,原样",
			jobLength: 107, seamActive: false, want: 107,
			why: "107 = 17×6+5",
		},
		{
			name:      "计划 96 帧、接缝 → 124−22 = 102",
			jobLength: 96, seamActive: true, want: 102,
			why: "接缝要多渲 22 帧 pinned 头,再被 Trim 裁掉;成片因此比普通渲染短 5 帧(网格决定,见 SeamRenderLength)",
		},
		{
			name:      "计划 120 帧、接缝 → 158−22 = 136",
			jobLength: 120, seamActive: true, want: 136,
			why: "同一条口径的另一档:接缝反而比普通渲染长 12 帧",
		},
	}
	for _, c := range cases {
		if got := producedFrames(c.jobLength, c.seamActive); got != c.want {
			t.Errorf("%s: producedFrames(%d, %v) = %d,期望 %d(%s)",
				c.name, c.jobLength, c.seamActive, got, c.want, c.why)
		}
	}
}

// TestProducedFramesStaysOnGrid 产出的帧数必须落回 17k+5(非接缝)或 17k(接缝)——
// 这是 H3 视频 VAE 的时间布局要求,漂了就意味着"提交的长度"与"解码出来的长度"不是一回事。
func TestProducedFramesStaysOnGrid(t *testing.T) {
	for n := 5; n <= 400; n++ {
		plain := producedFrames(n, false)
		if plain != engine.AlignLength(n) {
			t.Fatalf("非接缝产出 %d 帧,应为 AlignLength(%d)=%d", plain, n, engine.AlignLength(n))
		}
		if plain%17 != 5 && plain != 5 {
			t.Fatalf("非接缝产出 %d 帧不在 17k+5 网格上(源长度 %d)", plain, n)
		}
		seam := producedFrames(n, true)
		// 接缝:提交的是 AlignLength(n+22) = 17k+5,裁掉 22 → 17(k−1),即 17 的整数倍
		if seam <= 0 || seam%17 != 0 {
			t.Fatalf("接缝产出 %d 帧不是 17 的整数倍(源长度 %d):pinned 头被裁掉后应整除 17", seam, n)
		}
	}
}

// TestLocalRenderResultReportsActualFrames 本地引擎必须回填实际帧数与"是否被改写"。
//
// 这条是**运行期**断言(Render 本身要一台真的 ComfyUI 才跑得起来,所以把结果组装
// 抽成 localRenderResult 这个可测的面)。字段掉的后果:上层永远以为"计划就是实际",
// 乙6 那种"两条路产物时长不一致"再也对不出账。
func TestLocalRenderResultReportsActualFrames(t *testing.T) {
	cases := []struct {
		name       string
		jobLength  int
		seamActive bool
		wantFrames int
		wantAdjus  bool
	}{
		{"计划 96 帧被网格量化 → 算是被改写", 96, false, 107, true},
		{"计划 107 帧本来就在网格上 → 不算改写", 107, false, 107, false},
		{"接缝裁剪 → 实际 102,与计划 96 不同", 96, true, 102, true},
	}
	for _, c := range cases {
		res := localRenderResult("/tmp/shot_001.mp4", "comfy", c.jobLength, c.seamActive)
		if res == nil {
			t.Fatalf("%s: 不该返回 nil", c.name)
		}
		if res.ActualFrames != c.wantFrames {
			t.Errorf("%s: ActualFrames = %d,期望 %d", c.name, res.ActualFrames, c.wantFrames)
		}
		if res.DurationAdjusted != c.wantAdjus {
			t.Errorf("%s: DurationAdjusted = %v,期望 %v(计划 %d → 实际 %d)",
				c.name, res.DurationAdjusted, c.wantAdjus, c.jobLength, res.ActualFrames)
		}
		if res.VideoPath != "/tmp/shot_001.mp4" || res.Engine != "comfy" {
			t.Errorf("%s: 产物路径/引擎名被改动:%+v", c.name, res)
		}
	}
}
