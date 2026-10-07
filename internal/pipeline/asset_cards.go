package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
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

// backfillCastIdentity 出图前把角色卡的身份真源补齐，并**如实播报**缺口（幂等）。
//
// 【为什么落在资产阶段】（2026-10-07 用户问：「人物生成的角色提示词去哪了？墨洄、尉迟彻、断桅、
// 邵舷、霜见等不是主角的角色都随机生成的吗？」）
//
// 真机第01集：这五张卡没有 `description`、`image_prompt` 只有锚表兜底串（"一个古装人、58–66 岁"）
// ⇒ 定妆照的脸是现编的，撞脸门当场报警（墨洄与霜见"长得像同一个人"，重出一次仍像）。
// 补齐它们需要三步（锚表原文 → 专职会话英译 → 出图），而**只补 script 阶段是不够的**：
// 那要重跑剧本、会重排分镜、把已经渲过的镜头全废掉。放在这里，用户重跑「资产生成」就能修好。
//
// 三件事都是幂等的：补过不再动、真提示词一个字不覆盖、没有待办时不建 LLM 客户端。
func (c *Coordinator) backfillCastIdentity(ctx context.Context, proj *project.Project, plan *project.Plan) {
	if c == nil || proj == nil || plan == nil || c.store == nil {
		return
	}
	changed := 0
	src, _ := c.store.LoadSource(proj.ID)
	if strings.TrimSpace(src) != "" {
		if names := FillAnchorDescriptions(plan, src); len(names) > 0 {
			changed += len(names)
			c.emit(proj.ID, PhaseAssets, 0, "info", 0,
				"📇 %d 张角色卡的身份真源从【人物外形锚表】补回（原先没有描述，出图只能现编脸）：%s",
				len(names), strings.Join(names, "、"))
		}
		if cut := TruncatedAnchorSections(src); len(cut) > 0 {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
				"⚠ 【人物外形锚表】里这些小节被剧本正文截断了（源文本本身不全，只能照抄到断点）：%s —— "+
					"补全源文本后重跑本阶段，这些人的外形才能逐项照抄", strings.Join(cut, "、"))
		}
		// **锚表里也没有的人**：用小说原文的"首次出场段"当外形材料（用户 2026-10-07 问的就是这条真源）。
		if names := FillCastDescriptionsFromSource(plan, src); len(names) > 0 {
			changed += len(names)
			c.emit(proj.ID, PhaseAssets, 0, "info", 0,
				"📖 %d 张卡的外形材料取自**本集原文的首次出场段**（锚表里没有这几个人）：%s",
				len(names), strings.Join(names, "、"))
		}
	}
	if n := NeedsTranslationCount(plan); n > 0 {
		if client := c.cardTranslateClient(); client == nil {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
				"⚠ %d 张角色卡只有中文设定、需要专职会话译成英文定妆提示词，但**没有可用的 LLM**"+
					"（设置页未配置）—— 这些人出图时只有年龄与头身比，脸靠现编；配好 LLM 后重跑本阶段即可补上", n)
		} else if m, err := TranslateCardPrompts(ctx, client, plan); err != nil {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
				"⚠ 角色卡的英文定妆提示词生成失败(%v) —— 这些人出图时只有年龄与头身比，"+
					"服装/脸型/发型照不上设定；可重跑本阶段", err)
		} else if m > 0 {
			changed += m
			c.emit(proj.ID, PhaseAssets, 0, "success", 0,
				"✔ 已为 %d 张角色卡补写英文定妆提示词（逐项照抄锚表：脸型/眉眼/发型/服装/记号）", m)
		}
	}
	if changed > 0 {
		if err := c.store.SavePlan(proj.ID, plan); err != nil {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
				"⚠ 角色卡已补，但方案存盘失败(%v) —— 重跑本阶段会再来一次", err)
		}
	}
	if gaps := IdentityPromptGaps(plan); len(gaps) > 0 {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
			"⚠ 这些角色卡的参考图提示词**只有年龄**（身份没锁：出图会现编脸，跨镜/跨集很难一致）：%s",
			strings.Join(gaps, "、"))
	}
}

// cardTranslateClient 专职翻译会话要用的 LLM 客户端；未配置就返回 nil（调用方如实播报，不静默跳过）。
func (c *Coordinator) cardTranslateClient() *llm.Client {
	if c == nil || c.cfg == nil {
		return nil
	}
	g := c.cfg.Get()
	if strings.TrimSpace(g.LLM.BaseURL) == "" || strings.TrimSpace(g.LLM.Model) == "" {
		return nil
	}
	return llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
}
