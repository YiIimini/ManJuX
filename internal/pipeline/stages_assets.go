package pipeline

import (
	"context"
	"fmt"
	"hash/fnv"
	"image"
	_ "image/jpeg" // 复用来的定妆照/场景图可能是 jpeg:注册解码器才能读宽高
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/fsutil"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
)

func (c *Coordinator) runAssets(ctx context.Context, proj *project.Project, onlyAssets []string) error {
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		return fmt.Errorf("尚未生成分镜方案:请先运行「剧本分镜」阶段")
	}
	// 身份真源唯一化：这本书有技能侧素材三卡就用它的定妆串（追补单 N4，见 asset_cards.go）。
	// 放在这里而不是各出图点：下面每条路径（初出/补缺/多视角/三视图板）都从 plan 取提示词，
	// 在**读完之后、出第一张图之前**覆盖一次，四条路才会看到同一个真源。
	c.applyCardPrompts(proj, plan)
	assets, err := c.store.LoadAssets(proj.ID)
	if err != nil {
		return err
	}
	p := proj.Config.Params.Assets
	// 风格要真人、但角色动漫化开关开着:必须当场说清"为什么没按你选的风格走"
	if notice := styleFallbackNotice(proj, plan); notice != "" {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⚠ %s", notice)
	}

	// 自动生成缺失参考图(定妆照/场景图 = 项目的成片画幅,见 assetImageSize:
	// 参考图与成片同比例,是"构图漂移"的第一道闸)
	var comfyGen engine.Engine
	if p.Source == "krea2" {
		eng, err := c.reg.Get("comfy")
		if err != nil {
			return err
		}
		if err := eng.Available(ctx); err != nil {
			return fmt.Errorf("自动定妆需要本地 ComfyUI: %w", err)
		}
		comfyGen = eng
	}

	genAsset := func(kind, id, name, imagePrompt string, ek EntityKind, attempt int) (string, error) {
		return c.genAssetImage(ctx, proj, comfyGen, kind, id, name, imagePrompt, assets, ek, attempt)
	}

	// ---------- 定妆照比例自检(技能侧清单 §5.1)----------
	//
	// 定妆照**出图后**按技能侧的比例锚点核对三庭/眼距/头身比,明显偏离就告警并建议重出该卡
	// (实现与判据见 internal/qc/proportion.go —— 阈值口径全部来自技能侧比例句,平台只做
	// "测与报")。三态开关默认开;关掉连事件都不发(不留痕)。
	//
	// 三条处置必须能分辨,否则这一步会变成"看起来验过了"(与画面文字检测同一要求):
	//   - 关了(质检总开关 / 比例自检开关)或类型不适用(器物没有人体比例) → **连事件都不发**;
	//   - 开着但没有视觉模型 → **如实降级**,且每次运行只提醒一次(每张卡刷一条等于噪音);
	//   - 跑了但失败(读图/调用/解析) → 同样如实告警,不当作通过。
	//
	// 记账放在调用方(而不是共享实现里):"一次运行只提醒一次"是**本次运行**的性质,
	// 而共享实现也被「单资产重新生成」那条路用(那次是一次用户动作,每次都该提醒)。
	propNoCapWarned := false
	checkProportion := func(ek EntityKind, imagePrompt, name, path string) *qc.ProportionReport {
		rep, outcome := c.checkPortraitProportion(ctx, proj, ek, imagePrompt, name, path)
		if outcome == proportionNoCapability && !propNoCapWarned {
			propNoCapWarned = true
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⚠ %s", qc.ProportionSkipNoCapability)
		}
		return rep
	}

	missing := 0
	total := 0
	reused := 0
	stopped := 0 // 被停止(而非"生成失败")的资产数:停止是用户动作,不能记成失败
	onlySet := map[string]bool{}
	for _, id := range onlyAssets {
		onlySet[id] = true
	}
	// pendingViews 攒到定妆照全部出完后**统一**生成多视角转台图。
	//
	// 为什么不边出定妆照边出多视角(旧写法):多视角走 H3(约 20GB),定妆照走
	// Krea-2 —— 一个角色一次来回,10 个角色就是 20 次模型换入换出,显存反复腾挪
	// (代码里记过的"每步 150 秒"就是这种挤压)。攒起来先跑完所有 Krea-2 再跑所有
	// H3,模型各加载一次,资产阶段整体快得多(2026-09-12「管线生产需要合理化」)。
	type pendingView struct {
		id, name, prompt, master, description string
		kind                                  EntityKind
	}
	var pendingViews []pendingView
	check := func(kind, id, name, description, imagePrompt string, ek EntityKind) {
		if len(onlySet) > 0 && !onlySet[id] {
			return
		}
		total++
		if a, ok := assets[id]; ok && a.File != "" {
			full := filepath.Join(c.store.Dir(proj.ID), a.File)
			if _, err := os.Stat(full); err == nil {
				if fp := project.FingerprintFile(full); fp != a.Fingerprint {
					a.Fingerprint = fp
					assets[id] = a
				}
				// 存量回填:项目已有资产若库中无同名条目,自动沉淀入库。
				// 判据用 Find(而不是只认精确名):同一角色换了叫法时库里已有条目,
				// 只认精确名会拿本集叫法**新建一条**,同一角色在库里变成两条
				// (描述为空的那条从此再也不会被别名/描述匹配命中)。
				if c.lib != nil {
					if _, _, ok := c.lib.Find(kind, name, description); !ok {
						if _, err := c.lib.Put(kind, name, description, imagePrompt, full, proj.ID, a.Source); err == nil {
							c.emit(proj.ID, PhaseAssets, 0, "info", 0, "已入库:%s「%s」(存量回填)", kindLabel(kind), name)
						}
					}
				}
				return
			}
		}
		if ctx.Err() != nil { // 已停止:不再尝试生成,剩下的交给续跑
			stopped++
			return
		}
		// ① 资产库复用:名称精确匹配之外还认"同一角色的另一种叫法"(别名/描述指纹),
		//    否则 LLM 换个称呼就要重出一张定妆照 —— 用户看到的正是"相同角色无中生有
		//    多产生出定妆照"(2026-09-12 报障)。判定为同一人时库里会记下别名。
		//    实现与渲染前的 backfillAssets 共用(reuseFromLibrary):两条补齐路径必须
		//    同一套"先查库、再生成"语义(见该函数的说明)。
		if r, ok := c.reuseFromLibrary(proj.ID, kind, id, name, description); ok {
			assets[id] = project.Asset{
				ID: id, Kind: kind, Name: name,
				File: r.Rel, ImagePrompt: r.PromptFor(imagePrompt),
				Source: "library", Fingerprint: project.FingerprintFile(r.Dest),
			}
			// 场景图的方向必须与本项目画幅一致:库按 kind+name 命中,条目里没有宽高,
			// 于是某个场景的朝向由"第一个生成它的项目"单方面锁定 —— 竖幅项目复用横向
			// 场景图(或反过来)时,参考图与成片画幅不一致,H3 只能自行裁切/拉伸,构图与
			// 信息量错位而界面上看不出任何异常。这里不静默喂进去:发现就如实说清楚,
			// 由用户决定重生成(重生成会写入共享条目,故不替用户做主)。
			if kind == "scenes" {
				rw := proj.Config.Params.Render.Width
				rh := proj.Config.Params.Render.Height
				if iw, ih, ierr := imagePixelSize(r.Dest); ierr == nil && sceneOrientationMismatch(iw, ih, rw, rh) {
					c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
						"⚠ 复用的场景图「%s」方向与本项目画幅不一致(图 %d×%d,项目 %d×%d):渲染时模型只能自行裁切/拉伸 —— 需要贴合画幅请对这张图点「重新生成」",
						name, iw, ih, rw, rh)
				}
			}
			reused++
			via := ""
			if r.Name != name {
				via = fmt.Sprintf("(同一角色,已按「%s」的定妆照复用)", r.Name)
			}
			if r.Copied > 0 {
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "♻ 资产库复用:%s「%s」%s(跨项目共享,免生成;含 %d 张多视角)", kindLabel(kind), name, via, r.Copied)
			} else {
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "♻ 资产库复用:%s「%s」%s(跨项目共享,免生成)", kindLabel(kind), name, via)
			}
			return
		}
		// ② Krea-2 生成缺失参考图;成功后自动入库供后续项目复用
		if comfyGen != nil && strings.TrimSpace(imagePrompt) != "" {
			if path, err := genAsset(kind, id, name, imagePrompt, ek, 0); err == nil {
				rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
				assets[id] = project.Asset{
					ID: id, Kind: kind, Name: name,
					File: filepath.ToSlash(rel), ImagePrompt: imagePrompt,
					Source: c.imageSourceKey(), Fingerprint: project.FingerprintFile(path),
				}
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ 已生成:%s「%s」%s",
					kindLabel(kind), name, entityKindSuffix(kind, ek))
				// 定妆照出图后立刻做一次比例自检(见 checkProportion 的说明):
				// 早发现早重出,别等到成片里才看出"脸被拉长/眼距失衡"。
				// 同一份自检里也量**朝向**(构图项):定妆照是全片身份的权威参考图,
				// 出了四分之三侧脸会让 H3 拿侧脸当身份锚 —— 而种子是按名字锚定的
				// ("同名必同图"),不换种子它就会一直是那张侧脸(见 regenerateOnBadFacing)。
				if kind == "characters" {
					if rep := checkProportion(ek, imagePrompt, name, path); rep != nil {
						path = c.regenerateOnBadFacing(ctx, proj, assets, kind, id, name, imagePrompt, ek, path, rep, genAsset)
					}
				}
				// 角色的多视角转台图排进第二阶段(见 pendingViews 注释)
				if kind == "characters" && p.ViewsOn() {
					pendingViews = append(pendingViews, pendingView{
						id: "char:" + id, name: name, prompt: imagePrompt,
						master: path, description: description, kind: ek,
					})
				}
				if c.lib != nil {
					if _, err := c.lib.Put(kind, name, description, imagePrompt, path, proj.ID, c.imageSourceKey()); err == nil {
						c.emit(proj.ID, PhaseAssets, 0, "info", 0, "已入库:%s「%s」(后续项目自动复用)", kindLabel(kind), name)
					}
				}
				return
			} else if ctx.Err() != nil {
				// 停止导致的取消:不是"生成失败",别在界面上刷一排红字
				c.emit(proj.ID, PhaseAssets, 0, "info", 0, "已停止:%s「%s」未生成(续跑时自动补齐)", kindLabel(kind), name)
				stopped++
				return
			} else {
				c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "生成失败:%s「%s」:%v", kindLabel(kind), name, err)
			}
		}
		missing++
		if p.Source == "upload" {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "缺少参考图:%s「%s」(可上传,缺失镜头将降级纯文生)", kindLabel(kind), name)
		}
	}
	// 造型变体卡必须排在**同族主卡之后**生成(§4.1):变体卡的定妆照是从主卡派生出来的,
	// 主卡还没出图时本集就只剩"退回文生图"这一条路(而各自文生图正是同一角色几张脸会漂的原因)。
	// 分组只调整**生成顺序**,不改任何判据(计划里的其它语义都不依赖角色顺序)。
	for _, ch := range orderCharsMasterFirst(plan.Characters) {
		check("characters", "char:"+ch.ID, ch.Name, ch.Description, characterImagePrompt(proj, plan, ch), ResolveEntityKind(ch))
	}
	for _, sc := range plan.Scenes {
		// 场景提示词走**唯一装配点**(注入全片风格串 + 环境空场取景):
		// 直接把 sc.ImagePrompt 丢给图像模型 = 角色一套画风、场景另一套风格
		// (2026-09-26 乙1,四条出图路径必须同源)。
		check("scenes", "scene:"+sc.ID, sc.Name, sc.Description, SceneImagePrompt(proj, plan, sc), "")
	}
	// 道具图（2026-09-27 补）：技能侧的三类卡里，道具此前**只被读进来、从没出过图**
	// —— 库里 `props` 类目、上传的"道具图"、`assetImageSize` 注释里那句"道具图"都在，
	// 唯独资产阶段没遍历它。这里补齐：1:1 白底单件的单件图，供人审与跨镜一致性；
	// **不进 H3 的参考图集合**（官方：Props never get a picture；refs.go 只带角色与场景）。
	// 跨集复用靠资产库按名字命中（同书 40 集只生成一次），所以导入时把道具全带上不浪费。
	for _, pr := range plan.Props {
		check("props", "prop:"+pr.ID, pr.Name, pr.Description, PropImagePrompt(proj, plan, pr), KindObject)
	}

	// Q 版形象(chibi):主定妆照就绪的角色同步生成 Q 版,内心活动镜头的表达素材。
	// 独立资产 ID(char:<id>_chibi),渲染 ref 不携带,仅作画面内容/花絮素材。
	//
	// **方案里已经有 Q 版角色卡时不再自动生成**(2026-09-25 用户报「Q版形象相同的角色
	// 创建多个」):技能端会把"内心Q版化身"写成一张独立角色卡(本机实例:char_chibi
	// 「迷你青禾(内心Q版)」),而平台又会给每个真人角色再造一份 <id>_chibi ——
	// 同一个 Q 版形象于是有两套资产、两个库条目(实测库里同时存在「迷你青禾」与
	// 「迷你青禾内心Q版」)。技能端既然已经明确指定了 Q 版形象,平台就不该再猜一份。
	planHasChibi := planHasChibiCard(plan)
	if planHasChibi {
		c.emit(proj.ID, PhaseAssets, 0, "info", 0, "方案里已有 Q 版角色卡,跳过平台自动 Q 版(避免同一形象出现两份)")
	}
	if proj.Config.Params.Script.Chibi && comfyGen != nil && !planHasChibi {
		for _, ch := range plan.Characters {
			chibiID := "char:" + ch.ID + "_chibi"
			if a, ok := assets[chibiID]; ok && a.File != "" {
				if _, err := os.Stat(filepath.Join(c.store.Dir(proj.ID), a.File)); err == nil {
					continue
				}
			}
			// 复用同 seed 机制,保证同角色 Q 版稳定
			safe := director.SanitizeForCache("characters_char_" + ch.ID + "_chibi")
			out := filepath.Join(c.store.Dir(proj.ID), "assets", "characters", safe+".png")
			// Q 版走**Q 版那一套渲染方法**(取景/忠实度/画风都不同:大头小身、保留身份
			// 记号、不得拉回真人头身比)。此前这里手写了一句 chibi 前缀再套人类条款 ——
			// 非人角色(器物/灵宠/妖兽/灵体)的 Q 版因此仍然长着人脸;更糟的是那段
			// 前缀是**追加**在 LLM 的人像取景之后,同一段提示词里同时要求"大头身"和
			// "upper-body framing, front-facing"。现在统一走唯一装配点:取景由该类型
			// 自己的 SheetFraming **替换**掉人像尾巴(审计 P1-2)。
			chibiPrompt := PortraitPrompt(PortraitSubject{
				Kind: KindChibi, Name: ch.Name, Description: ch.Description,
				ImagePrompt: ch.ImagePrompt, FacesOn: proj.Config.Params.Script.FacesOn(),
			}, plan)
			// Q 版种子必须与主定妆照同一套派生(审计 P1-2):这里原先用
			// `len(ch.ID)*1000 + len(ch.Name)*7 + 13` —— **长度哈希**,
			// 两个名字长度相同的角色会撞到同一个种子(正是主定妆照早就改掉的坑)。
			seed := nameSeed("chibi", ch.ID)
			c.emit(proj.ID, PhaseAssets, 0, "info", 0, "生成:「%s」Q 版形象…", ch.Name)
			if path, err := comfyGen.GenerateImage(ctx, chibiPrompt, imageNegative, seed, 1024, 1024, out); err == nil {
				rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
				assets[chibiID] = project.Asset{
					ID: chibiID, Kind: "characters", Name: ch.Name + " · Q版",
					File: filepath.ToSlash(rel), ImagePrompt: chibiPrompt,
					Source: c.chibiSourceKey(), Fingerprint: project.FingerprintFile(path),
				}
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ Q 版就绪:「%s」(内心活动素材)", ch.Name)
				// Q 版默认不做人体比例自检(它故意就是二到三头身),只有这张卡自带
				// 技能侧人体比例句时才测 —— 判据在 PortraitProportionCheckable 里。
				checkProportion(KindChibi, chibiPrompt, ch.Name+" · Q版", path)
			} else {
				c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "Q 版生成失败(不影响主流程):%v", err)
			}
		}
	}

	// ---------- 第二阶段:多视角转台图(H3 一次加载,批量出完) ----------
	// 与定妆照分阶段跑:本段全部是 H3 作业,模型只需加载一次(见 pendingViews 注释)。
	if len(pendingViews) > 0 && ctx.Err() == nil {
		c.emit(proj.ID, PhaseAssets, 0, "info", 0, "阶段②:多视角 %d 个角色(按类型分别用转身/转台/旋动,每个约数分钟)…", len(pendingViews))
		viewsDone := 0
		for _, v := range pendingViews {
			if ctx.Err() != nil {
				break
			}
			c.genCharacterViews(ctx, proj, comfyGen, strings.TrimPrefix(v.id, "char:"), v.name, v.prompt, v.master, v.kind)
			viewsDone++
			// 多视角写回资产库:同一个角色全局只付一次转身成本,后续项目复用直接带走
			if c.lib != nil {
				if _, err := c.lib.Put("characters", v.name, v.description, v.prompt, v.master, proj.ID, c.imageSourceKey()); err != nil {
					c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "多视角写回资产库失败:角色「%s」:%v", v.name, err)
				}
			}
		}
		if viewsDone < len(pendingViews) {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⏹ 多视角转台图已停止:%d/%d 完成,其余将在续跑时补齐(定妆照已就绪,不受影响)",
				viewsDone, len(pendingViews))
		}
	}

	if err := c.store.SaveAssets(proj.ID, assets); err != nil {
		return err
	}
	if p.Source == "skip" {
		c.emit(proj.ID, PhaseAssets, 0, "success", 100, "✔ 资产策略=跳过(全部镜头纯文生 t2va)")
		return nil
	}
	pct := 100
	if total > 0 {
		pct = (total - missing) * 100 / total
	}
	// 停止语义:只要本次运行被取消,就如实说"停在哪、剩下的怎么办",不要报 ✔,
	// 也不要预告"将降级纯文生" —— 未生成的资产在续跑时会自动补齐。
	// (2026-09-12 用户反馈「场景也有失败」:其中 8 条其实是停止时被取消的任务,
	// 旧文案把 context canceled 记成"生成失败"、还补一句"将以 t2va 渲染",误导。)
	if stopped > 0 || ctx.Err() != nil {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⏹ 资产阶段已停止:定妆照已就绪 %d/%d,其余 %d 个未生成(续跑时自动补齐,不会降级纯文生)",
			total-missing-stopped, total, stopped)
		return nil
	}
	// 「造型卡 N 张 / 角色 M 位」两个数都给:技能侧把造型档做成独立卡之后,按卡计数会
	// 把《翻篇》的 12 个角色报成 21 个,用户看到的就是"怎么多出来一堆新角色"(§4.2)。
	c.emit(proj.ID, PhaseAssets, 0, "success", pct, "✔ 资产就绪:%d/%d(角色 %d 位 / 造型卡 %d 张 / 场景 %d;库复用 %d)",
		total-missing, total, project.CharFamilyCount(plan.Characters), len(plan.Characters), len(plan.Scenes), reused)
	if missing > 0 {
		c.emit(proj.ID, PhaseAssets, 0, "warn", pct, "提示:缺 %d 张参考图,对应镜头将以 t2va 纯文生渲染", missing)
	}
	return nil
}

