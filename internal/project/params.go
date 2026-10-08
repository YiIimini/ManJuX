package project

import (
	"regexp"
	"strings"
)

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
	// Views 角色多视角转台图（**默认关**，2026-09-27 起：三视图板接手角度参考）：定妆照 → H3 原地转身 → 抽帧拼转台图,渲染时作该角色
	// 的第二张参考。缺了就补,是"角色只有一张正面照"的直接解法。
	// 代价是每个角色一次单镜渲染(数分钟,仅首次;补完写回资产库,全局摊一次)。
	//
	// 用 *bool 而不是 bool:老项目 json 里根本没有这个键,解成 false 会让新能力
	// 在存量项目上默认关闭(用户报的正是存量项目里角色只有正面照)。nil 语义 =
	// 「未表态」（老项目 json 没这个键）→ 按**默认关**处理，见 ViewsOn()。
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
//
// 两个取值用 `pipeline` 的常量对照着读（`ViewSourceEdit` / `ViewSourceH3`）——
// 但**不在 project 里 import pipeline**（那会成环，本仓的依赖纪律是单向无环），
// 所以这里是字面量；`viewsource_test.go` 有一条门钉住两边的字符串一致。
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

// ViewsOn 多视角（转台图 / 背面·侧面·四分之三）是否生成。**默认关**。
//
// 2026-09-27 用户定调：「有了三视图，就不需要转台图、背面、侧面、四分之三了」——
// 三视图细节板接手了角度参考（见 pipeline/refs.go 的 character_angles 兜底），
// 而转台图那条要跑一次 H3 **转身视频** + 抽帧 + 横向拼图，是资产阶段最贵的一步；
// 背面/侧面/四分之三那几张是逐张编辑，同样是为"没有板子时"准备的。
// 显式写 `views: true` 仍会生成（要对比两条路、或某本书的板子出不来时留的后门）。
func (a AssetsParams) ViewsOn() bool { return a.Views != nil && *a.Views }

// Bool 返回可比较的布尔值(供需要显式 true/false 的调用方使用)。
func Bool(v bool) *bool { return &v }

// RenderParams 渲染阶段(render)参数。
type RenderParams struct {
	Engine    string `json:"engine"` // auto / comfy / api
	Accel     string `json:"accel"`  // 加速档:refine(精修:不挂蒸馏LoRA+完整步数) / quality(Acc-8Step质量) / turbo(官方Turbo,默认) / express(极速预览:盘上有 TaoMate 三步 LoRA 就用它@3 步,否则 turbo@4 —— 只用于抽卡/筛方向)
	Width     int    `json:"width"`  // 32 整除,短边≤768
	Height    int    `json:"height"`
	Steps     int    `json:"steps"`      // turbo 4-8 / 基线 20
	Seed      int64  `json:"seed"`       // 0=每镜随机;非 0=全项目固定
	CondCache *bool  `json:"cond_cache"` // 两阶段渲染(条件缓存);nil = 未表态,回退全局设置
	Seam      bool   `json:"seam"`       // 镜间连续接缝(MotionContext;仅本地 ComfyUI;相邻镜同分辨率)
	// SeamResetEvery 接缝重置间隔(镜):接缝把上一镜尾部的 22 帧钉进本镜头部,
	// 而换场/换人时钉进去的就是**另一个地方/另一个人**的画面,误差还会逐链累积。
	//   0 或 1 = 每场戏重置一次(默认:同场景+同角色才接,见 pipeline.seamBlockers);
	//   N > 1  = 每 N 镜强制重建一次锚点(同场同人也断,把累积漂移清零)。
	// 缺键(老项目)= 0,与默认行为一致;非零值会进渲染指纹(改了就重出,不会静默沿用)。
	SeamResetEvery int `json:"seam_reset_every"`
}

