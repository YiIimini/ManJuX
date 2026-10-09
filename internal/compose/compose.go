// Package compose 负责合成成片:镜头规范化 → 拼接 → 响度归一化 → 字幕烧录 → faststart。
//
// 拼接与接缝转场**交给成熟的装配引擎**（MoviePy；见 engine.go 与 assemble_mpy.py）：
// 平台只喂输入、读引擎回报的真实时间轴 —— 不再自己拼 ffmpeg 滤镜图（2026-10-09 真机 P0：
// 自写的 xfade 链在"过渡窗口越过前一镜末帧"时静默丢帧、退出码还是 0，成片只剩前 3 镜画面）。
//
// 关键纪律(知识库):H3 源视频仅首帧为 IDR,任何裁剪/拼接必须重编码,
// 禁止流复制;成片必须 +faststart(微信/播放器起播兼容)。
//
// 音频纪律(2026-09-24 用户报「配音也不完整 / 有杂音」后成文,改之前先读):
//   - **接缝用交叉淡入淡出,不切内容**:转场交给引擎后,重叠的两镜音频一进一出地叠,
//     不砍掉任何一段 —— 比旧实现"让出与画面重叠等长的音频"更不容易丢台词;
//   - **每镜音频先补到与画面等长**(apad + -shortest):H3 偶有音轨比画面短的情况,
//     不补的话裁剪区间会越界,只能静默丢帧;
//   - **中间轨一律无损**(flac/mkv):拼接不是"再压一遍 AAC"的地方,否则每过一道
//     就多一层嘶声 —— 这正是"有杂音"的一半来源;
//   - **响度量两遍**(measure → linear 应用):单遍 loudnorm 是逐帧动态调增益,
//     会在对话与静音之间"抽气",听起来像底噪忽大忽小。
package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/audio"
	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// Params 合成参数。
type Params struct {
	// Width/Height 是**渲染**尺寸:它同时决定逐镜规范化的画布,以及 H3 生成时的尺寸。
	//
	// 它**不是**交付尺寸 —— 两者在 2026-09-27 之后解耦(见 DeliverWidth/DeliverHeight)。
	Width, Height int
	// DeliverWidth/DeliverHeight 交付分辨率(项目参数 assemble.deliver_width/height)。
	//
	// 存在的理由(红果/抖音对标实测):H3 本地渲染的短边上限是 768(768×1344),而平台
	// 交付口径是 1080×1920。渲染尺寸不能再涨,于是"把 768 放大到 1080"这一步必须有人做
	// —— 不做就等于把放大交给平台(平台会按自己的算法二次编码)。这里让它由我们自己
	// 在终混时做,并使用 lanczos。
	//
	// 0/0(两个都为 0)= 跟随 Width/Height,即成片与镜头同规格(老项目/不关心交付档的
	// 项目走这条,行为与解耦之前完全一致)。
	DeliverWidth, DeliverHeight int
	Transition                  string // cut / fade / dissolve
	// BGM 成片的**单条**配乐文件路径（项目参数 audio.bgm）。
	// 空 = 不铺配乐（片段里也不再各带各的：这是官方 qc-checklist 的口径，见 bgm.go）。
	BGM string
	// BurnSubtitles 是否把字幕烧进成片。**默认开**。
	//
	// ⚠ **有意分歧 DEV-03（勿当 bug 修）**：官方 qc-checklist STEP 8 要求"除用户明确要求外不加字幕"，
	// 平台默认加 —— 官方那套是给创意短片的，而 ManJuX 做的是**竖屏漫剧**：观众静音刷手机，
	// 字幕是交付的一部分（红果这类平台的标配），且字幕与配音同源于台词的 <d> 段落。
	// 做纯展示向短片时应当关掉 —— 所以它是开关而不是写死。见 docs/与官方的有意分歧.md。
	BurnSubtitles bool
	SubtitleSize  int // 字号(屏高百分比)
	// SubtitleMarginV 字幕下边距(屏高百分比;0 = 默认 15%)。
	//
	// 为什么要抬到 15%(对标实测):抖音/红果在画面底部盖着一层平台 UI,创作者侧口径是
	// 字幕要避开底部约 15% 的区域。旧实现写死 8% —— 恰好压在危险区里,手机上会被
	// 进度条/文案条压住。(取值下限见 subtitleMarginVPct 的说明。)
	SubtitleMarginV int
	Loudness        int     // LUFS
	FadeSec         float64 // 转场时长
	// Peak 真峰值上限 dBTP(loudnorm 的 TP;项目参数 assemble.peak,默认 0.92 线性
	// 即约 -0.72 dBTP)。
	//
	// 为什么必须由调用方给(审计 P1-1):`assemble.peak` 这个参数一直存在、界面能调、
	// Sanitize 也在校验,但真正生效的地方(两处 loudnorm 滤镜串)写死 `TP=-1.5` ——
	// 调它没有任何效果。0 表示"用默认值"(调用方不关心时的安全行为)。
	Peak float64
	// Denoise 音频净化:高通去隆隆声 + 频谱降噪去嘶声/底噪(默认开)。
	Denoise bool
	// Grade 调色档:off / soft / film / heavy,空串与不认识的值都按 film(默认开,
	// 见 GradeFilter/NormalizeGrade)。档位在终混统一应用,且排在字幕之前。
	Grade string
	// ToolDir 装配引擎的 **python 运行环境落点**（可选）:拼接脚本(assemble_mpy.py,
	// MoviePy)需要一个装有 moviepy 的解释器。查找顺序:环境变量 MANJUX_MPY_PYTHON
	// → <ToolDir>/mpy-venv/Scripts/python.exe → 首次引导创建(uv 优先,退回 python -m venv)。
	// 空 = 用系统临时目录下的默认落点。管线传的是 <project.Store>/tools。
	ToolDir string
}

// defaultPeakLinear peak 参数的默认值(0.92 线性 ≈ -0.72 dBTP)。
const defaultPeakLinear = 0.92

// subtitleMarginVPctDefault 字幕下边距的默认值(屏高百分比)。
//
// 15% 是**下限口径**:创作者侧对标的说法是"字幕要避开底部约 15% 的平台 UI 区"
// (进度条、账号信息条都盖在那里),所以下边距取 15% 恰好是安全区的边界,
// 而不是往下贴。旧实现写死 8% —— 手机上会被平台 UI 压住下半截。
// 上限在 project.Sanitize 里钳(30%),免得改大之后字幕跑到画面中线以上。
const subtitleMarginVPctDefault = 15

// peakDBTP 把项目的线性 peak 参数折算成 loudnorm 的 TP(dBTP)。
//
// 换算:dBTPS = 20·log10(线性峰值)。0.92 → -0.72 dBTP,比旧写死的 -1.5 略紧
// (更响一点),这正是设置页那个数字原本的意思。
func peakDBTP(linear float64) float64 {
	if linear <= 0 || linear > 1 {
		linear = defaultPeakLinear
	}
	return 20 * math.Log10(linear)
}

