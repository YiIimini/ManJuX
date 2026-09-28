package project

import (
	"strings"
	"testing"
)

// spec_checks_break_test.go 两条"生成必崩"的机检（2026-09-28 实测驱动）。
//
// 依据：真渲 48 镜的第一集，VLM 自检中位 74 分，**最低四镜（28/58/58/58）失败方式完全一样**：
// 「手指数量与关节异常，手掌与刀柄融合成一团肉块」「多指或融合」「与工具粘连」——
// 四镜全是**手部 ＋ 工具**的小景别。另一类是伪汉字：角色卡的烙印写着 `reading "丙十七"`，
// 于是额头与碑面被画成「丙十十」这种无意义字形。
//
// 提示词里早就写着"手指细小动作不要写"，模型照写 ⇒ 必须有门。拿真方案验证过：这两条门
// 抓到了全部四镜最低分镜与那张出问题的卡。
func TestHandToolCloseUp(t *testing.T) {
	cases := []struct {
		name string
		size string
		text string
		want bool
	}{
		{"近景 + 刀尖字口（真案 #5）", "近景", "随即右腕往下一压，把「丙十七」末字那一捺从字口里撬开一道斜缝", true},
		{"特写 + 添刻（真案 #6）", "特写", "刀尖顺着撬开的斜缝一撬一挑，随后一笔一笔往里添刻", true},
		{"英文 blade", "close-up", "his blade rests in the carved groove", true},
		{"中景 + 同样的动作 ⇒ 不报（工具活放中景是安全的）", "中景", "他把刀尖捅进字口", false},
		{"近景 + 无工具词 ⇒ 不报", "近景", "他垂着眼不抬头，下颌咬紧，一道影子掠过碑面", false},
		{"全景 + 工具 ⇒ 不报", "全景", "他把刀尖捅进字口", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := handToolCloseUp(tc.size, tc.text); got != tc.want {
				t.Errorf("handToolCloseUp(%q,%q) = %v,期望 %v", tc.size, tc.text, got, tc.want)
			}
		})
	}
}

func TestBodyReadableText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"真案：卡上写着印记读作某字", `a pale-edged old branding scar reading "丙十七" on the left forehead`, `reading "`},
		{"真案：动作写刻字", "石面深深刻着「丙十七」三字", "刻着"},
		{"英文 reads", "a scar on his wrist reads Chen", "reads "},
		{"安全写法：磨损不可辨 ⇒ 不报", "a pale ragged brand mark, the characters long since worn smooth", ""},
		{"安全写法：风化无字 ⇒ 不报", "the other stele faces are wind-worn and blank", ""},
		{"普通文本 ⇒ 不报", "他跪在碑前，风从坡上下来", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bodyReadableText(tc.in); got != tc.want {
				t.Errorf("bodyReadableText(%q) = %q,期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBreakGatesReachContract 两条都要真的接进契约（门写了不接线＝没写）。
func TestBreakGatesReachContract(t *testing.T) {
	p := &Plan{
		Characters: []Character{{ID: "c1", Name: "碑生", ImagePrompt: `... a brand reading "丙十七" ...`}},
		Scenes:     []Scene{{ID: "s1", Name: "西坡碑林·黄昏", Description: "x", ImagePrompt: "x"}},
		Shots: []Shot{{
			ID: 1, Scene: "s1", ShotSize: "特写", Duration: 6, Mode: "ref2va",
			Characters: []string{"c1"}, Action: "刀尖顺着斜缝一撬一挑，把那一捺翻开",
			H3Prompt: compliantH3ForSpeakerTest(),
		}},
	}
	var handHit, textHit bool
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "手部工具精细动作") {
			handHit = true
		}
		if strings.Contains(is.Msg, "可读文字") {
			textHit = true
		}
	}
	if !handHit {
		t.Error("小景别 ＋ 工具精细动作要报（实测这一类生成必崩）")
	}
	if !textHit {
		t.Error("把可读文字写到皮肤/器物上要报（实测必出伪汉字）")
	}
}
