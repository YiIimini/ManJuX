package project

import (
	"strings"
	"testing"
)

// first_shot_stamp_test.go 首镜那个**多余的时间戳**必须在唯一读取边界上被抹平（2026-10-14）。
//
// 【用户原话】「分镜脚本中的小分镜到底有没有做好？」
//
// 【真机取证】用户那份方案 `人间收件箱·第01集·雨夜叩门`：正文里 **11 镜全写成
// `[Shot 1] At 00:00.000 …`**，而官方多镜形式是**首镜不带时间戳**、从 `[Shot 2]` 起才写
// `At MM:SS.mmm`（判据见 h3_format.go 的②）。平台的门 `H3FormatViolations` 早就会把这一条
// 报成 warn —— 但**没有任何一步去改它**；而那一集渲出来的片段里内切基本没出现
// （真机 scdet 实测：镜 1 计划 2 刀只看出 1 刀、镜 2 计划 2 刀一处都没有，判据见
// internal/filmaudit/cuts.go）。这是**纯机械格式**（一个词都不动），所以按本仓纪律
// 「能确定性做到的事不要让模型自觉」收在读取边界（`HealPlan`）+ 如实播报。
//
// 四条：① 只抹首镜那一个、后面的真切点一个字都不许动；② 门自己那一层（H3FormatViolations）
// 前后对照；③ 计划侧的切点数据不许因此变样（`ParseCutsFromPrompt` 前后一致）；
// ④ 真入口（`HealPlan`）确实调了它并把计数报出来。

// realShapedShotWithFirstStamp 真机形态的正文：镜 2 的原文（逐字截取头尾）。
// 首镜带 `At 00:00.000`，后面两刀（3.000 / 5.400）是**真正的切点**。
func realShapedShotWithFirstStamp() string {
	return "detailed_description:\nCinematic, 2D-animated. \n" +
		"[Shot 1] At 00:00.000 a medium shot holds on 周砚 in the half-open doorway, " +
		"his dark-blue work jacket darkened at the shoulders by rain.\n" +
		"[Shot 2] At 00:03.000 the shot cuts to a wide framing from behind him, down the alley.\n" +
		"[Shot 3] At 00:05.400 the camera tilts down slowly from his shoulders to the step.\n\n" +
		"overall_soundscape:\nRain hammering the iron above his head.\n\n" +
		"non_diegetic_music:\nN/A\n"
}

// ① 只抹首镜那一个时间戳：结构原样、后面的真切点一个字都不动。
func TestNormalizeFirstShotTimestampDropsOnlyTheFirst(t *testing.T) {
	p := &Plan{Shots: []Shot{
		{ID: 2, H3Prompt: realShapedShotWithFirstStamp()},
		{ID: 3, H3Prompt: realShapedShotWithFirstStamp()},
		// 反面形态：本来就没写时间戳的镜（不该被当成"改过"计数）。
		{ID: 4, H3Prompt: "[Shot 1] a close shot holds on his hands.\n[Shot 2] At 00:02.500 the shot cuts to a wide view.\n"},
	}}
	if got := NormalizeFirstShotTimestamp(p); got != 2 {
		t.Fatalf("应当只有前两镜需要抹（第 3 镜本来就没写），实际 %d", got)
	}
	for _, i := range []int{0, 1} {
		got := p.Shots[i].H3Prompt
		if strings.Contains(got, "[Shot 1] At") {
			t.Errorf("镜 %d 的首镜时间戳没抹掉：%s", p.Shots[i].ID, headOfStr(got, 120))
		}
		if !strings.Contains(got, "[Shot 1] a medium shot holds") {
			t.Errorf("镜 %d 抹的方式不对（首镜标记本身必须留着）：%s", p.Shots[i].ID, headOfStr(got, 120))
		}
		// 真正的切点：`[Shot 2] At 00:03.000` / `[Shot 3] At 00:05.400` 一处都不许动。
		for _, want := range []string{"[Shot 2] At 00:03.000", "[Shot 3] At 00:05.400"} {
			if n := strings.Count(got, want); n != 1 {
				t.Errorf("镜 %d 的真切点 %q 应当原样留着 1 处，实际 %d 处", p.Shots[i].ID, want, n)
			}
		}
	}
	// 幂等：再跑一次不许有任何改动。
	before := p.Shots[0].H3Prompt
	if got := NormalizeFirstShotTimestamp(p); got != 0 {
		t.Errorf("幂等：第二次应当 0 镜，实际 %d", got)
	}
	if p.Shots[0].H3Prompt != before {
		t.Error("幂等：第二次调用改动了正文")
	}
}

