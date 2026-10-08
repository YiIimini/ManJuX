package pipeline

import (
	"os"
	"strings"
	"testing"
)

// 画面文字检测的**开关与降级**策略(清单 §3.1a)。
//
// 为什么值得单独穷举这三行:它是"运行成本"与"不许假装验过"的交界处 ——
//   - 关掉它必须**彻底静默**(连报告条目都不写),否则"关掉"看起来就像"每镜都验过且干净";
//   - 没配视觉模型 / 机械质检已判不合格都必须**留痕**(那是"没验过",不是"验过且干净")。
//
// 这三种处置一旦被写错,不会报错、不会有日志,只有针对性断言能钉住。
func TestPlanTextDetect(t *testing.T) {
	cases := []struct {
		name       string
		enabled    bool
		haveClient bool
		mechOK     bool
		wantRun    bool
		wantSkip   string // "" = 连报告条目都不写
	}{
		{name: "开关关闭 → 不跑,且报告里不留痕", enabled: false, haveClient: true, mechOK: true, wantRun: false, wantSkip: ""},
		{name: "开关关闭时也不因缺模型留痕", enabled: false, haveClient: false, mechOK: true, wantRun: false, wantSkip: ""},
		{name: "开了但未配置视觉模型 → 不跑,如实写「未配置视觉模型」", enabled: true, haveClient: false, mechOK: true, wantRun: false, wantSkip: "未做画面文字检测:未配置视觉模型"},
		{name: "开了、有模型,但机械质检未通过 → 不跑(画面本就要重渲),留痕", enabled: true, haveClient: true, mechOK: false, wantRun: false, wantSkip: "未做画面文字检测:机械质检未通过(该镜本就要重渲)"},
		{name: "开了、有模型、机械合格 → 跑", enabled: true, haveClient: true, mechOK: true, wantRun: true, wantSkip: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planTextDetect(tc.enabled, tc.haveClient, tc.mechOK)
			if got.Run != tc.wantRun {
				t.Errorf("Run = %v,期望 %v", got.Run, tc.wantRun)
			}
			if got.Skipped != tc.wantSkip {
				t.Errorf("Skipped = %q,期望 %q", got.Skipped, tc.wantSkip)
			}
			// 不变量:凡是真要跑的,就不该带跳过原因(两者同时出现 = 报告自相矛盾)
			if got.Run && got.Skipped != "" {
				t.Errorf("Run=true 却带了跳过原因 %q", got.Skipped)
			}
			// 不变量:开了却没跑,必须能说出为什么(否则就是静默跳过)
			if tc.enabled && !got.Run && got.Skipped == "" {
				t.Error("功能开着却没跑,却不写原因 —— 报告里看不出到底看没看")
			}
		})
	}
}

// 接线闸门:runQC 必须真的把这条链接上,而不是只留一个能配的开关。
//
// 与 qc_verdict_test.go 里 TestAllModeQCFailureIsRemembered 同一手法(源码级钉住):
// runQC 需要真项目 + 真产物才能跑起来,而这里要钉的是**接线**本身 ——
// 参数被读到了、检测结果进了报告、集级告警进了阶段结论。任何一处被删掉,这条就红。
func TestQCTextDetectIsWired(t *testing.T) {
	src := qcStageSource(t)
	for _, want := range []string{
		"p.TextDetectOn()",            // 三态开关真的参与了决策(缺键 = 开)
		"planTextDetect(textDetectOn", // 开关、视觉模型、机械质检三者共同决定处置
		"qc.TextDetectSkipped(",       // 没验过要留痕
		"qc.TextDetectFailed(",        // 失败如实记
		"qc.DetectOnScreenText(",      // 真的去抽帧 + 调视觉模型
		"qc.TextWarnText(",            // 命中 → 逐镜 warn
		"qc.TextHitAlert(",            // 集级命中率判据
		"qcVerdictOf(len(plan.Shots), checked, failed, notChecked, vlmFlagged, speechFlagged, voiceFlagged, textAlert)", // 告警并进结论
	} {
		if !strings.Contains(src, want) {
			t.Errorf("质检阶段缺少接线 %q —— 开关/告警会变成「接了但没生效」", want)
		}
	}
}

// qcStageSource 读质检阶段源码(源码级闸门用)。
func qcStageSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("stages_qc_assemble.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
