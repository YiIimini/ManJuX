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
	if info.Width != 320 || info.Height != 192 {
		t.Errorf("成片画幅应为 320x192,实际 %dx%d", info.Width, info.Height)
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
// 所以这里钉住判据本身:烧字幕那条路(带 subtitles= 滤镜)之后**不准**再出现
// libx264,视频只能 copy —— 想改回重编码就得先改这条断言。
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
	copied := strings.Index(region, `"-c:v", "copy"`)
	if copied < 0 {
		t.Error("不烧字幕的终混必须流复制视频(-c:v copy):否则每一镜又白推一代 x264 代次损失")
	}
	// 烧字幕那一支本来就要重编码(它有 subtitles= 滤镜),所以只要求:
	// 重编码出现在流复制**之前**(= 只在烧字幕分支里),不烧字幕的分支不得再出现。
	if lx := strings.LastIndex(region, `"-c:v", "libx264"`); lx > copied {
		t.Error("不烧字幕的终混不该再重编码视频(视频在这一步没有任何滤镜)")
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
	// (5+3)/4.5 + 2×0.3 = 2.38
	if got := ShotSpeechBudget(sh); math.Abs(got-2.38) > 0.02 {
		t.Errorf("应有口播时长约 2.38 秒,实际 %.2f", got)
	}
}
