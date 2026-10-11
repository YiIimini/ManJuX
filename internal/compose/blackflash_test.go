package compose

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// blackflash_test.go 黑闪切除的判据。
//
// 夹具两类，都是**真机实测/原文**：
//   - ffmpeg `blackdetect` 的日志行 = 2026-10-08《顺其自然（真实事迹短片）》第01集那几条；
//   - 合成片段 = 本机 ffmpeg 现造（没装 ffmpeg 的机器上跳过，不假装验过）。
func TestParseBlackRunsRealLog(t *testing.T) {
	// 真机原文（`ffmpeg -vf blackdetect=d=0.02:pix_th=0.10:pic_th=0.98`）：
	log := `[Parsed_blackdetect_0 @ 000001] black_start:0 black_end:0.208333 black_duration:0.208333
[Parsed_blackdetect_0 @ 000001] black_start:4.458333 black_end:4.541667 black_duration:0.0833333
[Parsed_blackdetect_0 @ 000001] black_start:2.416667 black_end:2.541667 black_duration:0.125`
	runs := ParseBlackRuns(log)
	if len(runs) != 3 {
		t.Fatalf("应解析出 3 段黑，实得 %d：%v", len(runs), runs)
	}
	if runs[0].Start != 0 || runs[0].End != 0.208333 {
		t.Errorf("第 1 段（真机片头黑）不对：%v", runs[0])
	}
	if d := runs[2].Dur(); d < 0.124 || d > 0.126 {
		t.Errorf("第 3 段时长应≈0.125s（真机镜 13 的那 125 毫秒），实得 %.4f", d)
	}
	// 没有黑段时不许编
	if got := ParseBlackRuns("nothing here"); len(got) != 0 {
		t.Errorf("没有黑段时应返回空：%v", got)
	}
}

func TestStrippableBlackRuns(t *testing.T) {
	short := []BlackRun{{0, 0.208333}, {4.458333, 4.541667}, {2.416667, 2.541667}}
	if got := strippableBlackRuns(short, MaxBlackRunSec, MaxBlackTotalSec); len(got) != 3 {
		t.Errorf("真机那三段短黑都该剪，实得 %d 段：%v", len(got), got)
	}
	// 长黑段不剪（真机那种"整镜黑/暗场"必须留给质检报，不能在这里悄悄改）
	long := []BlackRun{{0, 2.0}, {5.0, 6.5}}
	if got := strippableBlackRuns(long, MaxBlackRunSec, MaxBlackTotalSec); len(got) != 0 {
		t.Errorf("长黑段不该剪，实得 %v", got)
	}
	// 累计上限：4 段 0.4s 只剪前两段（0.8s ≤ 1.0s）
	many := []BlackRun{{0, 0.4}, {2, 2.4}, {4, 4.4}, {6, 6.4}}
	if got := strippableBlackRuns(many, MaxBlackRunSec, MaxBlackTotalSec); len(got) != 2 {
		t.Errorf("累计上限 1.0s 内只该剪 2 段，实得 %d：%v", len(got), got)
	}
	// 一帧级噪声（<0.02s）不动
	if got := strippableBlackRuns([]BlackRun{{1.0, 1.01}}, MaxBlackRunSec, MaxBlackTotalSec); len(got) != 0 {
		t.Errorf("一帧级噪声不该动：%v", got)
	}
	// 混合：短的剪、长的留着
	mixed := []BlackRun{{0, 0.2}, {3.0, 6.0}, {7.0, 7.13}}
	if got := strippableBlackRuns(mixed, MaxBlackRunSec, MaxBlackTotalSec); len(got) != 2 {
		t.Errorf("混合时该剪那两段短的，实得 %v", got)
	}
}

// ffexe 找一个可用的 ffmpeg+ffprobe（找不到就跳过集成测试 —— 不假装验过）。
func ffexe(t *testing.T) *ff.FF {
	t.Helper()
	if f, err := ff.New("", ""); err == nil {
		return f
	}
	ffp, e1 := exec.LookPath("ffmpeg")
	fp, e2 := exec.LookPath("ffprobe")
	if e1 == nil && e2 == nil {
		if f, err := ff.New(ffp, fp); err == nil {
			return f
		}
	}
	t.Skip("本机找不到 ffmpeg/ffprobe，跳过集成测试")
	return nil
}

