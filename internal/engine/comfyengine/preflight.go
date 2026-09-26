package comfyengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// 节点类名常量:集中一处,避免同一字符串散落在 preflight/admin/engine 各处以
// 致升级改名时改漏。前两个同时用于管理页引擎栈显示;其余记录在此备查与后续
// 扩展(条件缓存/接缝的自动降级逻辑已分别在 engine.go 与 seamPrevExists 内)。
const (
	classCondSave   = "MiniMaxH3CondSave"
	classCondLoad   = "MiniMaxH3CondLoad"
	classMotionCtx  = "MiniMaxH3MotionContext"
	classMotionLoad = "MiniMaxH3MotionContextLoadLatent"
	classMotionSave = "MiniMaxH3MotionContextSaveLatent"
	classMotionTrim = "MiniMaxH3MotionContextTrim"

	// 渲染真正会依赖的 H3 插件节点。标准节点(UNETLoader/KSamplerSelect/
	// VAEDecode 等)是 ComfyUI 内置,不会缺,故不列入;这里只盯插件节点 ——
	// 它们随 ComfyUI 或插件升级被改名/移除时,正是「渲染跑到一半才失败」的
	// 典型来源。
	classRef2V      = "MiniMaxH3ReferenceToVideo"
	classI2V        = "MiniMaxH3ImageToVideo"
	classSigmaShift = "MiniMaxH3SigmaShift"
	classEmptyLatAV = "EmptyMiniMaxH3LatentAV"
)

// engineReachable 体检前的引擎可用性前置:**不可达时先按设置把它拉起来**。
//
//	旧实现是「不可达就返回空(无缺失)」,理由是"调用方在此之前已确认过引擎可用" ——
//	但渲染阶段并没有:stages_render 直接调 Preflight,其后只调 ClearQueue/FreeVRAM
//	(都不启动引擎),本阶段第一次 Available 要等第一镜的 Render。而引擎被空闲看门狗
//	停掉(默认 10 分钟)后是常态,于是冷启动下「缺插件节点 / 缺 SAM3 权重 / 缺放大
//	权重」的体检被静默跳过,失败退回"跑到一半才由 ComfyUI 抛原始报文" ——
//	正是这几处注释声称已经消灭的体验(2026-09-26 渲染管线审查)。
//
//	现在先确保可用:调的是渲染/放大/局部重绘本身都会调的那个 Available(含按
//	AutoStart 代管启动;已存活时只是一次探测,不额外花时间)。**仍然不可达时依旧返回
//	nil**,不把"连不上"伪造成"缺文件/缺节点":调用方随后自己会拿到精确的引擎错误,
//	这条边界由 TestUpscaleAssetsEngineDown 钉住。
func (e *Engine) engineReachable(ctx context.Context) bool {
	if e.client().Alive(ctx) {
		return true
	}
	return e.Available(ctx) == nil
}

// PreflightOptions 本次渲染的取镜范围。
type PreflightOptions struct {
	Modes []string // 本次会渲染到的 mode(如 ref2va / t2va),决定检查哪条生成链
}

// Preflight 渲染前体检:确认本次渲染必然用到的 H3 根节点在 ComfyUI 侧存在。
//
// 此前只有「打开管理页」才做节点检测,而且只查 2 个节点;渲染要跑到一半才失败,
// 用户看到的是 ComfyUI 转述的原始 node_errors 报文,分不清是缺插件还是缺模型。
// 这里把检查提前到渲染开始,并给出可操作的中文提示。
//
// 注意本函数只管**节点**那一半;放大链路的**权重**缺失由 UpscaleAssets 负责 ——
// 两者合起来才真的分得清「缺插件」与「缺模型」。
//
// 只覆盖 graph 根节点(生成链入口 + sigma 偏移):它们缺失必然全镜失败,提前
// 阻断是净收益;可选能力(条件缓存/镜间接缝)刻意不列入,以免误判挡住生产。
//
// 引擎不可达时返回空:不把「暂时连不上」误判成「节点缺失」(调用方随后会拿到
// 精确的引擎错误)。但**先尝试把它拉起来** —— 冷启动下"没体检"不等于"没缺失",
// 见 engineReachable。
func (e *Engine) Preflight(ctx context.Context, opt PreflightOptions) []string {
	if !e.engineReachable(ctx) {
		return nil
	}
	required := map[string]string{}
	add := func(class, label string) { required[class] = label }

	needRef, needI2V := false, false
	for _, m := range opt.Modes {
		if m == "ref2va" || m == "ref2v" {
			needRef = true
		} else {
			needI2V = true
		}
	}
	if needRef {
		add(classRef2V, "参考生视频(Ref2V)")
	}
	if needI2V {
		add(classI2V, "图生视频(T2V/I2V/首末帧)")
	}
	if needRef || needI2V {
		add(classSigmaShift, "双流 sigma 偏移")
	}
	// 条件缓存与镜间接缝不纳入阻断:条件缓存在引擎内已有自动降级(缺
	// MiniMaxH3CondSave 即跳过缓存),接缝缺失时 ComfyUI 会回明确的 node_errors
	// 便于定位 —— 把它们也算作致命项,一旦误判就会挡住整个生产,得不偿失。

	// map 遍历无序 → 排序保证提示内容稳定,便于用户比对与报障
	classes := make([]string, 0, len(required))
	for c := range required {
		classes = append(classes, c)
	}
	sort.Strings(classes)

	var missing []string
	for _, c := range classes {
		if !e.client().HasNode(ctx, c) {
			missing = append(missing, fmt.Sprintf("%s(节点 %s)", required[c], c))
		}
	}
	return missing
}

