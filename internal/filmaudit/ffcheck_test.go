package filmaudit

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ffcheck_test.go 只用**真机 ffmpeg 日志原文**当夹具（testdata/ff_*_real.txt，逐字，不许改写）。
//
// 夹具是 2026-10-09 在本机对**真机成片**
// `data/projects/被开除一年后，我回公司送水/被开除一年后，我回公司送水·第01集·这筐纸别动/output/final.mp4`
// 跑出来的 stderr（合并）：
//
//	ff_silence_real.txt   ffmpeg -af silencedetect=n=-50dB:d=1.5 -f null -
//	ff_freeze_real.txt    ffmpeg -vf freezedetect=n=-60dB:d=1.5 -f null -
//	ff_loudness_real.txt  ffmpeg -af ebur128=peak=true -f null -
//	ff_cuts_real.txt      ffmpeg -vf "select='gt(scene,0.3)',showinfo" -f null -
//
// ff_freeze_clip014_real.txt 是同一集的一镜（clips/shot_014.mp4）在**同一个滤镜、更松的容差**
// （freezedetect=n=-30dB:d=0.5）下的原文：真机那一集在 -60dB 下**一段像素级冻帧都没有**
// （日志里一行 freeze 都没有），要验"冻帧解析出来的数字与日志一致"就必须找一个真出冻帧的产物。
//
// ff_cuts_none_real.txt 同一集的一镜（clips/shot_001.mp4）在 `select='gt(scene,0.99)',showinfo`
// （阈值没有一帧越得过）下的原文 —— 用来验"滤镜真的跑了、但一帧都没选中"这种形态：
// 它是**采到了、实测 0 处**，与"没采到"必须分开（否则 0 处会被漏报成没事）。
//
// 【读这几个数之前必须知道的一件事】那份 final.mp4 的**视频轨只有 23.708 秒**（569 帧 @24fps），
// 而音频轨是 122.499 秒 —— 也就是说冻帧与切点这两条命令只覆盖了片子开头 24 秒，静音与响度覆盖全片。
// 同一个"成片"上四条命令的**覆盖范围并不一样**；这一层判据只解读日志本身，
// "哪一段被验过、哪一段没有"的如实播报是调用方的事。

// ffFixture 读一份真机日志夹具（逐字）。
func ffFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读不到夹具 %s：%v", name, err)
	}
	return string(b)
}

// byWhere 取出某一项（Where）的结论。
func byWhere(fs []Finding, where string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Where == where {
			out = append(out, f)
		}
	}
	return out
}

// 静音解析：解出的每个数都必须是日志原文里出现的数（下面每个数都能在夹具里 grep 到）。
func TestParseSilenceRealLog(t *testing.T) {
	log := ffFixture(t, "ff_silence_real.txt")
	runs := ParseSilence(log)
	// start / end / duration 三列逐字抄自日志的 `silence_start` / `silence_end` 行。
	want := []struct{ start, end, dur float64 }{
		{13.5545, 15.272604, 1.718104},
		{16.465604, 18.341125, 1.875521},
		{20.203104, 23.742333, 3.539229},
		{25.468937, 29.189083, 3.720146},
		{29.839208, 33.143771, 3.304562},
		{33.614688, 36.012458, 2.397771},
		{36.839146, 38.725604, 1.886458},
		{38.725875, 41.808167, 3.082292},
		{44.588458, 49.861125, 5.272667},
		{51.604937, 55.237958, 3.633021},
	}
	if len(runs) != len(want) {
		t.Fatalf("该解出 %d 段静音，实际 %d 段：%+v", len(want), len(runs), runs)
	}
	for i, w := range want {
		got := runs[i]
		if math.Abs(got.Start-w.start) > 1e-9 || math.Abs(got.Dur-w.dur) > 1e-9 {
			t.Errorf("第 %d 段该是 start=%.6f dur=%.6f（日志原文），实际 start=%.6f dur=%.6f",
				i+1, w.start, w.dur, got.Start, got.Dur)
		}
		// End 由 start+dur 推出：日志里的 duration 与 (end-start) 本身差 1e-6 级别的四舍五入。
		if math.Abs(got.End-w.end) > 1e-4 {
			t.Errorf("第 %d 段 end 该在 %.6f 附近，实际 %.6f", i+1, w.end, got.End)
		}
	}
	// 10 段里有 7 段超过 2 秒 ⇒ 判据（闸门本体）该出 7 条 warn。
	if fs := StdFindings(FFAnalysis{Silence: log}, -1); len(byWhere(fs, "film.silence")) != 7 {
		t.Errorf("超过 2 秒的静音该报 7 条，实际 %d 条：%+v",
			len(byWhere(fs, "film.silence")), byWhere(fs, "film.silence"))
	}
}

