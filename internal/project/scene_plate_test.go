package project

import (
	"strings"
	"testing"
)

// scene_plate_test.go 场景板"贫瘠"检测（用户 2026-09-28：「场景简陋单一导致分镜内容一点也不理想」）。
//
// 判据是把《四、场景提示词指令》的**意图**翻译成可机检的三件（不是搬它的中文词表 ——
// 那套是即梦的写法，平台这边是 H3 的英文散文格式）：
// ① 建立镜头串；② 大景元素 ≥2 项；③ 光的质感（不是"白天"这种时间词）。
func TestScenePlateThin(t *testing.T) {
	rich := "ULTRA-WIDE 45-DEGREE AERIAL VIEW of an abandoned temple courtyard at dead-gold dusk: " +
		"ultra-wide angle, grand scale, epic wide shot, vast space, distant horizon, cinematic establishing shot; " +
		"wet flagstones, distant blue-grey mountain ridges beyond the wall; the last sun lies in long bars across wet stone"
	cases := []struct {
		name string
		in   string
		want int // 缺几项
	}{
		{"真实形态的板子：不缺", rich, 0},
		{"简陋：没有建立镜头串", strings.Replace(rich, PlanEstablishingExtra, "", 1), 1},
		{"简陋：只有一面墙（大景元素不足）", "a stone wall in a temple courtyard, ultra-wide angle, grand scale, epic wide shot, vast space, distant horizon, cinematic establishing shot, soft light", 1},
		{"简陋：只有白天没有光的质感", "ULTRA-WIDE AERIAL VIEW, ultra-wide angle, grand scale, epic wide shot, vast space, distant horizon, cinematic establishing shot, sky and mountains, daytime", 1},
		{"空串交给别的门（这里不重复报）", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(ScenePlateThin(tc.in)); got != tc.want {
				t.Errorf("ScenePlateThin 缺 %d 项，期望 %d：%v", got, tc.want, ScenePlateThin(tc.in))
			}
		})
	}
}

// TestScenePlateThinReachesContract 接线：贫瘠场景要在契约里报出来。
func TestScenePlateThinReachesContract(t *testing.T) {
	p := &Plan{
		Scenes: []Scene{{ID: "s1", Name: "荒山破庙·雨夜", Description: "x", ImagePrompt: "a ruined temple at night"}},
	}
	var hit bool
	for _, is := range ValidatePlan(p) {
		if strings.Contains(is.Msg, "贫瘠") {
			hit = true
		}
	}
	if !hit {
		t.Error("简陋的场景板要报（板子简陋，分镜就只能在简陋的空间里拍）")
	}
}