// proportionCheckPlan 一张定妆照的比例自检该怎么走(纯函数,便于穷举)。
type proportionCheckPlan struct {
	Run     bool   // 真的去读图 + 调视觉模型
	Skipped string // 不跑的原因(写进事件流;空串 = 连事件都不发)
}

// planProportionCheck 由(开关、视觉模型可用性、类型是否适用)决定本次怎么处置。
//
// 抽成纯函数是为了让"开关真的能关""类型不适用不算没验过""没配模型不假装验过"这三条
// 可被穷举验证 —— 这类缺陷(功能开着却静默跳过、或事件流里看不出到底量没量)不会报错,
// 只有针对性断言能钉住(与 planTextDetect 同一手法)。
func planProportionCheck(enabled, haveClient, applicable bool) proportionCheckPlan {
	switch {
	case !enabled:
		// 关掉的功能不该在事件流里留痕:否则"关掉"看起来就像"每张卡都量过且合格"。
		return proportionCheckPlan{Run: false, Skipped: ""}
	case !applicable:
		// 类型不适用(器物/灵宠/妖兽/无实体没有人体比例,或 Q 版故意的二到三头身):
		// 这不是"没验过",是"没这项" —— 同样不留痕,免得每张非人卡都刷一条。
		return proportionCheckPlan{Run: false, Skipped: ""}
	case !haveClient:
		// 开着却没有可用能力:如实降级(调用方会把它写成一条 warn,不假装通过)。
		return proportionCheckPlan{Run: false, Skipped: qc.ProportionSkipNoCapability}
	default:
		return proportionCheckPlan{Run: true}
	}
}

