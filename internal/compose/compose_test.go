package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// 端到端合成测试:本机没有 ffmpeg 就跳过(合成本来就依赖它),有就跑真链路。
//
// 为什么值得占这个位置:叠化转场的音频链是本轮改动里最容易"看着对、跑起来错"的一段
// —— apad=whole_dur + atrim + 按静音求解的切点 + concat + 两遍 loudnorm + flac 中间轨,
// 它同时受"长度必须精确对齐画面""滤镜名/参数必须在真 ffmpeg 上成立"两条约束。
// 只靠读代码判断不了,必须真跑一遍。
func TestComposeEndToEndAudioAlignsToVideo(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过端到端合成测试: %v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()

	// 3 个 4 秒镜头:画面是测试图,音频只有前 1.5 秒有声、之后留白
	// (刻意让音轨短于画面 —— 这正是 H3 偶发的情况,规范化那步必须补到等长)。
	clips := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, "clip"+string(rune('a'+i))+".mp4")
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc=size=320x192:rate=24:duration=4",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=1.5",
			"-map", "0:v", "-map", "1:a",
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "128k", "-ac", "2", p); err != nil {
			t.Fatalf("造测试镜头失败: %v", err)
		}
		clips = append(clips, p)
	}

	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 4, Dialogue: []project.Line{{Speaker: "甲", Text: "第一句台词"}}},
		{ID: 2, Duration: 4, Dialogue: []project.Line{{Speaker: "甲", Text: "第二句台词"}}},
		{ID: 3, Duration: 4, Dialogue: []project.Line{{Speaker: "甲", Text: "第三句台词"}}},
	}}
	res, err := Compose(ctx, f, clips, plan, outDir, Params{
		Width: 320, Height: 192, Transition: "fade", BurnSubtitles: true,
		SubtitleSize: 4, Loudness: -16, FadeSec: 0.5, Denoise: true,
	}, nil)
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	if _, err := os.Stat(res.FinalPath); err != nil {
		t.Fatalf("成片不存在: %v", err)
	}
	info, err := f.Probe(res.FinalPath)
	if err != nil {
		t.Fatalf("探测成片失败: %v", err)
	}
	// 叠化 0.5 秒 × 2 个接缝:总长 = 12 - 1 = 11 秒
	if math.Abs(info.Duration-11) > 0.15 {
		t.Errorf("成片时长应约 11 秒(12 - 2×0.5),实际 %.2f", info.Duration)
	}
	if !info.HasAudio {
		t.Fatal("成片必须带音轨(配音是主线)")
	}
	if info.AudioRate != 48000 {
		t.Errorf("音轨采样率应为 48000,实际 %d", info.AudioRate)
	}
	// 画幅:交付档是**按画幅自动**的(见 deliver.go)—— 这里渲染是 320×192(横屏),
	// 未填交付尺寸时按交付标准出 1920×1080,而不是跟随渲染尺寸。这条断言盯的是
	// "默认交付档对不对",而不是这条链的音频对齐(那是下面几行的事)。
	if info.Width != deliverHorizontalW || info.Height != deliverHorizontalH {
		t.Errorf("成片画幅应为交付标准 %dx%d,实际 %dx%d", deliverHorizontalW, deliverHorizontalH, info.Width, info.Height)
	}
	// 音频实况必须留下可读的说明(接缝切点统计 + 响度链是否降级)
	if len(res.AudioNotes) == 0 {
		t.Error("应回传音频处理实况(接缝切点/响度链),否则用户无法判断这次到底修没修到")
	}
	notes := ""
	for _, n := range res.AudioNotes {
		notes += n + "|"
		t.Logf("audio note: %s", n)
	}
	// 造的数据里"有声音"的只有每个镜头前 1.5 秒 —— 两个接缝的让出区间因此都能
	// 完全落在静音里(切点贴到窗口一端),这正是"不切在字上"的可验证判据。
	if !strings.Contains(notes, "接缝切点:2/2 处完全落在静音") {
		t.Errorf("两个接缝都应判为落在静音(有声段只有每镜前 1.5 秒),实际:%s", notes)
	}
}

