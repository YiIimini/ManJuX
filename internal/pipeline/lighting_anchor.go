package pipeline

import (
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// lighting_anchor.go **场级光照锚**（2026-09-30 用户：「尼玛，场景卡给谁，让谁去做？」）。
//
// ## 这条归谁
//
// 场景卡由**平台自己**产出（④场景会话 + `SceneImagePrompt` 装配），镜头提示词也由平台装配
// —— 所以这一条从头到尾都是平台侧的活，**不欠任何人**（我上一轮把它挂成"等你口径"是错的）。
//
// ## 为什么需要
//
// 实测那一集质检采样帧最暗占比：镜 8 **47%**、镜 10 **52%**、镜 11 **77%**（判黑屏不合格）。
// 每镜虽然都有亮度守卫句（11/11），但**同一场戏的曝光与主光方向没有统一口径**：
// 各镜各写各的，于是有的偏亮有的快黑，接在一起就跳。
//
// 场景卡本来就带 `LightState`（「日/夜 + 主光方向」，官方场景卡规范要求，缺了有门在报），
// 但那一项此前**只影响场景图本身**，没有进镜头提示词 —— 这一条就是把它接进去：
// **同一场戏的所有镜头共用一套光态**。
//
// ## 判据
//
//   - 镜头能对上场景卡（按 id，退回按名字）且卡里有 `light_state` → 写进场级光照锚；
//   - 光态里出现"夜/night"字样 → 追加一句"夜戏也必须有画面内可见光源"（用户口径）；
//   - 对不上卡或卡里没写 → **不加**（预检那道门已经在报"场景卡缺 light_state"，这里不重复报）。

// sceneLightingClause 场级光照锚（没有可锚的就返回空串）。
func sceneLightingClause(plan *project.Plan, shot *project.Shot) string {
	if plan == nil || shot == nil {
		return ""
	}
	sc, ok := sceneForShot(plan, *shot)
	if !ok {
		return ""
	}
	light := strings.TrimSpace(sc.LightState)
	if light == "" {
		return ""
	}
	clause := "\n- SCENE LIGHTING (scene-level, mandatory): every shot in \"" + sc.Name + "\" shares ONE light state — " +
		light + ". Keep the same key-light direction, the same colour temperature and the same exposure as the other " +
		"shots of this scene, so consecutive shots do not jump in brightness."
	// 时间档只有一处判据（project.LightTierOf）：**灯/烛/月不是时间词**。
	// 【2026-10-08 真机代价】旧词表把"灯/月"当夜 ⇒ 真机那本书 7 个场景里 **5 个白天场景被注入
	// "This is a NIGHT scene"** ⇒ 模型按夜景渲 ⇒ 成片 4 镜被判黑屏（其中一镜 100% 暗）、
	// 那几镜的台词整镜没念。用户报「视频看的莫名其妙的」。
	switch project.LightTierOf(light) {
	case project.LightNight:
		clause += " This is a NIGHT scene: a visible in-frame light source must be present (a lamp, a fire, " +
			"a shaft of moonlight with a readable direction) — a lightless near-black frame is not acceptable."
	case project.LightDim:
		// 傍晚/黄昏/黎明：不是夜戏，但也不能被当成大白天 —— 只要求"别掉进没有细节的黑"。
		clause += " This is a low-light (dusk/dawn) scene: the light sources written above must stay clearly " +
			"visible in frame and the main subject must stay readable — do not let the frame fall into near-black."
	case project.LightDay:
		// 白天场景**明确不许**照夜景渲：这一句是照着真机现场加的（场景图本身偏暗时模型会跟着暗）。
		clause += " This is a DAYLIT scene: keep the frame properly exposed and readable — do not turn it into " +
			"a night look, do not copy an underexposed look from the reference picture, and avoid any frame that " +
			"is nearly black or overly dark."
	}
	return clause
}

// sceneForShot 镜头对上的场景卡：先按 id（装配侧写的就是 id），退回按名字。
func sceneForShot(plan *project.Plan, shot project.Shot) (project.Scene, bool) {
	name := strings.TrimSpace(shot.Scene)
	if name == "" {
		return project.Scene{}, false
	}
	for _, sc := range plan.Scenes {
		if sc.ID == name || sc.Name == name {
			return sc, true
		}
	}
	// 名字带括注/写法差异时按基名再对一次（与实体对号同一套口径）
	base := project.StripBracketedName(name)
	for _, sc := range plan.Scenes {
		if project.StripBracketedName(sc.Name) == base {
			return sc, true
		}
	}
	return project.Scene{}, false
}
