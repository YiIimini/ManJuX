package project

import (
	"strings"
	"testing"
)

// 契约门**第二批**的守卫（2026-09-28 用户："这些给我留着干嘛？"）。
//
// 逐条对应 `contract_gates.go` 里那个函数；出处也写在那边。
func TestContractGatesBatch2(t *testing.T) {
	// ── ① 切镜动词白名单（官方 §4.2）──────────────────────────────────────
	// 五个官方写法都放行
	for _, ok := range PlanCutVerbs {
		sh := Shot{ID: 1, H3Prompt: "detailed_description:\n[Shot 1] a beat.\n[Shot 2] At 00:02.000, " + ok + " the blade."}
		if ids := CutVerbIssues([]Shot{sh}); len(ids) != 0 {
			t.Errorf("官方写法 %q 不该被报，实际 %v", ok, ids)
		}
	}
	// 自创写法要报
	for _, bad := range []string{"we cut to the blade", "the scene jumps to the street", "the frame switches to a close view"} {
		sh := Shot{ID: 1, H3Prompt: "detailed_description:\n[Shot 1] a beat.\n[Shot 2] At 00:02.000, " + bad + "."}
		if ids := CutVerbIssues([]Shot{sh}); len(ids) == 0 {
			t.Errorf("自创切镜写法 %q 该被报出来", bad)
		}
	}
	// **动作描写里的 cut 不是切镜**（"a cut on his hand"）—— 不许误报
	act := Shot{ID: 1, H3Prompt: "detailed_description:\n[Shot 1] a long cut on his hand bleeds slowly."}
	if ids := CutVerbIssues([]Shot{act}); len(ids) != 0 {
		t.Errorf("「a cut on his hand」是动作描写，不该当成切镜，实际 %v", ids)
	}

	// ── ② 三视图板 15 格（平台 BoardLayoutClause）─────────────────────────
	full := "one sheet: three full-body standing views; six head angles; six detail tiles"
	if miss := BoardCellIssues(full); len(miss) != 0 {
		t.Errorf("三段齐全的板子不该报，实际缺 %v", miss)
	}
	if miss := BoardCellIssues("one sheet: three full-body standing views"); len(miss) != 2 {
		t.Errorf("缺两段就该报两条，实际 %v", miss)
	}

	// ── ③ 场景四要素（用户《四》:63）────────────────────────────────────
	good := Scene{Name: "玄青宗命魂殿",
		Description: "供奉弟子魂灯的幽暗殿宇",
		ImagePrompt: "a dim hall at dusk, atmosphere of solemn stillness, weathered carved beams"}
	if got := SceneElementIssues([]Scene{good}); len(got) != 0 {
		t.Errorf("四要素齐全的场景不该报，实际 %v", got)
	}
	// 缺时间与氛围
	bad := Scene{Name: "旧城派送站棚", ImagePrompt: "a delivery shed with weathered planks"}
	got := SceneElementIssues([]Scene{bad})
	if len(got) != 1 {
		t.Fatalf("该报一条（缺项合成一条），实际 %v", got)
	}
	joined := strings.Join(got[0].Words, "/")
	if !strings.Contains(joined, "具体时间") || !strings.Contains(joined, "空间氛围") {
		t.Errorf("缺项要逐项点名，实际 %q", joined)
	}

	// ── ④ 场景视角九选一 ────────────────────────────────────────────────
	// **大小写必须归一**：真实场景卡写的是全大写 `ULTRA-WIDE 45-DEGREE AERIAL VIEW of …`
	if names := SceneViewIssues([]Scene{{Name: "破庙",
		ImagePrompt: "ULTRA-WIDE 45-DEGREE AERIAL VIEW of an abandoned courtyard"}}); len(names) != 0 {
		t.Errorf("写了全大写视角串的场景不该被报（判据要小写归一），实际 %v", names)
	}
	if names := SceneViewIssues([]Scene{{Name: "破庙", ImagePrompt: "an abandoned courtyard"}}); len(names) != 1 {
		t.Errorf("一条视角词都没有的场景要报，实际 %v", names)
	}

	// ── ⑤ 屏幕文字引号（官方硬约束）──────────────────────────────────────
	quoted := Shot{ID: 1, H3Prompt: `on the plaque above the gate the characters "\u4e00\u7edf\u6c5f\u5c71" are carved`}
	if ids := ScreenTextQuoteIssues([]Shot{quoted}); len(ids) != 0 {
		t.Errorf("引号包住的屏幕文字不该被报，实际 %v", ids)
	}
	bare := Shot{ID: 1, H3Prompt: "on the plaque above the gate some characters are carved"}
	if ids := ScreenTextQuoteIssues([]Shot{bare}); len(ids) != 1 {
		t.Errorf("画面要读的字没加引号要报，实际 %v", ids)
	}
	// **不许把 signature 当成 sign**（第一版就这么在夹具上误报的）
	sig := Shot{ID: 1, H3Prompt: "the figure keeps the same signature marks and the same design on the sleeve"}
	if ids := ScreenTextQuoteIssues([]Shot{sig}); len(ids) != 0 {
		t.Errorf("signature/design 不是「要读的字」，裸 Contains 会误报，实际 %v", ids)
	}
}
