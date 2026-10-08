package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// voice_clash_test.go 「女的配男音」的判据。
//
// 夹具 = **真机第01集 plan.json 里存着的引述原文**（模型自己写的那一版，2026-10-08 抄录，
// 一字未改）。四句各代表一类：模型写的性别词与卡上的权威声线**互相矛盾**、或**缺性别**。
const (
	// 镜 4：卡上是男声（实习生 = 22 岁男生），模型自己写成 "young woman's"。
	realVoiceClauseShot4 = `[Shot 3] At 00:06.000 cut to medium close-up of 韩沿 on right; right palm flat on last slip, left hand pushes green desk lamp two centimetres left. Off-screen left, young woman's light, eager, slightly breathless voice (S2): <d>[Chinese]为什么不一次汇完?</d>`
	// 镜 3：卡上是女声（韩沿），模型自己又写了一遍 "in a woman's forties voice"。
	realVoiceClauseShot3 = `[Shot 3] At 00:06.000 the shot cuts to a medium close-up, 韩沿's face on the right third turned down toward the envelope; she reads, expression unchanged. She says, in a woman's forties voice, steady and matter-of-fact (S1): <d>[Chinese]顺其自然。还是他。</d>`
	// 镜 1：无卡说话人（邮局同事）被平台配**中性**声线，模型自己写 "a man's brisk"。
	realVoiceClauseShot1 = `[Shot 3] At 00:05.000 cut to medium close-up from counter's right: 周 three-quarter profile lifts head, looks past glass screen toward street window, cold light on cheek. Off-screen right, a man's brisk, curious voice (S1): <d>[Chinese]又是那个人?</d>`
	// 镜 12：**内心音**（`dialogue` 为空、台词只写在 narration 里），模型那版引述**没有 female**。
	realInnerShot12 = `[Shot 3] At 00:05.000 the shot cuts to a wide from further back: the gate row stands empty, snow on the top step's edge. 韩沿's off-screen inner voice, the same steady, matter-of-fact forties voice (S3), says: <d>[Chinese]他今年,能自己刷卡进城了。</d>`
)

func voiceFixturePlan() *project.Plan {
	return &project.Plan{Characters: []project.Character{
		{Name: "韩沿", Voice: "Mature mid-range female voice, even and matter-of-fact"},
		{Name: "实习生", Voice: "Bright high young male voice, fast and a little breathy"},
	}}
}

func voiceFixtureShot(id int, prompt, line string) *project.Shot {
	sh := &project.Shot{ID: id, H3Prompt: prompt}
	if line != "" {
		sh.Dialogue = []project.Line{{Speaker: speakerOfLine(line), Text: line}}
	}
	return sh
}

func speakerOfLine(line string) string {
	switch line {
	case "为什么不一次汇完?":
		return "实习生"
	case "顺其自然。还是他。":
		return "韩沿"
	}
	return "邮局同事"
}

// TestVoiceClashGenderWordsAreStripped 权威声线左边的**模型自己写的性别词**必须被清掉。
//
// 【真机代价】镜 4 提交给模型的原文里同时有 `young woman's light, eager` 与
// `says in Bright high young male voice` —— H3 把这段当音频条件，用户听到的就是「女的配男音」。
func TestVoiceClashGenderWordsAreStripped(t *testing.T) {
	plan := voiceFixturePlan()
	cases := []struct {
		name    string
		prompt  string
		line    string
		mustHas string
		mustNot []string
	}{
		{"镜 4（卡上男声，模型写 young woman）", realVoiceClauseShot4, "为什么不一次汇完?",
			"Bright high young male voice", []string{"woman", "eager"}},
		{"镜 3（卡上女声，模型又写一遍 woman's forties）", realVoiceClauseShot3, "顺其自然。还是他。",
			"Mature mid-range female voice", []string{"woman's"}},
		{"镜 1（无卡→中性声，模型写 a man's brisk）", realVoiceClauseShot1, "又是那个人?",
			"", []string{"man's"}},
	}
	for _, c := range cases {
		sh := voiceFixtureShot(1, c.prompt, c.line)
		out, bound, unresolved := BindLineVoices(c.prompt, plan, sh)
		if bound != 1 || unresolved != 0 {
			t.Errorf("%s：应绑 1 句、未认 0 句，实得 %d/%d\n%s", c.name, bound, unresolved, out)
		}
		if c.mustHas != "" && !strings.Contains(out, c.mustHas) {
			t.Errorf("%s：权威声线没绑进去（缺 %q）\n%s", c.name, c.mustHas, out)
		}
		for _, bad := range c.mustNot {
			if strings.Contains(out, bad) {
				t.Errorf("%s：模型自己写的性别/年龄描述 %q 还留在权威声线旁边\n%s", c.name, bad, out)
			}
		}
	}
	// 三句都该被记一笔（如实播报"平台替模型改了措辞"）。
	if plan.VoiceClashStripped < 2 {
		t.Errorf("清掉性别描述的句数应如实记账（真机这三句至少 2 句要清，实得 %d）", plan.VoiceClashStripped)
	}
}