// proportionCheckOutcome 一次"定妆照出图后"的比例自检实际走到了哪一步。
//
// 为什么要分成三态而不是 bool:调用方需要**区分**"没这项"与"开着却没有能力" ——
// 前者连事件都不发,后者必须如实降级(但一次运行只提醒一次)。bool 会把两者混成一个
// "false",于是要么每张非人卡都刷一条噪音,要么把"没能力"也静默掉。
type proportionCheckOutcome int

const (
	proportionNotApplicable proportionCheckOutcome = iota // 开关关闭 / 类型不适用:连事件都不发
	proportionNoCapability                                // 开着但没有可用视觉能力:调用方负责如实留痕
	proportionChecked                                     // 真的量了(偏离/失败已按需告警)
)

// checkPortraitProportion 定妆照出图后的比例自检 —— **唯一实现点**(资产阶段与
// 「单资产重新生成」共用同一份,免得两条出图路径量出两种结论)。
//
// 它自己负责:读图 → 调视觉模型 → 明显偏离时发 warn(文案点名哪一项、期望多少、量到多少)。
// 它**不**负责:没有可用能力时的留痕(那是调用方的记账,见 proportionCheckOutcome 的说明)。
//
// 返回的 *qc.ProportionReport 供调用方做进一步处置(当前只有一处:**朝向明显偏离时换种子
// 重出**,见 regenerateOnBadFacing);没跑/失败/无能力时返回 nil —— nil 表示"没量到",
// 任何"据此重出"的判断都必须把 nil 当作**不重出**(不能拿"没验过"当"不合格")。
func (c *Coordinator) checkPortraitProportion(ctx context.Context, proj *project.Project, ek EntityKind, imagePrompt, name, path string) (*qc.ProportionReport, proportionCheckOutcome) {
	// 质检总开关关掉时不做(用户明确表示不要质检);细粒度由比例自检自己那条三态开关管。
	enabled := proj.Config.Params.QC.Enabled && proj.Config.Params.QC.ProportionCheckOn()
	applicable := PortraitProportionCheckable(ek, imagePrompt)
	// 与多视角校验共用同一份视觉模型配置(设置页 VLM);未配置返回 nil。
	client := TurnaroundVerifier(c.cfg.Get().VLM)
	if plan := planProportionCheck(enabled, client != nil, applicable); !plan.Run {
		if plan.Skipped != "" {
			return nil, proportionNoCapability
		}
		return nil, proportionNotApplicable
	}
	if strings.TrimSpace(path) == "" {
		return nil, proportionNotApplicable
	}
	rep, err := qc.DetectPortraitProportion(ctx, client, path, HumanProportionClausePresent(imagePrompt))
	if err != nil {
		// 读图/调用/解析失败 = "没验过",如实说出来,不当作通过(与画面文字检测同一口径)。
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
			"⚠ 定妆照「%s」比例自检失败(未判定,不当作通过):%v", name, err)
		return nil, proportionChecked
	}
	// 明显偏离才出声;比例正常或只有轻微偏差 → 静默,不刷屏。
	if w := qc.ProportionWarnText(rep); w != "" {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⚠ 定妆照「%s」%s", name, w)
	}
	return rep, proportionChecked
}

