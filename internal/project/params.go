package project

// StageParams 管线各阶段参数(项目级完整快照,创建项目时从全局默认拷贝,
// 之后与全局默认互不影响 —— 实现「单项目独立调优」)。
type StageParams struct {
	Script   ScriptParams   `json:"script"`
	Assets   AssetsParams   `json:"assets"`
	Render   RenderParams   `json:"render"`
	QC       QCParams       `json:"qc"`
	Assemble AssembleParams `json:"assemble"`
}

// ScriptParams 剧本/分镜阶段(script)参数。
type ScriptParams struct {
	MaxShots     int      `json:"max_shots"`      // 目标镜头数(3-12)
	TotalSeconds int      `json:"total_seconds"`  // 目标总时长(秒)
	Styles       []string `json:"styles"`         // 渲染风格预设 key(可多选组合)
	StyleHint    string   `json:"style_hint"`     // 附加风格要求(自由文本)
	Chibi        bool     `json:"chibi"`          // 定妆时同步生成 Q 版形象(内心活动表达)
}

// AssetsParams 资产阶段(assets)参数。
type AssetsParams struct {
	Source string `json:"source"` // upload(手动上传)/ krea2(Krea-2 自动生成)/ skip(纯文生 t2va)
}

// RenderParams 渲染阶段(render)参数。
type RenderParams struct {
	Engine    string `json:"engine"`     // auto / comfy / api
	Accel     string `json:"accel"`      // 加速档:quality(Acc-8Step质量) / turbo(官方Turbo,默认) / express(极速预览:int8VAE+4步)
	Width     int    `json:"width"`      // 32 整除,短边≤768
	Height    int    `json:"height"`
	Steps     int    `json:"steps"`      // turbo 4-8 / 基线 20
	Seed      int64  `json:"seed"`       // 0=每镜随机;非 0=全项目固定
	Turbo     bool   `json:"turbo"`      // 是否挂加速 LoRA(全局 Comfy 设置提供文件名)
	CondCache bool   `json:"cond_cache"` // 两阶段渲染(条件缓存)
	Seam      bool   `json:"seam"`       // 镜间连续接缝(MotionContext;仅本地 ComfyUI;相邻镜同分辨率)
}

// QCParams 质检阶段(qc)参数。
type QCParams struct {
	Enabled       bool    `json:"enabled"`
	DarkRatioFail float64 `json:"dark_ratio_fail"` // 暗像素占比 ≥ 此值判失败(默认 0.60)
	DarkRatioWarn float64 `json:"dark_ratio_warn"` // 暗像素占比 ≥ 此值警告(默认 0.45)
	MinDuration   float64 `json:"min_duration"`    // 单镜最短时长(秒)
	MaxDuration   float64 `json:"max_duration"`    // 单镜最长时长(秒)
	EnableVLM     bool    `json:"enable_vlm"`      // VLM 智能审片(抽帧判分+重渲建议,需视觉模型)
}

// AssembleParams 合成阶段(assemble)参数。
type AssembleParams struct {
	Transition    string  `json:"transition"`     // cut / fade / dissolve
	BurnSubtitles bool    `json:"burn_subtitles"` // 烧录字幕
	Loudness      int     `json:"loudness"`       // 目标响度 LUFS(默认 -16)
	Peak          float64 `json:"peak"`           // 峰值归一(默认 0.92)
	SubtitleSize  int     `json:"subtitle_size"`  // 字幕字号(屏高百分比,默认 5)
	Upgrade2K     bool    `json:"upgrade_2k"`     // 云端 2K 定稿:合成完成后自动 regeneration 升 2K(消耗云额度)
}

// DefaultStageParams 返回全局默认的阶段参数。
func DefaultStageParams() StageParams {
	return StageParams{
		Script: ScriptParams{MaxShots: 6, TotalSeconds: 42, StyleHint: ""},
		Assets: AssetsParams{Source: "krea2"}, // 默认自动定妆:缺失参考图由 Krea-2 生成,不再要求手动上传
		Render: RenderParams{
			Engine: "auto", Width: 768, Height: 1344,
			// Seam 默认开:镜间连续(MotionContext)让相邻镜头运镜/画面延续,
			// 配合音频中点切与叠化,整片观感丝滑;上一镜无存档自动降级普通
			// 渲染,云端/混合链安全(2026-09-09 用户定调直接升级)。
			Steps: 8, Seed: 0, Turbo: true, CondCache: true, Seam: true,
		},
		QC: QCParams{
			Enabled: true, DarkRatioFail: 0.60, DarkRatioWarn: 0.45,
			MinDuration: 3.0, MaxDuration: 18.0,
		},
		Assemble: AssembleParams{
			// 默认叠化过渡(分镜间画面变化大,硬切生硬;需要快节奏硬切可切 cut)
			Transition: "dissolve", BurnSubtitles: true,
			Loudness: -16, Peak: 0.92, SubtitleSize: 4,
			Upgrade2K: false,
		},
	}
}

// Sanitize 钳制参数到合法区间(防手改 JSON 打崩管线)。
func (p *StageParams) Sanitize() {
	if p.Script.MaxShots < 1 {
		p.Script.MaxShots = 1
	}
	if p.Script.MaxShots > 20 {
		p.Script.MaxShots = 20
	}
	if p.Script.TotalSeconds < p.Script.MaxShots*4 {
		p.Script.TotalSeconds = p.Script.MaxShots * 7
	}
	if p.Assets.Source != "upload" && p.Assets.Source != "krea2" && p.Assets.Source != "skip" {
		p.Assets.Source = "upload"
	}
	r := &p.Render
	if r.Engine != "auto" && r.Engine != "comfy" && r.Engine != "api" {
		r.Engine = "auto"
	}
	if r.Width < 32 {
		r.Width = 768
	}
	if r.Height < 32 {
		r.Height = 1344
	}
	r.Width, r.Height = r.Width/32*32, r.Height/32*32
	if r.Steps < 1 {
		r.Steps = 8
	}
	if r.Steps > 30 {
		r.Steps = 30
	}
	if p.QC.DarkRatioFail <= 0 || p.QC.DarkRatioFail > 1 {
		p.QC.DarkRatioFail = 0.60
	}
	if p.QC.DarkRatioWarn <= 0 || p.QC.DarkRatioWarn > 1 || p.QC.DarkRatioWarn > p.QC.DarkRatioFail {
		p.QC.DarkRatioWarn = 0.45
	}
	switch p.Assemble.Transition {
	case "cut", "fade", "dissolve":
	default:
		p.Assemble.Transition = "cut"
	}
	if p.Assemble.Loudness == 0 {
		p.Assemble.Loudness = -16
	}
	if p.Assemble.Peak <= 0 || p.Assemble.Peak > 1 {
		p.Assemble.Peak = 0.92
	}
	if p.Assemble.SubtitleSize <= 0 {
		p.Assemble.SubtitleSize = 4
	}
}
