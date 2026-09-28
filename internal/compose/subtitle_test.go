package compose

import (
	"math"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/audio"
	"github.com/YiIimini/ManJuX/internal/project"
)

// synthEnv 造一条包络:整段是底噪,spans 指定的区间是"人声"。
// 门限自适应的取值(底噪 0.0005 → -66dB,人声 0.35 → -9dB)足够拉开,检测结果确定。
func synthEnv(total float64, spans []cueSpan) *audio.Envelope {
	hop := audio.Hop
	n := int(total/hop) + 1
	rms := make([]float64, n)
	for i := range rms {
		rms[i] = 0.0005
	}
	for _, sp := range spans {
		i0, i1 := int(sp.Start/hop), int(sp.End/hop)
		for i := i0; i < i1 && i < n; i++ {
			rms[i] = 0.35
		}
	}
	return &audio.Envelope{Hop: hop, RMS: rms}
}

// TestAlignCuesUsesRealSpeech 三句台词 + 三个真实发声段 → 每条字幕落在对应的发声段上
// (而不是镜内均分的老窗口上)。
func TestAlignCuesUsesRealSpeech(t *testing.T) {
	const d = 10.0
	spans := []cueSpan{{Start: 1.0, End: 2.0}, {Start: 4.0, End: 5.5}, {Start: 7.5, End: 8.5}}
	tr := speechTrack{Env: synthEnv(d, spans), From: 0, To: d}

	got, ok := alignCues(0, d, tr, 3)
	if !ok {
		t.Fatal("三句台词对三个发声段,必须判为已对齐")
	}
	if len(got) != 3 {
		t.Fatalf("应给出 3 条落点,实际 %d", len(got))
	}
	for i, sp := range spans {
		if math.Abs(got[i].Start-(sp.Start-cueLead)) > 0.06 {
			t.Errorf("第 %d 句起点应贴在人声段起点 %.2f 附近,实际 %.2f", i+1, sp.Start, got[i].Start)
		}
		if math.Abs(got[i].End-(sp.End+cueTrail)) > 0.06 {
			t.Errorf("第 %d 句终点应贴在人声段终点 %.2f 附近,实际 %.2f", i+1, sp.End, got[i].End)
		}
	}
	// 与镜头级窗口的对比:老窗口第一句是 [1.0,4.0](镜内均分),新落点必须明显不同
	old := fallbackCues(0, d, 3)
	if math.Abs(got[0].End-old[0].End) < 0.5 {
		t.Errorf("落点仍与镜头级窗口重合(说明没吃真实人声):新 %.2f vs 旧 %.2f", got[0].End, old[0].End)
	}
	// 相邻两条不许重叠(留白把两句推到一起时要把前一条压回去)
	for i := 0; i+1 < len(got); i++ {
		if got[i].End > got[i+1].Start {
			t.Errorf("第 %d、%d 条字幕重叠:%.2f > %.2f", i+1, i+2, got[i].End, got[i+1].Start)
		}
	}
}

// TestAlignCuesFallsBackOnCountMismatch 发声段数与台词句数不符 → 回退镜头级窗口
// (与旧实现逐字一致的窗口),不许"按比例摊"出一个看着合理的错位落点。
func TestAlignCuesFallsBackOnCountMismatch(t *testing.T) {
	const d = 10.0
	tr := speechTrack{Env: synthEnv(d, []cueSpan{{Start: 1, End: 2}, {Start: 5, End: 6}}), From: 0, To: d}
	got, ok := alignCues(0, d, tr, 3)
	if ok {
		t.Fatal("两段人声对三句台词必须判为未对齐")
	}
	want := fallbackCues(0, d, 3)
	for i := range want {
		if math.Abs(got[i].Start-want[i].Start) > 1e-9 || math.Abs(got[i].End-want[i].End) > 1e-9 {
			t.Errorf("第 %d 条应精确回退到镜头级窗口(%.3f,%.3f),实际 (%.3f,%.3f)",
				i+1, want[i].Start, want[i].End, got[i].Start, got[i].End)
		}
	}
}

