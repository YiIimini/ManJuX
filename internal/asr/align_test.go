package asr

import (
	"math"
	"path/filepath"
	"testing"
)

// 真机夹具（2026-10-09，用户：「看看全网上有没有最优的解决方案？」）：
// 《被开除一年后，我回公司送水·第01集》镜 10 / 镜 12 的**词级转写原文**逐字拷进来的，
// 是升级后的 `D:\Ai\Tools\ASR\transcribe.py`（`word_timestamps=True`）真跑出来的。
//
// 为什么必须是真机这两镜：它们是"段级判据判不了"的那种形态 —— 台词 2 句、ASR 也是 2 段，
// 可段的边界与句的边界**并不重合**，而镜 10 的第一句还被听成同音错字（工牌 → 公牌）。
func realWords(t *testing.T, name string) *Result {
	t.Helper()
	r, err := Parse(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读不到夹具 %s：%v", name, err)
	}
	return r
}

// 词级行必须解出来（数字照日志原文断言，避免"解了个大概"）。
func TestParseWordsReal(t *testing.T) {
	r := realWords(t, "asr_shot010_words_real.txt")
	if len(r.Words) != 6 {
		t.Fatalf("镜 10 该解到 6 个词，实际 %d（%+v）", len(r.Words), r.Words)
	}
	want := []Word{
		{Start: 4.18, End: 4.78, Text: "公"},
		{Start: 4.78, End: 4.92, Text: "牌"},
		{Start: 4.92, End: 6.30, Text: "呢?"},
		{Start: 6.30, End: 7.08, Text: "被"},
		{Start: 7.08, End: 7.26, Text: "收"},
		{Start: 7.26, End: 7.84, Text: "了。"},
	}
	for i, w := range want {
		got := r.Words[i]
		if math.Abs(got.Start-w.Start) > 1e-6 || math.Abs(got.End-w.End) > 1e-6 || got.Text != w.Text {
			t.Errorf("第 %d 个词该是 %+v，实际 %+v", i+1, w, got)
		}
	}
	// 段级行照样在（同一个文件里两段，老解析器不认识的 `w` 行不能挤掉它）
	if len(r.Segments) != 2 || r.Segments[0].Text != "公牌呢?" {
		t.Errorf("段级行该照旧解析出来，实际 %+v", r.Segments)
	}
	// 词不该被误当成段（`w ` 前缀与 `[...]` 是两套）
	for _, s := range r.Segments {
		if len([]rune(s.Text)) == 1 {
			t.Errorf("词级行被当成段解析了：%+v", s)
		}
	}
}

// **向后兼容**：升级前的转写文件（只有段、没有词）必须照旧可用，Words 为空。
//
// 这条是本轮最要紧的兼容判据：工具升级是**单向**的（老项目盘上全是老文件），
// 上层要靠 `len(Words)==0` 自动退回段级落点，不许崩、不许报错。
func TestParseOldFormatStillWorks(t *testing.T) {
	r := realWords(t, "shot_010.asr.txt")
	if len(r.Segments) == 0 {
		t.Fatal("老格式文件的段级行必须照旧解析出来")
	}
	if len(r.Words) != 0 {
		t.Errorf("老格式文件里没有词级段，Words 该为空，实际 %v", r.Words)
	}
}

// 真机镜 10：两句台词的落点必须落在**各自那句**的词上（而不是整段/整镜）。
//
// 现状对照：这一集成片里第 1 句字幕挂在 76.23–78.11s，而实测人声在 77.78–81.78s
// （体检原话「字幕比声音早出」）—— 差的就是"句边界 vs 段边界"这件事。
func TestAlignLinesRealShot010(t *testing.T) {
	r := realWords(t, "asr_shot010_words_real.txt")
	spans, ok := AlignLines([]string{"工牌呢。", "被收了。"}, r.Words)
	if !ok {
		t.Fatal("这两句在词流里逐字可对（同音错 1 个字），必须对齐得上")
	}
	if len(spans) != 2 {
		t.Fatalf("该返回 2 个落点，实际 %d", len(spans))
	}
	if math.Abs(spans[0].Start-4.18) > 0.02 || math.Abs(spans[0].End-6.30) > 0.02 {
		t.Errorf("第 1 句「工牌呢。」该落在 4.18–6.30（词边界），实际 %.2f–%.2f",
			spans[0].Start, spans[0].End)
	}
	if math.Abs(spans[1].Start-6.30) > 0.02 || math.Abs(spans[1].End-7.84) > 0.02 {
		t.Errorf("第 2 句「被收了。」该落在 6.30–7.84，实际 %.2f–%.2f",
			spans[1].Start, spans[1].End)
	}
	// 第 1 句是「工牌呢」被听成「公牌呢」⇒ 3 个字命中 2 个。**这一条就是"容忍同音错字"的判据**：
	// 若把阈值抬到要求逐字相等，这里 immediately 变成 ok=false（字幕整段退回窗口）。
	if math.Abs(spans[0].Ratio-2.0/3.0) > 0.01 {
		t.Errorf("第 1 句的字符命中比例该是 2/3（同音错 1 个字），实际 %.3f", spans[0].Ratio)
	}
	if math.Abs(spans[1].Ratio-1.0) > 0.01 {
		t.Errorf("第 2 句逐字全中，比例该是 1.0，实际 %.3f", spans[1].Ratio)
	}
}

