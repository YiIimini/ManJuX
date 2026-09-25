package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/sysmon"
)

func (c *Coordinator) runRender(ctx context.Context, proj *project.Project, only []int, engineOverride string, seedOverride int64) error {
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		return fmt.Errorf("尚未生成分镜方案:请先运行「剧本分镜」阶段")
	}
	assets, err := c.store.LoadAssets(proj.ID)
	if err != nil {
		return err
	}
	manifest, err := c.store.LoadManifest(proj.ID)
	if err != nil {
		return err
	}
	g := c.cfg.Get()
	rp := proj.Config.Params.Render
	// 单镜渲染超时取自设置页(全局),此前该配置项被引擎写死忽略 —— 用户怎么改
	// 都不生效,只能吃 45 分钟硬编码。现在经 Job.Timeout 透传给各引擎。
	shotTimeout := time.Duration(g.Render.ShotTimeout) * time.Minute
	if rp.Turbo {
		// turbo LoRA 生效时步数钳到 4-8
		if rp.Steps > 8 {
			rp.Steps = 8
		}
	}

	// 引擎解析:本次覆盖 > 项目配置(引擎=api 时云端 API Key 必须就绪)
	engName := rp.Engine
	if engineOverride == "auto" || engineOverride == "comfy" || engineOverride == "api" {
		engName = engineOverride
	}
	if engName == "auto" {
		comfyEng, _ := c.reg.Get("comfy")
		if comfyEng != nil && comfyEng.Available(ctx) == nil {
			engName = "comfy"
		} else {
			apiEng, _ := c.reg.Get("api")
			if apiEng != nil && apiEng.Available(ctx) == nil {
				engName = "api"
			}
		}
	}
	if engName == "" {
		return fmt.Errorf("无可 用引擎:本地 ComfyUI 与云端 API 均不可用,请检查设置")
	}
	eng, err := c.reg.Get(engName)
	if err != nil {
		return err
	}
	c.emit(proj.ID, PhaseRender, 0, "info", 0, "引擎=%s(%s)", engName, engineLabel(engName))
	// 引擎能力表:本地专属动作(接缝/清队列/释放显存/本地预检/本地资产补齐)
	// 一律按"它实际能做什么"判断,不再比较引擎名 —— 见 engine.Caps 的说明。
	caps := engine.CapsOf(engName)
	if note := unsupportedParamNote(caps, rp); note != "" {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ %s", note)
	}
	if rp.Seam && caps.Seam {
		c.emit(proj.ID, PhaseRender, 0, "info", 0, "镜间连续接缝已开启:每镜尾部 22 帧钉入下一镜(仅本地引擎;重渲某镜后建议后续镜头一并重渲保持链一致)")
	}

	shots := plan.Shots
	if len(only) > 0 {
		want := map[int]bool{}
		for _, id := range only {
			want[id] = true
		}
		var filtered []project.Shot
		for _, s := range shots {
			if want[s.ID] {
				filtered = append(filtered, s)
			}
		}
		shots = filtered
	}
	if len(shots) == 0 {
		return fmt.Errorf("没有可渲染的镜头")
	}

	clipsDir := filepath.Join(c.store.Dir(proj.ID), "clips")
	_ = os.MkdirAll(clipsDir, 0o755)
	// 渲染前节点体检:把「插件节点缺失」这类注定失败的问题挡在生成参考图与
	// 排队之前。此前只有打开管理页才查节点(且只查 2 个),渲染跑到一半才失败,
	// 用户只看到 ComfyUI 转述的原始报文,分不清缺插件还是缺模型。
	if ce, ok := eng.(*comfyengine.Engine); ok && caps.Local {
		// 已知冲突插件(只告警不阻断):例如 MiniMaxH3-Cache 的启动期 monkeypatch
		// 会让镜间接缝必然失败,而用户可能为其它工作流有意保留它。
		for _, warn := range ce.Conflicts() {
			c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ %s(详见 docs/COMPATIBILITY.md)", warn)
		}
		modes := make([]string, 0, len(shots))
		for _, s := range shots {
			modes = append(modes, s.Mode)
		}
		if missing := ce.Preflight(ctx, comfyengine.PreflightOptions{Modes: modes}); len(missing) > 0 {
			msg := strings.Join(missing, "、")
			c.emit(proj.ID, PhaseRender, 0, "error", 0,
				"✕ ComfyUI 缺少渲染所需节点,已阻止本次渲染:%s(请确认对应插件已安装,管理页可核对引擎栈)", msg)
			return fmt.Errorf("ComfyUI 节点缺失:%s", msg)
		}
	}
	// 渲染前补齐缺失参考图(不问资产来源模式,除 skip;缺图先生成再渲染)。
	//
	// 范围:本次只渲一部分镜头时,**只补这些镜头会用到的资产** —— 旧实现传的是整份
	// plan,于是"只改一镜 seed"可能顺手生成全片所有缺失定妆照与所有角色的多视角
	// (每个角色数分钟 H3),白烧 GPU(审计 P0-9)。
	need := needForShots(shots)
	if len(only) > 0 {
		c.emit(proj.ID, PhaseRender, 0, "info", 0, "本次仅重渲 %d 个镜头:资产补齐只针对这些镜头需要的角色/场景", len(shots))
	}
	// 本次显式选了云端引擎时不做本地资产补齐:那是用户明确不要本地 GPU 时间的语义,
	// 而补齐必然要起 Krea-2/H3(旧实现忽略了这个覆盖,审计 P0-9 附带项)。
	if need != nil && !caps.Local {
		c.emit(proj.ID, PhaseRender, 0, "info", 0, "本次引擎=%s:跳过本地资产补齐(单镜范围内缺图将降级纯文生)", engName)
	} else if !need.isEmpty() {
		c.backfillAssets(ctx, proj, plan, assets, need)
		// 多视角转台图补齐:角色只有单张正面照时,渲染前补出转身多视角
		// (库复用/手动上传/存量项目三类不会重跑资产阶段,只能在这里兜)
		c.backfillAssetViews(ctx, proj, plan, assets, need)
	}
	// 阶段起点清一次遗留队列(仅本地引擎;绝不在逐镜循环内清,防误杀排队任务)
	// 并释放残留显存:上一阶段(Krea-2 定妆/抽卡等)模型常驻会把 H3 主模型
	// (~20GB)挤出显存触发 dynamic VRAM loading——每步在 CPU↔GPU 搬权重,
	// 实测 150s/步(正常 ~2s),极端时整镜 45 分钟超时(2026-09-10 血案)。
	if caps.Queue {
		eng.ClearQueue(ctx)
		b, hasB := sysmon.VRAMUsedMB()
		_ = eng.FreeVRAM(ctx)
		// 卸载+empty_cache 是异步落下去的,立刻读会永远得到"没释放"(实测一直显示
		// 22.3 GB → 22.3 GB,用户据此以为这步是假的)。这里轮询最多 5 秒取最小值,
		// 如实反映释放结果;若确实没降也照实说,不粉饰。
		if hasB {
			after, ok := b, false
			for i := 0; i < 10; i++ {
				time.Sleep(500 * time.Millisecond)
				if a, hasA := sysmon.VRAMUsedMB(); hasA {
					ok = true
					if a < after {
						after = a
					}
					if after < b-256 { // 已明显下降,不用再等
						break
					}
				}
			}
			if ok {
				if after >= b {
					c.emit(proj.ID, PhaseRender, 0, "info", 0, "🧽 渲染前释放显存:%.1f GB(引擎无可卸载的常驻模型)", b/1024)
				} else {
					c.emit(proj.ID, PhaseRender, 0, "info", 0, "🧽 渲染前释放显存:%.1f GB → %.1f GB(腾出主模型常驻空间)", b/1024, after/1024)
				}
			}
		}
	}
	done, skipped, failed := 0, 0, 0
	// legacyModel 本次被跳过的镜头里,有多少是**没记录渲染模型**的老产物
	// (本字段引入之前的清单)。刚换过底模时这些镜头仍是旧模型的画面:
	// 如实提示,让用户自己决定要不要全量重渲 —— 不静默替他烧 GPU。
	legacyModel := 0
	var failures []string
	for i, shot := range shots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		base := (i * 100) / len(shots)

		// 参考图组装(ref2va):角色定妆照 + 场景图(契约与顺序见 RefPlan)
		var refs []engine.RefImage
		var roles []RefRole
		mode := shot.Mode
		if proj.Config.Params.Assets.Source != "skip" && mode == "ref2va" {
			var missing []string
			refs, roles, missing = RefPlan(c.store.Dir(proj.ID), shot, assets,
				proj.Config.Params.Assets.CompactRefsOn())
			if len(refs) == 0 {
				mode = "t2va" // 资产缺失降级纯文生
				c.emit(proj.ID, PhaseRender, shot.ID, "warn", base, "镜头 %d 无参考图,降级 t2va", shot.ID)
			} else if len(missing) > 0 {
				// 少一张图会让后续 <Picture> 编号整体前移,而提示词的 subject_definitions
				// 是按 characters 顺序写的 —— 必须让用户看见,不能悄悄指错图。
				c.emit(proj.ID, PhaseRender, shot.ID, "warn", base,
					"镜头 %d 缺 %d 张参考图(%s),提示词中的画面编号会随之错位,建议补齐后重渲",
					shot.ID, len(missing), strings.Join(missing, "/"))
			}
		} else if mode == "ref2va" && proj.Config.Params.Assets.Source == "skip" {
			mode = "t2va"
		}

		// 镜头种子:唯一收口点 ShotSeed(override > 项目固定种子 > 按角色组合派生)
		seed := ShotSeed(rp.Seed, seedOverride, shot.ID, shot.Characters)
		// 角色一致性锚定:出场角色的固定外貌描述强制前置,消除 LLM 逐镜漂移
		// 语言标记兜底(幂等):老方案存盘时还没有这层处理
		// 渲染提示词:身份锚定 + 画风硬约束(动漫化/哑光)+ 台词语言标记,四路渲染共用
		prompt := ShotRenderPrompt(proj, plan, &shot)
		// 参考图**权威清单**:张数与编号由渲染器按实际装配结果写出(LLM 看不见张数,
		// 猜错就会把多视角拼合图当成场景图 —— 2026-09-25 镜头6 角色漂移的根因,见
		// refmanifest.go)。无参考图时 RefManifest 返回空串,提示词原样放行。
		prompt = EnsureRefManifest(prompt, RefManifest(shot, roles))
		// 指纹记录「配置 seed」而非运行时随机值 —— 随机模式(=0)下每次时间戳不同,
		// 指纹必然失配,断点续跑的已渲镜头全部误判失效(从头重跑的第二根因)。
		// 渲染仍用上面的随机 seed 保多样;产物有效性以 提示词/参考图/分辨率/步数/档位 为准。
		inputFP := renderFingerprint(prompt, refs, rp, mode, rp.Seed)
		outName := fmt.Sprintf("shot_%03d", shot.ID)

		// 断点续跑:同输入指纹 + 帧数对得上 + 文件存在 → 跳过
		wantFrames := shotFrames(shot)
		wantModel := renderModelTag(c.cfg.Get(), mode, engName)
		if m, ok := manifest[strconv.Itoa(shot.ID)]; ok && shotArtifactValid(m, inputFP, wantFrames, wantModel) {
			full := filepath.Join(c.store.Dir(proj.ID), m.File)
			if st, err := os.Stat(full); err == nil && st.Size() > 0 {
				skipped++
				if m.Model == "" {
					legacyModel++
				}
				c.emit(proj.ID, PhaseRender, shot.ID, "info", base+100/len(shots), "镜头 %d 产物有效,跳过(断点续跑)", shot.ID)
				continue
			}
		}

		// 参考图张数写进日志:一是"参考图去冗余"是否生效一眼可查(转台图已含正面,
		// 每个角色应只占 1 张),二是出问题时能立刻判断是不是参考图少了。
		c.emit(proj.ID, PhaseRender, shot.ID, "info", base, "镜头 %d/%d 渲染开始(%s,%ds,seed=%d,参考图 %d 张:%s)",
			shot.ID, len(shots), mode, shot.Duration, seed, len(refs), refComposition(refs))
		c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotRendering, engName, "", inputFP, "")

		var res *engine.Result
		var err error
		for attempt := 1; attempt <= 2; attempt++ {
			res, err = eng.Render(ctx, engine.Job{
				ProjectID: proj.ID, ShotID: shot.ID,
				Prompt: prompt, Mode: mode, RefImages: refs,
				Width: rp.Width, Height: rp.Height,
				Length: wantFrames, Seed: seed, Steps: rp.Steps,
				Accel:     rp.Accel,
				OutputDir: clipsDir, OutputName: outName,
				Timeout:  shotTimeout,
				Seam:     rp.Seam && caps.Seam,
				PrevShot: shot.ID - 1,
				OnProgress: func(p int, note string) {
					total := base + p/len(shots)
					if total > 100 {
						total = 100
					}
					c.emit(proj.ID, PhaseRender, shot.ID, "info", total, "镜头 %d:%s", shot.ID, note)
				},
			})
			if err == nil || ctx.Err() != nil {
				break
			}
			// 超时类失败多为显存挤压导致的动态换页极慢(150s/步)甚至假死:
			// 先释放显存再原地重试一次,多数可恢复正常速度(2026-09-10)。
			// 用哨兵判断而非匹配「超时」二字 —— 调整文案不该悄悄改掉重试行为。
			if !errors.Is(err, engine.ErrRenderTimeout) || attempt == 2 {
				break
			}
			c.emit(proj.ID, PhaseRender, shot.ID, "warn", base, "⚠ 镜头 %d 超时,释放显存后重试一次:%v", shot.ID, err)
			if caps.VRAM {
				_ = eng.FreeVRAM(ctx)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// 单镜失败不中止:记录后继续后续镜头,阶段末汇总
			failed++
			failures = append(failures, fmt.Sprintf("镜%d:%v", shot.ID, err))
			c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotFailed, engName, "", inputFP, err.Error())
			c.emit(proj.ID, PhaseRender, shot.ID, "error", base, "✕ 镜头 %d 渲染失败(继续下一镜):%v", shot.ID, err)
			continue
		}
		rel, _ := filepath.Rel(c.store.Dir(proj.ID), res.VideoPath)
		c.setShotStatusLen(proj.ID, manifest, shot.ID, project.ShotDone, engName, rel, inputFP, "", wantFrames, wantModel)
		if res.TaskID != "" {
			manifest[strconv.Itoa(shot.ID)] = mergeTask(manifest[strconv.Itoa(shot.ID)], res.TaskID)
		}
		done++
		c.emit(proj.ID, PhaseRender, shot.ID, "success", base+100/len(shots), "✔ 镜头 %d 完成(%s)", shot.ID, rel)
	}
	if err := c.store.SaveManifest(proj.ID, manifest); err != nil {
		return err
	}
	_ = g
	if failed > 0 {
		// 有失败:报告但不抛错(qc/assemble 自行处理缺失);全失败才视为阶段失败
		if done+skipped == 0 {
			return fmt.Errorf("全部 %d 镜渲染失败:%s", failed, strings.Join(failures, ";"))
		}
		c.emit(proj.ID, PhaseRender, 0, "warn", 100, "⚠ 渲染结束:成功 %d,跳过 %d,失败 %d(%s)", done, skipped, failed, strings.Join(failures, ";"))
		return nil
	}
	c.emit(proj.ID, PhaseRender, 0, "success", 100, "✔ 渲染完成:新出 %d 镜,跳过 %d 镜", done, skipped)
	if legacyModel > 0 {
		c.emit(proj.ID, PhaseRender, 0, "warn", 100,
			"⚠ 跳过的 %d 镜没有渲染模型记录(产出于该字段引入之前):若你刚换过底模,这些镜头仍是旧模型的画面,"+
				"与本次新渲的镜头画风可能不一致 —— 需要统一全片画风时请勾选这些镜头重渲(引擎=%s,当前底模=%s)",
			legacyModel, engName, renderModelTag(c.cfg.Get(), "ref2va", engName))
	}
	return nil
}

