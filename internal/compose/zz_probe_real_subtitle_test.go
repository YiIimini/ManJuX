package compose

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 真机探针（2026-10-09 用户：「1. 对话的字幕显示存在延迟；2. 分镜直接拼接画面很明显」）。
//
// 盘上那一集（《我捡的团子·第01集》）的产物齐着：`plan/plan.json`、`clips/*.asr.txt`、`output/final.srt`。
// 这个探针**不重编视频**，只把**新的落点逻辑**跑在真机上，与旧 `final.srt` 逐条对照 ——
// 也就是"这次改完，真机那一集的字幕会落在哪儿"。盘上没有这一集时跳过（不进八门的必然路径）。
func TestZZProbeRealSubtitleTiming(t *testing.T) {
	dir := `D:\Ai\Datas\ManJuX\data\projects\我捡的团子，暖了三千人的船\我捡的团子，暖了三千人的船·第01集·霜里刨出个活物`
	if _, err := os.Stat(dir); err != nil {
		t.Skip("盘上没有这一集，跳过真机探针")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "plan", "plan.json"))
	if err != nil {
		t.Skipf("读不到 plan.json：%v", err)
	}
	var plan project.Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("真机 plan.json 解析失败：%v", err)
	}

	// 时长与 ASR 都从剪辑侧车文件取（`# ... dur=7.9s`），不重编、不调 ffprobe。
	durations := make([]float64, len(plan.Shots))
	asrSpans := make([][]cueSpan, len(plan.Shots))
	asrWords := make([][]asr.Word, len(plan.Shots))
	for i, sh := range plan.Shots {
		clip := filepath.Join(dir, "clips", "shot_"+pad3(sh.ID)+".mp4")
		durations[i] = durFromASRHeader(clip)
		asrSpans[i], asrWords[i] = asrSpansOf(clip)
	}
	fades := seamFades(transFade, durations, 0.5, sceneSeamFlags(&plan))
	starts := shotStarts(durations, fades)

	// 真机那次包络这一档对每镜都退化了（这正是"字幕铺满整镜"的原因），所以这里显式给 nil。
	tracks := make([]speechTrack, len(plan.Shots))
	for i := range tracks {
		tracks[i] = speechTrack{From: 0, To: durations[i]}
	}
	cp := planCues(&plan, starts, durations, tracks, asrSpans, asrWords)

	t.Logf("落点播报：\n  %s", strings.Join(cp.notes, "\n  "))
	t.Logf("接缝：同场 %d 处硬切（1 帧）／换场 %d 处叠化 —— fades=%v",
		countSameSceneSeams(sceneSeamFlags(&plan)), len(fades)-countSameSceneSeams(sceneSeamFlags(&plan)), fades)

	// 与旧 SRT 逐条对比：旧的第 1 条是 0.837→7.037（挂 6.2 秒），新的应贴在 ASR 的 3.50–4.50 上。
	for i, sh := range plan.Shots {
		parts := shotSubtitleParts(sh)
		if len(parts) == 0 {
			continue
		}
		got := cp.shotSpans(i, starts[i], durations[i], len(parts))
		old := fallbackCues(starts[i], durations[i], len(parts))
		t.Logf("镜 %2d  ASR 段=%v\n        新落点=%s\n        旧落点=%s（镜头级窗口）",
			sh.ID, asrSpans[i], cueText(got), cueText(old))
	}
	first := cp.shotSpans(0, starts[0], durations[0], len(shotSubtitleParts(plan.Shots[0])))
	if len(first) == 0 {
		t.Fatal("镜 1 应该有字幕落点")
	}
	if first[0].Start < 3.3 || first[0].End > 4.8 {
		t.Errorf("镜 1 的字幕该贴在 ASR 的 3.50–4.50 上（旧行为是 0.84–7.04），实际 %.2f–%.2f",
			first[0].Start, first[0].End)
	}
}

func pad3(n int) string {
	return fmt.Sprintf("%03d", n)
}

// durFromASRHeader 从 `<clip>.asr.txt` 的抬头取时长（`# model=medium lang=zh prob=1.00 dur=7.9s segs=1`）。
func durFromASRHeader(clip string) float64 {
	b, err := os.ReadFile(strings.TrimSuffix(clip, filepath.Ext(clip)) + ".asr.txt")
	if err != nil {
		return 0
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if i := strings.Index(ln, "dur="); i >= 0 {
			s := ln[i+4:]
			if j := strings.IndexAny(s, "s "); j > 0 {
				if d, err := time.ParseDuration(strings.TrimSpace(s[:j]) + "s"); err == nil {
					return d.Seconds()
				}
			}
		}
	}
	return 0
}

// shotSubtitleParts 与 director.ShotSubtitleParts 同义（这里只要条数，避免多引一个包）。
func shotSubtitleParts(sh project.Shot) []string {
	var out []string
	for _, l := range sh.Dialogue {
		if t := strings.TrimSpace(l.Text); t != "" {
			out = append(out, t)
		}
	}
	if n := strings.TrimSpace(sh.Narration); n != "" {
		out = append(out, n)
	}
	return out
}

func cueText(spans []cueSpan) string {
	var b strings.Builder
	for i, s := range spans {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(fmt.Sprintf("%.2f", s.Start))
		b.WriteString("→")
		b.WriteString(fmt.Sprintf("%.2f", s.End))
	}
	return b.String()
}
