package pipeline

import (
	"os"
	"strings"
	"testing"
)

// empty_prompt_wiring_test.go 「哪几镜的正文是空的」必须在**剧本阶段**就被点名。
//
// 【为什么要有这道接线门（2026-10-08 真机《外卖员写了七千封回信》第01集）】镜头 8 的
// `h3_prompt` 是空的，而这条缺陷此前只在**渲染阶段**以 fatal 出现 —— 那一刻 15 件资产
// （角色/造型/场景/道具）已经全部生成完，整条产线卡在中止状态（用户报「短篇咋不能正常渲染视频」）。
// 剧本阶段存盘前点名，才让用户在资产阶段之前就知道是哪几镜、该怎么办。
func TestEmptyPromptShotsAreNamedAtScriptStage(t *testing.T) {
	raw, err := os.ReadFile("stages_script.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "project.EmptyPromptShotIDs(plan)") {
		t.Error("剧本阶段没有点名空正文的镜 —— 用户只会在资产阶段白跑完之后才被渲染门拦住")
	}
	if !strings.Contains(src, "渲染会被致命门拦住") {
		t.Error("点名要说清后果（渲染会被致命门拦住、空提示词只会出一帧近全黑）")
	}
	// 点名必须在**存盘之前**：存盘之后才报的话，用户拿到的是"已就绪"的假象。
	iCall := strings.Index(src, "project.EmptyPromptShotIDs(plan)")
	iSave := strings.Index(src, "c.store.SavePlan(proj.ID, plan)")
	if iCall < 0 || iSave < 0 || iCall > iSave {
		t.Error("点名必须排在 SavePlan 之前（否则先报「方案就绪」再报缺陷）")
	}
}