// TestComposeCutKeepsFullAudio 硬切路径只重编码音频、不裁剪 —— 每个镜头的话都必须是完整的。
func TestComposeCutKeepsFullAudio(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过: %v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()
	clips := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		p := filepath.Join(dir, "c"+string(rune('a'+i))+".mp4")
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc=size=320x192:rate=24:duration=3",
			"-f", "lavfi", "-i", "sine=frequency=330:duration=3",
			"-map", "0:v", "-map", "1:a", "-shortest",
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "128k", "-ac", "2", p); err != nil {
			t.Fatalf("造测试镜头失败: %v", err)
		}
		clips = append(clips, p)
	}
	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)
	res, err := Compose(ctx, f, clips, &project.Plan{}, outDir, Params{
		Width: 320, Height: 192, Transition: "cut", BurnSubtitles: false,
		Loudness: -16, FadeSec: 0.5, Denoise: false,
	}, nil)
	if err != nil {
		t.Fatalf("硬切合成失败: %v", err)
	}
	info, err := f.Probe(res.FinalPath)
	if err != nil {
		t.Fatalf("探测成片失败: %v", err)
	}
	if math.Abs(info.Duration-6) > 0.15 {
		t.Errorf("硬切不应重叠,总长应约 6 秒,实际 %.2f", info.Duration)
	}
	if !info.HasAudio {
		t.Fatal("硬切路径同样必须保留音轨")
	}
}

// TestComposeIllegalTransitionStaysCutAndKeepsSubtitlesAligned 非法转场值(手改
// JSON / 技能端导入的局部参数包)必须**整条链**都按硬切走(审计:转场两套判据)。
//
// 旧实现:字幕与总时长按 `trans != "cut"` 扣重叠、画面按 `switch Transition{case "fade"}`
// 走硬切 —— 于是成片实际是 9 秒硬切,界面却报 8 秒、字幕还整体提前了 1 秒。
func TestComposeIllegalTransitionStaysCutAndKeepsSubtitlesAligned(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过: %v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()
	clips := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, "c"+string(rune('a'+i))+".mp4")
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc=size=320x192:rate=24:duration=3",
			"-f", "lavfi", "-i", "sine=frequency=330:duration=3",
			"-map", "0:v", "-map", "1:a", "-shortest",
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "128k", "-ac", "2", p); err != nil {
			t.Fatalf("造测试镜头失败: %v", err)
		}
		clips = append(clips, p)
	}
	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 3, Dialogue: []project.Line{{Speaker: "甲", Text: "一"}}},
		{ID: 2, Duration: 3, Dialogue: []project.Line{{Speaker: "甲", Text: "二"}}},
		{ID: 3, Duration: 3, Dialogue: []project.Line{{Speaker: "甲", Text: "三"}}},
	}}
	res, err := Compose(ctx, f, clips, plan, outDir, Params{
		Width: 320, Height: 192, Transition: "wipe", BurnSubtitles: false,
		Loudness: -16, FadeSec: 0.5, Denoise: false,
	}, nil)
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	// 非法值 → 硬切:成片就是 9 秒,没有重叠可扣
	if math.Abs(res.Duration-9) > 0.2 {
		t.Errorf("非法转场值应按硬切出片,总长应约 9 秒,实际 %.2f", res.Duration)
	}
	// 字幕:第三镜必须从 6 秒前后开始(按叠化算会early到 5 秒 —— 那正是旧缺陷)
	srt, err := os.ReadFile(res.SRTPath)
	if err != nil {
		t.Fatalf("读字幕失败: %v", err)
	}
	lines := strings.Split(string(srt), "\n")
	var third string
	cue := 0
	for _, ln := range lines {
		if strings.Contains(ln, " --> ") {
			cue++
			if cue == 3 {
				third = ln
				break
			}
		}
	}
	if third == "" {
		t.Fatalf("字幕里找不到第三条时间轴,实际:\n%s", string(srt))
	}
	start := srtSeconds(t, strings.TrimSpace(strings.Split(third, "-->")[0]))
	if start < 5.9 || start > 6.6 {
		t.Errorf("第三镜字幕应从约 6.24s 开始(镜头起点 6s + 8%%),实际 %.2fs —— 按叠化算会漂到 5 秒档", start)
	}
}

