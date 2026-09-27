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
	Model   string `json:"model"` // 官方推荐 deepseek-flash(V4.1-Flash;旧 v4-flash/v4-pro 均退役或即将退役)
	Timeout int    `json:"timeout_seconds"`
}

// DefaultComfyBaseURL ComfyUI 服务的默认地址。设置默认值、引擎启动的端口兜底
// 与文档引用共用同一处 —— 此前该端口以字面量散落在 config 两处 + 引擎一处,
// 改端口时漏改任何一处都会让渲染连不上引擎。
const DefaultComfyBaseURL = "http://127.0.0.1:8190"

// ComfySettings 本地 ComfyUI 引擎设置。
type ComfySettings struct {
	BaseURL     string `json:"base_url"`   // 例如 http://127.0.0.1:8190(默认见 DefaultComfyBaseURL)
	RootDir     string `json:"root_dir"`   // ComfyUI 环境根目录(含 ComfyUI/ 与 standalone-env/)
	AutoStart   bool   `json:"auto_start"` // 渲染前未运行时自动启动
	IdleFreeMin int    `json:"idle_stop_min"`
	// ↑ 空闲**释放显存**(分钟):管线结束后无任务则 /free 卸载模型把显存让出去,进程常驻;
	//   0=不释放。**动作是卸模型不是关进程**(2026-09-26 用户报障修正):关进程的时机只有
	//   退出平台,空闲关进程会多付一次 20GB 权重的冷启动。
	//   json key 保留历史的 `idle_stop_min` 以免旧配置丢值(语义已变,名称不改)。
	// 模型文件名(须与 ComfyUI 实际文件名完全一致,大小写敏感)
	UNetRef2VA      string `json:"unet_ref2va"`       // 有角色参考图镜头用
	UNetFL2VA       string `json:"unet_fl2va"`        // 空镜/首帧镜头用
	TurboLoRARef2VA string `json:"turbo_lora_ref2va"` // ref2va 加速 LoRA(空串=不启用)
	TurboLoRAFL2VA  string `json:"turbo_lora_fl2va"`  // fl2va 加速 LoRA(空串=不启用)
	VideoVAE        string `json:"video_vae"`
	AudioVAE        string `json:"audio_vae"`  // 缺失=黑屏,必填
	CLIP            string `json:"clip"`       // Qwen3-VL 文本编码器(CLIPLoader type=minimax)
	CondCache       bool   `json:"cond_cache"` // 两阶段渲染(条件缓存 .pt)
	// 定妆照/场景图/抽卡图自动生成。**家族由 ImageFamily 决定连线**(见
	// comfyengine/imagefamily.go):每个家族的文本编码器类型、采样档、位移节点都不同,
	// 空值按键名推断(Krea-2 → krea2 / z_image → Z-Image / qwen_image_2.1 → Qwen-Image-2.1)。
	//
	// 2026-09-25 用户定调「Krea-2 出图这么多问题,那就不用它生图」,默认家族改为
	// **Qwen-Image-2.1**(7B,生成与编辑同一个权重,中文文字与指令遵循最强)。
	ImageFamily string `json:"image_family"` // 默认 qwen_image_2_1
	ImageUNet   string `json:"image_unet"`   // 默认 qwen_image_2.1_int8_convrot.safetensors
	ImageCLIP   string `json:"image_clip"`   // 默认 qwen3vl_8b_int8_convrot.safetensors(type=qwen_image)
	ImageVAE    string `json:"image_vae"`    // 默认 qwen_image_2.1_vae_bf16.safetensors
	// 图像编辑(多视角重绘 / 上传图白底化 / 换姿势)—— 2026-09-25 起**与生成同一个
	// Qwen-Image-2.1 权重**:一个模型既出定妆照也做编辑,资产链只对一个家族,
	// 少一层"生成一套画风、编辑另一套画风"的漂移源。
	//
	// 旧的 Qwen-Image-Edit-2511 仍可选中(imageedit.go 按编辑底模文件名自动选图形态),
	// 选中它时下面两个 LoRA 才有意义:Lightning 4 步加速、Multiple-Angles 转角度
	// —— 它们都是为 2511 训的,套到 2.1 上不成立,所以 2.1 档下默认留空。
	EditUNet string `json:"edit_unet"` // 默认 qwen_image_2.1_int8_convrot.safetensors
	EditCLIP string `json:"edit_clip"` // 默认 qwen3vl_8b_int8_convrot.safetensors(type=qwen_image)
	EditVAE  string `json:"edit_vae"`  // 默认 qwen_image_2.1_vae_bf16.safetensors
	// 下面两个 LoRA 是 **2511 专用**（见上面那段与 imageedit.go:217-230 的挂载点）。
	//
	// 【2026-09-27 技能侧追补单 N2 的答复 —— 这里不是漏配，是刻意留空】
	// 那份单子看到盘上有
	// `Qwen-Image-Edit-2511-Lightning-4steps-V1.0-bf16.safetensors`（810 MB）与
	// `qwen-image-edit-2511-multiple-angles-lora.safetensors`（281 MB）就以为该接上，并判断
	// "edit_unet 已指向 2511"。**事实相反**：本机 `edit_unet = qwen_image_2.1_int8_convrot.safetensors`
	// （2.1 族），而盘上这两个 LoRA **只有 2511 版**（没有 2.1 版）—— 把 2511 的 LoRA 挂到 2.1 底模上
	// 不是"更慢"或"没用"，而是**污染出图**（权重与底模不同代，patch 的投影维度对不上）。
	// 所以 2.1 档下必须留空，且这一条被三处守着：
	//   ① `editTripleIsLegacy2511` 迁移 —— 把"2511 底模 + 这两个 LoRA"的旧组合迁到 2.1 并清空；
	//   ② `image_family_migration_test.go` 断言"2511 专用 LoRA 必须清空"；
	//   ③ `EditAssets`（编辑链路的权重体检）会报"底模与 LoRA 不同代"的误配。
	// 什么时候才该填：把 `edit_unet` 换回 2511 那一支（imageedit.go 按文件名自动切图形态），
	// 此时这两个 LoRA 生效、4 步加速与"换机位"才可用。
	EditTurboLoRA  string `json:"edit_turbo_lora"`  // 2511 专用加速 LoRA（Lightning 4 步）；2.1 档留空
	EditAnglesLoRA string `json:"edit_angles_lora"` // 2511 专用转角度 LoRA（Multiple-Angles）；2.1 档留空

	// ReserveVRAMGB 启动 ComfyUI 时预留的显存(GB),留给系统/浏览器。
	// 实测渲染前别的进程已占 1-2GB,不预留就等于和桌面抢最后几 GB(CUDA OOM 或
	// 整机卡顿)。0 = 交给 ComfyUI 自定(见 comfyLaunchArgs)。
	ReserveVRAMGB int `json:"reserve_vram_gb"`
	// SageAttention Sage 注意力加速(官方推荐,H3 生成约快一倍,画质损失很小)。
	// nil = 未表态(装了 sageattention 包就自动启用);true/false = 显式开关。
	// 用三态是因为它**影响画面**(2026-09-10 用户实证过注意力后端导致画质劣化),
	// 必须让用户能一键关掉,而不该由代码替他决定。
	SageAttention *bool `json:"sage_attention"`
}

