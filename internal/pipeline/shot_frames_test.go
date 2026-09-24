package pipeline

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 断点续跑判据的穷举测试(审计 P0-8)。
//
// 守的是"改了镜头时长却跳过重渲、界面还报成功":4 秒改 6 秒(为了装下被截断的台词)
// 的镜头必须重新渲染,而**没改**的镜头必须照旧跳过(否则每次续跑都全量重渲)。
func TestDurationChangeInvalidatesRenderedShot(t *testing.T) {
	const fp = "fp-abc"
	done := func(length int) project.ClipManifest {
		return project.ClipManifest{
			ShotID: 3, Status: project.ShotDone, File: "clips/shot_003.mp4",
			InputFP: fp, Length: length,
		}
	}

	cases := []struct {
		name       string
		m          project.ClipManifest
		inputFP    string
		wantFrames int
		valid      bool
		why        string
	}{
		{"指纹与帧数都一致 → 复用", done(96), fp, 96, true, "什么都没改,不该重渲"},
		{"时长 4s→6s → 必须重渲", done(96), fp, 144, false,
			"旧片只有 4 秒,装不下新台词(用户报的正是这条)"},
		{"时长 6s→4s → 同样重渲", done(144), fp, 96, false, "变短也要重生成,不能留长片"},
		{"老清单没记帧数 → 不推翻指纹", done(0), fp, 144, true,
			"Length=0 是本字段引入前的清单:否则升级一版就让存量项目全部重渲"},
		{"提示词/资产变了 → 重渲", done(96), fp + "-changed", 96, false, "指纹是主判据"},
		{"未完成的镜头 → 不算有效", project.ClipManifest{Status: project.ShotRendering, File: "x.mp4", InputFP: fp, Length: 96}, fp, 96, false,
			"上次渲染中断的产物不能当成品"},
		{"没有产物文件 → 不算有效", project.ClipManifest{Status: project.ShotDone, InputFP: fp, Length: 96}, fp, 96, false,
			"清单有记录但文件缺失(被清理过)时必须重渲"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shotArtifactValid(tc.m, tc.inputFP, tc.wantFrames); got != tc.valid {
				t.Fatalf("shotArtifactValid = %v,期望 %v(%s)", got, tc.valid, tc.why)
			}
		})
	}
}

// TestShotFramesIsSingleSource 帧数与时长只有一个换算点:提交给引擎的 Length 与
// 复用判据里的 wantFrames 必须是同一个值(否则"判据认为 4 秒、实际渲 6 秒"这类
// 错位会长期隐形)。
func TestShotFramesIsSingleSource(t *testing.T) {
	cases := map[int]int{4: 96, 6: 144, 7: 168, 15: 360}
	for dur, want := range cases {
		if got := shotFrames(project.Shot{Duration: dur}); got != want {
			t.Errorf("时长 %ds 应为 %d 帧(24fps),实际 %d", dur, want, got)
		}
	}
}