// maxFacingRegen 朝向偏离时最多**换种子重出**几次(不含首次)。
//
// 为什么是 2:实测(见 docs/定妆照朝向-实测与处置.md)同一段提示词换种子出图,
// 4 个样本里 3 个正面 —— 单次失手约 1/4,连失三次约 1/64。再往上加只是拿 GPU 时间
// 换一个已经很小的概率,而每次重出都要多跑一次视觉调用;两次之后仍不过就如实告警收手,
// 让用户决定(资产详情里还有「重新生成」那条随机种子的路)。
const maxFacingRegen = 2

// regenerateOnBadFacing 朝向明显偏离时换种子重出,并返回最终可用的图路径。
//
// 背景(2026-09-27 真机实测):定妆照的种子是**按资产名锚定**的(nameSeed("characters", name)),
// 也就是"同名必同图" —— 这是跨集一致性的保险。代价是:一旦某次采样把脸偏成了四分之三侧,
// 那个角色**每次重跑都是同一张侧脸**,不会自愈。而框住朝向的提示词压不住模型
// (前置强调做过 A/B,结果一模一样),所以唯一有效的办法是换种子重出。
//
// 纪律:
//   - 只在**量到** clear 时才重出(rep==nil 表示没量到,绝不拿"没验过"当"不合格"去烧 GPU);
//   - 每次重出都独立播报,用户能看见"重出了几次、为什么";
//   - 用完次数仍不过 → 保留最后一张 + 如实告警,不静默接受。
func (c *Coordinator) regenerateOnBadFacing(ctx context.Context, proj *project.Project, assets map[string]project.Asset,
	kind, id, name, imagePrompt string, ek EntityKind, path string, rep *qc.ProportionReport,
	gen func(kind, id, name, imagePrompt string, ek EntityKind, attempt int) (string, error)) string {
	if !qc.FacingNeedsRegen(rep) {
		return path
	}
	for attempt := 1; attempt <= maxFacingRegen; attempt++ {
		if ctx.Err() != nil {
			return path
		}
		c.emit(proj.ID, PhaseAssets, 0, "info", 0,
			"↻ 定妆照「%s」朝向自检未过(不是正面):换种子重出第 %d/%d 次…", name, attempt, maxFacingRegen)
		next, err := gen(kind, id, name, imagePrompt, ek, attempt)
		if err != nil {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⚠ 定妆照「%s」朝向重出失败(保留上一张):%v", name, err)
			return path
		}
		// 重出写的是**同一个文件**(路径由资产 id 派生),所以要刷新内容指纹 ——
		// 否则资产表里留着上一张的指纹,后续"文件变了没有"的判据会读错。
		if a, ok := assets[id]; ok {
			a.Fingerprint = project.FingerprintFile(next)
			assets[id] = a
		}
		path = next
		rep, _ = c.checkPortraitProportion(ctx, proj, ek, imagePrompt, name, path)
		if !qc.FacingNeedsRegen(rep) {
			if qc.FacingOf(rep) == qc.DeviationNone {
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ 定妆照「%s」朝向已修正(第 %d 次重出为正面)", name, attempt)
			}
			return path
		}
	}
	c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
		"⚠ 定妆照「%s」换种子重出 %d 次仍不是正面:已保留最后一张,可在「资产定妆」里对该卡手动「重新生成」(另一颗种子)",
		name, maxFacingRegen)
	return path
}

func kindLabel(kind string) string {
	switch kind {
	case "characters":
		return "角色"
	case "scenes":
		return "场景"
	case "props":
		return "道具"
	}
	return kind
}

// ---------- 阶段三:render ----------

// 资产/定妆照的尺寸口径缺省值:与项目 render 的出厂画幅一致(9:16)。
const (
	DefaultAssetWidth  = 768
	DefaultAssetHeight = 1344
)

// assetImageSize 资产图(定妆照 / 场景图 / 道具图)的生成尺寸 = **项目的成片画幅**。
//
// 【为什么要改(甲2)】旧实现写死 832×1248(约 2:3),而成片是 768×1344(9:16):
// 参考图与成片**比例不一致**,H3 拿一张 2:3 的参考去渲 9:16 的画面,只能自行裁切或
// 拉伸 —— 构图漂移(人脸被拉宽、头顶被切)从第一帧就埋下了,而界面上看不出任何异常。
// 尺寸与成片对齐后,参考图与画面同比例,模型不需要猜。
//
// 【后果必须说清】尺寸一变,**已有的定妆照/场景图就作废了**(画面内容会变),
// 这是预期行为(宁可贵一次,不要长期漂移):要拿到新比例的参考图,存量项目得重出资产
// (资产详情里的「重新生成」或删掉重跑资产阶段)。资产表里的 Fingerprint 是**内容指纹**,
// 重出后自动更新,不存在"旧图被判成新图"的窗口。
//
// 场景不再单独"按项目方向翻转":项目的 Width/Height 本身就是成片方向,直接使用即可
// (旧实现给横向项目翻出 1248×832,那是与 2:3 主图配套的历史写法)。
func assetImageSize(proj *project.Project) (int, int) {
	if proj == nil {
		return DefaultAssetWidth, DefaultAssetHeight
	}
	rp := proj.Config.Params.Render
	if rp.Width <= 0 || rp.Height <= 0 {
		// 老项目/技能端导入包可能没有 render 参数(读盘兜底见 project.ensureDefaultParams)
		return DefaultAssetWidth, DefaultAssetHeight
	}
	return rp.Width, rp.Height
}