func mergeTask(m project.ClipManifest, taskID string) project.ClipManifest {
	m.TaskID = taskID
	return m
}

func engineLabel(name string) string {
	switch name {
	case "comfy":
		return "本地 ComfyUI · H3-Base"
	case "api":
		return "MiniMax 云端 API"
	}
	return name
}

func renderFingerprint(prompt string, refs []engine.RefImage, rp project.RenderParams, mode string, seed int64) string {
	var files []string
	for _, r := range refs {
		files = append(files, r.Path+":"+project.FingerprintFile(r.Path))
	}
	// 知识库工程教训:指纹必须覆盖采样质量配置(VAE/LoRA 档位),否则换档后旧缓存静默命中。
	// 兼容:默认档(turbo/空)不附加标记 —— 与旧版指纹串完全一致,存量项目续跑不触发全量重渲;
	// 显式 quality/express 档才追加(换档=有意重渲)。
	accelTag := ""
	if rp.Accel == "quality" || rp.Accel == "express" {
		accelTag = "accel" + rp.Accel
	}
	// qualityTag:采样环境版本(v1=2026-09-10 撤下 fp16_accumulation/ck-attention
	// 后的画质基线;画质敏感项再变更时递增,旧镜指纹失配自动重渲)。
	return project.FingerprintText(prompt, mode, fmt.Sprint(rp.Width, "x", rp.Height, "s", rp.Steps, "seed", seed, "seam", rp.Seam, accelTag, "q1"), strings.Join(files, ","))
}

