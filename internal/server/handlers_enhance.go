// handlers_enhance.go 镜头的两项增强能力入口。
//
//	局部重绘(Mask Fix) —— 只重生成跟踪到的目标区域,其余画面逐帧保留;
//	音频驱动(Audio Drive) —— 用定稿配音驱动重渲,人物口型与节奏跟随音频。
//
// 两者的引擎实现分别在 comfyengine/maskgraph.go 与 render 的音频条件分支里,
// 本文件只负责"取项目数据 → 组装入参 → 调用 → 回写产物"。
package server

import (
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

// shotEnhanceCtx 两个端点共用的取数:定位镜头、校验产物、准备源视频与参考图。
type shotEnhanceCtx struct {
	proj     *project.Project
	plan     *project.Plan // 分镜方案(渲染期提示词装配需要:身份锚定 + 画风硬约束)
	shot     *project.Shot
	src      string // 源镜头本地绝对路径
	outName  string
	outPath  string
	timeout  time.Duration
	engineCE *comfyengine.Engine
}

func (s *Server) prepareShotEnhance(w http.ResponseWriter, r *http.Request, suffix string) (*shotEnhanceCtx, bool) {
	id := r.PathValue("id")
	shotID, err := strconv.Atoi(r.PathValue("shotID"))
	if err != nil {
		writeErr(w, 400, fmt.Errorf("镜头编号无效"))
		return nil, false
	}
	proj, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return nil, false
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚无分镜方案"))
		return nil, false
	}
	manifest, err := s.store.LoadManifest(id)
	if err != nil {
		writeErr(w, 404, err)
		return nil, false
	}
	var shot *project.Shot
	for i := range plan.Shots {
		if plan.Shots[i].ID == shotID {
			shot = &plan.Shots[i]
			break
		}
	}
	if shot == nil {
		writeErr(w, 404, fmt.Errorf("镜头 %d 不存在", shotID))
		return nil, false
	}
	m, ok := manifest[strconv.Itoa(shotID)]
	if !ok || m.Status != project.ShotDone || m.File == "" {
		writeErr(w, 400, fmt.Errorf("镜头 %d 尚无已完成产物,请先渲染", shotID))
		return nil, false
	}
	ce, err := s.comfyEngine()
	if err != nil {
		writeErr(w, 503, err)
		return nil, false
	}
	outName := fmt.Sprintf("shot_%03d_%s", shotID, suffix)
	return &shotEnhanceCtx{
		proj:     proj,
		plan:     plan,
		shot:     shot,
		src:      filepath.Join(s.store.Dir(id), m.File),
		outName:  outName,
		outPath:  filepath.Join(s.store.Dir(id), "clips", outName+".mp4"),
		timeout:  enhanceTimeout(time.Duration(s.cfg.Get().Render.ShotTimeout) * time.Minute),
		engineCE: ce,
	}, true
}

// enhanceChainGrace 两项增强(局部重绘/音频驱动)在单镜渲染预算之外**额外的**链路开销。
//
// 这两条链比一次整镜渲染更长:要先把源镜头上传到 ComfyUI、跑一遍 SAM3 逐帧跟踪
// (整镜渲染没有这一步)、渲完还要贴回原画面再下载;而它们此前与整镜渲染共用同一份
// `render.shot_timeout` —— 一次合法但偏慢的重绘会在 H3 采样完成、快贴回时被杀在
// 超时上,GPU 时间已经花掉,重跑等于白烧一倍。
//
// 给一段明确、有界的宽限,而不是让两条链共用一个数(定妆图那条链当初也是因为
// "共用一个 45 分钟"才另立了 comfyengine.defaultImageTimeout)。
const enhanceChainGrace = 15 * time.Minute

// enhanceTimeout 增强链路的超时预算 = 单镜渲染超时 + 链路开销宽限。
//
// shotTimeout <= 0 表示设置页那项没配(或配成 0):此时交回 0,让引擎用自身的兜底值
// (comfyengine.defaultShotTimeout)—— 直接返回 0+15 分钟反而比引擎兜底更短。
func enhanceTimeout(shotTimeout time.Duration) time.Duration {
	if shotTimeout <= 0 {
		return 0
	}
	return shotTimeout + enhanceChainGrace
}

