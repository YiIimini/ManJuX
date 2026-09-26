package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 帧数记账闸门(乙6):引擎回传的**实际帧数**必须记账,不能只留计划值。
//
// 两条路各有各的改写:本地提交长度要对齐 17k+5 网格(计划 96 → 渲 107 帧)、接缝再扣
// 22 帧;云端只吃整数秒且区间更窄(4 秒镜被抬成 5 秒:计划 96 → 120 帧)。
// 旧实现只把**计划值**写进清单,"你计划 4 秒、云端按 5 秒渲了"这件事在日志与界面上
// 一个字都没有 —— 成片比计划长、台词落点全偏,却没有任何痕迹可查。
func TestShotFrameRecord(t *testing.T) {
	cases := []struct {
		name       string
		planned    int
		res        *engine.Result
		wantLength int
		wantNote   []string // 期望出现在记录里的片段(空 = 不该有记录)
	}{
		{
			name: "计划与实际一致 → 不刷无意义的日志", planned: 120,
			res:        &engine.Result{ActualFrames: 120, DurationAdjusted: false},
			wantLength: 120,
		},
		{
			name: "云端把 4 秒镜抬成 5 秒 → 如实记「计划 96 / 实际 120」", planned: 96,
			res:        &engine.Result{ActualFrames: 120, DurationAdjusted: true},
			wantLength: 96, // 清单长度仍是"本次按多少帧渲的",见 shotFrameRecord 的口径说明
			wantNote:   []string{"计划 96 帧", "实际 120 帧", "4.00s", "5.00s", "以实际值为准"},
		},
		{
			name: "本地对齐网格(96 → 107)同样要记", planned: 96,
			res:        &engine.Result{ActualFrames: 107, DurationAdjusted: true},
			wantLength: 96,
			wantNote:   []string{"计划 96 帧", "实际 107 帧"},
		},
		{
			name: "引擎没给实际帧数 → 不编数字,清单照旧记计划值", planned: 96,
			res: &engine.Result{}, wantLength: 96,
		},
		{
			name: "没有结果对象(异常路径)→ 不崩、不留假账", planned: 96,
			res: nil, wantLength: 96,
		},
		{
			name: "调用方没给计划帧数 → 实际值是唯一的真话", planned: 0,
			res:        &engine.Result{ActualFrames: 120, DurationAdjusted: true},
			wantLength: 120,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			length, note := shotFrameRecord(tc.planned, tc.res)
			if length != tc.wantLength {
				t.Errorf("清单长度 = %d,期望 %d", length, tc.wantLength)
			}
			if len(tc.wantNote) == 0 {
				if note != "" {
					t.Errorf("没有差异时不该产生记录,实际 %q", note)
				}
				return
			}
			if note == "" {
				t.Fatal("引擎改写了时长却没有记录 —— 用户看不出成片为什么比计划长")
			}
			for _, want := range tc.wantNote {
				if !strings.Contains(note, want) {
					t.Errorf("记录里应包含 %q,实际 %q", want, note)
				}
			}
		})
	}
}

// TestShotFrameRecordKeepsResumeJudgmentIntact 记账方式必须与**断点续跑判据**相容:
// 清单长度记计划值,所以"计划没改 → 照旧复用、计划改了 → 重渲"两条都还成立。
//
// 反过来,若把实际帧数写进 Length,云端那条 4 秒镜(计划 96 / 实际 120)每次续跑都会
// 判"帧数对不上"→ 无限重渲(这正是本函数不那样做的原因)。
func TestShotFrameRecordKeepsResumeJudgmentIntact(t *testing.T) {
	const fp = "fp-abc"
	planned, res := 96, &engine.Result{ActualFrames: 120, DurationAdjusted: true}
	length, note := shotFrameRecord(planned, res)
	if note == "" {
		t.Fatal("云端改写时长必须留下记录")
	}
	m := project.ClipManifest{
		ShotID: 3, Status: project.ShotDone, File: "clips/shot_003.mp4", InputFP: fp, Length: length,
	}
	if !shotArtifactValid(m, fp, planned, "", "") {
		t.Error("同一计划重跑必须判「产物有效」:否则云端每续跑一次就全量重渲")
	}
	if shotArtifactValid(m, fp, 144, "", "") {
		t.Error("时长 4s→6s 必须判失效重渲(P0-8 那条闸门不能因为记账改动而松掉)")
	}
}
