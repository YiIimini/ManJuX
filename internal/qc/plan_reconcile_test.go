package qc

import (
	"context"
	"math"
	"strings"
	"testing"
)

// 质检与**计划**对账 + 逐帧黑屏判定的契约测试(审计 2026-09-26:M27 / M28 / M29)。
//
// 三条都属于"报告里看着没问题、实际没在看"的静默失效:
//   - M27 黑屏判据是三帧汇总平均 → 只有一帧全黑被稀释到 warn 阈值以下;
//   - M28 画幅/帧率只记录不核对 → 合成补黑边改帧率而报告里一条 warning 都没有;
//   - M29 只看全局 3–18s → 3.0–3.9s 的残片(计划下限 4s)一律判"通过"。

// TestDarkRatioCatchesSingleBlackSample M27:采样帧里只要**有一帧**全黑就算数,
// 不能被汇总平均稀释掉。
//
// 造片:10 秒里前 8 秒白、后 2 秒全黑。旧的"三帧汇总平均"在 25/50/75%(2.5s/5s/7.5s)
// 三个点上全落在白区 → darkRatio≈0 → 判"通过";尾部黑是 H3 最常见的失效形态。
func TestDarkRatioCatchesSingleBlackSample(t *testing.T) {
	f := newFF(t)
	dir := t.TempDir()
	clip := makeClip(t, dir, 10, 8, "white", "black")

	rep := Check(context.Background(), f, 1, clip, Params{
		DarkRatioFail: 0.60, DarkRatioWarn: 0.45,
		MinDuration: 1, MaxDuration: 60,
	})
	if len(rep.DarkRatios) < 5 {
		t.Fatalf("应逐帧回报暗像素占比(≥5 个采样点),实际 %d 个:%v", len(rep.DarkRatios), rep.DarkRatios)
	}
	worst := 0.0
	for _, d := range rep.DarkRatios {
		if d > worst {
			worst = d
		}
	}
	if worst < 0.45 {
		t.Fatalf("尾部 2 秒全黑必须至少有一帧超 warn 阈值,实测逐帧值 %v", rep.DarkRatios)
	}
	if rep.DarkRatio != worst {
		t.Errorf("DarkRatio 应为最暗帧的值(%.2f),实际 %.2f", worst, rep.DarkRatio)
	}
	if !hasProblem(rep, "疑似黑屏") {
		t.Fatalf("尾部黑必须判黑屏(旧实现对这种片返回 ≈0),实际 problems=%v", rep.Problems)
	}
}

// TestCheckComparesWithPlannedDuration M29:与**本镜计划**时长对账。
//
// 短于计划 = 截断或旧时长残留(本地引擎只会向上对齐,不会变短);
// 长于计划 = 云端整秒抬升 / 对齐口径不同。
func TestCheckComparesWithPlannedDuration(t *testing.T) {
	f := newFF(t)
	dir := t.TempDir()
	clip := makeClip(t, dir, 5, 5, "white", "white")

	// 计划 6s,实测 5s → 短 1s:残片级别,必须进 Problems
	rep := Check(context.Background(), f, 1, clip, Params{
		MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.9,
		ExpectedDuration: 6,
	})
	if !hasProblem(rep, "时长短于计划") {
		t.Errorf("实测比计划短 1 秒应判问题(可能是截断/旧产物),实际 problems=%v warnings=%v",
			rep.Problems, rep.Warnings)
	}

	// 计划 4s,实测 5s → 长 1s:云端把 4s 抬成 5s 的情形,要如实告警但不阻断
	rep = Check(context.Background(), f, 1, clip, Params{
		MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.9,
		ExpectedDuration: 4,
	})
	if !hasWarn(rep, "超出计划") {
		t.Errorf("实测比计划长 1 秒应告警(云端按整秒抬升),实际 warnings=%v", rep.Warnings)
	}
	if hasProblem(rep, "时长短于计划") {
		t.Errorf("比计划长不该判成短于计划:%v", rep.Problems)
	}

	// 计划 5s,实测 5s → 完全一致,不该有任何时长对账消息
	rep = Check(context.Background(), f, 1, clip, Params{
		MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.9,
		ExpectedDuration: 5,
	})
	for _, s := range append(append([]string{}, rep.Problems...), rep.Warnings...) {
		if strings.Contains(s, "计划") {
			t.Errorf("时长与计划一致时不该出现对账消息:%q", s)
		}
	}

	// 调用方没给计划(0)= 不核对,行为与旧版一致
	rep = Check(context.Background(), f, 1, clip, Params{
		MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.9,
	})
	if rep.ExpectedDuration != 0 {
		t.Errorf("未给计划时不该凭空填一个期望值,实际 %.1f", rep.ExpectedDuration)
	}
}