// TestComposeFadeShortShotKeepsAudioInSync 短镜 + 长转场:音轨与画面必须等长
// (审计:"整个接缝不让"的三套判据之一 —— 音轨保持整段、画面照样叠,音画从此错位)。
func TestComposeFadeShortShotKeepsAudioInSync(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过: %v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()
	// 中间一镜只有 0.7 秒,而转场要 2 秒 —— 长于半镜的病态组合
	durs := []float64{4, 0.7, 4}
	clips := make([]string, 0, 3)
	for i, d := range durs {
		p := filepath.Join(dir, "s"+string(rune('a'+i))+".mp4")
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=320x192:rate=24:duration=%.3f", d),
			"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=330:duration=%.3f", d),
			"-map", "0:v", "-map", "1:a", "-shortest",
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "128k", "-ac", "2", p); err != nil {
			t.Fatalf("造测试镜头失败: %v", err)
		}
		clips = append(clips, p)
	}
	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)
	res, err := Compose(ctx, f, clips, &project.Plan{}, outDir, Params{
		Width: 320, Height: 192, Transition: "fade", BurnSubtitles: false,
		Loudness: -16, FadeSec: 2.0, Denoise: false,
	}, nil)
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	if _, err := os.Stat(res.FinalPath); err != nil {
		t.Fatalf("成片未落盘: %v", err)
	}
	video, audio := streamDurations(t, f, res.FinalPath)
	if math.Abs(video-audio) > 0.12 {
		t.Errorf("音轨必须与画面等长(短镜接缝也不能例外):画面 %.3fs / 音轨 %.3fs", video, audio)
	}
	if math.Abs(res.Duration-video) > 0.12 {
		t.Errorf("回报的成片时长 %.3fs 与实际画面 %.3fs 不符", res.Duration, video)
	}
}

// TestComposeDeliversAtStandardSize 交付层的端到端契约(红果/抖音对标 G1)。
//
// 一条真实链路要同时满足四件事,缺任何一件都是"看着成功、交付不合格":
//  1. **成片画幅 = 交付档**,不是渲染尺寸(H3 的短边上限是 768,交付口径是 1080×1920);
//  2. **字幕按交付画幅排版**:ASS 的 PlayRes 与 MarginV 吃的是交付高度 ——
//     否则 1080 的画面上会贴着一套按 1344 算出来的字号与安全区;
//  3. **下边距避开平台底部 UI 安全区**(旧实现写死 8%,压在平台 UI 里);
//  4. **成片不带镜头文件继承来的容器元数据**(ComfyUI 的整份工作流 JSON)。
func TestComposeDeliversAtStandardSize(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过: %v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()
	clip := filepath.Join(dir, "clip.mp4")
	// 渲染尺寸取项目的出厂竖幅 768×1344 的缩小版(320×560,同比例),免得测试白等编码。
	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x560:rate=24:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-map", "0:v", "-map", "1:a", "-shortest",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-ac", "2", clip); err != nil {
		t.Fatalf("造测试镜头失败: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 3, Dialogue: []project.Line{{Speaker: "甲", Text: "交付画幅要对"}}},
	}}
	var notes []string
	// 只给渲染尺寸,**不给**交付尺寸 —— 走"按画幅自动"的默认档(1080×1920)。
	res, err := Compose(ctx, f, []string{clip}, plan, outDir, Params{
		Width: 320, Height: 560, Transition: "cut", BurnSubtitles: true,
		SubtitleSize: 4, Loudness: -16, Grade: GradeOff,
	}, func(_ int, n string) { notes = append(notes, n) })
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	info, err := f.Probe(res.FinalPath)
	if err != nil {
		t.Fatalf("探测成片失败: %v", err)
	}
	if info.Width != deliverVerticalW || info.Height != deliverVerticalH {
		t.Fatalf("竖屏项目应交付 %dx%d,实际 %dx%d", deliverVerticalW, deliverVerticalH, info.Width, info.Height)
	}
	// 交付这一步必须留痕(静默放大等于用户不知道成片被重采样过)
	if joined := strings.Join(notes, "|"); !strings.Contains(joined, "交付") {
		t.Errorf("交付分辨率必须是如实播报的一部分,实际进度文案:%q", joined)
	}

	ass, err := os.ReadFile(filepath.Join(outDir, "final.ass"))
	if err != nil {
		t.Fatalf("读 final.ass 失败: %v", err)
	}
	text := string(ass)
	if !strings.Contains(text, fmt.Sprintf("PlayResX: %d", deliverVerticalW)) ||
		!strings.Contains(text, fmt.Sprintf("PlayResY: %d", deliverVerticalH)) {
		t.Errorf("字幕必须按**交付画幅**排版(PlayRes 应为 %dx%d):\n%s",
			deliverVerticalW, deliverVerticalH, firstLines(text, 8))
	}
	// 下边距 = 交付高度 × 15%(安全区下限);旧值 8% 正是"压在平台 UI 里"的那一版
	wantMargin := deliverVerticalH * subtitleMarginVPctDefault / 100
	if !strings.Contains(text, fmt.Sprintf(",%d,%d,%d,1", deliverVerticalW*5/100, deliverVerticalW*5/100, wantMargin)) {
		t.Errorf("字幕下边距应为交付高度的 %d%%(=%dpx),实际样式行:%s",
			subtitleMarginVPctDefault, wantMargin, styleLine(text))
	}

	// 导出卫生:成片里不该再带着 ComfyUI 的工作流 JSON
	tags := formatTags(t, f, res.FinalPath)
	if tags != "" && strings.Contains(tags, "prompt") {
		t.Errorf("成片容器元数据里不该留着镜头继承来的 prompt 标签(那是整份工作流 JSON):%s", tags)
	}
}

