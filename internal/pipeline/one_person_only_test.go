package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// one_person_only_test.go 定妆照的**图像提示词里不许出现「别人」**（2026-10-10 用户报的画面级缺陷）。
//
// 【用户原话】「咋每个角色的定妆照肩膀上都有三个 Q 型人物？」
//
// 【取证】装配后的真机卡（`char:char_shenyan`，6827 字）里有两处**别人的外形描述**：
//
//	① `CAST DISTINCTNESS (reference note, NOT to be drawn) — other cast members of this series:
//	   (A palm-sized miniature of a lean young man …); (A palm-sized flat matte-black shadow creature …); …`
//	② `FACE ID … NOT: A palm-sized miniature of a lean young man … NOT: A palm-sized flat matte-black
//	   shadow creature … NOT: A tall slim young man of twenty-one …`
//
// 名单里有两条是**巴掌大的小人**（Q 版形象与灵宠）⇒ 出图就多出几个小尺寸人物蹲在肩上。
// 2026-09-12 已经咬过同型一次（「郑伯的定妆照里出现了小尺寸的卫东与 Q 版江砚」），
// 当时加的修法是**否定句**（`Do NOT depict any of these other characters`）—— 而图像侧是
// **CFG=1.0 的蒸馏档：否定词不生效、名词会被当指令画出来**（`artstyle.go` 顶部 / `entity_prompts.go`
// 文件头）⇒「列出来 + 说不许画」注定只是半个办法。
//
// 【判据】装配出来的**图像提示词**：
//
//	a. 不许出现 `CAST DISTINCTNESS`（那块整块下线）；
//	b. 不许出现 `NOT: ` 那种「别人的外形」清单；
//	c. 不许出现**其他角色**的英文外形摘要（夹具给三个别人，逐条断言其特征串不在提示词里）；
//	d. **必须**还留着主体自己的身份锁与「这一帧只有一个人」的正面句（防撞脸的两层不能一起删光）。
//
// 防撞脸现在的两层（都不依赖图像模型的否定词能力）：
//   - 主体**自己**的身份锁逐字重述（`PortraitFaceIDClause`，只讲自己）；
//   - **出图后看图判身份**（`cast_distinct.go`：与同剧已生成角色逐张比，判撞脸就带
//     `CastSeparationClause` 换种子重出一次）。
func TestPortraitPromptNeverListsOtherCast(t *testing.T) {
	plan := &project.Plan{Style: "国风电影级", Characters: []project.Character{
		// 主体：真机形态（英文外形写在 image_prompt 里，description 是中文）
		{ID: "char_shenyan", Name: "沈砚", Description: "二十岁，瘦长脸，右眉尾旧疤",
			ImagePrompt: "IDENTITY LOCK (do not change): twenty years old; lean long narrow face with a clear " +
				"jawline and modest cheekbones, deep-black phoenix-style eyes with a slight upward tilt at the " +
				"outer corner, a thin old scar at the outer end of the left brow; grey hemp short work jacket."},
		// 别人三条：照真机那块名单的形态（两条「巴掌大」，一条另一个年轻人）
		{ID: "char_shenyan_q", Name: "沈砚·Q版", Kind: "chibi",
			ImagePrompt: "A palm-sized miniature of a lean young man at two and a half heads tall, big head on a " +
				"small short-legged body, long narrow face with a clear jawline."},
		{ID: "char_tuntun", Name: "吞吞", Kind: "spirit_pet",
			ImagePrompt: "A palm-sized flat matte-black shadow creature lying low on the ground, no eyes, no mouth, " +
				"no nose, its outline feathered and dissolving like ink blotted into paper."},
		{ID: "char_peijiu", Name: "裴九", Kind: "human",
			ImagePrompt: "A tall slim young man of twenty-one, angular melon-seed face with a narrow fleshless chin " +
				"and high cheekbones, the mid-face a little hollow, phoenix eyes with a faint upward tilt."},
	}}
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	got := PortraitPromptForProject(proj, plan, plan.Characters[0])

	// a/b：那两块整块下线
	if strings.Contains(got, "CAST DISTINCTNESS") {
		t.Errorf("定妆照的图像提示词里还有 CAST DISTINCTNESS（那是「别人的外形」清单，CFG=1.0 下会被画出来）：\n%s", got)
	}
	if strings.Contains(got, "NOT: ") {
		t.Errorf("定妆照的图像提示词里还有「NOT: 别人的外形」清单（同上，且否定词在图像侧不生效）：\n%s", got)
	}
	// c：别人的外形摘要（逐条真机形态的特征串）一个字都不许有
	for _, other := range []string{
		"palm-sized miniature",      // Q 版小人的体型
		"shadow creature",           // 灵宠
		"angular melon-seed face",   // 另一个年轻人的脸型
		"no eyes, no mouth",         // 灵宠的细节
		"two and a half heads tall", // 小人的比例
	} {
		if strings.Contains(got, other) {
			t.Errorf("别人的外形（%q）漏进了定妆照图像提示词 —— 用户看到的就是「肩上多了几个小人」：\n%s", other, got)
		}
	}
	// d：主体自己的身份锁与「这一帧只有一个人」必须还在（防撞脸的两层不能一起删光）
	for _, keep := range []string{
		"IDENTITY LOCK",
		"twenty years old; lean long narrow face",
		"ONE specific person",
		"do not fall back to a generic good-looking young face",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("主体自己的身份/单人帧纪律被一起删掉了（缺 %q）：\n%s", keep, got)
		}
	}
	// 再钉一次「图像侧不列别人」的**唯一出口**：装配点里不许再出现那两处调用。
	code := codeLines(readSrc(t, "prompts.go"))
	for _, bad := range []string{"castContrastBlock(", "castFaceIDs("} {
		if strings.Contains(code, bad) {
			t.Errorf("图像装配点还在调 %s —— 那是把「别人的外形」塞进提示词的入口（整块已下线）", bad)
		}
	}
}
