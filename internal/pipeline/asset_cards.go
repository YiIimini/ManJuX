package pipeline

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// asset_cards.go 把技能侧**素材三卡的定妆串**接成角色参考图的身份真源（追补单 N4）。
//
// ## 规则
//
// 这本书目录里有 `素材/角色卡.md` 时，角色参考图（定妆照/转台/三视图板）的提示词**用卡片的定妆串**，
// 而不是平台 LLM 现写的那段 —— 两边都声称以《人物外形锚表》为准，但只要有**两套描述**，
// 就一定会有漂移；卡片是技能侧逐项照抄锚表 + 逐项英译的产物，把它当唯一真源最省事。
//
// ## 只在"有卡"时生效，且**不动风格**
//
//   - 没有卡片（或卡片里没有这个角色）→ 平台照旧用自己的 `image_prompt`，一个字节都不改；
//   - 卡片串里那段 `GUOFENG …` 风格句**先剥掉**再交出去：平台的介质/风格块是平台的职责，
//     而 `styleExpandFor` 只让位"比例"、不让位介质（见其注释）—— 两段风格句同场只会让模型取中值。
//
// ## 为什么落在资产阶段入口（而不是每处取提示词的地方）
//
// 资产阶段与"补缺"两条路都会**各自**读一次 plan 然后拿 `ch.ImagePrompt` 出图。
// 真源替换必须在**拿到 plan 之后、出第一张图之前**做一次，两条路才会看到同一个提示词；
// 分散到各调用点就是本项目记过的"同一个语义两处实现"。

// applyCardPrompts 用技能侧定妆串覆盖 plan 里角色的参考图提示词，返回覆盖了几个人。
//
// bookDir 由项目目录的**父目录名**（书名）在小说库里定位 —— 平台的目录约定是
// `data/projects/<书名>/<书名·第NN集·标题>/`，与 `data/小说/<书名>/` 同名，这是现成的对应关系。
func (c *Coordinator) applyCardPrompts(proj *project.Project, plan *project.Plan) int {
	if c == nil || plan == nil || proj == nil || c.store == nil || c.cfg == nil {
		return 0
	}
	root := strings.TrimSpace(c.cfg.Get().NovelDir)
	if root == "" {
		return 0
	}
	series := filepath.Base(filepath.Dir(c.store.Dir(proj.ID)))
	bookDir := filepath.Join(root, series)
	raw, err := os.ReadFile(filepath.Join(bookDir, "素材", "角色卡.md"))
	if err != nil {
		return 0
	}
	cards := project.ParseCharacterCards(string(raw))
	if len(cards) == 0 {
		return 0
	}
	boards := project.ParseCharacterCardBoards(string(raw))
	applied := 0
	for i := range plan.Characters {
		ch := &plan.Characters[i]
		p, ok := project.MatchCardPrompt(ch.Name, cards)
		if !ok || strings.TrimSpace(p) == "" {
			continue
		}
		// 板串（② 三视图细节板）同样按卡片覆盖一次 —— 板子的 15 格内容是技能侧口径。
		if bp, okb := project.MatchCardPrompt(ch.Name, boards); okb && strings.TrimSpace(bp) != "" {
			ch.BoardPrompt = bp
		}
		if strings.TrimSpace(ch.ImagePrompt) == p {
			continue // 已经是它了（重复调用不该重复计数）
		}
		ch.ImagePrompt = p
		applied++
	}
	if applied > 0 {
		c.emit(proj.ID, PhaseAssets, 0, "info", 0,
			"📇 %d 个角色的参考图提示词改用技能侧素材三卡的**定妆串**（身份真源唯一化；风格仍按全片注入）"+
				"—— 来源:%s", applied, filepath.ToSlash(filepath.Join(series, "素材", "角色卡.md")))
	}
	return applied
}