// formatTags 读成片的容器级元数据(没有则空串)。
func formatTags(t *testing.T, f *ff.FF, path string) string {
	t.Helper()
	ctx := context.Background()
	raw, err := sysexec.Hide(exec.CommandContext(ctx, f.Ffprobe,
		"-v", "error", "-show_entries", "format_tags", "-of", "json", path)).Output()
	if err != nil {
		t.Fatalf("读元数据失败: %v", err)
	}
	return string(raw)
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func styleLine(ass string) string {
	for _, l := range strings.Split(ass, "\n") {
		if strings.HasPrefix(l, "Style: Dialogue") {
			return l
		}
	}
	return "(找不到 Style 行)"
}

// TestComposeAnnouncesAspectMismatch L13:输入镜头的画幅与成片规格不一致时,
// 规范化的 scale+pad 会把差异静默兜成"补黑边" —— 成片里混着带边的镜头,
// 日志里却一个字都没有。现在必须在进度文案里如实播报。
func TestComposeAnnouncesAspectMismatch(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过: %v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()
	// 16:9 的输入,成片规格是 9:16 —— 必然要补黑边
	wide := filepath.Join(dir, "wide.mp4")
	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=24:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-map", "0:v", "-map", "1:a", "-shortest",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-ac", "2", wide); err != nil {
		t.Fatalf("造测试镜头失败: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)
	var notes []string
	if _, err := Compose(ctx, f, []string{wide}, &project.Plan{}, outDir, Params{
		Width: 320, Height: 640, Transition: "cut", BurnSubtitles: false,
		Loudness: -16, FadeSec: 0.5, Denoise: false,
	}, func(_ int, n string) { notes = append(notes, n) }); err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	joined := strings.Join(notes, "|")
	if !strings.Contains(joined, "画幅") || !strings.Contains(joined, "640×360") {
		t.Errorf("画幅不一致(640×360 → 320×640,会补黑边)必须如实播报,实际进度文案:%q", joined)
	}
}

// TestFinalMuxCopiesVideoWhenNotBurningSubtitles M35:不烧字幕那一路对视频必须流复制。
//
// 这是一条**源码级**契约断言(而非行为断言):`-c:v copy` 的效果是"最终视频流与
// master 逐帧一致",而 master 落在 Compose 内部临时目录、成功即清理,测试无法取到
// 它去比 framemd5;在测试里复刻一遍规范化命令又会让断言跟着实现漂。
// 判据(2026-09-26 甲1 调色档落地后更新):终混只有**一条**"零滤镜"路 ——
// 既没烧字幕、又没开调色(needEncode=false)时才允许 -c:v copy。调色与字幕都是
// 视频滤镜,任一存在就必须重编码;而"要不要重编码"只由 needEncode 一处判定。
// 所以这里钉住判据本身:零滤镜那一路的视频只能 copy —— 想改回无脑重编码就得先改这条断言。
func TestFinalMuxCopiesVideoWhenNotBurningSubtitles(t *testing.T) {
	raw, err := os.ReadFile("compose.go")
	if err != nil {
		t.Fatalf("读取 compose.go 失败: %v", err)
	}
	src := string(raw)
	i := strings.Index(src, `finalPath := filepath.Join(outDir, "final.mp4")`)
	if i < 0 {
		t.Fatal("找不到终混段:合成链路的形状变了,这条断言需要跟着更新")
	}
	region := src[i:]
	if j := strings.Index(region, "res.FinalPath = finalPath"); j > 0 {
		region = region[:j]
	}
	if !strings.Contains(region, "if needEncode {") {
		t.Error("重编码与流复制必须由 needEncode(有没有视频滤镜)统一判定,不能各写各的判据")
	}
	copied := strings.Index(region, `"-c:v", "copy"`)
	if copied < 0 {
		t.Error("零滤镜(未烧字幕且未开调色)的终混必须流复制视频(-c:v copy):否则每一镜又白推一代 x264 代次损失")
	}
	// 烧字幕/调色那一支本来就要重编码(它有滤镜),所以只要求:
	// 重编码出现在流复制**之前**(= 只在需要滤镜的分支里),copy 的分支不得再出现。
	if lx := strings.LastIndex(region, `"-c:v", "libx264"`); lx > copied {
		t.Error("零滤镜的终混不该再重编码视频(那一步没有任何滤镜)")
	}
}

// srtSeconds 把 "00:00:06,240" 解析成秒。
func srtSeconds(t *testing.T, s string) float64 {
	t.Helper()
	s = strings.Replace(strings.Replace(s, ",", ".", 1), ":", "", -1)
	// hhmmss.mmm
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("解析字幕时间失败(%q): %v", s, err)
	}
	h := math.Floor(v / 10000)
	m := math.Floor((v - h*10000) / 100)
	sec := v - h*10000 - m*100
	return h*3600 + m*60 + sec
}

// streamDurations 用 ffprobe 读成片里**视频流与音频流各自**的实际时长(秒)。
//
// 为什么不用 ff.Probe:它回报的是容器时长(取较长的那条),而"音轨比画面长"
// 这类缺陷恰恰只体现在两条流各自的时长里 —— 容器时长看起来是"对"的。
func streamDurations(t *testing.T, f *ff.FF, path string) (video, audio float64) {
	t.Helper()
	cmd := sysexec.Hide(exec.Command(f.Ffprobe, "-v", "error", "-print_format", "json",
		"-show_streams", path))
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe 读流信息失败: %v", err)
	}
	var po struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Duration  string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(raw, &po); err != nil {
		t.Fatalf("解析流信息失败: %v", err)
	}
	for _, s := range po.Streams {
		d, _ := strconv.ParseFloat(s.Duration, 64)
		switch s.CodecType {
		case "video":
			video = d
		case "audio":
			audio = d
		}
	}
	return video, audio
}

