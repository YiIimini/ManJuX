package qc

import (
	"strings"
	"testing"
)

// darkscene_test.go **暗调场景**不该被判"疑似黑屏"（2026-10-07 真机实测）。
//
// 现场：那一集 13 镜里 **5 镜**被判不合格（最暗采样帧 0.67–0.82 ≥ 阈值 0.60）⇒ 整集"质检未通过"
// ⇒ **批量产线暂停在第 1/88 集**。而那 5 镜全在**计划里写明是暗调**的场景里
// （维护舱"右侧一列先亮，其余沉在黑暗里"、舱门缝"缝外漆黑无光"），同场景"三排灯全亮"那一镜
// 只判到 warn（0.58）—— 判据测到的是创作意图，不是崩坏。
//
// 判据两条，逐条钉死：
//
//	① 计划声明暗调（Params.DarkScene）⇒ 阈值整组上抬到 0.90/0.70，且**绝不放宽用户自己配的值**；
//	② 真崩坏（整镜全黑 / 后半段全黑，实测贴近 1.0）**仍然要抓住** —— 放宽不等于放水。
func TestDarkVerdictForDarkSceneAndRealBreakage(t *testing.T) {
	// 真机那两个场景的实测值（qc_report.json 里抄出来的）
	darkScene := Params{DarkRatioFail: 0.60, DarkRatioWarn: 0.45, DarkScene: true}
	normal := Params{DarkRatioFail: 0.60, DarkRatioWarn: 0.45}

	// ① 真机被判"不合格"的那几个值：普通镜要报、暗调镜只该告警
	for _, worst := range []float64{0.67, 0.72, 0.73, 0.77, 0.82} {
		if prob, _ := darkVerdict(worst, normal); prob == "" {
			t.Errorf("普通镜 %.2f 该判不合格（阈值 0.60），现在放过了", worst)
		}
		prob, warn := darkVerdict(worst, darkScene)
		if prob != "" {
			t.Errorf("暗调场景 %.2f 又被判不合格了（放宽后阈值 0.90）—— 那 5 镜就是这么把整集卡住的：%s", worst, prob)
		}
		if warn == "" || !strings.Contains(warn, "暗调") {
			t.Errorf("暗调场景 %.2f 该给一条**说清原因**的告警，实际 %q", worst, warn)
		}
	}
	// 同场景"三排灯全亮"那一镜（0.58）：**不判不合格**，但要留一条记账（说明是按暗调口径放行的）
	if prob, warn := darkVerdict(0.58, darkScene); prob != "" || warn == "" {
		t.Errorf("0.58 在暗调场景下不该判不合格、但该留一条记账，实际 problem=%q warn=%q", prob, warn)
	}
	// ② **真崩坏仍要抓住**：整镜全黑 / 后半段全黑（实测贴近 1.0）
	for _, worst := range []float64{0.90, 0.95, 1.0} {
		prob, _ := darkVerdict(worst, darkScene)
		if prob == "" {
			t.Errorf("%.2f 已经是「画面几乎全黑」，暗调场景也要判不合格 —— 放宽不等于放水", worst)
		} else if !strings.Contains(prob, "计划声明的暗调场景") {
			t.Errorf("暗调场景下的不合格文案要说清阈值为什么放宽过，实际：%s", prob)
		}
	}
	// 普通镜的 0.90 也照旧不合格
	if prob, _ := darkVerdict(0.90, normal); prob == "" {
		t.Error("普通镜 0.90 该判不合格")
	}
	// ③ 暗调场景的不合格线取"两者中较宽"：用户配了 0.95（比 0.90 宽）⇒ 用用户的 0.95（不是 0.90）
	strict := Params{DarkRatioFail: 0.95, DarkRatioWarn: 0.45, DarkScene: true}
	if prob, _ := darkVerdict(0.96, strict); prob == "" || !strings.Contains(prob, "阈值 95%") {
		t.Errorf("用户把线配在 95%% 时，0.96 必须按 95%% 判不合格（不许悄悄放宽到 90%%），实际：%q", prob)
	}
	if prob, _ := darkVerdict(0.92, strict); prob != "" {
		t.Errorf("用户线是 95%%，0.92 就不该判不合格（那是用户自己的选择），实际：%q", prob)
	}
	// ④ 干净画面：什么都不报
	if prob, warn := darkVerdict(0.10, darkScene); prob != "" || warn != "" {
		t.Errorf("亮画面不该报任何东西：problem=%q warn=%q", prob, warn)
	}
}
