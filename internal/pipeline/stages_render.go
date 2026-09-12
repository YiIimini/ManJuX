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
	if rp.Seam && engName == "comfy" {
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
	if ce, ok := eng.(*comfyengine.Engine); ok && engName == "comfy" {
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
	// 渲染前补齐缺失参考图(不问资产来源模式,除 skip;缺图先生成再渲染)
	c.backfillAssets(ctx, proj, plan, assets)
	// 多视角转台图补齐:角色只有单张正面照时,渲染前补出转身多视角
	// (库复用/手动上传/存量项目三类不会重跑资产阶段,只能在这里兜)
	c.backfillAssetViews(ctx, proj, plan, assets)
	// 阶段起点清一次遗留队列(仅本地引擎;绝不在逐镜循环内清,防误杀排队任务)
	// 并释放残留显存:上一阶段(Krea-2 定妆/抽卡等)模型常驻会把 H3 主模型
	// (~20GB)挤出显存触发 dynamic VRAM loading——每步在 CPU↔GPU 搬权重,
	// 实测 150s/步(正常 ~2s),极端时整镜 45 分钟超时(2026-09-10 血案)。
	if engName == "comfy" {
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
	var failures []string
	for i, shot := range shots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		base := (i * 100) / len(shots)

		// 参考图组装(ref2va):角色定妆照 + 场景图(契约与顺序见 AssembleShotRefs)
		var refs []engine.RefImage
		mode := shot.Mode
		if proj.Config.Params.Assets.Source != "skip" && mode == "ref2va" {
			var missing []string
			refs, missing = AssembleShotRefs(c.store.Dir(proj.ID), shot, assets,
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

		seed := rp.Seed
		if seedOverride > 0 {
			seed = seedOverride + int64(shot.ID)*101 // 单镜微调种子:同值同镜稳定复现
		} else if seed == 0 {
			// 角色锚定种子:默认种子曾用时间随机,同角色同定妆照跨集仍换脸
			// (2026-09-09 用户实证「貅兜兜飘逸」)。改为按镜头角色组合派生,
			// 同角色组合跨集得到同基种子,配合同一张库定妆照,一致性最强;
			// shot.ID 项保住集内镜头间的多样性。
			seed = nameSeed("shot", strings.Join(shot.Characters, "+")) + int64(shot.ID)*101
		}
		// 角色一致性锚定:出场角色的固定外貌描述强制前置,消除 LLM 逐镜漂移
		// 语言标记兜底(幂等):老方案存盘时还没有这层处理
		prompt := EnsureChineseDialogue(anchorPrompt(plan, &shot))
		// 指纹记录「配置 seed」而非运行时随机值 —— 随机模式(=0)下每次时间戳不同,
		// 指纹必然失配,断点续跑的已渲镜头全部误判失效(从头重跑的第二根因)。
		// 渲染仍用上面的随机 seed 保多样;产物有效性以 提示词/参考图/分辨率/步数/档位 为准。
		inputFP := renderFingerprint(prompt, refs, rp, mode, rp.Seed)
		outName := fmt.Sprintf("shot_%03d", shot.ID)

		// 断点续跑:同输入指纹且文件存在 → 跳过
		if m, ok := manifest[strconv.Itoa(shot.ID)]; ok && m.Status == project.ShotDone && m.InputFP == inputFP {
			full := filepath.Join(c.store.Dir(proj.ID), m.File)
			if st, err := os.Stat(full); err == nil && st.Size() > 0 {
				skipped++
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
				Length: shot.Duration * 24, Seed: seed, Steps: rp.Steps,
				Accel:     rp.Accel,
				OutputDir: clipsDir, OutputName: outName,
				Timeout:  shotTimeout,
				Seam:     rp.Seam && engName == "comfy",
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
			if engName == "comfy" {
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
		c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotDone, engName, rel, inputFP, "")
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

func (c *Coordinator) setShotStatus(projectID string, manifest map[string]project.ClipManifest, shotID int, st project.ShotStatus, eng, file, fp, errMsg string) {
	key := strconv.Itoa(shotID)
	m := manifest[key]
	m.ShotID = shotID
	m.Status = st
	m.Engine = eng
	m.File = file
	m.InputFP = fp
	m.Error = errMsg
	m.UpdatedAt = time.Now()
	manifest[key] = m
	c.store.SaveManifest(projectID, manifest) // 每次落盘,崩溃可恢复
}

// anchorPrompt 角色一致性锚定:把出场角色/场景的固定外貌描述和全局风格
// 强制前置到镜头提示词。H3 是 VLM 输入,中文锚定段与六段式正文并存无碍;
// 跨镜同角色描述逐字一致,配合定妆照 ref 双通道压住 LLM 逐镜漂移。

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
