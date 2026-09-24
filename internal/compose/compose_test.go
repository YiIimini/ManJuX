package compose

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
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