// shotFrames 镜头时长 → 帧数(H3 侧 24fps)。
//
// 收成一个函数是因为它有两个消费者:提交给引擎的 Job.Length,以及"这个已有产物还能
// 不能复用"的判据。以前只有前者,后者缺位 —— 于是"改了时长却跳过重渲"能一路走到成片。
func shotFrames(shot project.Shot) int { return shot.Duration * 24 }

// shotArtifactValid 判定**已有产物能否复用**(断点续跑的核心判据),抽成纯函数以便穷举。
//
// 2026-09-24 修(审计 P0-8):判据原先是"状态 Done + 输入指纹一致",而指纹串里**不含时长**
// (往指纹里加字段会让存量项目全部重渲,不能那么修)。于是用户把 4 秒改成 6 秒(为了装下
// 被截断的台词)、点「保存并重渲本镜」,指纹不变 → 走跳过分支 → 旧片原样保留,界面还报成功。
//
// Length 的兼容语义:`Length == 0` 表示这份清单是**本字段引入之前**写的,判据不推翻
// 指纹结论 —— 否则升级一版就会让所有存量项目的已渲镜头集体重渲(几十集的代价),
// 而它并不比"照旧"更正确。渲染过一次之后 Length 就有值了,从此改动必被抓住。
func shotArtifactValid(m project.ClipManifest, inputFP string, wantFrames int, wantModel string) bool {
	if m.Status != project.ShotDone || m.File == "" || m.InputFP != inputFP {
		return false
	}
	if m.Length > 0 && m.Length != wantFrames {
		return false
	}
	// 换了底模/加速 LoRA → 旧片作废(否则同一集里混着两种模型的画风)。
	// Model=="" 是本字段引入前的清单:不推翻指纹结论,由调用方提示用户。
	if m.Model != "" && wantModel != "" && m.Model != wantModel {
		return false
	}
	return true
}