// ---------- 放大后处理的权重体检 ----------

// upscaleAsset 放大链路对 ComfyUI 的一项**权重**依赖:某节点的某个 combo 输入
// 必须能列出这个文件名,否则该节点在真正执行时才会报输入不合法。
type upscaleAsset struct {
	class string // 节点类名
	field string // combo 输入名
	file  string // 期望的文件名
	label string // 给用户看的中文名
	where string // 缺失时该把文件放进哪个模型目录(相对 ComfyUI 的 models/)
}

// UpscaleAssets 放大后处理的权重体检(与 Preflight 的**节点**体检互补)。
//
// 起因是实测踩到的:ltx 隐空间放大链路在代码里早已实现、也有图契约测试,但本机
// ComfyUI 只装了 ltx-2-spatial-upscaler 权重、没装配套的 LTX 视频 VAE —— 于是
// 这条路**一上手就失败**,而报错要等 ComfyUI 真正执行到那个节点才以原始报文
// 的形式抛回来,用户分不清是缺插件、缺模型还是参数写错。Preflight 的注释里写着
// 要帮用户分清「缺插件还是缺模型」,但当时只实现了节点那一半。
//
// 引擎不可达时返回空:与 Preflight 同理 —— 连不上不等于资产缺失,那是 Available
// 的职责。但同样**先尝试拉起**(见 engineReachable):放大链路虽已在上游调过
// Available,其它调用方(如未来新增的入口)不该再踩"冷启动静默跳过体检"这个坑。
func (e *Engine) UpscaleAssets(ctx context.Context, opt engine.UpscaleOptions) []string {
	if !e.engineReachable(ctx) {
		return nil
	}

	// 两条链路依赖的权重不同,分开列出 —— 这也是它们前置条件不同的根源
	var need []upscaleAsset
	if opt.Type == "ltx" {
		need = []upscaleAsset{
			{class: "VAELoader", field: "vae_name", file: opt.LTXVAE, label: "LTX 视频 VAE", where: "vae"},
			{class: "LatentUpscaleModelLoader", field: "model_name", file: opt.ModelName, label: "隐空间放大权重", where: "latent_upscale_models"},
		}
	} else {
		need = []upscaleAsset{
			{class: "UpscaleModelLoader", field: "model_name", file: opt.ModelName, label: "帧级超分权重", where: "upscale_models"},
		}
	}

	var missing []string
	for _, a := range need {
		if strings.TrimSpace(a.file) == "" {
			missing = append(missing, fmt.Sprintf("%s:未指定文件名", a.label))
			continue
		}
		list, ok := e.client().ComboList(ctx, a.class, a.field)
		if !ok {
			// 取不到可选值 = 节点不存在或该输入已改名。放大链路的节点都来自
			// ComfyUI 内置(nodes_hunyuan / nodes_lt_upsampler),通常是版本太旧。
			missing = append(missing, fmt.Sprintf("%s:ComfyUI 侧读不到节点 %s 的可选值(节点缺失或输入已改名)", a.label, a.class))
			continue
		}
		found := false
		for _, v := range list {
			if v == a.file {
				found = true
				break
			}
		}
		if !found {
			// folder_paths 按目录 mtime 失效缓存,放好文件后无需重启 ComfyUI
			missing = append(missing, fmt.Sprintf(
				"%s「%s」不在 ComfyUI 的模型目录里:请把该文件放进 models/%s/(放好后 ComfyUI 会自动重新扫描,不必重启)",
				a.label, a.file, a.where))
		}
	}
	return missing
}