// MiniMaxSettings MiniMax H3 云端 API 设置。
type MiniMaxSettings struct {
	BaseURL    string `json:"base_url"` // 国内 https://api.minimax.cn / 国际 https://api.minimaxi.com
	APIKey     string `json:"api_key"`
	Model      string `json:"model"`      // MiniMax-H3 / MiniMax-H3-Max
	Resolution string `json:"resolution"` // 768P / 2K / 480P(Max)
}

// FFSettings ffmpeg/ffprobe 设置。
type FFSettings struct {
	FFmpegPath  string `json:"ffmpeg_path"` // 空串=从 PATH 查找
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
	DefaultEngine string  `json:"default_engine"` // auto / comfy / api
	Width         int     `json:"width"`          // 须被 32 整除,短边≤768
	Height        int     `json:"height"`
	Steps         int     `json:"steps"`          // turbo 8 / 基线 20
	Seed          int64   `json:"seed"`           // 0=每镜随机;非 0=全项目固定
	BurnSubtitles bool    `json:"burn_subtitles"` // 合成时烧录字幕
	Transition    string  `json:"transition"`     // cut / fade / dissolve
	MaxShots      int     `json:"max_shots"`      // 单次分镜上限(成本护栏)
	FadeSec       float64 `json:"fade_sec"`       // 转场时长(秒)
	ShotTimeout   int     `json:"shot_timeout"`   // 单镜渲染超时(分钟)
}

