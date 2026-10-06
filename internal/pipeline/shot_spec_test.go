package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// shot_spec_test.go 镜头表结构字段**真的进渲染输入**（2026-10-06 审计 §八-13）。
//
// 这个包的一号缺陷类型是"字段有、门也有，但渲染端零消费"：用户在工作台把 `end_state` /
// `positions` / `offscreen` / `view` / `camera` 改掉，提示词一个字不变、渲染指纹也不变
// ⇒ **改了等于没改**，成片里看不出任何区别。判据因此落在两件事上：
//
//	① 缺什么补什么：这些字段没写进正文时，装配点要把它补进去（幂等，正文已有则不重复写）；
//	② 传导：字段变了 ⇒ 装配出来的提示词必须变（这正是"指纹失配 ⇒ 会重渲"的前提）。

func specFixture() (*project.Project, *project.Plan, project.Shot) {
	plan := &project.Plan{
		Characters: []project.Character{
			{ID: "char_shen", Name: "沈砚"},
			{ID: "char_xie", Name: "谢无咎"},
		},
	}
	shot := project.Shot{
		ID:        7,
		Duration:  6,
		Scene:     "scene_bilin",
		ShotSize:  "中景",
		Camera:    "slow push in",
		View:      "low-angle upward view",
		EndState:  "他站在碑前，手里的碑刀刃口朝下，身后的灯灭了一半",
		Positions: map[string]string{"char_shen": "画面左三分之一、前景、面朝右"},
		Offscreen: map[string]string{"char_xie": "在门外喊话，本镜不进画面"},
		H3Prompt:  "detailed_description:\nGUOFENG. [Shot 1] he stands in front of the stele.",
	}
	return &project.Project{}, plan, shot
}

// ① 缺什么补什么：正文里没有的都要补进去，而且**正文里已有的不许重复写**。
func TestEnsureShotSpecClauseFillsOnlyWhatIsMissing(t *testing.T) {
	_, plan, shot := specFixture()
	// 先把 view 与 camera 写进正文（模型照契约写了的情形）
	shot.H3Prompt += " The camera is a slow push in from a low-angle upward view."
	got := EnsureShotSpecClause(shot.H3Prompt, plan, shot)
	if !strings.Contains(got, shotSpecMarker) {
		t.Fatalf("该补的结构字段没补进来：\n%s", got)
	}
	if strings.Count(got, "low-angle upward view") != 1 {
		t.Errorf("正文里已经有这个机位词了，不该再补一行：\n%s", got)
	}
	if strings.Count(got, "slow push in") != 1 {
		t.Errorf("正文里已经有这个运镜了，不该再补一行：\n%s", got)
	}
	// end_state / positions / offscreen 此前渲染端零消费 —— 必须补进来，且**逐字**、带角色名
	for _, want := range []string{
		shot.EndState,
		"on-screen position of 沈砚 (char_shen): 画面左三分之一、前景、面朝右",
		"OFF-SCREEN in this shot — 谢无咎 (char_xie) must NOT appear in frame",
		"continuity handoff",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("装配结果里缺 %q：\n%s", want, got)
		}
	}
	// 幂等
	if again := EnsureShotSpecClause(got, plan, shot); again != got {
		t.Errorf("重复调用不该重复追加：\n%s", again)
	}
}

// ② 空字段不补（不许凭空空加一节）；一个字都没有时原样放行。
func TestEnsureShotSpecClauseSkipsEmpty(t *testing.T) {
	proj, plan, shot := specFixture()
	_ = proj
	bare := project.Shot{ID: 1, Duration: 5, H3Prompt: "detailed_description:\n[Shot 1] he opens the door."}
	if got := EnsureShotSpecClause(bare.H3Prompt, plan, bare); got != bare.H3Prompt {
		t.Errorf("字段全空时不该改动正文：\n%s", got)
	}
	// 只有 view 有值：只补 view 那一行
	onlyView := shot
	onlyView.Camera, onlyView.EndState = "", ""
	onlyView.Positions, onlyView.Offscreen = nil, nil
	onlyView.H3Prompt = "detailed_description:\n[Shot 1] he stands there."
	got := EnsureShotSpecClause(onlyView.H3Prompt, plan, onlyView)
	if !strings.Contains(got, "low-angle upward view") || strings.Contains(got, "continuity handoff") {
		t.Errorf("只该补 view 那一行：\n%s", got)
	}
	if !strings.Contains(EnsureShotSpecClause("", plan, shot), shotSpecMarker) == false {
		// 空正文：直接返回空串（不改动）
		if EnsureShotSpecClause("", plan, shot) != "" {
			t.Error("空正文不该被塞进结构条款")
		}
	}
}

// ③ **传导**：字段改了，装配出来的提示词就得变（否则指纹不变、用户改了等于没改）。
func TestShotSpecChangesReachTheRenderedPrompt(t *testing.T) {
	proj, plan, shot := specFixture()
	base := assembleShotPrompt(proj, plan, &shot, nil, nil)
	if !strings.Contains(base, shot.EndState) || !strings.Contains(base, "slow push in") {
		t.Fatalf("装配链没把结构字段带进来（这一条就是 §八-13 的本体）：\n%s", base)
	}
	// 改尾帧状态（用户在工作台做的动作）
	edited := shot
	edited.EndState = "他退回门外，灯全灭了，手上只剩半截刀柄"
	got := assembleShotPrompt(proj, plan, &edited, nil, nil)
	if got == base {
		t.Error("改了 end_state 装配结果却没变 —— 提示词与指纹都不动，等于没改")
	}
	if !strings.Contains(got, edited.EndState) {
		t.Errorf("改后的尾帧状态没进提示词：\n%s", got)
	}
	// 改离屏角色
	edited2 := shot
	edited2.Offscreen = map[string]string{"char_xie": "他从窗外只看了一眼，不进来"}
	got2 := assembleShotPrompt(proj, plan, &edited2, nil, nil)
	if got2 == base || !strings.Contains(got2, "不进来") {
		t.Errorf("改了 offscreen 没传导到提示词：\n%s", got2)
	}
	// 顺序必须**稳定**（同样的字段顺序不同、拼出来的串要一致，否则指纹会无谓失配）
	reordered := shot
	reordered.Positions = map[string]string{"char_shen": "画面左三分之一、前景、面朝右"}
	reordered.Offscreen = map[string]string{"char_xie": "在门外喊话，本镜不进画面"}
	if again := assembleShotPrompt(proj, plan, &reordered, nil, nil); again != base {
		t.Errorf("同样的字段重建之后拼出来的串不一样（map 遍历顺序泄漏进来了）——会让指纹无谓失配：\n%s", again)
	}
}
