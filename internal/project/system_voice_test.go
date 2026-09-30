package project

import (
	"strings"
	"testing"
)

// 系统音说话人（2026-09-30 用户：「视频配音什么鬼？系统一次，主角一次？」）。
//
// 实测《名册之外》第 01 集：系统播报的 speaker 写的是 **`舱体主控`**（不是"系统"），
// 旧判据只认精确等于「旁白/系统/画外音/内心」⇒ 漏掉它 ⇒ 平台按**画内台词**处理：
// 找一张叫「舱体主控」的角色卡（不存在）、把系统播报排进角色音色表 ⇒ 模型用**角色的嗓子**
// 又把系统的话念了一遍 ⇒ 听到的就是"系统一次、主角一次"。夹具就是那批真实说话人。
func TestSystemVoiceSpeakers(t *testing.T) {
	// ① 真机那批系统音：必须判成"不是画面里的人在说"
	for _, s := range []string{"舱体主控", "主控", "维修终端", "舱内广播", "系统提示音", "面板播报", "AI 助手"} {
		if !IsVoiceoverSpeaker(s) {
			t.Errorf("「%s」是真机里的系统音，却没被判成画外/系统音 —— 会被当角色念一遍", s)
		}
	}
	// ② 系统音要与"人的画外音"分开（旁白/内心不是系统音）
	for _, s := range []string{"旁白", "内心"} {
		if !IsVoiceoverSpeaker(s) {
			t.Errorf("「%s」应是画外音", s)
		}
		if IsSystemVoiceSpeaker(s) {
			t.Errorf("「%s」是人的画外音，不该走系统音那一路（会拿机器/面板当参考图）", s)
		}
	}
	// ③ 角色名不许被误判（否则真人台词会被当机器音，参考图与音色全错）
	for _, s := range []string{"碑生（丙十七）", "祁七", "值班员", "管理员", "邵舷"} {
		if IsVoiceoverSpeaker(s) {
			t.Errorf("「%s」是角色，被判成了画外/系统音", s)
		}
	}
	// ④ 系统音判据本身
	if !IsSystemVoiceSpeaker("舱体主控") || !IsSystemVoiceSpeaker("系统") {
		t.Error("系统音判据漏了真机说话人")
	}
	if IsSystemVoiceSpeaker("旁白") {
		t.Error("旁白不是系统音")
	}
	// ⑤ 与既有那条门联动：系统音说话人不在画面里时，**不该**再报"说话人不在本镜 characters 里"
	plan := &Plan{
		Characters: []Character{{ID: "char_qiqi", Name: "祁七"}},
		Shots: []Shot{{
			ID: 1, Characters: []string{"char_qiqi"}, H3Prompt: "x", Duration: 8,
			Dialogue: []Line{{Speaker: "舱体主控", Text: "重新检测到生命反应。", OS: true}},
		}},
	}
	for _, it := range ValidatePlan(plan) {
		if strings.Contains(it.Msg, "舱体主控") && strings.Contains(it.Msg, "不在本镜") {
			t.Errorf("系统音被当成「画面里没这个人」报了错：%s", it.Msg)
		}
	}
}
