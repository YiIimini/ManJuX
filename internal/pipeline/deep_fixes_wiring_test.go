package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/qc"
)

// TestQcVerdictSaysUnverifiedInsteadOfAllPass 【2026-10-08 深度排查】
//
// 三态里的第三态（**没验过**）此前不进集级结论：整集 ASR 挂了、音色一律"未判"、
// 空间锚点一次没核查 —— 用户看到的仍然是「✔ 质检通过:13/13 镜全部合格」，
// 而这句话的语义是"这些项都验过且干净"。
func TestQcVerdictSaysUnverifiedInsteadOfAllPass(t *testing.T) {
	base := qcCounts{Total: 13, Checked: 13}
	// ① 真的全验过且干净 ⇒ success（这条不能被上面的改动带坏）。
	if v := qcVerdictOf(base, nil); v.Level != "success" || !strings.Contains(v.Msg, "全部合格") {
		t.Fatalf("全验过且干净时应当 success：%+v", v)
	}
	// ② 语音核对整集失败（ASR 挂了）⇒ 不许再打"全部合格"。
	v := qcVerdictOf(qcCounts{Total: 13, Checked: 13, SpeechUnverified: 13}, nil)
	if v.Level == "success" || strings.Contains(v.Msg, "全部合格") {
		t.Fatalf("整集语音没验成时不许报\"全部合格\"：%+v", v)
	}
	if !strings.Contains(v.Msg, "没验过") || !strings.Contains(v.Msg, "语音") {
		t.Errorf("文案要说清\"哪一项没验过\"：%q", v.Msg)
	}
	// ③ 音色一律未判 ⇒ 同样要说出来。
	if v := qcVerdictOf(qcCounts{Total: 13, Checked: 13, VoiceUnjudged: 13}, nil); v.Level == "success" {
		t.Fatalf("音色整集未判时不许报 success：%+v", v)
	}
	// ④ 空间锚点一次没核查（未配视觉模型）⇒ 同样。
	if v := qcVerdictOf(qcCounts{Total: 13, Checked: 13, LandmarkUnchecked: 13}, nil); v.Level == "success" {
		t.Fatalf("空间锚点全没核查时不许报 success：%+v", v)
	}
	// ⑤ 判负优先于"未验"（更严重的那一类先摆出来）。
	v = qcVerdictOf(qcCounts{Total: 13, Checked: 13, VoiceFlagged: 2, VoiceUnjudged: 5}, nil)
	if !strings.Contains(v.Msg, "音色与角色卡不符") {
		t.Errorf("判负要优先于\"未验\"出现：%q", v.Msg)
	}
	// ⑥ 纯函数不许 panic/空文案。
	if v := qcVerdictOf(qcCounts{}, nil); v.Level != "error" {
		t.Errorf("没有可质检的镜头时应当是 error：%+v", v)
	}
}

// TestQcVLMBudgetIsBounded 质检阶段的视觉调用必须有**上限**（2026-10-08 深度排查）：
// 此前质检完全绕开 vlm_guard 那道闸，限流时一集照打十几次、用户看到一屏 429。
func TestQcVLMBudgetIsBounded(t *testing.T) {
	// 13 镜的常规集：够用（每镜最多 2 次 + 余量）。
	if n := qcVLMBudget(13); n < 13*2 {
		t.Errorf("13 镜的预算太小，正常质检会被自己的闸挡住：%d", n)
	}
	// 但**必须有上限**：不能随镜数无限涨（88 集的极端情况也不该打几百次）。
	if n := qcVLMBudget(1000); n > 120 {
		t.Errorf("预算必须有硬上限：%d", n)
	}
	// 至少留够最小集的空间。
	if n := qcVLMBudget(1); n < 12 {
		t.Errorf("最小预算太小：%d", n)
	}
}

// TestQcLandmarkIsRecordedAsThreeStates 接线：质检阶段必须把三态写进报告，
// 并且**受 vlmSkipReason 那道闸**约束（此前完全没走）。
func TestQcLandmarkIsRecordedAsThreeStates(t *testing.T) {
	src := readSrc(t, "stages_qc_assemble.go")
	for _, want := range []struct{ needle, why string }{
		{"r.Landmark = lm", "核查结果要写进报告（否则界面上还是看不到\"没验\"）"},
		{"qc.LandmarkSkipped(", "没验的出口必须留痕（未配模型/场景没写地标/非建立镜/被闸住）"},
		{"func (c *Coordinator) checkLandmark(", "核查逻辑要收成一个入口（四个出口都写清理由）"},
		{"c.VLMResetBudgetN(qcVLMBudget(len(plan.Shots)))", "质检阶段要给自己设视觉调用预算（此前完全没有闸）"},
		{"landmarkUnchecked++", "没核查要计入集级\"未验\"（否则结论还是\"全部合格\"）"},
		{"voiceUnjudged++", "音色未判要计入集级\"未验\""},
		{"speechUnverified++", "语音没验成要计入集级\"未验\""},
		{"c.vlmSkipReason(proj)", "三个视觉调用点都要过限流/预算那道闸"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("接线缺 %q —— %s", want.needle, want.why)
		}
	}
	// 报告里必须真的有这个字段（qc.Report）。
	var zero qc.Report
	if zero.Landmark != nil {
		t.Error("qc.Report.Landmark 初值应当为 nil（只有真跑过才写）")
	}
}

