package project

import (
	"strings"
	"testing"
)

// prompt_cjk_gate_test.go 汉字卫生门的**击穿用例**（与技能侧 manju_plan.py 同一门、同一口径）。
//
// 门必须证明两件事（与 plan_contract_test.go 同一条纪律）：
//  1. 它真的会拦 —— 阈值上下各一条边界用例；
//  2. 它不乱拦 —— 主体名占满、`<d>` 内台词整段中文都不许报（误拦的规则比没有规则更糟）。
//
// 依据：官方 ON-SCREEN TEXT 规则「没被英文引号括起来的文字就是模型画成字形的来源」，
// 而汉字合法的位置只有 `<d>[Chinese]…</d>` 与**主体名**。判据与阈值见 prompt_cjk.go。

// cjkIssues 取问题表里消息含 sub 的条目。
//
// 过滤词用「可避免」而不是「可避免的汉字」：fail 级消息里那三个字带 `**` 强调标记
// （平台的告警文案风格），逐字匹配会漏掉 fail 级 —— 门一漏，用例就变成"只测了 warn"。
func cjkIssues(issues []Issue, sub string) []Issue {
	var out []Issue
	for _, it := range issues {
		if strings.Contains(it.Msg, sub) {
			out = append(out, it)
		}
	}
	return out
}

// cjkShotPlan 造一个"除此之外都合规"的方案：只替换 h3_prompt。
//
// 必须让 h3_prompt 自带 `<d>`：契约里「有台词却没有任何 <d>」是 fail，那条与本门无关，
// 混进来会让边界用例读不出真正的原因。同理，替换进来的提示词要过 compliantH3 补上
// 主视角/建立镜头串 —— 覆盖率类判据（§1.1）与本门无关，不该出现在这里的报告里。
func cjkShotPlan(h3 string) *Plan {
	p := samplePlan()
	p.Shots[0].H3Prompt = compliantH3(h3)
	return p
}

// TestValidatePlanCJKBoundaries 阈值口径与技能侧一致：`<d>` 外可避免汉字 >60 warn、>200 fail。
// 60/61/200/201 四条边界各一条 —— 阈值差一个数就是两侧走散。
func TestValidatePlanCJKBoundaries(t *testing.T) {
	cases := []struct {
		n     int
		level string // "" = 一条都不该有
	}{
		{n: 60, level: ""},
		{n: 61, level: "warn"},
		{n: 200, level: "warn"},
		{n: 201, level: "fail"},
	}
	for _, tc := range cases {
		p := cjkShotPlan("<d>[Chinese]我准了。</d>\n" + strings.Repeat("字", tc.n))
		got := cjkIssues(ValidatePlan(p), "可避免")
		if tc.level == "" {
			if len(got) != 0 {
				t.Fatalf("<d> 外 %d 个汉字不该报（阈值 60），实际 %v", tc.n, PlanIssuesText(got))
			}
			continue
		}
		if len(got) != 1 || got[0].Level != tc.level {
			t.Fatalf("<d> 外 %d 个汉字应为 %s 级 1 条，实际 %v",
				tc.n, tc.level, PlanIssuesText(ValidatePlan(p)))
		}
	}
}