// TestInnerVoiceGetsTheOwnersVoice 旁白/内心音也要绑声线，且要绑到**"谁在想"那个人**的卡上。
//
// 【真机代价】镜 11/12 的台词只写在 `narration` 里（`dialogue` 为空），而 BindLineVoices 原先
// 在 `len(shot.Dialogue)==0` 时直接返回 ⇒ 一句声线都没绑，模型自己写的措辞成了音频条件
// （镜 12 那句里**没有 female** —— 女主的内心音被配成男声，就是用户报的「女的配男音」）。
func TestInnerVoiceGetsTheOwnersVoice(t *testing.T) {
	plan := voiceFixturePlan()
	sh := &project.Shot{ID: 12, VoKind: "inner", Narration: "他今年,能自己刷卡进城了。", H3Prompt: realInnerShot12}
	out, bound, unresolved := BindLineVoices(realInnerShot12, plan, sh)
	if bound != 1 || unresolved != 0 {
		t.Fatalf("内心音应被绑上 1 句、未认 0，实得 %d/%d\n%s", bound, unresolved, out)
	}
	if !strings.Contains(out, "Mature mid-range female voice") {
		t.Errorf("内心音没绑到「韩沿」卡上的声线：\n%s", out)
	}
	if strings.Contains(out, "forties voice (S3), says:") {
		t.Errorf("模型自己那句（没有 female）还在：\n%s", out)
	}
}

// TestNarrationWithoutSpeakerGetsPlatformDefault 认不出"谁在想"时**不能留着模型自己那句**，
// 要落到平台默认（旁白＝中性；系统镜＝合成声）—— 平台没有任何依据假定旁白是男性。
func TestNarrationWithoutSpeakerGetsPlatformDefault(t *testing.T) {
	plan := voiceFixturePlan()
	prompt := `[Shot 2] At 00:04.000 the shot holds. A steady, matter-of-fact forties voice, unhurried, says: <d>[Chinese]他今年,能自己刷卡进城了。</d>`
	sh := &project.Shot{ID: 12, VoKind: "inner", Narration: "他今年,能自己刷卡进城了。", H3Prompt: prompt}
	out, bound, _ := BindLineVoices(prompt, plan, sh)
	if bound != 1 {
		t.Fatalf("认不出说话人时也要绑平台默认，实得 bound=%d\n%s", bound, out)
	}
	if !strings.Contains(out, narratorVoiceDefault) {
		t.Errorf("应绑平台旁白默认（现在是中性）：\n%s", out)
	}
	if strings.Contains(narratorVoiceDefault, "male") {
		t.Error("旁白默认音色里不许写 male —— 平台没有依据假定旁白是男性（用户报的正是「女的配男音」）")
	}
	// speech 镜却没有对话记录：属于异常，如实计入 unresolved（不许猜）
	prompt2 := `[Shot 2] At 00:04.000 the shot holds. A brisk voice, says: <d>[Chinese]谁知道呢。</d>`
	sh2 := &project.Shot{ID: 2, VoKind: "speech", H3Prompt: prompt2}
	if _, _, unresolved := BindLineVoices(prompt2, plan, sh2); unresolved != 1 {
		t.Errorf("认不出说话人的 speech 镜要计入 unresolved（实得 %d）", unresolved)
	}
}