// CondCacheOn 条件缓存是否生效:未表态(nil)按开处理。
//
// 为什么是三态而不是 bool:这个开关既要能"由项目显式关掉"(此前是假开关:项目里关掉、
// 只要全局开着就照样写 .pt,那个目录堆到过 4.9GB),又不能在**缺键**的旧 project.json 上
// 被零值误读成"关" —— bool 的 false 同时表示"用户关了"和"这个字段还没有",
// 一旦传给引擎就会把"没表态"当成"显式关闭",把缓存静默关掉(慢一倍且无提示)。
func (r RenderParams) CondCacheOn() bool { return r.CondCache == nil || *r.CondCache }

// QCParams 质检阶段(qc)参数。
type QCParams struct {
	Enabled       bool    `json:"enabled"`
	DarkRatioFail float64 `json:"dark_ratio_fail"` // 暗像素占比 ≥ 此值判失败(默认 0.60)
	DarkRatioWarn float64 `json:"dark_ratio_warn"` // 暗像素占比 ≥ 此值警告(默认 0.45)
	MinDuration   float64 `json:"min_duration"`    // 单镜最短时长(秒)
	MaxDuration   float64 `json:"max_duration"`    // 单镜最长时长(秒)
	EnableVLM     bool    `json:"enable_vlm"`      // VLM 智能审片(抽帧判分+重渲建议,需视觉模型)
	// EnableTextDetect 画面文字检测(技能侧清单 §3.1a):抽帧问视觉模型"画面里有没有被
	// 烧进去的可读文字",命中即告警 —— 用来长期盯「台词被模型画成字幕」这件事。
	//
	// 为什么是三态(与 Views/CondCache/Denoise 同一套语义):它的默认是**开**,而
	// bool 的 false 同时表示"用户关了"和"这个字段还没有" —— 用 bool 的话,存量项目
	// (json 里没有这个键)会被静默读成"显式关闭",于是这条监控在**最需要它的老项目**上
	// 一次都不跑。取指针后:缺键 = 未表态 = 开,用户显式关掉才是关。
	EnableTextDetect *bool `json:"enable_text_detect"`
	// EnableProportionCheck 定妆照**比例自检**(技能侧清单 §5.1):定妆照出图后按技能侧的
	// 比例锚点核对三庭/眼距/头身比,明显偏离就告警(并建议重出该卡)——"丑"以前是主观的,
	// 有锚点后它就是可测的。它跑在**资产生成**时(每张新出的定妆照一次视觉调用),
	// 所以和画面文字检测一样放在质检参数里:它是质检功能,只是执行时机在资产阶段。
	//
	// 与 EnableTextDetect 同一套三态语义,理由也相同:默认**开**,而 bool 的 false 同时
	// 表示"用户关了"和"这个字段还没有" —— 存量项目会被静默读成"显式关闭"。
	EnableProportionCheck *bool `json:"enable_proportion_check"`
	// EnableASR 语音核对(2026-10-07,用户「配音存在乱配音」):渲染完成后用本机 ASR
	// (faster-whisper,D:\Ai\Tools\ASR)把每镜音轨转写一遍,再与计划台词逐句对账 ——
	// 判「念的是不是那句台词」:重复念、凭空加话(剧本里没有)、整句漏念。
	//
	// 【为什么默认开】它是这条链上**唯一**能看见"乱配音"的机器判据:音轨里说了什么,
	// 此前只有"有没有人声/人声多长"两个数可量(字幕是平台自己烧的,跟音轨是两回事)。
	// 代价是每镜 10~15 秒 CPU(不与 ComfyUI 抢显存),一集 13 镜约 3 分钟 —— 换来的是
	// 真机第01集当场抓到的三类硬伤。没装 ASR 的机器上如实播报"跳过",不假装验过。
	//
	// 与 EnableTextDetect 同一套三态语义(默认开,缺键不能读成"显式关闭")。
	EnableASR *bool `json:"enable_asr"`
	// ASRModel 转写模型档(tiny/base/small/medium/large-v3);空 = medium。
	// 更大的档更准也更慢,与渲染显存无关(ASR 固定走 CPU)。
	ASRModel string `json:"asr_model,omitempty"`
	// EnablePitch 音色核对(2026-10-08,用户「女的配男音」):量每镜音轨的**中位基频**,
	// 与角色卡上那段权威声线对一遍 —— 判「是不是这个人在念」。
	//
	// 【为什么默认开】它不做任何模型调用(只解码音轨 + 自相关),代价几乎为零,而它是
	// 这条链上唯一能看见"声线配错了"的机器判据(提示词写对了 H3 也可能不照做 —— 真机实测)。
	// 判负的处置与 ASR 一致:只标记、不拦批次;渲染阶段还会据此**换种子有界重出**(见 voice_check.go)。
	// 与 EnableTextDetect 同一套三态语义(默认开,缺键不能读成"显式关闭")。
	// 与 EnableASR 一样**不带 omitempty**：字段必须在 API 响应里始终出现，
	// 否则前端那个开关会因为"键不存在"显示成关，而后端把 nil 当**开** —— 界面与行为对不上。
	EnablePitch *bool `json:"enable_pitch"`
}

