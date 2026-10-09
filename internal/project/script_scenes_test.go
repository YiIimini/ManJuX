package project

import (
	"encoding/json"
	"strings"
	"testing"
)

// 场次覆盖对账（2026-09-30 用户：「分析是否场景过于单一」+「好好完善」）。
//
// 夹具**照第 02 集的真实数字写**：剧本 4 场、分镜 15 镜，其中一场占 49%、
// 第 3 场只剩 1 镜、唯一的夜戏整场没有 —— 那正是"场景过于单一"的来源。
func TestSceneCoverageIssues(t *testing.T) {
	brief := `
【场 1|碑奴粥棚|清晨|外】
  目的：…
【场 2|第七坡碑前|上午|外】
【场 3|号棚内外|晌午到散工|内外】
【场 4|号棚门槛|夜|内外】
`
	scenes := ParseScriptScenes(brief)
	if len(scenes) != 4 {
		t.Fatalf("场次表没读全：得到 %d 场 %+v", len(scenes), scenes)
	}
	if scenes[3].Place != "号棚门槛" || scenes[3].Time != "夜" {
		t.Errorf("第 4 场读错：%+v", scenes[3])
	}

	// 真机形态：场景卡 id 与剧本地点名不同写法，也要能对上
	plan := &Plan{
		Scenes: []Scene{
			{ID: "scene_zhou_peng", Name: "碑奴营粥棚"},
			{ID: "scene_seventh_slope", Name: "第七坡碑场"},
			{ID: "scene_hao_peng", Name: "碑奴号棚通铺"},
			{ID: "scene_hao_peng_night", Name: "号棚门槛夜色", LightState: "夜，西侧冷月光，棚内一盏微弱油灯"},
		},
	}
	add := func(n int, scene string, dur int) {
		for i := 0; i < n; i++ {
			plan.Shots = append(plan.Shots, Shot{ID: len(plan.Shots) + 1, Scene: scene, Duration: dur})
		}
	}
	add(6, "scene_seventh_slope", 9) // 剧本场2：6 镜（真机是 7，占 47%）
	add(5, "scene_zhou_peng", 9)     // 剧本场1
	add(3, "scene_hao_peng", 9)      // 剧本场3：只剩 3 镜
	// 剧本场4（夜戏）**一镜都不给** —— 真机就是整场丢掉的
	issues := SceneCoverageIssues(plan, scenes)
	if len(issues) == 0 {
		t.Fatal("该报的问题一条都没报")
	}
	joined := strings.Join(issues, "\n")
	if !strings.Contains(joined, "只用到") {
		t.Errorf("没有报出场次数不足：\n%s", joined)
	}
	if !strings.Contains(joined, "夜场") {
		t.Errorf("没有报出「剧本有夜场而全集没用上」：\n%s", joined)
	}
	if !strings.Contains(joined, "44%") && !strings.Contains(joined, "占") {
		t.Errorf("没有报出单场占比过高：\n%s", joined)
	}

	// 覆盖均衡时**不许报**
	ok := &Plan{Scenes: plan.Scenes}
	ok.Shots = nil
	add2 := func(scene string, dur int) {
		ok.Shots = append(ok.Shots, Shot{ID: len(ok.Shots) + 1, Scene: scene, Duration: dur})
	}
	for i := 0; i < 3; i++ {
		add2("scene_zhou_peng", 9)
		add2("scene_seventh_slope", 9)
		add2("scene_hao_peng", 9)
		add2("scene_hao_peng_night", 9)
	}
	if got := SceneCoverageIssues(ok, scenes); len(got) != 0 {
		t.Errorf("均衡覆盖不该报问题，实得：%v", got)
	}

	// 老包（剧本没有场次表）不许报 —— 无从对照
	if got := SceneCoverageIssues(plan, nil); len(got) != 0 {
		t.Errorf("没有场次表时不该报：%v", got)
	}
}

