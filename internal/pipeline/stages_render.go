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
	// 读取时清掉的占位台词（`<d>[Chinese]…</d>`）如实播报：给模型一个"这里要说点什么"的空标签，
	// 它就会自己编一句或发出怪声 —— 用户听到的是"怪配音、重复配音"（实测那一集 36/74 个 <d> 是这种）。
	// 清理挂在唯一读取边界（project.LoadPlan），这里只负责说一声，不静默改输入。
	if plan.StrippedPlaceholders > 0 {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0,
			"⚠ 方案里有 %d 处**空台词标签**（`<d>…</d>` 内容只有省略号）已在读取时清掉 —— "+
				"这类标签会被模型当成「这里要说点什么」，从而自己编台词或发怪声（听感就是重复配音）；"+
				"要根治请重跑一次「剧本分镜」，提示词已禁止写占位标签", plan.StrippedPlaceholders)
	}
	// 单镜渲染超时取自设置页(全局),此前该配置项被引擎写死忽略 —— 用户怎么改
	// 都不生效,只能吃 45 分钟硬编码。现在经 Job.Timeout 透传给各引擎。
	shotTimeout := time.Duration(g.Render.ShotTimeout) * time.Minute
	if rp.Turbo && rp.Accel != "refine" {
		// turbo LoRA 生效时步数钳到 4-8。
		// refine 档例外:那一档**不挂蒸馏 LoRA**(见 accelLoraFor),步数不能被 4step/8step
		// 的锚定值压回去 —— 否则"精修档"名存实亡,渲出来还是 8 步的糊图。
		if rp.Steps > 8 {
			rp.Steps = 8
		}
	}

	// 档位可见性（ComfyUI 侧整改单 2026-09-27 · P0-2）：选了「画质档」但底模是量化版时，
	// 引擎会**回退**成 turbo —— 回退本身是有意设计且有测试的，但**静默**的后果是
	// "界面上写着画质档、跑出来与默认档一模一样"，事后只能翻代码才知道。
	// 每次运行播报**一次**（不逐镜刷），说清"没生效 + 为什么 + 怎么才能真正生效"。
	if note := comfyengine.AccelTierNote(rp.Accel, "ref2va", g.Comfy); note != "" {
		c.emit(proj.ID, PhaseRender, 0, "warn", 0, "⚠ %s", note)
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
		c.emit(proj.ID, PhaseRender, 0, "info", 0,
			"镜间连续接缝已开启(仅本地引擎):%s;换场/换人/到重置点时**自动断开并重建锚点**,断开原因逐镜写在日志里"+
				"(重渲某镜后建议后续镜头一并重渲保持链一致)", seamResetLabel(rp.SeamResetEvery))
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
	//
	// 【必须先过准入闸】旧实现无条件动手,而这一步有两颗地雷:
	//   - ClearQueue 会把**别人排队中**的任务清掉(那边的 WaitForDone 会以
	//     「任务已从队列消失」失败);
	//   - FreeVRAM 是 /free 卸权重,正跑着的任务会被踢出显存 → 每步重新流式加载
	//     (实测 150s/步,整镜 45 分钟超时)。
	// 一次性 GPU 操作(局部重绘/音频驱动/重生成定妆照)不受管线互斥约束,用户完全
	// 可能"先点了它再点渲染出片"。所以这里与 ReleaseVRAMIfIdle 共用同一道准入
	// (vramReleaseAllowed):探测不到队列状态、或队列里还有活,就**不动手**,只说明。
	if caps.Queue {
		allowed := true
		if ce, ok := eng.(*comfyengine.Engine); ok {
			actx, acancel := context.WithTimeout(ctx, 5*time.Second)
			st, aerr := ce.Admin(actx)
			acancel()
			if !vramReleaseAllowed(st, aerr) {
				busy := ""
				if st != nil {
					busy = fmt.Sprintf("(运行中 %d / 排队 %d)", st.Running, st.Pending)
				}
				c.emit(proj.ID, PhaseRender, 0, "warn", 0,
					"⚠ 引擎上还有别的任务%s,本次**跳过**渲染前的清队与显存释放 —— 不打断别人的任务;"+
						"若这一镜跑得异常慢,请在引擎空闲时重跑本阶段", busy)
				allowed = false
			}
		}
		if allowed {
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
	}
	done, skipped, failed := 0, 0, 0
	// legacyModel 本次被跳过的镜头里,有多少是**没记录渲染模型**的老产物
	// (本字段引入之前的清单)。刚换过底模时这些镜头仍是旧模型的画面:
	// 如实提示,让用户自己决定要不要全量重渲 —— 不静默替他烧 GPU。
	legacyModel := 0
	// refBindWarned 本次运行是否已经就"subject 一行都没绑上图"告警过(只报一次即可,
	// 逐镜刷屏会把真正的关键信息淹掉)。
	refBindWarned := false
	var failures []string
	for i, shot := range shots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		base := (i * 100) / len(shots)

		// 参考图组装(ref2va):角色定妆照 + 场景图(契约与顺序见 RefPlan)
		var refs []engine.RefImage
		var roles []RefRole
		mode := project.EffectiveShotMode(shot)
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
		//
		// 绑定前先量一次"有多少 subject 行能绑到图上":一行都没绑上 = 官方句式没落进
		// 提示词,模型会回到"按文字重画角色"(同一角色逐镜不同人),而清单照附、日志照写,
		// 看不出任何异常。这类静默回归必须说出来(每次运行只说一次,不逐镜刷屏)。
		if len(roles) > 0 && !refBindWarned {
			if b, tot := subjectBindStats(prompt, roles); tot > 0 && b == 0 {
				refBindWarned = true
				c.emit(proj.ID, PhaseRender, shot.ID, "warn", base,
					"⚠ 分镜方案的 subject_definitions 一行都没能绑到参考图(角色名与句式对不上):模型会按**文字**重画角色,"+
						"同一角色可能逐镜不同人 —— 建议核对方案里的 `<Subject N> is <角色名>` 写法")
			}
		}
		prompt = EnsureRefManifest(prompt, shot, roles)
		// 关键道具的**跨镜文字锚定**（2026-09-28）：道具不进参考图集合（官方规则），
		// 一致性只能靠文字 —— 把该镜引用到的器物卡原文逐字锚进来，模型才不会每镜自己发明一把刀。
		prompt = EnsurePropAnchors(prompt, plan, shot)
		// 场次切换的转场字幕（用户《仿真人10S分镜头指令》/《3D漫剧15秒分镜》：渐隐渐显 + "[地点・时间]" 字幕）。
		// 放在装配点的理由同道具锚定：这是**已知量**（场次一变，地点时间就在场景卡名字里），
		// 让 LLM 每镜自觉复述只会时灵时不灵；而且官方屏幕文字规则要求文字逐字进英文双引号。
		var prevShot *project.Shot
		for i := range plan.Shots {
			if plan.Shots[i].ID == shot.ID && i > 0 {
				prevShot = &plan.Shots[i-1]
				break
			}
		}
		prompt = EnsureTransitionCaption(prompt, plan, shot, prevShot)
		// 指纹记录「配置 seed」而非运行时随机值 —— 随机模式(=0)下每次时间戳不同,
		// 指纹必然失配,断点续跑的已渲镜头全部误判失效(从头重跑的第二根因)。
		// 渲染仍用上面的随机 seed 保多样;产物有效性以 提示词/参考图/分辨率/步数/档位 为准。
		//
		// 【单镜 seed 覆盖必须进指纹】旧实现传的是 rp.Seed(项目配置值),于是
		// ShotTuneDialog 的 seed 输入框(与「随机 seed」按钮)在提示词/时长/模式都没改
		// 时**完全不生效**:inputFP 与上一次逐字相同 → 走"产物有效,跳过(断点续跑)"
		// 分支 → 视频还是旧的那条,而前端弹「已按微调参数重渲」。判据见
		// fingerprintSeed(未显式换 seed 时与旧指纹串逐字一致,存量项目不会被判失效)。
		inputFP := renderFingerprint(prompt, refs, rp, mode, fingerprintSeed(rp, seedOverride, shot))
		outName := fmt.Sprintf("shot_%03d", shot.ID)

		// 断点续跑:同输入指纹 + 帧数对得上 + 文件存在 → 跳过
		wantFrames := shotFrames(shot)
		wantModel := renderModelTag(c.cfg.Get(), mode, engName)
		// 接缝生效时这一镜还依赖**上游那一镜的 latent**:记下渲它时上游的指纹,
		// 跳过前比一次(上游被重渲过 = 槽里的 latent 已经不是当初那份)。
		// 上游不在本次范围内(单镜重渲)时,manifest 里的值就是它当前的版本。
		//
		// seamHere 现在要过**准入判据**(甲3):同场同人才接、换场/换人/每 N 镜一律断开,
		// 见 seamBlockers。断开原因先攒着,只在**真的要渲这一镜**时播报 —— 断点续跑跳过的
		// 镜头不该重复刷屏(那些镜头本次并没有重新决定什么)。
		seamWant := rp.Seam && caps.Seam && shot.ID > 1
		seamHere := false
		seamNote := ""
		if seamWant {
			if prev, ok := shotByID(plan, shot.ID-1); ok {
				seamHere = seamAllowed(plan, shot, prev, rp.SeamResetEvery)
				seamNote = seamBlockNote(plan, shot, prev, rp.SeamResetEvery)
			} else {
				// 方案里找不到上一镜(被裁剪/手工改过镜号):没有可接的上游,同样如实说。
				seamNote = fmt.Sprintf("镜头 %d 与前镜不接缝:方案里找不到上一镜(镜 %d)",
					shot.ID, shot.ID-1)
			}
		}
		wantSeamFrom := ""
		if seamHere {
			wantSeamFrom = manifest[strconv.Itoa(shot.ID-1)].InputFP
		}
		if m, ok := manifest[strconv.Itoa(shot.ID)]; ok && shotArtifactValid(m, inputFP, wantFrames, wantModel, wantSeamFrom) {
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
		if seamNote != "" {
			// 断开**必须如实播报**(甲3-④):不许静默降级 —— 用户要能从日志里看出
			// "这一镜为什么没接",而不是只见"有的接了、有的没接"。
			c.emit(proj.ID, PhaseRender, shot.ID, "info", base, "%s", seamNote)
		}
		c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotRendering, engName, "", inputFP, "")

		var res *engine.Result
		var err error
		// 重试阶梯（2026-09-28 按官方 `references/fallback-policy.md` 补全）。
		//
		// 官方原话是三级之后才问人：
		//   ① 重试并把提示词**引用 Reference Anchors 块**加强（平台的锚定块本来就常驻正文，
		//      所以这一级等价于"原样重试"，但**换种子**——同一颗种子重出等于画同一张图）；
		//   ② **把镜头缩短到 ≤6 秒**，再出一次（实测这一级最有效：多数崩坏发生在长片段的尾巴上）；
		//   ③ 三次都不行 ⇒ **如实标注**（官方："do not silently mix corrected and uncorrected
		//      clips into assembly"）——平台的做法是标成 failed 并让成片阶段把缺口说出来。
		//
		// 第三级（切到另一个视频模型）在本机没有第二个视频引擎，故记为"不适用"而非跳过不提：
		// 平台的等价物是引擎里的加速档位，但它属于 run 级参数、不是单镜参数（见 Start 的 engineOverride）。
		shortened := false
		for attempt := 1; attempt <= 3; attempt++ {
			res, err = eng.Render(ctx, engine.Job{
				ProjectID: proj.ID, ShotID: shot.ID,
				Prompt: prompt, Mode: mode, RefImages: refs,
				Width: rp.Width, Height: rp.Height,
				Length: wantFrames, Seed: seed, Steps: rp.Steps,
				Accel:     rp.Accel,
				OutputDir: clipsDir, OutputName: outName,
				Timeout: shotTimeout,
				// Seam 用**准入判据的结果**(seamHere),不再是无条件 rp.Seam:
				// 换场/换人/到重置点时不把上一镜的 22 帧钉进来(见 seamBlockers)。
				Seam:     seamHere,
				PrevShot: shot.ID - 1,
				// 项目参数 render.cond_cache 必须真的说了算:交给引擎,未表态时由引擎
				// 回退全局设置(见 engine.Job.CondCache 的说明)。
				CondCache: rp.CondCache, // 三态:*bool 直接透传(nil = 未表态 → 引擎回落全局设置)
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
			// 第一级：超时类失败多为显存挤压导致的动态换页极慢(150s/步)甚至假死 ——
			// 先释放显存再原地重试一次,多数可恢复正常速度(2026-09-10)。
			// 用哨兵判断而非匹配「超时」二字 —— 调整文案不该悄悄改掉重试行为。
			if errors.Is(err, engine.ErrRenderTimeout) && attempt == 1 {
				c.emit(proj.ID, PhaseRender, shot.ID, "warn", base, "⚠ 镜头 %d 超时,释放显存后重试一次:%v", shot.ID, err)
				if caps.VRAM {
					_ = eng.FreeVRAM(ctx)
				}
				seed += 101 // 换一颗种子：同种子重出等于画同一张图（与资产侧同一条纪律）
				continue
			}
			// 第二级：缩短到 ≤6 秒再出（官方 fallback-policy 第 2 级）。
			if attempt == 2 && !shortened {
				if shortPrompt, newDur, ok := project.ShortenShotPrompt(shot.H3Prompt, shot.Duration, planShortenSeconds); ok {
					c.emit(proj.ID, PhaseRender, shot.ID, "warn", base,
						"⚠ 镜头 %d 第二次失败,按官方降级阶梯**缩短到 %d 秒**再出一次(原 %d 秒):%v",
						shot.ID, newDur, shot.Duration, err)
					prompt = shortPrompt
					wantFrames = newDur * 24 // 与 shotFrames 同口径（H3 侧 24fps），此处只有时长、没有 Shot 结构
					seed += 101
					shortened = true
					continue
				}
			}
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// 单镜失败不中止:记录后继续后续镜头,阶段末汇总。
			// 官方纪律：**不把修好的和没修的悄悄混进成片** —— 所以这里如实标 failed（成片阶段会把缺口说出来），
			// 不把 one-take/缩短过的版本当成"正常镜头"混过去。
			failed++
			msg := err.Error()
			if shortened {
				msg = "官方阶梯已试到第二级(换种子 → 缩短到 " + strconv.Itoa(planShortenSeconds) + " 秒)，仍失败：" + msg
			}
			failures = append(failures, fmt.Sprintf("镜%d:%v", shot.ID, err))
			c.setShotStatus(proj.ID, manifest, shot.ID, project.ShotFailed, engName, "", inputFP, msg)
			c.emit(proj.ID, PhaseRender, shot.ID, "error", base, "✕ 镜头 %d 渲染失败(继续下一镜):%s", shot.ID, msg)
			continue
		}
		rel, _ := filepath.Rel(c.store.Dir(proj.ID), res.VideoPath)
		// 乙6:引擎回传的**实际帧数**必须记账,不能只留计划值。
		// 清单长度(length)与"计划 / 实际"记录分别取(口径见 shotFrameRecord 的说明)。
		length, frameNote := shotFrameRecord(wantFrames, res)
		c.setShotStatusLen(proj.ID, manifest, shot.ID, project.ShotDone, engName, rel, inputFP, "", length, wantModel)
		if seamHere {
			// 记下"渲这一镜时上游是谁":上游日后被重渲(指纹变)时,这一镜必须跟着重渲,
			// 否则接缝处钉的还是旧 latent(画面跳变,而成片与阶段照常报成功)。
			m := manifest[strconv.Itoa(shot.ID)]
			m.SeamFrom = wantSeamFrom
			manifest[strconv.Itoa(shot.ID)] = m
		}
		if res.TaskID != "" {
			manifest[strconv.Itoa(shot.ID)] = mergeTask(manifest[strconv.Itoa(shot.ID)], res.TaskID)
		}
		done++
		doneMsg := fmt.Sprintf("✔ 镜头 %d 完成(%s)", shot.ID, rel)
		if frameNote != "" {
			doneMsg += " —— " + frameNote
		}
		c.emit(proj.ID, PhaseRender, shot.ID, "success", base+100/len(shots), "%s", doneMsg)
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

// fingerprintSeed 指纹里该写哪个 seed:唯一判据来源。
//
// 两条要求同时成立才正确:
//   - **换 seed 必须换指纹**:单镜微调(ShotTuneDialog 的 seed 输入框/「随机 seed」)
//     在提示词、时长、模式都没改时,旧实现让 inputFP 逐字不变 → 走跳过分支 →
//     视频还是旧的那条,而界面报「已按微调参数重渲」(典型"能调、看着生效、实际没做");
//   - **没换 seed 时指纹串必须与旧版逐字一致**:否则升级一版就让存量项目所有已渲镜头
//     判为失效(几十集全量重渲)。所以未显式覆盖时仍写 rp.Seed(旧行为),不写派生值。
//
// 随机种子绝不会流到这里:渲染链路用的 seed 由 ShotSeed 派生(确定性),随机种子只
// 出现在 RandomSeed() 那类显式抽卡动作里(见 stages_assets.go 的说明)。
func fingerprintSeed(rp project.RenderParams, seedOverride int64, shot project.Shot) int64 {
	if seedOverride <= 0 {
		return rp.Seed
	}
	return ShotSeed(rp.Seed, seedOverride, shot.ID, shot.Characters)
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
	if rp.Accel == "quality" || rp.Accel == "express" || rp.Accel == "refine" {
		accelTag = "accel" + rp.Accel
	}
	// qualityTag:采样环境版本(v1=2026-09-10 撤下 fp16_accumulation/ck-attention
	// 后的画质基线;画质敏感项再变更时递增,旧镜指纹失配自动重渲)。
	seamTag := fmt.Sprint("seam", rp.Seam)
	if rp.SeamResetEvery > 0 {
		// 接缝间隔改了 = 某一镜是不是"接着上一镜渲的"变了,产物必须重出。
		// **只在显式设过(>0)时才进指纹**:默认 0 时指纹串与旧版逐字一致,
		// 存量项目不会因为升级一版就被判全量失效(与 accelTag 同一口径)。
		seamTag += fmt.Sprintf("reset%d", rp.SeamResetEvery)
	}
	return project.FingerprintText(prompt, mode, fmt.Sprint(rp.Width, "x", rp.Height, "s", rp.Steps, "seed", seed, seamTag, accelTag, "q1"), strings.Join(files, ","))
}

// planShortenSeconds 官方降级阶梯第二级的时长上限（`references/fallback-policy.md`：
// "shorten the shot to ≤6s and split the dropped seconds into a new adjacent row"）。
const planShortenSeconds = 6

// shotFrames 镜头时长 → 帧数(H3 侧 24fps)。
//
// 收成一个函数是因为它有两个消费者:提交给引擎的 Job.Length,以及"这个已有产物还能
// 不能复用"的判据。以前只有前者,后者缺位 —— 于是"改了时长却跳过重渲"能一路走到成片。
func shotFrames(shot project.Shot) int { return shot.Duration * 24 }

// shotFrameRecord 本镜的**帧数记账**(乙6):写进清单的长度 + 给用户看的「计划 / 实际」记录。
//
// 【为什么这两个值要分开(而不是把实际值直接写进清单的 Length)】
// 引擎会在自己的口径上改写提交时长,两条路各有各的改写:
//   - 本地:提交长度必须对齐 17k+5 网格(计划 96 → 渲 107 帧),接缝生效还要扣掉被
//     Trim 裁掉的 22 帧 pinned 头;
//   - 云端:只吃整数秒且区间更窄,4 秒镜被**抬成 5 秒**(计划 96 → 120 帧)。
//
// 而清单的 Length 是**断点续跑判据**的一部分(shotArtifactValid 拿它跟
// shot.Duration*24 比)。若把"实际值"写进 Length:云端那条 4 秒镜(计划 96 / 实际 120)
// 每次续跑都会判"帧数对不上"→ 无限重渲;本地同理(96 → 107)。所以:
//   - **Length 继续记「本次是按多少帧渲的」**(= 计划值),判据与存量清单口径不变;
//   - **实际值**由引擎回传(engine.Result.ActualFrames / DurationAdjusted)带上来,
//     逐镜写进镜头记录(本条 note)——
//     用户必须看得见"你计划 4 秒、云端按 5 秒渲了",而不是让清单里的计划值冒充实际值。
//
// 注:要让**清单本身**也带上实际值,需要 project.ClipManifest 再开一列
// (该文件不属本批改动范围)。在那之前先保证"用户看得见",而不是静默记假账。
//
// 返回值:length = 写进清单的帧数(0 = 调用方不关心,保留原值);note = 逐镜记录文案
// (空 = 计划与实际一致,没什么要说)。
func shotFrameRecord(planned int, res *engine.Result) (length int, note string) {
	length = planned
	if res == nil || res.ActualFrames <= 0 {
		// 引擎没给实际帧数(旧引擎 / 异常路径):不编一个数字出来,清单照旧记计划值。
		return length, ""
	}
	if length <= 0 {
		// 调用方连计划帧数都没给:实际值是唯一的真话,记它。
		return res.ActualFrames, ""
	}
	if res.ActualFrames == planned && !res.DurationAdjusted {
		return length, "" // 计划与实际一致:不刷无意义的日志
	}
	note = fmt.Sprintf("计划 %d 帧(%.2fs)/ 实际 %d 帧(%.2fs)%s", planned, float64(planned)/24,
		res.ActualFrames, float64(res.ActualFrames)/24, durationAdjustedWhy(res.DurationAdjusted))
	return length, note
}

// durationAdjustedWhy 时长被改写时的说明尾注(未被改写则只做陈述)。
func durationAdjustedWhy(adjusted bool) string {
	if adjusted {
		return " —— 时长被引擎按自己的口径改写(本地对齐 17k+5 网格 / 云端只吃整数秒),成片时长以实际值为准"
	}
	return ""
}

// ---------- 镜间连续接缝(MotionContext)的**准入判据** ----------
//
// 【为什么要按需断开(甲3,漂移放大器)】旧实现是 `Seam: rp.Seam && caps.Seam,
// PrevShot: shot.ID-1` —— 只要上一镜的 latent 还在槽里就无条件接。而接缝做的事是
// 把**上一镜尾部的 22 帧**钉进本镜的头部作为条件:
//   - 换场时(上一镜在客栈、本镜在荒野),钉进去的 22 帧就是**另一个地方**的画面,
//     模型为了自洽只能把新场景往旧场景上靠(景物错位、色调被拽走);
//   - 换人/换角色组合时,钉进去的是别人的脸与服饰,本镜角色的身份被上一镜污染;
//   - 而且误差是**逐链累积**的:每接一镜就多复制一次上一镜的偏差,十几镜之后
//     人物/色调已经明显偏了,而每一处接缝单看都"很小"。
// 所以接缝只在"**同一场戏、同一批人**"的连续镜头之间才成立(那才是它要解决的
// "同场景相邻镜不连贯"),并且要能被"每 N 镜重建锚点"打断一次,把累积的漂移清零。
//
// 断开**不是降级**,是正确行为 —— 但它必须是**可见**的:日志里要写明"这一镜为什么
// 不接",否则用户只会看到"有的镜头接了、有的没接",无法判断是设计还是故障。
//
// 判据与原因由同一个函数算出(seamBlockers):不做"判一次、再猜一次原因"的两份逻辑,
// 否则改judge时文案会悄悄与事实脱节。

// seamBlockers 本镜**不能**与上一镜接缝的全部原因(空 = 可以接缝)。
//
// plan 只用于把场景 id 翻成场景名(报错文案给人看);nil 时退化为 id,不影响判据。
func seamBlockers(plan *project.Plan, shot, prev project.Shot, resetEvery int) []string {
	var out []string
	// ② 每 N 镜重建锚点(0/1 = 每场重置,由下面的同场景判据天然实现)。
	if resetEvery > 1 && shot.ID > 1 && (shot.ID-1)%resetEvery == 0 {
		out = append(out, fmt.Sprintf("每 %d 镜重建一次锚点(镜 %d 是新一轮首镜,累积漂移在此清零)",
			resetEvery, shot.ID))
	}
	// ① 同场景:换场不接(钉进去的 22 帧是另一个地方)。
	if shot.Scene != prev.Scene {
		out = append(out, fmt.Sprintf("与前镜不同场景(%s → %s)",
			sceneNameOf(plan, prev.Scene), sceneNameOf(plan, shot.Scene)))
	}
	// ⚠ 这里**特意没有**"角色集合必须有交集"这条判据。初版加过,实测后被撤掉:
	// 《求你们骂我》第 01 集 64 镜里相邻镜有共同出场角色的只有 40/63,而同场景的有 60/63 ——
	// 也就是说那条判据会白断 23 处接缝,而被断掉的恰恰是**正反打**(同一空间里 A 说话→B 说话):
	// 它们角色不同、但背景/光线/机位必须连续,接缝在这种镜头上价值最高。
	// 接缝要的是"空间连续",不是"人相同"。
	return out
}

// seamAllowed 本镜能否与上一镜接缝(判据的**唯一出口**,纯函数便于穷举)。
func seamAllowed(plan *project.Plan, shot, prev project.Shot, resetEvery int) bool {
	return len(seamBlockers(plan, shot, prev, resetEvery)) == 0
}

// seamBlockNote 断开时的**播报文案**(空 = 允许接缝,不播报)。
func seamBlockNote(plan *project.Plan, shot, prev project.Shot, resetEvery int) string {
	reasons := seamBlockers(plan, shot, prev, resetEvery)
	if len(reasons) == 0 {
		return ""
	}
	return fmt.Sprintf("镜头 %d 与前镜不接缝:%s —— 本镜按独立锚点重建(把上一镜的 22 帧钉进来会把误差逐链累积)",
		shot.ID, strings.Join(reasons, ";"))
}

// seamResetLabel 接缝重置口径的人话(0/1 = 每场重置;>1 = 每 N 镜重置)。
func seamResetLabel(every int) string {
	if every > 1 {
		return fmt.Sprintf("每 %d 镜重建一次锚点", every)
	}
	return "每场戏重建一次锚点"
}

// shotByID 按镜号取方案里的镜头(接缝判据要看上游那一镜的属性)。
func shotByID(plan *project.Plan, id int) (project.Shot, bool) {
	if plan == nil {
		return project.Shot{}, false
	}
	for _, s := range plan.Shots {
		if s.ID == id {
			return s, true
		}
	}
	return project.Shot{}, false
}

// sceneNameOf 场景 id → 人可读的名字(报错文案用;认不出就退回 id)。
func sceneNameOf(plan *project.Plan, id string) string {
	if plan != nil {
		for _, sc := range plan.Scenes {
			if sc.ID == id && strings.TrimSpace(sc.Name) != "" {
				return sc.Name
			}
		}
	}
	if strings.TrimSpace(id) == "" {
		return "未标注场景"
	}
	return id
}

// charactersIntersect 两个角色 id 集合是否有交集(空集合不算交集:
// 两个空镜之间没有共同主体可锚,宁可断开重锚,也不要拿上一镜的景钉住下一镜)。
//
// 按**造型家族**判(§4.2):技能侧把造型档做成独立卡之后,同一个人的两张卡 id 不同
// (`char_01v1` / `char_01v2`),按裸 id 比就会把"同一角色换了一身衣服"读成"换了人" ——
// 而那恰恰是要接缝的场合(同一个人的连续镜头)。
//
// ⚠ 当前 seamBlockers **刻意没有**"角色集合必须有交集"这条判据(理由见那里:接缝要的是
// 空间连续,不是人相同)。本函数保留为口径的唯一实现点:将来若要重新引入这条判据,
// 必须走它,而不是各写一份裸 id 比较。
func charactersIntersect(a, b []string) bool {
	for _, x := range a {
		if strings.TrimSpace(x) == "" {
			continue
		}
		for _, y := range b {
			if strings.TrimSpace(y) == "" {
				continue
			}
			if project.CharFamilyKey(x, "") == project.CharFamilyKey(y, "") {
				return true
			}
		}
	}
	return false
}

// shotArtifactValid 判定**已有产物能否复用**(断点续跑的核心判据),抽成纯函数以便穷举。
//
// 2026-09-24 修(审计 P0-8):判据原先是"状态 Done + 输入指纹一致",而指纹串里**不含时长**
// (往指纹里加字段会让存量项目全部重渲,不能那么修)。于是用户把 4 秒改成 6 秒(为了装下
// 被截断的台词)、点「保存并重渲本镜」,指纹不变 → 走跳过分支 → 旧片原样保留,界面还报成功。
//
// Length 的兼容语义:`Length == 0` 表示这份清单是**本字段引入之前**写的,判据不推翻
// 指纹结论 —— 否则升级一版就会让所有存量项目的已渲镜头集体重渲(几十集的代价),
// 而它并不比"照旧"更正确。渲染过一次之后 Length 就有值了,从此改动必被抓住。
//
// wantSeamFrom:本镜若吃上游接缝(wantSeamFrom 非空 = 接缝生效,值为上游当前的 InputFP)。
// 上游被重渲过 → 槽里的 latent 换了 → 本镜的产物作废(否则接缝处画面跳变而无人知晓)。
// 老清单没记这一列(SeamFrom=="")时不推翻指纹结论 —— 与 Length/Model 同一条兼容口径。
func shotArtifactValid(m project.ClipManifest, inputFP string, wantFrames int, wantModel, wantSeamFrom string) bool {
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
	if wantSeamFrom != "" && m.SeamFrom != "" && m.SeamFrom != wantSeamFrom {
		return false
	}
	return true
}

// renderModelTag 本次渲染的**模型身份**:底模 + 加速 LoRA(本地)或云端模型名。
//
// 为什么必须有它:用户换了底模(2026-09-24 换 Minimax-h3_Singularity)之后,
// 断点续跑会拿旧模型的产物当"已完成"跳过 —— 一集里两种画风混着,而指纹看不出来。
//
// 除了底模/LoRA 名字,还有几项**同样决定画面却不体现在文件名里**的全局设置
// (云端分辨率、本地 VideoVAE/CLIP、SageAttention 开关):改了它们再点重渲,
// 旧实现会一路"产物有效,跳过",用户以为新设置生效、成片还是旧设置出的画面。
// 用 modelRuntimeTag 收口,并且**只在偏离内置默认值时才追加** —— 保持未改过设置
// 的机器上标签串与旧版逐字相同(否则升级一版就把存量项目的已渲镜头全部判失效)。
func renderModelTag(g config.Settings, mode, engName string) string {
	base := ""
	if engName == "api" {
		base = "api:" + g.MiniMax.Model
	} else if mode == "ref2va" {
		base = g.Comfy.UNetRef2VA + "+" + g.Comfy.TurboLoRARef2VA
	} else {
		base = g.Comfy.UNetFL2VA + "+" + g.Comfy.TurboLoRAFL2VA
	}
	return base + modelRuntimeTag(g, engName)
}

// modelRuntimeTag 运行期"决定画面"的设置(默认值时不产生任何标记,见 renderModelTag)。
func modelRuntimeTag(g config.Settings, engName string) string {
	def := config.Default()
	var tags []string
	if engName == "api" {
		// 云端分辨率(768P/2K/480P 是真正的出像素规格,设置页可改)
		if res := strings.TrimSpace(g.MiniMax.Resolution); res != "" && res != def.MiniMax.Resolution {
			tags = append(tags, "res"+res)
		}
	} else {
		if v := strings.TrimSpace(g.Comfy.VideoVAE); v != "" && v != def.Comfy.VideoVAE {
			tags = append(tags, "vae:"+v)
		}
		if c := strings.TrimSpace(g.Comfy.CLIP); c != "" && c != def.Comfy.CLIP {
			tags = append(tags, "clip:"+c)
		}
		// SageAttention:nil(自动)与 true 是同一个状态(见 handlers_comfy.go 的判据),
		// 只有显式关掉才改画面。
		if g.Comfy.SageAttention != nil && !*g.Comfy.SageAttention {
			tags = append(tags, "sageoff")
		}
	}
	if len(tags) == 0 {
		return ""
	}
	return "|" + strings.Join(tags, ",")
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