// PitchOn 音色核对是否生效:未表态(nil)按开处理(理由与 ASROn 相同)。
// 关掉它连报告条目都不写 —— "关掉"看起来就是"没这项",而不是"每镜都验过且干净"。
func (q QCParams) PitchOn() bool { return q.EnablePitch == nil || *q.EnablePitch }

// ASROn 语音核对是否生效:未表态(nil)按开处理(理由与 TextDetectOn 相同)。
// 关掉它连报告条目都不写(见 pipeline.planSpeechCheck)——"关掉"看起来就是"没这项",
// 而不是"每镜都验过且干净"。
func (q QCParams) ASROn() bool { return q.EnableASR == nil || *q.EnableASR }

// ASRModelOr 转写模型档:空/非法一律回落 medium(与 asr.Transcribe 的默认一致)。
func (q QCParams) ASRModelOr() string {
	m := strings.TrimSpace(q.ASRModel)
	for _, ok := range []string{"tiny", "base", "small", "medium", "large-v3"} {
		if m == ok {
			return m
		}
	}
	return "medium"
}

// TextDetectOn 画面文字检测是否生效:未表态(nil)按开处理。
//
// 关掉它不影响其它质检项 —— 报告里连条目都不写(见 pipeline.planTextDetect),
// 于是"关掉"看起来就是"没这项",而不是"每镜都验过且干净"。
func (q QCParams) TextDetectOn() bool { return q.EnableTextDetect == nil || *q.EnableTextDetect }

// ProportionCheckOn 定妆照比例自检是否生效:未表态(nil)按开处理。
//
// 关掉它连事件都不发(见 pipeline.planProportionCheck 与 stages_assets.go 的接线),
// 于是"关掉"就是"没这项",而不是"每张卡都量过且合格"。
func (q QCParams) ProportionCheckOn() bool {
	return q.EnableProportionCheck == nil || *q.EnableProportionCheck
}