// Result 合成产物。
type Result struct {
	FinalPath string
	SRTPath   string
	Duration  float64
	// Width/Height 成片的实测画幅(= 交付分辨率)。
	//
	// 为什么要回传:交付分辨率与渲染尺寸解耦之后,"成片到底多大"不再是配置里那个数
	// (可能被放大、可能按画幅自动取标准档、奇数边长还可能被取偶)—— 事件流里报一个
	// **实测**值,用户才不用去追配置推断。探测失败时为 0(调用方不显示)。
	Width, Height int
	// AudioNotes 音频处理实况(切点是否落在静音、降噪是否生效),供管线如实回显。
	AudioNotes []string
	// MasterPath 768p 母版的落盘路径（空 = 没写成，播报里已有原因）。
	// 它是云端「整片 2K 定稿」的合法源（与渲染同规格 + 含音轨），见 master.go。
	MasterPath string
}

// Compose 执行合成。clips 为有序镜头文件(绝对路径),plan 用于字幕。
func Compose(ctx context.Context, f *ff.FF, clips []string, plan *project.Plan, outDir string, p Params, onProgress func(pct int, note string)) (*Result, error) {
	if len(clips) == 0 {
		return nil, fmt.Errorf("没有可合成的镜头")
	}
	work, err := os.MkdirTemp(outDir, "compose-")
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if !failed {
			os.RemoveAll(work)
		} else {
			os.Rename(work, work+"-failed") // 保留排查现场
		}
	}()
	res := &Result{}
	// announce 把"静默兜底"改成如实播报:画幅补黑边、缺音轨、转场参数被改写都走这里。
	announce := func(msg string) {
		if onProgress != nil {
			onProgress(5, msg)
		}
	}

	// ---- 步骤一:逐镜规范化(重编码,统一分辨率/fps/采样)----
	if onProgress != nil {
		onProgress(5, "镜头规范化(重编码)")
	}
	// 色彩口径(乙3):先把全片的色彩元数据量一遍,再决定逐镜怎么写标记 ——
	// 必须在规范化之前,标记是跟着这次重编码一起写进 part 的(见 color.go)。
	colorInfos := probeColors(ctx, f, clips)
	colorDec := planColor(colorInfos)
	colorArgs := colorDec.masterArgs()
	for _, note := range colorDec.notes {
		if onProgress != nil {
			onProgress(3, note)
		}
	}
	parts := make([]string, 0, len(clips))
	durations := make([]float64, 0, len(clips))
	for i, clip := range clips {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		part := filepath.Join(work, fmt.Sprintf("part_%03d.mp4", i))
		vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=24,setsar=1",
			p.Width, p.Height, p.Width, p.Height)
		// 输入镜头本身的规格要**先量一次**(审计 L13):规范化的 scale+pad 会把任何画幅
		// 差异静默兜成"补黑边",fps 也会被强制成 24 —— 成片里混着带黑边的镜头,
		// 而日志与报告里一个字都没有。量的是**输入**(part 是刚被我们缩放过的东西,
		// 它的宽高必然等于目标,查它等于什么都没查)。
		if src, serr := f.Probe(clip); serr == nil {
			if src.Width > 0 && src.Height > 0 && (src.Width != p.Width || src.Height != p.Height) {
				announce(fmt.Sprintf("⚠ 第 %d 镜画幅 %d×%d 与成片 %d×%d 不一致:已缩放并补黑边(画面不变形,但这一镜会带边)",
					i+1, src.Width, src.Height, p.Width, p.Height))
			}
			if !src.HasAudio {
				announce(fmt.Sprintf("⚠ 第 %d 镜没有音轨:成片里这一段是静音(该镜的配音/环境声缺失)", i+1))
			}
		}
		// apad + -shortest:音轨补齐到与画面等长。H3 产物偶有音轨略短于视频,
		// 不补的话后面按"画面时长"裁音频会越界(表现为末尾静音或整轨前移)。
		normArgs := []string{"-y", "-i", clip,
			"-vf", vf,
			"-af", "aresample=48000,apad",
			"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "192k", "-ac", "2"}
		// 色彩标记:只是把"这段像素本来是什么口径"写进输出,不做任何色彩转换。
		normArgs = append(normArgs, shotColorArgs(colorInfos[i])...)
		// 容器级元数据在这里就清掉:镜头文件里内嵌着 ComfyUI 的整份工作流 JSON,
		// 不清就会被后面每一代继承到成片(见 stripMetadataArgs)。
		normArgs = append(normArgs, stripMetadataArgs()...)
		normArgs = append(normArgs, "-shortest", part)
		if _, err := f.Run(ctx, normArgs...); err != nil {
			return nil, fmt.Errorf("镜头 %d 规范化失败: %w", i+1, err)
		}
		info, err := f.Probe(part)
		if err != nil {
			return nil, err
		}
		// 规范化后的 part 必须真的是目标规格:fps 那条滤镜若被解析失败,
		// 成片帧率就与 shotFrames 的 24fps 口径脱钩(时长、字幕、接缝全按 24 算)。
		if info.FPS > 0 && math.Abs(info.FPS-24) > 0.05 {
			announce(fmt.Sprintf("⚠ 第 %d 镜规范化后帧率为 %.2ffps(应为 24):时长与字幕按 24fps 推导,请检查 ffmpeg 版本",
				i+1, info.FPS))
		}
		parts = append(parts, part)
		durations = append(durations, info.Duration)
		if onProgress != nil {
			onProgress(5+(i+1)*50/len(clips), fmt.Sprintf("规范化 %d/%d", i+1, len(clips)))
		}
	}

	// 逐接缝的**重叠时长**由平台算（seamFades：同场次内部只留 1 帧 ≈ 硬切，换场叠化
	// FadeSec，且不超过相邻任一镜的一半），交给装配引擎执行；每镜在成片里的**真实**
	// 起点则由引擎回报（见 engine.go 的 timeline），平台不再自己按 fades 累加推 ——
	// 一处算、处处一致：字幕落点、母版时长全读引擎回报的这一份。
	//
	// 2026-09-26 审计：偏移量不能由"又一套判据"推 —— 旧实现这里判 `trans != "cut"`、
	// 画面那边判 `switch p.Transition`，参数写坏时字幕按"有重叠"扣、画面却硬切。现在
	// 重叠只由下面这一份 fades 推出，判据分裂无处可生。
	trans := effectiveTransition(p.Transition)
	fd, fadeClamped := clampFadeSec(p.FadeSec)
	// **同一场次内部的接缝一律硬切**（2026-10-09 用户：「分镜直接拼接画面很明显」）：
	// 叠化是"换场"的语言（平台自己的口径就写在 transition.go 与分镜规则里：「不同场景之间用渐隐渐显」），
	// 同场之内两镜是同一时刻的连续动作 —— 给它们叠化，等于把两帧不同姿态的人像**糊在一起**
	// （观众看到的是"重影/闪烁"，比硬切显眼得多）。真机那一集 11 镜只有 2 个场次，
	// 10 处接缝全叠化：同一场里那 9 处本该硬切的地方都在糊。
	sceneSeams := sceneSeamFlags(plan)
	fades := seamFades(trans, durations, fd, sceneSeams)
	// 同场硬切了几处如实播报（否则用户按"全片叠化"去理解画面，会以为自己参数没生效）。
	if n := countSameSceneSeams(sceneSeams); n > 0 && trans == transFade && onProgress != nil {
		onProgress(3, fmt.Sprintf("同场次内部 %d 处接缝按**硬切**（叠化只用在换场处）—— "+
			"同场两镜是同一时刻的连续动作，叠化会把两帧不同姿态的人像糊成重影、比硬切显眼得多。"+
			"这条行为按平台口径固定（叠化=换场的语言），没有开关", n))
	}
	// 参数被改写就如实播报:静默改参数与"参数不生效"是同一类缺陷的两面。
	for _, note := range transitionNotes(p.Transition, p.FadeSec, fd, fadeClamped) {
		if onProgress != nil {
			onProgress(3, note)
		}
	}

	// ---- 逐镜人声包络（只为字幕落点）----
	// 接缝转场交给引擎后用的是**交叉淡入淡出**（不切内容），音频不再被让出 —— 所以
	// 包络只服务字幕的"落在真实发声段落上"。全片没有一句字幕时跳过（没人用它）。
	var envs []*audio.Envelope
	if hasCueText(plan) {
		envs = decodeEnvelopes(ctx, f, parts)
	}
	// heads/tails:该镜在成片里的可听区间。引擎的接缝是交叉淡入淡出（不切内容）,
	// 所以每镜整段都在 ⇒ 恒为整镜（字幕按整镜窗口排）。
	heads, tails := fullAudioWindows(durations)

	// ---- 步骤二:拼接（交给成熟的装配引擎 MoviePy，见 engine.go）----
	// 平台原来自己拼 xfade 链：2026-10-09 真机 P0，画面只剩前 3 镜而音轨完好、退出码
	// 还是 0。拼接与接缝转场全部交给 assemble_mpy.py：平台只喂输入 + 读引擎回报的真实
	// 时间轴 —— 自己算偏移、自己拼图正是成熟引擎每天在做的事。
	master, starts, err := stitchWithEngine(ctx, f, parts, engineFades(fades), work, p.ToolDir, colorArgs, onProgress)
	if err != nil {
		return nil, err
	}

	// ---- 保留 768p 母版（云端「整片 2K」的唯一合法源）----
	//
	// 交付档会把成片放大到 1080×1920，而 H3-Regenerate-2K 只收 768P 规格的源
	// （官方模型卡：2K 只能靠 in-context 重新生成，且该模块未开源、只能走 API）——
	// 不另存一份母版，整片 2K 就永远被预校验拦下（这条链从来没成功过）。
	// 位置很关键：必须在交付缩放/调色/字幕**之前**（那三步都会改像素，母版要的是"与渲染同规格"）。
	if mp, note := publishMaster(ctx, f, master, outDir); note != "" {
		if mp != "" {
			res.MasterPath = mp
		}
		res.AudioNotes = append(res.AudioNotes, note)
		announce(note)
	}

	// ---- 步骤三:字幕 + 调色 + 响度 + faststart 终混 ----
	if onProgress != nil {
		onProgress(85, "字幕与响度终混")
	}
	// 成片总长:**实测**装配引擎中间轨的**画面**时长(BGM 铺底吃这个值)。算术值
	// (各镜累加 − 各接缝重叠)只作兜底:容器时间基与末帧对齐会让实测差几十毫秒。
	total := filmDuration(durations, fades)
	if info, perr := f.Probe(master); perr == nil && info != nil && info.VideoDuration > 0 {
		total = info.VideoDuration
	} else {
		note := "⚠ 量不到拼接中间轨的画面时长：BGM 铺底按各镜算术值推算（可能有几十毫秒误差）"
		res.AudioNotes = append(res.AudioNotes, note)
		announce(note)
	}
	// 拼接引擎的产出如实回显（这一轮把平台自写的 xfade 链换成装配引擎的可见痕迹）。
	res.AudioNotes = append(res.AudioNotes, fmt.Sprintf(
		"拼接引擎：%d 镜拼接完成，画面实测 %.2f 秒（字幕落点读引擎回报的真实时间轴）", len(parts), total))

	// 字幕落点(乙4):按每镜的真实人声排;对不上就回退镜头级窗口并如实记 note。
	// note 进 AudioNotes —— 它本来就是"音频实况"的如实回显通道(管线逐条播报),
	// 而字幕落点这两个分支恰恰都是拿音频包络判出来的。
	var cues *cuePlan
	if hasCueText(plan) {
		tracks := make([]speechTrack, len(durations))
		for i := range tracks {
			if i < len(heads) && i < len(tails) {
				tracks[i] = speechTrack{From: heads[i], To: tails[i]}
			}
			if i < len(envs) {
				tracks[i].Env = envs[i]
			}
		}
		// 第二个证据源：逐镜 **ASR 转写**（质检那一步就写在剪辑旁 `<clip>.asr.txt`），
		// 里面是**实测的发声时间**。真机那一集包络这一档对每镜都退化，字幕因此按整镜铺开
		// （镜 1 挂 6.2 秒、真实发声只有 1 秒）—— 用户报的「字幕显示存在延迟」就是它。
		//
		// 同一份文件里还有**词级时间戳**（升级版 ASR 工具才有；老文件没有 ⇒ nil ⇒ 自动退回
		// 段级/窗口档）。有它就能把**已知台词**逐句对到词上（⓪档，见 subtitle.go 的 cueAlign）。
		asrSpans := make([][]cueSpan, len(clips))
		asrWords := make([][]asr.Word, len(clips))
		for i := range asrSpans {
			if i < len(clips) {
				asrSpans[i], asrWords[i] = asrSpansOf(clips[i])
			}
		}
		cp := planCues(plan, starts, durations, tracks, asrSpans, asrWords)
		cues = &cp
		res.AudioNotes = append(res.AudioNotes, cp.notes...)
	}

	srtPath := filepath.Join(outDir, "final.srt")
	if plan != nil {
		// 【2026-09-29 审计】原来是 `_ = os.WriteFile(...)` —— 写失败也照填 `res.SRTPath`，
		// 于是导出包/产物面板会把一个**不存在的字幕文件**列成已完成，
		// 用户去文件夹里找不到，只能怀疑自己眼瞎。
		if werr := os.WriteFile(srtPath, []byte(buildSRT(plan, starts, durations, cues)), 0o644); werr != nil {
			res.AudioNotes = append(res.AudioNotes,
				fmt.Sprintf("⚠ 字幕文件没写出来（%v）——成片里没有对外字幕", werr))
			srtPath = "" // 没写成就别把它当成产物路径交出去
		}
	}
	if srtPath != "" {
		res.SRTPath = srtPath
	}

	chain, _, note := prepareAudio(ctx, f, master, p)
	res.AudioNotes = append(res.AudioNotes, note)
	// 单条 BGM 铺底 + 人声闪避（官方 qc-checklist STEP 8：成片一条连续 BGM，片段里不各带配乐）。
	// 独立成一步、只在设了 audio.bgm 时参与：没设就完全走原路（零风险）。
	if strings.TrimSpace(p.BGM) != "" {
		// 【2026-10-08 深度排查】原来这里用 `strings.HasPrefix(bnote, "🎵")` 判"要不要换成混音轨"
		// —— **行为挂在播报文案上**：谁把 bgm.go 里那句的 🎵 去掉，就会静默丢掉整条 BGM
		// （master 不换成混音轨，日志照样说"已铺单条 BGM"）。改为显式 ok。
		if mixed, bnote, mok := mixBGMBed(ctx, f, master, p.BGM, total, master+".bgm.wav"); bnote != "" {
			res.AudioNotes = append(res.AudioNotes, bnote)
			if onProgress != nil {
				onProgress(90, bnote)
			}
			if mok {
				master = mixed
			}
		}
	} else {
		res.AudioNotes = append(res.AudioNotes, BGMNote)
	}
	if onProgress != nil && note != "" {
		onProgress(88, note)
	}

	// 调色档(甲1):统一在终混这一步应用,且**必须排在字幕之前** ——
	// 颗粒(noise)、暗角(vignette)与锐化若作用在字幕上,字会被压糊(见 finalVideoFilters)。
	burnt := p.BurnSubtitles && plan != nil
	// 交付分辨率(见 deliver.go):缩放在整条链的最前面 —— 于是调色(锐化/颗粒/暗角)
	// 与字幕都作用在**交付像素**上,而不是 768 原生像素上。
	deliverW, deliverH, deliverNote := p.deliverSize()
	scaleFilter, scaleNote := deliverScale(p.Width, p.Height, deliverW, deliverH)
	for _, n := range []string{deliverNote, scaleNote} {
		if n != "" && onProgress != nil {
			onProgress(84, n)
		}
	}
	vf, needEncode := finalVideoFilters(p.Grade, burnt, scaleFilter)
	if g := GradeFilter(p.Grade); g != "" && onProgress != nil {
		onProgress(86, fmt.Sprintf("🎨 调色档 %s 已应用(在字幕之前:颗粒与暗角不作用在字幕上)", NormalizeGrade(p.Grade)))
	}

	finalPath := filepath.Join(outDir, "final.mp4")
	// **先写 .part 再原子换名**（2026-09-29 运行时审计 P1-4）。
	//
	// 原来 ffmpeg 直接写 `final.mp4`：合成中途点「停止」/关窗/拔电/杀进程时，
	// ffmpeg 被 `CommandContext` 干掉，留下**截断但非零字节**的半成品 ——
	// 而交付判据 `project.FileOnDisk` 只判"存在、非目录、Size>0"，于是半成品算已交付：
	//
	//	① 收尾打「✔ 全流程完成」；
	//	② 批量产线断点续跑**永久跳过这一集**（它自己注释里承认"跳过的集再也不会被跑"
	//	   是最坏形态，但只防了 0 字节）；
	//	③ 用户看到一串完成、成片播不了（`+faststart` 还会把 moov 放最后，
	//	   半成品大概率连容器都读不了）。
	//
	// 同仓对照：`comfyengine/client.go` 的下载早就做对了 `.part` + rename。
	partPath := finalPath + ".part"
	args := []string{"-y", "-i", master, "-af", chain}
	if needEncode {
		args = append(args, "-vf", vf)
	}
	if burnt {
		ass := filepath.Join(outDir, "final.ass")
		// 字幕按**交付画幅**排版(PlayRes = 交付尺寸):ASS 是在缩放之后烧的,
		// 字号与下边距都吃交付高度 —— 1080×1920 交付时 15% 下边距 = 288px。
		if err := os.WriteFile(ass, []byte(buildASS(plan, starts, durations, p.SubtitleSize, p.subtitleMarginVPct(), deliverW, deliverH, cues)), 0o644); err != nil {
			return nil, err
		}
	}
	// 色彩标记透传:全片口径统一时把那份标记写到成品上(不写等于让播放器继续猜)。
	args = append(args, colorArgs...)
	// 成片不带镜头文件继承来的容器元数据(ComfyUI 工作流 JSON)。
	args = append(args, stripMetadataArgs()...)
	if needEncode {
		args = append(args,
			"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
			"-movflags", "+faststart", "-f", "mp4", partPath)
	} else {
		// 一个视频滤镜都没有(未烧字幕且未开调色)这一路**视频零滤镜**:master 已经是
		// 同规格 x264(装配引擎产出:全硬切走 concat 复制、有转场走 MoviePy 重编),这里再解一遍码、
		// 再压一遍等于白付一次整集 medium 编码(分钟级),并把每一镜又推一代 x264 代次
		// 损失(审计 M35)—— 与该文件自己的音频纪律("中间轨一律无损")自相矛盾。
		// mkv→mp4 允许视频流复制。
		args = append(args,
			"-c:v", "copy",
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
			// `-f mp4` 必须显式给：临时文件名以 `.part` 结尾，ffmpeg 从扩展名**推不出**
			// 封装格式（报 "Unable to choose an output format"）。换名之后扩展名才是 .mp4。
			"-movflags", "+faststart", "-f", "mp4", partPath)
	}
	// 烧字幕这一路必须在 outDir 里跑:libass 只接受相对文件名(规避 Windows 盘符转义)。
	if burnt {
		if err := runIn(ctx, f.Ffmpeg, outDir, args...); err != nil {
			return nil, fmt.Errorf("终混失败: %w", err)
		}
	} else if _, err := f.Run(ctx, args...); err != nil {
		return nil, fmt.Errorf("终混失败: %w", err)
	}
	// **成片到这里才"存在"**：ffmpeg 正常退出后再换名 —— 上面任何一条 return 都会
	// 让 `.part` 留在盘上（不会被误判成交付），由下一次合成覆盖或清理带走。
	if err := os.Rename(partPath, finalPath); err != nil {
		return nil, fmt.Errorf("成片落盘失败（临时文件 %s 换名失败）: %w", filepath.Base(partPath), err)
	}
	if onProgress != nil {
		onProgress(100, "合成完成")
	}
	failed = false // 成功:清理中间目录;失败保留 compose-*-failed 现场供排查
	res.FinalPath = finalPath
	// 成片时长以**实测**为准(审计:时长误报)。算术累加依赖"每镜重叠恰好等于
	// fades",而容器时间基与末帧对齐会让实测差几十毫秒;UI 的
	// 「✔ 成片就绪:final.mp4(N 秒)」吃的就是这个值 —— 报个算错的值,等于让人
	// 按错的时间轴去排下一集。探测失败才退回算术值(不阻断出片)。
	if info, perr := f.Probe(finalPath); perr == nil && info.Duration > 0 {
		res.Duration = info.Duration
		res.Width, res.Height = info.Width, info.Height
	} else {
		res.Duration = total
	}
	return res, nil
}

