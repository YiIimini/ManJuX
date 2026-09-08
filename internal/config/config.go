// Package config 管理 ManJuX 全局设置(持久化于 data/settings.json)。
// 设置采用原子写(临时文件+rename)防止意外损坏。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// LLMSettings 文本大模型(OpenAI 兼容 /chat/completions)。
type LLMSettings struct {
	BaseURL string `json:"base_url"` // 例如 https://api.deepseek.com/v1
	APIKey  string `json:"api_key"`
	Model   string `json:"model"` // 例如 deepseek-chat
	Timeout int    `json:"timeout_seconds"`
}

// ComfySettings 本地 ComfyUI 引擎设置。
type ComfySettings struct {
	BaseURL  string `json:"base_url"`  // 例如 http://127.0.0.1:8190
	RootDir  string `json:"root_dir"`  // ComfyUI 环境根目录(含 ComfyUI/ 与 standalone-env/)
	AutoStart bool  `json:"auto_start"` // 渲染前未运行时自动启动
	IdleStopMin int  `json:"idle_stop_min"` // 空闲自动退出(分钟):管线结束后无任务则停掉代管引擎释放显存;0=常驻
	// 模型文件名(须与 ComfyUI 实际文件名完全一致,大小写敏感)
	UNetRef2VA     string `json:"unet_ref2va"`     // 有角色参考图镜头用
	UNetFL2VA      string `json:"unet_fl2va"`      // 空镜/首帧镜头用
	TurboLoRARef2VA string `json:"turbo_lora_ref2va"` // ref2va 加速 LoRA(空串=不启用)
	TurboLoRAFL2VA  string `json:"turbo_lora_fl2va"`  // fl2va 加速 LoRA(空串=不启用)
	VideoVAE       string `json:"video_vae"`
	AudioVAE       string `json:"audio_vae"`   // 缺失=黑屏,必填
	CLIP           string `json:"clip"`        // Qwen3-VL 文本编码器(CLIPLoader type=minimax)
	CondCache      bool   `json:"cond_cache"`  // 两阶段渲染(条件缓存 .pt)
	// 定妆照/场景图自动生成(Krea-2 Turbo,8 步蒸馏)
	ImageUNet string `json:"image_unet"` // 默认 krea2_turbo_fp8_scaled.safetensors
	ImageCLIP string `json:"image_clip"` // 默认 qwen3vl_4b_fp8_scaled.safetensors(type=krea2)
	ImageVAE  string `json:"image_vae"`  // 默认 qwen_image_vae.safetensors
}

// MiniMaxSettings MiniMax H3 云端 API 设置。
type MiniMaxSettings struct {
	BaseURL    string `json:"base_url"`    // 国内 https://api.minimax.cn / 国际 https://api.minimaxi.com
	APIKey     string `json:"api_key"`
	Model      string `json:"model"`       // MiniMax-H3 / MiniMax-H3-Max
	Resolution string `json:"resolution"`  // 768P / 2K / 480P(Max)
}

// FFSettings ffmpeg/ffprobe 设置。
type FFSettings struct {
	FFmpegPath  string `json:"ffmpeg_path"`  // 空串=从 PATH 查找
	FFprobePath string `json:"ffprobe_path"`
}

// VLMSettings 视觉大模型(OpenAI 兼容多模态;审片判分用,智谱 glm-4.6v-flash 免费)。
type VLMSettings struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// RenderSettings 渲染默认参数(项目级可覆盖)。
type RenderSettings struct {
	DefaultEngine string `json:"default_engine"` // auto / comfy / api
	Width         int    `json:"width"`          // 须被 32 整除,短边≤768
	Height        int    `json:"height"`
	Steps         int    `json:"steps"`          // turbo 8 / 基线 20
	Seed          int64  `json:"seed"`           // 0=每镜随机;非 0=全项目固定
	BurnSubtitles bool   `json:"burn_subtitles"` // 合成时烧录字幕
	Transition    string `json:"transition"`     // cut / fade / dissolve
	MaxShots      int    `json:"max_shots"`      // 单次分镜上限(成本护栏)
}

// Settings 全局设置根。
type Settings struct {
	ServerPort int            `json:"server_port"`
	DataDir    string         `json:"data_dir"`
	AutoOpenUI bool           `json:"auto_open_ui"` // 启动时自动打开浏览器工作台
	LLM        LLMSettings    `json:"llm"`
	Comfy      ComfySettings  `json:"comfy"`
	MiniMax    MiniMaxSettings `json:"minimax"`
	FF         FFSettings      `json:"ff"`
	VLM        VLMSettings     `json:"vlm"`
	Render     RenderSettings  `json:"render"`
	NovelDir   string        `json:"novel_dir"` // 小说库目录(小说管理页扫描)
}

