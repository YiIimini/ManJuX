package director

import (
	"fmt"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// TestSpeechSecondsAndNeededDuration 台词时长预算的核心换算。
func TestSpeechSecondsAndNeededDuration(t *testing.T) {
	// 空白与标点里的空白不计。期望值**从语速常量推导**，不写死数字 ——
	// 速度常量一改（2026-09-28：4.5 → 4.1，依据 H3 公开契约文档"中文约 4.1 可发声字/秒"）测试就假红。
	want9 := 9/CharsPerSecond + 0.3
	if got := SpeechSeconds("一二三四五六七八九"); got < want9-0.05 || got > want9+0.05 {
		t.Errorf("9 字台词应约 %.2f 秒(语速 %.1f),实际 %.2f", want9, CharsPerSecond, got)
	}
	if got := SpeechSeconds("   \n  "); got != 0 {
		t.Errorf("纯空白应算 0,实际 %.2f", got)
	}
	sh := project.Shot{Duration: 4, Dialogue: []project.Line{{Text: "你怎么也来这里了？"}}} // 9 字
	if got := NeededDuration(sh); got != 4 {
		t.Errorf("9 字台词需 3 秒(含收尾),不应超过 4 秒下限,实际 %d", got)
	}
	// 20 字:20/4.1+0.3 ≈ 5.18 → 6 秒（语速改小后同样的字数需要更长）
	sh.Dialogue = []project.Line{{Text: strings.Repeat("字", 20)}}
	want := wantSecondsFor(20)
	if got := NeededDuration(sh); got != want {
		t.Errorf("20 字台词应需 %d 秒(语速 %.1f),实际 %d", want, CharsPerSecond, got)
	}
	// 旁白同样占用时间
	sh.Dialogue = nil
	sh.Narration = strings.Repeat("字", 20)
	wn := wantSecondsFor(20)
	if got := NeededDuration(sh); got != wn {
		t.Errorf("旁白必须计入时长预算(应 %d 秒),实际 %d", wn, got)
	}
}

// TestFitDialogueDurations 回归用户报障「配音也不完整」在分镜侧的根治:
// 台词装不下就抬时长,而且只加不减(缩短只会制造新的截断)。
func TestFitDialogueDurations(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 4, Dialogue: []project.Line{{Text: strings.Repeat("字", 20)}}}, // 4s 装不下 20 字
		{ID: 2, Duration: 8, Dialogue: []project.Line{{Text: "短句"}}},                    // 够长,不动
		{ID: 3, Duration: 2, Dialogue: nil},                                             // 太短,抬到下限
	}}
	// 传**窗口下限**：这条门测的是"装不下就抬、只加不减"这条逻辑本身（与下标无关）。
	n, details := FitDialogueDurations(plan, MinShotSeconds)
	if n != 2 {
		t.Fatalf("应调整 2 个镜头,实际 %d(%v)", n, details)
	}
	wd := wantSecondsFor(20)
	if plan.Shots[0].Duration != wd {
		t.Errorf("镜 1 应抬到 %d 秒(20 字 + 收尾),实际 %d", wd, plan.Shots[0].Duration)
	}
	if plan.Shots[1].Duration != 8 {
		t.Errorf("镜 2 已够长,不应被改动,实际 %d", plan.Shots[1].Duration)
	}
	if plan.Shots[2].Duration != MinShotSeconds {
		t.Errorf("镜 3 应抬到下限 %d 秒,实际 %d", MinShotSeconds, plan.Shots[2].Duration)
	}
	if !strings.Contains(strings.Join(details, ";"), "台词装得下") {
		t.Errorf("调整明细应说明原因:%v", details)
	}
	if FitDialogueDurations(nil, MinShotSeconds); false {
		t.Error("nil 方案不应 panic")
	}
}

// TestFitDialogueDurationsClampsAtCeiling 超长台词抬到上限就停,并给出拆镜建议。
func TestFitDialogueDurationsClampsAtCeiling(t *testing.T) {
	long := strings.Repeat("字", 60) // 60/4.5+0.3 ≈ 13.6 秒 > 上限 10 秒
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 4, Dialogue: []project.Line{{Text: long}}},
	}}
	FitDialogueDurations(plan, MinShotSeconds)
	if plan.Shots[0].Duration != MaxShotSeconds {
		t.Fatalf("应抬到上限 %d 秒,实际 %d", MaxShotSeconds, plan.Shots[0].Duration)
	}
	msg := DialogueOverflow(plan.Shots[0])
	if !strings.Contains(msg, "拆成两镜") {
		t.Errorf("抬到上限仍装不下时必须建议拆镜,实际:%q", msg)
	}
	if DialogueOverflow(project.Shot{Duration: 6, Dialogue: []project.Line{{Text: "短句"}}}) != "" {
		t.Error("装得下的镜头不该报溢出")
	}
}

