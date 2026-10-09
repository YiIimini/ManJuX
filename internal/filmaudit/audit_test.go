package filmaudit

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/project"
)

// ParseSRT 必须把 `HH:MM:SS,mmm` 算对。
//
// 【为什么单列这条（2026-10-09）】第一版把毫秒位当"1 位小数"处理（`div=1000` 再按位数往下除），
// `00:00:03,349` 被算成 **37.900** —— 体检于是报出"这条字幕挂了 15 秒"这种**假问题**。
// 夹具是盘上那份 final.srt 的原文（逐字）。
func TestParseSRTRealFixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "final_real.srt"))
	if err != nil {
		t.Fatalf("读不到夹具：%v", err)
	}
	cues := ParseSRT(string(b))
	if len(cues) != 19 {
		t.Fatalf("该读到 19 条 cue，实际 %d", len(cues))
	}
	// 夹具第一条：0.837 → 7.037（毫秒位 3 位，绝不能被算成 37.900）
	if math.Abs(cues[0].Start-0.837) > 1e-9 || math.Abs(cues[0].End-7.037) > 1e-9 {
		t.Errorf("第 1 条该是 0.837→7.037，实际 %.3f→%.3f（毫秒位算错就会变成 37.9/53.0）",
			cues[0].Start, cues[0].End)
	}
	if cues[0].Text != "姜晚舟:活的。" {
		t.Errorf("第 1 条文字该是「姜晚舟:活的。」，实际 %q", cues[0].Text)
	}
	// 相邻差值：毫秒位 1/2/3 位都要对（`,5` / `,05` / `,500`）
	for _, tc := range []struct {
		in   string
		want float64
	}{
		{"00:00:03,349", 3.349},
		{"00:01:25,862", 85.862},
		{"00:00:00,5", 0.5},
		{"00:00:00,05", 0.05},
		{"01:02:03,004", 3723.004},
	} {
		ms := srtTimeRe.FindStringSubmatch(tc.in)
		if ms == nil {
			t.Fatalf("%q 没被正则匹配上", tc.in)
		}
		if got := srtSeconds(ms); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%q 该是 %.3f 秒，实际 %.3f", tc.in, tc.want, got)
		}
	}
}

// Audit 在**真机那一次的数据**上必须报出用户当时报的四类问题。
//
// 夹具：方案 = `internal/project/testdata/plan_real_voiceover.json`（盘上那份 plan.json 逐字拷的），
// 字幕 = `testdata/final_real.srt`（同一集的 final.srt 原文），ASR 段 = 那一集 11 个 `shot_00N.asr.txt`
// 里逐字抄下来的时间段（镜 2/5 确实是 0 段）。
func TestAuditFindsTheFourRealComplaints(t *testing.T) {
	in := realEpisode(t)
	fs := Audit(in)
	all := strings.Join(msgOf(fs), "\n")

	// ① 字幕落点：镜 1 的字幕挂 0.837→7.037，而实测发声是 [3.50-4.50] ⇒ 必须点名偏差
	if !strings.Contains(all, "shot #1 第 1 句") || !strings.Contains(all, "实测发声在 3.50s") {
		t.Errorf("镜 1 的 2.7 秒偏差必须点名：\n%s", all)
	}
	// ② 该念没念：镜 2 / 镜 5 的 ASR 是 0 段
	for _, id := range []string{"shot #2", "shot #5"} {
		if !strings.Contains(all, id) || !strings.Contains(all, "没有任何人声") {
			t.Errorf("%s 的音轨里没有人声这件事必须点名：\n%s", id, all)
		}
	}
	// ③ 说话人不在画面里（镜 5：小灯）
	if !strings.Contains(all, "台词是 小灯 说的") || !strings.Contains(all, "不在本镜画面里") {
		t.Errorf("镜 5 的画外音必须点名：\n%s", all)
	}
	// ④ 模型把画内台词标成了画外音（那份夹具**自愈前** 19 句全标 os=true，其中 18 句的说话人就在本镜）
	if !strings.Contains(all, "标成了画外音（os=true）") {
		t.Errorf("模型把画内台词标成画外音这件事必须点名（已提交的提示词带着旧标法）：\n%s", all)
	}
	// ⑤ 段数与句数不符时，字幕越出人声区间（真机镜 10：两句台词、ASR 只有一段 0.18–9.27）
	if !strings.Contains(all, "人声区间是") {
		t.Errorf("段数与句数不符时字幕越出人声区间必须点名：\n%s", all)
	}
	// ⑥ 接缝：真机 11 镜只有 2 个场次
	if !strings.Contains(all, "同一场次的连续动作") {
		t.Errorf("同场接缝必须点名：\n%s", all)
	}
}

