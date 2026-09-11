package comfyengine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AdminStatus ComfyUI 管理状态聚合。
type AdminStatus struct {
	Alive   bool         `json:"alive"`
	Version string       `json:"version"`
	GPU     string       `json:"gpu"`
	Running int          `json:"queue_running"`
	Pending int          `json:"queue_pending"`
	Owned   bool         `json:"owned"` // 是否为本应用代管启动(可停止)
	RootDir string       `json:"root_dir"`
	BaseURL string       `json:"base_url"`
	Models  []ModelCheck `json:"models"`
	Nodes   []NodeCheck  `json:"nodes"`
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
	Label string `json:"label"`
	Class string `json:"class"`
	OK    bool   `json:"ok"`
}

// Admin 聚合管理状态(不触发代管启动)。
func (e *Engine) Admin(ctx context.Context) (*AdminStatus, error) {
	c := e.cfg()
	e.setClient(NewClient(c.BaseURL))
	st := &AdminStatus{Alive: false, RootDir: c.RootDir, BaseURL: c.BaseURL, Owned: e.ownedProc() != nil}
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
		{"定妆照 UNet · Krea2", "image_unet", "UNETLoader", "unet_name"},
		{"定妆照 CLIP", "image_clip", "CLIPLoader", "clip_name"},
		{"定妆照 VAE", "image_vae", "VAELoader", "vae_name"},
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
	// H3 关键节点检测
	for _, ncd := range []struct{ label, class string }{
		{"条件缓存 · ConditioningCache", classCondSave},
		{"镜间接缝 · MotionContext", classMotionCtx},
	} {
		nc := NodeCheck{Label: ncd.label, Class: ncd.class}
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
