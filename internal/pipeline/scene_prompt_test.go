package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 场景参考图的装配闸门(2026-09-26 乙1:本批唯一未修的高危)。
//
// 事故形态:场景图**四条出图路径各自**把 LLM 的 image_prompt 原样丢给图像模型,
// 而角色走 styleExpand 装配 —— 同一部剧里人物是国漫二维、空景却是另一套介质。
// 场景图同样是 H3 的参考图,参考图与成片不在同一个视觉世界,模型只能二选一地糊。
//
// 这里守三件事:
//  1. 场景提示词必须带**全片风格串**(与角色同源);
//  2. 上游点名写实/3D 时**不许**再叠"二维/赛璐璐"介质块(与角色侧同一道守卫);
//  3. 所有会出像素/入库的场景路径都走 SceneImagePrompt(源码级闸门,行为测试覆盖不到漏网的那条)。

func scenePlan() *project.Plan {
	return &project.Plan{
		Style: "国漫二维动画, 赛璐璐平涂, 柔和低对比光",
		Scenes: []project.Scene{{
			ID: "sc01", Name: "临水城前街", Description: "雨后的青石长街与木牌楼",
			ImagePrompt: "wide view of a wet stone street in an old Chinese town, wooden archway, " +
				"plain background, upper-body framing, front-facing",
		}},
	}
}

// TestSceneImagePromptInjectsSeriesStyle 场景图必须与角色图**同一套画风**:
// 全片风格串要进提示词,而人像取景要被换成环境空场。
func TestSceneImagePromptInjectsSeriesStyle(t *testing.T) {
	plan := scenePlan()
	sc := plan.Scenes[0]
	got := SceneImagePrompt(&project.Project{}, plan, sc)

	if !strings.Contains(got, "赛璐璐平涂") {
		t.Errorf("场景提示词必须带上全片风格串(plan.Style),否则同一部剧两套画风:\n%s", got)
	}
	if !strings.Contains(got, "SCENE FIDELITY") {
		t.Errorf("场景缺少忠实度块(装配没走到 KindScene 那一套):\n%s", got)
	}
	if !strings.Contains(got, "establishing") || !strings.Contains(got, "unpeopled") {
		t.Errorf("场景取景必须是「整块地方的空场环境图」:\n%s", got)
	}
	// LLM 从人像模板抄来的取景必须被剥掉:同一段提示词里不能同时要"整块地方"和"半身正面"
	low := strings.ToLower(got)
	for _, bad := range []string{"upper-body", "upper body", "front-facing"} {
		if strings.Contains(low, bad) {
			t.Errorf("场景提示词里残留人像取景 %q —— 模型会画成半身人像:\n%s", bad, got)
		}
	}
	// 场景绝不能被套上"人的脸必须与文字一致"那一段(那是 KindHuman 的条款)
	if strings.Contains(got, "IDENTITY FIDELITY") {
		t.Errorf("场景被套上了人像忠实度块(类型分发错了):\n%s", got)
	}
}

// TestSceneImagePromptKeepsPhotorealGuard 上游点名写实/3D 时**不许**再叠二维介质块 —— 与
// 角色侧 styleExpand 同一道守卫(guard 一旦只在角色那半生效,场景就又把画风拽回赛璐璐)。
func TestSceneImagePromptKeepsPhotorealGuard(t *testing.T) {
	cases := []struct {
		name     string
		style    string
		medium   bool   // 期望出现"二维/赛璐璐"介质块
		evidence string // 期望出现的写实/3D 场景词
	}{
		{"写实电影风", "PHOTOREAL 3D CINEMATIC WORLD, SEMI-STYLISED ANIME-ADJACENT FACES, matte finish",
			false, "3D CG environment render"},
		{"真人实拍", "真人实拍质感 + 港片胶片", false, "photorealistic cinematic environment"},
		{"3D 皮克斯", "PIXAR-STYLE 3D RENDER, SOFT GLOBAL ILLUMINATION", true, "3D CG environment"},
		{"国漫二维", "国漫二维动画, 赛璐璐平涂", true, "painted background art"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := &project.Plan{Style: tc.style, Scenes: []project.Scene{{
				ID: "sc01", Name: "地点", ImagePrompt: "wide view of a deserted street",
			}}}
			got := SceneImagePrompt(&project.Project{}, plan, plan.Scenes[0])
			if has := strings.Contains(got, MediumMarker); has != tc.medium {
				t.Errorf("介质块出现 = %v,期望 %v(风格 %q):\n%s", has, tc.medium, tc.style, got)
			}
			if !strings.Contains(got, tc.evidence) {
				t.Errorf("风格 %q 下场景应落到环境口径 %q:\n%s", tc.style, tc.evidence, got)
			}
			// 场景条款里不许出现人像/皮肤类措辞(否则就是在逼模型画人)
			for _, bad := range []string{"skin texture", "facial features"} {
				if strings.Contains(got, bad) {
					t.Errorf("场景提示词里出现了人像措辞 %q:\n%s", bad, got)
				}
			}
		})
	}
}

