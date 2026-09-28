package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/project"
)

// `runRender` 编排层的**第一批**接线测试（2026-09-29 代码健康审计 P0-1）。
//
// ## 它们要挡的是什么
//
// 审计的实测结论：`stages_render.go` 里被覆盖的 85 条语句**全部**落在主函数之后抽出去的
// 纯函数里（种子派生、接缝判定、产物校验…），而 **`runRender` 函数体内 0 个已覆盖语句块**。
// 也就是说"判据"测得不错，"**把判据接起来**"这层一条没测 ——
// 接线错了（判据用对了但顺序错、参数传错、分支走反）不会被任何测试发现。
//
// 这几条覆盖的是最容易接错、后果最直接的三处：
//
//	① 没有方案就渲染 → 必须是**带指引的** ErrNoPlan（用户知道去点哪里）
//	② 正常路径 → 每镜都真的调了引擎，且**引擎拿到的参数是管线的决定**
//	③ 引擎返回成功但**没落产物** → 不能算这一镜完成（"报成功但成片里没这段"）
func renderFixture(t *testing.T, shots int) (*Coordinator, *fakeEngine, *project.Project) {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.NewManager(filepath.Join(root, "settings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewStore(filepath.Join(root, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	lib, err := library.New(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	proj, err := store.Create("渲染接线测试", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	fe := newFakeEngine()
	reg := engine.NewRegistry()
	reg.Register(fe) // 公开注册点：这就是审计说的"唯一拦路石"
	c := New(store, cfg, reg, new(ff.FF), NewBus(), lib)

	// 方案：N 个镜头，每个都短到不需要切小镜头（避免触发别的门，聚焦本测试要测的接线）
	plan := &project.Plan{Title: "渲染接线测试", Style: "国风电影级"}
	for i := 1; i <= shots; i++ {
		plan.Shots = append(plan.Shots, project.Shot{
			ID: i, Scene: "", ShotSize: "中景", Camera: "slow push in", AngleZH: "平视",
			Action: "他推开门", Duration: 4, HookType: "reveal",
			H3Prompt: "detailed_description:\nGUOFENG. [Shot 1] he opens the door and steps in.",
		})
	}
	if err := store.SavePlan(proj.ID, plan); err != nil {
		t.Fatal(err)
	}
	// 渲染参数：不用接缝、不要条件缓存（都不影响本测试，但少一层变数）
	proj.Config.Params.Render.Seam = false
	proj.Config.Params.Render.Steps = 8
	proj.Config.Params.Render.Accel = ""
	if err := store.SaveMeta(proj); err != nil {
		t.Fatal(err)
	}
	return c, fe, proj
}

// ① 没有方案 → 带指引的 ErrNoPlan（不是一句"渲染失败"）。
func TestRunRenderWithoutPlanGivesActionableError(t *testing.T) {
	root := t.TempDir()
	cfg, _ := config.NewManager(filepath.Join(root, "settings.json"), root)
	store, _ := project.NewStore(filepath.Join(root, "projects"))
	lib, _ := library.New(filepath.Join(root, "library"))
	proj, _ := store.Create("没有方案", project.ProjectConfig{Params: project.DefaultStageParams()})
	c := New(store, cfg, engine.NewRegistry(), new(ff.FF), NewBus(), lib)

	err := c.runRender(context.Background(), proj, nil, "", 0)
	if err == nil {
		t.Fatal("没有方案时必须报错，不能当成功")
	}
	// 用户拿到的必须是**能行动的那句**：告诉他去点哪个阶段
	if !strings.Contains(err.Error(), "剧本分镜") {
		t.Errorf("报错要带上「该去点哪里」，实际：%v", err)
	}
}

// ② 正常路径：每镜都真的调了引擎，且引擎拿到的是**管线的决定**（不是零值）。
func TestRunRenderDrivesEnginePerShot(t *testing.T) {
	c, fe, proj := renderFixture(t, 3)

	if err := c.runRender(context.Background(), proj, nil, "", 0); err != nil {
		t.Fatalf("三镜的渲染编排不该失败：%v", err)
	}
	if n := fe.callCount(); n != 3 {
		t.Fatalf("三镜该调三次引擎，实际 %d 次", n)
	}
	// 镜号必须按顺序、且**是它自己的那一镜**（接错的表现是"每镜都用第一镜的提示词"）
	for i := 1; i <= 3; i++ {
		call := fe.callOf(i)
		if call.ShotID != i {
			t.Errorf("第 %d 次调用该是镜头 %d，实际 %d（镜号接错会让整集渲成同一镜）", i, i, call.ShotID)
		}
		if !strings.Contains(call.Prompt, "opens the door") {
			t.Errorf("第 %d 次调用拿到的提示词不含这一镜的正文：%q", i, call.Prompt)
		}
		if call.OutputDir == "" {
			t.Errorf("第 %d 次调用没给产物目录 —— 引擎会写到当前工作目录", i)
		}
	}
}

// ③ 引擎说"成功"但没落产物 → 不能算这一镜完成。
//
// 这是"报成功但成片里没这段"的直接来源：清单里写着 done、播放时那一段是黑的。
func TestRunRenderRejectsSuccessWithoutArtifact(t *testing.T) {
	c, fe, proj := renderFixture(t, 1)
	fe.withPlan(fakeResult{emptyOut: true}) // 第一次调用：返回成功、不写文件

	err := c.runRender(context.Background(), proj, nil, "", 0)
	if err == nil {
		t.Fatal("引擎没落产物时不能报成功 —— 那会让清单显示 done 而成片缺一段")
	}
	m, merr := c.store.LoadManifest(proj.ID)
	if merr == nil {
		if item, ok := m["1"]; ok && item.Status == project.ShotDone {
			t.Error("没落产物的镜头不该被标成 done")
		}
	}
}