// AssembleParams 合成阶段(assemble)参数。
type AssembleParams struct {
	// BGM 这一集的**单条**配乐文件（相对项目目录或绝对路径；空 = 不铺配乐）。
	// 官方 qc-checklist STEP 8：成片是一条连续 BGM，**片段里不各带各的**（见 compose/bgm.go）。
	BGM string `json:"bgm"`
	// BGMDescription 这一集**配乐需求的自然语言描述**（风格/情绪/乐器/节奏，例：
	// "低沉竹笛与弦乐，压抑，慢速，碑下独白段落"）。
	//
	// 用途（2026-09-28 用户要求："生成的背景音乐也要做成资产库里进行管理，后续存在相同的可以直接复用"）：
	// 配乐按**需求**进资产库，同一份需求在不同集/不同项目里会算出同一个条目名，命中即免重生成。
	// 解析优先级见 pipeline 的 bgmPathOf：显式文件 assemble.bgm ＞ 库内同需求条目 ＞（将来）现场生成。
	BGMDescription string  `json:"bgm_description,omitempty"`
	Transition     string  `json:"transition"`     // cut / fade / dissolve
	BurnSubtitles  bool    `json:"burn_subtitles"` // 烧录字幕
	Loudness       int     `json:"loudness"`       // 目标响度 LUFS(默认 -16)
	Peak           float64 `json:"peak"`           // 峰值归一(默认 0.92 → loudnorm 的 TP≈-0.72 dBTP)
	SubtitleSize   int     `json:"subtitle_size"`  // 字幕字号(屏高百分比,默认 4)
	// SubtitleMarginV 字幕下边距(屏高百分比,默认 15)。
	//
	// 为什么是 15(红果/抖音对标 §1.7):平台在画面底部盖着 UI(进度条、账号信息条),
	// 创作者侧口径是字幕要避开底部约 15% 的区域;旧实现写死 8% —— 恰好压在危险区里。
	// 这里只做钳制,真正决定像素值的地方在 compose(subtitleMarginVPctDefault),
	// 且吃的是**交付高度**(1080×1920 交付时 15% = 288px)。
	SubtitleMarginV int `json:"subtitle_margin_v"`
	// DeliverWidth/DeliverHeight 交付分辨率(成片最终画幅)。
	//
	// 为什么要跟 render.width/height 分开(红果/抖音对标 §3 的 G1):`render.width/height`
	// **同时**驱动 H3 的生成尺寸与成片的交付尺寸,而 H3 本地渲染的短边上限是 768、
	// 平台交付口径是 1080×1920 —— 旧实现里"把交付升到 1080"做不到(改了 H3 会渲不出来),
	// 于是把 768→1080 的放大工作留给了平台。现在渲染归渲染、交付归交付:
	// 成片在终混时缩放到这里填的尺寸,且**缩放在调色与字幕之前**(锐化/颗粒因此作用在
	// 交付像素上,字幕也按交付画幅排版)。
	//
	// 0/0 = 未表态 → **按画幅取交付标准**(竖屏 1080×1920 / 横屏 1920×1080)。
	// 为什么默认敢自动放大(而不是"跟随渲染"):交付标准不是风格选择,而"指望人记得去
	// 项目参数里改"正是本项目反复吃过的亏(见 GradeAuto 的说明)。要按渲染尺寸原样交付的
	// 项目,把这两个值显式填成渲染尺寸即可(那时不缩放,与解耦之前完全一致)。
	//
	// 只填一边视为未表态(坏配置不猜另一半);奇数边长会被向下取偶(yuv420p 要求)。
	DeliverWidth  int `json:"deliver_width"`
	DeliverHeight int `json:"deliver_height"`
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
	// Grade 调色档(甲1 / 技能侧《平台侧整改清单》§2.1):
	// auto 按风格自动 / off 不调色 / soft 轻调 / film 电影感 / heavy 重调。
	//
	// 空串 = 未表态 → 与 GradeAuto 同义(老项目 json 里没有这个键,解成空串)。
	// **未表态不再等价于 film**:技能侧《翻篇》的风格串是动画电影
	// (ANIMATED-FEATURE CINEMATIC LOOK…),film 的压饱和 + 暗角 + 颗粒叠在动画赛璐璐上
	// 偏重;双方约定"观感偏重时优先调档位、不动风格串",所以未表态时由 EffectiveGrade
	// 按 plan.Style 选档(动画电影 → soft,其余 → film)。
	//
	// 最终档位只在 EffectiveGrade 一处决定(合成入口调用它一次)。compose.NormalizeGrade
	// 仍是装载之后的最后一道滤镜口径,它把空值/认不出的值按 film 兜底 —— 也就是说
	// "auto" 万一漏到 compose(老版本读新快照),退化成旧的 film 行为,不会把调色关掉。
	Grade string `json:"grade"`
}

