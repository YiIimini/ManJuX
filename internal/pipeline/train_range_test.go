package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/engine/h3api"
)

// train_range_test.go H3 **训练区间**播报的门（2026-10-11，用户：「渲染出来的视频很不如意」）。
//
// 【这道门要挡什么】官方节点把 H3 的训练区间标成 124–362 帧（5.17–15.08 秒）。平台既有口径
// 允许 4 秒镜（技能端旧包整批 4 秒，见 `internal/director/dialogue.go`），而 4 秒镜对齐后只喂
// 107 帧 —— **不在训练区间里**，这类镜的通病是「首帧正常、中后段散/糊」。
// 平台不许悄悄把窗口抬上去（那等于整本拒收旧包），所以只能**如实播报**：
// 判据必须在真正要渲这一镜时给出，而且要按**喂给模型的帧数**判 —— 那个数只能问**当前那个引擎**。

// ① 判据本体：喂给模型的帧数落在训练区间之外 ⇒ 给一句话；区间内 ⇒ 一个字都不说。
//
// **两个引擎的口径必须各按各的判**（本地 17k+5 网格 / 云端整数秒且夹 5~15 秒）：
// 5.0 秒那一档两边**结论相反**（本地对齐后正好 124 = 进区间；云端提交 120 = 低于下限）。
func TestTrainRangeNoteJudgesTheFramesTheModelGets(t *testing.T) {
	const comfy, api = "comfy", h3api.Name
	cases := []struct {
		name        string
		frames      int // 计划帧数（shotFrames(shot) = 时长 × 24）
		seam        bool
		eng         string
		wantNote    bool
		mustContain []string
		whyZh       string
	}{
		{name: "4 秒镜", frames: 96, eng: comfy, wantNote: true, mustContain: []string{"107", "124"},
			whyZh: "真机旧包整批 4 秒镜：对齐后 107 帧 < 124 ⇒ 必须报（这类镜画面容易在中后段散）"},
		{name: "5 秒镜", frames: 120, eng: comfy, wantNote: false,
			whyZh: "120 帧对齐后**正好 124**（下限）⇒ 不许报 —— 拿计划帧数判会在这里误报"},
		{name: "12 秒镜", frames: 288, eng: comfy, wantNote: false, whyZh: "对齐后 294 帧，在区间内"},
		{name: "15 秒镜", frames: 360, eng: comfy, wantNote: false, whyZh: "对齐后 362 帧，正好压在上限上（不许报）"},
		{name: "16 秒镜", frames: 384, eng: comfy, wantNote: true, mustContain: []string{"396", "362"},
			whyZh: "对齐后 396 帧 > 362 ⇒ 尾部是崩坏高发区，必须报"},
		{name: "4 秒接缝镜", frames: 96, seam: true, eng: comfy, wantNote: false,
			whyZh: "接缝要多渲 22 帧 pinned 头（AlignLength(118)=124 帧）⇒ 喂给模型的够长，不许报"},
		{name: "16 秒接缝镜", frames: 384, seam: true, eng: comfy, wantNote: true, mustContain: []string{"413"},
			whyZh: "接缝同样要按 +22 算（413 帧）"},
		{name: "零帧", frames: 0, eng: comfy, wantNote: false, whyZh: "没时长就没结论（不许编）"},
		{name: "负帧", frames: -24, eng: comfy, wantNote: false, whyZh: "脏数据不许编结论"},

		// ── 云端（h3api）：只吃整数秒、且被夹在 5~15 秒 ⇒ 与本地**同一份计划、不同结论** ──
		{name: "云端 4 秒镜", frames: 96, eng: api, wantNote: true, mustContain: []string{"120", "124"},
			whyZh: "云端把 4 秒抬成 5 秒 = 120 帧（不是本地的 107）⇒ 仍低于下限，必须报、且数字要说对"},
		{name: "云端 5 秒镜", frames: 120, eng: api, wantNote: true, mustContain: []string{"120"},
			whyZh: "**与本地结论相反**：云端提交 120 帧（<124）该报，本地对齐 124 不该报 —— 只按本地说会漏报这一档"},
		{name: "云端 6 秒镜", frames: 144, eng: api, wantNote: false, whyZh: "云端 144 帧（≥124）在区间内"},
		{name: "云端 15 秒镜", frames: 360, eng: api, wantNote: false, whyZh: "云端 15 秒 = 360 帧，在上限内"},
		{name: "云端 16 秒镜", frames: 384, eng: api, wantNote: false,
			whyZh: "云端把 16 秒夹回 15 秒 = 360 帧（不是本地的 396）⇒ 在区间内，不许照本地那套报"},
	}
	for _, c := range cases {
		got := trainRangeNote(c.frames, c.seam, c.eng)
		if c.wantNote && got == "" {
			t.Errorf("%s（%d 帧 / 接缝=%v / 引擎=%s）：该报却没报 —— %s", c.name, c.frames, c.seam, c.eng, c.whyZh)
			continue
		}
		if !c.wantNote && got != "" {
			t.Errorf("%s（%d 帧 / 接缝=%v / 引擎=%s）：不该报却报了：%s —— %s", c.name, c.frames, c.seam, c.eng, got, c.whyZh)
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
		t.Error("判据没走本地引擎那条口径（comfyengine.SeamRenderLength）—— 自己算一遍 = 两处公式迟早分叉")
	}
	if !strings.Contains(body, "h3api.SubmittedFrames(plannedFrames)") {
		t.Error("判据没走云端那条口径（h3api.SubmittedFrames）—— 5.0 秒那一档会漏报")
	}
	if !strings.Contains(body, "engName == h3api.Name") {
		t.Error("没有按引擎分支（且不许写死 \"api\"：引擎名有唯一出处 h3api.Name）")
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
	// 这道门的判据本身也得站得住：真机那两个数就是引擎给的（124/362）。
	if engine.H3TrainMinFrames != 124 || engine.H3TrainMaxFrames != 362 {
		t.Errorf("训练区间被改成了 %d–%d 帧：真机（官方节点标注）是 124–362，改了要重跑一遍真机",
			engine.H3TrainMinFrames, engine.H3TrainMaxFrames)
	}
	// 两个引擎的时长口径都钉住（改一处就会让上面的行为表变成假红/假绿）。
	if comfyengine.SeamRenderLength(96, false) != 107 || comfyengine.SeamRenderLength(96, true) != 124 {
		t.Errorf("本地那条口径变了：96 帧普通渲 %d、接缝渲 %d（真机是 107 / 124）",
			comfyengine.SeamRenderLength(96, false), comfyengine.SeamRenderLength(96, true))
	}
	if h3api.SubmittedFrames(96) != 120 || h3api.SubmittedFrames(120) != 120 || h3api.SubmittedFrames(384) != 360 {
		t.Errorf("云端那条口径变了：96→%d、120→%d、384→%d（真机是 120 / 120 / 360）",
			h3api.SubmittedFrames(96), h3api.SubmittedFrames(120), h3api.SubmittedFrames(384))
	}
}

// ③ 接线：播报必须发生在**真要渲这一镜**的时候，且在引擎调用之前（事后说就没用了）。
func TestTrainRangeNoteIsWiredIntoTheRenderPass(t *testing.T) {
	src := readSrc(t, "stages_render.go")
	const call = "trainRangeNote(wantFrames, seamHere, engName)"
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
	// 接缝判定必须先算出来（seamHere），引擎名也要已定（engName）—— 否则喂入帧数会算错一档。
	for _, dep := range []string{"seamHere = seamAllowed(", "engName := rp.Engine"} {
		i := strings.Index(src, dep)
		if i < 0 || i > at {
			t.Errorf("%s 必须在播报之前算好 —— 否则喂入帧数会被算错", dep)
		}
	}
	// 播报级别必须是 warn：这是"画面不如意"的一条确定性根因（不是流水信息），
	// 而且它会改变用户下一步的动作（改时长/拆镜/换引擎），不该混在 info 里。
	emitLine := src[at:]
	if i := strings.Index(emitLine, "c.emit("); i >= 0 && i < 200 {
		emitLine = emitLine[i : i+120]
	}
	if !strings.Contains(emitLine, "\"warn\"") {
		t.Errorf("训练区间之外该是 warn 级：%s", strings.TrimSpace(lineOf(src, at)))
	}
}

// ④ 生成侧不许自己产出"区间外"的镜：`FitDialogueDurations` 的两类调用方必须传**不同的下限**。
//
// 混用一个数就会犯两个方向相反的错：生成侧用 4 ⇒ 平台自己产出 4 秒镜（不在训练区间里）；
// 导入侧用 5 ⇒ 把技能端旧包的 4 秒镜悄悄抬长（单方面改契约）。
func TestGenerationFloorAndImportFloorStayApart(t *testing.T) {
	gen := readSrc(t, "stages_script.go")
	if !strings.Contains(gen, "director.FitDialogueDurations(plan, director.GenMinShotSeconds)") {
		t.Error("生成路径没传 GenMinShotSeconds —— 平台自己会产出 4 秒镜（喂给模型的帧数不在训练区间里）")
	}
	imp := readSrc(t, "../../internal/server/handlers_shot.go")
	if !strings.Contains(imp, "director.FitDialogueDurations(plan, 0)") {
		t.Error("导入路径没传窗口下限（0）—— 技能端旧包的 4 秒镜会被悄悄抬长（单方面改契约）")
	}
	if strings.Contains(codeLines(imp), "GenMinShotSeconds") {
		t.Error("导入路径不该用生成下限：导入必须空转，不静默改用户的方案")
	}
}

// funcBody 取一个函数的**函数体**源码（从签名所在行到下一个行首 `}` 收尾）。
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