// genAssetImage 资产阶段/渲染前补齐的**单张参考图生成**(打印播报 + 交给唯一出口)。
//
// 「怎么出图」全部收在 genSheetImage 里(变体卡从主卡派生,见 sheetderive.go):
// 这一层只负责"资产阶段的日志口径",不参与任何路线判断 —— 三条出图路径各写一份
// "要不要派生/怎么降级"正是"同一个角色几张脸"复发的老毛病。
//
// attempt > 0 表示这是**朝向自检未过后的换种子重出**(见 regenerateOnBadFacing):
// 种子在名字锚定的基准上加 attempt×101,于是"同名必同图"在重出这一路被有意打破 ——
// 重出要的正是"换一颗种子再抽一次"。同一颗种子重出等于画出同一张图,白烧一次 GPU。
func (c *Coordinator) genAssetImage(ctx context.Context, proj *project.Project, ce engine.Engine, kind, id, name, imagePrompt string, assets map[string]project.Asset, ek EntityKind, attempt int) (string, error) {
	w, h := assetImageSize(proj)
	// 名字锚定种子(fnv 全串哈希;曾用长度哈希易碰撞):同名资产重跑/跨集
	// 补齐得到同一张图。换图走「重新生成」(随机 seed)或上传替换。
	seed := nameSeed(kind, name) + int64(attempt)*101
	c.emit(proj.ID, PhaseAssets, 0, "info", 0, "%s 生成:%s「%s」(%d×%d)…", c.imageSourceLabel(), kindLabel(kind), name, w, h)
	path, err := c.genSheetImage(ctx, ce, sheetJob{
		proj: proj, assets: assets, kind: kind, id: id, name: name,
		prompt: imagePrompt, ek: ek, w: w, h: h, seed: seed,
	})
	if err != nil {
		return path, err
	}
	// 角色卡的**三视图细节板**（官方 production reference sheet，见 board.go）：
	// 从刚出的定妆照派生，供人审一致性与建模向参考。它不是 H3 的参考图
	// （refs.go 只认 <base>.png / <base>_turnaround.png），失败只告警。
	if kind == "characters" {
		c.genCharacterBoard(ctx, proj, ce, name, imagePrompt, path, ek)
	}
	return path, nil
}

// orderCharsMasterFirst 把一批角色卡按"同族主卡在前"重排,供资产阶段按序生成。
//
// 为什么必须排:变体卡(`char_01v2`)的定妆照是**从同族主卡派生**出来的
// (见 sheetderive.go)。主卡图还没落盘时,变体只能退回文生图 —— 而"各自文生图"
// 正是同一角色几张脸会漂的那个老毛病。
//
// 组间保持**首次出现**的顺序(不改写"谁先谁后"的既有观感),组内按档位号升序;
// 认不出家族的卡(家族键为空)各自成组、保持原序。
func orderCharsMasterFirst(chars []project.Character) []project.Character {
	order := make([]string, 0, len(chars))
	groups := map[string][]project.Character{}
	for i, ch := range chars {
		key := project.CharFamilyKey(ch.ID, ch.Name)
		if key == "" {
			key = "\x00anon:" + fmt.Sprint(i) // 认不出家族的卡不与他人合并
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], ch)
	}
	out := make([]project.Character, 0, len(chars))
	for _, key := range order {
		g := groups[key]
		sort.SliceStable(g, func(i, j int) bool {
			return project.CharVariantNumberOf(g[i].ID) < project.CharVariantNumberOf(g[j].ID)
		})
		out = append(out, g...)
	}
	return out
}

// entityKindSuffix 只在**角色**这一类后面挂实体类型(人/物/灵宠/妖兽/无实体/Q 版)。
//
// 场景没有实体类型,而原先是无条件挂 EntityKindLabel(ek) —— 场景的 ek 是零值,
// 空值一路走到 default 得到「人」,于是真机日志里出现
// 「✔ 已生成:场景「临水城」(人)」(2026-09-26 实测)。日志写错比不写更坏:
// 用户看到会以为平台把场景当人画了,而这类"显示与事实不符"他已经报过两次。
func entityKindSuffix(kind string, ek EntityKind) string {
	if kind != "characters" {
		return ""
	}
	return "(" + EntityKindLabel(ek) + ")"
}

// genCharacterViews 资产阶段的多视角产出:定妆照 → H3 Ref2V 转身视频 → 抽帧 →
// 横向拼转台图。返回转台图路径(失败返回空串)。
//
// 具体实现与产物命名契约见 turnaround.go 的 GenerateTurnaround(资产库详情弹窗的
// 「补全多视角」与渲染前自动补齐共用同一份实现)。本函数只负责"项目侧上下文":
// 产物目录取项目资产目录,失败只告警不阻断资产阶段(增强项)。
func (c *Coordinator) genCharacterViews(ctx context.Context, proj *project.Project, ce engine.Engine, id, name, imagePrompt, masterPath string, kind EntityKind) string {
	turn, err := GenerateTurnaround(ctx, ce, c.ff, TurnaroundOptions{
		MasterPath: masterPath,
		Name:       name,
		Prompt:     imagePrompt,
		Kind:       kind,
		Source:     proj.Config.Params.Assets.ViewsSourceOf(),
		Realistic:  !proj.Config.Params.Script.FacesOn(),
		Steps:      proj.Config.Params.Render.Steps,
		Accel:      proj.Config.Params.Render.Accel,
		Verify:     TurnaroundVerifier(c.cfg.Get().VLM),
		Emit:       func(f string, a ...any) { c.emit(proj.ID, PhaseAssets, 0, "info", 0, f, a...) },
	})
	if err != nil {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "多视角生成失败(不影响定妆照):%v", err)
		return ""
	}
	return turn
}

// libReuse 一次成功的库复用:项目侧落点 + 配对元数据。
type libReuse struct {
	Dest   string // 项目侧主图绝对路径
	Rel    string // 项目侧主图相对路径(斜杠分隔,写进资产表用)
	Name   string // 库条目的**权威名字**(别名命中时与本集叫法不同)
	Prompt string // 库条目自己的 image_prompt(空 = 条目没记)
	Copied int    // 一并带进项目的多视角/转台图张数
}

// PromptFor 复用时的 imagePrompt 取值:条目自带提示词时以**条目**为准。
//
// 为什么不能写本集方案那段:复用的是库里的**像素**,而本集那段是另一套措辞
// (含本集 castContrastBlock / plan.Style)。写进去会被下游当成"这张图的提示词"
// —— 补多视角时按它拼编辑指令(角度帧被拉向另一种长相)、写回库时又覆盖共享条目的
// 元数据,错配从一集扩散到全剧集。
func (r libReuse) PromptFor(fallback string) string {
	if strings.TrimSpace(r.Prompt) != "" {
		return r.Prompt
	}
	return fallback
}

