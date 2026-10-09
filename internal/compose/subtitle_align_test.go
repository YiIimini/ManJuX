package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/project"
)

// ⓪ 档（词级对齐）的**接线判据**：夹具是**真机**那份词级转写原文
// （《被开除一年后，我回公司送水·第01集》镜 10 / 镜 12，逐字拷进 testdata/）。
//
// 【这一档补的是什么（2026-10-09 用户：「看看全网上有没有最优的解决方案？」）】成片语音由 H3
// 音画同生生出，**没有现成时间戳**；业界标准做法是 forced alignment（WhisperX / stable-ts）。
// 落到我们这套链路上就是：whisper 给**词级时间戳**（工具升级后写在 `<clip>.asr.txt` 里），
// 平台把**已知台词**逐句对到词上。它比②强的正是真机那种形态 —— 镜 10 台词 2 句、ASR 也是 2 段，
// 但段的边界与句的边界不重合（4.18-6.30 其实是第一句「工牌呢」），②只能按段贴、于是错位。
func writeSidecar(t *testing.T, dir, name, from string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", from))
	if err != nil {
		t.Fatalf("读不到夹具 %s：%v", from, err)
	}
	clip := filepath.Join(dir, name+".mp4")
	if err := os.WriteFile(filepath.Join(dir, name+".asr.txt"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return clip
}

// 内心镜那种形态（先给最简单的单镜方案）：词级可用 ⇒ 走 cueAlign，落点钉在词边界上。
func TestPlanCuesUsesWordAlignment(t *testing.T) {
	dir := t.TempDir()
	clip := writeSidecar(t, dir, "shot_010", "asr_shot010_words_real.txt")
	spans, words := asrSpansOf(clip)
	if len(words) == 0 {
		t.Fatal("这一份夹具里该有词级时间戳（没有就说明夹具拷错了）")
	}
	plan := &project.Plan{
		Title:      "T",
		Characters: []project.Character{{ID: "char_a", Name: "甲"}},
		Scenes:     []project.Scene{{ID: "scene_a", Name: "走廊"}},
		Shots: []project.Shot{{
			ID: 10, Scene: "scene_a", Characters: []string{"char_a"}, Duration: 9,
			Dialogue: []project.Line{{Speaker: "甲", Text: "工牌呢。"}, {Speaker: "甲", Text: "被收了。"}},
		}},
	}
	cp := planCues(plan, []float64{0}, []float64{9},
		[]speechTrack{{From: 0, To: 9}}, [][]cueSpan{spans}, [][]asr.Word{words})
	if len(cp.notes) == 0 || !strings.Contains(strings.Join(cp.notes, "|"), "词级对齐") {
		t.Errorf("用了词级对齐就必须如实播报（notes=%v）", cp.notes)
	}
	got := cp.byShot[0]
	if len(got) != 2 {
		t.Fatalf("两句台词该有两个落点，实际 %+v", got)
	}
	// 落点钉在**词边界**上（4.18–6.30 / 6.30–7.84），带前后留白；相邻两条重叠时
	// clampCues 会把前一条的收尾压回到下一条起点之前 —— 所以这里钉起点、收尾只给区间。
	if got[0].Start < 4.05 || got[0].Start > 4.20 {
		t.Errorf("第 1 句起点该是 4.18 减留白（≈4.10），实际 %.2f", got[0].Start)
	}
	if got[0].End < 6.00 || got[0].End > 6.32 {
		t.Errorf("第 1 句收尾该在 6.30 上下，实际 %.2f", got[0].End)
	}
	if got[1].Start < 6.15 || got[1].Start > 6.32 {
		t.Errorf("第 2 句起点该是 6.30 减留白（≈6.22），实际 %.2f", got[1].Start)
	}
	if got[1].End < 7.80 {
		t.Errorf("第 2 句收尾该到 7.84 上下，实际 %.2f", got[1].End)
	}
	// **核心**：不许退回镜头级窗口（老行为是 0–9 均分 ⇒ 第 1 句会从 0 起）
	if got[0].Start < 1.0 {
		t.Errorf("第 1 句仍从镜头开头起（%.2f）—— 说明没吃词级对齐，又回到"+
			"整镜铺开的老行为", got[0].Start)
	}
}

// 老格式侧车（只有段、没有词）⇒ **绝不**走 ⓪ 档：按段级那档走，行为与升级前一致。
func TestPlanCuesFallsBackWithoutWordTimestamps(t *testing.T) {
	dir := t.TempDir()
	// 手写一份老格式（真机升级前的侧车就是这个形状：只有 `[start-end] 文本`）
	old := "# shot_010.mp4\n# model=medium lang=zh prob=1.00 dur=9.3s segs=2\n" +
		"[  4.18-  6.30] 公牌呢?\n[  6.30-  7.84] 被收了。\n"
	clip := filepath.Join(dir, "shot_010.mp4")
	if err := os.WriteFile(filepath.Join(dir, "shot_010.asr.txt"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	spans, words := asrSpansOf(clip)
	if len(words) != 0 {
		t.Fatalf("老格式文件不该解出词，实得 %+v", words)
	}
	plan := &project.Plan{
		Title:      "T",
		Characters: []project.Character{{ID: "char_a", Name: "甲"}},
		Scenes:     []project.Scene{{ID: "scene_a", Name: "走廊"}},
		Shots: []project.Shot{{
			ID: 10, Scene: "scene_a", Characters: []string{"char_a"}, Duration: 9,
			Dialogue: []project.Line{{Speaker: "甲", Text: "工牌呢。"}, {Speaker: "甲", Text: "被收了。"}},
		}},
	}
	cp := planCues(plan, []float64{0}, []float64{9},
		[]speechTrack{{From: 0, To: 9}}, [][]cueSpan{spans}, [][]asr.Word{words})
	if strings.Contains(strings.Join(cp.notes, "|"), "词级对齐") {
		t.Error("老格式侧车（没有词级时间戳）不该报「词级对齐」")
	}
	// 段数 = 句数 ⇒ 走②（按 ASR 段落）—— 这是升级前的行为，必须原样保留
	if cp.byShot[0][0].Start < 4.0 {
		t.Errorf("该按 ASR 段落在 4.18 附近起，实际 %.2f", cp.byShot[0][0].Start)
	}
}
