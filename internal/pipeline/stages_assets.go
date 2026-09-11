package pipeline

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/engine"
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
	onlySet := map[string]bool{}
	for _, id := range onlyAssets {
		onlySet[id] = true
	}
	check := func(kind, id, name, description, imagePrompt string) {
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
		// ① 资产库复用(名称精确匹配):跨项目同一角色/场景直接复用,不重复生成
		if c.lib != nil {
			if _, img, ok := c.lib.Match(kind, name); ok {
				destDir := filepath.Join(c.store.Dir(proj.ID), "assets", kind)
				_ = os.MkdirAll(destDir, 0o755)
				dest := filepath.Join(destDir, director.SanitizeForCache(kind+"_"+id)+filepath.Ext(img))
				if raw, err := os.ReadFile(img); err == nil {
					_ = os.WriteFile(dest, raw, 0o644)
					rel, _ := filepath.Rel(c.store.Dir(proj.ID), dest)
					assets[id] = project.Asset{
						ID: id, Kind: kind, Name: name,
						File: filepath.ToSlash(rel), ImagePrompt: imagePrompt,
						Source: "library", Fingerprint: project.FingerprintFile(dest),
					}
					c.lib.BumpUsed(kind, name)
					reused++
					c.emit(proj.ID, PhaseAssets, 0, "success", 0, "♻ 资产库复用:%s「%s」(跨项目共享,免生成)", kindLabel(kind), name)
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
				c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ 已生成:%s「%s」", kindLabel(kind), name)
				// 角色定妆照生成后顺带产出多视角转台图(增强项,失败不阻断)
				if kind == "characters" {
					c.genCharacterViews(ctx, proj, comfyGen, id, name, imagePrompt, path)
				}
				if c.lib != nil {
					if _, err := c.lib.Put(kind, name, description, imagePrompt, path, proj.ID, "krea2"); err == nil {
						c.emit(proj.ID, PhaseAssets, 0, "info", 0, "已入库:%s「%s」(后续项目自动复用)", kindLabel(kind), name)
					}
				}
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
		check("characters", "char:"+ch.ID, ch.Name, ch.Description, ch.ImagePrompt+styleExpand(plan.Style)+castContrastBlock(plan, ch.Name))
	}
	for _, sc := range plan.Scenes {
		check("scenes", "scene:"+sc.ID, sc.Name, sc.Description, sc.ImagePrompt)
	}

	// Q 版形象(chibi):主定妆照就绪的角色同步生成 Q 版,内心活动镜头的表达素材。
	// 独立资产 ID(char:<id>_chibi),渲染 ref 不携带,仅作画面内容/花絮素材。
	if proj.Config.Params.Script.Chibi && comfyGen != nil {
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
			chibiPrompt := "cute chibi SD figure of the same character, big head, tiny body, full body, simple pastel background, " +
				"expressive face, figure product photography, " + ch.ImagePrompt + styleExpand(plan.Style) + castContrastBlock(plan, ch.Name)
			seed := int64(len(ch.ID)*1000 + len(ch.Name)*7 + 13)
			c.emit(proj.ID, PhaseAssets, 0, "info", 0, "Krea-2 生成:「%s」Q 版形象…", ch.Name)
			if path, err := comfyGen.GenerateImage(ctx, chibiPrompt, krea2Negative, seed, 1024, 1024, out); err == nil {
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

	if err := c.store.SaveAssets(proj.ID, assets); err != nil {
		return err
	}
	pct := 100
	if total > 0 {
		pct = (total - missing) * 100 / total
	}
	if p.Source == "skip" {
		c.emit(proj.ID, PhaseAssets, 0, "success", 100, "✔ 资产策略=跳过(全部镜头纯文生 t2va)")
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
	return ce.GenerateImage(ctx, imagePrompt, krea2Negative, seed, w, h, out)
}

// genCharacterViews 用 H3 Ref2V 生成"角色原地转身"的短视频,再抽帧横向拼成
// 多视角转台图。返回转台图路径(失败返回空串)。
//
// 为什么不用 Krea-2 img2img 直接出多张:实测行不通 —— Krea-2 是**纯文生图模型,
// 没有身份条件注入机制**,只能靠 denoise 在"改动幅度"与"保住身份"之间猜:
// denoise 0.7 会把人整个换掉(木偶角色被改写成人类),0.55 则身份勉强保住但
// 角度几乎不变。这条路是模型的固有限制,不是参数调优能解决的。
//
// 正解是让**有身份参考机制**的 H3 Ref2V 来完成:以定妆照为参考生成一段转身视频,
// 身份由 ref 图锁住、角度由提示词驱动,两者不再互斥(这正是 ComfyUI-ALLinONE
// 的 Character Sheet 模式的做法)。成本是"一次单镜渲染"(数分钟),换来的是
// 真正可用的多视角参考。
//
// 产物(**全部落在 assets/characters/ 下,与定妆照同目录同主名**):
//   - assets/characters/<base>_view_threequarter|side|back.png  多视角原图
//   - assets/characters/<base>_turnaround.png   横向拼接的转台图(渲染时作额外参考;
//     合成一张是为了只占 1 个参考槽,多图并列容易把模型绕晕)
//   - assets/characters/<base>_views/turnaround.mp4  转身视频本身(体积大且不作参考,
//     留在子目录里,不参与入库)
//
// 抽帧必须落在**定妆照同目录**(而不是 _views/ 子目录):资产库的
// relatedImages/ClearRelated 用 `filepath.Glob(<dir>/<base>_view_*.png)` 收集与
// 清理,glob 不递归 —— 帧放子目录里的结果是「多视角原图既收不进库、也清不掉」。
// 文件名里的 tag 与前端 LibraryView 的 labelOf() 标签(threequarter/side/back)
// 逐字对齐,标签才能显示成「四分之三/侧面/背面」而不是笼统的「多视角」。
//
// 这是增强项:任何一步失败都只告警、不阻断资产阶段,退回单张定妆照继续。
func (c *Coordinator) genCharacterViews(ctx context.Context, proj *project.Project, ce engine.Engine, id, name, imagePrompt, masterPath string) string {
	safe := director.SanitizeForCache("characters_" + id)
	dir := filepath.Join(c.store.Dir(proj.ID), "assets", "characters")
	// base 取给定妆照的文件主名 —— 与 genAssetImage 落盘的 <safe>.png 一致,
	// 也让资产库的 glob 一定能命中(不依赖两处 safe 拼法永远同步)。
	base := strings.TrimSuffix(filepath.Base(masterPath), filepath.Ext(masterPath))
	viewsDir := filepath.Join(dir, safe+"_views")
	if err := os.MkdirAll(viewsDir, 0o755); err != nil {
		return ""
	}
	clip := filepath.Join(viewsDir, "turnaround.mp4")

	c.emit(proj.ID, PhaseAssets, 0, "info", 0, "多视角:「%s」转身视频生成中(H3 Ref2V,约数分钟)…", name)
	job := engine.Job{
		ProjectID: proj.ID, ShotID: 0,
		Prompt:    turnaroundPrompt(name, imagePrompt),
		Mode:      "ref2va",
		RefImages: []engine.RefImage{{Kind: "character", Path: masterPath}},
		Width:     832, Height: 1248,
		Length:    124, // 17k+5 网格,约 5.2 秒
		Seed:      nameSeed("turnaround", name),
		Steps:     proj.Config.Params.Render.Steps,
		Accel:     proj.Config.Params.Render.Accel,
		OutputDir: viewsDir, OutputName: "turnaround",
		OnProgress: func(p int, note string) {
			c.emit(proj.ID, PhaseAssets, 0, "info", 0, "多视角「%s」:%s(%d%%)", name, note, p)
		},
	}
	if _, err := ce.Render(ctx, job); err != nil {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "多视角转身视频生成失败(不影响定妆照):%v", err)
		return ""
	}

	// 从转身视频按时间点抽帧:1.3s 四分之三 → 2.6s 侧面 → 3.9s 背面。
	// 0s 的正面帧**刻意不抽**:它与定妆照本就是同一张脸同一角度,拼转台图时直接
	// 用定妆照作第一格(库里也已经有 portrait.png),不必再占一份磁盘与一个 glob 名额。
	stamps := []struct{ at, tag string }{
		{"1.3", "threequarter"},
		{"2.6", "side"},
		{"3.9", "back"},
	}
	views := []string{}
	for _, s := range stamps {
		f := filepath.Join(dir, base+"_view_"+s.tag+".png")
		if _, err := c.ff.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-ss", s.at, "-i", clip, "-map", "0:v:0", "-frames:v", "1", "-update", "1", f); err != nil {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "多视角抽帧失败(%ss):%v", s.at, err)
			continue
		}
		if st, err := os.Stat(f); err == nil && st.Size() > 0 {
			views = append(views, f)
		}
	}
	if len(views) < 2 {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "多视角可用帧不足(仅 %d 张),跳过转台图", len(views))
		return ""
	}

	// 转台图 = 正面定妆照 + 抽到的各角度帧(顺序即 正面→四分之三→侧面→背面)
	frames := make([]string, 0, len(views)+1)
	if _, err := os.Stat(masterPath); err == nil {
		frames = append(frames, masterPath)
	}
	frames = append(frames, views...)

	turn := filepath.Join(dir, base+"_turnaround.png")
	if err := c.hstackImages(ctx, frames, turn); err != nil {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "转台图拼接失败(各角度帧已保留在 %s):%v", dir, err)
		return ""
	}
	c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔「%s」转台图已生成(%d 个视角)", name, len(frames))
	return turn
}

// turnaroundPrompt 组装 H3 Ref2V 的转身视频提示词(六段式,与 director 的
// sysH3Format 同构)。身份由参考图锁定,提示词只需驱动"原地缓慢转身"这一动作,
// 因此不写任何可能诱发换脸的风格化词汇。
func turnaroundPrompt(name, imagePrompt string) string {
	desc := stripComposition(imagePrompt)
	if strings.TrimSpace(desc) == "" {
		desc = name
	}
	return `subject_definitions:
<Subject 1> is ` + desc + ` in <Picture 1>.

summary:
[reference generation]
A character turnaround reference: the character stands still in the centre of frame and slowly rotates in place, showing the front view first, then turning through three-quarter and full profile to the back view. Plain seamless background, even studio lighting, the character never leaves the frame and never changes appearance.

retention_analysis:
- <Picture 1> : fully_preserved - character identity (face shape, hairstyle, hair colour, costume, accessories, body proportions) must stay identical to the reference image in every frame. FACE LOCK: copy the facial features, bone structure and skin details from <Picture 1> exactly; do NOT blend, average, or substitute any other face.

detailed_description:
Character turnaround on a plain seamless background, soft even studio lighting, no props, no text.
[Shot 1] The character stands centred, facing the camera directly, arms relaxed at the sides, neutral expression; the camera is static and the framing is an upper-body medium shot.
[Shot 2] At 00:01.300 The character has rotated about forty-five degrees, now seen from a three-quarter angle; facial features, hair and costume remain unchanged.
[Shot 3] At 00:02.600 The character has rotated about ninety degrees, now in full profile; the silhouette of the hairstyle and costume stays identical.
[Shot 4] At 00:03.900 The character has rotated about one hundred and eighty degrees, now seen from behind; the back of the hairstyle and the costume are visible, still identical.
The whole clip is one continuous slow rotation with no cuts, no camera movement and no background change. No on-screen text, no subtitles, no watermarks, no logos. Subjects must remain clearly visible and adequately lit throughout the video; avoid rendering any frame nearly black or overly dark.

overall_soundscape:
Quiet room tone, no speech, no music, no sound effects.

non_diegetic_music:
N/A`
}

// stripComposition 剥掉定妆照提示词里的**构图要求**。
//
// 必须剥:定妆照提示词含 front-facing / plain background / upper-body framing,
// 而多视角要的正是"不朝正面" —— 两组指令直接打架,实测是身份漂移的主因之一。
// 只保留身份与服装描述,让角度指令说了算。
func stripComposition(p string) string {
	for _, w := range []string{
		"front-facing", "plain background", "upper-body framing", "upper body framing",
		"upper-body", "upper body", "full body", "portrait",
	} {
		p = strings.ReplaceAll(p, w, "")
	}
	return strings.Join(strings.Fields(p), " ") // 压缩多余空格
}

// hstackImages 横向拼接多张图(ffmpeg hstack):统一缩放到同一高度后并排,
// 得到一张转台图。
func (c *Coordinator) hstackImages(ctx context.Context, inputs []string, out string) error {
	if len(inputs) < 2 {
		return fmt.Errorf("至少需要两张图才能拼接")
	}
	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	for _, in := range inputs {
		args = append(args, "-i", in)
	}
	filters := make([]string, 0, len(inputs)+1)
	refs := ""
	for i := range inputs {
		filters = append(filters, fmt.Sprintf("[%d:v]scale=-1:1248[v%d]", i, i))
		refs += fmt.Sprintf("[v%d]", i)
	}
	filters = append(filters, fmt.Sprintf("%shstack=inputs=%d[out]", refs, len(inputs)))
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[out]", "-frames:v", "1", out)
	_, err := c.ff.Run(ctx, args...)
	return err
}

// nameSeed 资产名→稳定种子:同名必同图,是跨集一致性的第二道保险
// (第一道是资产库按名复用)。
func nameSeed(kind, name string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(kind + "\x00" + name))
	return int64(h.Sum32())
}