// 日志被截断时**不编造时长**：最后一段只有起点 ⇒ Dur 记 0，也不据此报警。
func TestParseSilenceTruncatedLogDoesNotInventDuration(t *testing.T) {
	log := ffFixture(t, "ff_silence_real.txt")
	cut := log[:strings.LastIndex(log, "silence_end:")]
	if cut == log {
		t.Fatal("夹具里没找到 silence_end —— 这条断言会变成空的，先对字样")
	}
	runs := ParseSilence(cut)
	if len(runs) != 10 {
		t.Fatalf("截断后该仍有 10 段（最后一段只剩起点），实际 %d 段", len(runs))
	}
	if d := runs[9].Dur; d != 0 {
		t.Errorf("没有收尾行 ⇒ 时长无从得知，该记 0（不编造），实际 %.6f", d)
	}
	if runs[9].Start != runs[9].End {
		t.Errorf("时长未知的段不该凭空给出结束点：%+v", runs[9])
	}
	// 少掉的恰好是最后那一段的判定（7 → 6）；没量到时长的段既不许当"断音"、也不许当"合格"。
	fs := StdFindings(FFAnalysis{Silence: cut}, -1)
	if got := len(byWhere(fs, "film.silence")); got != 6 {
		t.Errorf("截断后该还剩 6 条静音结论，实际 %d 条：%+v", got, byWhere(fs, "film.silence"))
	}
}

// 冻帧解析：真机成片（-60dB/1.5s）一段都没有；同一集那一镜（-30dB/0.5s）有 6 段，
// 含"首尾相接"与"到文件结尾还没收尾"两种形态。
func TestParseFreezeRealLogs(t *testing.T) {
	film := ffFixture(t, "ff_freeze_real.txt")
	if runs := ParseFreeze(film); len(runs) != 0 {
		t.Errorf("那次真机 -60dB/1.5s 一段冻帧都没量到，实际 %d 段：%+v", len(runs), runs)
	}
	// 没有冻帧 ⇒ 这条判据不出声（"没采到"与"采到且干净"都由调用方按日志是否为空区分）。
	if fs := byWhere(StdFindings(FFAnalysis{Freeze: film}, -1), "film.freeze"); len(fs) != 0 {
		t.Errorf("没有冻帧时不许出冻帧结论：%+v", fs)
	}

	clip := ffFixture(t, "ff_freeze_clip014_real.txt")
	runs := ParseFreeze(clip)
	want := []struct{ start, dur float64 }{
		{0.333333, 1.166667},
		{1.5, 1.166667},
		{2.666667, 0.583333},
		{3.5, 0.583333},
		{4.083333, 1.666667},
		{5.75, 0}, // 只有 freeze_start：日志到文件结束都没打 duration/end
	}
	if len(runs) != len(want) {
		t.Fatalf("该解出 %d 段冻帧，实际 %d 段：%+v", len(want), len(runs), runs)
	}
	for i, w := range want {
		if math.Abs(runs[i].Start-w.start) > 1e-9 || math.Abs(runs[i].Dur-w.dur) > 1e-9 {
			t.Errorf("第 %d 段该是 start=%.6f dur=%.6f（日志原文），实际 start=%.6f dur=%.6f",
				i+1, w.start, w.dur, runs[i].Start, runs[i].Dur)
		}
	}
	// 首尾相接的两段**不许并成一段**（那是两次独立的冻结，日志里就是两组 start/duration/end）。
	if math.Abs(runs[1].Start-runs[0].End) > 1e-9 {
		t.Errorf("首尾相接的判定错了：run0=%+v run1=%+v", runs[0], runs[1])
	}
	// ≥1 秒的 3 段 ⇒ 3 条 warn（最后那段时长未知，不许算进"合格"也不许算进"冻结"）。
	freezeFs := byWhere(StdFindings(FFAnalysis{Freeze: clip}, -1), "film.freeze")
	if len(freezeFs) != 3 {
		t.Fatalf("≥1 秒的冻帧该报 3 条，实际 %d 条：%+v", len(freezeFs), freezeFs)
	}
	for _, f := range freezeFs {
		if f.Level != "warn" {
			t.Errorf("冻帧该是 warn：%+v", f)
		}
	}
	if !strings.Contains(freezeFs[2].Msg, "4.08–5.75s") || !strings.Contains(freezeFs[2].Msg, "1.67 秒") {
		t.Errorf("冻帧文案要点名时间段与秒数：%q", freezeFs[2].Msg)
	}
}