// ---------- 转场判据(唯一收口点) ----------
//
// 逐接缝的**重叠时长**由这里统一算出（seamFades），交给装配引擎（见 engine.go）做
// 交叉叠化；字幕起点不再由重叠推算，而是直接读引擎回报的真实时间轴。
// 2026-09-26 渲染管线审计在这里抓出三套互不一致的判据（字幕按 trans != "cut" 扣、
// 画面按 switch 走、音轨各自判），后果都不是报错而是"字幕逐镜提前、成片时长少报、
// 音轨比画面长"这类静默漂移。下面把它收成四件各自可单测的事:归一化转场、钳制转场
// 时长、逐接缝算实际重叠、由重叠推成片总长(兜底)。

// 归一化后的转场取值(只有这两种会进入拼接)。
const (
	transCut  = "cut"
	transFade = "fade"
)

// 转场时长的允许区间。
//
// 下界 0.1 s 沿用既有口径(pipeline 的 resolveFadeSec 测试里"项目显式 0.1 是合法
// 最小值");上界 3 s 是安全网 —— `AssembleParams.FadeSec` 此前**没有任何上界**
// (project 的 Sanitize 不校验它,config 的 render.fade_sec 全包无校验),一个被
// 写坏的值(单位写错、复制粘贴成 60)会让整部片子变成一场长叠化,而画面、字幕、
// 音轨三者的重叠还得一起跟着漂。超过 3 秒的叠化已不是"转场"而是特效。
const (
	minFadeSec     = 0.1
	maxFadeSec     = 3.0
	defaultFadeSec = 0.5
)