// maskFixFrames 局部重绘要提交给 H3 的帧数 = **源片的实际帧数**。
//
// 为什么不能用计划时长×24(审计:局部重绘的帧数用「计划时长」):源片是本地引擎
// 渲出来的 AlignLength(计划帧数)(4 秒镜 96 → 实际 107 帧),而 ⑦ 的
// MVEx_SubjectUncrop 拿的是源片的**全部帧** —— 传 96 会让 H3 只出 96 帧去贴
// 107 帧的底片:要么节点尺寸/帧数不匹配直接抛错,要么尾部 11 帧保留未修的崩坏内容、
// 成片还短 0.46 秒。2× 放大那条链路早就是 ffprobe 实测源片(见 handleShotUpscale),
// 这里与它同一套。
//
// 帧数向下取到 H3 的 17k+5 网格:本地引擎的产物本就在网格上(向下取是恒等变换),
// 只有源片来自云端(帧数不在网格上)时才会回收几帧 —— 那时宁可靠 H3 能接受的
// 长度去贴回(尾部几帧不修),也不要让节点因为非法 length 直接失败。
func maskFixFrames(info *ff.ProbeInfo) (int, error) {
	if info == nil || info.Duration <= 0 {
		return 0, fmt.Errorf("源镜头时长异常,无法确定帧数:局部重绘要与源片逐帧对齐")
	}
	fps := info.FPS
	if fps <= 0 {
		fps = 24 // 极少数容器不报帧率,按 H3 的产出口径兜底(与 2× 放大一致)
	}
	n := int(math.Round(info.Duration * fps))
	if n <= 1 {
		return 0, fmt.Errorf("源镜头时长异常(%.2fs),无法确定帧数", info.Duration)
	}
	return engine.AlignLengthDown(n), nil
}

// enhanceJob 两项增强能力共同的 Job 组装(差异只有 Audios)。
//
// 抽成纯函数是为了让"帧数到底传了什么"可被单测断言 —— 这里前后漏过两次:
// 音频驱动的 Job **完全没有 Length**(引擎按 AlignLength(0)=5 帧建 latent 与条件编码,
// 接口却返回 ok),局部重绘传的是计划帧数而不是源片帧数(见 maskFixFrames)。
func enhanceJob(ctx *shotEnhanceCtx, rp project.RenderParams, prompt string, refs []engine.RefImage, frames int, audios []string) engine.Job {
	return engine.Job{
		ProjectID: ctx.proj.ID, ShotID: ctx.shot.ID,
		Prompt: prompt, Mode: "ref2va", RefImages: refs,
		Audios: audios,
		Length: frames,
		Width:  rp.Width, Height: rp.Height, Seed: rp.Seed,
		Steps: rp.Steps, Accel: rp.Accel,
		OutputDir: filepath.Dir(ctx.outPath), OutputName: ctx.outName, Timeout: ctx.timeout,
	}
}

// comfyEngine 取本地 ComfyUI 引擎(两项增强能力都依赖它)。
func (s *Server) comfyEngine() (*comfyengine.Engine, error) {
	eng, err := s.reg.Get("comfy")
	if err != nil {
		return nil, fmt.Errorf("未注册本地引擎")
	}
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		return nil, fmt.Errorf("引擎类型不支持图像/视频生成")
	}
	return ce, nil
}