// 响度解析：真机日志里的三个数（-16.3 LUFS / LRA 10.7 LU / 真峰值 -1.0 dBFS）。
func TestParseLoudnessRealLog(t *testing.T) {
	log := ffFixture(t, "ff_loudness_real.txt")
	l := ParseLoudness(log)
	if !l.OK {
		t.Fatal("真机日志里有 ebur128 的 Summary 块 ⇒ 必须算采到")
	}
	if math.Abs(l.Integrated+16.3) > 1e-9 {
		t.Errorf("集成响度该是 -16.3 LUFS（Summary 原文），实际 %.2f", l.Integrated)
	}
	if math.Abs(l.LRA-10.7) > 1e-9 {
		t.Errorf("响度范围该是 10.7 LU（Summary 原文），实际 %.2f", l.LRA)
	}
	if math.Abs(l.TruePeakDBTP+1.0) > 1e-9 {
		t.Errorf("真峰值该是 -1.0 dBFS（Summary 原文），实际 %.2f", l.TruePeakDBTP)
	}
	// **只认 Summary**：逐帧行里也有 `I:` / `LRA:` 字样（那是运行值）。
	// 把 Summary 之前的一切切掉 ⇒ 一个字都不许说，更不许拿逐帧的运行值当结论。
	if l2 := ParseLoudness(log[:strings.Index(log, "Summary:")]); l2.OK {
		t.Errorf("没有 Summary 块时必须算没采到（逐帧行里的 I/LRA 是运行值），实际 %+v", l2)
	}
	// 真机这一集：-16.3 与目标差 0.3 LU（在 ±1.5 内）；真峰值 -1.0 dBTP **恰好**在上限上，
	// "超过"是严格大于 ⇒ 两条判据都不该响（这一集听感上确实没问题）。
	if fs := byWhere(StdFindings(FFAnalysis{Loudness: log}, -1), "film.loudness"); len(fs) != 0 {
		t.Errorf("真机这一集响度在合格一侧 ⇒ 不该出结论，实际 %+v", fs)
	}
}

// 切点解析：真机日志里 7 个 pts_time，逐个数出来。
func TestParseCutTimesRealLog(t *testing.T) {
	cuts := ParseCutTimes(ffFixture(t, "ff_cuts_real.txt"))
	want := []float64{3.416667, 5.791667, 8, 9.875, 12.333333, 17.166667, 20.458333}
	if len(cuts) != len(want) {
		t.Fatalf("该解出 %d 处硬切，实际 %d 处：%v", len(want), len(cuts), cuts)
	}
	for i, w := range want {
		if math.Abs(cuts[i]-w) > 1e-9 {
			t.Errorf("第 %d 处该是 %.6f 秒（日志原文），实际 %.6f", i+1, w, cuts[i])
		}
	}
}