// ---------- 局部重绘链路的体检 ----------

// maskNodes 局部重绘图依赖的节点。前两个来自 ComfyUI 内置(nodes_sam3),后两个
// 来自自定义包 MaskVidExperiments —— 用户装了 ComfyUI 但没装那个包时,症状同样是
// "点到一半才失败",所以一并前置体检。
var maskNodes = []struct {
	class     string
	label     string
	builtinIn bool
}{
	{"SAM3_VideoTrack", "SAM3 视频跟踪", true},
	{"SAM3_TrackToMask", "SAM3 跟踪转遮罩", true},
	{"MVEx_SubjectCrop", "MaskVidExperiments 按遮罩裁剪", false},
	{"MVEx_SubjectUncrop", "MaskVidExperiments 回贴原画面", false},
}

// MaskAssets 局部重绘的**节点 + 权重**体检,返回缺失清单(空=齐备)。
//
// 为什么单独一条(2026-09-24 审计):这条链路此前既不在 Preflight 的节点体检里,
// 也没有权重体检 —— 缺 SAM3 checkpoint 或缺 MaskVidExperiments 时,用户点「局部重绘」
// 只会看到 ComfyUI 抛出的 CheckpointLoaderSimple / 未知节点原始报文。而它恰恰是
// 最需要"事前说清"的功能:一次要上传源视频、跑十几分钟。
//
// 引擎不可达时返回空:与 Preflight/UpscaleAssets 同一条原则 —— 连不上不等于资产缺失。
// 但局部重绘的调用点(MaskFix handler)**没有**先调 Available,所以这里必须先尝试拉起,
// 否则冷启动时 SAM3/插件体检会被静默跳过(2026-09-26 渲染管线审查)。
func (e *Engine) MaskAssets(ctx context.Context) []string {
	if !e.engineReachable(ctx) {
		return nil
	}
	var missing []string
	for _, n := range maskNodes {
		if !e.client().HasNode(ctx, n.class) {
			src := "ComfyUI 内置(请升级 ComfyUI)"
			if !n.builtinIn {
				src = "自定义包 MaskVidExperiments(请把该包放进 custom_nodes/ 后重启 ComfyUI)"
			}
			missing = append(missing, fmt.Sprintf("%s:缺少节点 %s —— 它来自 %s", n.label, n.class, src))
		}
	}
	// 权重:以 ComfyUI 自己给出的可选值为准(不要在本地 os.Stat 猜路径)
	if list, ok := e.client().ComboList(ctx, "CheckpointLoaderSimple", "ckpt_name"); !ok {
		missing = append(missing, "读不到 CheckpointLoaderSimple 的可选值(节点缺失或输入已改名),无法确认 SAM3 权重")
	} else {
		found := false
		for _, v := range list {
			if v == sam3Checkpoint {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, fmt.Sprintf(
				"SAM3 跟踪权重「%s」不在 ComfyUI 的模型目录里:请把该文件放进 models/checkpoints/(放好后不必重启)",
				sam3Checkpoint))
		}
	}
	return missing
}

// ---------- 已知冲突插件检测 ----------

// knownConflictingPacks 已知会破坏 ManJuX 渲染的插件包(目录名片段 + 机制说明)。
// 详见 docs/COMPATIBILITY.md 第三节。
var knownConflictingPacks = []struct {
	fragment string
	why      string
}{
	{
		fragment: "MiniMaxH3-Cache",
		why: "该包在 ComfyUI 启动时全局替换 MiniMaxH3Model._forward,替换后的签名丢掉了 " +
			"denoise_mask / cond_audio,会让镜间接缝必然失败(即使当前工作流里没有 cache 节点), " +
			"且上游未修复、升级无解。请把该插件目录改名以 .disabled 结尾后重启 ComfyUI",
	},
}

// Conflicts 检测 custom_nodes 下已安装但未禁用的已知冲突插件,返回告警文本(空=无)。
//
// 只告警不阻断:用户可能因其它工作流而有意保留该插件,直接拦下渲染会误伤。
func (e *Engine) Conflicts() []string {
	c := e.cfg()
	if strings.TrimSpace(c.RootDir) == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(c.RootDir, "ComfyUI", "custom_nodes"))
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		name := ent.Name()
		if strings.HasSuffix(strings.ToLower(name), ".disabled") {
			continue // 已按文档禁用,不再打扰
		}
		for _, p := range knownConflictingPacks {
			if strings.Contains(name, p.fragment) {
				out = append(out, fmt.Sprintf("检测到冲突插件 %s:%s", name, p.why))
			}
		}
	}
	return out
}