// TestValidatePlanCJKSubtractsSubjectNames **主体名占满时不误报**（本门最容易翻车的地方）。
//
// 官方要求「用文字点名主体」（渲染期 PICTURE MANIFEST 靠它对编号），而提示词里一个名字
// 会被反复提到：9 个角色 × 3 字名 × 8 次出现 = 216 个汉字 —— 已经压过 fail 线（200），
// 全部来自主体名，一个都不该报。扣不干净就是天天误报，真出问题时反被淹掉。
func TestValidatePlanCJKSubtractsSubjectNames(t *testing.T) {
	names := []string{"顾青禾", "沈砚之", "陆见微", "裴照野", "苏挽星", "贺兰辞", "温雪堂", "谢长风", "白露台"}
	p := samplePlan()
	p.Characters = nil
	p.Scenes[0].Name = "旧城派送站棚" // 场景名同样是主体名，同样要扣
	var sb strings.Builder
	sb.WriteString("<d>[Chinese]我准了。</d>\n")
	for i, n := range names {
		p.Characters = append(p.Characters, Character{
			// 夹具照真实形态写：规范要求人形卡带比例锚点（8.5 头身）——没有会被"卡完备性"门抓住
			ID: "char_" + n, Name: n, Kind: "human", Voice: "voice",
			ImagePrompt: "eight and a half heads tall, lean, straight-backed; " + n,
		})
		for k := 0; k < 8; k++ { // subject_definitions / retention_analysis / detailed_description 里反复点名
			sb.WriteString("<Subject " + string(rune('1'+i)) + "> is " + n + " in <Picture " +
				string(rune('1'+i)) + ">, front sheet.\n")
		}
	}
	p.Shots[0].Characters = []string{"char_" + names[0]}
	// 夹具照真实形态写：出场角色必须有画面位置（官方分镜表 Character Positions 子字段）
	p.Shots[0].Positions = map[string]string{"char_" + names[0]: "画面中央、中景、面朝镜头"}
	// 台词一并清掉：本用例只测汉字门，而样例台词「阿甲」不在我刚换上的角色表里，
	// 会额外报一条无关 warn（说话人不在角色表），把"零 issue"的判据搅浑。
	p.Shots[0].Dialogue = nil
	p.Shots[0].Narration = ""
	p.Shots[0].H3Prompt = compliantH3(sb.String())

	// 先证明这份提示词里的汉字确实压过了 fail 线（否则用例是空的）
	if n := CountCJK(p.Shots[0].H3Prompt); n <= PlanCJKFailOutsideDialogue {
		t.Fatalf("用例无效：提示词只有 %d 个汉字，没压过 fail 线 %d", n, PlanCJKFailOutsideDialogue)
	}
	if issues := ValidatePlan(p); len(issues) != 0 {
		t.Fatalf("主体名占满时不该有任何 issue（名字必须全部扣除），实际：%v", PlanIssuesText(issues))
	}

	// 反向：主体名之外真灌了中文美术卡 → 必须照拦（名字不许当"免罚额度"用）
	p2 := samplePlan()
	p2.Shots[0].H3Prompt = p.Shots[0].H3Prompt + strings.Repeat("主色/结构/记忆点/光线。", 30)
	if fails := PlanFails(ValidatePlan(p2)); len(cjkIssues(fails, "可避免")) != 1 {
		t.Fatalf("主体名之外的中文必须照拦，实际：%v", PlanIssuesText(ValidatePlan(p2)))
	}
}

// TestValidatePlanCJKProtectsDialogue `<d>` 内的中文台词不计入（它是官方指定的"说"通道，
// 且**必须逐字保留** —— H3 拿它出原生中文配音）。
func TestValidatePlanCJKProtectsDialogue(t *testing.T) {
	p := cjkShotPlan("<d>[Chinese]" + strings.Repeat("字", 500) + "</d>\n[Shot 1] she speaks.")
	if issues := ValidatePlan(p); len(issues) != 0 {
		t.Fatalf("<d> 内的台词不该计入汉字卫生门，实际：%v", PlanIssuesText(issues))
	}
	// 跨行台词同样受保护（不能只认单行）
	p2 := cjkShotPlan("<d>[Chinese]第一行\n第二行</d>\n" + strings.Repeat("字", 201))
	if fails := cjkIssues(PlanFails(ValidatePlan(p2)), "可避免"); len(fails) != 1 {
		t.Fatalf("跨行 <d> 必须整块受保护、块外 201 字照拦，实际：%v", PlanIssuesText(ValidatePlan(p2)))
	}
}