// renderModelTag 本次渲染的**模型身份**:底模 + 加速 LoRA(本地)或云端模型名。
//
// 为什么必须有它:用户换了底模(2026-09-24 换 Minimax-h3_Singularity)之后,
// 断点续跑会拿旧模型的产物当"已完成"跳过 —— 一集里两种画风混着,而指纹看不出来。
func renderModelTag(g config.Settings, mode, engName string) string {
	if engName == "api" {
		return "api:" + g.MiniMax.Model
	}
	if mode == "ref2va" {
		return g.Comfy.UNetRef2VA + "+" + g.Comfy.TurboLoRARef2VA
	}
	return g.Comfy.UNetFL2VA + "+" + g.Comfy.TurboLoRAFL2VA
}

func (c *Coordinator) setShotStatus(projectID string, manifest map[string]project.ClipManifest, shotID int, st project.ShotStatus, eng, file, fp, errMsg string) {
	c.setShotStatusLen(projectID, manifest, shotID, st, eng, file, fp, errMsg, 0)
}

// setShotStatusLen 同上,并记录**本镜实际渲出的帧数**(0 = 调用方不关心,保留原值)。
//
// 帧数单独记的原因是"改了时长却没重渲"(审计 P0-8):指纹串里不能加字段(会让存量
// 项目全部重渲),而时长又不该被忽略 —— 于是跳过判据额外比一次帧数,见 renderStage。
func (c *Coordinator) setShotStatusLen(projectID string, manifest map[string]project.ClipManifest, shotID int, st project.ShotStatus, eng, file, fp, errMsg string, length int, model ...string) {
	key := strconv.Itoa(shotID)
	m := manifest[key]
	m.ShotID = shotID
	m.Status = st
	m.Engine = eng
	m.File = file
	m.InputFP = fp
	m.Error = errMsg
	if length > 0 {
		m.Length = length
	}
	if len(model) > 0 && model[0] != "" {
		m.Model = model[0]
	}
	m.UpdatedAt = time.Now()
	manifest[key] = m
	c.store.SaveManifest(projectID, manifest) // 每次落盘,崩溃可恢复
}

// anchorPrompt 角色一致性锚定:把出场角色/场景的固定外貌描述和全局风格
// 强制前置到镜头提示词。H3 是 VLM 输入,中文锚定段与六段式正文并存无碍;
// 跨镜同角色描述逐字一致,配合定妆照 ref 双通道压住 LLM 逐镜漂移。
// 实现见 prompts.go(含角色动漫化/哑光硬约束 facesOn)。

// refComposition 把参考图构成缩成一行(角色/场景 + 是否拼合图),供渲染日志核对。
func refComposition(refs []engine.RefImage) string {
	chars, hasScene := 0, false
	turn := 0
	for _, r := range refs {
		if r.Kind == "scene" {
			hasScene = true
			continue
		}
		chars++
		if strings.Contains(filepath.Base(r.Path), "_turnaround") {
			turn++
		}
	}
	parts := []string{fmt.Sprintf("角色 %d", chars)}
	if turn > 0 {
		parts = append(parts, fmt.Sprintf("其中拼合转身图 %d", turn))
	}
	if hasScene {
		parts = append(parts, "场景 1")
	}
	return strings.Join(parts, " + ")
}