// TestAlignCuesFallsBackWithoutEnvelope 包络缺失/解不出 → 同样回退,且不上报"已对齐"。
func TestAlignCuesFallsBackWithoutEnvelope(t *testing.T) {
	const d = 8.0
	for _, tr := range []speechTrack{{From: 0, To: d}, {Env: &audio.Envelope{Hop: audio.Hop}, From: 0, To: d}} {
		got, ok := alignCues(2.0, d, tr, 2)
		if ok {
			t.Fatal("没有可用包络时不能声称已对齐")
		}
		want := fallbackCues(2.0, d, 2)
		if math.Abs(got[0].Start-want[0].Start) > 1e-9 || math.Abs(got[1].Start-want[1].Start) > 1e-9 {
			t.Errorf("应回退到镜头级窗口(起点相对镜内偏移 + 镜起点 2.0):%+v", got)
		}
	}
}

// TestSpeechSpansClipsToAudibleWindow 人声段落要夹在该镜**实际可听**的区间里 ——
// 被接缝让出去的音频在成片里是没有声音的,按它排字幕等于给观众看一段无声的字。
func TestSpeechSpansClipsToAudibleWindow(t *testing.T) {
	env := synthEnv(10, []cueSpan{{Start: 0.2, End: 2.0}, {Start: 4.0, End: 6.0}})
	got := speechSpans(env, 1.0, 5.0)
	if len(got) != 2 {
		t.Fatalf("应有两段落在窗口附近的发声段,实际 %d: %+v", len(got), got)
	}
	if math.Abs(got[0].Start-1.0) > 0.05 || math.Abs(got[0].End-2.0) > 0.05 {
		t.Errorf("首段应被夹到可听区间起点:%.2f-%.2f", got[0].Start, got[0].End)
	}
	if math.Abs(got[1].End-5.0) > 0.05 {
		t.Errorf("次段应被夹到可听区间终点:%.2f-%.2f", got[1].Start, got[1].End)
	}
	if got := speechSpans(nil, 0, 5); got != nil {
		t.Error("无包络应返回 nil")
	}
	if got := speechSpans(env, 5, 5); got != nil {
		t.Error("零宽窗口应返回 nil")
	}
}

// TestPlanCuesNotes 对不齐要留痕:哪些镜按真实人声、哪些镜退回了镜头级窗口。
// "退化了但不说"正是这条链路最危险的失败方式(作者以为字幕已经对上了)。
func TestPlanCuesNotes(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 10, Dialogue: []project.Line{{Speaker: "甲", Text: "第一句"}}},
		{ID: 2, Duration: 10, Dialogue: []project.Line{{Speaker: "甲", Text: "第二句"}}, Narration: "旁白三"},
		{ID: 3, Duration: 10, Dialogue: []project.Line{{Speaker: "甲", Text: "第四句"}}},
	}}
	starts := []float64{0, 10, 20}
	durations := []float64{10, 10, 10}
	tracks := []speechTrack{
		// 第 1 镜:一段人声对一句 → 对齐
		{Env: synthEnv(10, []cueSpan{{Start: 2, End: 3}}), From: 0, To: 10},
		// 第 2 镜:两句话但只有一段人声 → 条数不符,回退
		{Env: synthEnv(10, []cueSpan{{Start: 2, End: 3}}), From: 0, To: 10},
		// 第 3 镜:包络解不出来 → 回退
		{From: 0, To: 10},
	}
	cp := planCues(plan, starts, durations, tracks)
	notes := strings.Join(cp.notes, "|")
	if !strings.Contains(notes, "1/3 镜按真实人声对齐") {
		t.Errorf("应如实回显对齐比例,实际 %q", notes)
	}
	if !strings.Contains(notes, "第 2 镜的人声段落数与台词句数不符") {
		t.Errorf("条数不符要单独点名,实际 %q", notes)
	}
	if !strings.Contains(notes, "第 3 镜没有可用的人声包络") {
		t.Errorf("包络缺失要单独点名,实际 %q", notes)
	}
	// 第 1 镜的落点确实吃到了人声(而不是镜内均分的 1.0)
	if got := cp.shotSpans(0, 0, 10, 1); math.Abs(got[0].Start-1.92) > 0.06 {
		t.Errorf("第 1 镜的字幕应落在 2 秒的人声上,实际 %.2f", got[0].Start)
	}
	// 第 2 镜按计划回退:第一句起点仍是镜头级窗口的 1.0
	if got := cp.shotSpans(1, 10, 10, 2); math.Abs(got[0].Start-11.0) > 1e-9 {
		t.Errorf("第 2 镜应回退到镜头级窗口(11.0),实际 %.2f", got[0].Start)
	}
	// 计划之外的镜(下标越界)也不能崩,直接回退
	if got := cp.shotSpans(9, 0, 10, 1); len(got) != 1 {
		t.Error("越界的位置应回退镜头级窗口而不是 panic")
	}
}

