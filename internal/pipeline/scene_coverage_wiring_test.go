package pipeline

import (
	"strings"
	"testing"
)

// 场次表必须**落进方案**、而且必须在**存盘之前**（2026-10-09 用户：「场景和分镜运镜很单一」）。
//
// 判据盯**整句赋值**（不是标识符出现）：标识符出现在注释里也能满足宽串断言，那种守卫是摆设 ——
// 所以这里连"这一行是不是注释"都一起查。
func TestBriefScenesLandInThePlanBeforeSave(t *testing.T) {
	src := readSrc(t, "stages_script.go")
	const assign = "plan.BriefScenes = project.ParseScriptScenes(text)"
	at := strings.Index(src, assign)
	if at < 0 {
		t.Fatal("剧本阶段没把场次表落进方案 —— 渲染前门（ValidatePlan）与重渲 / 导入包路径就又看不见这笔账了")
	}
	if line := lineOf(src, at); strings.HasPrefix(strings.TrimSpace(line), "//") {
		t.Fatalf("那句赋值只在注释里：%q", line)
	}
	// 顺序要命：存盘之后才落进去，写盘那份方案里就没有它。
	save := strings.Index(src, "c.store.SavePlan(proj.ID, plan)")
	if save < 0 {
		t.Fatal("找不到剧本阶段的存盘调用（判据的前提没了，得先核对这一段）")
	}
	if at > save {
		t.Error("场次表是在存盘**之后**才落进方案的 —— 写盘那份方案里没有它，渲染前门照样看不见")
	}
	// 覆盖对账要吃**落到方案里的那一份**：解析两次就多一条可能不一致的路径。
	if !strings.Contains(src, "project.SceneCoverageIssues(plan, plan.BriefScenes)") {
		t.Error("剧本阶段的播报没用落到方案里的场次表（解析两份，两份还可能不一致）")
	}
}

// lineOf 取 offset 所在的那一整行。
func lineOf(src string, offset int) string {
	start := strings.LastIndex(src[:offset], "\n") + 1
	end := strings.Index(src[offset:], "\n")
	if end < 0 {
		return src[start:]
	}
	return src[start : offset+end]
}
