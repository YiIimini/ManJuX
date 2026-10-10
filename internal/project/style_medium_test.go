package project

import (
	"strings"
	"testing"
)

// 真机两份原文（逐字，2026-10-10《废柴被逐那天，我捡到一只吞吞》）：
//
//	① 本书自己的风格串（立项.json 的 art_style / 风格卡第一段代码块）——**正确的那一份**；
//	② 剧本会话自编进 plan.style 的那一句 ——**错的那一份**（把互斥介质并列）。
const (
	realBookStyle = "DREAMLIKE CHINESE ARCHITECTURAL FANTASY, cinematic vertical poster look: colossal " +
		"suspended palaces and cloud-piercing stone stairways, layered flying eaves; a semi-realistic " +
		"hand-painted finish with visible brushwork and fine film grain; palette of deep ink-blue, " +
		"jade-green, warm bronze-gold. Handsome, well-proportioned characters with RICH EXPRESSIVE faces. " +
		"This is eastern xianxia fantasy, NOT neon cyberpunk, NOT a western castle, NOT flat anime " +
		"cel-shading, NOT a plastic 3D render, NOT a child's cartoon."
	realLLMStyle = "中式梦幻磅礴的半写实绘画风，3D CG 质感：眉骨有阴影、眼尾有一线光、皮肤带暗部；" +
		"执法堂青砖高窗的晌午硬光、山门万丈石阶铺进云海。"
)

// ① 本书自己的风格串里那句 `NOT a plastic 3D render` 是**否定**（风格卡专门解释了 qwen2.1 是
// cfg=1.0、负面词无效，所以禁区必须写成"是 X，不是 Y"的正向排除句）—— 不许被当成"声明了 3D"。
// 这条是本文件最容易踩的一脚：把否定算成声明 ⇒ 每本**正确**的书都会被误报。
func TestStyleMediumIgnoreNegatedMentions(t *testing.T) {
	fams := StyleMediumFamilies(realBookStyle)
	for _, f := range fams {
		if f == "3d" || f == "2d" {
			t.Fatalf("否定句里的介质被当成声明了：families=%v（真机风格串写着 NOT a plastic 3D render / "+
				"NOT flat anime cel-shading / NOT a child's cartoon）", fams)
		}
	}
	if len(fams) != 1 || fams[0] != "painted" {
		t.Errorf("这本书只声明了「手绘/半写实」（hand-painted + brushwork），实际 %v", fams)
	}
	if n := StyleMediumConflictNote(realBookStyle); n != "" {
		t.Errorf("这本书的风格串**不矛盾**，却被判成 %q", n)
	}
}

// ② 剧本会话自编的那一句：半写实绘画 + 3D CG ⇒ 必须点名（这是这轮要接进 ValidatePlan 的判据）。
func TestStyleMediumConflictsInLLMStyle(t *testing.T) {
	fams := StyleMediumFamilies(realLLMStyle)
	if len(fams) != 2 {
		t.Fatalf("该读出两种介质（手绘 + 三维 CG），实际 %v", fams)
	}
	note := StyleMediumConflictNote(realLLMStyle)
	if note == "" {
		t.Fatal("互斥介质必须点名")
	}
	if !strings.Contains(note, "手绘") || !strings.Contains(note, "三维 CG") {
		t.Errorf("点名要说清是哪几样，实际 %q", note)
	}
}

// ③ ValidatePlan 必须把它挂成 **warn**（不是 fatal/fail：用户拍板优先那条口径不变，
// 平台只负责在渲染前说清），且**真机那句风格串**要能在方案上被报出来。
func TestValidatePlanWarnsOnMediumConflict(t *testing.T) {
	p := &Plan{Style: realLLMStyle}
	p.Shots = []Shot{{ID: 1, H3Prompt: "subject_definitions:\n<Subject 1> is A."}}
	var hit []Issue
	for _, it := range ValidatePlan(p) {
		if it.Where == "plan.style" && strings.Contains(it.Msg, "介质互相矛盾") {
			hit = append(hit, it)
		}
	}
	if len(hit) != 1 {
		t.Fatalf("该有且只有一条 plan.style 的介质矛盾告警，实际 %d 条：%v", len(hit), hit)
	}
	if hit[0].Level != "warn" {
		t.Errorf("定级该是 warn（照它渲、只点名），实际 %q", hit[0].Level)
	}
	if !strings.Contains(hit[0].Msg, "手绘") || !strings.Contains(hit[0].Msg, "三维 CG") {
		t.Errorf("告警要说清是哪几样，实际 %q", hit[0].Msg)
	}
	// 反向针：本书**正确**的那一句风格串不许触发这条告警（否则每本正确的书都会被骂）。
	ok := &Plan{Style: realBookStyle}
	ok.Shots = p.Shots
	for _, it := range ValidatePlan(ok) {
		if it.Where == "plan.style" && strings.Contains(it.Msg, "介质互相矛盾") {
			t.Errorf("正确的风格串不该被告警：%q", it.Msg)
		}
	}
}