// relFile 把绝对路径转成项目内相对路径(与 manifest 的约定一致)。
func (s *Server) relFile(projectID, abs string) string {
	rel, err := filepath.Rel(s.store.Dir(projectID), abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// handleShotMaskFix POST /api/projects/{id}/shot/{shotID}/mask-fix
// body { prompt?, target, object_index? } —— 局部重绘:只重生成跟踪到的目标区域。
//
// target 是**必填**的英文名词短语(SAM3 是开放词表跟踪器,不给目标它就无法判断
// 该重绘哪一块;引擎侧同样会拦)。缺它时这里就返回 400 并说清楚要填什么,
// 不要让请求跑到 ComfyUI 才抛原始报文。
func (s *Server) handleShotMaskFix(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prompt      string `json:"prompt"`
		Target      string `json:"target"`
		ObjectIndex string `json:"object_index"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(in.Target) == "" {
		writeErr(w, 400, fmt.Errorf("%s", comfyengine.MaskTargetHint))
		return
	}
	ctx, ok := s.prepareShotEnhance(w, r, "maskfix")
	if !ok {
		return
	}
	// 事前体检:节点(SAM3/MaskVidExperiments)+ 权重(sam3 checkpoint)。这条链路
	// 要上传源视频、跑十几分钟,缺件必须在点下去之前说清,而不是跑到一半抛原始报文。
	//
	// 先确保引擎可用再体检(审计 M25):MaskAssets 在引擎**不可达**时按设计返回空
	// ("不把连不上误判成缺文件"),冷启动时体检因此被静默跳过 —— 用户先白等 1-2 分钟
	// 启动,再拿到 ComfyUI 的原始 node_errors。放大那条链路早就这么做了,这里补齐。
	if err := ctx.engineCE.Available(r.Context()); err != nil {
		writeErr(w, 503, fmt.Errorf("局部重绘需要本地 ComfyUI:%w", err))
		return
	}
	if miss := ctx.engineCE.MaskAssets(r.Context()); len(miss) > 0 {
		writeErr(w, 409, fmt.Errorf("局部重绘前置条件不满足:\n- %s", strings.Join(miss, "\n- ")))
		return
	}
	rp := ctx.proj.Config.Params.Render
	// 默认复用该镜原提示词:只把崩坏部分的描述改对,其余保持不变,重绘的局部才接得上
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		// 与主渲染同一条提示词(身份锚定 + 画风硬约束 + [Chinese] 语言标记)。
		// 只拿 shot.H3Prompt 的话,重绘出来的一块脸会没有哑光/动画约束 —— 局部重绘
		// 恰恰是在脸上动刀,少了这条最容易看出"补丁"。
		prompt = pipeline.ShotRenderPrompt(ctx.proj, ctx.plan, ctx.shot)
	}
	// 音频驱动必须带**官方全参考模式的音频标记**(<Audio N> 定义 + [audio reuse] 任务前缀
	// + fully_copy 保留标记):不写标记,H3 只把音频当音色参考,口型会与配音脱节
	// (ComfyUI 官方文档的社区实测,见 pipeline/audio_ref.go 的引用)。
	prompt = pipeline.EnsureAudioDriveMarkers(prompt)
	assets, _ := s.store.LoadAssets(ctx.proj.ID)
	refs, roles, _ := pipeline.RefPlan(s.store.Dir(ctx.proj.ID), *ctx.shot, assets,
		ctx.proj.Config.Params.Assets.CompactRefsOn())
	// 参考图权威清单(与主渲染同一份):编号错位会让拼合图被当成场景图
	// —— 2026-09-25 角色漂移根因,见 pipeline/refmanifest.go。
	prompt = pipeline.EnsureRefManifest(prompt, *ctx.shot, roles)

	s.opLog("✨ 局部重绘:%s 镜 %d 开始(目标:%s)", ctx.proj.ID, ctx.shot.ID, strings.TrimSpace(in.Target))
	// 帧数取源片的**实际帧数**(不是计划时长×24):贴回原画面对帧数必须逐帧一致,
	// 见 maskFixFrames。探测失败就明确报错 —— 蒙一个长度只会让 ComfyUI 侧的
	// SAM3 跟踪 + H3 重绘(十几分钟)跑完才在贴回节点上失败。
	info, err := s.ff.Probe(ctx.src)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("读取源镜头信息失败: %w", err))
		return
	}
	frames, err := maskFixFrames(info)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	job := enhanceJob(ctx, rp, prompt, refs, frames, nil)
	if _, err := ctx.engineCE.MaskFix(r.Context(), ctx.src, comfyengine.MaskOptions{
		Prompt:      prompt,
		Target:      strings.TrimSpace(in.Target),
		ObjectIndex: in.ObjectIndex,
	}, job, rp.Width, rp.Height, frames, ctx.outPath); err != nil {
		s.opLog("✕ 局部重绘失败:%v", err)
		writeErr(w, 500, fmt.Errorf("局部重绘失败:%w", err))
		return
	}

	// 产物作为**独立文件**保留,不覆盖原镜头产物 —— 由用户比对后再决定是否采用
	rel := s.relFile(ctx.proj.ID, ctx.outPath)
	s.opLog("✔ 局部重绘完成:%s 镜 %d → %s", ctx.proj.ID, ctx.shot.ID, filepath.Base(ctx.outPath))
	// 重活做完把显存交还(队列还有活/管线在跑时内部会自己拦下,见 ReleaseVRAMIfIdle)
	s.pipe.ReleaseVRAMIfIdle("局部重绘完成")
	writeJSON(w, 200, map[string]any{
		"ok": true, "file": rel,
		"url":  "/api/projects/" + ctx.proj.ID + "/files/" + rel,
		"note": "重绘结果作为独立文件产出,原镜头未改动;比对满意后可在故事板中替换",
	})
}

// handleShotUpscale POST /api/projects/{id}/shot/{shotID}/upscale
// body { type?, model?, ltx_vae? } —— 2× 放大后处理,保留原音轨。
//
// 与局部重绘/音频驱动的区别:那两项是**重新生成**画面,这一项是**放大已完成的画面**
// —— 引擎层(comfyengine/upscalegraph.go)早已就绪并有契约测试,缺的是这一层入口。
//
// 帧数与 fps **从源产物推**,不用项目当前参数:放大是逐帧后处理,帧数必须与原片一致,
// 拿配置里的 length 会让放大后的时长与原镜对不上(且 8n+1 补齐与裁回都按这个数走)。
//
// 三个入参都可省:省了就用 engine.DefaultUpscaleOptions() 的默认值(含 ltx 链路要用的
// VAE 文件名)—— 前端因此不必让用户手打文件名,只填自己想覆盖的那一项即可。
func (s *Server) handleShotUpscale(w http.ResponseWriter, r *http.Request) {
	// 用指针而不是 string:必须区分「**没传**(用默认值)」与「**传了空白**(非法,该拦)」。
	// 用 string + TrimSpace(...) != "" 判断会把"传了空白"悄悄当成"没传"从而填回默认值,
	// 那条"模型名为空"的校验就永远不会触发 —— 契约测试正是这样抓出来的。
	var in struct {
		Type   *string `json:"type"`
		Model  *string `json:"model"`
		LTXVAE *string `json:"ltx_vae"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}

	// 先定"方式",再按方式取默认权重:两者必须成套 —— ModelName 的含义随 Type 而变
	// (ltx 去 latent_upscale_models/ 找,esrgan 去 upscale_models/ 找),
	// 详见 engine.UpscaleOptionsFor 的说明。
	kind := engine.DefaultUpscaleOptions().Type
	if in.Type != nil {
		kind = strings.TrimSpace(*in.Type)
	}
	// 参数体检放在调引擎之前:ltx/esrgan 对依赖的要求不同,错在这里能给出人话,
	// 到引擎里只会变成一句看不懂的节点缺失
	if kind != "ltx" && kind != "esrgan" {
		writeErr(w, 400, fmt.Errorf("放大方式只支持 ltx 或 esrgan,收到「%s」", kind))
		return
	}
	opt := engine.UpscaleOptionsFor(kind)
	if in.Model != nil {
		opt.ModelName = strings.TrimSpace(*in.Model)
	}
	if in.LTXVAE != nil {
		opt.LTXVAE = strings.TrimSpace(*in.LTXVAE)
	}
	if strings.TrimSpace(opt.ModelName) == "" {
		writeErr(w, 400, fmt.Errorf("请指定放大模型文件名(model)"))
		return
	}
	if opt.Type == "ltx" && strings.TrimSpace(opt.LTXVAE) == "" {
		writeErr(w, 400, fmt.Errorf("LTX 隐空间放大需要 LTX 的 VAE 文件名(ltx_vae),留空即用默认的 %s", engine.DefaultLTXUpscaleVAE))
		return
	}

	ctx, ok := s.prepareShotEnhance(w, r, "upscale2x")
	if !ok {
		return
	}

	// 先确认引擎可用再体检权重:UpscaleAssets 在引擎不可达时按设计返回空
	// (不把"连不上"误判成"缺文件"),若跳过这一步,冷启动时体检会被静默跳过,
	// 用户又要等 ComfyUI 起来、跑进节点才拿到那句看不懂的报错。
	// 这里调 Available 不会多花时间 —— Upscale 内部同样要调它(含按 AutoStart 代管启动)。
	if err := ctx.engineCE.Available(r.Context()); err != nil {
		writeErr(w, 503, fmt.Errorf("2× 放大需要本地 ComfyUI:%w", err))
		return
	}
	// 权重体检:放大是长任务,而缺权重只会在 ComfyUI 执行到那个节点时才暴露。
	// 提前到提交之前,并把"缺哪个文件、该放进哪个目录"直接写进报错。
	if miss := ctx.engineCE.UpscaleAssets(r.Context(), opt); len(miss) > 0 {
		writeErr(w, 400, fmt.Errorf("无法执行 %s 放大:%s", opt.Type, strings.Join(miss, "; ")))
		return
	}

	info, err := s.ff.Probe(ctx.src)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("读取源镜头信息失败:%w", err))
		return
	}
	fps := info.FPS
	if fps <= 0 {
		fps = 24 // 极少数容器不报帧率,按 H3 的产出口径兜底
	}
	length := int(math.Round(info.Duration * fps))
	if length <= 1 {
		writeErr(w, 400, fmt.Errorf("源镜头时长异常(%.2fs),无法确定帧数", info.Duration))
		return
	}

	s.opLog("🔍 2× 放大:%s 镜 %d 开始(%s / %d 帧 @ %.2ffps)", ctx.proj.ID, ctx.shot.ID, opt.Type, length, fps)
	if _, err := ctx.engineCE.Upscale(r.Context(), ctx.src, opt, length, fps,
		ctx.outName, ctx.outPath, ctx.timeout); err != nil {
		s.opLog("✕ 2× 放大失败:%v", err)
		writeErr(w, 500, fmt.Errorf("2× 放大失败:%w", err))
		return
	}

	rel := s.relFile(ctx.proj.ID, ctx.outPath)
	s.opLog("✔ 2× 放大完成:%s 镜 %d → %s", ctx.proj.ID, ctx.shot.ID, filepath.Base(ctx.outPath))
	s.pipe.ReleaseVRAMIfIdle("2× 放大完成")
	writeJSON(w, 200, map[string]any{
		"ok": true, "file": rel,
		"url":  "/api/projects/" + ctx.proj.ID + "/files/" + rel,
		"note": "放大结果作为独立文件产出,原镜头未改动;音轨沿用源片",
	})
}