// 真机镜 12：逐字全中的那两句（对照上一条，说明判据对"全中"也一样准）。
func TestAlignLinesRealShot012(t *testing.T) {
	r := realWords(t, "asr_shot012_words_real.txt")
	spans, ok := AlignLines([]string{"背面是空的。", "现在不是了。"}, r.Words)
	if !ok || len(spans) != 2 {
		t.Fatalf("该对齐出 2 个落点，实际 ok=%v spans=%v", ok, spans)
	}
	if math.Abs(spans[0].Start-0.50) > 0.02 || math.Abs(spans[0].End-2.50) > 0.02 {
		t.Errorf("第 1 句该落在 0.50–2.50，实际 %.2f–%.2f", spans[0].Start, spans[0].End)
	}
	if math.Abs(spans[1].Start-3.48) > 0.02 || math.Abs(spans[1].End-4.66) > 0.02 {
		t.Errorf("第 2 句该落在 3.48–4.66，实际 %.2f–%.2f", spans[1].Start, spans[1].End)
	}
}

// **没念的台词不许被硬对齐**（这是"该念没念"那条判据的地基）。
//
// 反向场景就是真机镜 2/5：计划里有台词、音轨里根本没有（ASR 0 段）。若对齐器"总能找出
// 一个落点"，那么"这句到底念没念"就永远判不出来 —— 那这条升级反而把问题盖住了。
func TestAlignRefusesWhenLineWasNotSpoken(t *testing.T) {
	r := realWords(t, "asr_shot012_words_real.txt")
	// ① 完全无关的一句：字符命中比例接近 0
	if _, ok := AlignLines([]string{"完全无关的一句台词哦"}, r.Words); ok {
		t.Error("音轨里没有的台词不该被对齐上（否则「该念没念」永远判不出来）")
	}
	// ② 掺在中间：第一句对得上、第二句没有 ⇒ 整体不 ok（不许只对一半就返回）
	if _, ok := AlignLines([]string{"背面是空的。", "谁把灯关了"}, r.Words); ok {
		t.Error("有一句对不上时，整体该判不 ok（半个答案比没有答案更危险）")
	}
	// ③ 顺序也不能倒（词流不回头）
	if _, ok := AlignLines([]string{"现在不是了。", "背面是空的。"}, r.Words); ok {
		t.Error("台词顺序与音轨顺序相反时不该硬对齐")
	}
}

// 字数很少的短句不放过（"嗯。"这种真机常见）：1 个字命中 1 个字 = 1.0，但要确认它不会
// 因为太短而滥配到任意位置 —— 判据是**只认词流里真的那个字**。
func TestAlignShortLineUsesTheRealWord(t *testing.T) {
	words := []Word{
		{Start: 0.10, End: 0.40, Text: "你"},
		{Start: 0.40, End: 0.70, Text: "嗯"},
		{Start: 0.70, End: 1.00, Text: "好"},
	}
	spans, ok := AlignLines([]string{"嗯。"}, words)
	if !ok || len(spans) != 1 {
		t.Fatalf("「嗯。」该对到词流里的「嗯」，实际 ok=%v spans=%v", ok, spans)
	}
	if math.Abs(spans[0].Start-0.40) > 1e-6 || math.Abs(spans[0].End-0.70) > 1e-6 {
		t.Errorf("该落在 0.40–0.70，实际 %.2f–%.2f", spans[0].Start, spans[0].End)
	}
}

// 归一化：标点与空格不参与对齐（否则"错一个标点"也算错）。
func TestNormAlignTextDropsPunctuation(t *testing.T) {
	got := string(normAlignText("工牌呢。！？ …B-2 "))
	want := "工牌呢b2"
	if got != want {
		t.Errorf("归一化该是 %q，实际 %q", want, got)
	}
}

// JoinWords：出问题时要能把"ASR 到底听到什么"原样打出来（诊断用）。
func TestJoinWordsReal(t *testing.T) {
	r := realWords(t, "asr_shot012_words_real.txt")
	if got := JoinWords(r.Words); got != "背面是空的。现在不是了。" {
		t.Errorf("词流拼回来该是台词原文，实际 %q", got)
	}
}
