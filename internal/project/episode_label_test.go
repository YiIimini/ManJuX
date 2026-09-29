package project

import (
	"strings"
	"testing"
)

// 方案标题必须是**人能认出来的那一集**（2026-09-29 用户报「作品库里的每一集名称没有正确展示」）。
//
// 现场：五步专职会话那条路没人写 `plan.title`，它是空串；而作品库卡片、工作台标题、
// 阶段抽屉都拿 `plan.title` 当标题显示 —— 用户在作品库里看到一排《》。
// "这一集叫什么"平台本来就知道：项目 id 就是 `书·第NN集·章名`。
func TestPlanTitleFallsBackToEpisodeLabel(t *testing.T) {
	// 集标签：书名去掉（作品库按书分组，组头已经写了书名）
	if got := EpisodeLabel("名册之外，八万零一号·第01集·重新检测到生命反应"); got != "第01集·重新检测到生命反应" {
		t.Fatalf("集标签取错：%q", got)
	}
	// 章名里本身带 `·` 的（真机里有：第33集·值星长三七二·四一）不许被截断
	if got := EpisodeLabel("名册之外，八万零一号·第33集·值星长三七二·四一"); got != "第33集·值星长三七二·四一" {
		t.Fatalf("章名里的分隔符被吃掉了：%q", got)
	}
	// 不是分集项目：原样返回
	if got := EpisodeLabel("单集项目"); got != "单集项目" {
		t.Fatalf("非分集项目应原样返回：%q", got)
	}

	// 接线：LoadPlan 出来的方案**必须有标题**（空标题就是《》的来源）
	root := t.TempDir()
	st, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	const id = "名册之外，八万零一号·第01集·重新检测到生命反应"
	if _, err := st.Create(id, ProjectConfig{Params: DefaultStageParams()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePlan(id, &Plan{Shots: []Shot{{ID: 1, Scene: "s", H3Prompt: "x"}}}); err != nil {
		t.Fatal(err)
	}
	p, err := st.LoadPlan(id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(p.Title) == "" {
		t.Fatal("LoadPlan 出来的方案没有标题 —— 作品库/工作台会显示《》")
	}
	if !strings.Contains(p.Title, "第01集") {
		t.Fatalf("标题应含集号：%q", p.Title)
	}
	// 模型/技能侧写过的标题不许被覆盖
	if err := st.SavePlan(id, &Plan{Title: "技能侧起的标题", Shots: []Shot{{ID: 1, Scene: "s", H3Prompt: "x"}}}); err != nil {
		t.Fatal(err)
	}
	p2, _ := st.LoadPlan(id)
	if p2.Title != "技能侧起的标题" {
		t.Fatalf("已有标题被覆盖了：%q", p2.Title)
	}
}
