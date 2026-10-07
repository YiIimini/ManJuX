package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// —— 配音侧：把每句台词的声线绑成**权威文本**（2026-10-07 用户「配音存在乱配音」）——
//
// 夹具＝**真机第01集逐句抄下来的引述**，一字不改（下面这些字符串就是仓库里那份 plan.json 的原话）。
// 为什么必须照抄：这条修复要判的是"模型自己写的改写版能不能被换掉"，
// 自己编一句"in a clear voice"测不到真机那 11 种措辞。

// cardVoiceQiqi 真机角色卡上祁七的 voice（逐字）。
const cardVoiceQiqi = "A cool, even, slightly hollow young female voice, measured and clipped, " +
	"each word read out flatly like a calibration readout rather than a feeling."

func realVoicePlan() *project.Plan {
	return &project.Plan{
		Characters: []project.Character{
			{ID: "char_qiqi", Name: "祁七", Kind: "android", Voice: cardVoiceQiqi},
		},
	}
}

// TestBindLineVoicesRebindsTheModelsRewording 模型每镜改写的声线必须被换掉。
func TestBindLineVoicesRebindsTheModelsRewording(t *testing.T) {
	plan := realVoicePlan()
	shot := &project.Shot{
		ID: 2, VoKind: "speech", Characters: []string{"char_qiqi"},
		Dialogue: []project.Line{
			{Speaker: "舱内AI", Text: "该单元不在名册上。", OS: true},
			{Speaker: "祁七", Text: "名册最后一行是多少。"},
		},
		H3Prompt: "detailed_description:\n" +
			"[Shot 1] The bay's overhead speaker voice, a flat, sexless synthetic voice, " +
			"even-paced and breathless, off-screen (S1) says: <d>[Chinese]该单元不在名册上。</d>\n" +
			"[Shot 2] Her lights move once, jaw level. She asks, in a clear, cool young voice " +
			"with a slight mechanical edge (S2) says: <d>[Chinese]名册最后一行是多少。</d>",
	}
	out, bound, unresolved := BindLineVoices(shot.H3Prompt, plan, shot)
	if bound != 2 || unresolved != 0 {
		t.Fatalf("应重绑 2 句、认不出 0 句，实得 bound=%d unresolved=%d\n%s", bound, unresolved, out)
	}
	// ① 角色卡上的原文（逐字）必须出现在引述里；卡片值末尾的句号会被吞（免得与语法的冒号撞成 ".："）。
	if !strings.Contains(normLoose(out), normLoose(strings.TrimRight(cardVoiceQiqi, "."))) {
		t.Errorf("祁七那句没绑上角色卡原文：\n%s", out)
	}
	// ② 无卡说话人（舱内AI）用平台默认合成声，并保留"画外"与它自己的 (Sx)。
	if !strings.Contains(out, "舱内AI (S1) says off-screen in "+syntheticVoiceDefault+": ") {
		t.Errorf("舱内AI 那句没绑上平台默认合成声：\n%s", out)
	}
	if !strings.Contains(out, "祁七 (S2) says in ") {
		t.Errorf("祁七那句的引述形状不对：\n%s", out)
	}
	// ③ 模型自己写的那几种措辞**一个字都不该留下**（H3 拿它当音频条件，留着音色就跟着它走）。
	for _, leftover := range []string{
		"a clear, cool young voice",
		"slight mechanical edge",
		"a flat, sexless synthetic voice, even-paced and breathless, off-screen (S1)",
	} {
		if strings.Contains(out, leftover) {
			t.Errorf("模型改写的声线还留在提示词里：%q\n%s", leftover, out)
		}
	}
	// ④ 幂等：再跑一次不再改（否则每装配一次就把引述重写一遍、指纹乱跳）。
	again, bound2, _ := BindLineVoices(out, plan, shot)
	if bound2 != 0 || again != out {
		t.Errorf("重绑不幂等：第二次 bound=%d、文本%s", bound2, map[bool]string{true: "变了", false: "没变"}[again != out])
	}
}

// TestBindLineVoicesRealInnerShots 真机三种内心/Q版镜的写法都要能吃掉（画面描述要留下）。
func TestBindLineVoicesRealInnerShots(t *testing.T) {
	plan := realVoicePlan()
	cases := []struct{ name, prompt, keep string }{
		{
			"shot5 Q版在肩上说话（画面描述必须留下）",
			"on her left shoulder the palm-sized Q-version figure stands, arms folded, chin lifted, " +
				"and speaks with 祁七's own voice, off-screen: the android girl with a clear, cool young voice " +
				"and a slight mechanical edge (S2) says: <d>[Chinese]下一行是空的。连一道格线都没留。</d>",
			"the palm-sized Q-version figure stands, arms folded, chin lifted",
		},
		{
			"shot11 闭嘴句必须留下",
			"the Q-version figure rises out of the still air, arms folded, chin lifted, speaking in " +
				"祁七's own voice while her lips stay completely closed: the android girl with a clear, " +
				"cool young voice and a slight mechanical edge (S2) says: <d>[Chinese]她不知道自己为什么要挂上它。</d>",
			"her lips stay completely closed",
		},
		{
			"shot13 短句写法",
			"The palm-sized Q-version self on her left shoulder crouches, arms folded, chin up, " +
				"speaking in 祁七's own voice, cool, slightly mechanical (S2) says: <d>[Chinese]谁烧的门。外面还有活物吗。</d>",
			"The palm-sized Q-version self on her left shoulder crouches",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sh := &project.Shot{ID: 5, VoKind: "inner", Characters: []string{"char_qiqi"},
				Dialogue: []project.Line{{Speaker: "祁七", Text: textOf(tc.prompt)}}}
			out, bound, unresolved := BindLineVoices(tc.prompt, plan, sh)
			if bound != 1 || unresolved != 0 {
				t.Fatalf("应重绑 1 句，实得 bound=%d unresolved=%d\n%s", bound, unresolved, out)
			}
			if !strings.Contains(out, tc.keep) {
				t.Errorf("画面描述被误删：%q\n%s", tc.keep, out)
			}
			if !strings.Contains(out, "says off-screen in ") {
				t.Errorf("内心镜的声线没写成画外：\n%s", out)
			}
			if strings.Contains(out, "cool young voice") || strings.Contains(out, "slightly mechanical") {
				t.Errorf("模型改写的声线还留着：\n%s", out)
			}
		})
	}
}

