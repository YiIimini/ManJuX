package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// casting 清单必须只列**本镜真的会开口的人**(2026-09-27 真机实证)。
//
// 用户报障原话:「渲染的分镜里全是小孩子乱吵的配音」。装配点原先把 `plan.Characters`
// 整个传给 EnsureVoiceInline,于是**每一个镜头**尾部都挂着一份全剧音色表(含一个 10 岁
// 顽童的童声),而本集 54 个镜头里 17 个既没有台词也没有旁白 —— 那些镜头拿到的是
// 一份"谁该用什么嗓子"的授权清单,却没有一句可念的话,官方句式还写着
// "use exactly these timbres",模型只能自己造人声。
func TestShotSpeakersOnlyListsActualSpeakers(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "c1", Name: "沈砚", Voice: "clear young male voice"},
		{ID: "c2", Name: "壮汉", Voice: "gruff low male voice"},
		{ID: "c3", Name: "顽童", Voice: "High cheeky child's voice, around 10, jeering"},
	}}
	shot := &project.Shot{ID: 11, Dialogue: []project.Line{{Speaker: "壮汉", Text: "还装死?"}}}

	got := ShotSpeakers(plan, shot)
	if len(got) != 1 || got[0].Name != "壮汉" {
		t.Fatalf("只该列本镜说话的人(壮汉),实际:%+v", got)
	}
	for _, c := range got {
		if c.Name == "顽童" {
			t.Error("没有台词的角色(顽童)不得进 casting 清单 —— 童声就是「小孩子乱吵」的来源")
		}
	}

	// 没有台词的镜头:一个角色都不列(旁白音色由 EnsureVoiceInline 自己按旁白句给)
	silent := &project.Shot{ID: 1, Narration: "骂声汹涌。"}
	if got := ShotSpeakers(plan, silent); len(got) != 0 {
		t.Errorf("没有台词的镜头不该列任何角色音色,实际:%+v", got)
	}

	// 说话人不在角色表里(家丁/路人):不给音色 —— 宁可让模型自由发挥,也不塞错人的嗓子
	unknown := &project.Shot{ID: 3, Dialogue: []project.Line{{Speaker: "家丁", Text: "站住!"}}}
	if got := ShotSpeakers(plan, unknown); len(got) != 0 {
		t.Errorf("认不出的说话人不该匹配到任何音色,实际:%+v", got)
	}

	// 台词为空串的"行"不算发言(契约允许有 speaker 没 text 的占位)
	empty := &project.Shot{ID: 4, Dialogue: []project.Line{{Speaker: "沈砚", Text: "   "}}}
	if got := ShotSpeakers(plan, empty); len(got) != 0 {
		t.Errorf("空台词不算发言,实际:%+v", got)
	}

	// nil 安全(装配路径上 plan/shot 可能为空)
	if got := ShotSpeakers(nil, shot); got != nil {
		t.Errorf("plan 为 nil 时应返回 nil,实际:%+v", got)
	}
	if got := ShotSpeakers(plan, nil); got != nil {
		t.Errorf("shot 为 nil 时应返回 nil,实际:%+v", got)
	}
}

// 端到端(装配后):没有台词的镜头**不该出现 casting 段**;有台词的镜头只出现那一个人的音色。
func TestSilentShotCarriesNoVoiceCasting(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "c1", Name: "沈砚", Voice: "clear young male voice"},
		{ID: "c2", Name: "壮汉", Voice: "gruff low male voice"},
		{ID: "c3", Name: "顽童", Voice: "High cheeky child's voice, around 10, jeering"},
	}}
	silent := strings.Join([]string{
		"subject_definitions:",
		"<Subject 1> is 沈砚 …",
		"overall_soundscape:",
		"A low bed of street crowd murmur and scattered jeers.",
	}, "\n")
	if out := EnsureVoiceInline(silent, plan, ShotSpeakers(plan, &project.Shot{ID: 1})); strings.Contains(out, voiceBindKey) {
		t.Errorf("没有台词也没有旁白的镜头不得带 casting 段(那正是模型自造人声的授权):\n%s", out)
	}

	speaking := silent + "\n[Shot 11] 壮汉 shouts: <d>[Chinese]还装死?</d>"
	out := EnsureVoiceInline(speaking, plan, ShotSpeakers(plan, &project.Shot{
		ID: 11, Dialogue: []project.Line{{Speaker: "壮汉", Text: "还装死?"}}}))
	if !strings.Contains(out, voiceBindKey) {
		t.Error("有台词的镜头仍要有 casting 段(音色是跨镜一致性的依据)")
	}
	if strings.Contains(out, "顽童") {
		t.Errorf("没说话的角色不得出现在 casting 段里:\n%s", out)
	}
}

// 接线闸门:装配点必须传"本镜说话人",不许再传全剧角色表。
func TestShotRenderPromptPassesShotSpeakers(t *testing.T) {
	raw, err := os.ReadFile("prompts.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	// 2026-10-07：装配点从 ShotSpeakers 换成 ShotCasting（= ShotSpeakers + 无卡说话人的平台默认
	// 音色）—— 判据仍是"按镜头过滤"，且**不得**退回全剧角色表。
	if !strings.Contains(src, "ShotCasting(plan, shot)") {
		t.Error("装配点必须按镜头过滤音色清单(ShotCasting)—— 传全剧角色表会让每个镜头都背一份含童声的清单")
	}
	// 反面判据:装配点不得再把全剧角色表传进去(那正是本次修的 bug)。
	// 允许 voice_bind.go 自己保留 plan 形参(签名不变),只看装配点那一行。
	for _, ln := range strings.Split(src, "\n") {
		if strings.Contains(ln, "EnsureVoiceInline(") && strings.Contains(ln, "plan.Characters)") {
			t.Errorf("不得再把 plan.Characters 整个传给 EnsureVoiceInline:没有台词的镜头会被授权自造人声\n%s", ln)
		}
	}
}