// Default 返回带默认值的设置。
func Default() *Settings {
	return &Settings{
		ServerPort: 8790,
		DataDir:    "", // 由 Manager 依 exe 位置填充
		NovelDir:   "", // 由 Manager 默认填 <DataDir>/Novel(存在时)
		AutoOpenUI: true,
		LLM: LLMSettings{
			BaseURL: "https://api.deepseek.com/v1",
			APIKey:  "",
			Model:   "deepseek-chat",
			Timeout: 600,
		},
		Comfy: ComfySettings{
			BaseURL:         "http://127.0.0.1:8190",
			RootDir:         "", // 由 Manager 自动探测填充
			AutoStart:       true,
			IdleStopMin:     10,
			UNetRef2VA:      "MiniMax_H3_ref2va_pruned_int8_convrot.safetensors",
			UNetFL2VA:       "MiniMax_H3_fl2va_pruned_int8_convrot.safetensors",
			TurboLoRARef2VA: "minimax_h3_ref2v_turbo_4step_v0.1_comfyui_bf16.safetensors",
			TurboLoRAFL2VA:  "minimax_h3_fl2v_turbo_4step_v1.1_768p_comfyui_bf16.safetensors",
			VideoVAE:        "minimax_h3_video_vae_fp16.safetensors",
			AudioVAE:        "minimax_h3_audio_vae_fp32.safetensors",
			CLIP:            "qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors",
			CondCache:       true,
			ImageUNet:       "krea2_turbo_fp8_scaled.safetensors",
			ImageCLIP:       "qwen3vl_4b_fp8_scaled.safetensors",
			ImageVAE:        "qwen_image_vae.safetensors",
		},
		MiniMax: MiniMaxSettings{
			BaseURL:    "https://api.minimax.cn",
			APIKey:     "",
			Model:      "MiniMax-H3",
			Resolution: "768P",
		},
		VLM: VLMSettings{
			BaseURL: "https://open.bigmodel.cn/api/paas/v4",
			APIKey:  "",
			Model:   "glm-4.6v-flash",
		},
		Render: RenderSettings{
			DefaultEngine: "auto",
			Width:         768,
			Height:        1344,
			Steps:         8,
			Seed:          0,
			BurnSubtitles: true,
			Transition:    "cut",
			MaxShots:      24,
		},
	}
}

// Manager 设置管理器(线程安全)。
type Manager struct {
	mu       sync.Mutex
	path     string
	settings *Settings
}

// NewManager 创建管理器:path 为设置文件路径;exeDir 用于推导默认数据目录。
// 文件存在则加载(缺失字段以默认值补齐),否则生成默认设置。
func NewManager(path, exeDir string) (*Manager, error) {
	m := &Manager{path: path}
	def := Default()
	if def.DataDir == "" {
		def.DataDir = filepath.Join(exeDir, "data")
	}
	if def.Comfy.RootDir == "" {
		def.Comfy.RootDir = filepath.Join(exeDir, "comfyui")
	}
	if def.NovelDir == "" {
		def.NovelDir = filepath.Join(def.DataDir, "Novel")
	}
	raw, err := os.ReadFile(path)
	if err == nil && len(raw) > 0 {
		s := Default()
		s.DataDir = def.DataDir
		s.Comfy.RootDir = def.Comfy.RootDir
		if err := json.Unmarshal(raw, s); err != nil {
			return nil, err
		}
		// 已有文件但字段为空时回落到推导值
		if s.DataDir == "" {
			s.DataDir = def.DataDir
		}
		if s.Comfy.RootDir == "" {
			s.Comfy.RootDir = def.Comfy.RootDir
		}
		if s.NovelDir == "" {
			s.NovelDir = def.NovelDir
		}
		m.settings = s
	} else {
		m.settings = def
	}
	if err := m.saveLocked(); err != nil {
		return nil, err
	}
	return m, nil
}

// Get 返回设置快照(调用方不应修改其内部切片/映射)。
func (m *Manager) Get() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.settings
}

// Update 以乐观方式整体更新设置。
func (m *Manager) Update(s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = &s
	return m.saveLocked()
}

// saveLocked 原子写:临时文件+rename。调用方须持有锁。
func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m.settings, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