// "相邻不足 0.2 秒的点并成一个"这条规则在真机日志里**碰不到**（真机那 7 个点彼此差 1 秒以上），
// 所以这里用**人造**日志只验这一条规则（行格式照 showinfo 原文抄）；真机数字一律来自上面的夹具。
// 正好 0.2 秒那种边界不测（浮点表示 1.45-1.25 = 0.199999…），免得测的是浮点而不是规则。
func TestParseCutTimesMergesNeighbours(t *testing.T) {
	line := func(n, pts, ptsTime string) string {
		return "[Parsed_showinfo_1 @ 0x1] n: " + n + " pts: " + pts + " pts_time:" + ptsTime +
			" duration:    512 duration_time:0.0416667 fmt:yuv420p cl:left sar:64/63 s:1080x1920"
	}
	log := strings.Join([]string{
		line("0", "24000", "1"),
		line("1", "25000", "1.041667"), // 与上一行只差 1 帧 ⇒ 同一个硬切点，并掉
		line("2", "30000", "1.25"),     // 与留下的那个点差 0.25 秒 ⇒ 另算一处
		line("3", "37200", "1.55"),     // 与留下的那个点差 0.3 秒 ⇒ 再算一处
	}, "\n")
	cuts := ParseCutTimes(log)
	want := []float64{1, 1.25, 1.55}
	if len(cuts) != len(want) {
		t.Fatalf("该并成 %d 处硬切，实际 %d 处：%v", len(want), len(cuts), cuts)
	}
	for i, w := range want {
		if math.Abs(cuts[i]-w) > 1e-9 {
			t.Errorf("第 %d 处该是 %.2f 秒，实际 %.2f", i+1, w, cuts[i])
		}
	}
}

// 三态纪律：没采到（空串/空白/垃圾日志）⇒ **一个字都不说**，既不出 warn，也不出"合格"的 info。
func TestStdFindingsSkipsWhatWasNotCaptured(t *testing.T) {
	// 一段"根本没跑到滤镜"的日志：命令写错/滤镜不存在时 ffmpeg 就打这些（没有 showinfo 的初始化行）。
	garbage := "ffmpeg version 9.0.1-full_build\n" +
		"Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'final.mp4':\n" +
		"  Duration: 00:02:02.50, start: 0.000000, bitrate: 1334 kb/s\n" +
		"[AVFilterGraph @ 0x1] No such filter: 'silencedetectt'\n" +
		"silence_start: not-a-number\n" +
		"freeze_end: also-not-a-number\n"
	cases := []FFAnalysis{
		{}, // 四项都没采到：调用方会播报"没验过"
		{Silence: " \n\t", Loudness: "\n", CutLog: "   "},                       // 只有空白
		{Silence: garbage, Freeze: garbage, Loudness: garbage, CutLog: garbage}, // 垃圾日志
	}
	for ci, in := range cases {
		for _, want := range []int{-1, 0, 5} {
			if fs := StdFindings(in, want); len(fs) != 0 {
				t.Errorf("第 %d 组：没采到/垃圾日志时不许出任何结论（wantSceneCuts=%d），实际 %+v",
					ci+1, want, fs)
			}
		}
	}
	// 解析器本体：空/空白/垃圾都不许 panic、不许编出数字。
	for _, log := range []string{"", "   ", "\n\n", garbage} {
		if r := ParseSilence(log); len(r) != 0 {
			t.Errorf("ParseSilence(%q) 该零结果，实际 %+v", log, r)
		}
		if r := ParseFreeze(log); len(r) != 0 {
			t.Errorf("ParseFreeze(%q) 该零结果，实际 %+v", log, r)
		}
		if c := ParseCutTimes(log); len(c) != 0 {
			t.Errorf("ParseCutTimes(%q) 该零结果，实际 %v", log, c)
		}
		if l := ParseLoudness(log); l.OK {
			t.Errorf("ParseLoudness(%q) 该算没采到，实际 %+v", log, l)
		}
	}
}