// minSeamFade 单条接缝重叠的下限(1 帧 @24fps):同场次"硬切"用一个 1 帧的极短叠化
// 表达(装配引擎把 0 当作硬切,但 1 帧能让整条链的几何保持自洽);源片时长探测异常(≈0)
// 时逐接缝重叠也会被压到这里。
const minSeamFade = 1.0 / 24

// effectiveTransition 归一化转场:只有 fade/dissolve 走叠化,其余(含空串与手改
// JSON 写进来的任意值)一律硬切 —— 与 project.StageParams.Sanitize 的"非法值按
// cut"同一口径。区别在于 Sanitize 只在**保存参数**时生效(读盘兜底
// ensureDefaultParams 在已有 scheme 时直接 return),这里是装载之后的最后一道。
func effectiveTransition(s string) string {
	switch s {
	case "fade", "dissolve":
		return transFade
	default:
		return transCut
	}
}

// clampFadeSec 把转场时长钳到合法区间,并回报是否被改写。
// 0/负值/NaN 视为"用默认值"(AssembleParams.FadeSec 的既有约定),不算改写。
func clampFadeSec(sec float64) (float64, bool) {
	if math.IsNaN(sec) || sec <= 0 {
		return defaultFadeSec, false
	}
	if sec < minFadeSec {
		return minFadeSec, true
	}
	if sec > maxFadeSec {
		return maxFadeSec, true
	}
	return sec, false
}

