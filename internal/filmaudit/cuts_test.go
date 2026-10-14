package filmaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// cuts_test.go 「小分镜兑现」判据（2026-10-14）。
//
// 【为什么要有（用户：「分镜脚本中的小分镜到底有没有做好？」）】计划里的内切有契约门全过，
// 但那些门只看**计划的时间点** —— 真机三镜实测：计划 2 刀，片段里 0–1 刀（见 cuts.go 文件头）。
// 判据本体见 cuts.go；这里钉三条：① 少了要报；② 没量到一个字都不说（三态）；
// ③ 多了不报（内容切点对运动/闪光敏感）—— 每条都用**真机形态**的输入。

// 真机那份方案的一镜：8 秒、计划 3 刀（0 / 3.20 / 5.50），带台词。
func realShotWithThreeCuts(id int) project.Shot {
	return project.Shot{
		ID:       id,
		Duration: 8,
		Cuts: []project.Cut{
			{At: 0}, {At: 3.20}, {At: 5.50},
		},
	}
}

// ① 计划 2 个内切，实测只看出 1 个 ⇒ **必须报**（这正是真机镜 1 的形态）。
func TestCutsFewerThanPlannedIsReported(t *testing.T) {
	sh := realShotWithThreeCuts(1)
	level, msg := CutDeliveryNote(sh, []float64{5.041667}, true)
	if msg == "" {
		t.Fatal("计划 2 个内切、实测只 1 个 —— 必须出声（真机镜 1 就是这个形态）")
	}
	if level != "warn" {
		t.Errorf("级别应是 warn（只提示、不阻断），实际 %q", level)
	}
	for _, want := range []string{"计划 2 个内切", "只看出 1 个", "重出这一镜"} {
		if !strings.Contains(msg, want) {
			t.Errorf("结论里应含 %q，实际：%s", want, msg)
		}
	}
	// 口径必须写在结论里（免得下一个读的人以为"多了也报"）。
	if !strings.Contains(msg, "内容切点") || !strings.Contains(msg, "不报") {
		t.Errorf("结论要写明这是**内容切点**口径、以及『多了不报』：%s", msg)
	}
}

// ② 一处都没切出来 ⇒ 报（真机镜 2 的形态），而且**要说清是"滤镜一处都没选中"**：
// 空括号"（实测切点 ）"看着像模板坏了，反倒不像一条结论。
func TestCutsNoneAtAllIsReported(t *testing.T) {
	sh := realShotWithThreeCuts(2)
	level, msg := CutDeliveryNote(sh, []float64{}, true)
	if msg == "" || level != "warn" {
		t.Fatalf("一处都没切出来必须报（真机镜 2 就是这个形态），实际 level=%q msg=%q", level, msg)
	}
	for _, want := range []string{"只看出 0 个", "一处都没选中", "一刀都没切"} {
		if !strings.Contains(msg, want) {
			t.Errorf("结论里应写明 %q，实际：%s", want, msg)
		}
	}
	if strings.Contains(msg, "实测切点 ）") || strings.Contains(msg, "（实测切点 （") {
		t.Errorf("别留空括号 / 双括号：%s", msg)
	}
}

// ③ **没量到**（量尺为 nil / 解码失败）⇒ 一个字都不说 —— 三态：没验过 ≠ 合格，也不编一条。
func TestCutsNotMeasuredStaysSilent(t *testing.T) {
	sh := realShotWithThreeCuts(3)
	if level, msg := CutDeliveryNote(sh, nil, false); msg != "" || level != "" {
		t.Fatalf("没量到时不许出结论（会在别处如实说一句），实际 level=%q msg=%q", level, msg)
	}
}

// ④ 实测**比计划多**时不报：内容切点对快速运动/闪光同样敏感（真机镜里"雨点扫过灯下""水汽升起"
//
//	都能顶到阈值以上），报了会把真问题淹掉。
func TestCutsMoreThanPlannedStaysSilent(t *testing.T) {
	sh := realShotWithThreeCuts(4)
	if level, msg := CutDeliveryNote(sh, []float64{1.0, 2.0, 3.0, 4.0, 5.0}, true); msg != "" {
		t.Fatalf("实测比计划多时不该报（噪声方向），实际 level=%q msg=%q", level, msg)
	}
}

// ⑤ 本来就没计划内切（单刀 / 没切）时不出声 —— 那是 CutCoverageIssues 的活，不是这一条。
func TestCutsSingleShotStaysSilent(t *testing.T) {
	sh := project.Shot{ID: 5, Duration: 8, Cuts: []project.Cut{{At: 0}}}
	if level, msg := CutDeliveryNote(sh, []float64{}, true); msg != "" {
		t.Fatalf("没计划内切时不该出声，实际 level=%q msg=%q", level, msg)
	}
	sh2 := project.Shot{ID: 6, Duration: 8} // 连结构字段都没有
	if level, msg := CutDeliveryNote(sh2, []float64{}, true); msg != "" {
		t.Fatalf("没有 cuts 结构时不该出声，实际 level=%q msg=%q", level, msg)
	}
}

