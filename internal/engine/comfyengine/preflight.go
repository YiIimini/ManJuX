package comfyengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// 只覆盖 graph 根节点(生成链入口 + sigma 偏移):它们缺失必然全镜失败,提前
// 阻断是净收益;可选能力(条件缓存/镜间接缝)刻意不列入,以免误判挡住生产。
//
// 引擎不可达时返回空:不把「暂时连不上」误判成「节点缺失」(调用方在此之前
// 已确认过引擎可用,这里只是兜底)。
func (e *Engine) Preflight(ctx context.Context, opt PreflightOptions) []string {
	// 只在引擎已就绪时体检:不可达就交给原有渲染路径处理(只有它才该按
	// AutoStart 决定是否代管启动)。这样既避免预检触发意外启动,也避免把
	// 「暂时连不上」误判成「节点缺失」而挡住本可运行的任务。
	if !e.client().Alive(ctx) {
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
