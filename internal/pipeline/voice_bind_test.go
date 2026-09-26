package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 声线绑定闸门(2026-09-26 真机实测,用户报「配音也垃圾啊」)。
//
// 现场:提示词顶部写着一段中文声线说明(下面列 `- 沈砚: a teenage boy's voice, light and quick`),
// 而正文那句只有一个裸的引述:
//
//	Off-screen narration: the narrator says in an off-screen voiceover: <d>[Chinese]还装死？</d>
//
// 音色信息**不在说话点上**,模型每镜自己编 —— 一集里旁白能有好几种嗓子;更糟的是顶部
// 那些短英文行被模型当成"要念的一行",成片字幕里出现了 "a every' s voice light and qught"。
// 这条钉住修法:音色写进引述、清单改成不会被误当台词的形式。
func TestEnsureVoiceInlineBindsNarratorVoice(t *testing.T) {
	line := "detailed_description:\n[Shot 1] 一个壮汉抬脚朝他肩膀踩下来。 " +
		"Off-screen narration: the narrator says in an off-screen voiceover: <d>[Chinese]还装死？</d>"
	plan := &project.Plan{Characters: []project.Character{
		{ID: "c1", Name: "沈砚", Voice: "a teenage boy's voice, light and quick"},
	}}
	got := EnsureVoiceInline(line, plan, plan.Characters)

	if !strings.Contains(got, "off-screen voiceover in a warm, low-pitched adult male narrator voice") {
		t.Fatalf("旁白音色必须写进引述里(否则模型每镜现编):\n%s", got)
	}
	if !strings.Contains(got, voiceBindKey) {
		t.Fatal("必须附上官方句式的 casting 段")
	}
	if !strings.Contains(got, "沈砚 speaks with a teenage boy's voice, light and quick") {
		t.Errorf("角色音色应进 casting 段:\n%s", got)
	}
	// 幂等:反复调用不得叠加
	if n := strings.Count(EnsureVoiceInline(got, plan, plan.Characters), voiceBindKey); n != 1 {
		t.Errorf("声线绑定必须幂等,实际 %d 个 casting 段", n)
	}
	if n := strings.Count(EnsureVoiceInline(got, plan, plan.Characters), "in a warm, low-pitched"); n != 1 {
		t.Errorf("音色插进引述也只能插一次,实际 %d 次", n)
	}
}

// TestEnsureVoiceInlineFillsPlaceholder 字面占位符 `[voice description]` 不许留在提示词里 ——
// 模型会把它当内容念出来 / 打出来。
func TestEnsureVoiceInlineFillsPlaceholder(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "c1", Name: "沈砚", Voice: "a teenage boy's voice, light and quick"},
	}}
	// 指名道姓的句子:换成那个人的音色
	named := "detailed_description:\n[Shot 1] 沈砚 says in a [voice description]: <d>[Chinese]别过来。</d>"
	if out := EnsureVoiceInline(named, plan, plan.Characters); strings.Contains(out, "[voice description]") ||
		!strings.Contains(out, "沈砚 says in a teenage boy's voice, light and quick:") {
		t.Errorf("点名角色的句子应换成人名对应的音色:\n%s", out)
	}
	// 认不出是谁的句子:整句换成一个中性的、可发音的描述,但**不许**留占位符
	anon := "detailed_description:\n[Shot 1] a guard says in a [voice description]: <d>[Chinese]站住。</d>"
	if out := EnsureVoiceInline(anon, plan, plan.Characters); strings.Contains(out, "[voice description]") {
		t.Errorf("认不出说话人时也不许把占位符留在提示词里:\n%s", out)
	}
}

// TestEnsureVoiceInlineNoVoiceNoNoise 没有任何音色信息时**不许凭空加段** ——
// 空段会让模型自己编一个音色(和"不写"相比更坏)。
func TestEnsureVoiceInlineNoVoiceNoNoise(t *testing.T) {
	base := "detailed_description:\n[Shot 1] 空镜,只有风声。"
	if got := EnsureVoiceInline(base, &project.Plan{}, nil); got != base {
		t.Errorf("没有旁白也没有角色音色时不得改动提示词,实际:\n%s", got)
	}
}
