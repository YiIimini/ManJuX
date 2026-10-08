package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	if got := strippableBlackRuns(short, maxBlackRunSec, maxBlackTotalSec); len(got) != 3 {
		t.Errorf("真机那三段短黑都该剪，实得 %d 段：%v", len(got), got)
	}
	// 长黑段不剪（真机那种"整镜黑/暗场"必须留给质检报，不能在这里悄悄改）
	long := []BlackRun{{0, 2.0}, {5.0, 6.5}}
	if got := strippableBlackRuns(long, maxBlackRunSec, maxBlackTotalSec); len(got) != 0 {
		t.Errorf("长黑段不该剪，实得 %v", got)
	}
	// 累计上限：4 段 0.4s 只剪前两段（0.8s ≤ 1.0s）
	many := []BlackRun{{0, 0.4}, {2, 2.4}, {4, 4.4}, {6, 6.4}}
	if got := strippableBlackRuns(many, maxBlackRunSec, maxBlackTotalSec); len(got) != 2 {
		t.Errorf("累计上限 1.0s 内只该剪 2 段，实得 %d：%v", len(got), got)
	}
	// 一帧级噪声（<0.02s）不动
	if got := strippableBlackRuns([]BlackRun{{1.0, 1.01}}, maxBlackRunSec, maxBlackTotalSec); len(got) != 0 {
		t.Errorf("一帧级噪声不该动：%v", got)
	}
	// 混合：短的剪、长的留着
	mixed := []BlackRun{{0, 0.2}, {3.0, 6.0}, {7.0, 7.13}}
	if got := strippableBlackRuns(mixed, maxBlackRunSec, maxBlackTotalSec); len(got) != 2 {
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
