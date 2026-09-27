package project

import (
	"strings"
	"testing"
)

// speaker_bind_test.go 说话人与本镜画面的绑定归一化（2026-09-27 实测驱动）。
//
// 实测依据：真跑一次 LLM 直出（40 镜）后契约 fail=5，**全部**是
// 「说话人「卖菜婆」不在本镜 characters 里」。语义上那本来就该是画外音
// （人在画面外喊的那句），契约自己那句提示里也写着正确处置（"要么标 os=true"）。
func TestMarkOffscreenSpeakers(t *testing.T) {
	p := &Plan{
		Characters: []Character{
			{ID: "c1", Name: "沈砚", ImagePrompt: "x"},
			{ID: "c2", Name: "卖菜婆", ImagePrompt: "x"},
			{ID: "c3", Name: "周廷", ImagePrompt: "x"},
		},
		Shots: []Shot{
			{ // ① 说话人不在画面里 → 标 os
				ID: 1, Characters: []string{"c1"},
				Dialogue: []Line{{Speaker: "卖菜婆", Text: "三年了，你还敢出门。"}},
			},
			{ // ② 说话人在画面里 → 不动
				ID: 2, Characters: []string{"c1", "c2"},
				Dialogue: []Line{{Speaker: "卖菜婆", Text: "你也有今天。"}},
			},
			{ // ③ 画外音标签 → 不动（本来就不该在图里）
				ID: 3, Characters: []string{"c1"},
				Dialogue: []Line{{Speaker: "旁白", Text: "街上安静下来。"}},
			},
			{ // ④ 名字写错了（不在本方案角色表里）→ 不动，交给契约失败暴露
				ID: 4, Characters: []string{"c1"},
				Dialogue: []Line{{Speaker: "路过的老王", Text: "谁啊。"}},
			},
			{ // ⑤ 造型档后缀也算在场（沈砚·一品 点名相同）
				ID: 5, Characters: []string{"c1"},
				Dialogue: []Line{{Speaker: "沈砚", Text: "我记下了。"}},
			},
			{ // ⑥ 已经是 os 的不重复计数
				ID: 6, Characters: []string{"c1"},
				Dialogue: []Line{{Speaker: "周廷", Text: "把他拖走。", OS: true}},
			},
		},
	}
	if n := MarkOffscreenSpeakers(p); n != 1 {
		t.Fatalf("只该标 1 条（①），实际 %d 条", n)
	}
	if !p.Shots[0].Dialogue[0].OS {
		t.Error("① 不在画面里的说话人应标成画外音")
	}
	if p.Shots[1].Dialogue[0].OS {
		t.Error("② 人在画面里，不该标画外音")
	}
	if p.Shots[2].Dialogue[0].OS {
		t.Error("③ 画外音标签本来就不在图里，不必动")
	}
	if p.Shots[3].Dialogue[0].OS {
		t.Error("④ 名字不在角色表里 = 可能写错了，**不许**用自动标 os 盖掉")
	}
	if p.Shots[4].Dialogue[0].OS {
		t.Error("⑤ 本人在画面里，不该标")
	}
	// 幂等：再跑一遍不该再计数
	if n := MarkOffscreenSpeakers(p); n != 0 {
		t.Errorf("重复调用不该再标（幂等），实际 %d", n)
	}
	// 台词文本一个字节都不许动
	if p.Shots[0].Dialogue[0].Text != "三年了，你还敢出门。" {
		t.Error("归一化只改 os，不许动台词文本")
	}
}

// TestMarkOffscreenSpeakersQuietsTheGate 归一化之后契约不再因这条 fail。
func TestMarkOffscreenSpeakersQuietsTheGate(t *testing.T) {
	p := &Plan{
		Characters: []Character{{ID: "c1", Name: "沈砚", ImagePrompt: "IDENTITY LOCK x"},
			{ID: "c2", Name: "卖菜婆", ImagePrompt: "IDENTITY LOCK y"}},
		Scenes: []Scene{{ID: "s1", Name: "临水城·城门·雨夜", Description: "四要素", ImagePrompt: "x"}},
		Shots: []Shot{{
			ID: 1, Scene: "s1", ShotSize: "中景", Duration: 6, Mode: "ref2va",
			Characters: []string{"c1"}, Action: "他站着",
			Dialogue: []Line{{Speaker: "卖菜婆", Text: "你也有今天。"}},
			H3Prompt: compliantH3ForSpeakerTest(),
		}},
	}
	hasFail := func() bool {
		for _, is := range ValidatePlan(p) {
			if is.Level == "fail" && strings.Contains(is.Msg, "不在本镜") {
				return true
			}
		}
		return false
	}
	if !hasFail() {
		t.Fatal("标 os 之前这条门应该报（否则这条测试就是空跑）")
	}
	MarkOffscreenSpeakers(p)
	if hasFail() {
		t.Error("标成画外音之后不该再报这条 fail")
	}
}

// compliantH3ForSpeakerTest 一份**真实形态**的六段提示词（夹具照真实形态写）。
func compliantH3ForSpeakerTest() string {
	return "subject_definitions:\n<Subject 1> is 沈砚; its front sheet is the first picture.\n\n" +
		"summary:\n[reference generation] A short beat.\n\n" +
		"retention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved - identity.\n\n" +
		"detailed_description:\nCinematic, 3D CG. Chinese period guofeng.\n[Shot 1] A medium shot; " +
		"ultra-wide eye-level deep perspective. Sound events: cloth shifting. " +
		"No watermarks, no logos, no captions. Subjects must remain clearly visible and adequately lit " +
		"throughout the video; avoid rendering any frame nearly black or overly dark.\n\n" +
		"overall_soundscape:\nQuiet room tone and distant rain.\n\nnon_diegetic_music:\nN/A"
}