// ⑥ 量到的切点里贴着头的那一个不算"一刀"（`showinfo` 有时会把片段开头自己那一帧选进来）。
func TestCutsZeroPointIsNotACut(t *testing.T) {
	sh := realShotWithThreeCuts(7)
	// 计划 2 刀；实测只有 0.0 一个点（= 片段开头）⇒ 等于一刀都没切 ⇒ 必须报。
	if level, msg := CutDeliveryNote(sh, []float64{0}, true); msg == "" || level != "warn" {
		t.Fatalf("只有头部的 0 秒不算切点，应当报「一处都没切出来」，实际 level=%q msg=%q", level, msg)
	}
	// 计划 2 刀、实测 0.0 + 两个真切点 ⇒ 够了，不报。
	if level, msg := CutDeliveryNote(sh, []float64{0, 3.1, 5.4}, true); msg != "" {
		t.Fatalf("计划 2 刀、实测 0.0+2 个真切点 ⇒ 不该报，实际 level=%q msg=%q", level, msg)
	}
}

// ⑦ 判据吃的是**真机 ffmpeg 日志原文**（三份，逐字不许改写）：
//
//	testdata/ff_cuts_clip001_real.txt  人间收件箱·第01集 镜 1 的片段 → 1 个切点（计划 2 刀）⇒ 报
//	testdata/ff_cuts_clip002_real.txt  同一集镜 2 的片段 → 0 个切点（计划 2 刀）⇒ 报
//	testdata/ff_cuts_real.txt          另一集**整片**（122.5 秒）→ 多处切点 ⇒ 多了，**不报**
//
// 前两份是这一轮真机取证的原始日志（2026-10-14，本机 ffmpeg，`select='gt(scene,0.3)',showinfo`）；
// 第三份是既有夹具（整片），它出现那么多切点正说明**内容切点会多报** —— 这就是"多了不报"
// 那条口径的真实依据（不是想当然）。
func TestCutDeliveryOnRealClipLogs(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("读不到真机日志 %s：%v", name, err)
		}
		if !cutLogUsable(string(raw)) {
			t.Fatalf("%s 应被判为『滤镜真的跑过』（日志里有 Parsed_showinfo）", name)
		}
		return string(raw)
	}
	sh := realShotWithThreeCuts(1) // 计划 2 个内切

	ts1 := ParseCutTimes(read("ff_cuts_clip001_real.txt"))
	if len(ts1) != 1 {
		t.Fatalf("真机镜 1 的日志应解析出 1 个切点，实际 %d 个：%v", len(ts1), ts1)
	}
	if level, msg := CutDeliveryNote(sh, ts1, true); level != "warn" || msg == "" {
		t.Errorf("镜 1（计划 2 刀 / 实测 1 刀）必须报，实际 level=%q msg=%q", level, msg)
	}

	// 镜 2：滤镜跑了、**一帧都没选中** ⇒ 实测 0 个切点 ⇒ 一刀都没切出来，必须报。
	// （"采到了、实测 0 处"与"没采到"必须分开 —— 见 ffcheck.go 的 cutLogUsable 说明。）
	ts2 := ParseCutTimes(read("ff_cuts_clip002_real.txt"))
	if len(ts2) != 0 {
		t.Fatalf("真机镜 2 的日志应解析出 0 个切点，实际 %v", ts2)
	}
	if level, msg := CutDeliveryNote(sh, ts2, true); level != "warn" || msg == "" {
		t.Errorf("镜 2（计划 2 刀 / 实测 0 刀）必须报，实际 level=%q msg=%q", level, msg)
	}

	// 整片那份：多处切点 ≥ 计划 2 刀 ⇒ **不报**（"多了不报"的真实依据）。
	tsFilm := ParseCutTimes(read("ff_cuts_real.txt"))
	if len(tsFilm) < 3 {
		t.Fatalf("整片那份夹具应当有多处切点（它是 122.5 秒的成片），实际 %d 个", len(tsFilm))
	}
	if level, msg := CutDeliveryNote(sh, tsFilm, true); msg != "" {
		t.Errorf("实测（%d 个）比计划（2 刀）多时不该报，实际 level=%q msg=%q", len(tsFilm), level, msg)
	}
}

// ⑧ 落成体检结论时**镜号要点名**（不然用户不知道该重出哪一镜）。
func TestCutDeliveryFindingsNameTheShot(t *testing.T) {
	in := Input{
		Plan: &project.Plan{Shots: []project.Shot{
			realShotWithThreeCuts(1),
			realShotWithThreeCuts(2),
		}},
		ShotCuts: [][]float64{{5.041667}, nil}, // 镜 1 量到了（少切）；镜 2 没量到
	}
	fs := cutDeliveryFindings(in)
	if len(fs) != 1 {
		t.Fatalf("应当只有镜 1 出一条（镜 2 没量到 ⇒ 跳过），实际 %d 条：%+v", len(fs), fs)
	}
	if fs[0].Where != "shot #1" {
		t.Errorf("要点名镜号，实际 %q", fs[0].Where)
	}
	// 反面：两镜都没量到 ⇒ 一条都不出（调用方会说"没量到"那句）。
	in2 := Input{Plan: in.Plan, ShotCuts: [][]float64{nil, nil}}
	if fs := in2; len(cutDeliveryFindings(fs)) != 0 {
		t.Errorf("全都没量到时不该出结论，实际 %+v", cutDeliveryFindings(in2))
	}
}

