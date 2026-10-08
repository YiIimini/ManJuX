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

	// ── 用户点名的"可以同时跑起来"仍在，但顺序有讲究（2026-09-29 真机实测）─────
	// ④场景要先跑：⑤分镜按 `"scene":"场景名"` 引用，并行时名字还没定，模型自己起一个
	// ⇒ 实测那一镜装配不到场景参考图（「⚠ 镜头 1 缺 1 张参考图(归墟环断口维护舱)」）。
	// ②人物道具 与 ⑤分镜 仍然并行：角色名来自同一份【人物外形锚表】，两边看到的一样。
	kinds := ParallelKinds()
	if len(kinds) != 2 {
		t.Fatalf("并行的是 ②⑤ 两步（④ 先跑），实际 %d 步", len(kinds))
	}
	for _, want := range []SessionKind{SessionCast, SessionStoryboard} {
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
	//
	// 【2026-10-08 判据更新】原来这一条还要求"材料被贴上【本集原文】标签"（`b + "\n\n【本集原文】\n" + src`）。
	// 那个标签会把【本集剧情】(剧本) 与【本集原文】(素材) 的分工**抹掉** —— 模型只看到"一份原文"，
	// 于是围着小说原文排镜头（用户 2026-10-08：「…而不是去要求原文内容怎样怎样」）。
	// 现在只要求：设定集**在最前**、材料**原样**（两个节标记都还在，不得被改标）。
	with := withSettingBrief("【本集剧情】剧本内容\n\n【本集原文】小说正文", "【本作数字锚】现在是哪一年：灰纪元第八万年")
	if !strings.HasPrefix(with, "【本作数字锚】") {
		t.Errorf("设定集摘要该前置，实际：%s", with)
	}
	// 反面：不许再给整份材料加一个外层的【本集原文】标签 —— 旧的拼法是 `【本集原文】\n` + 材料开头，
	// 于是在材料里【本集剧情】前面紧贴着一个来源标签，模型就只看到"一份原文"。
	if strings.Contains(with, "【本集原文】\n【本集剧情】") {
		t.Errorf("材料被重新贴标签了（【本集原文】紧贴材料开头）—— 剧本/原文的分工会被抹掉：%s", with)
	}
	// 而且材料必须**逐字完整**地出现在结果里（一个字都不许被吃掉或改写）。
	if !strings.Contains(with, "【本集剧情】剧本内容\n\n【本集原文】小说正文") {
		t.Errorf("材料没有原样保留：%s", with)
	}
	if got := withSettingBrief("正文", "   "); got != "正文" {
		t.Errorf("空摘要 + 无节标记的材料必须原样返回（行为与从前一致），实际 %q", got)
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
