package project

import (
	"strings"
	"testing"
)

// speech_test.go 生成规范 §6.6 的**两个方向**都要判住（最容易被搞错的一节）：
//
//	真画外（inner/system/narration）→ **必须**写「嘴唇完全闭合」；
//	画面里有人在说（speech）        → **绝不**能写那句（写了就是哑剧）。
//
// 而且判据必须**语义优先**：规范原话说旧管线那个 `narration` 字段
// 「其实是没归到角色卡的话语，大量是别人在说话」（实测 739 镜里约 47% 是 speech、只 2% 真旁白）
// ⇒ 不能按「字段非空」推，`vo_kind` 在就必须以它为准。
func TestSpeechKindIsLegal(t *testing.T) {
	p := voPlan()
	p.Shots[0].VoKind = VoKindSpeech
	p.Shots[0].ChibiIDs = nil
	p.Shots[0].H3Prompt = "detailed_description:\nGUOFENG. [Shot 1] he speaks, mouth moving, lip sync matches."
	p.Shots = p.Shots[:1]
	_, warns := voIssues(t, p)
	if hasMsg(warns, "取值不认识") {
		t.Errorf("speech 是合法取值（规范 §6.6 五值之一）：%v", warns)
	}
	if hasMsg(warns, "哑剧") {
		t.Errorf("没写闭嘴句就不该报哑剧：%v", warns)
	}
}

func TestSpeechWithClosedLipsIsMime(t *testing.T) {
	p := voPlan()
	p.Shots[0].VoKind = VoKindSpeech
	p.Shots[0].ChibiIDs = nil
	// 画面里有人在说，正文却写了闭嘴句 ⇒ 哑剧
	p.Shots[0].H3Prompt = "detailed_description:\nGUOFENG. [Shot 1] he speaks to her. The on-screen " +
		"characters' lips remain completely closed while that voice is heard."
	p.Shots = p.Shots[:1]
	fails, warns := voIssues(t, p)
	if len(fails) != 0 {
		t.Fatalf("哑剧是 warn（不拦导入，但要吵）：%v", fails)
	}
	if !hasMsg(warns, "哑剧") {
		t.Errorf("speech 镜写了闭嘴句必须报哑剧：%v", warns)
	}
}

// TestHasVoiceoverPrefersVoKind 判据优先级：`vo_kind` 在 ⇒ 以它为准。
func TestHasVoiceoverPrefersVoKind(t *testing.T) {
	// speech + narration 字段非空（旧管线那种"没归到角色卡的话语"）⇒ **不是**画外音
	sh := Shot{VoKind: VoKindSpeech, Narration: "三年了，你还敢出门。"}
	if HasVoiceover(sh) {
		t.Error("vo_kind=speech 时不该按 narration 字段判成画外音（会写出哑剧）")
	}
	// 三种真画外都要认
	for _, k := range []string{VoKindInner, VoKindSystem, VoKindNarration} {
		if !HasVoiceover(Shot{VoKind: k}) {
			t.Errorf("vo_kind=%s 应判为画外音", k)
		}
	}
	// vo_kind 空 ⇒ 退回字段启发式（老方案）
	if !HasVoiceover(Shot{Narration: "旁白一句"}) {
		t.Error("没有 vo_kind 时要退回字段启发式（老方案仍要能补闭嘴句）")
	}
	if HasVoiceover(Shot{Dialogue: []Line{{Speaker: "阿甲", Text: "x"}}}) {
		t.Error("画面内对白不是画外音")
	}
}

// TestEnsureVoiceoverClauseSkipsSpeech 后处理补闭嘴句也要认 vo_kind（否则它会亲手造出哑剧）。
func TestEnsureVoiceoverClauseSkipsSpeech(t *testing.T) {
	sh := &Shot{VoKind: VoKindSpeech, Narration: "他在跟她说话。", H3Prompt: "x"}
	_ = sh
	// pipeline 侧的 EnsureVoiceoverClause 走 HasVoiceover —— 这里只钉判据本身，
	// 免得两处各写一遍（pipeline 那条有自己的测试）。
	if HasVoiceover(*sh) {
		t.Fatal("speech 镜不该被判成需要补闭嘴句")
	}
	if !strings.Contains(VoiceoverClauseMarker, "lips") {
		t.Error("幂等判据串不能改（两侧都靠它）")
	}
}
