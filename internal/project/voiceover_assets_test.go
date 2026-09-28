package project

import (
	"strings"
	"testing"
)

// voiceover_assets_test.go 画外演出素材的**确定性补全**（2026-09-28 实测驱动）。
//
// 事故：一次真跑里整个方案唯一的 fail 是「#11 vo_kind=inner 但 chibi_ids 为空」——
// 模型标了"内心镜"却没写演出素材 id，而素材本来就是按角色自动生成的（资产阶段同步出 Q 版），
// 缺的只是"对上号"。用户看到的是"又说不能渲"。
func TestFillVoiceoverAssetsInner(t *testing.T) {
	p := &Plan{
		Characters: []Character{{
			ID: "char_01", Name: "碑生", Kind: "human",
			ImagePrompt: "IDENTITY LOCK (do not change): narrow eyes, a pale brand scar; old felt cap.",
		}},
		Scenes: []Scene{{ID: "scene_bei", Name: "西坡碑林"}},
		Shots: []Shot{{
			ID: 11, Scene: "scene_bei", Characters: []string{"char_01"},
			VoKind: VoKindInner, H3Prompt: "x",
		}},
	}
	if n := FillVoiceoverAssets(p); n != 1 {
		t.Fatalf("应补 1 镜，实际 %d", n)
	}
	sh := p.Shots[0]
	if len(sh.ChibiIDs) != 1 || sh.ChibiIDs[0] != "chibi_char_01" {
		t.Fatalf("chibi_ids 应按技能侧命名补成 chibi_char_01，实际 %v", sh.ChibiIDs)
	}
	if !p.HasCharacterID("chibi_char_01") {
		t.Fatal("Q 版卡必须真的建进方案（契约查的是卡，不是自动生成的资产）")
	}
	ch, _ := findCharacter(p, "chibi_char_01")
	if ch.Kind != "chibi" {
		t.Errorf("Q 版卡的 kind 应为 chibi，实际 %q", ch.Kind)
	}
	if !strings.Contains(ch.ImagePrompt, "IDENTITY LOCK (do not change): narrow eyes") {
		t.Error("Q 版卡的提示词必须**照抄主卡的身份锁**——Q 版是同一张脸的另一套画法")
	}
	if !strings.Contains(ch.ImagePrompt, "2.5-3 head-tall") {
		t.Error("Q 版要写清头身比")
	}
	// 幂等：再调一次不该重复建卡
	if n := FillVoiceoverAssets(p); n != 0 {
		t.Errorf("幂等：第二次不该再补，实际 %d", n)
	}
	if len(p.Characters) != 2 {
		t.Errorf("不该重复建卡，实际 %d 张", len(p.Characters))
	}
	// 补完必须过契约
	if got := voiceoverRefIssues(p); len(got) > 0 {
		t.Errorf("补完后不该再有悬空引用：%v", got)
	}
}

func TestFillVoiceoverAssetsSystem(t *testing.T) {
	p := &Plan{
		Characters: []Character{{ID: "char_01", Name: "碑生", Kind: "human", ImagePrompt: "IDENTITY LOCK: x"}},
		Scenes:     []Scene{{ID: "scene_bei", Name: "西坡碑林"}},
		Shots: []Shot{{
			ID: 5, Scene: "scene_bei", Characters: []string{"char_01"},
			VoKind: VoKindSystem, H3Prompt: "x",
		}},
	}
	if n := FillVoiceoverAssets(p); n != 1 {
		t.Fatalf("应补 1 镜，实际 %d", n)
	}
	sh := p.Shots[0]
	if sh.SystemAssets["bot"] != "chibi_sys_01" || !p.HasCharacterID("chibi_sys_01") {
		t.Errorf("系统镜的 bot 要有真卡：%v", sh.SystemAssets)
	}
	panel := sh.SystemAssets["panel"]
	if panel == "" || !p.HasPropID(panel) {
		t.Fatalf("系统镜的 panel 要有真道具卡：%v", sh.SystemAssets)
	}
	if !hasStr(sh.PropIDs, panel) {
		t.Error("panel 还必须并进 prop_ids（渲染按 prop_ids 锚定外观）")
	}
	pr, _ := func() (Prop, bool) {
		for _, x := range p.Props {
			if x.ID == panel {
				return x, true
			}
		}
		return Prop{}, false
	}()
	// 面板上**不能有字**这一条不变；但**写法必须是正向的**：
	// 图片侧跑的是蒸馏模型（CFG 1.0），实测**对否定词免疫、却会把名词当指令画出来** ——
	// 写成 "no letters / no numbers / no UI text"，模型反而会把这些字画上去
	// （用户 2026-09-28 报的"莫名的文字"就是这么来的，见 artstyle.go 的说明）。
	// 所以判据是"**说了表面空白无标记**"，不是"说了不许写字"。
	low := strings.ToLower(pr.ImagePrompt)
	if !strings.Contains(low, "blank") || !strings.Contains(low, "unmarked") {
		t.Errorf("面板提示词要用**正向**写法说明表面空白无标记（写 \"no letters\" 反而会画出字）：%q",
			pr.ImagePrompt)
	}
	for _, bad := range []string{"no letters", "no numbers", "no text", "no ui text"} {
		if strings.Contains(low, bad) {
			t.Errorf("面板提示词里不许用否定词「%s」—— 图片模型会把名词画出来：%q", bad, pr.ImagePrompt)
		}
	}
	if got := voiceoverRefIssues(p); len(got) > 0 {
		t.Errorf("补完后不该再有悬空引用：%v", got)
	}
}

// voiceoverRefIssues 取契约里"画外演出资产引用"那一类的 fail（测试用）。
func voiceoverRefIssues(p *Plan) []string {
	var out []string
	for _, is := range ValidatePlan(p) {
		if is.Level != "fail" {
			continue
		}
		if strings.Contains(is.Msg, "chibi_ids") || strings.Contains(is.Msg, "system_assets") {
			out = append(out, is.Msg)
		}
	}
	return out
}
