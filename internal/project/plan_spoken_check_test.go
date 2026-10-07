package project

import (
	"strings"
	"testing"
)

// —— 分镜 → 配音 的两条判据（2026-10-07 用户「配音存在乱配音」）——
//
// ① 「谁在画面外」由**平台**判（说话人在不在本镜出场名单），不看模型标的 os；
// ② 说出去的话（`<d>`）与显示出来的字（dialogue）必须逐句对得上。

// TestOffScreenSpeakersLeavesSystemVoiceToHasVoiceover 真机第01集第 2 镜的形态：
// vo_kind=speech（画面里祁七在说），同时**舱内AI** 有一句台词、它不在本镜出场名单里。
//
// 【这里的判据是"平台靠哪条路把'画外'这件事说给 H3"】
//
//	· 整镜画外（vo_kind=inner/system/narration）→ HasVoiceover=true → 那句"嘴唇闭合"兜底；
//	· 混合镜（vo_kind=speech）→ HasVoiceover=false（不能整镜闭嘴，画面里有人在说），
//	  而 OffScreenSpeakers 又把 os 行/系统音交给 HasVoiceover 那条路
//	  （system_voice_test ⑤ 明确要求：系统音不许被报成"画面里没这个人"）⇒ 画外这件事没人说。
//
// 现在补在**逐句**上（pipeline.BindLineVoices）：画外那句写成
// "舱内AI (S1) says off-screen in <权威声线>: " —— 逐句、确定性，不依赖 vo_kind 短路。
// 这一条守的是"两边的分工"，逐句那句由 pipeline/voice_rebind_test.go 守。
func TestOffScreenSpeakersLeavesSystemVoiceToHasVoiceover(t *testing.T) {
	p := &Plan{
		Characters: []Character{{ID: "char_qiqi", Name: "祁七"}},
		Shots: []Shot{{
			ID: 2, Scene: "scene_bay", VoKind: VoKindSpeech,
			Characters: []string{"char_qiqi"},
			Dialogue: []Line{
				{Speaker: "舱内AI", Text: "该单元不在名册上。", OS: true},
				{Speaker: "祁七", Text: "名册最后一行是多少。"},
			},
		}},
	}
	if got := OffScreenSpeakers(p, p.Shots[0]); len(got) != 0 {
		t.Errorf("系统音/os 行不该被报成\"画面里没这个人\"（那是 HasVoiceover 那条路的分工），实得 %v", got)
	}
	if HasVoiceover(p.Shots[0]) {
		t.Fatal("vo_kind=speech 的混合镜不该整镜算画外（否则画面里说话的人也会被要求闭嘴）")
	}
	// 有卡角色但不在本镜 ⇒ 仍然要报（这条函数本来的用途，别顺手改坏）。
	p.Shots[0].Dialogue = append(p.Shots[0].Dialogue, Line{Speaker: "霜见", Text: "我读到的不是这样。"})
	p.Characters = append(p.Characters, Character{ID: "char_shuangjian", Name: "霜见"})
	if got := OffScreenSpeakers(p, p.Shots[0]); len(got) != 1 || got[0] != "霜见" {
		t.Errorf("有卡但不在本镜的说话人必须报出来，实得 %v", got)
	}
}

// TestSpokenTextGateCatchesChannelMismatch 逐句对账门：
// 句数不一致 ⇒ fail（平台补不了）；文本/顺序不一致 ⇒ warn（平台会确定性对齐，产出侧自己写对更稳）。
func TestSpokenTextGateCatchesChannelMismatch(t *testing.T) {
	// 真机 shot 2 的形态：同一个位置，声音念的与字幕烧的不是一句
	//（`<d>` 里模型改了词 —— 观众听到的与屏幕上的字不一样）。
	textMismatch := shotWithSpeech("祁七", "名册最后一行是多少。",
		"… She says, in a clear, cool young voice (S2) says: <d>[Chinese]名册最后一行写的是什么。</d>")
	if !hasIssue(ValidatePlan(textMismatchPlan(textMismatch)), "warn", "与 dialogue 不一致") {
		t.Error("同一个位置 `<d>` 与 dialogue 不同 ⇒ 必须报（听到的与屏上的字不一样）")
	}
	// 句数对不上（计划 2 句、正文 1 句）⇒ 必有一句只有字幕没有声音。
	countMismatch := shotWithSpeech("祁七", "名册最后一行是多少。",
		"… <d>[Chinese]名册最后一行是多少。</d>")
	countMismatch.Dialogue = append(countMismatch.Dialogue, Line{Speaker: "舱内AI", Text: "该单元不在名册上。", OS: true})
	if !hasIssue(ValidatePlan(textMismatchPlan(countMismatch)), "fail", "句数不一致") {
		t.Error("句数对不上 ⇒ 必须报（平台不知道该往哪一句上改）")
	}
	// 顺序不同（同一批句子换了先后）⇒ warn（字幕按 dialogue 顺序显示，会与听到的错位）。
	order := shotWithSpeech("祁七", "第一句。",
		"… (S2) says: <d>[Chinese]第二句。</d> … <d>[Chinese]第一句。</d>")
	order.Dialogue = []Line{{Speaker: "祁七", Text: "第一句。"}, {Speaker: "祁七", Text: "第二句。"}}
	if !hasIssue(ValidatePlan(textMismatchPlan(order)), "warn", "与 dialogue 不一致") {
		t.Error("只是顺序不同 ⇒ 报 warn（不是 fail，字句都对）")
	}
	// 标点差异**不该**报（官方要求标点归一，比标点会把同一句判成不一样）。
	punct := shotWithSpeech("祁七", "名册最后一行是多少。",
		"… (S2) says: <d>[Chinese]名册最后一行是多少</d>")
	punct.Dialogue[0].Text = "名册最后一行是多少"
	if hasIssue(ValidatePlan(textMismatchPlan(punct)), "warn", "与 dialogue 不一致") {
		t.Error("只差标点不该报：官方要求 `<d>` 里的标点归一成 , . ? !")
	}
}