// TestPitchSwitchIsHonoredEverywhere `qc.enable_pitch` 关掉之后，**质检与重出**都不许再跑它。
func TestPitchSwitchIsHonoredEverywhere(t *testing.T) {
	qcSrc := readSrc(t, "stages_qc_assemble.go")
	if !strings.Contains(qcSrc, "if !p.PitchOn() {") {
		t.Error("质检阶段没看 qc.enable_pitch 开关：关掉了还在跑")
	}
	vcSrc := readSrc(t, "voice_check.go")
	if !strings.Contains(vcSrc, "c.ff != nil && p.PitchOn()") {
		t.Error("渲染阶段的有界重出没看 qc.enable_pitch：用户关了它照样为它换种子重出（白烧 GPU）")
	}
}

// TestRenderPrologueRunsOnlyOnFirstPass 重出轮次不许重跑前戏（资产补齐 + 相关播报）。
func TestRenderPrologueRunsOnlyOnFirstPass(t *testing.T) {
	src := readSrc(t, "stages_render.go")
	if !strings.Contains(src, "if pass == 0 {") {
		t.Error("前戏没有 pass==0 守卫：重出轮次会重复播报、并再扫一遍资产（必要时白烧一次补图 GPU）")
	}
	// 显存释放**不在**守卫内（重出前释放一次对渲染有利）。
	iGuard := strings.Index(src, "if pass == 0 {")
	iFree := strings.Index(src, "eng.FreeVRAM(ctx)")
	if iGuard < 0 || iFree < iGuard {
		t.Error("显存释放被一起关掉了？它必须在 pass==0 守卫**之后**照常执行")
	}
}

// TestScriptStageFillsEmptyBodiesBeforeNaming 【2026-10-08 用户第二次报「还是报一样的错误」】
//
// 剧本阶段的顺序：**先**按这一镜自己的材料拼最小正文（平台兜底），**再**点名仍然为空的镜。
// 顺序反了就变成"先吓用户一跳、再默默修好"（而真正的效果是渲染能继续了）。
func TestScriptStageFillsEmptyBodiesBeforeNaming(t *testing.T) {
	src := readSrc(t, "stages_script.go")
	iFill := strings.Index(src, "project.FillEmptyShotBodies(plan)")
	iName := strings.Index(src, "project.EmptyPromptShotIDs(plan)")
	if iFill < 0 {
		t.Fatal("剧本阶段没有调 project.FillEmptyShotBodies —— 空正文的镜会一路走到渲染入口的致命门，48 集产线停在第 1 集")
	}
	if iName < 0 {
		t.Fatal("剧本阶段的空正文点名不见了（没材料的镜必须说出来）")
	}
	if iFill > iName {
		t.Error("顺序反了：必须**先**兜底拼正文、**再**点名仍然为空的镜")
	}
}

// TestRenderStageAnnouncesFilledBodies 渲染阶段必须如实播报"这几镜的正文是平台拼的"
// （静默补上等于骗人：用户以为那是模型写的）。
func TestRenderStageAnnouncesFilledBodies(t *testing.T) {
	src := readSrc(t, "stages_render.go")
	if !strings.Contains(src, "plan.FilledBodyShots") {
		t.Error("渲染阶段没有播报 plan.FilledBodyShots —— 平台替模型补的正文用户无从得知")
	}
}

// TestBothStagesAnnounceDroppedChineseMaterial 【真机 preflight 抓出来的第二处】
//
// 兜底正文**只能用英文材料**（汉字出现在 `<d>` 外会被入口的汉字卫生门拒收：>200 即 fail）。
// 于是这一镜的中文走位/动作描述进不去 —— 少了这部分信息**必须说出来**（那几镜只能靠参考图与机位兜画），
// 两个阶段都要说（剧本阶段决定存盘后的方案；渲染阶段才是用户点下去的那一刻）。
func TestBothStagesAnnounceDroppedChineseMaterial(t *testing.T) {
	for _, f := range []string{"stages_script.go", "stages_render.go"} {
		src := readSrc(t, f)
		if !strings.Contains(src, "plan.FilledBodyCJK") {
			t.Errorf("%s 没有播报 plan.FilledBodyCJK —— 中文材料没进正文这件事被静默吞掉了", f)
		}
	}
}
