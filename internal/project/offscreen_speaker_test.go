package project

import "testing"

// 画面外的人说话时，画面里的人不许对口型（2026-09-29 用户：
// 「镜头外的说话声音不应该镜头内人动嘴唇」）。
//
// 【为什么这是新缺口】`HasVoiceover` 覆盖的是"这一镜本来就是画外演出"（旁白/内心/系统音/os），
// 而**说话人有角色卡、但这一镜没让他出场**这种情形它判不出来 ⇒ 既不写闭嘴句、也没有门
// ⇒ H3 只看见 `<d>[Chinese]…</d>`，就让画面里的角色张嘴把别人的台词念了出来。
//
// 判据（夹具照真实方案形状写：出场名单是 id，说话人是名字）：
func TestOffScreenSpeakersDetectsSpeakersNotInShot(t *testing.T) {
	plan := &Plan{
		Characters: []Character{
			{ID: "char_qiqi", Name: "祁七"},
			{ID: "char_shaoxian", Name: "邵舷"},
			{ID: "char_shuangjian", Name: "霜见（读数员）"},
		},
		Shots: []Shot{
			// 镜头里只有邵舷，台词却是祁七说的 —— 画外音，画面里的人必须闭嘴
			{ID: 1, Characters: []string{"char_shaoxian"},
				Dialogue: []Line{{Speaker: "祁七", Text: "维护舱里还有一台。"}}},
			// 说话的人就在画面里 —— 正常对口型，不算画外
			{ID: 2, Characters: []string{"char_shaoxian"},
				Dialogue: []Line{{Speaker: "邵舷", Text: "收到。"}}},
			// 名字带括注也要能对上（出场名单里是「霜见（读数员）」）
			{ID: 3, Characters: []string{"char_shuangjian"},
				Dialogue: []Line{{Speaker: "霜见（读数员）", Text: "读数正常。"}}},
			// os 标记 / 旁白说话人由 HasVoiceover 管，这里不重复计数
			{ID: 4, Characters: []string{"char_qiqi"},
				Dialogue: []Line{{Speaker: "旁白", Text: "舱门开了。", OS: true}}},
			// 空镜里有人说话：画面里没人，同样是画外
			{ID: 5, Dialogue: []Line{{Speaker: "祁七", Text: "……"}}},
		},
	}
	for _, tc := range []struct {
		id   int
		want int
		why  string
	}{
		{1, 1, "祁七不在这一镜里，台词是画外的"},
		{2, 0, "说话的人就在画面里"},
		{3, 0, "名字带括注要按基名对上"},
		{4, 0, "os 标记那条走 HasVoiceover"},
		{5, 1, "空镜里没有人，说话人自然是画外"},
	} {
		got := OffScreenSpeakers(plan, plan.Shots[tc.id-1])
		if len(got) != tc.want {
			t.Errorf("镜头 #%d 期望 %d 个画外说话人（%s），实得 %v", tc.id, tc.want, tc.why, got)
		}
	}
}