// GradeAuto 调色档"自动":不写死档位,按项目风格串选默认档(动画电影 → soft,其余 → film)。
//
// 为什么要有这一档(技能侧《平台侧整改清单》§2.1):平台默认档一直是写死的 film,
// 而技能侧《翻篇》的风格串就是动画电影类,film 叠上去偏重 —— 技能侧只能在项目
// `立项.json` 里人工记一句"建议 soft",而"指望人不漏不现实"正是那次事故的成因。
// 所以默认档必须能跟着风格走,但**只在项目没显式表态时**接管(选了档位就照用户的)。
//
// 存盘用 "auto" 而不是空串:空串在快照里看着像"这个键没写",而它其实是一个明确语义;
// 且老版本读到 "auto" 会走 compose.NormalizeGrade 的兜底(= film,与旧行为一致)。
const GradeAuto = "auto"

// EffectiveGrade 解析**最终**调色档,并回答"这一档是不是按风格自动降下来的"。
//
// 全项目只有这一处决定最终档位:合成入口(Coordinator.runAssemble)调它一次,compose 只认
// 结果。本项目吃过"同一语义两处实现"的亏(project.Sanitize 与 compose.NormalizeGrade 的
// 同口径双写就是被逼出来的),所以判据不再散到别处,也不在 compose 里再判一次。
//
// 优先级:**显式表态 > 按风格自动**。off/soft/film/heavy 一律照用户/技能侧的;
// 只有未表态(空串 / auto)才看风格,且认不出的脏值与 Sanitize 同口径落回 film。
//
// style 传 plan.Style(方案里的视觉风格串,英文;技能侧直出);空串/认不出按"非动画"处理。
func (a AssembleParams) EffectiveGrade(style string) (grade string, autoDowngraded bool) {
	switch g := strings.ToLower(strings.TrimSpace(a.Grade)); g {
	case "off", "soft", "film", "heavy":
		return g, false
	case "", GradeAuto:
		if animatedFeatureStyle(style) {
			return "soft", true
		}
		return "film", false
	default:
		return "film", false
	}
}

// animatedStyleKeywords "动画电影"类风格串的关键词(小写;命中即算)。
//
// 口径来自技能侧清单 §2.1:ANIMATED-FEATURE / donghua / anime。
// 刻意**不用**"卡通/2D/animation"这类更宽的词:风格串带否定式条款是常态(《翻篇》结尾
// 就写着 "no flat 2D cartoon look, no ink outlines, no cel shading"),宽词会把
// "我不要卡通"读成"我要卡通" —— pipeline.artstyle 的 photorealRequested 栽过同一个跟头
// ("NO PHOTOREAL DETAIL" 被当成"要写实",见那里的记录)。
var animatedStyleKeywords = []string{
	"animated-feature", "animated feature", "animated film", "feature animation",
	"donghua", "anime", "动画电影",
}

// reStyleNegation 否定词(整词边界,免得削到 "noir"/"notebook" 这类词)。
var reStyleNegation = regexp.MustCompile(`\b(no|not|without|avoid|never|nor|zero)\b`)

