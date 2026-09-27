package comfyengine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AdminStatus ComfyUI 管理状态聚合。
type AdminStatus struct {
	Alive   bool   `json:"alive"`
	Version string `json:"version"`
	GPU     string `json:"gpu"`
	Running int    `json:"queue_running"`
	Pending int    `json:"queue_pending"`
	// QueueKnown 队列数字是否**真的查到了**。
	//
	// 为什么必须单独一个字段(2026-09-24 审计 P0-5):/queue 查询失败时 Running/Pending
	// 保持 0,于是"引擎忙到答不上探测"与"引擎彻底空闲"在返回值上完全同构 ——
	// 显存护栏据此以为空闲、调 /free 把正在跑的权重卸掉(之后每步重新流式加载,
	// 实测 150s/步,整镜 45 分钟超时)。加上这个字段,**探测不到就不许当空闲**。
	QueueKnown bool         `json:"queue_known"`
	Owned      bool         `json:"owned"` // 是否为本应用代管启动(可停止)
	RootDir    string       `json:"root_dir"`
	BaseURL    string       `json:"base_url"`
	Models     []ModelCheck `json:"models"`
	Nodes      []NodeCheck  `json:"nodes"`
	Sage       *SageStatus  `json:"sage,omitempty"` // Sage 加速状态(装了没/会不会启用)
}

// ModelCheck 模型配置核对。
type ModelCheck struct {
	Label string   `json:"label"`          // 显示名
	Key   string   `json:"key"`            // 设置键
	Name  string   `json:"name"`           // 配置的文件名
	OK    bool     `json:"ok"`             // 是否在 ComfyUI 列表中
	List  []string `json:"list,omitempty"` // 可用清单(未命中时给候选)
}

// NodeCheck 自定义节点检测。
type NodeCheck struct {
	Label    string `json:"label"`
	Class    string `json:"class"`
	OK       bool   `json:"ok"`
	Required bool   `json:"required,omitempty"` // 缺了会**直接失败**（false = 缺了会自动降级）
}

// adminNodeChecks 管理页要核对的节点清单（**唯一真源**，见下）。
//
// 2026-09-27（用户「ComfyUI 我升级了」之后的同步核对）：此前这里只查 2 个**可选**节点
// （条件缓存 / 镜间接缝）—— 它们缺了会降级，所以"缺了也不致命"；而**渲染必用的根节点**
// 只有 `Preflight`（按下渲染那一刻）才查。
// 后果：升级后少了某个自定义节点，**在管理页上看不出来**，要等第一镜跑到一半由 ComfyUI
// 抛原始报文才发现 —— 正是 `Preflight` 注释里说要消灭的那种体验。
//
// 现在把必用根节点也搬到这里，并且**与 Preflight 共用同一批常量**
// （`classRef2V` / `classI2V` / `classSigmaShift`），两侧不会走散：
//   - required=true：缺则**渲染直接失败**（提前看见比跑到一半好）；
//   - required=false：缺则**自动降级/退回**（不阻断生产，但列出来让人知道当前能力少了一块）。
//
// 判据口径与 Preflight 保持一致：只列"根节点"（生成链入口 + sigma 偏移）与两个可选降级项，
// 不把每个中间节点都列出来 —— 列全了反而没人看。
var adminNodeChecks = []NodeCheck{
	{Label: "参考生视频 · ReferenceToVideo（缺则渲染直接失败）", Class: classRef2V, Required: true},
	{Label: "图生视频 · ImageToVideo（缺则渲染直接失败）", Class: classI2V, Required: true},
	{Label: "双流 sigma 偏移 · SigmaShift（缺则渲染直接失败）", Class: classSigmaShift, Required: true},
	{Label: "条件缓存 · ConditioningCache（缺则自动退回内联编码）", Class: classCondSave},
	{Label: "镜间接缝 · MotionContext（缺则跳过接缝）", Class: classMotionCtx},
}