// SplitSettings 小说拆集参数(设置页可调,2026-09-11 用户定调:灵活配置)。
type SplitSettings struct {
	ChunkSize    int `json:"chunk_size"`     // 分段大小(字/段,0=20000;按输出预算倒推:每段最多8集×700字×1.5token+思考30K≈40K,须<65K)
	MaxOrigChars int `json:"max_orig_chars"` // 每集附原文截断上限(字,0=9000)
	Concurrency  int `json:"concurrency"`    // 并行拆集并发数(0=3)
	PrevSummary  int `json:"prev_summary"`   // 前情提要截断(字,0=400)
	BriefMin     int `json:"brief_min"`      // 每集大纲最短(字,0=400)
	BriefMax     int `json:"brief_max"`      // 每集大纲最长(字,0=700)
	// ShotLimit 镜头数安全网上限;0 = 用内置安全网(project.MaxShotsCeiling = 120)。
	//
	// 2026-09-24 审计 P1-1:这个字段此前**只有一个界面**,Go 侧零读取点 ——
	// 内容驱动模式下真正的上限一直是硬编码的 120。现在它真的生效;默认值同时从
	// 30 改成 0(= 与旧行为一致),否则把字段接上的那一刻起,所有内容驱动项目都会
	// 从"上限 120"悄悄变成"上限 30",方案结尾会被截掉。
	ShotLimit int `json:"shot_limit"`
}

// Settings 全局设置根。
//
// 注意:这里**只放真的会被读到的东西**。"有界面、能保存、Go 侧零读取点"的设置项
// 是审计 P1-1 点名的一类缺陷(用户调了它、系统毫无反应,而且不报错)。已据此清掉
// `auto_open_ui`:桌面模式下工作台就是那个原生窗口,再弹一个浏览器标签只会更吵,
// 也就是说这个开关在当前架构里没有正确语义 —— 留着比删掉更糟。
// 新增字段请一并确认它有读取点(internal/config/fields_contract_test.go 会扫)。
type Settings struct {
	ServerPort int             `json:"server_port"`
	DataDir    string          `json:"data_dir"`
	LLM        LLMSettings     `json:"llm"`
	Comfy      ComfySettings   `json:"comfy"`
	MiniMax    MiniMaxSettings `json:"minimax"`
	FF         FFSettings      `json:"ff"`
	VLM        VLMSettings     `json:"vlm"`
	Render     RenderSettings  `json:"render"`
	Split      SplitSettings   `json:"split"`
	NovelDir   string          `json:"novel_dir"` // 小说库目录(小说管理页扫描)
}

