package pipeline

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/fsutil"
	"github.com/YiIimini/ManJuX/internal/project"
)

func (c *Coordinator) runAssets(ctx context.Context, proj *project.Project, onlyAssets []string) error {
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		return fmt.Errorf("尚未生成分镜方案:请先运行「剧本分镜」阶段")
	}
	assets, err := c.store.LoadAssets(proj.ID)
	if err != nil {
		return err
	}
	p := proj.Config.Params.Assets
	// 风格要真人、但角色动漫化开关开着:必须当场说清"为什么没按你选的风格走"
	if notice := styleFallbackNotice(proj, plan); notice != "" {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "⚠ %s", notice)
	}

	// Krea-2 自动生成缺失参考图(角色竖幅半身 832×1248;场景按项目画幅方向)
	var comfyGen engine.Engine
	if p.Source == "krea2" {
		eng, err := c.reg.Get("comfy")
		if err != nil {
			return err
		}
		if err := eng.Available(ctx); err != nil {
			return fmt.Errorf("Krea-2 定妆照需要本地 ComfyUI: %w", err)
		}
		comfyGen = eng
	}

	genAsset := func(kind, id, name, imagePrompt string) (string, error) {
		return c.genAssetImage(ctx, proj, comfyGen, kind, id, name, imagePrompt)
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
				// 存量回填:项目已有资产若库中无同名条目,自动沉淀入库
				if c.lib != nil {
					if _, _, ok := c.lib.Match(kind, name); !ok {
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
		if c.lib != nil {
			if e, img, ok := c.lib.Find(kind, name, description); ok {
				destDir := filepath.Join(c.store.Dir(proj.ID), "assets", kind)
				_ = os.MkdirAll(destDir, 0o755)
				dest := filepath.Join(destDir, director.SanitizeForCache(kind+"_"+id)+filepath.Ext(img))
				if raw, err := os.ReadFile(img); err == nil {
					_ = os.WriteFile(dest, raw, 0o644)
					// 多视角图/转台图一并带进项目(按项目侧命名):只带主图的话,
					// 复用回来的角色在 H3 渲染里就只有一张正面照,多视角参考形同虚设
					// —— 而"库里有、项目没有"正是 2026-09-12 用户实证的根因。
					copied := copyAssetRelated(c.lib.RelatedFiles(kind, name), destDir,
						director.SanitizeForCache(kind+"_"+id))
					rel, _ := filepath.Rel(c.store.Dir(proj.ID), dest)
					assets[id] = project.Asset{
						ID: id, Kind: kind, Name: name,
						File: filepath.ToSlash(rel), ImagePrompt: imagePrompt,
						Source: "library", Fingerprint: project.FingerprintFile(dest),
					}
					c.lib.BumpUsed(kind, name)
					reused++
					via := ""
					if e != nil && strings.TrimSpace(e.Name) != name {
						via = fmt.Sprintf("(同一角色,已按「%s」的定妆照复用)", e.Name)
					}
					if copied > 0 {
						c.emit(proj.ID, PhaseAssets, 0, "success", 0, "♻ 资产库复用:%s「%s」%s(跨项目共享,免生成;含 %d 张多视角)", kindLabel(kind), name, via, copied)
					} else {
						c.emit(proj.ID, PhaseAssets, 0, "success", 0, "♻ 资产库复用:%s「%s」%s(跨项目共享,免生成)", kindLabel(kind), name, via)
					}
					return
				}
			}
		}
		// ② Krea-2 生成缺失参考图;成功后自动入库供后续项目复用
		if comfyGen != nil && strings.TrimSpace(imagePrompt) != "" {
			if path, err := genAsset(kind, id, name, imagePrompt); err == nil {
				rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
				assets[id] = project.Asset{
					ID: id, Kind: kind, Name: name,
					File: filepath.ToSlash(rel), ImagePrompt: imagePrompt,
					Source: "krea2", Fingerprint: project.FingerprintFile(path),
				}
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ 已生成:%s「%s」(%s)", kindLabel(kind), name, EntityKindLabel(ek))
				// 角色的多视角转台图排进第二阶段(见 pendingViews 注释)
				if kind == "characters" && p.ViewsOn() {
					pendingViews = append(pendingViews, pendingView{
						id: "char:" + id, name: name, prompt: imagePrompt,
						master: path, description: description, kind: ek,
					})
				}
				if c.lib != nil {
					if _, err := c.lib.Put(kind, name, description, imagePrompt, path, proj.ID, "krea2"); err == nil {
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
	for _, ch := range plan.Characters {
		check("characters", "char:"+ch.ID, ch.Name, ch.Description, characterImagePrompt(proj, plan, ch), ResolveEntityKind(ch))
	}
	for _, sc := range plan.Scenes {
		check("scenes", "scene:"+sc.ID, sc.Name, sc.Description, sc.ImagePrompt, "")
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
			c.emit(proj.ID, PhaseAssets, 0, "info", 0, "Krea-2 生成:「%s」Q 版形象…", ch.Name)
			if path, err := comfyGen.GenerateImage(ctx, chibiPrompt, imageNegative, seed, 1024, 1024, out); err == nil {
				rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
				assets[chibiID] = project.Asset{
					ID: chibiID, Kind: "characters", Name: ch.Name + " · Q版",
					File: filepath.ToSlash(rel), ImagePrompt: chibiPrompt,
					Source: "krea2-chibi", Fingerprint: project.FingerprintFile(path),
				}
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ Q 版就绪:「%s」(内心活动素材)", ch.Name)
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
				if _, err := c.lib.Put("characters", v.name, v.description, v.prompt, v.master, proj.ID, "krea2"); err != nil {
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
	c.emit(proj.ID, PhaseAssets, 0, "success", pct, "✔ 资产就绪:%d/%d(库复用 %d)", total-missing, total, reused)
	if missing > 0 {
		c.emit(proj.ID, PhaseAssets, 0, "warn", pct, "提示:缺 %d 张参考图,对应镜头将以 t2va 纯文生渲染", missing)
	}
	return nil
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

func (c *Coordinator) genAssetImage(ctx context.Context, proj *project.Project, ce engine.Engine, kind, id, name, imagePrompt string) (string, error) {
	w, h := 832, 1248 // 角色半身竖幅
	if kind == "scenes" {
		if proj.Config.Params.Render.Width >= proj.Config.Params.Render.Height {
			w, h = 1248, 832
		}
	}
	safe := director.SanitizeForCache(kind + "_" + id)
	out := filepath.Join(c.store.Dir(proj.ID), "assets", kind, safe+".png")
	// 名字锚定种子(fnv 全串哈希;曾用长度哈希易碰撞):同名资产重跑/跨集
	// 补齐得到同一张图。换图走「重新生成」(随机 seed)或上传替换。
	seed := nameSeed(kind, name)
	c.emit(proj.ID, PhaseAssets, 0, "info", 0, "Krea-2 生成:%s「%s」(%d×%d)…", kindLabel(kind), name, w, h)
	return ce.GenerateImage(ctx, imagePrompt, imageNegative, seed, w, h, out)
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
		id, name, prompt string
		kind             EntityKind
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
			for _, ch := range plan.Characters {
				if ch.ID == cid {
					ek = ResolveEntityKind(ch)
					break
				}
			}
			list = append(list, want{id: cid, name: a.Name, prompt: a.ImagePrompt, kind: ek})
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
		desc := ""
		if c.lib != nil {
			if e, _, ok := c.lib.Match("characters", w.name); ok {
				if strings.TrimSpace(prompt) == "" {
					prompt = e.ImagePrompt
				}
				desc = e.Description
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
			if _, err := c.lib.Put("characters", w.name, desc, prompt, master, proj.ID, a.Source); err != nil {
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
	for _, ch := range plan.Characters {
		// 范围:单镜重渲时只补这一镜会用到的角色(否则会顺手生成全片资产,
		// 见 asset_scope.go 的说明 —— 审计 P0-9)。
		if !need.wantsChar(ch.ID) {
			continue
		}
		check("characters", "char:"+ch.ID, ch.Name, ch.Description, characterImagePrompt(proj, plan, ch), ResolveEntityKind(ch))
	}
	for _, sc := range plan.Scenes {
		if !need.wantsScene(sc.ID) {
			continue
		}
		check("scenes", "scene:"+sc.ID, sc.Name, sc.Description, sc.ImagePrompt, "")
	}
	if len(list) == 0 {
		return true
	}
	eng, err := c.reg.Get("comfy")
	if err != nil {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ 缺 %d 张参考图且本地引擎不可用,对应镜头将降级纯文生(建议先启动 ComfyUI)", len(list))
		return false
	}

	if err := eng.Available(ctx); err != nil {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ 缺 %d 张参考图且本地 ComfyUI 未就绪(%v),对应镜头将降级纯文生", len(list), err)
		return false
	}
	c.emit(proj.ID, PhaseRender, 0, "info", 0, "检测到 %d 张参考图缺失,渲染前自动补齐(Krea-2)…", len(list))
	for _, m := range list {
		path, err := c.genAssetImage(ctx, proj, eng, m.kind, m.id, m.name, m.prompt)
		if err != nil {
			c.emit(proj.ID, PhaseRender, 0, "warn", 0, "补齐失败:%s「%s」:%v(该镜降级 t2va)", kindLabel(m.kind), m.name, err)
			continue
		}
		rel, _ := filepath.Rel(c.store.Dir(proj.ID), path)
		assets[m.id] = project.Asset{
			ID: m.id, Kind: m.kind, Name: m.name,
			File: filepath.ToSlash(rel), ImagePrompt: m.prompt,
			Source: "krea2", Fingerprint: project.FingerprintFile(path),
		}
		c.emit(proj.ID, PhaseRender, 0, "success", 0, "✔ 已补齐:%s「%s」(%s)", kindLabel(m.kind), m.name, EntityKindLabel(m.ek))
		if c.lib != nil {
			_, _ = c.lib.Put(m.kind, m.name, m.desc, m.prompt, path, proj.ID, "krea2")
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