// TestBindLineVoicesLeavesWhatItCannotResolve 认不出来的一律不动（宁可让模型发挥，也不塞错声线）。
func TestBindLineVoicesLeavesWhatItCannotResolve(t *testing.T) {
	plan := realVoicePlan()
	sh := &project.Shot{ID: 1, VoKind: "speech", Characters: []string{"char_qiqi"},
		Dialogue: []project.Line{{Speaker: "祁七", Text: "名册最后一行是多少。"}},
		H3Prompt: "… she says in a clear, cool young voice (S2) says: <d>[Chinese]台词被模型改成了别的字。</d>"}
	out, bound, unresolved := BindLineVoices(sh.H3Prompt, plan, sh)
	if bound != 0 || unresolved != 1 {
		t.Fatalf("对不上的台词不该动：bound=%d unresolved=%d\n%s", bound, unresolved, out)
	}
	if out != sh.H3Prompt {
		t.Errorf("认不出却改了文本：\n%s", out)
	}
	// 规则文本里的示例标签（`<d>…</d>`、`<d></d>`、`<d>（无）</d>`）不是台词，别当句子数。
	rules := "MANDARIN ONLY: matching the [Chinese] tag inside each <d>…</d>; never write <d></d> or <d>（无）</d>"
	if got, _, _ := BindLineVoices(rules, plan, sh); got != rules {
		t.Errorf("规则文本里的示例标签被当成台词改了：\n%s", got)
	}
}

// TestShotCastingIncludesSpeakersWithoutCards 真机 shot 1：唯一说话人「舱内AI」没有角色卡 ⇒
// 原来那份 casting 块**整段缺席**，一个正在说话的人没有任何音色约束。
func TestShotCastingIncludesSpeakersWithoutCards(t *testing.T) {
	plan := realVoicePlan()
	sh := &project.Shot{ID: 1, Characters: []string{"char_qiqi"},
		Dialogue: []project.Line{{Speaker: "舱内AI", Text: "重新检测到生命反应。", OS: true}}}
	got := ShotCasting(plan, sh)
	if len(got) != 1 || got[0].Name != "舱内AI" {
		t.Fatalf("无卡说话人必须进 casting 清单，实得 %+v", got)
	}
	if got[0].Voice != syntheticVoiceDefault {
		t.Errorf("系统音该用平台默认合成声，实得 %q", got[0].Voice)
	}
	// 没台词的镜：一份 casting 都不该有（2026-09-27「小孩子乱吵」那条的回归判据）。
	if silent := ShotCasting(plan, &project.Shot{ID: 12, Characters: []string{"char_qiqi"}}); len(silent) != 0 {
		t.Errorf("没有台词的镜不该有 casting 清单：%+v", silent)
	}
}

// TestDefaultVoiceForSeparatesSystemFromNarrator 「舱内AI」同时命中 IsVoiceoverSpeaker（名字里有 AI）——
// 若先走旁白那条，一个舱内电子音会被配成"warm, low-pitched adult male narrator"（第一次重绑的实测结果）。
func TestDefaultVoiceForSeparatesSystemFromNarrator(t *testing.T) {
	if got := defaultVoiceFor("舱内AI"); got != syntheticVoiceDefault {
		t.Errorf("系统音该用合成声默认，实得 %q", got)
	}
	if got := defaultVoiceFor("旁白"); got != narratorVoiceDefault {
		t.Errorf("旁白该用旁白默认，实得 %q", got)
	}
	if got := defaultVoiceFor("家丁甲"); got != genericVoiceDefault {
		t.Errorf("认不出的说话人该用中性默认（也不能让模型每镜现编），实得 %q", got)
	}
}

// textOf 从夹具提示词里取 `<d>` 内容（测试辅助：dialogue 必须与它逐字一致）。
func textOf(prompt string) string {
	i := strings.Index(prompt, "<d>[Chinese]")
	if i < 0 {
		return ""
	}
	rest := prompt[i+len("<d>[Chinese]"):]
	j := strings.Index(rest, "</d>")
	if j < 0 {
		return ""
	}
	return rest[:j]
}