// TestStripBlackFlashRemovesShortFlashAndKeepsLongBlack 端到端：真造一条带黑闪的片段。
func TestStripBlackFlashRemovesShortFlashAndKeepsLongBlack(t *testing.T) {
	f := ffexe(t)
	ctx := context.Background()
	dir := t.TempDir()

	mk := func(out string, parts []string) {
		args := []string{"-y", "-v", "error"}
		for _, p := range parts {
			args = append(args, "-f", "lavfi", "-i", p)
		}
		var ins []string
		for i := range parts {
			ins = append(ins, "["+string(rune('0'+i))+":v]")
		}
		fc := strings.Join(ins, "") + "concat=n=" + string(rune('0'+len(parts))) + ":v=1:a=0[v]"
		args = append(args, "-filter_complex", fc, "-map", "[v]", "-pix_fmt", "yuv420p", out)
		if _, err := f.Run(ctx, args...); err != nil {
			t.Skipf("造夹具失败（%v）", err)
		}
	}

	// ① 亮 1.0s → 黑 0.25s → 亮 1.75s（总 3.0s，无音轨）
	short := filepath.Join(dir, "short.mp4")
	mk(short, []string{
		"color=c=white:s=320x240:d=1.0:r=24",
		"color=c=black:s=320x240:d=0.25:r=24",
		"color=c=white:s=320x240:d=1.75:r=24",
	})
	before, err := f.Probe(short)
	if err != nil {
		t.Fatal(err)
	}
	removed, n, longRuns, err := StripBlackFlash(ctx, f, short, 24)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || removed < 0.24 || removed > 0.26 {
		t.Errorf("应剪掉 1 段、约 0.25s，实得 %d 段 / %.3fs", n, removed)
	}
	if len(longRuns) != 0 {
		t.Errorf("这条片段没有长黑段，不该报：%v", longRuns)
	}
	after, err := f.Probe(short)
	if err != nil {
		t.Fatal(err)
	}
	if d := before.Duration - after.Duration; d < 0.20 || d > 0.35 {
		t.Errorf("剪完时长应缩短≈0.25s（前 %.3f → 后 %.3f）", before.Duration, after.Duration)
	}
	if runs, err := DetectBlackRuns(ctx, f, short); err != nil || len(runs) != 0 {
		t.Errorf("剪完之后不该再有黑段（runs=%v, err=%v）", runs, err)
	}
	// 幂等：第二次调用（标记已在）不再动文件
	st, _ := os.Stat(short)
	if _, _, _, err := StripBlackFlash(ctx, f, short, 24); err != nil {
		t.Fatalf("第二次调用不该报错：%v", err)
	}
	st2, _ := os.Stat(short)
	if st.Size() != st2.Size() {
		t.Error("已处理过的片段不该被再动一次（幂等标记失效）")
	}

	// ② 亮 1.0s → 黑 2.5s → 亮 1.0s ⇒ 长黑段不剪，但要报出来
	longClip := filepath.Join(dir, "long.mp4")
	mk(longClip, []string{
		"color=c=white:s=320x240:d=1.0:r=24",
		"color=c=black:s=320x240:d=2.5:r=24",
		"color=c=white:s=320x240:d=1.0:r=24",
	})
	removed2, n2, longRuns2, err := StripBlackFlash(ctx, f, longClip, 24)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 || removed2 != 0 {
		t.Errorf("长黑段不该剪（实得 %d 段 / %.3fs）", n2, removed2)
	}
	if len(longRuns2) != 1 || longRuns2[0].Dur() < 2.0 {
		t.Errorf("长黑段必须如实报出来（那是质检该报的）：%v", longRuns2)
	}
}