// transitionNotes 转场参数被改写时该如实播报的话(无需播报时返回空切片)。
func transitionNotes(rawTrans string, rawSec, fd float64, clamped bool) []string {
	var out []string
	switch rawTrans {
	case "cut", "fade", "dissolve":
	default:
		out = append(out, fmt.Sprintf(
			"⚠ 转场参数「%s」非法(只接受 cut/fade/dissolve),本条按 %s 执行:字幕时间轴与成片时长同步按此推导",
			rawTrans, transCut))
	}
	if clamped {
		out = append(out, fmt.Sprintf(
			"⚠ 转场时长 %.2fs 超出允许区间 %.1f–%.1fs,已按 %.2fs 执行", rawSec, minFadeSec, maxFadeSec, fd))
	}
	return out
}

// seamFades 逐接缝的实际重叠时长(秒),与 durations 等长(最后一镜后面没有接缝,
// 恒为 0 —— 这样消费者都能直接按下标取用)。
//
// 为什么要**逐个接缝**算而不是全片一个 fd:某一镜短于转场时(用户把 FadeSec 调到
// 2s、而某镜只有 1.5s),全局 fd 会让重叠窗口越过这一镜自身。
//
// 上限取"相邻任一镜的**一半**",而不是"相邻任一镜的全长":
//
//	· 一半是重叠能成立的必要条件(重叠窗口必须落在两镜之内);
//	· 全长只保证"装得下",却会让一镜被前后两条接缝吃光 —— 那条镜在成片里
//	  永远没有一帧是自己独占的(在时间轴上被跳过),成片比计划短了一整镜;
//	· 取一半后每镜至少保留自身 50% 的独占画面,时间轴严格推进(见
//	  TestShotStartsMatchPictureOverlap 的单调性断言)。
//
// 正常参数(fd ≤ 0.5s、镜长 ≥ 4s)下 min(一半) 恒等于 fd,行为与旧实现一致;
// 只有病态参数(转场长于半镜)才会被压下来。
func seamFades(trans string, durations []float64, fd float64, sameScene []bool) []float64 {
	fades := make([]float64, len(durations))
	if trans != transFade {
		return fades
	}
	for i := 0; i+1 < len(durations); i++ {
		// **同一场次内部的接缝不给可见的叠化**（2026-10-09 用户：「分镜直接拼接画面很明显」）。
		//
		// 叠化是"换场"的语言：同场两镜是同一时刻的连续动作，给它们叠化就是把两帧不同姿态的
		// 人像糊成重影 —— 观众看到的"拼接痕迹"恰恰是叠化**造出来**的，硬切反而看不见。
		//
		// 【为什么是 1 帧而不是 0】装配引擎(assemble_mpy.py)按 fades 走交叉叠化，
		// 0 会被它当作硬切；1/24 秒里两幅画面各半 —— 肉眼看就是硬切，而重叠时长
		// 这份数据一个字都不用改。
		use := fd
		if i < len(sameScene) && sameScene[i] {
			use = minSeamFade
		}
		half := math.Min(durations[i], durations[i+1]) / 2
		f := math.Min(use, half)
		if f < minSeamFade {
			// 连一帧的叠化都排不出来:只能给到可用的一半 —— 绝不能超过它,
			// 否则起点会倒退（成片起点由引擎回报，重叠越界无从成立）。
			f = math.Min(minSeamFade, half)
		}
		fades[i] = f
	}
	return fades
}

