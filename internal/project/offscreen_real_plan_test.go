package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 真机夹具（`data/projects/我捡的团子…第01集/plan/plan.json` **逐字拷进 testdata**）：
// 19 句台词**全部**被模型标成 `os=true`（画外音），而其中 **18 句的说话人就写在本镜的
// `characters` 里**，唯一真画外音是镜 5（小灯说「亮了。」，而小灯不在镜 5 的出场名单里）。
//
// 【用户报的就是这一集】「旁白说话角色未对齐（另一人说话，其它角色动嘴）」：
// 平台的 `OffScreenSpeakers` 原来遇到 `l.OS` 就 `continue` ⇒ 19 句全被跳过 ⇒
// 镜 5 那种**真画外音反而没有**"画内的人保持闭嘴"这条强制约束；而"人在画面里亲口说"的
// 镜头也全被写成画外音。判据必须按**方案的出场名单**判，不许被模型自述的 os 骗到。
func TestRealPlanOffscreenSpeakersIgnoreModelOS(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "plan_real_voiceover.json"))
	if err != nil {
		t.Fatalf("读不到真机夹具：%v", err)
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("真机夹具不是合法方案：%v", err)
	}

	// ① 先钉住**夹具形态**：它必须真是"全标 os=true"那种（否则下面的判据没在测那件事）。
	lines, allOS := 0, true
	for _, sh := range p.Shots {
		for _, l := range sh.Dialogue {
			lines++
			if !l.OS {
				allOS = false
			}
		}
	}
	if lines != 19 || !allOS {
		t.Fatalf("真机夹具形态变了：%d 句、全标 os=%v（真机是 19 句全标 true）", lines, allOS)
	}

	// ② 镜 5 的**真画外音**必须被认出来 —— 这是承重点：os=true 不许让它逃过判据。
	if len(p.Shots) < 5 {
		t.Fatalf("夹具少了镜 5：%d 镜", len(p.Shots))
	}
	got := OffScreenSpeakers(&p, p.Shots[4])
	if len(got) != 1 || got[0] != "小灯" {
		t.Errorf("镜 5 的台词是小灯说的、而小灯不在本镜出场名单里 ⇒ 必须报成画外说话人，实得 %v", got)
	}
	// ③ 人在画面里亲口说的镜**不许**被算成画外音（镜 1 的说话人姜晚舟就在出场名单里）
	if got := OffScreenSpeakers(&p, p.Shots[0]); len(got) != 0 {
		t.Errorf("镜 1 是姜晚舟在画面里说话，不该算画外音，实得 %v", got)
	}

	// ④ 归一化要把误标的改回来，并记账（`SpeakerOSFixed` 只数**纠正**，不数补标）。
	hr := HealPlan(&p)
	if hr.SpeakerOSCleared != 18 || hr.SpeakerOSMarked != 0 {
		t.Errorf("该纠正 18 句误标（镜 5 那句是真画外音、不动）、补标 0 处，实得 纠正=%d 补标=%d",
			hr.SpeakerOSCleared, hr.SpeakerOSMarked)
	}
	if p.Shots[0].Dialogue[0].OS {
		t.Error("镜 1 的说话人在本镜 ⇒ 归一化后必须是 os=false（画内对口型）")
	}
	if !p.Shots[4].Dialogue[0].OS {
		t.Error("镜 5 的说话人不在本镜 ⇒ 归一化后仍是 os=true（画外音）")
	}
	// ⑤ 幂等：再跑一次没有可纠正的了（否则每次读盘都会重复记账）
	if hr2 := HealPlan(&p); hr2.SpeakerOSCleared != 0 || hr2.SpeakerOSMarked != 0 {
		t.Errorf("归一化不幂等：第二次仍报 纠正=%d 补标=%d", hr2.SpeakerOSCleared, hr2.SpeakerOSMarked)
	}
}

// 整集级形态：一句"画面里的人在说"都没有 ⇒ 必须报；归一化之后**不许**再报。
func TestAllOffscreenSpeakersIsFlagged(t *testing.T) {
	mk := func(off bool) *Plan {
		p := &Plan{
			Characters: []Character{{ID: "char_a", Name: "阿甲"}},
			Scenes:     []Scene{{ID: "scene_room", Name: "破庙"}},
		}
		for i := 1; i <= 4; i++ {
			p.Shots = append(p.Shots, Shot{
				ID: i, Scene: "scene_room", Characters: []string{"char_a"},
				Dialogue: []Line{{Speaker: "阿甲", Text: "我准了。", OS: off}},
			})
		}
		return p
	}
	if !hasIssue(ValidatePlan(mk(true)), "warn", "全是画外音") {
		t.Errorf("整集全是画外音（没有一句画内台词）必须报：%v", PlanIssuesText(ValidatePlan(mk(true))))
	}
	if hasIssue(ValidatePlan(mk(false)), "warn", "全是画外音") {
		t.Errorf("有画内台词时不该报：%v", PlanIssuesText(ValidatePlan(mk(false))))
	}
	// 真机夹具归一化之后必须**不再**落到这个形态（镜 1..11 那 18 句都回到了画内对口型）
	raw, err := os.ReadFile(filepath.Join("testdata", "plan_real_voiceover.json"))
	if err != nil {
		t.Fatalf("读不到真机夹具：%v", err)
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("真机夹具不是合法方案：%v", err)
	}
	HealPlan(&p)
	if hasIssue(ValidatePlan(&p), "warn", "全是画外音") {
		t.Errorf("真机方案按出场名单归一后不该报「全是画外音」：%v", PlanIssuesText(ValidatePlan(&p)))
	}
}