// TestBlackFlashStripIsWiredIntoRender 接线：黑闪切除必须挂在**片段唯一确认可用的落盘点**上，
// 且排在指纹/清单记账之前（处理完的才是最终文件），并在渲染收尾处汇总播报。
//
// 这条规则凭什么要机检：引擎偶发吐黑是"看着渲成了、其实片头一黑"的那类缺陷 —— 2026-10-08
// 真机那一集 4 镜被判"疑似黑屏"，其中镜 13 的质检采样点正好压在 125 毫秒黑上（实测 1.00）。
func TestBlackFlashStripIsWiredIntoRender(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "pipeline", "stages_render.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []struct{ needle, why string }{
		{"compose.StripBlackFlash(ctx, c.ff, res.VideoPath, 0)", "渲染阶段没有调用黑闪切除 —— 已有片段里的黑闪永远留着"},
		{"acc.blackFlashShots++", "剪了黑闪要记账（跨重出轮次累加，别再累在每轮重读的 plan 上）"},
		{"acc.blackFlashSec += removed", "剪了多少秒要记账（同上，跨轮）"},
		{"黑闪自检没做成", "自检失败要如实播报（不许静默）"},
		{"共 %d 镜剪掉了片头/片中的**黑闪**", "渲染收尾要汇总播报一次"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("接线缺 %q —— %s", want.needle, want.why)
		}
	}
	// 顺序：先把片段处理干净，再记账（指纹/清单都记"最终文件"）。
	iStrip := strings.Index(src, "compose.StripBlackFlash(")
	iStatus := strings.Index(src, "c.setShotStatusLen(proj.ID, manifest, shot.ID, project.ShotDone")
	if iStrip < 0 || iStatus < 0 || iStrip > iStatus {
		t.Error("黑闪切除必须排在 `setShotStatusLen`（写清单/指纹）之前")
	}
}