// animatedFeatureStyle 风格串是不是"动画电影"类。
//
// 判据只认**正面描述**:先把否定式剥掉,再在剩下的部分里找关键词。不剥的话
// "no anime look" / "no animated-feature stylisation" 这类否定恰好会把判据反过来,
// 于是"明确不要动画"的写实项目反而被降档 —— 与 artstyle.photorealRequested 同一取向。
//
// 与 pipeline.StripNegations 的关系:那边服务于提示词清洗(还要处理大小写、逗号拼接、
// 段内否定短语的保留策略),这边只服务于"是不是动画电影"这一个布尔判断,所以是一份
// **窄化的兄弟实现** —— project 不能反向依赖 pipeline(见 Sanitize 里同口径双写的说明)。
// 两侧的否定词表保持同口径,改一处要看另一处。
func animatedFeatureStyle(style string) bool {
	if strings.TrimSpace(style) == "" {
		return false
	}
	var kept []string
	for _, seg := range strings.Split(strings.ToLower(style), ",") {
		s := strings.TrimSpace(seg)
		if s == "" {
			continue
		}
		// 段内出现否定词(含段首)时只保留它**前面**的正面描述:
		// "soft matte fur, no oily highlights" → "soft matte fur"。
		if i := reStyleNegation.FindStringIndex(s); i != nil {
			s = strings.TrimSpace(s[:i[0]])
		}
		if s != "" {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return false
	}
	positive := strings.Join(kept, ", ")
	for _, k := range animatedStyleKeywords {
		if strings.Contains(positive, k) {
			return true
		}
	}
	return false
}

// DenoiseOn 是否启用音频净化(未表态 = 开,与 ViewsOn/FacesOn 同一套语义)。
func (a AssembleParams) DenoiseOn() bool { return a.Denoise == nil || *a.Denoise }

// DeliverMinEdge/DeliverMaxEdge 交付分辨率的合法边长区间。
//
// 下限 64:再小不可能是"交付",多半是把字号/百分比填错了格;
// 上限 8192:4K 以上没有交付场景(对标文档明确"不要上 4K"),留出余量即可。
const (
	DeliverMinEdge = 64
	DeliverMaxEdge = 8192
)

// clampDeliverEdge 钳制 + 向下取偶(见 Sanitize;x264 的 yuv420p 要求偶数边长)。
func clampDeliverEdge(v int) int {
	if v < DeliverMinEdge {
		v = DeliverMinEdge
	}
	if v > DeliverMaxEdge {
		v = DeliverMaxEdge
	}
	return v / 2 * 2
}

// DefaultStageParams 返回全局默认的阶段参数。
func DefaultStageParams() StageParams {
	return StageParams{
		// MaxShots=0:内容驱动(2026-09-11 用户定调「该多少就多少」)——LLM 看
		// 原文自行决定镜头数,不人为钳制;Sanitize 上限 30 只作安全网
		//
		// Styles 默认**国风电影级**(2026-09-27 改):原默认是 `guoman_2d`(国漫二维动画),
		// 而平台 LLM 直出的提示词(director)当时也明文要求"必须是动画风格" —— 两层叠起来,
		// 用户看到的就是「平台端 LLM 解析渲染的内容贴合原文,但为什么是 2D 漫画风格」。
		// 新默认与技能侧《立项.json》的「渲染风格串」同源(用户 2026-09-26 规则:
		// 「一切创作产物的电影级画面风格必须是国风系」),这样两条产品线的默认画风一致:
		// 同一部剧走平台直出或走技能端脚本,出来的画风不会变成两种。
		// 仍然保留"非真人照片脸"的法务底线(半风格化理想脸 —— 见 pipeline/artstyle.go);
		// 要真人实拍的项目自己换成 live_action 并关掉 script.stylized_faces,
		// 要二维动画的自己换回 guoman_2d(预设表里都还在)。
		Script: ScriptParams{MaxShots: 0, TotalSeconds: 0, StyleHint: "",
			Styles: []string{"guofeng_cinematic"}, StylizedFaces: Bool(true)},
		// "krea2" 是**资产获取方式**的历史取值(含义:本地模型自动定妆),
		// 与实际用哪个图像模型无关 —— 模型由 settings.comfy.image_family 决定
		// (见 comfyengine/imagefamily.go)。资产文件上记录的 Source 才是"谁生成的"。
		Assets: AssetsParams{Source: "krea2", Views: Bool(false)},
		Render: RenderParams{
			Engine: "auto", Width: 768, Height: 1344,
			// Seam 默认开:镜间连续(MotionContext)让相邻镜头运镜/画面延续,
			// 配合音频中点切与叠化,整片观感丝滑;上一镜无存档自动降级普通
			// 渲染,云端/混合链安全(2026-09-09 用户定调直接升级)。
			Steps: 8, Seed: 0, CondCache: Bool(true), Seam: true,
			// SeamResetEvery 默认 0 = 每场戏重置(同场景 + 有共同出场角色才接缝,
			// 见 pipeline.seamBlockers):接缝只在"同一场戏的连续镜头"之间才有意义,
			// 换场/换人时钉进去的 22 帧是另一处画面,误差还会逐链累积。
			SeamResetEvery: 0,
		},
		QC: QCParams{
			Enabled: true, DarkRatioFail: 0.60, DarkRatioWarn: 0.45,
			MinDuration: 3.0, MaxDuration: 18.0,
			// 画面文字检测默认开:它要跑的是一次视觉调用/镜,但换来的是"字幕有没有被
			// 模型烧进画面"这条长期监控(技能侧清单 §3.1a)——不默认开就等于又要靠人记得查。
			// 未配置视觉模型时如实降级(报告里写"未配置视觉模型"),不会假装通过。
			EnableTextDetect: Bool(true),
			// 定妆照比例自检默认开(技能侧清单 §5.1):每张**新出**的定妆照一次视觉调用,
			// 换来"人物比例有没有偏离技能侧锚点"这条可测的信号 —— 不默认开,就又要靠人
			// 拿肉眼盯"丑不丑"。未配置视觉模型时如实降级(事件流里写明"未做",不假装通过)。
			EnableProportionCheck: Bool(true),
			// 语音核对默认开(2026-10-07,用户「配音存在乱配音」):渲染完成后把每镜音轨
			// 转写一遍再与计划台词对账 —— 这是这条链上唯一能看见"念的是不是那句台词"的
			// 机器判据,代价是每镜 10~15 秒 CPU(不与 ComfyUI 抢显存)。没装 ASR 的机器上
			// 如实播报"跳过",不假装验过、也不当作不合格。
			EnableASR: Bool(true),
			// 音色核对默认开(2026-10-08,用户「女的配男音」):量每镜音轨的**中位基频**与角色卡上
			// 那段权威声线对账 —— 不做任何模型调用,代价几乎为零,却是这条链上唯一能看见
			// "声线配错了"的机器判据(提示词写对了 H3 也可能不照做)。判负只标记、不拦批次。
			EnablePitch: Bool(true),
		},
		Assemble: AssembleParams{
			// 默认叠化过渡(分镜间画面变化大,硬切生硬;需要快节奏硬切可切 cut)
			// fade=平滑交叉叠化(用户 2026-09-10 定调:不要 dissolve 像素溶解的粒子观感)
			Transition: "fade", BurnSubtitles: true,
			Loudness: -16, Peak: 0.92, SubtitleSize: 4, SubtitleMarginV: 15,
			// 交付分辨率:0/0 = 按画幅自动(竖屏 1080×1920 / 横屏 1920×1080)。
			// 不在这里写死尺寸 —— 写死就意味着横屏项目也要照抄竖屏的交付档,
			// 而"按画幅算"这件事只有一个实现点(compose.deliverSize)。
			DeliverWidth: 0, DeliverHeight: 0,
			Upgrade2K: false, Denoise: Bool(true),
			// 电影调色默认开(H3 直出的平淡数码味是用户反复提的观感问题;
			// 要"一点不动"的项目显式选 off),但**档位不写死**:
			// 默认是"自动" —— 未表态时按 plan.Style 选档(动画电影 → soft,
			// 其余 → film,见 EffectiveGrade)。技能侧清单 §2.1:动画电影类风格串
			// (《翻篇》的 ANIMATED-FEATURE CINEMATIC LOOK…)配 film 偏重,而
			// "指望人记得去项目参数里改成 soft"就是那次漏掉的原因。
			Grade: GradeAuto,
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
	// QC 段**整个缺失**时补默认（开），而不是留成 false（关）。
	//
	// 【2026-09-28 审计 A11】`QC.Enabled` 是普通 bool，而 `Sanitize` 只校验"非法值"、
	// 不管"缺键" —— 于是任何**没有 qc 段**的 project.json（老项目、技能侧导入的包、
	// 手工改过的配置）加载后 `Enabled=false`，**质检被静默关掉**：暗帧/黑屏/画面文字
	// 检测一次都不跑，而用户在设置页看到的是那个开关的默认勾选状态，以为在质检。
	//
	// 判据用"整段零值"：`defaults()` 里 QC 的每一项都有非零默认（Enabled=true、
	// DarkRatioFail=0.60…），所以**整段全零只可能来自"这个键没写"**，不可能是用户真配成这样。
	// 取舍（写清楚免得下次被质疑）：若哪天有人把 qc 全部显式配成零值（含 Enabled=false），
	// 这里会把它当成"缺键"补回默认 —— 那种配置没有实际意义（等于关掉质检且各项阈值全 0），
	// 而"缺键导致静默关质检"是真实发生过的事故，两害相权取此。
	if q := p.QC; q.Enabled == false && q.DarkRatioFail == 0 && q.DarkRatioWarn == 0 &&
		q.MinDuration == 0 && q.MaxDuration == 0 {
		p.QC = DefaultStageParams().QC
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
	// 字幕下边距:未表态(0/负)落回默认 15%,上限 30% ——
	// 再大就把字幕推到画面中线以上,那不是"安全区"而是"字幕跑到脸上了"。
	//
	// 与 compose 的关系:compose.subtitleMarginVPct 对 0 也是同一个默认值(同口径双写,
	// 项目不能反向依赖 compose,所以只能两处各写一份,改一处要看另一处)。
	if p.Assemble.SubtitleMarginV <= 0 {
		p.Assemble.SubtitleMarginV = 15
	}
	if p.Assemble.SubtitleMarginV > 30 {
		p.Assemble.SubtitleMarginV = 30
	}
	// 交付分辨率(见 AssembleParams.DeliverWidth 的说明):
	//   - 只填一边 = 坏配置 → 两边都归 0(未表态,交给 compose 按画幅取交付标准),不猜;
	//   - 成对填写 → 钳到 [64, 8192] 并向下取偶(yuv420p 不接受奇数边长);
	//   - 0/0 → 原样保留(它是"按画幅自动"这一显式语义,不是"没填好")。
	dw, dh := p.Assemble.DeliverWidth, p.Assemble.DeliverHeight
	switch {
	case dw <= 0 || dh <= 0:
		p.Assemble.DeliverWidth, p.Assemble.DeliverHeight = 0, 0
	default:
		p.Assemble.DeliverWidth = clampDeliverEdge(dw)
		p.Assemble.DeliverHeight = clampDeliverEdge(dh)
	}
	// 调色档归一:未表态(空串 / auto)归成 GradeAuto;四个规范档位原样保留
	// (大小写/空白不敏感);其余认不出的脏值落回 film。
	//
	// 大小写/空白不敏感 —— 与 compose.NormalizeGrade 的宽容度对齐,否则
	// "OFF" 在这里被改成 film(调色照开)、在 compose 里却被认成 off,同一份配置两处口径。
	// 取值集合(四个规范档位 + auto)必须与 compose.NormalizeGrade 一致;那边额外容忍
	// none/light/strong 这类别名(手改 JSON 时的兜底),这里只管界面会写出来的规范值。
	// project 不能反向依赖 compose,所以是刻意保留的同口径双写,改一处必须改另一处。
	//
	// 为什么"未表态"必须保留成 GradeAuto、不能就地定成 film(原实现就是在这里落回 film):
	// 这个函数是**保存项目参数**那一处调用的 —— 一旦在这里定死成 film,凡是开过一次
	// 参数抽屉并保存的项目就再也不是"未表态",§2.1 的自动降档会静默失效(而用户
	// 明明什么都没改)。"认不出的脏值落回 film"保持不变:它不该放行自动降档。
	switch g := strings.ToLower(strings.TrimSpace(p.Assemble.Grade)); g {
	case "off", "soft", "film", "heavy":
		p.Assemble.Grade = g
	case "", GradeAuto:
		p.Assemble.Grade = GradeAuto
	default:
		p.Assemble.Grade = "film"
	}
}