// Default 返回带默认值的设置。
func Default() *Settings {
	return &Settings{
		ServerPort: 8790,
		DataDir:    "", // 由 Manager 依 exe 位置填充
		NovelDir:   "", // 由 Manager 默认填 <DataDir>/Novel(存在时)
		LLM: LLMSettings{
			BaseURL: "https://api.deepseek.com/v1",
			APIKey:  "",
			Model:   "deepseek-flash",
			Timeout: 600,
		},
		Comfy: ComfySettings{
			BaseURL:         DefaultComfyBaseURL,
			RootDir:         "", // 由 Manager 自动探测填充
			AutoStart:       true,
			IdleFreeMin:     10,
			UNetRef2VA:      "MiniMax_H3_ref2va_pruned_int8_convrot.safetensors",
			UNetFL2VA:       "MiniMax_H3_fl2va_pruned_int8_convrot.safetensors",
			TurboLoRARef2VA: "minimax_h3_ref2v_turbo_4step_v0.1_comfyui_bf16.safetensors",
			TurboLoRAFL2VA:  "minimax_h3_fl2v_turbo_4step_v1.1_768p_comfyui_bf16.safetensors",
			VideoVAE:        "minimax_h3_video_vae_fp16.safetensors",
			AudioVAE:        "minimax_h3_audio_vae_fp32.safetensors",
			CLIP:            "qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors",
			CondCache:       true,
			// 图像模型家族。**这里刻意写字符串字面量**:comfyengine 反向 import 了本包
			// (取 ComfySettings),本包再 import 它就成了循环依赖。两处取值由
			// comfyengine 的 TestDefaultImageFamilyMatchesConfig 钉住,改一处漏一处会红。
			ImageFamily:    "qwen_image_2_1",
			ImageUNet:      "qwen_image_2.1_int8_convrot.safetensors",
			ImageCLIP:      "qwen3vl_8b_int8_convrot.safetensors",
			ImageVAE:       "qwen_image_2.1_vae_bf16.safetensors",
			EditUNet:       "qwen_image_2.1_int8_convrot.safetensors",
			EditCLIP:       "qwen3vl_8b_int8_convrot.safetensors",
			EditVAE:        "qwen_image_2.1_vae_bf16.safetensors",
			EditTurboLoRA:  "",
			EditAnglesLoRA: "",
			ReserveVRAMGB:  1, // 给系统/浏览器留 1GB,避免渲染时把桌面一起拖卡
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
			Transition:    "fade",
			MaxShots:      0, // 0=内容驱动(LLM 按原文自行决定)
			FadeSec:       0.5,
			ShotTimeout:   45,
		},
		Split: SplitSettings{
			ChunkSize:    12000, // 细拆目标段:≈3-4 章/段→每段 4-8 集,集数密度按段内章数动态上限(2026-09-11「细中细」定调;输出预算:24集上限×700字×1.5+思考≈55K<65K)
			MaxOrigChars: 9000,
			Concurrency:  3,
			PrevSummary:  400,
			BriefMin:     400,
			BriefMax:     700,
			ShotLimit:    0, // 0=内容驱动(用内置安全网 120);与其字段说明同源
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
// 文件存在则加载(缺失字段以默认值补齐),否则生成默认设置文件。
// 注意:文件已存在时【不】回写磁盘 —— 曾有无条件 saveLocked,任何实例(含
// 旧版/服务模式并存实例)启动都会整文件重写,与「另一实例刚保存的新值」
// 形成读-改-写竞态,表现为用户设置莫名被旧值覆盖(2026-09-09 事故)。
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
		// 旧默认值迁移:v0.32 时代落盘的 chunk_size=40000 是截断防治
		// 期的默认(8 集/段上限配套),细拆定调后段目标 12000
		// (2026-09-11 用户定调「细中细」:56 章 49 集即此残留所致)
		if s.Split.ChunkSize == 40000 {
			s.Split.ChunkSize = 12000
		}
		// 旧默认值迁移(2026-09-25):图像模型从 Krea-2 换成 Qwen-Image-2.1
		// (用户定调「Krea-2 出图这么多问题,那就不用它生图」)。只有**落盘文件里没有
		// image_family 键**、且三件套仍是 Krea-2 那套旧默认时才迁移 —— 用户自己换过
		// 模型就原样保留,绝不覆盖手工选择。
		//
		// 判据必须是"键在不在",不能是"字段空不空":上面的 s := Default() 已经把
		// image_family 填成新默认值,老文件反序列化后该字段**永远非空**,迁移条件
		// 就永远不成立。实测踩过:家族名变了、模型文件名没变,于是按 2.1 的连线去加载
		// Krea-2 的编码器,ComfyUI 直接报
		// "Krea2 expects conditioning with 12x2560=30720 features ... but got 2560"。
		// 键在**comfy 段里**,不是顶层 —— 探针也要按同一层解,否则显式选择会被冲掉。
		var written struct {
			Comfy map[string]json.RawMessage `json:"comfy"`
		}
		_ = json.Unmarshal(raw, &written)
		_, familyWritten := written.Comfy["image_family"]
		if !familyWritten && imageTripleIsLegacyKrea2(s) {
			s.Comfy.ImageFamily = "qwen_image_2_1"
			s.Comfy.ImageUNet = def.Comfy.ImageUNet
			s.Comfy.ImageCLIP = def.Comfy.ImageCLIP
			s.Comfy.ImageVAE = def.Comfy.ImageVAE
			// 编辑侧一起迁(用户定调「都使用 2.1」):旧默认是 Qwen-Image-Edit-2511 + 两个
			// 2511 专用 LoRA。留着它们会出现"生成用 2.1、编辑用 2511"的两套画风。
			if editTripleIsLegacy2511(s) {
				s.Comfy.EditUNet = def.Comfy.EditUNet
				s.Comfy.EditCLIP = def.Comfy.EditCLIP
				s.Comfy.EditVAE = def.Comfy.EditVAE
				s.Comfy.EditTurboLoRA = def.Comfy.EditTurboLoRA
				s.Comfy.EditAnglesLoRA = def.Comfy.EditAnglesLoRA
			}
		}
		m.settings = s
		return m, nil
	}
	// 文件缺失/为空:落一份默认文件(此时没有可覆盖的用户值)
	m.settings = def
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

// legacyKrea2ImageTriple 是 2026-09-25 之前落盘的图像模型默认三件套。
// 三件套**逐字等于**它时,才认为用户从未改过图像模型,可以安全迁移到新默认
// (Qwen-Image-2.1)。用户手工换过任何一个,都不动。
// (调用方还要额外确认落盘文件里没有 image_family 键,见 New 里的说明。)
var legacyKrea2ImageTriple = [3]string{
	"krea2_turbo_fp8_scaled.safetensors",
	"qwen3vl_4b_fp8_scaled.safetensors",
	"qwen_image_vae.safetensors",
}

func imageTripleIsLegacyKrea2(s *Settings) bool {
	return s.Comfy.ImageUNet == legacyKrea2ImageTriple[0] &&
		s.Comfy.ImageCLIP == legacyKrea2ImageTriple[1] &&
		s.Comfy.ImageVAE == legacyKrea2ImageTriple[2]
}

// legacyEdit2511Triple 是 2026-09-25 之前落盘的**编辑**模型默认(Qwen-Image-Edit-2511
// + 两个 2511 专用 LoRA)。与生成侧同一套判据:逐字等于旧默认才迁移。
func editTripleIsLegacy2511(s *Settings) bool {
	return s.Comfy.EditUNet == "qwen_image_edit_2511_fp8mixed.safetensors" &&
		s.Comfy.EditCLIP == "qwen_2.5_vl_7b_fp8_scaled.safetensors" &&
		s.Comfy.EditVAE == "qwen_image_vae.safetensors" &&
		s.Comfy.EditTurboLoRA == "Qwen-Image-Edit-2511-Lightning-4steps-V1.0-bf16.safetensors" &&
		s.Comfy.EditAnglesLoRA == "qwen-image-edit-2511-multiple-angles-lora.safetensors"
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