// ⑨ **真入口**那一趟：真机日志 → cutsFromLog → MeasureCuts → shotCutTimes → 判据。
//
// 【为什么必须单写这一条（2026-10-14，我自己的缺陷）】上面⑦是**手写切片**直接喂判据，
// 而真入口走的是 `ParseCutTimes` 在"一帧都没选中"时返回的 **nil** ⇒ 真机镜 2
// （计划 2 刀 / 画面里 0 刀，正是最该报的那一镜）在体检里被印成「没量到」，判据一个字都没说，
// 单测却全绿。这条从**真机日志原文**一路走到结论：镜 1（1 刀）报、镜 2（0 刀）报、
// 解码失败的那格必须判成"没量到"（三态不许反着用）。
func TestCutsRealEntryKeepsZeroAndMissingApart(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("读不到真机日志 %s：%v", name, err)
		}
		return string(raw)
	}
	// ① 真机镜 2：滤镜跑过、一帧都没选中 ⇒ 必须是**非 nil 空切片**（量到 0 刀）。
	zero, err := cutsFromLog(read("ff_cuts_clip002_real.txt"))
	if err != nil {
		t.Fatalf("镜 2 的日志应当算『量到了』（滤镜已初始化）：%v", err)
	}
	if zero == nil {
		t.Fatal("『量到 0 刀』必须是**非 nil 空切片** —— nil 会被 shotCutTimes 当成『没量到』（真机缺陷就是这么来的）")
	}
	if len(zero) != 0 {
		t.Fatalf("镜 2 应当解析出 0 个切点，实际 %v", zero)
	}
	// ② 走真入口：clips 三格 = 镜 1 真机日志 / 镜 2 真机日志 / 解码失败。
	fn := func(path string) ([]float64, error) {
		switch path {
		case "clip1":
			return cutsFromLog(read("ff_cuts_clip001_real.txt"))
		case "clip2":
			return cutsFromLog(read("ff_cuts_clip002_real.txt"))
		}
		return nil, fmt.Errorf("解码失败：%s", path)
	}
	got, missing := MeasureCuts(fn, []string{"clip1", "clip2", "clip3"})
	if len(missing) != 1 || missing[0] != 2 {
		t.Fatalf("只有第三格（解码失败）算没量到，实际 missing=%v", missing)
	}
	if _, ok := shotCutTimes(got, 1); !ok {
		t.Fatal("镜 2 是『量到 0 刀』，不许被判成『没量到』—— 那会把已经量出来的失败印成没验过")
	}
	if _, ok := shotCutTimes(got, 2); ok {
		t.Fatal("解码失败那一格必须判成『没量到』（三态里的第三态）")
	}
	// ③ 判据层：镜 1、镜 2 各出一条（1 刀 / 0 刀），没量到的那格不出声。
	in := Input{
		Plan: &project.Plan{Shots: []project.Shot{
			realShotWithThreeCuts(1),
			realShotWithThreeCuts(2),
		}},
		ShotCuts: got[:2],
	}
	fs := cutDeliveryFindings(in)
	if len(fs) != 2 {
		t.Fatalf("镜 1（1 刀）与镜 2（0 刀）都该出一条，实际 %d 条：%+v", len(fs), fs)
	}
	if fs[0].Where != "shot #1" || fs[1].Where != "shot #2" {
		t.Errorf("要点名镜号，实际 %q / %q", fs[0].Where, fs[1].Where)
	}
	if !strings.Contains(fs[1].Msg, "只看出 0 个") {
		t.Errorf("镜 2 的结论要写明『只看出 0 个』，实际：%s", fs[1].Msg)
	}
}

// ⑩ 自定义量尺把"0 处"写成 nil（老实现/第三方量尺）也照样算『量到』——
// `MeasureCuts` 是那条统一口径的地方；去掉它，上面那条真入口门立刻变红。
func TestMeasureCutsNormalizesNilToMeasuredZero(t *testing.T) {
	fn := func(path string) ([]float64, error) { return nil, nil }
	got, missing := MeasureCuts(fn, []string{"clip1"})
	if len(missing) != 0 {
		t.Fatalf("没给错误 = 量到了，不该记进 missing，实际 %v", missing)
	}
	ts, ok := shotCutTimes(got, 0)
	if !ok {
		t.Fatal("nil,nil 也要算『量到 0 刀』（MeasureCuts 负责统一成空切片）")
	}
	if len(ts) != 0 {
		t.Fatalf("应当是空切片，实际 %v", ts)
	}
}