// reuseFromLibrary 把库条目(主图 + 多视角/转台图)复制进项目资产目录。
// ok=false = 库里没有可用条目(或读不到),调用方照旧自己生成。
//
// **两条补齐路径共用**(资产阶段的 check 与渲染前的 backfillAssets):后者此前完全
// 不查库,于是资产阶段没跑过就直接渲染时,同一角色会重新生成另一张脸并反写覆盖共享
// 条目(库里的旧图被新图顶替、角度图被清掉),而先跑的那一集仍在用旧脸 —— 跨集成了
// "两个沈砚",界面上两边还显示同一个名字。语义收在一处,才不会有一条路径漏掉。
func (c *Coordinator) reuseFromLibrary(projID, kind, id, name, description string) (libReuse, bool) {
	if c.lib == nil {
		return libReuse{}, false
	}
	e, img, ok := c.lib.Find(kind, name, description)
	if !ok {
		return libReuse{}, false
	}
	raw, err := os.ReadFile(img)
	if err != nil || len(raw) == 0 {
		return libReuse{}, false
	}
	destDir := filepath.Join(c.store.Dir(projID), "assets", kind)
	_ = os.MkdirAll(destDir, 0o755)
	base := director.SanitizeForCache(kind + "_" + id)
	dest := filepath.Join(destDir, base+filepath.Ext(img))
	if err := os.WriteFile(dest, raw, 0o644); err != nil {
		return libReuse{}, false
	}
	// **必须用条目自己的名字查关联图**(审计):RelatedFiles/EntryDir 都是按传入的
	// 名字推目录的,而别名命中时条目目录名是 e.Name、调用方手上是本集叫法 → 目录不存在
	// → 返回 nil → 库里的 _view_*.png / _turnaround.png 一张都不复制,界面却仍报
	// 「♻ 资产库复用」;同一个名字错位也让 BumpUsed 读不到 asset.json(复用计数永不涨)。
	libName := name
	if e != nil && strings.TrimSpace(e.Name) != "" {
		libName = e.Name
	}
	copied := copyAssetRelated(c.lib.RelatedFiles(kind, libName), destDir, base)
	c.lib.BumpUsed(kind, libName)
	r := libReuse{Dest: dest, Name: libName, Copied: copied}
	if e != nil {
		r.Prompt = strings.TrimSpace(e.ImagePrompt)
	}
	if rel, err := filepath.Rel(c.store.Dir(projID), dest); err == nil {
		r.Rel = filepath.ToSlash(rel)
	}
	return r, true
}

// imagePixelSize 读图片宽高(只读文件头,不解码像素)。
func imagePixelSize(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// sceneOrientationMismatch 这张图的方向是否与项目画幅**明确相反**(横↔竖)。
//
// 只判"明确相反":正方形图、任一边读不到(0)时都不判 —— 宁可少说一句,
// 不要凭猜测报错(方形场景图在竖幅项目里只是轻微裁切,不是本条要报的那个错)。
func sceneOrientationMismatch(imgW, imgH, projW, projH int) bool {
	if imgW <= 0 || imgH <= 0 || projW <= 0 || projH <= 0 {
		return false
	}
	return (imgW > imgH && projH > projW) || (imgH > imgW && projW > projH)
}

// viewsEntityKind 渲染前补多视角时的**实体类型取值链**:库条目里"看图判定"的结果优先,
// 方案文本推断兜底 —— 与 server.resolveEntityKind 保持同一套优先级。
//
// 为什么要看库:库里为了这个坑专门存了视觉模型**看图**判定的 EntityKind(用户上传一张
// 铜雀、名字叫「阿凿」,按文本推断会当成"人"去做原地转身,而器物该走转台自转),
// 库侧「补全多视角」真的用了它,而渲染前补齐此前只看方案文本 → 同一张图两条路径两种
// 做法:要么被身份闸门拒绝白烧 GPU,要么产出一张与主图形态不符的角度图当第二张参考。
// 库里的值不合规(空/未知键)时原样回落文本推断,不硬认。
func viewsEntityKind(textKind EntityKind, libEntityKind string) EntityKind {
	if k := NormalizeEntityKind(libEntityKind); k != "" {
		return k
	}
	return textKind
}

// copyAssetRelated 把库条目的多视角图/转台图按**项目侧命名**复制到定妆照同目录。
//
// 为什么必须改名:渲染时 turnaroundPath() 按主图主名拼 `<base>_turnaround.png`
// 找第二张参考,而库里的关联图文件名来自最初入库那个项目的命名
// (characters_char_张三_view_side.png)。照抄文件名的话,项目里就有转台图却永远
// 匹配不上 —— 角色在 H3 渲染里依旧只有一张正面照。
//
// 返回复制成功的张数;单项失败只跳过,不影响主图(角色仍可用,只是少一个视角)。
func copyAssetRelated(related []string, destDir, destBase string) int {
	n := 0
	for _, r := range related {
		raw, err := os.ReadFile(r)
		if err != nil || len(raw) == 0 {
			continue
		}
		dest := filepath.Join(destDir, RelatedSuffixName(destBase, r))
		if err := fsutil.WriteFileAtomic(dest, raw); err == nil {
			n++
		}
	}
	return n
}

// backfillAssetViews 渲染前补齐角色的多视角转台图(缺失才补)。
//
// 为什么放在渲染前:多视角图是"该角色在 H3 里的第二张参考",价值只在渲染阶段
// 兑现;而它恰好只会在三种情况下缺失 —— ①角色来自资产库复用(库里可能只有正面
// 照)②手动上传的定妆照 ③多视角功能上线前跑出来的存量项目。这三类的共同点是
// 资产阶段不会再跑一次,所以必须在渲染前兜住(2026-09-12 用户要求)。
//
// 成本:每个角色一次单镜渲染(数分钟 H3 Ref2V),因此只对**本次真要渲的 ref2va
// 镜头里出现、且确实没有转台图**的角色做;补完立刻写回资产库,后续项目复用即连
// 角度图一起带走 —— 同一个角色全局只付一次这个成本。
func (c *Coordinator) backfillAssetViews(ctx context.Context, proj *project.Project, plan *project.Plan, assets map[string]project.Asset, need *assetNeed) {
	ap := proj.Config.Params.Assets
	if !ap.ViewsOn() || ap.Source == "skip" {
		return
	}
	dir := c.store.Dir(proj.ID)
	type want struct {
		id, name, prompt, desc string
		kind                   EntityKind
	}
	var list []want
	seen := map[string]bool{}
	for _, s := range plan.Shots {
		if s.Mode != "ref2va" {
			continue
		}
		for _, cid := range s.Characters {
			// 范围:单镜重渲时只补这一镜的角色多视角(见 asset_scope.go,审计 P0-9)
			if !need.wantsChar(cid) {
				continue
			}
			if seen[cid] {
				continue
			}
			a, ok := assets["char:"+cid]
			if !ok || a.File == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, a.File)); err != nil {
				continue
			}
			if turnaroundPath(dir, a.File) != "" {
				continue // 已有转台图(资产阶段生成的或复用带进来的)
			}
			seen[cid] = true
			ek := KindHuman
			desc := ""
			for _, ch := range plan.Characters {
				if ch.ID == cid {
					ek = ResolveEntityKind(ch)
					desc = ch.Description
					break
				}
			}
			// 库里存着**看图判定**的 EntityKind(上传图由视觉模型判的),它比按方案文本
			// 推断更可信:用户上传一张铜雀、名字叫「阿凿」,按文本推断会当成"人"去做
			// 原地转身,而器物该走转台自转(库侧「补全多视角」早就按这个优先级取值,
			// 渲染前补齐这条路径此前只看方案文本)。
			libKind := ""
			if c.lib != nil && strings.TrimSpace(a.Name) != "" {
				if e, _, ok := c.lib.Find("characters", a.Name, desc); ok && e != nil {
					libKind = e.EntityKind
				}
			}
			ek = viewsEntityKind(ek, libKind)
			list = append(list, want{id: cid, name: a.Name, prompt: a.ImagePrompt, desc: desc, kind: ek})
		}
	}
	if len(list) == 0 {
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })
	eng, err := c.reg.Get("comfy")
	if err != nil {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ %d 个角色缺多视角参考图,多视角生成需要本地 ComfyUI(当前不可用)", len(list))
		return
	}
	if err := eng.Available(ctx); err != nil {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ %d 个角色缺多视角参考图,本地 ComfyUI 未就绪(%v),本次按单张正面照渲染", len(list), err)
		return
	}
	// 已在资产阶段补过的角色不再提示;首次补齐要明确告知"这一步要等"
	c.emit(proj.ID, PhaseRender, 0, "info", 0, "检测到 %d 个角色只有单张正面照,渲染前自动补多视角(图像编辑每张数秒;若退回 H3 转身则每人数分钟)…", len(list))
	for _, w := range list {
		if ctx.Err() != nil {
			return
		}
		a := assets["char:"+w.id]
		master := filepath.Join(dir, a.File)
		prompt := w.prompt
		desc := strings.TrimSpace(w.desc)
		// 库里这条角色的**权威名字**:w.name 是本集叫法,别名命中时条目目录名是
		// e.Name —— 按本集叫法 Put 会在库里**新建第二个条目**(safeName 不同),
		// 同一角色于是有两条记录(一条有角度图、一条只有主图),后续按名字取参考图
		// 的结果取决于目录名排序,表现为"随机的一致性差异";新条目的描述还是空串,
		// 从此再也参与不了别名/描述匹配。用条目自己的名字写回,同一角色保持一条。
		libName := w.name
		libDesc := desc
		if c.lib != nil {
			// Find 而不是 Match:别名条目按本集叫法精确匹配必然 miss(见上)。
			if e, _, ok := c.lib.Find("characters", w.name, w.desc); ok && e != nil {
				libName = e.Name
				if strings.TrimSpace(prompt) == "" {
					prompt = e.ImagePrompt
				}
				// 描述留空会把库里已有的描述覆盖成空串(Put 无条件写入),那就
				// 用条目自己的描述 —— 库里没记过时才用方案里的。
				if strings.TrimSpace(libDesc) == "" {
					libDesc = e.Description
				}
			}
		}
		if strings.TrimSpace(prompt) == "" {
			prompt = w.name
		}
		turn, err := GenerateTurnaround(ctx, eng, c.ff, TurnaroundOptions{
			MasterPath: master,
			Name:       w.name,
			Prompt:     prompt,
			Kind:       w.kind,
			Source:     proj.Config.Params.Assets.ViewsSourceOf(),
			Realistic:  !proj.Config.Params.Script.FacesOn(),
			Steps:      proj.Config.Params.Render.Steps,
			Accel:      proj.Config.Params.Render.Accel,
			Verify:     TurnaroundVerifier(c.cfg.Get().VLM),
			Emit:       func(f string, ar ...any) { c.emit(proj.ID, PhaseRender, 0, "info", 0, f, ar...) },
		})
		if err != nil {
			c.emit(proj.ID, PhaseRender, 0, "warn", 0, "多视角补齐失败:角色「%s」:%v(该角色仍按单张正面照渲染)", w.name, err)
			continue
		}
		rel, _ := filepath.Rel(dir, turn)
		c.emit(proj.ID, PhaseRender, 0, "success", 0, "✔ 多视角就绪:角色「%s」(%s),本次渲染起作第二张参考", w.name, filepath.ToSlash(rel))
		if c.lib != nil {
			if _, err := c.lib.Put("characters", libName, libDesc, prompt, master, proj.ID, a.Source); err != nil {
				c.emit(proj.ID, PhaseRender, 0, "warn", 0, "多视角写回资产库失败:角色「%s」:%v", w.name, err)
			}
		}
	}
}