// sceneSeamFlags 逐接缝判"这两镜是不是**同一个场次**"（true = 同场 ⇒ 不叠化）。
//
// 【2026-10-09】判据搬进 `project.SceneSeamFlags`（**方案自己的事实**，成片体检也要用同一份），
// 这里只做委托 —— 免得"合成按一个口径、体检按另一个口径说"。
func sceneSeamFlags(plan *project.Plan) []bool { return project.SceneSeamFlags(plan) }

// countSameSceneSeams 数有几处接缝是"同场次"（播报用）。
func countSameSceneSeams(flags []bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
}

// shotStarts 由时长与逐接缝重叠**推算**每镜起点。成片里这个真值改由装配引擎回报
// （见 engine.go 的 timeline.clips[].start，Compose 直接吃引擎那份）；这里保留为
// 纯函数，供不变式断言（TestShotStartsMatchPictureOverlap）与其它消费者沿用。
func shotStarts(durations, fades []float64) []float64 {
	starts := make([]float64, len(durations))
	var acc float64
	for i := range durations {
		starts[i] = acc
		acc += durations[i]
		if i < len(fades) {
			acc -= fades[i]
		}
	}
	return starts
}

// filmDuration 成片总长 = 各镜累加 − 各接缝重叠(= 末镜起点 + 末镜时长)。
func filmDuration(durations, fades []float64) float64 {
	return sum(durations) - sum(fades)
}

// ---------- 音频链 ----------

// decodeEnvelopes 逐镜解码人声包络，**只为字幕落点**（跨了"落在真实发声段落上"这一环）。
// 接缝不再让出音频（引擎在接缝处用交叉淡入淡出），所以这里只解一次、只喂字幕。
//
// 解不出来的镜留在切片里当 nil，由调用方回退（字幕退回镜头级窗口）—— 不在这里抛错：
// 音轨解不出来不该让整片合成失败。
func decodeEnvelopes(ctx context.Context, f *ff.FF, parts []string) []*audio.Envelope {
	envs := make([]*audio.Envelope, len(parts))
	for i, part := range parts {
		if ctx.Err() != nil {
			return envs
		}
		e, err := audio.Decode(ctx, f, part)
		if err != nil {
			continue // 按"无包络"处理
		}
		envs[i] = e
	}
	return envs
}

// fullAudioWindows 每镜在成片里的可听区间。接缝转场交给装配引擎后用的是**交叉淡入淡出**
// （不切内容），所以每镜整段都在 —— 恒为整镜（字幕按整镜窗口排）。
func fullAudioWindows(durations []float64) (heads, tails []float64) {
	heads = make([]float64, len(durations))
	tails = make([]float64, len(durations))
	for i, d := range durations {
		tails[i] = d
	}
	return heads, tails
}

// denoiseChain 净化链:高通砍掉隆隆声/直流,afftdn 压嘶声与宽带底噪。
//
// 参数刻意保守(nr=12dB、噪声基准 -30dBFS):频谱降噪开大了会让语音发闷发"水",
// 宁可少降一点。用户报的"杂音"里,这一链能治的是嘶声/底噪/低频轰鸣;
// H3 自己写在音景里的持续环境声压不掉(那要靠分镜侧的音景纪律,见 director.go)。
func denoiseChain() string {
	// 【2026-09-29 用户：「分镜视频的配音电流音太强了，需要优化处理掉，另外配音的声音有点大声，吓人」】
	//
	// 电流音是 H3 原生音频的**高频嘶声/嗡声**，原来的链只做了
	// `highpass=70`（去隆隆声）+ `afftdn nr=12`（频谱降噪），对 8 kHz 以上的那层"电流感"
	// 基本没动。补三处：
	//   · `afftdn=nr=20:nf=-32` —— 降噪强度从 12 提到 20、噪声底估计更靠下（更强但仍在
	//     "不把人声做成水下感"的范围内，nr 上限是 97）；
	//   · `lowpass=f=14000` —— 14 kHz 以上**对人声可懂度没有贡献**，却是电流音的主要居所，
	//     直接切掉（不是"闷"，正常通话带宽就到 8 k）；
	//   · 高通保持 70 Hz（去桌面/风声隆隆声，不影响基频）。
	//
	// "太大声"那一条**不是这条链的锅**：工作台里试听的 `clips/shot_0NN.mp4` 是 **H3 的原始输出**，
	// 净化与响度归一（I=-16 LUFS、TP=-1.5 dBTP）只在**合成**那一步做 —— 实测日志里
	// 「音频净化 + 两遍响度归一」确实跑了。要让**片段本身**也听着正常，得在渲染落盘时
	// 给每个片段做一遍同样的净化+归一（已知缺口，下一步）。
	return "highpass=f=70,afftdn=nr=20:nf=-32,lowpass=f=14000"
}

// loudnormMeasured 一遍测量得到的响度参数(两遍法的第一遍产物)。
type loudnormMeasured struct {
	InputI       string
	InputTP      string
	InputLRA     string
	InputThresh  string
	TargetOffset string
}

// loudnormReport ffmpeg loudnorm 打在 stderr 里的 JSON 报告(数字用 json.Number
// 保留原始文本,直接交给滤镜串,避免浮点格式化引入偏差)。
type loudnormReport struct {
	InputI       json.Number `json:"input_i"`
	InputTP      json.Number `json:"input_tp"`
	InputLRA     json.Number `json:"input_lra"`
	InputThresh  json.Number `json:"input_thresh"`
	TargetOffset json.Number `json:"target_offset"`
}

// prepareAudio 依次尝试:净化 + 两遍响度 → 净化 + 单遍 → 仅两遍 → 仅单遍,
// 返回最终可用的音频链与说明。为什么要逐级回退:净化滤镜要较新的 ffmpeg,
// 而用户机器上的 ffmpeg 来自系统 PATH —— 缺滤镜时就该降级而不是整片合成失败。
func prepareAudio(ctx context.Context, f *ff.FF, master string, p Params) (string, *loudnormMeasured, string) {
	tp := peakDBTP(p.Peak)
	base := peakBaseFilter(p.Loudness, tp)
	cands := []struct{ chain, label string }{}
	if p.Denoise {
		cands = append(cands, struct{ chain, label string }{denoiseChain() + ",", "音频净化"})
	}
	cands = append(cands, struct{ chain, label string }{"", "原声"})

	for _, c := range cands {
		if meas := measureLoudness(ctx, f, master, c.chain+base); meas != nil {
			return c.chain + loudnormApplied(meas, p.Loudness, tp), meas,
				c.label + " + 两遍响度归一(线性增益,不抽气)"
		}
		// 测量失败多为滤镜不可用:同一条链退回单遍写法再试
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error", "-i", master,
			"-af", c.chain+base, "-f", "null", "-"); err == nil {
			if c.chain == "" {
				return base, nil, "原声 + 单遍响度归一(两遍法不可用,已降级)"
			}
			return c.chain + base, nil, c.label + " + 单遍响度归一(两遍法不可用,已降级)"
		}
	}
	return base, nil, "⚠ 音频链全部回退失败,按单遍响度归一继续(音质可能不稳)"
}

