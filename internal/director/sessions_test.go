package director

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 五步专职会话的守卫（用户 2026-09-28 架构）。
func TestSessionLayer(t *testing.T) {
	// ── ① 每个会话只拿**自己那一步**的规则（"先告诉 LLM 这一步只干什么"）──────
	cast := SessionSystem(SessionCast, Options{})
	if !strings.Contains(cast, "CAST") && !strings.Contains(cast, "characters") {
		t.Error("② 人物道具会话的 system 段该讲角色/道具")
	}
	if strings.Contains(cast, "h3_prompt") {
		t.Errorf("② 不该带分镜的 H3 规则（那正是「一次出全部」的老毛病）：\n%s", cast[:min(400, len(cast))])
	}
	scene := SessionSystem(SessionScene, Options{})
	if !strings.Contains(scene, "LOCATION") && !strings.Contains(scene, "scene") {
		t.Error("④ 场景会话的 system 段该讲场景")
	}
	sb := SessionSystem(SessionStoryboard, Options{})
	for _, want := range []string{"shot", "h3_prompt"} {
		if !strings.Contains(strings.ToLower(sb), want) {
			t.Errorf("⑤ 分镜会话该含「%s」", want)
		}
	}
	// 每一步都写明"只做这一步、只输出这一段 JSON"
	for _, k := range ParallelKinds() {
		sys := SessionSystem(k, Options{})
		if !strings.Contains(sys, "EXACTLY ONE step") {
			t.Errorf("%s 的 system 段必须写明「只做这一步」", k)
		}
		if !strings.Contains(sys, "OUTPUT (JSON only)") {
			t.Errorf("%s 必须给出期望的输出形状（先告诉 LLM 干什么）", k)
		}
	}

	// ── ② 用户点名的"可以同时跑起来"就是这三步 ─────────────────────────────
	kinds := ParallelKinds()
	if len(kinds) != 3 {
		t.Fatalf("并行的是 ②④⑤ 三步，实际 %d 步", len(kinds))
	}
	for _, want := range []SessionKind{SessionCast, SessionScene, SessionStoryboard} {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Errorf("并行集合里缺 %s", want)
		}
	}
	// **③ 不在并行集合里**：它要知道有哪些角色（依赖 ②）
	for _, k := range kinds {
		if k == SessionViews {
			t.Error("③ 角色多视图依赖 ② 的产出，不该进并行集合")
		}
	}

	// ── ③ 归并：后到的会话**补空字段**，不覆盖已有内容 ─────────────────────
	merged := MergeSessions([]SessionResult{
		{Kind: SessionCast, Plan: &project.Plan{Characters: []project.Character{
			{ID: "char_a", Name: "沈砚", Description: "瘦长脸"}}}},
		{Kind: SessionStoryboard, Plan: &project.Plan{
			Style: "国风电影级",
			Shots: []project.Shot{{ID: 1, Duration: 5}},
			// 分镜会话**也**会回一份角色（模型爱顺带写），这里是它的空字段补充
			Characters: []project.Character{{ID: "char_a", Name: "沈砚", ImagePrompt: "a lean young man"}},
		}},
	})
	if len(merged.Characters) != 1 {
		t.Fatalf("同一个角色（id 相同）该合并成一条，实际 %d 条", len(merged.Characters))
	}
	if merged.Characters[0].Description != "瘦长脸" {
		t.Error("已有描述不该被后到的空值/别的值覆盖")
	}
	if merged.Characters[0].ImagePrompt != "a lean young man" {
		t.Error("后到的非空字段该补上（补空，不是覆盖）")
	}
	if len(merged.Shots) != 1 || merged.Style == "" {
		t.Error("分镜与画风该归并进来")
	}

	// ── ④ 逐会话的错误隔离：一个失败不拖垮同批 ─────────────────────────────
	failed := []SessionResult{
		{Kind: SessionCast, Err: errTest},
		{Kind: SessionScene, Plan: &project.Plan{Scenes: []project.Scene{{Name: "破庙"}}}},
	}
	if err := ErrAllSessionsFailed(failed); err != nil {
		t.Errorf("只要有一个会话有产出就不该当成整体失败：%v", err)
	}
	if err := ErrAllSessionsFailed([]SessionResult{{Kind: SessionCast, Err: errTest}}); err == nil {
		t.Error("全部失败必须报出来（别让零产出当成功落盘）")
	}

	// ── ⑤ 设定集摘要前置进 user 段（空摘要时行为与从前一致）────────────────
	with := withSettingBrief("正文", "【本作数字锚】现在是哪一年：灰纪元第八万年")
	if !strings.HasPrefix(with, "【本作数字锚】") || !strings.Contains(with, "【本集原文】\n正文") {
		t.Errorf("设定集摘要该前置，实际：%s", with)
	}
	if got := withSettingBrief("正文", "   "); got != "正文" {
		t.Errorf("空摘要必须原样返回（行为与从前一致），实际 %q", got)
	}
}

var errTest = errTestType{}

type errTestType struct{}

func (errTestType) Error() string { return "测试用错误" }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