// 干净的一集**不许**报这些（否则体检就是噪音）。
func TestAuditQuietOnCleanEpisode(t *testing.T) {
	p := &project.Plan{
		Characters: []project.Character{{ID: "char_a", Name: "阿甲"}, {ID: "char_b", Name: "阿乙"}},
		Scenes:     []project.Scene{{ID: "scene_room", Name: "破庙"}},
		Shots: []project.Shot{
			{ID: 1, Scene: "scene_room", Characters: []string{"char_a"}, Duration: 8,
				Dialogue: []project.Line{{Speaker: "阿甲", Text: "我准了。"}}, H3Prompt: "x"},
			{ID: 2, Scene: "scene_room", Characters: []string{"char_a", "char_b"}, Duration: 8,
				Dialogue: []project.Line{{Speaker: "阿乙", Text: "知道了。"}}, H3Prompt: "x"},
		},
	}
	in := Input{
		Plan:      p,
		Durations: []float64{8, 8},
		// 第 1 句在 2.00–3.00 说、第 2 句在 10.00–11.00 说，字幕正好压在上面
		ASRSpans: [][]asr.Segment{
			{{Start: 2.00, End: 3.00, Text: "我准了。"}},
			{{Start: 2.00, End: 3.00, Text: "知道了。"}},
		},
		Cues: []Cue{{Start: 2.00, End: 3.00, Text: "阿甲:我准了。"}, {Start: 10.00, End: 11.00, Text: "阿乙:知道了。"}},
	}
	all := strings.Join(msgOf(Audit(in)), "\n")
	for _, bad := range []string{"实测发声在", "没有任何人声", "不在本镜画面里", "全是画外音", "这条字幕挂了", "重叠"} {
		if strings.Contains(all, bad) {
			t.Errorf("干净的一集不该报 %q：\n%s", bad, all)
		}
	}
	if !strings.Contains(all, "与逐镜 ASR 实测发声段一致") {
		t.Errorf("干净的一集该有一条「落点一致」的如实告知：\n%s", all)
	}
}

// realEpisode 组装真机那一集的输入（方案与字幕来自夹具，ASR 段是本轮取证时逐字抄下的时间段）。
func realEpisode(t *testing.T) Input {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "project", "testdata", "plan_real_voiceover.json"))
	if err != nil {
		t.Fatalf("读不到方案夹具：%v", err)
	}
	var p project.Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("方案夹具解析失败：%v", err)
	}
	project.HealPlan(&p) // 与体检的读取边界一致
	srt, err := os.ReadFile(filepath.Join("testdata", "final_real.srt"))
	if err != nil {
		t.Fatalf("读不到字幕夹具：%v", err)
	}
	// 真机 11 镜的时长（`# ... dur=Xs`）与 ASR 段（`[a-b] 文本`），逐字抄自那一集的 shot_00N.asr.txt。
	durations := []float64{7.9, 8.0, 8.5, 8.5, 8.5, 7.8, 8.0, 10.6, 8.5, 9.2, 10.6}
	seg := func(xs ...float64) []asr.Segment {
		var out []asr.Segment
		for i := 0; i+1 < len(xs); i += 2 {
			out = append(out, asr.Segment{Start: xs[i], End: xs[i+1]})
		}
		return out
	}
	asrSpans := [][]asr.Segment{
		seg(3.50, 4.50),             // 镜1 活的。
		nil,                         // 镜2 segs=0
		seg(0, 2, 2, 5),             // 镜3 你冷不冷 / 师姐别挖了…
		seg(0.02, 2.02),             // 镜4 死人也是人。
		nil,                         // 镜5 segs=0
		seg(2.48, 7.76),             // 镜6 抱回来了？队里批的是破冰取样…
		seg(1.26, 7.86),             // 镜7 她是活的 / 活的更贵…
		seg(0.14, 3.14, 3.14, 5.14), // 镜8 炉四十天没亮 / 你要入册…
		seg(0, 4.43, 4.43, 8.43),    // 镜9 我算的是船 / 它不会飞…
		seg(0.18, 9.27),             // 镜10 那叫糖球吧 / 行。但得记着…
		seg(0, 5.6),                 // 镜11 三天之内…
	}
	return Input{
		ID:        "真机《我捡的团子·第01集》",
		Plan:      &p,
		Durations: durations,
		ASRSpans:  asrSpans,
		Cues:      ParseSRT(string(srt)),
		// 夹具那份 plan（自愈前）19 句全标 os=true，其中 18 句的说话人就在本镜出场名单里。
		RawInFrameAsOffscreen: 18,
		Transition:            "fade",
		FadeSec:               0,
	}
}

