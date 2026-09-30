package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 场级光照锚（2026-09-30 用户：「尼玛，场景卡给谁，让谁去做？」）。
//
// 这一条**从头到尾是平台侧的活**：场景卡由 ④场景会话产出、镜头提示词由平台装配 ——
// 我上一轮把它挂成"等用户口径"是错的。判据落在这里，夹具照真实场景卡形态写。
func TestSceneLightingAnchor(t *testing.T) {
	plan := &project.Plan{
		Scenes: []project.Scene{
			{ID: "scene_bilin", Name: "第七坡碑林坡地", LightState: "黄昏，主光从西侧山脊斜射，逆光轮廓"},
			{ID: "scene_dengtai", Name: "第一坡坡头灯台", LightState: "夜，唯一光源是灯台里的油灯"},
			{ID: "scene_nolight", Name: "荒坡", LightState: ""},
		},
	}
	// ① 有光态：必须写进场级锚，且点出"同一场共用一套光态"
	sh := project.Shot{ID: 1, Scene: "scene_bilin", H3Prompt: "x"}
	c := sceneLightingClause(plan, &sh)
	if !strings.Contains(c, "SCENE LIGHTING (scene-level") {
		t.Fatalf("没产出场级光照锚：%q", c)
	}
	if !strings.Contains(c, "黄昏") {
		t.Errorf("光照锚没带上场景卡的光态：%q", c)
	}
	if strings.Contains(c, "NIGHT scene") {
		t.Error("黄昏场被判成了夜戏")
	}
	// ② 夜戏：追加"画面内必须有可见光源"（用户口径）
	sh2 := project.Shot{ID: 2, Scene: "scene_dengtai"}
	c2 := sceneLightingClause(plan, &sh2)
	if !strings.Contains(c2, "NIGHT scene") || !strings.Contains(c2, "visible in-frame light source") {
		t.Errorf("夜戏没要求画面内可见光源：%q", c2)
	}
	// ③ 卡里没写光态 / 对不上卡 ⇒ 不加（预检那道门已经在报，别重复）
	if c3 := sceneLightingClause(plan, &project.Shot{ID: 3, Scene: "scene_nolight"}); c3 != "" {
		t.Errorf("卡里没光态时不该硬编一句：%q", c3)
	}
	if c4 := sceneLightingClause(plan, &project.Shot{ID: 4, Scene: "不存在的场景"}); c4 != "" {
		t.Errorf("对不上卡时不该加：%q", c4)
	}
	// ④ 按**名字**也要能对上（装配侧写 id，但老包可能写名字）
	byName := project.Shot{ID: 5, Scene: "第七坡碑林坡地"}
	if c := sceneLightingClause(plan, &byName); c == "" {
		t.Error("按场景名对不上卡")
	}
	// ⑤ 幂等：装配点重复调用不会叠加两次
	sh5 := project.Shot{ID: 6, Scene: "scene_bilin", H3Prompt: "x"}
	proj := &project.Project{}
	_ = ShotRenderPrompt(proj, plan, &sh5)
	first := sh5.H3Prompt
	_ = ShotRenderPrompt(proj, plan, &sh5)
	if strings.Count(sh5.H3Prompt, "SCENE LIGHTING (scene-level") != 1 {
		t.Errorf("光照锚被叠加了多次：%d 次", strings.Count(sh5.H3Prompt, "SCENE LIGHTING (scene-level"))
	}
	if len(first) == 0 {
		t.Error("装配后提示词为空")
	}
}