// styleExpand 风格英文具象化:用户的 art_style 多为中文速记(如「次世代
// 3D·BJD 虚拟人+半写实国漫AI漫剧质感」),STYLE LOCK 让 image_prompt 逐
// 字照搬中文——Krea-2 缺具体英文视觉词汇时照片先验胜出,人类角色生成
// 写实真人(2026-09-11 实证:同一本书木偶系角色因自带"wooden android"
// 描述正常,人类系全部照片质感)。按关键词展开确定性英文渲染词汇追加;
// 中文原风格保留(跨集一致性锚),真人剧关键词放行写实(合法风格)。
func (c *Coordinator) backfillAssets(ctx context.Context, proj *project.Project, plan *project.Plan, assets map[string]project.Asset) bool {
	if proj.Config.Params.Assets.Source == "skip" {
		return true
	}
	type miss struct{ kind, id, name, desc, prompt string }
	var list []miss
	check := func(kind, id, name, desc, prompt string) {
		if strings.TrimSpace(prompt) == "" {
			return
		}
		if a, ok := assets[id]; ok && a.File != "" {
			if _, err := os.Stat(filepath.Join(c.store.Dir(proj.ID), a.File)); err == nil {
				return
			}
		}
		list = append(list, miss{kind, id, name, desc, prompt})
	}
	for _, ch := range plan.Characters {
		check("characters", "char:"+ch.ID, ch.Name, ch.Description, ch.ImagePrompt+styleExpand(plan.Style)+castContrastBlock(plan, ch.Name))
	}
	for _, sc := range plan.Scenes {
		check("scenes", "scene:"+sc.ID, sc.Name, sc.Description, sc.ImagePrompt)
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
		c.emit(proj.ID, PhaseRender, 0, "success", 0, "✔ 已补齐:%s「%s」", kindLabel(m.kind), m.name)
		if c.lib != nil {
			_, _ = c.lib.Put(m.kind, m.name, m.desc, m.prompt, path, proj.ID, "krea2")
		}
	}
	_ = c.store.SaveAssets(proj.ID, assets)
	return true
}

// StylePresets 渲染风格预设(可多选组合):key → (显示名, 英文风格提示)。
// 提示词进 LLM 全局风格段,决定全片视觉基调。