// ② 门那一层的前后对照：格式门本来就报这一条（说清"平台早知道"），抹平之后就不报了。
func TestNormalizeFirstShotTimestampSilencesTheFormatGate(t *testing.T) {
	body := realShapedShotWithFirstStamp()
	before := H3FormatViolations(body)
	found := false
	for _, v := range before {
		if strings.Contains(v, "带了时间戳") {
			found = true
		}
	}
	if !found {
		t.Fatalf("前置条件不成立：格式门本该报首镜带时间戳，实际 %v", before)
	}
	p := &Plan{Shots: []Shot{{ID: 2, H3Prompt: body}}}
	if n := NormalizeFirstShotTimestamp(p); n != 1 {
		t.Fatalf("应当抹 1 镜，实际 %d", n)
	}
	for _, v := range H3FormatViolations(p.Shots[0].H3Prompt) {
		if strings.Contains(v, "带了时间戳") {
			t.Errorf("抹平之后格式门不该再报这一条：%s", v)
		}
	}
}

// ③ 计划侧的小分镜数据不许因此变样：切点时间点前后完全一致（首刀仍是 0）。
func TestNormalizeFirstShotTimestampKeepsParsedCuts(t *testing.T) {
	body := realShapedShotWithFirstStamp()
	before := ParseCutsFromPrompt(body)
	if len(before) != 3 || before[0].At != 0 {
		t.Fatalf("前置条件不成立：这份正文该解析出 3 个切点且首刀为 0，实际 %+v", before)
	}
	p := &Plan{Shots: []Shot{{ID: 2, H3Prompt: body}}}
	NormalizeFirstShotTimestamp(p)
	after := ParseCutsFromPrompt(p.Shots[0].H3Prompt)
	if len(after) != len(before) {
		t.Fatalf("切点数变了：%d ⇒ %d", len(before), len(after))
	}
	for i := range before {
		if before[i].At != after[i].At {
			t.Errorf("第 %d 刀的时间点变了：%.3f ⇒ %.3f（真切的点不许动）", i, before[i].At, after[i].At)
		}
	}
}

// ④ **真入口**：`HealPlan` 必须真的调它，并把改了几镜如实报出来（静默改文档 = 骗人）。
func TestHealPlanNormalizesAndReportsFirstShotStamp(t *testing.T) {
	p := Plan{
		Characters: []Character{{ID: "char_zhou", Name: "周砚", Voice: "A low clear baritone."}},
		Scenes:     []Scene{{ID: "scene_lane", Name: "铜锣巷七号门口"}},
		Shots: []Shot{
			healFixtureShot(2, realShapedShotWithFirstStamp(), ""),
			// 反面：这一镜本来就没写首镜时间戳 ⇒ 不许被计数。
			healFixtureShot(3, "[Shot 1] a wide shot holds on the empty lane.\n[Shot 2] At 00:02.000 the shot cuts to the mailbox.\n", ""),
		},
	}
	hr := HealPlan(&p)
	if hr.FirstShotStamps != 1 {
		t.Fatalf("`HealPlan` 该报 1 镜被抹平（另一镜本来就没写），实际 %d —— 没走真入口？", hr.FirstShotStamps)
	}
	got := p.Shots[0].H3Prompt
	if strings.Contains(got, "[Shot 1] At") {
		t.Errorf("真入口没抹掉首镜时间戳：%s", headOfStr(got, 120))
	}
	if !strings.Contains(got, "[Shot 2] At 00:03.000") {
		t.Errorf("真切点被误伤：%s", headOfStr(got, 200))
	}
	// 反面：不该动的那一镜一个字都没变。
	if !strings.Contains(p.Shots[1].H3Prompt, "[Shot 2] At 00:02.000") {
		t.Errorf("没写首镜时间戳的那一镜被改动了：%s", headOfStr(p.Shots[1].H3Prompt, 200))
	}
}

// headOfStr 截取开头一段（结论里打全文字太吵）。
func headOfStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