// TestCuePlanDrivesBothSubtitles 烧录的 ASS 与外发的 SRT 必须吃**同一份**落点
// (审计 M36 的老问题:两份字幕各算各的窗口)。
func TestCuePlanDrivesBothSubtitles(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 10, Dialogue: []project.Line{{Speaker: "甲", Text: "只有一句"}}},
	}}
	starts := []float64{0}
	durations := []float64{10}
	tracks := []speechTrack{{Env: synthEnv(10, []cueSpan{{Start: 4, End: 5}}), From: 0, To: 10}}
	cp := planCues(plan, starts, durations, tracks)
	if !strings.Contains(strings.Join(cp.notes, "|"), "1/1 镜按真实人声对齐") {
		t.Fatalf("测试前提没成立(没对齐):%v", cp.notes)
	}

	srt, err := srtWindow(t, buildSRT(plan, starts, durations, &cp))
	if err != nil {
		t.Fatal(err)
	}
	ass, err := assWindow(t, buildASS(plan, starts, durations, 4, subtitleMarginVPctDefault, 768, 1344, &cp))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(srt[0]-ass[0]) > 0.06 || math.Abs(srt[1]-ass[1]) > 0.06 {
		t.Errorf("SRT 与 ASS 的落点应同源(仅允许 SRT 的 50ms 行内留白):SRT [%.2f,%.2f] vs ASS [%.2f,%.2f]",
			srt[0], srt[1], ass[0], ass[1])
	}
	if srt[0] < 3.5 {
		t.Errorf("落点应跟着人声段(约 3.92 秒起),实际 %.2f —— 说明还在用镜内均分的老窗口", srt[0])
	}
	// 公开入口(没有落点计划)保持旧行为,不因新增能力而改变既有调用方的输出
	oldSRT, err := srtWindow(t, BuildSRT(plan, starts, durations))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(oldSRT[0]-1.0) > 0.06 {
		t.Errorf("公开入口应保持镜头级窗口(1.0 秒起),实际 %.2f", oldSRT[0])
	}
}

// TestSceneCaptionEvents ASS 里必须有**场次字幕**：换场（与首镜）各一条，样式 Caption，
// 文字取场景卡名字（地点·时间），同场景连拍不重复。
//
// 来源（用户《五、3D漫剧15秒分镜指令》/《【额外】仿真人10S分镜头指令》）：
// "不同场景间通过渐隐渐显转场，如需地点时间信息，可在画面描述中以字幕形式自然呈现"。
// 文字由后期 ASS 烧（汉字字体稳），**不进视频提示词**（H3 画汉字必成伪汉字）。
func TestSceneCaptionEvents(t *testing.T) {
	plan := &project.Plan{
		Scenes: []project.Scene{
			{ID: "s1", Name: "西坡碑林·黄昏"},
			{ID: "s2", Name: "拓碑棚·清晨"},
		},
		Shots: []project.Shot{
			{ID: 1, Scene: "s1", Duration: 5, Dialogue: []project.Line{{Speaker: "甲", Text: "一"}}},
			{ID: 2, Scene: "s1", Duration: 5, Dialogue: []project.Line{{Speaker: "甲", Text: "二"}}},
			{ID: 3, Scene: "s2", Duration: 5, Dialogue: []project.Line{{Speaker: "甲", Text: "三"}}},
		},
	}
	starts := []float64{0, 5, 10}
	durations := []float64{5, 5, 5}
	ass := BuildASS(plan, starts, durations, 4, 768, 1344)
	caps := 0
	for _, ln := range strings.Split(ass, "\n") {
		if !strings.HasPrefix(ln, "Dialogue:") || !strings.Contains(ln, ",Caption,") {
			continue
		}
		caps++
		if !strings.Contains(ln, "西坡碑林·黄昏") && !strings.Contains(ln, "拓碑棚·清晨") {
			t.Errorf("场次字幕的文字应是场景卡名字（地点·时间），实际：%s", ln)
		}
	}
	if caps != 2 {
		t.Errorf("首镜 + 换场各一条，应 2 条场次字幕，实际 %d 条", caps)
	}
	if !strings.Contains(ass, "Style: Caption,") {
		t.Error("ASS 里要有 Caption 样式")
	}
}