// handleShotAudioDrive POST /api/projects/{id}/shot/{shotID}/audio-drive
// body { audio_path } —— 音频驱动重渲:人物口型与动作节奏跟随给定音频。
func (s *Server) handleShotAudioDrive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AudioPath string `json:"audio_path"`
		Prompt    string `json:"prompt"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(in.AudioPath) == "" {
		writeErr(w, 400, fmt.Errorf("请提供配音文件路径(audio_path)"))
		return
	}
	if _, err := filepath.Abs(in.AudioPath); err != nil {
		writeErr(w, 400, fmt.Errorf("配音路径无效"))
		return
	}
	ctx, ok := s.prepareShotEnhance(w, r, "audiodrive")
	if !ok {
		return
	}
	rp := ctx.proj.Config.Params.Render
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		// 与主渲染同一条提示词(身份锚定 + 画风硬约束 + [Chinese] 语言标记),
		// 否则重渲出来的片段又会出现"没有哑光约束"的油腻脸与听不懂的鸟语(2026-09-12/09-24)
		prompt = pipeline.ShotRenderPrompt(ctx.proj, ctx.plan, ctx.shot)
	}
	// 音频驱动必须带**官方全参考模式的音频标记**(<Audio N> 定义 + [audio reuse] 任务前缀
	// + fully_copy 保留标记):不写标记,H3 只把音频当音色参考,口型会与配音脱节
	// (ComfyUI 官方文档的社区实测,见 pipeline/audio_ref.go 的引用)。
	prompt = pipeline.EnsureAudioDriveMarkers(prompt)
	assets, _ := s.store.LoadAssets(ctx.proj.ID)
	refs, roles, _ := pipeline.RefPlan(s.store.Dir(ctx.proj.ID), *ctx.shot, assets,
		ctx.proj.Config.Params.Assets.CompactRefsOn())
	// 参考图权威清单(与主渲染同一份),理由见上一个调用点。
	prompt = pipeline.EnsureRefManifest(prompt, *ctx.shot, roles)

	s.opLog("🎙 音频驱动:%s 镜 %d 开始", ctx.proj.ID, ctx.shot.ID)
	// 音频走 Job.Audios → H3 Ref2V 的 ref_audios 条件(见 comfyengine 的音频参考实现)
	// Length 必须显式给:漏了它,引擎侧 AlignLength(0) 会把它兜成 5 帧(0.21 秒),
	// 于是产出一支 5 帧的片子而接口照样报成功。这一路没有贴回,长度就取**本镜的
	// 计划帧数**(引擎再按 17k+5 对齐)—— 与局部重绘取源片帧数是两种不同的口径,
	// 但都经由 enhanceJob 一处组装,免得再漏。
	if _, err := ctx.engineCE.Render(r.Context(),
		enhanceJob(ctx, rp, prompt, refs, ctx.shot.Duration*24, []string{in.AudioPath})); err != nil {
		s.opLog("✕ 音频驱动失败:%v", err)
		writeErr(w, 500, fmt.Errorf("音频驱动失败:%w", err))
		return
	}
	rel := s.relFile(ctx.proj.ID, ctx.outPath)
	s.opLog("✔ 音频驱动完成:%s 镜 %d → %s", ctx.proj.ID, ctx.shot.ID, filepath.Base(ctx.outPath))
	s.pipe.ReleaseVRAMIfIdle("音频驱动完成")
	writeJSON(w, 200, map[string]any{
		"ok": true, "file": rel,
		"url":  "/api/projects/" + ctx.proj.ID + "/files/" + rel,
		"note": "音频驱动结果作为独立文件产出,原镜头未改动",
	})
}