// TestClipPostProcessMarkersExpire 两个片段后处理（音频净化、黑闪切除）都必须用
// "标记与文件对得上才算处理过" —— 片段会被重渲覆盖，用 `os.Stat(标记)` 判幂等会让
// **新渲出来的片段被静默跳过**（2026-10-08 真机：shot_001 重渲后的 2 处 0.125s 黑闪没被剪，
// 成片里照旧一黑）。
func TestClipPostProcessMarkersExpire(t *testing.T) {
	for _, c := range []struct{ file, what string }{
		{"blackflash.go", "黑闪切除"},
		{"clip_normalize.go", "音频净化"},
	} {
		raw, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if !strings.Contains(src, "clipMarkerValid(") {
			t.Errorf("%s(%s) 没用 clipMarkerValid —— 片段被重渲后旧标记会让后处理被静默跳过", c.what, c.file)
		}
		if strings.Contains(src, "os.Stat(marker)") {
			t.Errorf("%s(%s) 还在用 os.Stat(marker) 判幂等：只看标记在不在 = 重渲后漏做", c.what, c.file)
		}
	}
	// 判据本身：指纹要来自**文件内容**（大小 + 修改时间 + 首 64KB 哈希），不能只是个常量。
	//
	// 【2026-10-08 深度排查加强】原来只要求"大小 + 修改时间"—— 那能被**恰好相同**的覆盖骗过
	// （FAT/exFAT 的 mtime 只有 2 秒粒度、复制保留时间戳、同长度重渲），标记照样判"有效"、
	// 后处理照样静默跳过。所以这里钉：指纹里必须有内容哈希。
	raw, err := os.ReadFile("clipmark.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []struct{ needle, why string }{
		{"info.Size()", "指纹要带文件大小"},
		{"info.ModTime().UnixNano()", "指纹要带修改时间"},
		{"fnv.New64a()", "指纹要带**内容哈希**（只比大小+mtime 会被同尺寸同时间戳的覆盖骗过）"},
		{"clipFingerprintBytes", "内容哈希要明确只取开头一段（整份读太贵，片段只需区分头 64KB）"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("标记指纹缺少 %s：%s", want.why, want.needle)
		}
	}
	// 行为夹具：同尺寸、只改一个字节的覆盖，指纹必须变（这正是"同长度重渲"的形态）。
	dir := t.TempDir()
	p := filepath.Join(dir, "c.mp4")
	if err := os.WriteFile(p, []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := clipFingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("AAAB"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 把 mtime 拨回原值：模拟"大小与时间戳都没变、只有内容变了"（复制保留时间戳/粗粒度 mtime）。
	if err := os.Chtimes(p, st.ModTime(), st.ModTime()); err != nil {
		t.Skipf("本平台改不动时间戳，跳过这一条：%v", err)
	}
	after, err := clipFingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Errorf("同大小同时间戳、内容变了一个字节，指纹却没变（%s）—— 标记会误判\"处理过了\"", before)
	}
}

// TestFrameSpanSnapsRealBlackRunToWholeFrames 帧窗口的判据本体：真机那一处黑闪的秒值是 `%.6f`
// 打印值（black_start:5.291667），而那一帧真实 pts 是 127/24＝5.2916666… ⇒ 换帧必须落在这 4 帧上。
//
// 【为什么必须是单位门】夹具里若用 `d=1.0` 这种**二进制精确**的时间戳（旧门就是这么造的），
// 窗口边界恰好落在帧上，"最黑那一帧被漏掉"根本不会现形 —— 真机用的是 127/24 这种循环小数才逼得出来。
func TestFrameSpanSnapsRealBlackRunToWholeFrames(t *testing.T) {
	cases := []struct {
		name string
		run  BlackRun
		fps  float64
		i0   int
		i1   int
		ok   bool
	}{
		// 真机 2026-10-10《星纪山海：我在废土养神兽》第01集镜 1（24fps）
		{"真机镜1黑段", BlackRun{5.291667, 5.458333}, 24, 127, 130, true},
		// 起点被舍到偏大/偏小 1e-6 秒（同型误差的另一侧）都不许漏帧、也不许多吃帧
		{"起点略偏大", BlackRun{5.291668, 5.458333}, 24, 127, 130, true},
		{"起点略偏小", BlackRun{5.291666, 5.458333}, 24, 127, 130, true},
		// 真机 2026-10-08《顺其自然》那一集的另外两条
		{"真机片头黑", BlackRun{0, 0.208333}, 24, 0, 4, true},
		{"真机镜13黑", BlackRun{2.416667, 2.541667}, 24, 58, 60, true},
		// 短到落在同一帧里 ⇒ 丢帧丢不出这一段
		{"同一帧内", BlackRun{1.0, 1.004}, 24, 0, 0, false},
	}
	for _, c := range cases {
		i0, i1, ok := frameSpan(c.run, c.fps)
		if ok != c.ok || (ok && (i0 != c.i0 || i1 != c.i1)) {
			t.Errorf("%s：%v ⇒ 期望 (%d,%d,%v)，实得 (%d,%d,%v)", c.name, c.run, c.i0, c.i1, c.ok, i0, i1, ok)
		}
	}
	// fps<=0 ＝ 调用方没给（stages_render 传的就是 0）⇒ 按缺省帧率换帧
	if i0, i1, ok := frameSpan(BlackRun{5.291667, 5.458333}, 0); !ok || i0 != 127 || i1 != 130 {
		t.Errorf("fps=0 应按缺省 %.0ffps 换帧，实得 (%d,%d,%v)", defaultClipFPS, i0, i1, ok)
	}
}

// TestStripBlackFlashCutsRealMachineBlackRun 端到端（真机形态）：黑段起点落在 127/24（日志打印成
// 5.291667）上时，**4 帧全黑必须一帧不剩地剪掉**，而且"声称剪掉的秒数"必须等于画面**实际少掉的秒数**
// （音轨按同一批帧切：少切一帧 ＝ 画面还在黑，多切一帧 ＝ 音画错位）。
//
// 【夹具为什么按帧数造】旧门用 `color=...:d=1.0` 造黑段 —— 那段时间戳是二进制精确值、窗口边界恰好落在帧上，
// 所以"最黑那一帧漏剪"永远不现形；真机是 127/24＝5.2916666…（循环小数）才把它逼出来。
func TestStripBlackFlashCutsRealMachineBlackRun(t *testing.T) {
	f := ffexe(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "real.mp4")
	// 24fps：灰 127 帧 → 纯黑 4 帧 → 灰 61 帧 ＝ 192 帧 / 8.000s；带 48k 音轨（音画要一起量）。
	args := []string{"-y", "-v", "error",
		"-f", "lavfi", "-i", "color=c=gray:s=320x240:r=24:d=20",
		"-f", "lavfi", "-i", "color=c=black:s=320x240:r=24:d=20",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:d=20",
		"-filter_complex", "[0:v]trim=end_frame=127,setpts=PTS-STARTPTS[a];" +
			"[1:v]trim=end_frame=4,setpts=PTS-STARTPTS[b];" +
			"[0:v]trim=start_frame=127:end_frame=188,setpts=PTS-STARTPTS[c];" +
			"[a][b][c]concat=n=3:v=1:a=0[v]",
		"-map", "[v]", "-map", "2:a", "-t", "8.0",
		"-c:v", "libx264", "-crf", "18", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-ar", "48000", "-ac", "2", p}
	if _, err := f.Run(ctx, args...); err != nil {
		t.Skipf("造夹具失败（%v）", err)
	}
	runs, err := DetectBlackRuns(ctx, f, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("夹具没造出黑段（%v）—— 这条门就等于没跑", runs)
	}
	if runs[0].Start < 5.29 || runs[0].Start > 5.30 {
		t.Fatalf("夹具黑段起点该≈5.2917（真机那一处），实得 %.6f", runs[0].Start)
	}
	adur0 := audioDur(t, p) // 音轨也要量（反向验证咬出来的：只量画面会漏掉"音轨没切/多切"）
	before, err := f.Probe(p)
	if err != nil {
		t.Fatal(err)
	}
	removed, n, _, err := StripBlackFlash(ctx, f, p, 24)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("应剪 1 段，实得 %d", n)
	}
	if want := 4.0 / 24; removed < want-0.005 || removed > want+0.005 {
		t.Errorf("4 帧全黑应剪掉 %.4fs，实得 %.4fs", want, removed)
	}
	if again, err := DetectBlackRuns(ctx, f, p); err != nil || len(again) != 0 {
		t.Errorf("剪完必须一帧黑都不剩，实得 %v（err=%v）—— 改前最黑那一帧会漏在窗口外", again, err)
	}
	after, err := f.Probe(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := before.Duration - after.Duration; math.Abs(got-4.0/24) > 0.5/24 {
		t.Errorf("画面实际少掉的秒数（%.4fs）必须等于声称剪掉的（%.4fs）—— 不等就是虚报",
			got, removed)
	}
	if cut := adur0 - audioDur(t, p); math.Abs(cut-4.0/24) > 0.06 {
		t.Errorf("音轨也必须切掉同一批帧（%.4fs），实得 %.4fs —— 音轨没切/多切都会音画错位", 4.0/24, cut)
	}
}

// TestForceStripBlackFlashIgnoresMarker 存量修复入口：片段带着"已处理过"标记时普通入口不动它，
// Force 入口必须照样把剩下的黑剪掉（cmd/clipfix 全靠这条 —— 它要修的正是"标记有效、黑还在"的片段）。
func TestForceStripBlackFlashIgnoresMarker(t *testing.T) {
	f := ffexe(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "clip.mp4")
	args := []string{"-y", "-v", "error",
		"-f", "lavfi", "-i", "color=c=white:s=320x240:r=24:d=1.0",
		"-f", "lavfi", "-i", "color=c=black:s=320x240:r=24:d=0.25",
		"-f", "lavfi", "-i", "color=c=white:s=320x240:r=24:d=1.75",
		"-filter_complex", "[0:v][1:v][2:v]concat=n=3:v=1:a=0[v]",
		"-map", "[v]", "-c:v", "libx264", "-crf", "18", "-pix_fmt", "yuv420p", p}
	if _, err := f.Run(ctx, args...); err != nil {
		t.Skipf("造夹具失败（%v）", err)
	}
	// 假装"旧版处理过"：标记有效（指纹与文件对得上），黑段其实还在。
	if err := writeClipMarker(p+".noblack", p, "假装旧版处理过"); err != nil {
		t.Fatal(err)
	}
	if _, n, _, err := StripBlackFlash(ctx, f, p, 24); err != nil || n != 0 {
		t.Errorf("带有效标记时普通入口应什么都不做（n=%d, err=%v）", n, err)
	}
	if runs, err := DetectBlackRuns(ctx, f, p); err != nil || len(runs) == 0 {
		t.Fatalf("夹具的黑段这时应当还在（runs=%v, err=%v）", runs, err)
	}
	removed, n, _, err := ForceStripBlackFlash(ctx, f, p, 24)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || removed <= 0 {
		t.Errorf("Force 入口必须绕开标记照剪（n=%d, removed=%.3f）", n, removed)
	}
	if again, err := DetectBlackRuns(ctx, f, p); err != nil || len(again) != 0 {
		t.Errorf("Force 剪完不该还有黑段（%v, err=%v）", again, err)
	}
}

// TestClipFixBypassesProcessedMarkerInSource cmd/clipfix 的存在意义是修**存量**片段，而那些片段带着
// "已处理过"标记 ⇒ 它必须走 Force 入口（走普通入口 ＝ 声称修了、实际一个字节都没动）。
//
// 【2026-10-11 加严】它现在走的是 `ForceStripBlackFlashMax`（多一个"单段上限"参数，供
// `-max-black` 显式放宽用）—— 这条门因此要同时钉两件事：① 走 Force 那条链；② **上限从算好的
// limit 传进去**（不许又写死 0，否则 `-max-black` 是个摆设）。
func TestClipFixBypassesProcessedMarkerInSource(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "cmd", "clipfix", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "compose.ForceStripBlackFlashMax(") {
		t.Error("clipfix 必须走 compose.ForceStripBlackFlashMax：存量片段带着\"已处理过\"标记，普通入口会静默跳过")
	}
	if !strings.Contains(src, "compose.ForceStripBlackFlashMax(ctx, ffm, p, 0, limit)") {
		t.Error("clipfix 没把算好的单段上限（limit）传进去 —— `-max-black` 会变成摆设")
	}
	if strings.Contains(src, "compose.StripBlackFlash(") {
		t.Error("clipfix 不该再调普通入口 compose.StripBlackFlash（它会被标记挡住，等于没修）")
	}
}

// audioDur 量一条视频里音轨的时长（秒）。为什么必须单独量：黑闪切除要让画面与音轨丢掉**同一批帧**，
// 只量画面时长差会把"音轨整段没切 / 多切一帧"放过去 —— 2026-10-10 反向验证当场咬出这条门太软。
func audioDur(t *testing.T, path string) float64 {
	t.Helper()
	ffp, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("本机找不到 ffprobe，跳过音轨时长比对")
	}
	out, err := exec.Command(ffp, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("量音轨时长失败: %v", err)
	}
	s := strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("音轨时长解析失败（%q）: %v", s, err)
	}
	return v
}