// Admin 聚合管理状态(不触发代管启动)。
func (e *Engine) Admin(ctx context.Context) (*AdminStatus, error) {
	c := e.cfg()
	e.setClient(NewClient(c.BaseURL))
	st := &AdminStatus{Alive: false, RootDir: c.RootDir, BaseURL: c.BaseURL, Owned: e.ownedProc() != nil}
	// Sage 状态与引擎是否在跑无关(它只看包是否装了、设置是否开着),放在前面填:
	// 引擎停着时管理页也要能显示"为什么没加速",而不是一片空白。
	sg := e.SageStatus(ctx)
	st.Sage = &sg
	if !e.client().Alive(ctx) {
		clearPidFile(c) // 引擎已死(含托盘/任务管理器手杀):清残留 pid
		return st, nil
	}
	st.Alive = true
	if e.ownedProc() == nil {
		// 上实例代管启动的引擎:按 pid 文件接管,恢复停止/退出管理权
		st.Owned = e.adoptFromPidFile()
	}
	if ver, err := e.client().Version(ctx); err == nil {
		st.Version = ver
	}
	if run, pend, err := e.client().Queue(ctx); err == nil {
		st.Running, st.Pending = len(run), len(pend)
		st.QueueKnown = true // 只有真查到了才敢说"队列为空"(见 QueueKnown 的说明)
	}

	// GPU(从 system_stats devices)
	if raw, err := e.client().get(ctx, "/system_stats"); err == nil {
		var out struct {
			Devices []struct {
				Name string `json:"name"`
			} `json:"devices"`
		}
		if json.Unmarshal(raw, &out) == nil && len(out.Devices) > 0 {
			st.GPU = out.Devices[0].Name
		}
	}
	// 模型核对(object_info 列表)
	checks := []struct{ label, key, node, field string }{
		{"UNET · Ref2VA", "unet_ref2va", "UNETLoader", "unet_name"},
		{"UNET · FL2VA", "unet_fl2va", "UNETLoader", "unet_name"},
		{"Turbo LoRA · Ref2VA", "turbo_lora_ref2va", "LoraLoaderModelOnly", "lora_name"},
		{"Turbo LoRA · FL2VA", "turbo_lora_fl2va", "LoraLoaderModelOnly", "lora_name"},
		{"视频 VAE", "video_vae", "VAELoader", "vae_name"},
		{"音频 VAE", "audio_vae", "VAELoader", "vae_name"},
		{"CLIP · Qwen3VL", "clip", "CLIPLoader", "clip_name"},
		{"定妆照 UNet", "image_unet", "UNETLoader", "unet_name"},
		{"定妆照 CLIP", "image_clip", "CLIPLoader", "clip_name"},
		{"定妆照 VAE", "image_vae", "VAELoader", "vae_name"},
		// 多视角图像编辑(Qwen-Image-Edit-2511):这套缺件不会让渲染失败,只会让
		// 多视角静默退回 H3 转身视频(那条路会把角色重画成别人、校验拒绝)。所以在
		// 管理页把每一项都列出来核对,缺了就看得见 —— 与 MaskAssets 的"点下去之前
		// 就说清"同一原则。
		{"多视角编辑 UNet · Qwen-Image-Edit", "edit_unet", "UNETLoader", "unet_name"},
		{"多视角编辑 CLIP · Qwen2.5-VL", "edit_clip", "CLIPLoader", "clip_name"},
		{"多视角编辑 VAE", "edit_vae", "VAELoader", "vae_name"},
		{"多视角编辑加速 LoRA · Lightning 4 步", "edit_turbo_lora", "LoraLoaderModelOnly", "lora_name"},
		{"多视角编辑转角度 LoRA · Multiple-Angles", "edit_angles_lora", "LoraLoaderModelOnly", "lora_name"},
	}
	for _, ck := range checks {
		mc := ModelCheck{Label: ck.label, Key: ck.key}
		switch ck.key {
		case "unet_ref2va":
			mc.Name = c.UNetRef2VA
		case "unet_fl2va":
			mc.Name = c.UNetFL2VA
		case "turbo_lora_ref2va":
			mc.Name = c.TurboLoRARef2VA
		case "turbo_lora_fl2va":
			mc.Name = c.TurboLoRAFL2VA
		case "video_vae":
			mc.Name = c.VideoVAE
		case "audio_vae":
			mc.Name = c.AudioVAE
		case "clip":
			mc.Name = c.CLIP
		case "image_unet":
			mc.Name = c.ImageUNet
		case "image_clip":
			mc.Name = c.ImageCLIP
		case "image_vae":
			mc.Name = c.ImageVAE
		case "edit_unet":
			mc.Name = c.EditUNet
		case "edit_clip":
			mc.Name = c.EditCLIP
		case "edit_vae":
			mc.Name = c.EditVAE
		case "edit_turbo_lora":
			mc.Name = c.EditTurboLoRA
		case "edit_angles_lora":
			mc.Name = c.EditAnglesLoRA
		}
		if list, ok := e.client().ComboList(ctx, ck.node, ck.field); ok {
			if mc.Name != "" {
				for _, v := range list {
					if v == mc.Name {
						mc.OK = true
						break
					}
				}
			}
			if !mc.OK && len(list) > 0 && len(list) <= 30 {
				mc.List = list
			}
		}
		st.Models = append(st.Models, mc)
	}
	// H3 关键节点检测（清单见 adminNodeChecks：必用根节点 + 两个可选降级项）
	for _, np := range adminNodeChecks {
		nc := np
		nc.OK = e.client().HasNode(ctx, nc.Class)
		st.Nodes = append(st.Nodes, nc)
	}
	return st, nil
}

// StartAdmin 手动代管启动。
func (e *Engine) StartAdmin(ctx context.Context) error {
	touchBusy()
	if e.client().Alive(ctx) {
		return fmt.Errorf("ComfyUI 已在运行")
	}
	return e.launch(ctx, e.cfg())
}

// StopAdmin 手动停止:结束全部 ComfyUI 相关进程 —— 代管进程树
// (taskkill /T) + rootDir 内所有 python(外部/上实例启动的同安装实例一并清),
// 停止按钮承诺「杀干净」,不再出现停止后进程残留/端口仍活。
// 引擎本来就没在运行 → 幂等成功;清完进程但端口仍活 → 提示核对根目录配置。
func (e *Engine) StopAdmin() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	alive := e.client().Alive(ctx)
	cancel()
	n := e.killAllComfy("手动停止")
	notifyLifecycle(false, "手动停止")
	if alive && n == 0 {
		return fmt.Errorf("未能定位 ComfyUI 进程:请检查「设置→ComfyUI→根目录」是否与实际安装一致")
	}
	return nil
}

// QueueClear 清空待执行队列。
func (e *Engine) QueueClear(ctx context.Context) { e.client().ClearPending(ctx) }

// Interrupt 中断当前执行。
func (e *Engine) Interrupt(ctx context.Context) { e.client().Interrupt(ctx) }

// FreeVRAM 释放显存;失败上报(引擎未运行/请求异常都返回 error)。
func (e *Engine) FreeVRAM(ctx context.Context) error { return e.client().Free(ctx) }
