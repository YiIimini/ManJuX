package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
)

// train_range_test.go H3 **训练区间**播报的门（2026-10-11，用户：「渲染出来的视频很不如意」）。
//
// 【这道门要挡什么】官方节点把 H3 的训练区间标成 124–362 帧（5.17–15.08 秒）。平台既有口径
// 允许 4 秒镜（技能端旧包整批 4 秒，见 `internal/director/dialogue.go`），而 4 秒镜对齐后只喂
// 107 帧 —— **不在训练区间里**，这类镜的通病是「首帧正常、中后段散/糊」。
// 平台不许悄悄把窗口抬上去（那等于整本拒收旧包），所以只能**如实播报**：
// 判据必须在真正要渲这一镜时给出，而且要按**喂给模型的帧数**判（不是计划帧数）。

// ① 判据本体：喂给模型的帧数落在训练区间之外 ⇒ 给一句话；区间内 ⇒ 一个字都不说。
func TestTrainRangeNoteJudgesTheFramesTheModelGets(t *testing.T) {
	cases := []struct {
		name        string
		frames      int // 计划帧数（shotFrames(shot) = 时长 × 24）
		seam        bool
		wantNote    bool
		mustContain []string
		whyZh       string
	}{
		{name: "4 秒镜", frames: 96, wantNote: true, mustContain: []string{"107", "124"},
			whyZh: "真机旧包整批 4 秒镜：对齐后 107 帧 < 124 ⇒ 必须报（这类镜画面容易在中后段散）"},
		{name: "5 秒镜", frames: 120, wantNote: false,
			whyZh: "120 帧对齐后**正好 124**（下限）⇒ 不许报 —— 拿计划帧数判会在这里误报"},
		{name: "12 秒镜", frames: 288, wantNote: false, whyZh: "对齐后 294 帧，在区间内"},
		{name: "15 秒镜", frames: 360, wantNote: false, whyZh: "对齐后 362 帧，正好压在上限上（不许报）"},
		{name: "16 秒镜", frames: 384, wantNote: true, mustContain: []string{"396", "362"},
			whyZh: "对齐后 396 帧 > 362 ⇒ 尾部是崩坏高发区，必须报"},
		{name: "4 秒接缝镜", frames: 96, seam: true, wantNote: false,
			whyZh: "接缝要多渲 22 帧 pinned 头（AlignLength(118)=124 帧）⇒ 喂给模型的够长，不许报"},
		{name: "16 秒接缝镜", frames: 384, seam: true, wantNote: true, mustContain: []string{"413"},
			whyZh: "接缝同样要按 +22 算（413 帧）"},
		{name: "零帧", frames: 0, wantNote: false, whyZh: "没时长就没结论（不许编）"},
		{name: "负帧", frames: -24, wantNote: false, whyZh: "脏数据不许编结论"},
	}
	for _, c := range cases {
		got := trainRangeNote(c.frames, c.seam)
		if c.wantNote && got == "" {
			t.Errorf("%s（%d 帧 / 接缝=%v）：该报却没报 —— %s", c.name, c.frames, c.seam, c.whyZh)
			continue
		}
		if !c.wantNote && got != "" {
			t.Errorf("%s（%d 帧 / 接缝=%v）：不该报却报了：%s —— %s", c.name, c.frames, c.seam, got, c.whyZh)
			continue
		}
		for _, want := range c.mustContain {
			if !strings.Contains(got, want) {
				t.Errorf("%s：播报里该写清 %s（用户要照着这个数改时长），实际：%s", c.name, want, got)
			}
		}
	}
}