// 切点口径：不知道方案里有几处 ⇒ 只报实测；与方案不符 ⇒ 说清"少了/多了"各自意味着什么。
//
// 这里**直接喂闸门本体**（`StdFindings(FFAnalysis{CutLog: 真机日志}, M)`），
// 不绕一圈拿别的函数的输出当输入 —— 否则判据改坏了测试还是绿的。
func TestStdFindingsCutCounts(t *testing.T) {
	log := ffFixture(t, "ff_cuts_real.txt")
	if cs := ParseCutTimes(log); len(cs) != 7 {
		t.Fatalf("夹具该有 7 处硬切，实际 %d 处 —— 下面的断言都建立在这个数上", len(cs))
	}

	// ① wantSceneCuts < 0（不知道该有几处）⇒ 只出 info，且报出实测数
	got := byWhere(StdFindings(FFAnalysis{CutLog: log}, -1), "film.cuts")
	if len(got) != 1 || got[0].Level != "info" || !strings.Contains(got[0].Msg, "7 处硬切点") {
		t.Errorf("不知道方案里几处时该只出 1 条 info 并报实测 7 处，实际 %+v", got)
	}

	// ② 与方案一致（7 == 7）⇒ 不出结论
	if got := byWhere(StdFindings(FFAnalysis{CutLog: log}, 7), "film.cuts"); len(got) != 0 {
		t.Errorf("实测与方案一致时不该出结论，实际 %+v", got)
	}

	// ③ 实测少于方案（7 < 14）⇒ warn，点名"少了"与"同场接缝仍在叠化"
	got = byWhere(StdFindings(FFAnalysis{CutLog: log}, 14), "film.cuts")
	if len(got) != 1 || got[0].Level != "warn" {
		t.Fatalf("实测少于方案该出 1 条 warn，实际 %+v", got)
	}
	for _, s := range []string{"实测 7 处硬切", "场次边界 14 处", "少了 7 处", "同场接缝仍在叠化"} {
		if !strings.Contains(got[0].Msg, s) {
			t.Errorf("少切点的文案里该有 %q：%q", s, got[0].Msg)
		}
	}

	// ④ 实测多于方案（7 > 3）⇒ warn，点名"多了"与"意外硬切"
	got = byWhere(StdFindings(FFAnalysis{CutLog: log}, 3), "film.cuts")
	if len(got) != 1 || got[0].Level != "warn" {
		t.Fatalf("实测多于方案该出 1 条 warn，实际 %+v", got)
	}
	for _, s := range []string{"实测 7 处硬切", "场次边界 3 处", "多了 4 处", "意外硬切"} {
		if !strings.Contains(got[0].Msg, s) {
			t.Errorf("多切点的文案里该有 %q：%q", s, got[0].Msg)
		}
	}

	// ⑤ 滤镜真的跑了、但**一帧都没选中**（真机日志：同一集 clips/shot_001.mp4 在 gt(scene,0.99) 下
	//    —— 阈值没有一帧越过）⇒ 这是「采到了、实测 0 处」，不许当成「没采到」（那会把 0 处漏报成没事）。
	none := ffFixture(t, "ff_cuts_none_real.txt")
	if cs := ParseCutTimes(none); len(cs) != 0 {
		t.Fatalf("这份夹具该一处都没选中，实际 %v", cs)
	}
	got = byWhere(StdFindings(FFAnalysis{CutLog: none}, 6), "film.cuts")
	if len(got) != 1 || got[0].Level != "warn" || !strings.Contains(got[0].Msg, "实测 0 处硬切") {
		t.Errorf("滤镜跑过但一帧没选中 ⇒ 该按「采到且实测 0 处」报 warn，实际 %+v", got)
	}
	if got := byWhere(StdFindings(FFAnalysis{CutLog: none}, -1), "film.cuts"); len(got) != 1 || got[0].Level != "info" {
		t.Errorf("wantSceneCuts<0 时该项也该出 1 条 info（0 处），实际 %+v", got)
	}
}