// 场次表**落进方案**之后，渲染前门也能对账（2026-10-09 用户：「场景和分镜运镜很单一」）。
//
// 此前这笔账只在剧本阶段播报一句：预检（cmd/preflight）、重渲、导入包路径都**拿不到剧本文本**，
// 用户要到成片才发现「场景过于单一」。夹具仍照第 02 集的真实数字写。
func TestSceneCoverageOnRenderFrontGate(t *testing.T) {
	brief := `
【场 1|碑奴粥棚|清晨|外】
【场 2|第七坡碑前|上午|外】
【场 3|号棚内外|晌午到散工|内外】
【场 4|号棚门槛|夜|内外】
`
	plan := &Plan{
		Scenes: []Scene{
			{ID: "scene_zhou_peng", Name: "碑奴营粥棚"},
			{ID: "scene_seventh_slope", Name: "第七坡碑场"},
			{ID: "scene_hao_peng", Name: "碑奴号棚通铺"},
			{ID: "scene_hao_peng_night", Name: "号棚门槛夜色", LightState: "夜，西侧冷月光，棚内一盏微弱油灯"},
		},
	}
	// 真机形态：剧本场 2 占 6/14 镜（43% > 40% 上限）；用到的场景只有 3 个（少一场）；
	// 剧本第 4 场是夜戏 —— 一镜都没给。
	for i := 0; i < 6; i++ {
		plan.Shots = append(plan.Shots, Shot{ID: len(plan.Shots) + 1, Scene: "scene_seventh_slope"})
	}
	for i := 0; i < 5; i++ {
		plan.Shots = append(plan.Shots, Shot{ID: len(plan.Shots) + 1, Scene: "scene_zhou_peng"})
	}
	for i := 0; i < 3; i++ {
		plan.Shots = append(plan.Shots, Shot{ID: len(plan.Shots) + 1, Scene: "scene_hao_peng"})
	}
	plan.BriefScenes = ParseScriptScenes(brief)
	if len(plan.BriefScenes) != 4 {
		t.Fatalf("场次表没读全：%+v", plan.BriefScenes)
	}

	// ① 数据版三态齐（顺序确定：单场占比 → 场次不足 → 夜戏丢失）
	got := SceneCoveragePlanIssues(plan)
	want := []SceneCoverageKind{SceneShareOverCap, SceneMissing, SceneNightDropped}
	if len(got) != len(want) {
		t.Fatalf("该报 %d 条，实际 %d 条 %+v", len(want), len(got), got)
	}
	for i, k := range want {
		if got[i].Kind != k {
			t.Errorf("第 %d 条该是 %s，实际 %s", i+1, k, got[i].Kind)
		}
	}

	// ② 承重点：**渲染前门**必须把这三条报出来（此前只有剧本阶段那一句播报）
	txt := strings.Join(PlanIssuesText(ValidatePlan(plan)), "\n")
	for _, sub := range []string{"一场占了", "分镜只用到", "夜场"} {
		if !strings.Contains(txt, sub) {
			t.Errorf("渲染前门没报出 %q：\n%s", sub, txt)
		}
	}

	// ③ 落盘再读回不丢 —— 场次表是**方案的一部分**，不是内存里的临时表
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("方案序列化失败：%v", err)
	}
	var back Plan
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("方案反序列化失败：%v", err)
	}
	if len(back.BriefScenes) != 4 || back.BriefScenes[3].Time != "夜" {
		t.Errorf("场次表没能落盘再读回：%+v", back.BriefScenes)
	}

	// ④ 老方案（方案里没有场次表）一律不判 —— 与"老包没有场次表就不判"同口径
	old := &Plan{Scenes: plan.Scenes, Shots: plan.Shots}
	if n := len(SceneCoveragePlanIssues(old)); n != 0 {
		t.Errorf("没有场次表的方案不该报，实际 %d 条", n)
	}
	if hasIssue(ValidatePlan(old), "warn", "一场占了") {
		t.Errorf("没有场次表的方案不该在渲染前门被报：%v", PlanIssuesText(ValidatePlan(old)))
	}
}