// TestAlignDialogueTagsRewritesToDialogue 平台在读取边界上把 `<d>` 文本对齐到 dialogue（确定性修复）。
func TestAlignDialogueTagsRewritesToDialogue(t *testing.T) {
	p := textMismatchPlan(shotWithSpeech("祁七", "名册最后一行是多少。",
		"… (S2) says: <d>[Chinese]名册最后一行写的是什么。</d>"))
	if n := AlignDialogueTags(p); n != 1 {
		t.Fatalf("文本不一致的镜应当被对齐，实得 %d 镜", n)
	}
	got := p.Shots[0].H3Prompt
	if !strings.Contains(got, "<d>[Chinese]名册最后一行是多少。</d>") {
		t.Errorf("没有对齐到 dialogue 原文（语言标记要保留）：\n%s", got)
	}
	if n := AlignDialogueTags(p); n != 0 {
		t.Errorf("对齐不幂等：第二次又改了 %d 镜", n)
	}
	// 句数不等 ⇒ **不猜**（那是"少说了一句"，由 fail 门让模型重写）。
	p2 := textMismatchPlan(shotWithSpeech("祁七", "第一句。", "… (S2) says: <d>[Chinese]完全不相干的另一句。</d>"))
	p2.Shots[0].Dialogue = append(p2.Shots[0].Dialogue, Line{Speaker: "祁七", Text: "第二句。"})
	if n := AlignDialogueTags(p2); n != 0 {
		t.Errorf("句数不等时不该猜着对齐，实得改了 %d 镜", n)
	}
	// 接线：HealPlan（唯一读取边界）必须真的调用它 —— 否则判据写好了没人用。
	p3 := textMismatchPlan(shotWithSpeech("祁七", "名册最后一行是多少。",
		"… (S2) says: <d>[Chinese]名册最后一行写的是什么。</d>"))
	if hr := HealPlan(p3); hr.AlignTags != 1 {
		t.Errorf("HealPlan 没把对齐挂上（AlignTags=%d）—— 读取边界不对齐，成片就是听到的与写着的不一样", hr.AlignTags)
	}
	if !strings.Contains(p3.Shots[0].H3Prompt, "名册最后一行是多少。") {
		t.Error("HealPlan 走完之后 `<d>` 里应当是对齐后的原文")
	}
}

// TestSpokenTextGateCatchesSpeechShotWithoutLines 真机第01集第 12 镜：
// vo_kind=speech（"画面里有人在说"）却一句台词都没有。
func TestSpokenTextGateCatchesSpeechShotWithoutLines(t *testing.T) {
	p := textMismatchPlan(shotWithSpeech("", "", "… the camera pushes in on the slit, no speech at all."))
	p.Shots[0].Dialogue = nil
	p.Shots[0].VoKind = VoKindSpeech
	if !hasIssue(ValidatePlan(p), "warn", "却一句台词都没有") {
		t.Error("speech 镜没有台词 ⇒ 必须报（H3 会自己填人声/噪声，而字幕一条都没有）")
	}
}

// TestSpokenTextGateCatchesSpokenButUnregistered 正文里说了 dialogue 没登记的话 ⇒ warn。
func TestSpokenTextGateCatchesSpokenButUnregistered(t *testing.T) {
	p := textMismatchPlan(shotWithSpeech("", "",
		"… <d>[Chinese]这句台词没登记在 dialogue 里。</d>"))
	p.Shots[0].Dialogue = nil
	p.Shots[0].VoKind = VoKindNarration
	if !hasIssue(ValidatePlan(p), "warn", "说了计划里没有登记的话") {
		t.Error("正文里说了话、dialogue 里没登记 ⇒ 必须报（观众听得到、看不到）")
	}
}

