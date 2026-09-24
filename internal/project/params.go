package project

import "strings"

// StageParams 管线各阶段参数(项目级完整快照,创建项目时从全局默认拷贝,
// 之后与全局默认互不影响 —— 实现「单项目独立调优」)。
type StageParams struct {
	Script   ScriptParams   `json:"script"`
	Assets   AssetsParams   `json:"assets"`
	Render   RenderParams   `json:"render"`
	QC       QCParams       `json:"qc"`
	Assemble AssembleParams `json:"assemble"`
}

// MaxShotsCeiling 镜头数安全网上限。0 是合法值(=内容驱动,镜头数由 LLM 按
// 原文节拍自行决定),此处只防手改 JSON 写出天文数字把渲染成本打爆。
const MaxShotsCeiling = 120

// ScriptParams 剧本/分镜阶段(script)参数。
type ScriptParams struct {
	MaxShots     int      `json:"max_shots"`     // 目标镜头数:0=内容驱动,1-120=指定值(>6 走分批生成)
	TotalSeconds int      `json:"total_seconds"` // 目标总时长(秒);0=由镜头数推算
	Styles       []string `json:"styles"`        // 渲染风格预设 key(可多选组合)
	StyleHint    string   `json:"style_hint"`    // 附加风格要求(自由文本)
	Chibi        bool     `json:"chibi"`         // 定妆时同步生成 Q 版形象(内心活动表达)
	// StylizedFaces 角色动漫化 + 非真人脸 + 哑光(去"油腻")。
	//
	// 与 AssetsParams.Views 同样的 *bool 语义:老项目 json 里没有这个键,解成 false
	// 会让新约束对存量项目默认关闭 —— 而存量项目恰恰就是用户报「人物油腻 / 真人脸」
	// 的那一批。未表态 = 开,显式 false 才关(要做真人剧的项目自己关)。
	StylizedFaces *bool `json:"stylized_faces"`
}

// FacesOn 是否启用「动漫化 + 非真人脸 + 哑光」硬约束(未表态 = 开)。
func (s ScriptParams) FacesOn() bool { return s.StylizedFaces == nil || *s.StylizedFaces }

// AssetsParams 资产阶段(assets)参数。
type AssetsParams struct {
	Source string `json:"source"` // upload(手动上传)/ krea2(Krea-2 自动生成)/ skip(纯文生 t2va)
	// Views 角色多视角转台图:定妆照 → H3 原地转身 → 抽帧拼转台图,渲染时作该角色
	// 的第二张参考。缺了就补,是"角色只有一张正面照"的直接解法。
	// 代价是每个角色一次单镜渲染(数分钟,仅首次;补完写回资产库,全局摊一次)。
	//
	// 用 *bool 而不是 bool:老项目 json 里根本没有这个键,解成 false 会让新能力
	// 在存量项目上默认关闭(用户报的正是存量项目里角色只有正面照)。nil 语义 =
	// 「未表态」→ 按默认开处理,见 ViewsOn()。
	Views *bool `json:"views"`
	// CompactRefs 参考图去冗余:只喂转台图、不喂单独那张定妆照(省一张参考图)。
	//
	// **默认关,且推荐保持关**(2026-09-12 实测回退):转台图是 hstack 拼合的 4 格横向图,
	// 模型按入口分辨率缩放它时每一格只剩约 1/4 的像素 —— 定妆照里那张锐利的脸被摊薄,
	// 而"锁脸"靠的正是这张脸的细节。实测只喂拼合图后,渲染出来的人物服装/道具照抄得
	// 很准、脸却变成"另一个更像俊男的版本"(脸更长、眉更重、头发更散),用户当场报
	// 「定妆照/多视角/视频渲染的人物一致性去哪了」。所以默认**两张都喂**:定妆照负责
	// 锐利的脸(锁脸锚点),转台图负责其它角度。仅在明确不需要角度参考、只要省显存时
	// 才打开(显式 true)。
	CompactRefs *bool `json:"compact_refs"`
	// ViewsSource 多视角用哪条路生成:"auto"(默认,见下)/ "edit" / "h3"。
	//
	//   edit = Qwen-Image-Edit-2511 图像编辑:拿定妆照本体改一个角度,身份/配色/画风
	//          按像素保住。每张几秒。
	//   h3   = H3 转身视频抽帧(旧路径)。视频模型会**顺手重新设计**角色(实测:赛璐璐
	//          少年→写实真人、插画铜雀→3D 机械鸟),逐帧身份校验因此拒绝,多视角拿不到。
	//   auto = 编辑链路就绪(权重在 ComfyUI 模型目录里、服务在线)就用 edit,否则 h3。
	//          留空/不识别都按 auto。
	//
	// 之所以留这个开关:①权重没下完的人应当照旧可用;②换模型时需要能 A/B 对比。
	ViewsSource string `json:"views_source"`
}

// ViewsSourceOf 归一化多视角生成方式,返回 "auto" / "edit" / "h3"(不识别一律 auto)。
func (a AssetsParams) ViewsSourceOf() string {
	switch strings.ToLower(strings.TrimSpace(a.ViewsSource)) {
	case "edit", "qwen", "qwen-edit":
		return "edit"
	case "h3", "video":
		return "h3"
	}
	return "auto"
}