// TestForceStripBlackFlashMaxRelaxesOnlyWhenAsked 默认上限一个字不改；显式放宽才剪更长的段。
//
// 【为什么要有（2026-10-11 真机）】真机《星纪山海》第01集的两处**镜首纯黑**是 0.542s（镜 11）与
// 0.583s（镜 15）—— 只比默认上限（0.5 秒）多一点点，按设计不剪（"整段黑可能是计划里写明的暗场"）。
// 这一轮给用户的出口是 `cmd/clipfix -max-black <秒>`（**显式**）：这条门就是钉住
// ① 默认仍不剪 ② 给了上限就真剪掉 ③ 剪完一帧黑都不剩。
//
// 夹具**照真机形态造**：纯黑**头** 13 帧（13/24 ＝ 0.5417s，与真机镜 11 的 `black_start:0
// black_end:0.541667` 一字不差）后面接灰帧。
func TestForceStripBlackFlashMaxRelaxesOnlyWhenAsked(t *testing.T) {
	if MaxBlackRunSec != 0.5 || MaxBlackTotalSec != 1.0 {
		t.Fatalf("平台默认上限被改了：单段 %.2f / 累计 %.2f（真机定的是 0.5 / 1.0 —— 改默认要重跑真机并写进文档）",
			MaxBlackRunSec, MaxBlackTotalSec)
	}
	// 判据面（纯函数）：真机那两段的秒数逐字贴进来。
	for _, c := range []struct {
		name    string
		r       BlackRun
		defOk   bool // 默认上限下会不会剪
		relaxOk bool // 放宽到 0.6 秒后会不会剪
	}{
		{"镜 11 开头 0.5417s", BlackRun{Start: 0, End: 0.541667}, false, true},
		{"镜 15 开头 0.5833s", BlackRun{Start: 0, End: 0.583333}, false, true},
		{"2026-10-10 镜 1 的短黑闪 0.1667s", BlackRun{Start: 5.291667, End: 5.458333}, true, true},
	} {
		def, _ := BlackRunVerdictWith(c.r, 0) // 0 ＝ 平台默认
		if def != c.defOk {
			t.Errorf("%s：默认上限下判成 %v，该是 %v", c.name, def, c.defOk)
		}
		relax, _ := BlackRunVerdictWith(c.r, 0.6)
		if relax != c.relaxOk {
			t.Errorf("%s：放宽到 0.6 秒后判成 %v，该是 %v", c.name, relax, c.relaxOk)
		}
	}

	f := ffexe(t)
	ctx := context.Background()
	build := func(t *testing.T) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "blackhead.mp4")
		args := []string{"-y", "-v", "error",
			"-f", "lavfi", "-i", "color=c=black:s=320x240:r=24:d=20",
			"-f", "lavfi", "-i", "color=c=gray:s=320x240:r=24:d=20",
			"-filter_complex", "[0:v]trim=end_frame=13,setpts=PTS-STARTPTS[a];" +
				"[1:v]trim=end_frame=60,setpts=PTS-STARTPTS[b];" +
				"[a][b]concat=n=2:v=1:a=0[v]",
			"-map", "[v]", "-t", "3.0",
			"-c:v", "libx264", "-crf", "18", "-pix_fmt", "yuv420p", p}
		if _, err := f.Run(ctx, args...); err != nil {
			t.Skipf("造夹具失败（%v）", err)
		}
		runs, err := DetectBlackRuns(ctx, f, p)
		if err != nil || len(runs) != 1 || runs[0].End < 0.50 || runs[0].End > 0.60 {
			t.Fatalf("夹具没造出「镜首 13 帧纯黑」的形态（runs=%v err=%v）—— 这条门等于没跑", runs, err)
		}
		return p
	}

	// ① 默认：不动（黑段还在，且**如实报出**"没剪"）。
	p := build(t)
	removed, n, longRuns, err := ForceStripBlackFlashMax(ctx, f, p, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || n != 0 {
		t.Errorf("默认上限下不该剪（这一段 0.54s > %.2fs），实得剪掉 %d 段 %.4fs", MaxBlackRunSec, n, removed)
	}
	if len(longRuns) != 1 {
		t.Errorf("默认上限下必须**如实报出**这一段没剪，实得 %v", longRuns)
	}
	if again, err := DetectBlackRuns(ctx, f, p); err != nil || len(again) != 1 {
		t.Errorf("默认跑完黑段必须原样还在，实得 %v（err=%v）", again, err)
	}

	// ② 显式放宽到 0.6 秒：真剪掉，且一帧黑都不剩。
	p = build(t)
	removed, n, longRuns, err = ForceStripBlackFlashMax(ctx, f, p, 24, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || removed < 0.5 {
		t.Errorf("放宽到 0.6 秒后该剪掉 1 段（≈0.54s），实得 %d 段 %.4fs（longRuns=%v）", n, removed, longRuns)
	}
	if len(longRuns) != 0 {
		t.Errorf("这一段按 0.6 秒的上限算**已经被剪了**，不该再报成\"没剪的长黑段\"：%v", longRuns)
	}
	if again, err := DetectBlackRuns(ctx, f, p); err != nil || len(again) != 0 {
		t.Errorf("放宽剪完必须一帧黑都不剩，实得 %v（err=%v）", again, err)
	}
}