// nameSeed 资产名→稳定种子:同名必同图,是跨集一致性的第二道保险
// (第一道是资产库按名复用)。
//
// 种子派生的**唯一收口点**(审计 P1-2):全部"要可复现"的生成都走它 ——
// 定妆照/场景(nameSeed)、Q 版(nameSeed("chibi", id))、镜头(见 ShotSeed)、
// 多视角(turnaround / viewedit)。历史上有过四种派生方式,其中**长度哈希**
// (`len(id)*1000+len(name)*7`)会让同名长度的角色撞种子(实测踩过),
// 已全部收敛到这里 + ShotSeed + RandomSeed 三个出口。
func nameSeed(kind, name string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(kind + "\x00" + name))
	return int64(h.Sum32())
}

// ShotSeed 镜头种子派生(fnv 锚定 + 镜号步进)。
//
// 参数:override = 本次运行的种子覆盖(0=不覆盖);base = 项目固定种子(0=按角色组合派生)。
// 三条出像素的路径(主渲染、单镜 2K、局部重绘/音频驱动经 ShotRenderPrompt 的渲染调用)
// 都必须用它,否则"同一镜重渲"会得到不同的脸。
func ShotSeed(base, override int64, shotID int, characters []string) int64 {
	if override > 0 {
		return override + int64(shotID)*101 // 单镜微调:同值同镜稳定复现
	}
	if base != 0 {
		return base + int64(shotID)*101
	}
	// 角色锚定种子:按镜头角色组合派生 —— 同角色组合跨集得到同基种子,
	// 配合同一张库定妆照,一致性最强;shot.ID 项保住集内镜头间的多样性。
	return nameSeed("shot", strings.Join(characters, "+")) + int64(shotID)*101
}

// RandomSeed 每次都不同的种子(重新生成 / 抽卡这类"就是要换一张"的场景)。
//
// 集中一处是为了让"哪里是随机"一眼可查:随机种子参与不了断点续跑判据,
// 所以只能出现在这两类显式动作里,绝不能出现在渲染主链路上
// (渲染链路用 ShotSeed,否则每次续跑都会重渲)。
func RandomSeed() int64 { return time.Now().UnixNano() & 0x7fffffff }