// peakBaseFilter loudnorm 的基础滤镜串(含目标响度与真峰值上限)。
//
// 抽成函数是为了让"项目的 peak 参数到底有没有进滤镜串"可被断言 ——
// 这正是审计 P1-1 里 `assemble.peak` 那个假开关的根因:参数存在、界面能调,
// 而真正生效的 filture 串里写死 TP=-1.5。
func peakBaseFilter(loudness int, tp float64) string {
	return fmt.Sprintf("loudnorm=I=%d:TP=%.2f:LRA=11", loudness, tp)
}

// loudnormApplied 用测量值构造第二遍(linear=true)的 loudnorm 滤镜串。
func loudnormApplied(m *loudnormMeasured, target int, tp float64) string {
	if m == nil || m.InputI == "" {
		return fmt.Sprintf("loudnorm=I=%d:TP=%.2f:LRA=11", target, tp)
	}
	return fmt.Sprintf("loudnorm=I=%d:TP=%.2f:LRA=11:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true",
		target, tp, m.InputI, m.InputTP, m.InputLRA, m.InputThresh, m.TargetOffset)
}

// measureLoudness 第一遍:只分析不输出,从 stderr 里抠出 loudnorm 的 JSON 报告。
func measureLoudness(ctx context.Context, f *ff.FF, input, chain string) *loudnormMeasured {
	out, _ := f.Run(ctx, "-hide_banner", "-i", input,
		"-af", chain+":print_format=json", "-f", "null", "-")
	a := strings.LastIndex(out, "{")
	b := strings.LastIndex(out, "}")
	if a < 0 || b <= a {
		return nil
	}
	// loudnorm 的报告是 stderr 里最后一个 JSON 对象
	var probe loudnormReport
	if json.Unmarshal([]byte(out[a:b+1]), &probe) != nil {
		return nil
	}
	if probe.InputI == "" {
		return nil
	}
	return &loudnormMeasured{
		InputI: probe.InputI.String(), InputTP: probe.InputTP.String(),
		InputLRA: probe.InputLRA.String(), InputThresh: probe.InputThresh.String(),
		TargetOffset: probe.TargetOffset.String(),
	}
}

// ShotSpeechBudget 该镜"应该有"的人声秒数(台词 + 旁白)。
//
// 放在 compose 包是因为它和成片音频同源(同一套语速常量);质检用它和实测语音时长
// 对比 —— 差太多就是"配音不完整"。
func ShotSpeechBudget(sh project.Shot) float64 {
	sec := 0.0
	for _, l := range sh.Dialogue {
		sec += director.SpeechSeconds(l.Text)
	}
	sec += director.SpeechSeconds(sh.Narration)
	return sec
}

// runIn 在指定工作目录执行(字幕相对路径方案)。
func runIn(ctx context.Context, bin, dir string, args ...string) error {
	cmd := sysexec.Hide(exec.CommandContext(ctx, bin, args...))
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 800 {
			tail = tail[len(tail)-800:]
		}
		return fmt.Errorf("%v\n%s", err, tail)
	}
	return nil
}

func sum(d []float64) float64 {
	var s float64
	for _, v := range d {
		s += v
	}
	return s
}

// subtitleWindow 该镜字幕**兜底**时间窗(相对镜头起点),返回 [start, end] 偏移。
//
// SRT(外发)与 ASS(烧录)必须用**同一个**窗口:此前一个用 8%~88%、另一个用
// 10%~90%,同一句话在两份字幕里落在两个时间点上,作者拿导出的 SRT 去平台对轨
// 会发现对不齐(审计 M36)。
//
// 这个窗口现在只作**兜底**:正常情况下每句字幕由 subtitle.go 的 alignCues 钉在
// 真实人声段落上;包络缺失或段落数与句数不符时才回退到这里(并如实播报)。
// 兜底口径与旧实现逐字一致 —— 退化时行为不变,不引入新问题。
func subtitleWindow(d float64) (start, end float64) {
	const inset = 0.1
	return d * inset, d * (1 - inset)
}

// joinInts 把镜号拼成 "1、3、5"(播报用)。
func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, v := range xs {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, "、")
}

// ---------- 字幕生成 ----------

// BuildSRT 依据镜头实际起始/时长生成 SRT 时间轴(starts 为转场扣除后的镜起始)。
//
// 保持原签名:走镜头级窗口(等价于"没有可用的人声落点计划")。
// 合成内部走 buildSRT —— 那里带着按真实人声排好的落点。
func BuildSRT(plan *project.Plan, starts, durations []float64) string {
	return buildSRT(plan, starts, durations, nil)
}

// buildSRT BuildSRT 的内部实现。cues 为按真实人声排好的落点计划(可为 nil = 全走镜头级窗口)。
func buildSRT(plan *project.Plan, starts, durations []float64, cues *cuePlan) string {
	var sb strings.Builder
	idx := 1
	for i, shot := range plan.Shots {
		d := 7.0
		if i < len(durations) {
			d = durations[i]
		}
		t := 0.0
		if i < len(starts) {
			t = starts[i]
		}
		text := director.ShotSubtitle(shot)
		if strings.TrimSpace(text) == "" {
			continue
		}
		// 每句的落点:优先真实人声(见 subtitle.go),对不上则镜头级窗口兜底
		lines := strings.Split(text, "\n")
		spans := cues.shotSpans(i, t, d, len(lines))
		for j, ln := range lines {
			ls := spans[j].Start + 0.05
			le := spans[j].End - 0.05
			if le-ls < 0.5 {
				le = ls + 0.5
			}
			fmt.Fprintf(&sb, "%d\n%s --> %s\n%s\n\n", idx, srtTime(ls), srtTime(le), ln)
			idx++
		}
	}
	return sb.String()
}

