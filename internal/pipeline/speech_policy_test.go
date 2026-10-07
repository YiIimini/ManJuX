package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// 语音核对（ASR）的处置穷举。
//
// 这一条与画面文字检测是**同一类洞**：功能开着却没跑、跑没跑在报告里看不出来、
// 或者"没验过"被当成"验过且干净"。三件事都不会报错，只有针对性断言能钉住。
func TestPlanSpeechCheck(t *testing.T) {
	cases := []struct {
		name                string
		enabled, haveTool   bool
		mechOK              bool
		wantRun             bool
		wantSkippedContains string
	}{
		{name: "开 + 有 ASR + 机械质检通过 → 真跑", enabled: true, haveTool: true, mechOK: true, wantRun: true},
		{
			// 关掉它连条目都不写：报告里"没这项"与"每镜都验过且干净"必须分得开。
			name: "开关关闭 → 不跑且不留痕", enabled: false, haveTool: true, mechOK: true,
			wantRun: false, wantSkippedContains: "",
		},
		{
			// 没装 ASR 的机器：不跑、不判不合格，但报告里必须写着"没验过"。
			name: "有 ASR 但机械质检不合格 → 不跑(它本就要重渲)", enabled: true, haveTool: true, mechOK: false,
			wantRun: false, wantSkippedContains: "机械质检未通过",
		},
		{
			name: "本机没有 ASR → 不跑且如实说明", enabled: true, haveTool: false, mechOK: true,
			wantRun: false, wantSkippedContains: "本机没有 ASR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planSpeechCheck(tc.enabled, tc.haveTool, tc.mechOK)
			if got.Run != tc.wantRun {
				t.Fatalf("Run = %v，期望 %v", got.Run, tc.wantRun)
			}
			if got.Run && got.Skipped != "" {
				t.Errorf("Run=true 却带了跳过原因 %q", got.Skipped)
			}
			// 不变量：功能开着却没跑，必须能说出为什么。
			if tc.enabled && !got.Run && got.Skipped == "" {
				t.Error("功能开着却没跑，却不写原因 —— 报告里看不出到底验没验")
			}
			if tc.wantSkippedContains != "" && !strings.Contains(got.Skipped, tc.wantSkippedContains) {
				t.Errorf("跳过原因应含 %q，实际 %q", tc.wantSkippedContains, got.Skipped)
			}
		})
	}
}

// 通过行上的语音标记：把"验过且干净"与"根本没验"在日志里分开。
func TestSpeechLabel(t *testing.T) {
	if got := speechLabel(nil); got != "" {
		t.Errorf("没开这项功能时不该有标记，实际 %q", got)
	}
	if got := speechLabel(qc.SpeechSkipped("未做语音核对:本机没有 ASR")); got != ",语音未验" {
		t.Errorf("跳过必须显式说「未验」，实际 %q", got)
	}
	if got := speechLabel(qc.SpeechFailed(errAny{})); !strings.Contains(got, "未验") {
		t.Errorf("核对失败也不能看起来像验过，实际 %q", got)
	}
	ok := &qc.SpeechReport{Checked: true, Segments: 2}
	if got := speechLabel(ok); got != ",语音✔" {
		t.Errorf("验过且干净应报 ✔，实际 %q", got)
	}
	bad := &qc.SpeechReport{Checked: true, Findings: []asr.Finding{{Kind: "repeat"}, {Kind: "extra"}}}
	if got := speechLabel(bad); got != ",语音✗2 处" {
		t.Errorf("有问题应报处数，实际 %q", got)
	}
}

// errAny 一个只为构造「失败」的最小 error（不引 errors 包）。
type errAny struct{}

func (errAny) Error() string { return "ASR 调用失败" }

// 接线闸门：runQC 必须真的把语音核对接上，而不是只留一个能配的开关。
//
// runQC 需要真项目 + 真产物 + 真 ASR 才能跑起来，而这里要钉的是**接线**本身：
// 开关被读到了、处置由纯函数决定、没验过要留痕、结果进了报告、告警并进了阶段结论。
// 任何一处被删掉，这条就红 —— 这类"接了但没生效"的缺口不会报错。
func TestQCSpeechIsWired(t *testing.T) {
	src := qcStageSource(t)
	for _, want := range []string{
		"p.ASROn()",                // 三态开关真的参与了决策（缺键 = 开）
		"asr.Locate()",             // 本机有没有 ASR 真的去查了
		"planSpeechCheck(speechOn", // 开关/工具/机械质检三者共同决定处置
		"qc.SpeechSkipped(",        // 没验过要留痕
		"r.Speech = qc.SpeechSkipped(pl.Skipped)", // …而且要落到报告的那一位上
		"qc.CheckSpeech(",                         // 真的去转写 + 对账
		"r.Speech = rep",                          // 核对结果真的进了报告（否则报告里永远看不到）
		"speechFlagged++",                         // 发现问题的镜数真的数了（否则阶段结论不会提）
		"project.PlanSpokenTexts(",                // 拿**计划台词**当权威文本对账
		"qc.SpeechWarnText(",                      // 命中 → 逐镜 warn
		"qc.SpeechBudget(",                        // 逐镜限时（ASR 卡住不该拖死质检）
		"speechLabel(r.Speech)",                   // "验过且干净"与"没验过"在日志里分得开
		// 告警必须并进阶段结论（否则用户只看得到一句"质检通过"）
		"qcVerdictOf(len(plan.Shots), checked, failed, notChecked, vlmFlagged, speechFlagged, textAlert)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("质检阶段缺少接线 %q —— 语音核对会变成「接了但没生效」", want)
		}
	}
	if !strings.Contains(src, `"warn"`) {
		t.Error("语音核对的相关播报必须是 warn 级 —— 它不该让批量产线整集停住")
	}
}