// styleExpand 风格英文具象化:用户的 art_style 多为中文速记(如「次世代
// 3D·BJD 虚拟人+半写实国漫AI漫剧质感」),STYLE LOCK 让 image_prompt 逐
// 字照搬中文——Krea-2 缺具体英文视觉词汇时照片先验胜出,人类角色生成
// 写实真人(2026-09-11 实证:同一本书木偶系角色因自带"wooden android"
// 描述正常,人类系全部照片质感)。按关键词展开确定性英文渲染词汇追加;
// 中文原风格保留(跨集一致性锚),真人剧关键词放行写实(合法风格)。
func (c *Coordinator) backfillAssets(ctx context.Context, proj *project.Project, plan *project.Plan, assets map[string]project.Asset, need *assetNeed) bool {
	if proj.Config.Params.Assets.Source == "skip" {
		return true
	}
	// 与 runAssets 同一个真源替换（两条路各自读 plan，必须都覆盖，否则"初出用卡片、补缺用 LLM"）
	c.applyCardPrompts(proj, plan)
	if notice := styleFallbackNotice(proj, plan); notice != "" {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ %s", notice)
	}
	type miss struct {
		kind, id, name, desc, prompt string
		ek                           EntityKind
	}
	var list []miss
	check := func(kind, id, name, desc, prompt string, ek EntityKind) {
		if strings.TrimSpace(prompt) == "" {
			return
		}
		if a, ok := assets[id]; ok && a.File != "" {
			if _, err := os.Stat(filepath.Join(c.store.Dir(proj.ID), a.File)); err == nil {
				return
			}
		}
		list = append(list, miss{kind, id, name, desc, prompt, ek})
	}
	for _, ch := range orderCharsMasterFirst(plan.Characters) {
		// 范围:单镜重渲时只补这一镜会用到的角色(否则会顺手生成全片资产,
		// 见 asset_scope.go 的说明 —— 审计 P0-9)。顺序:同族主卡在前(变体卡要从它派生)。
		if !need.wantsChar(ch.ID) {
			continue
		}
		check("characters", "char:"+ch.ID, ch.Name, ch.Description, characterImagePrompt(proj, plan, ch), ResolveEntityKind(ch))
	}
	for _, sc := range plan.Scenes {
		if !need.wantsScene(sc.ID) {
			continue
		}
		// 与资产阶段同源:渲染前补齐也必须走场景装配唯一实现点(见 StageAssets 里的说明)。
		check("scenes", "scene:"+sc.ID, sc.Name, sc.Description, SceneImagePrompt(proj, plan, sc), "")
	}
	// 道具图（与 runAssets 同一个装配点 PropImagePrompt）：两条路径都要遍历，
	// 否则会出现"初出有道具图、补缺却没有"。范围同角色/场景 —— 按本次取镜范围要（need）。
	for _, pr := range plan.Props {
		if !need.wantsProp(pr.ID) {
			continue
		}
		check("props", "prop:"+pr.ID, pr.Name, pr.Description, PropImagePrompt(proj, plan, pr), KindObject)
	}
	if len(list) == 0 {
		return true
	}
	// ① 先查库(这一步**不需要 GPU**):库里已有这个角色/场景时直接复用同一张图。
	//
	// 为什么必须排在"引擎是否可用"之前:旧实现这条路径完全不查库,于是资产阶段没跑过
	// 就直接渲染时,同一角色会重新生成另一张脸并反写覆盖共享条目(旧图被顶替、角度图被
	// 清掉),而先跑的那一集仍用旧脸 —— 跨集成了"两个沈砚",界面上两边显示同一个名字。
	// 顺带:本地引擎没起来时,库里明明有整套参考图也会报「缺 N 张参考图,将降级纯文生」,
	// 白白把镜头降级(复用只是复制像素,不需要引擎)。
	var todo []miss
	for _, m := range list {
		r, ok := c.reuseFromLibrary(proj.ID, m.kind, m.id, m.name, m.desc)
		if !ok {
			todo = append(todo, m)
			continue
		}
		assets[m.id] = project.Asset{
			ID: m.id, Kind: m.kind, Name: m.name,
			File: r.Rel, ImagePrompt: r.PromptFor(m.prompt),
			Source: "library", Fingerprint: project.FingerprintFile(r.Dest),
		}
		via := ""
		if r.Name != m.name {
			via = fmt.Sprintf("(同一角色,已按「%s」复用)", r.Name)
		}
		if r.Copied > 0 {
			c.emit(proj.ID, PhaseRender, 0, "success", 0, "♻ 渲染前补齐:%s「%s」%s(资产库复用,免生成;含 %d 张多视角)", kindLabel(m.kind), m.name, via, r.Copied)
		} else {
			c.emit(proj.ID, PhaseRender, 0, "success", 0, "♻ 渲染前补齐:%s「%s」%s(资产库复用,免生成)", kindLabel(m.kind), m.name, via)
		}
	}
	if len(todo) == 0 {
		_ = c.store.SaveAssets(proj.ID, assets)
		return true
	}
	eng, err := c.reg.Get("comfy")
	if err != nil {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ 缺 %d 张参考图且本地引擎不可用,对应镜头将降级纯文生(建议先启动 ComfyUI)", len(todo))
		_ = c.store.SaveAssets(proj.ID, assets)
		return false
	}

	if err := eng.Available(ctx); err != nil {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ 缺 %d 张参考图且本地 ComfyUI 未就绪(%v),对应镜头将降级纯文生", len(todo), err)
		_ = c.store.SaveAssets(proj.ID, assets)
		return false
	}
	c.emit(proj.ID, PhaseRender, 0, "info", 0, "检测到 %d 张参考图缺失,渲染前自动补齐(%s)…", len(todo), c.imageSourceLabel())
	for _, m := range todo {
		path, err := c.genAssetImage(ctx, proj, eng, m.kind, m.id, m.name, m.prompt, assets, m.ek, 0)
		if err != nil {
			c.emit(proj.ID, PhaseRender, 0, "warn", 0, "补齐失败:%s「%s」:%v(该镜降级 t2va)", kindLabel(m.kind), m.name, err)
			continue
		}
		rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
		assets[m.id] = project.Asset{
			ID: m.id, Kind: m.kind, Name: m.name,
			File: filepath.ToSlash(rel), ImagePrompt: m.prompt,
			Source: c.imageSourceKey(), Fingerprint: project.FingerprintFile(path),
		}
		c.emit(proj.ID, PhaseRender, 0, "success", 0, "✔ 已补齐:%s「%s」(%s)", kindLabel(m.kind), m.name, EntityKindLabel(m.ek))
		if c.lib != nil {
			// 写库失败不能静默:以前是 `_, _ =`,于是"✔ 已补齐"照报,而库里其实没有
			// 这个条目 —— 后续项目复不到它,用户只会觉得"怎么又生成了一张"。
			if _, err := c.lib.Put(m.kind, m.name, m.desc, m.prompt, path, proj.ID, c.imageSourceKey()); err != nil {
				c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ 写回资产库失败(不影响本项目):%s「%s」:%v", kindLabel(m.kind), m.name, err)
			}
		}
	}
	_ = c.store.SaveAssets(proj.ID, assets)
	return true
}

// StylePresets 渲染风格预设(可多选组合):key → (显示名, 英文风格提示)。
// 提示词进 LLM 全局风格段,决定全片视觉基调。

// planHasChibiCard 方案里是否已经有一张 Q 版角色卡(技能端写的"内心Q版化身")。
//
// 判据用 ResolveEntityKind —— 它同时认显式 kind=chibi 与名字/描述里的 Q 版特征
// (「迷你青禾(内心Q版)」这类命名正是靠它认出来的)。
func planHasChibiCard(plan *project.Plan) bool {
	if plan == nil {
		return false
	}
	for _, ch := range plan.Characters {
		if ResolveEntityKind(ch) == KindChibi {
			return true
		}
	}
	return false
}