// TestValidatePlanKeyframePromptCJK keyframe_prompt 是首帧 T2I 静帧提示词：它没有 `<d>`
// 这条合法通道，图生图模型画中文几乎必然成乱码 —— 出现任何汉字都 warn（技能侧同一口径）。
func TestValidatePlanKeyframePromptCJK(t *testing.T) {
	p := cjkShotPlan("<d>[Chinese]我准了。</d>\n[Shot 1] she nods.")
	p.Shots[0].KeyframePrompt = "A slim young man, front-facing, plain background, 雨中的院子"
	got := cjkIssues(ValidatePlan(p), "keyframe_prompt")
	if len(got) != 1 || got[0].Level != "warn" {
		t.Fatalf("keyframe_prompt 有汉字应 warn 1 条，实际：%v", PlanIssuesText(ValidatePlan(p)))
	}
	// 全 ASCII 的首帧提示词（技能侧 v2.3 清洗后的形态）不许报
	p.Shots[0].KeyframePrompt = "A slim young man in a rain-soaked courtyard, front-facing, plain background"
	if got := cjkIssues(ValidatePlan(p), "keyframe_prompt"); len(got) != 0 {
		t.Fatalf("全 ASCII 的 keyframe_prompt 不该报：%v", PlanIssuesText(got))
	}
}

// TestCJKOutsideDialogueMatchesSkillRule 判据本体：保护 `<d>`、扣主体名、只认基本区汉字。
//
// 与技能侧 manju_plan.py 的 cjk_outside_d() 逐条对齐（两侧跑同一张规则表）。
func TestCJKOutsideDialogueMatchesSkillRule(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		names []string
		want  int
	}{
		{"纯英文", "detailed_description: she nods.", nil, 0},
		{"块外中文", "she says it 很轻很轻地", nil, 5},
		{"块内中文不计", "she says: <d>[Chinese]我不签。</d>", nil, 0},
		{"跨行块内不计", "<d>[Chinese]我不\n签。</d> tail", nil, 0},
		{"主体名扣除", "<Subject 1> is 沈砚 in <Picture 1>", []string{"沈砚"}, 0},
		{"主体名全部出现都扣", "沈砚走进来,沈砚坐下。", []string{"沈砚"}, 5}, // "走进来,坐下。" 里的字
		{"中文标点不算汉字", "a plaque reads 「营业中」", nil, 3},
		{"扩展区不算(与技能侧同为基本区)", "𠀀𠀁", nil, 0},
	}
	for _, tc := range cases {
		if got := CJKOutsideDialogue(tc.text, tc.names); got != tc.want {
			t.Errorf("%s：期望 %d，实际 %d", tc.name, tc.want, got)
		}
	}
}

// TestTailUnprotectedCJKRuns 尾部窗口判据本体（pipeline 的收尾条款闸门用它）。
func TestTailUnprotectedCJKRuns(t *testing.T) {
	names := []string{"翻篇"}
	// ① 窗口内有块外汉字串 → 逐段报出来（非汉字字符断开成串，报错时能直接指出是哪一段）
	if got := TailUnprotectedCJKRuns("english notes here. 主色/结构/记忆点", 300, names); len(got) != 3 ||
		got[0] != "主色" || got[2] != "记忆点" {
		t.Fatalf("应报出窗口内的无防护汉字串，实际 %v", got)
	}
	// ② 汉字在窗口之外 → 不报（判据只看尾部）
	long := "乱七八糟的中文美术卡在这里。" + strings.Repeat("x", 400)
	if got := TailUnprotectedCJKRuns(long, 300, names); len(got) != 0 {
		t.Fatalf("窗口之外的汉字不该报，实际 %v", got)
	}
	// ③ 窗口内的 `<d>` 台词与主体名 → 不报（先遮罩再取窗口，标签被切在边界上也不会漏保护）
	if got := TailUnprotectedCJKRuns(strings.Repeat("x", 280)+"<d>[Chinese]我不签。</d>", 300, names); len(got) != 0 {
		t.Fatalf("<d> 内的台词不该报，实际 %v", got)
	}
	if got := TailUnprotectedCJKRuns(strings.Repeat("x", 290)+"翻篇", 300, names); len(got) != 0 {
		t.Fatalf("主体名不该报，实际 %v", got)
	}
}