func srtTime(sec float64) string {
	ms := int(sec * 1000)
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// BuildASS 生成 ASS 字幕(中文字体+描边;台词金色/旁白白色)。
// playW/playH 为成片画幅,字幕网格与字号随画幅缩放;
// 超宽台词自动断行(中文无空格,ASS 智能换行不生效,单行超出即出界截断)。
//
// 保持原签名:走镜头级窗口 + 默认下边距;合成内部走 buildASS(带真实人声落点与项目下边距)。
func BuildASS(plan *project.Plan, starts, durations []float64, sizePct, playW, playH int) string {
	return buildASS(plan, starts, durations, sizePct, subtitleMarginVPctDefault, playW, playH, nil)
}

// buildASS BuildASS 的内部实现。cues 为按真实人声排好的落点计划(可为 nil = 全走镜头级窗口);
// marginVPct 为字幕下边距(屏高百分比,<=0 用默认)。
func buildASS(plan *project.Plan, starts, durations []float64, sizePct, marginVPct, playW, playH int, cues *cuePlan) string {
	if sizePct <= 0 {
		sizePct = 4
	}
	if marginVPct <= 0 {
		marginVPct = subtitleMarginVPctDefault
	}
	if playW < 32 || playH < 32 {
		playW, playH = 1080, 1920
	}
	fontSize := playH * sizePct / 100
	// 下边距:避开平台底部 UI 安全区(见 subtitleMarginVPctDefault)。
	// 这里吃的是**交付高度**(playH)—— 1080×1920 交付时 15% = 288px。
	marginV := playH * marginVPct / 100
	// 场次字幕的字号比台词小一档（它是"这是哪儿"的提示，不该抢台词的注意力），
	// 停留 2.5 秒、从该镜起播后 0.4 秒出现（给渐显留出黑场时间，否则字幕压在黑屏上像报错）。
	captionFontSize := int(float64(fontSize) * 0.8)
	if captionFontSize < 20 {
		captionFontSize = 20
	}
	marginLR := playW * 5 / 100
	// 单行可容字数 ≈ 可用宽度/字宽(全角字宽≈字号);不足下限兜底
	maxRunes := int(float64(playW-marginLR*2) / float64(fontSize) * 0.98)
	if maxRunes < 6 {
		maxRunes = 6
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `[Script Info]
ScriptType: v4.00+
PlayResX: %d
PlayResY: %d
WrapStyle: 2
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Dialogue,Microsoft YaHei,%d,&H0020D9FF,&H000000FF,&H00101010,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,%d,%d,%d,1
Style: Narration,Microsoft YaHei,%d,&H00FFFFFF,&H000000FF,&H00101010,&H80000000,-1,0,0,0,100,100,0,0,1,3,1,2,%d,%d,%d,1
Style: Caption,Microsoft YaHei,%d,&H00D8F0FF,&H000000FF,&H00101010,&H80000000,0,0,0,0,100,100,2,0,1,3,1,2,%d,%d,%d,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`, playW, playH, fontSize, marginLR, marginLR, marginV, fontSize, marginLR, marginLR, marginV,
		captionFontSize, marginLR, marginLR, marginV+int(float64(fontSize)*2.2))

	// 场次字幕：换场（以及首镜）时在画面上方留一条"地点·时间"。
	//
	// 来源《五、3D漫剧15秒分镜指令》/《【额外】仿真人10S分镜头指令》："不同场景间通过渐隐渐显转场，
	// 如需地点时间信息，可在画面描述中以字幕形式自然呈现"。**文字由这里烧，不进视频提示词** ——
	// H3 画汉字必成伪汉字（实测），而 ASS 用系统汉字字体是稳的（用户文件也写了"若不可靠则后期替换"）。
	// 判据与提示词那边共用 project.SceneCaptionFor（一处实现，免得"提示词判换场、字幕判法不同"）。
	for i := range plan.Shots {
		caption := project.SceneCaptionFor(plan, i)
		if caption == "" {
			continue
		}
		t := 0.0
		if i < len(starts) {
			t = starts[i]
		}
		fmt.Fprintf(&sb, "Dialogue: 0,%s,%s,Caption,,0,0,0,,%s\n",
			assTime(t+captionLeadIn), assTime(t+captionHold), assEscape(caption))
	}

	for i, shot := range plan.Shots {
		d := 7.0
		if i < len(durations) {
			d = durations[i]
		}
		t := 0.0
		if i < len(starts) {
			t = starts[i]
		}
		// 逐条拆分显示:旁白/每段对白各占一条,按镜头时长均分;
		// 整镜堆一条会三四个段落同时压屏,小画幅下叠到画面中央不可读。
		parts := director.ShotSubtitleParts(shot)
		if len(parts) == 0 {
			continue
		}
		style := "Dialogue"
		if strings.TrimSpace(shot.Narration) != "" && len(shot.Dialogue) == 0 {
			style = "Narration"
		}
		// 落点与 SRT 同源(审计 M36):cues 给真实人声落点,对不上则由 shotSpans 回退
		// 到 subtitleWindow 的镜头级窗口(唯一出处,此处不再自己写 0.1/0.8)。
		spans := cues.shotSpans(i, t, d, len(parts))
		for j, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			// **先转义再换行**：`{}` 在 ASS 里是覆写标签块，块内不识别的内容会被静默吞掉
			// （技能侧 2026-09-28 通知带依据；平台这边是字幕唯一出口，所以在这一处收口）。
			p = wrapAssText(strings.ReplaceAll(assEscape(p), "\n", `\N`), maxRunes)
			fmt.Fprintf(&sb, "Dialogue: 0,%s,%s,%s,,0,0,0,,%s\n",
				assTime(spans[j].Start), assTime(spans[j].End), style, p)
		}
	}
	return sb.String()
}

const (
	// captionLeadIn 场次字幕相对该镜起点延迟出现（秒）：先让画面从黑渐显出来。
	captionLeadIn = 0.4
	// captionHold 场次字幕消失的时刻（相对该镜起点，秒）。
	captionHold = 2.9
)

// wrapAssText 把超宽字幕行按 maxRunes 断为多行(以 \N 相连);
// 中文断点优先落在标点后,不得已才按字符硬切。
func wrapAssText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 12
	}
	lines := strings.Split(text, `\N`)
	var out []string
	for _, ln := range lines {
		runes := []rune(ln)
		for len(runes) > maxRunes {
			cut := maxRunes
			// 从行宽末往前找最近标点(至少留 2/3 行宽),整句处断更顺眼
			for j := maxRunes - 1; j >= maxRunes*2/3; j-- {
				if isAssBreak(runes[j]) {
					cut = j + 1
					break
				}
			}
			out = append(out, string(runes[:cut]))
			runes = runes[cut:]
		}
		if len(runes) > 0 {
			out = append(out, string(runes))
		}
	}
	return strings.Join(out, `\N`)
}

// isAssBreak 断行标点(中文/英文句读与括号)。
func isAssBreak(r rune) bool {
	return strings.ContainsRune("，。、；：！？,.!?;:…—～·—…\"'「」『』()（）【】", r)
}

func assTime(sec float64) string {
	cs := int(sec * 100)
	h := cs / 360000
	cs %= 360000
	m := cs / 6000
	cs %= 6000
	s := cs / 100
	cs %= 100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}
