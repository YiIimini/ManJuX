package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// heal_voice_end_to_end_test.go 端到端：**真机那份方案形态**走完 heal + 装配之后，
// 提交版提示词必须是「只念这几句」，而不是「整镜没有任何台词」（2026-10-14 真机事故）。
//
// 【用户原话】「卧槽，现在角色完全没有配音了」
// 【真机取证】`人间收件箱·第01集·雨夜叩门`：盘上 `h3_prompt` 一个 `<d>` 都没有，
// 而 `HealPlan` 自称补了 11 镜 —— 补出来的尾巴落进最后一段「配乐段」，被同趟的
// `EnsureMusicNA` 整段换掉。提交版提示词于是**同时**写着「正文里有 `<d>`」与
// 「this shot has NO spoken dialogue at all」，模型照后者办 ⇒ 三个已渲镜
// 实测 mean_volume −21.6 / −27.7 / −22.9 dB：有环境声、一句台词都没有。
//
// 这条门钉的是**用户看得见的那个症状**（走错分支），不是中间某个函数调没调。
func TestHealedShotPromptStillSpeaks(t *testing.T) {
	const line = "箱子里……有东西在响。"
	body := "subject_definitions:\n" +
		"<Subject 1> is 周砚, the young locksmith of 铜锣巷七号, with the face, short black hair and " +
		"dark-blue work jacket shown in his character sheet.\n\n" +
		"summary:\n[reference generation] Close on 周砚 with his ear pressed to the old mailbox.\n\n" +
		"retention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved - face shape, hair, jacket.\n\n" +
		"detailed_description:\nCinematic, 2D-animated, Chinese donghua style with clean linework.\n" +
		"[Shot 1] At 00:00.000 a close shot holds on 周砚, rain running down his hair. " +
		"He speaks quietly to himself without lifting his head.\n\n" +
		"overall_soundscape:\nRain hissing into steam on warm metal, a dull knock from inside the box.\n\n" +
		"non_diegetic_music:\nN/A\n"

	p := project.Plan{
		Characters: []project.Character{{ID: "char_zhou", Name: "周砚", Voice: "A low clear baritone."}},
		Scenes:     []project.Scene{{ID: "scene_lane", Name: "铜锣巷七号门口"}},
		Shots: []project.Shot{{
			ID: 3, Duration: 8, Scene: "scene_lane", H3Prompt: body,
			Dialogue: []project.Line{{Speaker: "周砚", Text: line}},
		}},
	}
	project.HealPlan(&p)

	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	got := assembleShotPrompt(proj, &p, &p.Shots[0], nil, nil)

	if !strings.Contains(got, "<d>[Chinese]"+line+"</d>") {
		t.Fatalf("提交版里没有那句台词的 `<d>[Chinese]…</d>` —— 模型没有可念的东西。\n尾部：%s", tailOf(got, 300))
	}
	if !strings.Contains(got, "the ONLY human speech in this shot is") {
		t.Errorf("提交版没走「只念这几句」那一支（spokenLinesMarker 分支选错了）")
	}
	if strings.Contains(got, "NO spoken dialogue at all") {
		t.Errorf("提交版里同时写着「整镜没有任何台词」—— 模型会照它办，成片就没有人声（这正是真机症状）：\n%s",
			tailOf(got, 400))
	}
	if !strings.Contains(got, "MANDARIN ONLY") {
		t.Errorf("提交版缺语言纪律条款（MANDARIN ONLY）—— 那是 `EnsureChineseDialogue` 在正文里" +
			"看到 `<d>` 才会补的；没有它就说明台词通道没接上")
	}
}