// TestShotSpeechBudget 台词应有口播时长(质检用它核对成片里到底说没说够)。
func TestShotSpeechBudget(t *testing.T) {
	if got := ShotSpeechBudget(project.Shot{}); got != 0 {
		t.Errorf("无台词应为 0,实际 %.2f", got)
	}
	sh := project.Shot{
		Dialogue:  []project.Line{{Text: "一二三四五"}},
		Narration: "六七八",
	}
	// 期望值从**语速常量**推导（2026-09-28：4.5 → 4.1，依据 H3 公开契约文档"中文约 4.1 可发声字/秒"）：
	// 写死 2.38 秒会让语速一改就假红，而语速本身是**有出处**的口径。
	want := float64(5+3)/project.PlanCharsPerSecond + 2*project.PlanSpeechPadSeconds
	if got := ShotSpeechBudget(sh); math.Abs(got-want) > 0.02 {
		t.Errorf("应有口播时长约 %.2f 秒(语速 %.1f),实际 %.2f", want, project.PlanCharsPerSecond, got)
	}
}

// TestComposeAlignsSubtitleToRealSpeech 乙4 的端到端判据:镜头很长(6 秒)但人声只在
// 第 3~4 秒,字幕就必须落在那里 —— 而不是镜头级窗口(6 秒镜头按 10% 内缩,从 0.6 秒起)。
//
// 为什么值得真跑一遍:这条链路跨了四个环节(规范化后的 part → 解码包络 →
// 由拼接函数交出的可听区间 → 字幕落点),任何一处接错都会静默退回镜头级窗口
// (代码里到处是"对不上就回退"),而回退不报错、成片照样出 —— 只有真跑能钉住。
func TestComposeAlignsSubtitleToRealSpeech(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过字幕落点端到端测试: %v", err)
	}
	dir := t.TempDir()
	ctx := context.Background()

	// 6 秒画面 + 只在第 3 秒起的 1 秒人声(其余静音)
	clip := filepath.Join(dir, "clip.mp4")
	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x192:rate=24:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-filter_complex", "[1:a]adelay=3000|3000,apad[a]",
		"-map", "0:v", "-map", "[a]", "-t", "6",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-ac", "2", clip); err != nil {
		t.Fatalf("造测试镜头失败: %v", err)
	}

	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o755)
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 6, Dialogue: []project.Line{{Speaker: "甲", Text: "这一句在第三秒"}}},
	}}
	res, err := Compose(ctx, f, []string{clip}, plan, outDir, Params{
		Width: 320, Height: 192, Transition: "cut", BurnSubtitles: true,
		SubtitleSize: 4, Loudness: -16, Grade: GradeOff,
	}, nil)
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	notes := strings.Join(res.AudioNotes, "|")
	if !strings.Contains(notes, "1/1 镜按**人声包络**逐句对齐") {
		t.Errorf("应如实回显「按人声包络逐句对齐」,实际播报:%q", notes)
	}

	raw, err := os.ReadFile(res.SRTPath)
	if err != nil {
		t.Fatalf("读 SRT 失败: %v", err)
	}

	// 色彩口径(乙3)端到端:源片未标记 → 成品必须被**真的**标成 bt709
	// (只在参数里写、落不到成品 colr/VUI 上,等于没写 —— 播放器照旧猜)
	cs, err := probeClipColor(ctx, f, res.FinalPath)
	if err != nil {
		t.Fatalf("探测成片色彩失败: %v", err)
	}
	if cs.Space != "bt709" || cs.Transfer != "bt709" || cs.Primaries != "bt709" {
		t.Errorf("未标记的源片应被标成 bt709 并落到成品上,实际 %+v", cs)
	}

	cue, err := srtWindow(t, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	// 人声段 [3.0, 4.0] ± 前后余量(0.08 / 0.15),SRT 再各收 50ms
	if cue[0] < 2.5 || cue[0] > 3.5 {
		t.Errorf("字幕起点应贴在第 3 秒的人声上,实际 %.2f(镜头级窗口是 0.6)", cue[0])
	}
	if cue[1] < 3.8 || cue[1] > 5.0 {
		t.Errorf("字幕终点应贴在人声收尾附近(约 4.1),实际 %.2f", cue[1])
	}
	// 反证:老窗口是 [0.6, 5.4],起点差着 2 秒多
	if math.Abs(cue[0]-0.6) < 0.3 {
		t.Error("落点仍停在镜头级窗口上 —— 真实人声没有接进字幕链")
	}
}
