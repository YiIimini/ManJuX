package project

import (
	"strings"
	"testing"
)

// pacing_contract_test.go 叙事配额与自包含（技能侧《平台侧生成规范》§6.5 / §2 / §14）。
//
// 这三条配额是"节奏"的可机检代理：此前"这一集是不是平/是不是拖"只能靠感觉说。
// 自包含那条针对**一镜一次生成** —— 模型没有上一镜的记忆，字段里写"同上"等于什么都没写。

func pacingPlan() *Plan {
	mk := func(id int, size string, dur int, hook, line string) Shot {
		sh := Shot{
			ID: id, Scene: "scene_01", ShotSize: size, Duration: dur, HookType: hook, Mode: "ref2va",
			Action: "他做了个动作", EndState: "他站在门边", Camera: "Static Shot",
			H3Prompt: "detailed_description:\nGUOFENG. [Shot 1] x",
		}
		if line != "" {
			sh.Dialogue = []Line{{Speaker: "沈砚", Text: line}}
		}
		return sh
	}
	return &Plan{
		Characters: []Character{{ID: "char_01", Name: "沈砚", ImagePrompt: "x"}},
		Scenes:     []Scene{{ID: "scene_01", Name: "临水城·城门·雨夜", Description: "四要素", ImagePrompt: "x"}},
		Shots: []Shot{
			mk(1, "远景", 6, "reveal", ""),     // 无台词
			mk(2, "中景", 6, "suspense", ""),   // 无台词 → 连续 12 秒（正好在限内）
			mk(3, "近景", 5, "climax", "别过来。"), // 戏眼 5 秒（正好够）
			mk(4, "中景", 4, "tender", "我等你。"),
		},
	}
}

func pacingWarns(t *testing.T, p *Plan) []string {
	t.Helper()
	var warns []string
	for _, is := range ValidatePlan(p) {
		if is.Level == "warn" || is.Level == "fail" {
			warns = append(warns, is.Msg)
		}
	}
	return warns
}

func TestPacingQuotasPassOnCleanPlan(t *testing.T) {
	// 只盯**本轮这两条**的文案：夹具会合法触发别的门（缺 <d> 标记、视角留空…），
	// 那些是既有行为，不该在这里被当成"配额问题"（断言写宽了就是在替别的门做决定）。
	for _, m := range pacingWarns(t, pacingPlan()) {
		for _, mine := range []string{"第一句台词", "连续无台词段", "情绪核心镜", "同上/如上"} {
			if strings.Contains(m, mine) {
				t.Errorf("合规方案不该报 %s：%s", mine, m)
			}
		}
	}
}

// TestFirstLineTooLate 首句台词太晚（>12 s）要提示：开场太久没人在说话，观众先游离。
func TestFirstLineTooLate(t *testing.T) {
	p := pacingPlan()
	// 前面全是无台词长镜：6+5+6 = 17 秒才出现第一句台词
	p.Shots[0].Duration, p.Shots[1].Duration = 6, 5
	p.Shots[2].Dialogue = nil
	p.Shots[2].Duration = 6
	// 此时最长无台词段 = 17 秒，两条都要报
	warns := pacingWarns(t, p)
	if !hasMsg(warns, "第一句台词") {
		t.Errorf("首句台词超 12 秒要报：%v", warns)
	}
	if !hasMsg(warns, "连续无台词段") {
		t.Errorf("连续无台词段超 12 秒要报：%v", warns)
	}
}

// TestKeyBeatTooShort 戏眼被快切要提示（特写/近景上的 climax/tender/expression-beat）。
func TestKeyBeatTooShort(t *testing.T) {
	p := pacingPlan()
	p.Shots[2].Duration = 3 // climax 特写只给 3 秒
	if warns := pacingWarns(t, p); !hasMsg(warns, "情绪核心镜短于") {
		t.Errorf("戏眼 <5 秒要报：%v", warns)
	}
	// 非情绪核心镜的短镜不该被牵连（中景 + setup 不在判据里）
	p2 := pacingPlan()
	p2.Shots[2].HookType, p2.Shots[2].Duration = "setup", 3
	if warns := pacingWarns(t, p2); hasMsg(warns, "情绪核心镜短于") {
		t.Errorf("只有 climax/tender/expression-beat 才算戏眼：%v", warns)
	}
}

// TestSameAsAboveIsFatal "同上/如上"= fail（一镜一次生成，写指代等于什么都没写）。
func TestSameAsAboveIsFatal(t *testing.T) {
	cases := []struct {
		name string
		mut  func(p *Plan)
		want string
	}{
		{"中文同上", func(p *Plan) { p.Shots[1].Action = "同上" }, "同上"},
		{"如上", func(p *Plan) { p.Shots[2].EndState = "如上" }, "如上"},
		{"英文", func(p *Plan) { p.Shots[3].Camera = "same as above" }, "same as above"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := pacingPlan()
			tc.mut(p)
			var fails []string
			for _, is := range ValidatePlan(p) {
				if is.Level == "fail" {
					fails = append(fails, is.Msg)
				}
			}
			if !hasMsg(fails, tc.want) {
				t.Errorf("应判 fail 且提到 %q，实际 %v", tc.want, fails)
			}
		})
	}
	if sameAsAboveIn("他站在门边") != "" || sameAsAboveIn("") != "" {
		t.Error("正常文本不该被指代判据命中")
	}
}

// TestBlankActionWarned 必填字段留空要提示（end_state 对场景首镜合法留空，见 Shot.EndState）。
func TestBlankActionWarned(t *testing.T) {
	p := pacingPlan()
	p.Shots[1].Action = ""
	if warns := pacingWarns(t, p); !hasMsg(warns, "画面动作") {
		t.Errorf("action 留空要报：%v", warns)
	}
	// end_state 留空**不在这条门的范围内**（它对场景首镜合法留空，见 Shot.EndState 的说明）：
	// 这里只钉"本门不会因为 end_state 空就报画面动作的问题"。
	p2 := pacingPlan()
	p2.Shots[0].EndState = ""
	if warns := pacingWarns(t, p2); hasMsg(warns, "画面动作") {
		t.Errorf("end_state 空不该被当成 action 空：%v", warns)
	}
}
