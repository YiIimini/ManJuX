package pipeline

import (
	"context"
	"github.com/YiIimini/ManJuX/internal/project"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// placeholder_clip_test.go 失败镜的占位片段（官方 fallback 阶梯第 4 级）。
//
//	"Skip the shot and add a `placeholder: missing clip` note for downstream review."
//
// 平台化落地：**写黑场占位而不是直接跳过** —— 跳过会让成片悄悄变短、后面每一镜的画面与字幕
// 整体前移；占位保住时长与顺序，缺口在成片里看得见、在清单里查得到。
func TestWritePlaceholderClipRealFFmpeg(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机没有 ffmpeg，跳过:%v", err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "shot-07.mp4")
	if err := writePlaceholderClip(context.Background(), f, out, 5); err != nil {
		t.Fatalf("写占位失败:%v", err)
	}
	info, err := f.Probe(out)
	if err != nil {
		t.Fatalf("探测失败:%v", err)
	}
	// 时长必须等于计划时长（这是"保住时间轴"的全部意义）
	if info.Duration < 4.5 || info.Duration > 5.6 {
		t.Errorf("占位时长应为 5 秒左右，实际 %.2f", info.Duration)
	}
	// 必须有音轨：没有的话 compose 会走"缺音轨"兜底，对位判据退化
	if !info.HasAudio {
		t.Error("占位片段必须带静音音轨（否则成片音轨对位判据退化）")
	}
	if info.FPS < 23 || info.FPS > 25 {
		t.Errorf("帧率应与全片同口径(24)，实际 %.2f", info.FPS)
	}
	// 目录不存在时要能自己建
	nested := filepath.Join(dir, "a", "b", "c.mp4")
	if err := writePlaceholderClip(context.Background(), f, nested, 4); err != nil {
		t.Errorf("应自动建目录，实际报错:%v", err)
	}
}

func TestPlaceholderNoteSaysWhy(t *testing.T) {
	n := PlaceholderNote(7, 5, "渲染超时")
	for _, want := range []string{"镜 7", "占位片段", project.PlaceholderMarker, "渲染超时", "悄悄变短", "必须重合成"} {
		if !strings.Contains(n, want) {
			t.Errorf("说明里要说清 %q，实际：%s", want, n)
		}
	}
	if !strings.Contains(PlaceholderNote(7, 5, ""), "渲染失败") {
		t.Error("没给原因时也要有兜底说明")
	}
}

// TestPlaceholderClipHasNoExecLeak 占位片段不能用外部脚本（本机无独立 node 等环境时的纪律）：
// 只允许调用 ffmpeg —— 这条断言防的是"顺手 exec 个 python 脚本"这类引入。
func TestPlaceholderClipOnlyUsesFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机没有 ffmpeg")
	}
	// 行为断言：时长非法时用 4 秒兜底，而不是报错（管线不能因为一个坏时长就断）
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("没有 ffmpeg:%v", err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "zero.mp4")
	if err := writePlaceholderClip(context.Background(), f, out, 0); err != nil {
		t.Fatalf("时长 0 应兜底为 4 秒，实际报错:%v", err)
	}
	info, _ := f.Probe(out)
	if info.Duration < 3.5 || info.Duration > 4.6 {
		t.Errorf("兜底时长应约 4 秒，实际 %.2f", info.Duration)
	}
}