func msgOf(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Where+" "+f.Msg)
	}
	return out
}

// 自愈前计数：真机那份 plan 里 18 句的说话人**就在本镜出场名单里**却被标成画外音。
//
// 这个数必须在 HealPlan **之前**数（自愈会把它们改回 os=false，形态就看不见了），
// 而体检要报的正是"已提交的提示词带着旧标法"。
func TestCountInFrameMarkedOffscreenOnRealPlan(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "project", "testdata", "plan_real_voiceover.json"))
	if err != nil {
		t.Fatalf("读不到方案夹具：%v", err)
	}
	var p project.Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("方案夹具解析失败：%v", err)
	}
	if got := countInFrameMarkedOffscreen(&p); got != 18 {
		t.Errorf("自愈前该数到 18 句「人在画面里却被标成画外音」，实际 %d", got)
	}
	// 自愈之后这个数应当归零（同一份方案、同一条读取边界）
	project.HealPlan(&p)
	if got := countInFrameMarkedOffscreen(&p); got != 0 {
		t.Errorf("自愈之后不该再数到误标，实际 %d", got)
	}
	// 真画外音（镜 5 的小灯）**不算**误标
	if project.OffScreenSpeakers(&p, p.Shots[4]) == nil {
		t.Error("镜 5 的说话人不在本镜 ⇒ 必须仍被认成画外音")
	}
}

// ⛔ 一条都不许被截掉（2026-10-09，这条缺陷是**体检咬出我自己**的）。
//
// 第一版把 `ValidatePlan` 的非 info 结论**一刀切留前 12 条**，而顺序是它的产出顺序 ——
// 真机上就出现了 ⛔ 被 12 条 ⚠ 挤出窗口：体检报的“必须处理”比实际少。判据直接用
// “体检报的 ⛔ 数 == 契约门报的 ⛔ 数”，并先确认这一份夹具真的会触发截断（否则是空断言）。
func TestAuditNeverTruncatesFails(t *testing.T) {
	// 15 镜、每镜时长 0 秒（⇒ 每镜一条 fail「超出 H3 合法区间」），字段大量留空（⇒ 一堆 warn）。
	p := &project.Plan{Title: "T", Style: "x", Scenes: []project.Scene{{ID: "scene_a", Name: "走廊"}}}
	for i := 1; i <= 15; i++ {
		p.Shots = append(p.Shots, project.Shot{
			ID: i, Scene: "scene_a", Duration: 0,
			H3Prompt: "[Shot 1] A girl stands at a window.",
		})
	}
	wantFail, wantWarn := 0, 0
	for _, it := range project.ValidatePlan(p) {
		switch it.Level {
		case "fail":
			wantFail++
		case "warn":
			wantWarn++
		}
	}
	if wantFail == 0 || wantWarn <= 12 {
		t.Fatalf("夹具没触发截断（fail=%d warn=%d）—— 这条判据就成了空断言，先修夹具", wantFail, wantWarn)
	}
	fail, warn, _ := CountLevels(Audit(Input{Plan: p}))
	if fail != wantFail {
		t.Errorf("体检报的 ⛔ 是 %d 条，契约门是 %d 条 —— **⛔ 一条都不许被截掉**（用户看到的必须是一致的）",
			fail, wantFail)
	}
	if warn > wantWarn {
		t.Errorf("体检报的 ⚠ 比契约门还多？(%d > %d)", warn, wantWarn)
	}
}