// TestCheckSpecMismatchWarns M28:画幅/帧率与成片规格不符必须出现在报告里 ——
// 合成对画幅差异的处置是 scale+pad(补黑边)、对帧率是强制 fps=24,全是静默的。
func TestCheckSpecMismatchWarns(t *testing.T) {
	f := newFF(t)
	dir := t.TempDir()
	clip := makeClip(t, dir, 4, 4, "white", "white") // 320x240 @24fps

	rep := Check(context.Background(), f, 1, clip, Params{
		MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.9,
		ExpectWidth: 768, ExpectHeight: 1344, ExpectFPS: 30,
	})
	if !hasWarn(rep, "画幅与成片规格不符") {
		t.Errorf("画幅 320x240 vs 768x1344 必须告警,实际 warnings=%v", rep.Warnings)
	}
	if !hasWarn(rep, "帧率与成片规格不符") {
		t.Errorf("帧率 24 vs 30 必须告警,实际 warnings=%v", rep.Warnings)
	}
	if rep.FPS < 23.9 || rep.FPS > 24.1 {
		t.Errorf("报告应带上实测帧率,实际 %.2f", rep.FPS)
	}

	// 规格对得上时保持安静
	rep = Check(context.Background(), f, 1, clip, Params{
		MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.9,
		ExpectWidth: 320, ExpectHeight: 240, ExpectFPS: 24,
	})
	for _, w := range rep.Warnings {
		if strings.Contains(w, "规格不符") {
			t.Errorf("规格一致时不该告警:%q", w)
		}
	}
}

// TestSamplePositionsSpreadAcrossClip 采样点应铺开到接近片尾(尾部黑靠它抓)。
func TestSamplePositionsSpreadAcrossClip(t *testing.T) {
	ps := samplePositions(10)
	if len(ps) != 5 {
		t.Fatalf("应为 5 个采样点,实际 %d", len(ps))
	}
	if math.Abs(ps[len(ps)-1]-9.0) > 0.01 {
		t.Errorf("末点应落在 90%%(9.0s),实际 %.2f", ps[len(ps)-1])
	}
	for i := 1; i < len(ps); i++ {
		if ps[i] <= ps[i-1] {
			t.Errorf("采样点必须递增:%v", ps)
		}
	}
}

func hasWarn(r *Report, kw string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, kw) {
			return true
		}
	}
	return false
}

// TestStrippedBlackFlashIsNotCountedAsTruncation 平台自己剪掉的黑闪不算"产物被截断"。
//
// 【为什么要有（2026-10-11）】黑闪切除的默认单段上限抬到 0.6 秒之后，一次就能剪掉 0.58 秒 ——
// 若质检还按"实测比计划短 0.5 秒 = 残片"判，就会把**平台自己的剪辑**报成"请重渲该镜"。
// 真机形态：镜 15 计划 9s、渲出 9.5s，剪掉 0.583s 黑闪后只剩 8.92s。
//
// 判据：同样的实测时长，**带不带** StrippedSec 结论相反 —— 两个"短"判据（全局下限 + 与计划对账）
// 都要把它加回去；没传（= 没剪过）时旧行为一个字不变。
func TestStrippedBlackFlashIsNotCountedAsTruncation(t *testing.T) {
	f := newFF(t)
	dir := t.TempDir()
	clip := makeClip(t, dir, 5, 5, "white", "white") // 实测 ≈5.0s

	// ① 与计划对账：剪前 5.583s、计划 5.6s ⇒ 剪掉黑闪后剩 5.0s，不该判"截断"。
	rep := Check(context.Background(), f, 1, clip, Params{
		MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.9,
		ExpectedDuration: 5.6, StrippedSec: 0.583,
	})
	for _, s := range append(append([]string{}, rep.Problems...), rep.Warnings...) {
		if strings.Contains(s, "计划") {
			t.Errorf("剪掉的秒数加回去之后与计划一致，不该有任何对账消息：%q", s)
		}
	}
	if math.Abs(rep.StrippedSec-0.583) > 1e-9 {
		t.Errorf("报告里要如实留痕剪掉了几秒，实得 %.3f", rep.StrippedSec)
	}

	// ② 反证（把"加回去"那一步拿掉就该判负 —— 说明这条门真的在咬东西，不是摆设）：
	rep = Check(context.Background(), f, 1, clip, Params{
		MinDuration: 1, MaxDuration: 60, DarkRatioFail: 0.9,
		ExpectedDuration: 5.6,
	})
	if !hasProblem(rep, "时长短于计划") {
		t.Errorf("剪掉的秒数没传进来时，5.0s vs 计划 5.6s 该判「短于计划」：%v", rep.Problems)
	}
	if rep.StrippedSec != 0 {
		t.Errorf("没剪过的片段不该在报告里说有剪（实得 %.3f）", rep.StrippedSec)
	}

	// ③ 全局下限那条同样要减：4 秒镜对齐后 4.46s，剪掉 0.6s 只剩 3.86s < 下限。
	rep = Check(context.Background(), f, 1, clip, Params{
		MinDuration: 5.5, MaxDuration: 60, DarkRatioFail: 0.9, StrippedSec: 0.583,
	})
	if hasProblem(rep, "时长过短") {
		t.Errorf("剪掉的黑闪不该让产物被判「时长过短」：%v", rep.Problems)
	}
	rep = Check(context.Background(), f, 1, clip, Params{
		MinDuration: 5.5, MaxDuration: 60, DarkRatioFail: 0.9,
	})
	if !hasProblem(rep, "时长过短") {
		t.Errorf("不传剪掉秒数时该判「时长过短」（否则下限那道门被这次改动放空了）：%v", rep.Problems)
	}
}