// TestFillMissingDialogueTagsDoesNotDuplicateARewordedLine 「重复配音」的防线：
// 模型把某句**改写了词**（`<d>` 与 dialogue 句数相同、文本不同）时，平台**不许再补一句**
// 正确文本（那会让同一句在正文里出现两次，观众听到"重复配音" —— 2026-09-28 用户报过的症状）。
func TestFillMissingDialogueTagsDoesNotDuplicateARewordedLine(t *testing.T) {
	p := textMismatchPlan(shotWithSpeech("祁七", "名册最后一行是多少。",
		"… (S2) says: <d>[Chinese]名册最后一行写的是什么。</d>"))
	if n := FillMissingDialogueTags(p); n != 0 {
		t.Fatalf("句数已经对上、只是词被改了：不该再补一句（会重复配音），实得补了 %d 镜", n)
	}
	if got := strings.Count(p.Shots[0].H3Prompt, "<d>"); got != 1 {
		t.Fatalf("正文里应当仍只有 1 个 `<d>`，实得 %d 个：\n%s", got, p.Shots[0].H3Prompt)
	}
	// 真的**少说了一句**时才补（这是它本来的用途）。
	p2 := textMismatchPlan(shotWithSpeech("祁七", "名册最后一行是多少。", "… (S2) says: <d>[Chinese]第一句。</d>"))
	p2.Shots[0].Dialogue = []Line{{Speaker: "祁七", Text: "第一句。"}, {Speaker: "祁七", Text: "第二句。"}}
	if n := FillMissingDialogueTags(p2); n != 1 {
		t.Fatalf("少说了一句却没补：实得 %d 镜", n)
	}
	if !strings.Contains(p2.Shots[0].H3Prompt, "第二句。") {
		t.Error("补的那一句必须逐字用 dialogue 里的原文")
	}
}

// TestMimeGateSkipsMixedShots 混合镜（画面里有人说话 + 画面外有人说话）不算"哑剧"：
// 那句"闭着嘴"是给画外那句写的，合法且必要。
func TestMimeGateSkipsMixedShots(t *testing.T) {
	mixed := textMismatchPlan(shotWithSpeech("祁七", "名册最后一行是多少。",
		"… (S2) says: <d>[Chinese]名册最后一行是多少。</d> "+
			"… the speaker voice (S1) says: <d>[Chinese]该单元不在名册上。</d> "+
			"while the on-screen characters' lips remain completely closed"))
	mixed.Shots[0].VoKind = VoKindSpeech
	mixed.Shots[0].Dialogue = []Line{
		{Speaker: "祁七", Text: "名册最后一行是多少。"},
		{Speaker: "霜见", Text: "该单元不在名册上。"},
	}
	mixed.Characters = append(mixed.Characters, Character{ID: "char_shuangjian", Name: "霜见"})
	if hasIssue(ValidatePlan(mixed), "warn", "哑剧") {
		t.Error("混合镜里那句「闭着嘴」是给画面外那句写的 —— 不该报哑剧")
	}
	// 反方向：**没有**画面外说话人的 speech 镜写了那句 ⇒ 必须报（那才是真哑剧）。
	pure := textMismatchPlan(shotWithSpeech("祁七", "名册最后一行是多少。",
		"… (S2) says: <d>[Chinese]名册最后一行是多少。</d> while the lips remain completely closed"))
	pure.Shots[0].VoKind = VoKindSpeech
	if !hasIssue(ValidatePlan(pure), "warn", "哑剧") {
		t.Error("画面里有人在说、却写了「嘴唇完全闭合」⇒ 必须报哑剧")
	}
}

// shotWithSpeech 造一个只关心台词通道的镜。
func shotWithSpeech(speaker, text, prompt string) Shot {
	sh := Shot{ID: 1, Scene: "scene_bay", Characters: []string{"char_qiqi"},
		Positions: map[string]string{"char_qiqi": "画面中央、中景"},
		AngleZH:   "平视", View: ViewAerial45, ShotSize: "中景", Camera: "slow push-in",
		EndState: "他站在供桌前，右手抬起指向门外",
		H3Prompt: prompt}
	if speaker != "" {
		sh.Dialogue = []Line{{Speaker: speaker, Text: text}}
	}
	return sh
}

func textMismatchPlan(sh Shot) *Plan {
	return &Plan{
		Title: "配音通道用例", Logline: "一句话梗概",
		CoreBeats: []string{"核心冲突", "爽点①", "爽点②"},
		Style:     SampleStyleSentence,
		Characters: []Character{{
			ID: "char_qiqi", Name: "祁七", Kind: "human", Voice: "young female voice",
			Age: "19", Description: "性别：女\n身形：清瘦挺直",
			BoardPrompt: "reference sheet on a pure white seamless backdrop, three full-body views",
			ImagePrompt: "eight and a half heads tall; 祁七",
		}},
		Shots: []Shot{sh},
	}
}