// wantSecondsFor 按当前语速常量算出"这句话需要多少秒"（向上取整，下限 4）。
// 测试从常量推导而不是写死数字：语速本身是有出处的口径（H3 公开契约文档 4.1 字/秒），
// 改它是**有意的**，不该让一堆测试变成假红。
func wantSecondsFor(chars int) int {
	raw := float64(chars)/CharsPerSecond + 0.3
	n := int(raw)
	if raw > float64(n) {
		n++
	}
	if n < 4 {
		n = 4
	}
	return n
}

// TestCutsFloorRaisesDuration 切镜需要的时间也要算进镜头时长。
//
// 来源（2026-09-28 全面审计）：shuohao-skills(Apache-2.0)「每拍几秒是**算出来的**，不要让模型估」
// ＋ 用户《【额外】仿真人10S分镜头指令》「每个片段内拆成若干小镜头，每个镜头 1.5–4 秒」。
// 病根：平台此前只按**台词**算 ⇒ 一个 4 秒片段被要求切 4 刀、每刀 1 秒（低于 1.5 秒下限）
// ⇒ 模型要么草草带过、要么根本不切（实测那一集 35/35 镜一镜到底）。
func TestCutsFloorRaisesDuration(t *testing.T) {
	h3 := func(cuts int) string {
		s := "detailed_description:\n[Shot 1] he walks."
		for i := 2; i <= cuts; i++ {
			s += fmt.Sprintf("\n[Shot %d] At 00:0%d.000, closer.", i, i)
		}
		return s
	}
	// 4 刀 ⇒ 地板 6 秒：短于它的时长必须被抬上来，且**不缩**（只加不减是既有纪律）
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 4, H3Prompt: h3(4)},
		{ID: 2, Duration: 9, H3Prompt: h3(2)}, // 已经够长，不动
	}}
	n, details := FitDialogueDurations(plan, MinShotSeconds)
	if n != 1 {
		t.Fatalf("只该调整 1 镜，实际 %d(%v)", n, details)
	}
	if plan.Shots[0].Duration != 6 {
		t.Errorf("4 刀需要至少 6 秒(4×1.5)，实际 %d", plan.Shots[0].Duration)
	}
	if plan.Shots[1].Duration != 9 {
		t.Errorf("够长的镜头不该被动，实际 %d", plan.Shots[1].Duration)
	}
	if !strings.Contains(details[0], "切镜需要") {
		t.Errorf("明细要说清是「切镜需要」抬的，实际 %q", details[0])
	}
}

// TestFitDialogueDurationsKeepsGenerationFloorOnTrainingGrid 生成路径的产物必须落在 H3 训练区间里。
//
// 【为什么要有（2026-10-11，用户：「渲染出来的视频很不如意」）】这条路原来**只有**一个下限（窗口下限 4 秒），
// 于是无台词/短台词镜会被平台自己定成 4 秒 ⇒ 本地对齐后只喂 107 帧、云端 120 帧，**都不在训练区间
// （124–362 帧）里** —— 平台自己产出"画面容易在中后段散/糊"的镜。生成侧的口径本来就是 5 秒
// （`GenMinShotSeconds`：提示词按它渲染、云端 <5 会静默抬到 5、本地 4 秒只对齐成 107 帧）。
//
// 而**导入技能端的包**（`planPostProcess`）必须保持窗口下限、一个字都不改：旧包整批 4 秒镜是**合法形态**
// （真产物夹具有 35 个），悄悄抬长＝单方面改契约（版本级改动要两侧同改）。
func TestFitDialogueDurationsKeepsGenerationFloorOnTrainingGrid(t *testing.T) {
	// 平台自己生成：无台词镜 4 秒 ⇒ 抬到 5 秒（= 120 帧；本地对齐后 124 帧，正好进训练区间）。
	gen := &project.Plan{Shots: []project.Shot{{ID: 1, Duration: 4}}}
	if n, details := FitDialogueDurations(gen, GenMinShotSeconds); n != 1 {
		t.Fatalf("生成路径该把 4 秒的无台词镜抬到生成下限 %d 秒，实际调整 %d 镜(%v)",
			GenMinShotSeconds, n, details)
	}
	if got := gen.Shots[0].Duration; got != GenMinShotSeconds {
		t.Errorf("生成路径该抬到 %d 秒，实际 %d", GenMinShotSeconds, got)
	}
	if frames := engine.AlignLength(gen.Shots[0].Duration * 24); frames < engine.H3TrainMinFrames {
		t.Errorf("生成路径的产物对齐后是 %d 帧，仍在训练区间之外（下限 %d 帧）",
			frames, engine.H3TrainMinFrames)
	}
	// 导入技能端的包：同一个镜**一个字都不许改**。
	imp := &project.Plan{Shots: []project.Shot{{ID: 1, Duration: 4}}}
	if n, details := FitDialogueDurations(imp, 0); n != 0 {
		t.Errorf("导入路径必须空转（4 秒镜是旧包合法形态），实际调整 %d 镜(%v)", n, details)
	}
	if imp.Shots[0].Duration != 4 {
		t.Errorf("导入路径把用户的 4 秒镜改成了 %d 秒 —— 静默改用户的方案就是撒谎", imp.Shots[0].Duration)
	}
}