// 响度那两条判据在真机这一集上**恰好都落在合格一侧**（-16.3 LUFS 差 0.3；真峰值 -1.0 dBTP 正好在上限上），
// 也就是"把判据改坏了也看不出来"。所以这里拿真机日志的 Summary 段**原文**、只改那两个数字再喂回去，
// 证明两条判据真的在管这件事（改数字就算，改格式/改位置不算）。
func TestStdFindingsLoudnessThresholds(t *testing.T) {
	log := ffFixture(t, "ff_loudness_real.txt")
	sum := log[strings.LastIndex(log, "Summary:"):]

	// ① 集成响度差 3.0 LU（> 1.5）⇒ warn，文案要写清"实测 x LUFS / 目标 -16"
	loud := strings.Replace(sum, "I:         -16.3 LUFS", "I:         -13.0 LUFS", 1)
	if loud == sum {
		t.Fatal("夹具里那行集成响度没被替换到（字样变了）—— 这条断言会变成空的，先对字样")
	}
	if l := ParseLoudness(loud); !l.OK || math.Abs(l.Integrated+13.0) > 1e-9 {
		t.Fatalf("替换后的日志该解析出 -13.0 LUFS，实际 %+v", l)
	}
	got := byWhere(StdFindings(FFAnalysis{Loudness: loud}, -1), "film.loudness")
	if len(got) != 1 || got[0].Level != "warn" {
		t.Fatalf("响度差 3 LU 该出 1 条 warn，实际 %+v", got)
	}
	for _, s := range []string{"-13.0 LUFS", "目标 -16", "超过 1.5 LU"} {
		if !strings.Contains(got[0].Msg, s) {
			t.Errorf("响度文案里该有 %q：%q", s, got[0].Msg)
		}
	}

	// ② 真峰值 -0.2 dBTP（> -1.0 上限）⇒ warn，点名削顶
	peak := strings.Replace(sum, "Peak:       -1.0 dBFS", "Peak:       -0.2 dBFS", 1)
	if peak == sum {
		t.Fatal("夹具里那行真峰值没被替换到（字样变了）—— 这条断言会变成空的，先对字样")
	}
	got = byWhere(StdFindings(FFAnalysis{Loudness: peak}, -1), "film.loudness")
	if len(got) != 1 || got[0].Level != "warn" {
		t.Fatalf("真峰值超 -1.0 dBTP 该出 1 条 warn，实际 %+v", got)
	}
	for _, s := range []string{"-0.2 dBTP", "削顶"} {
		if !strings.Contains(got[0].Msg, s) {
			t.Errorf("真峰值文案里该有 %q：%q", s, got[0].Msg)
		}
	}

	// ③ 两项都越界 ⇒ 两条结论（一条不许遮掉另一条）
	both := strings.Replace(loud, "Peak:       -1.0 dBFS", "Peak:       -0.2 dBFS", 1)
	if got := byWhere(StdFindings(FFAnalysis{Loudness: both}, -1), "film.loudness"); len(got) != 2 {
		t.Errorf("响度与真峰值都越界该出 2 条，实际 %d 条：%+v", len(got), got)
	}

	// ④ 真机原文里 -1.0 dBTP **恰好**在上限上 ⇒ "超过"是严格大于，不许报（边界不许抖）
	if l := ParseLoudness(sum); !l.OK || l.TruePeakDBTP > truePeakWarnDBTP {
		t.Errorf("真机原文的真峰值 %.2f 恰好等于上限 ⇒ 不该判为超过", l.TruePeakDBTP)
	}
}

// 把四项**真机日志**一起喂进去：这就是集成后成片体检会多出来的那几条（确定性、读盘即可判）。
func TestStdFindingsOnRealFilmLogs(t *testing.T) {
	a := FFAnalysis{
		Silence:  ffFixture(t, "ff_silence_real.txt"),
		Freeze:   ffFixture(t, "ff_freeze_real.txt"),
		Loudness: ffFixture(t, "ff_loudness_real.txt"),
		CutLog:   ffFixture(t, "ff_cuts_real.txt"),
	}
	fs := StdFindings(a, 7)
	t.Logf("真机四项结论：%+v", fs)

	if got := byWhere(fs, "film.silence"); len(got) != 7 {
		t.Errorf("该出 7 条静音结论，实际 %d 条：%+v", len(got), got)
	}
	if got := byWhere(fs, "film.freeze"); len(got) != 0 {
		t.Errorf("这份素材没有冻帧，不该出结论：%+v", got)
	}
	if got := byWhere(fs, "film.loudness"); len(got) != 0 {
		t.Errorf("响度在合格一侧，不该出结论：%+v", got)
	}
	if got := byWhere(fs, "film.cuts"); len(got) != 0 {
		t.Errorf("实测 7 处与方案 7 处一致，不该出结论：%+v", got)
	}
	for _, f := range fs {
		if f.Level != "warn" || f.Where != "film.silence" {
			t.Errorf("这一集只该出静音的 warn，实际 %+v", f)
		}
	}
	// 文案要点名**时间段与秒数**（最长那一段：44.588458–49.861125，5.272667 秒）
	joined := strings.Join(msgOf(fs), "\n")
	for _, s := range []string{"44.59–49.86s", "5.27 秒"} {
		if !strings.Contains(joined, s) {
			t.Errorf("静音文案里该有 %q：\n%s", s, joined)
		}
	}
}