// CompactRefsOn 参考图是否去冗余(未表态 = **关**,见 CompactRefs 的实测说明)。
func (a AssetsParams) CompactRefsOn() bool { return a.CompactRefs != nil && *a.CompactRefs }

// ViewsOn 多视角补齐是否开启(未表态 = 开)。
func (a AssetsParams) ViewsOn() bool { return a.Views == nil || *a.Views }

// Bool 返回可比较的布尔值(供需要显式 true/false 的调用方使用)。
func Bool(v bool) *bool { return &v }

// RenderParams 渲染阶段(render)参数。
type RenderParams struct {
	Engine    string `json:"engine"` // auto / comfy / api
	Accel     string `json:"accel"`  // 加速档:quality(Acc-8Step质量) / turbo(官方Turbo,默认) / express(极速预览:int8VAE+4步)
	Width     int    `json:"width"`  // 32 整除,短边≤768
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
	Peak          float64 `json:"peak"`           // 峰值归一(默认 0.92 → loudnorm 的 TP≈-0.72 dBTP)
	SubtitleSize  int     `json:"subtitle_size"`  // 字幕字号(屏高百分比,默认 5)
	// FadeSec 转场时长(秒;fade/dissolve 用)。0 = 用设置页的 render.fade_sec。
	//
	// 为什么要单独一个字段(审计 P1-1):`render.fade_sec` 一直是纯摆设 ——
	// 合成阶段写死 0.5,用户在设置页把它改成 1.0 之后成片里毫无变化。
	// 现在改成"项目参数优先、其次设置页、最后兜底 0.5",两边都真的生效。
	FadeSec   float64 `json:"fade_sec"`
	Upgrade2K bool    `json:"upgrade_2k"` // 云端 2K 定稿:合成完成后自动 regeneration 升 2K(消耗云额度)
	// Denoise 音频净化(高通去隆隆声 + 频谱降噪去底噪/嘶声)。
	//
	// 用户报「视频有杂音」时,声源侧有两条:H3 自己生成的环境底噪,以及各镜 AAC 拼接
	// 带来的嘶声与爆音。前者压不掉(那是提示词写的音景),后者这一链能压。
	// 默认开;追求"原声一点不动"的项目可显式关。
	Denoise *bool `json:"denoise"`
}

// DenoiseOn 是否启用音频净化(未表态 = 开,与 ViewsOn/FacesOn 同一套语义)。
func (a AssembleParams) DenoiseOn() bool { return a.Denoise == nil || *a.Denoise }

// DefaultStageParams 返回全局默认的阶段参数。
func DefaultStageParams() StageParams {
	return StageParams{
		// MaxShots=0:内容驱动(2026-09-11 用户定调「该多少就多少」)——LLM 看
		// 原文自行决定镜头数,不人为钳制;Sanitize 上限 30 只作安全网
		//
		// Styles 默认国漫二维:2026-09-24 用户定调「角色不要真人脸,动漫化,避免侵权」
		// —— 二维赛璐璐既是用户要的画风,也顺带解决"人物油腻"(平涂肤色没有皮脂高光),
		// 且与现实人物天然拉开距离。要真人剧的项目自己把风格换成 live_action 并关掉
		// script.stylized_faces。
		Script: ScriptParams{MaxShots: 0, TotalSeconds: 0, StyleHint: "",
			Styles: []string{"guoman_2d"}, StylizedFaces: Bool(true)},
		Assets: AssetsParams{Source: "krea2", Views: Bool(true)}, // 默认自动定妆:缺失参考图由 Krea-2 生成,不再要求手动上传
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
			// fade=平滑交叉叠化(用户 2026-09-10 定调:不要 dissolve 像素溶解的粒子观感)
			Transition: "fade", BurnSubtitles: true,
			Loudness: -16, Peak: 0.92, SubtitleSize: 4,
			Upgrade2K: false, Denoise: Bool(true),
		},
	}
}

// Sanitize 钳制参数到合法区间(防手改 JSON 打崩管线)。
//
// 注意 MaxShots/TotalSeconds 的 0 必须原样保留 —— 它们不是「未填」,而是
// 「内容驱动」这一显式语义(2026-09-11 用户定调「该多少就多少」)。旧实现把
// MaxShots<1 一律钳成 1,于是首跑正常的十余镜分镜在用户点过一次参数抽屉保存后
// 就退化成「1 镜 / 7 秒」,而下方注释却写着「不人为钳制」,代码与设计相悖。
func (p *StageParams) Sanitize() {
	if p.Script.MaxShots < 0 {
		p.Script.MaxShots = 0
	}
	if p.Script.MaxShots > MaxShotsCeiling {
		p.Script.MaxShots = MaxShotsCeiling
	}
	// 时长同样允许 0(=交给镜头数推算)。仅当用户既指定了镜头数、又给了明显
	// 过短的时长时,才回退到默认节拍(每镜约 7 秒)。
	if p.Script.TotalSeconds < 0 {
		p.Script.TotalSeconds = 0
	} else if p.Script.TotalSeconds > 0 && p.Script.MaxShots > 0 && p.Script.TotalSeconds < p.Script.MaxShots*4 {
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