// TestSceneImagePromptIsIdempotent 幂等判据只有一个:SCENE FIDELITY。
// 资产阶段装配一次、渲染前补齐再装配一次,不得叠加。
func TestSceneImagePromptIsIdempotent(t *testing.T) {
	plan := scenePlan()
	sc := plan.Scenes[0]
	once := SceneImagePrompt(&project.Project{}, plan, sc)
	sc.ImagePrompt = once
	twice := SceneImagePrompt(&project.Project{}, plan, sc)
	if twice != once {
		t.Errorf("第二次装配不得改动结果(幂等):\n一次:%s\n二次:%s", once, twice)
	}
	if n := strings.Count(twice, "SCENE FIDELITY"); n != 1 {
		t.Errorf("忠实度块应恰好 1 份,实际 %d", n)
	}
	if n := strings.Count(twice, "RENDER STYLE"); n != 1 {
		t.Errorf("风格块应恰好 1 份,实际 %d", n)
	}
}

// TestSceneImagePromptToleratesMissingContext 服务端两条入库路径拿不到完整上下文时
// (proj/plan 可能为 nil)也不许 panic,并按平台默认(动漫化开关 = 开)装配。
func TestSceneImagePromptToleratesMissingContext(t *testing.T) {
	got := SceneImagePrompt(nil, nil, project.Scene{ID: "sc01", Name: "地点", ImagePrompt: "a wet street"})
	if !strings.Contains(got, "SCENE FIDELITY") {
		t.Errorf("没有项目上下文时也必须装配出场景条款:\n%s", got)
	}
	if !strings.Contains(got, MediumMarker) {
		t.Errorf("没有风格串时按平台兜底画风(动漫化默认开),介质块必须留下:\n%s", got)
	}
	if !strings.Contains(got, "a wet street") {
		t.Errorf("原始提示词不能丢:\n%s", got)
	}
}

// TestAllSceneImagePathsUseSceneAssembly 源码级闸门:**所有**会出像素/写库的场景路径
// 都必须走唯一装配点,并且不许再出现"直接摆 sc.ImagePrompt"的写法。
//
// 为什么是源码级:漏掉的形态是"某一条路径又自己拼了一遍",行为测试只覆盖它自己走的那条
// (与 imagesource_test.go / refmanifest_test.go 的成对闸门同一思路)。
func TestAllSceneImagePathsUseSceneAssembly(t *testing.T) {
	cases := []struct {
		file     string
		minCalls int
		why      string
	}{
		{"stages_assets.go", 2, "资产阶段 check + 渲染前 backfillAssets 两条"},
		{"coordinator.go", 1, "单个资产「重新生成」(第五条出像素的路径)"},
		{filepath.Join("..", "server", "handlers_asset.go"), 1, "上传即入库(库元数据会被后续项目复用)"},
		{filepath.Join("..", "server", "frame_asset.go"), 1, "视频截帧定妆入库"},
	}
	for _, tc := range cases {
		raw, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("读不到 %s: %v", tc.file, err)
		}
		src := string(raw)
		if n := strings.Count(src, "SceneImagePrompt("); n < tc.minCalls {
			t.Errorf("%s 里 SceneImagePrompt( 只有 %d 处,至少应有 %d 处(%s)—— 这条路径还在用裸场景提示词",
				tc.file, n, tc.minCalls, tc.why)
		}
		for i, ln := range strings.Split(src, "\n") {
			code := ln
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx] // 注释不算
			}
			if strings.Contains(code, "sc.ImagePrompt") &&
				!strings.Contains(code, "SceneImagePrompt(") &&
				!strings.Contains(code, "prompt := sc.ImagePrompt") { // 装配点自己的入口
				t.Errorf("%s:%d 直接取用了原始场景提示词 —— 必须走 SceneImagePrompt(见乙1):%s",
					tc.file, i+1, strings.TrimSpace(ln))
			}
		}
	}
}
