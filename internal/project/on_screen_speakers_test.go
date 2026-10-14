package project

import "testing"

// on_screen_speakers_test.go 「说话人在不在画面里」这条判据**只有一处出处**。
//
// 【为什么要单独钉（2026-10-12，按公众号那篇文章排查"抢台词"）】文章给的第一条硬规矩是
// 「别让好几个人同时处在正在说话的状态里」。要判它，先得判"谁在画面里"——
// 而这件事此前只有**画外**那一半（`OffScreenSpeakers` ⇒ 补"画内的人保持闭嘴"）。
// 现在 `OnScreenSpeakers` 与它共用 `speakerFrameSplit`：两半是**同一次计算**的两个结果。
// 这条门同时钉两件事：① 两半不重不漏；② 判据本身的几种真实形态都对。
func TestOnScreenSpeakersSplitsWithoutOverlap(t *testing.T) {
	plan := &Plan{Characters: []Character{
		{ID: "char_a", Name: "姜晚舟"},
		{ID: "char_b", Name: "小灯"},
		{ID: "char_c", Name: "曹锦"},
	}}
	sh := Shot{
		ID:         3,
		Characters: []string{"char_a", "char_b"},
		Dialogue: []Line{
			{Speaker: "姜晚舟", Text: "把灯递给我。"}, // 在画面里（出场名单里）
			{Speaker: "曹锦", Text: "别急。"},     // 不在本镜 ⇒ 画外
			{Speaker: "小灯", Text: "你拿稳。"},    // 在画面里
			{Speaker: "旁白", Text: "风停了。"},    // 画外音标签 ⇒ 两边都不算
			{Speaker: "姜晚舟", Text: ""},       // 空台词 ⇒ 不算说过话
			// 边界（**如实写下来**，不是漏判）：说话人根本不是本方案的角色时，
			// 它既不在出场名单里 ⇒ 会被 `OffScreenSpeakers` 收进"画面外"那一拨。
			// 这是**改造前就有的口径**（那个函数从不查"是不是本方案的角色"；查名字对不对得上
			// 是 `speaker_bind.go` 的 `markOffscreenSpeakers` → 契约 fail 的活）。
			// 与本次改动的关系：`OnScreenSpeakers` 只统计**对上号且在本镜**的说话人，
			// 所以打错名字既不会让"一次只一张嘴"多补一条、也不会少补。
			{Speaker: "路人甲", Text: "借过。"},
		},
	}
	on := OnScreenSpeakers(plan, sh)
	off := OffScreenSpeakers(plan, sh)
	if len(on) != 2 || on[0] != "姜晚舟" || on[1] != "小灯" {
		t.Errorf("画面内说话人应恰好是 [姜晚舟 小灯]，实际 %v", on)
	}
	if len(off) != 2 || off[0] != "曹锦" || off[1] != "路人甲" {
		t.Errorf("画面外说话人应恰好是 [曹锦 路人甲]，实际 %v", off)
	}
	// 不重不漏：同一个名字不许同时出现在两半里。
	for _, a := range on {
		for _, b := range off {
			if a == b {
				t.Errorf("「%s」同时被判成画面内与画面外", a)
			}
		}
	}
	// 幂等/稳定：同样的输入调两次结果一致（判据不许带随机或顺序漂移）。
	on2 := OnScreenSpeakers(plan, sh)
	if len(on2) != len(on) || on2[0] != on[0] || on2[1] != on[1] {
		t.Errorf("两次调用结果不同：%v vs %v", on, on2)
	}
}

// 带括注的名字（技能侧常写「祁七（主角）」）要按基名对上画面内的那个角色。
func TestOnScreenSpeakersMatchesBracketedName(t *testing.T) {
	plan := &Plan{Characters: []Character{{ID: "char_q", Name: "祁七"}}}
	sh := Shot{
		ID:         5,
		Characters: []string{"char_q"},
		Dialogue:   []Line{{Speaker: "祁七（主角）", Text: "别碰那条链。"}},
	}
	if on := OnScreenSpeakers(plan, sh); len(on) != 1 || on[0] != "祁七（主角）" {
		t.Errorf("带括注的说话人应算作画面内，实际 %v", on)
	}
	if off := OffScreenSpeakers(plan, sh); len(off) != 0 {
		t.Errorf("带括注的说话人在画面里，不该进画面外那半：%v", off)
	}
}

// 本镜 `offscreen` 字段标了"已出画"的角色：**台词仍算画外**。
//
// 边界说明（别当成漏判）：`OffScreenSpeakers` 本身只看 `characters` 名单，
// 不看 `offscreen` 字段 —— 那是 `speaker_bind.go` 的 `markOffscreenSpeakers` 在归一 os 时用的。
// 这条门钉住的是"两者分工不矛盾"：出场名单里有他 ⇒ 两个函数都认为他在画面里，
// 而出画这件事由 os 归一那一步表达（`markOffscreenSpeakers` 会把他的台词标成 os=true）。
func TestOnScreenSpeakersFollowsCastListOnly(t *testing.T) {
	plan := &Plan{Characters: []Character{{ID: "char_a", Name: "阿凿"}}}
	sh := Shot{
		ID:         2,
		Characters: []string{"char_a"},
		Offscreen:  map[string]string{"阿凿": "already left frame"},
		Dialogue:   []Line{{Speaker: "阿凿", Text: "我在外头。"}},
	}
	if on := OnScreenSpeakers(plan, sh); len(on) != 1 {
		t.Errorf("出场名单里有他 ⇒ 这一半算他在画面里，实际 %v", on)
	}
	// 而归一那一步要把他标成画外音（判据在 characters + offscreen 两个字段上）。
	if n := MarkOffscreenSpeakers(&Plan{Characters: plan.Characters, Shots: []Shot{sh}}); n != 1 {
		t.Errorf("已出画角色的台词应被标成 os=true（1 处），实际改了 %d 处", n)
	}
}

// nil 方案不许 panic（装配点会传 nil plan 的路径存在：`ShotRenderPrompt(nil, plan, …)`）。
func TestSpeakerFrameSplitNilPlan(t *testing.T) {
	if on := OnScreenSpeakers(nil, Shot{}); len(on) != 0 {
		t.Errorf("nil 方案应返回空，实际 %v", on)
	}
	if off := OffScreenSpeakers(nil, Shot{}); len(off) != 0 {
		t.Errorf("nil 方案应返回空，实际 %v", off)
	}
}