// ② 判据的帧数**必须来自引擎那一条口径**：计划帧数 → 喂给模型的帧数，平台只许有一处算法。
//
// 反向验证会咬这条：把 `comfyengine.SeamRenderLength(plannedFrames, seamHere)` 换成
// `engine.AlignLength(plannedFrames)`，接缝那两条用例立刻红（4 秒接缝镜被误报）。
func TestTrainRangeNoteUsesTheEngineGrid(t *testing.T) {
	body := funcBody(t, "stages_render.go", "func trainRangeNote(")
	if !strings.Contains(body, "comfyengine.SeamRenderLength(plannedFrames, seamHere)") {
		t.Error("判据没走引擎那条口径（comfyengine.SeamRenderLength）—— 自己算一遍 = 两处公式迟早分叉")
	}
	// 常量必须只有一份（engine.H3TrainMinFrames / H3TrainMaxFrames）：这里出现字面量就是第二份。
	if !strings.Contains(body, "engine.H3TrainMinFrames") || !strings.Contains(body, "engine.H3TrainMaxFrames") {
		t.Error("训练区间没用 engine.H3TrainMinFrames / H3TrainMaxFrames 这两个常量")
	}
	for _, lit := range []string{"124", "362"} {
		if strings.Contains(codeLines(body), lit) {
			t.Errorf("函数体里出现了训练区间的字面量 %s —— 该引用 engine 的常量（唯一出处）", lit)
		}
	}
	// 这道门的判据本身也得站得住：真机那两个数就是引擎给的（124/362），不是记忆里的。
	if engine.H3TrainMinFrames != 124 || engine.H3TrainMaxFrames != 362 {
		t.Errorf("训练区间被改成了 %d–%d 帧：真机（官方节点标注）是 124–362，改了要重跑一遍真机",
			engine.H3TrainMinFrames, engine.H3TrainMaxFrames)
	}
	if comfyengine.SeamRenderLength(96, false) != 107 || comfyengine.SeamRenderLength(96, true) != 124 {
		t.Errorf("引擎那条口径变了：96 帧普通渲 %d、接缝渲 %d（真机是 107 / 124）",
			comfyengine.SeamRenderLength(96, false), comfyengine.SeamRenderLength(96, true))
	}
}

// ③ 接线：播报必须发生在**真要渲这一镜**的时候，且在引擎调用之前（事后说就没用了）。
func TestTrainRangeNoteIsWiredIntoTheRenderPass(t *testing.T) {
	src := readSrc(t, "stages_render.go")
	const call = "trainRangeNote(wantFrames, seamHere)"
	at := strings.Index(src, call)
	if at < 0 {
		t.Fatal("渲染循环里没调 trainRangeNote —— 训练区间之外的提交又变成只有用户自己看得出来")
	}
	if line := lineOf(src, at); strings.HasPrefix(strings.TrimSpace(line), "//") {
		t.Fatalf("那句调用只出现在注释里：%q", line)
	}
	// 顺序：先播「渲染开始」，紧接着才是这条；且必须在提交引擎（job）之前。
	start := strings.Index(src, "镜头 %d/%d 渲染开始")
	if start < 0 {
		t.Fatal("找不到「渲染开始」那句（判据的前提没了）")
	}
	if at < start {
		t.Error("训练区间播报排在「渲染开始」之前 —— 日志顺序会和实际决策对不上")
	}
	// 接缝判定必须先算出来（seamHere），否则接缝镜的长度会按普通渲染算。
	seam := strings.Index(src, "seamHere = seamAllowed(")
	if seam < 0 || seam > at {
		t.Error("接缝状态（seamHere）必须在播报之前算好 —— 否则接缝镜的喂入帧数会被算短")
	}
	// 播报级别必须是 warn（info 会被前端默认折叠掉，而这是"画面不如意"的根因之一）。
	line := lineOf(src, at)
	emitLine := src[at:]
	if i := strings.Index(emitLine, "c.emit("); i >= 0 && i < 200 {
		emitLine = emitLine[i : i+120]
	}
	if !strings.Contains(emitLine, "\"warn\"") {
		t.Errorf("训练区间之外该是 warn 级（info 看不见）：%s", strings.TrimSpace(line))
	}
}

// funcBody 取一个函数的**函数体**源码（从签名所在行到下一个行首 `func `/`}` 收尾）。
//
// 用途：判据只关心"这个函数里怎么写的" —— 拿整份文件去 Contains 会把别处的
// `comfyengine.` 也算进来（那种门是摆设）。
func funcBody(t *testing.T, rel, sig string) string {
	t.Helper()
	src := readSrc(t, rel)
	at := strings.Index(src, sig)
	if at < 0 {
		t.Fatalf("找不到 %s（判据的前提没了）", sig)
	}
	rest := src[at:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("%s 的函数体没找到收尾（门会看起来通过、其实什么都没看）", sig)
	}
	return rest[:end+3]
}
