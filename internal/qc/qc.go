// Package qc 机械质检:时长/音轨/分辨率探测 + 抽帧亮度统计(黑屏防线)。
// 不做内容理解(那属于 LLM/VLM 审片,后续版本扩展)。
package qc

import (
	"context"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/YiIimini/ManJuX/internal/audio"
	"github.com/YiIimini/ManJuX/internal/ff"
)

// Report 单镜质检报告。
type Report struct {
	ShotID   int      `json:"shot_id"`
	File     string   `json:"file"`
	OK       bool     `json:"ok"`
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
	Duration float64  `json:"duration"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	FPS      float64  `json:"fps"`
	HasAudio bool     `json:"has_audio"`
	// DarkRatio 各采样帧里**最暗**那一帧的暗像素占比(0-1);DarkRatios 为逐帧原始值。
	//
	// 为什么取最暗帧而不是三帧汇总平均(审计 M27):汇总平均会把"三帧里只有一帧全黑"
	// 稀释成 ≈0.33 —— 低于 0.45 的 warn 阈值,于是约 1/3 时长的黑屏(尾部黑、中段黑
	// 这类 H3 常见失效)被静默判为合格。逐帧判定后,任何一帧超阈都算数。
	DarkRatio  float64    `json:"dark_ratio"`
	DarkRatios []float64  `json:"dark_ratios,omitempty"`
	VLM        *VLMReport `json:"vlm,omitempty"` // 智能审片(可选)
	// TextDetect 画面文字检测(可选,见 textdetect.go):判这一镜画面里有没有被模型
	// 烧进去的可读文字(字幕/招牌/UI/浮字)。命中只告警不判负,集级命中率超阈值时由
	// 调用方升级为显式告警(清单 §3.1a)。
	TextDetect *TextDetectReport `json:"text_detect,omitempty"`
	// SpeechSec 实测人声时长(秒);ExpectedSpeechSec 本镜台词应有的口播时长。
	// 两者差距过大就是「配音不完整」——这是用户 2026-09-24 报障里唯二可以
	// **机械判定**的一条(另一条是杂音,靠频谱,不在本包范围内)。
	SpeechSec         float64 `json:"speech_sec"`
	ExpectedSpeechSec float64 `json:"expected_speech_sec,omitempty"`
	// ExpectedDuration 本镜计划时长(秒,0 = 调用方没给);与实测时长对账用(审计 M29)。
	ExpectedDuration float64 `json:"expected_duration,omitempty"`
	// StrippedSec 质检时平台自己剪掉了多少秒黑闪（0 = 没剪；见 Params.StrippedSec）。
	// 留痕是为了让报告能解释"为什么实测比计划短、却没判不合格"。
	StrippedSec float64 `json:"stripped_sec,omitempty"`
	// Speech 语音核对(可选,见 speech.go):ASR 转写这一镜的音轨,再与计划台词逐句对账 ——
	// 判「念的是不是那句台词」(重复/自造/漏念)。它是**听出来的**结论,只标 warn 不拦批次;
	// 没装 ASR 时 Skipped 里如实写"没验过"。
	Speech *SpeechReport `json:"speech,omitempty"`
	// Voice 音色核对（可选，见 pitch.go）：把"角色卡上写的男声/女声"与"音轨里实际念出来的
	// 基频"对一遍 —— 判「谁在说、什么嗓子」。它是**量出来的**结论（不依赖 ASR 工具），
	// 判负只 warn 不拦批次；声线文本没写性别/同镜男女混合/人声太短时 Skipped 里如实写"没判"。
	Voice *VoicePitchReport `json:"voice,omitempty"`
	// Landmark 空间锚点核查（可选，见 landmark.go 的 LandmarkReport）：**三态**留痕 ——
	// 验过 / 没验（未配视觉模型、场景卡没写地标、非建立镜、限流或预算） / 想验但失败。
	// 此前这一步零输出，报告里分不清"验过且干净"与"根本没验"（2026-10-08 深度排查）。
	Landmark *LandmarkReport `json:"landmark,omitempty"`
}

// Params 质检阈值。
type Params struct {
	DarkRatioFail float64
	DarkRatioWarn float64
	// DarkScene 这一镜的**计划自己声明了它是暗调**（场景卡的 `light_state` 写着"沉在黑暗里/漆黑/无光"）。
	//
	// 【为什么要有这一位】（2026-10-07 真机实测）那一集 13 镜里 **5 镜**被判"疑似黑屏"不合格
	// （最暗采样帧 0.67–0.82 ≥ 阈值 0.60），于是整集"质检未通过"、**批量产线暂停在第 1/88 集**。
	// 但看现场：那 5 镜全在两个**计划里就写明是暗调**的场景里（维护舱"右侧一列先亮，其余沉在黑暗里"、
	// 舱门缝"缝外漆黑无光"），而同场景里"三排灯全亮"的那一镜实测只有 0.58（判 warn）——
	// 也就是说判据测到的是**创作意图**，不是崩坏。
	//
	// 处置：`DarkScene=true` 时阈值**整组上抬**（取 max(配置值, 0.90/0.70)），
	// 因为真崩坏（整镜全黑/尾部全黑）的实测值接近 1.0，仍会被抓住；而"大多数画面是黑的"在这里是设计。
	DarkScene   bool
	MinDuration float64
	MaxDuration float64
	// ExpectedSpeechSec 本镜台词的应有口播时长(秒);>0 时启用人声完整性判定。
	ExpectedSpeechSec float64
	// ExpectedDuration 本镜**计划**时长(秒);>0 时与产物实测时长对账。
	//
	// 为什么需要它(审计 M29):只比全局 3–18s 的话,3.0–3.9s 的残片(台词被截断)
	// 一律判"通过",而计划侧的合法区间是 4–10s;本地引擎按 17k+5 向上对齐、
	// 云端按整秒抬升,同一集混渲时每镜时长口径还会差 0.25~1s。
	ExpectedDuration float64
	// StrippedSec 平台**自己**从这份产物上剪掉的秒数（黑闪切除，0 = 没剪过 / 调用方没给）。
	//
	// 【为什么需要它（2026-10-11）】"短于计划"那条判据本意是抓"产物被截断/还是旧时长"，
	// 而黑闪切除是**平台自己剪的**：默认单段上限抬到 0.6 秒之后，一次剪掉 0.58 秒就会被
	// 报成"产物被截断,请重渲该镜" —— 用户白白重渲一镜。所以"短"的那一侧对账要把这个数
	// 加回去（秒数的唯一出处 = compose.StrippedBlackSec，从片段自己的 .noblack 标记里读；
	// 没标记 / 标记已失效一律 0 = 不加不减）。
	StrippedSec float64
	// ExpectWidth/ExpectHeight/ExpectFPS 成片规格;>0 时核对(0 = 不核对)。
	//
	// 为什么需要(审计 M28):此前只是"记录"了宽高,从不与规格核对,而合成侧对
	// 画幅差异的处置是 scale+pad(补黑边)—— 换引擎或改画幅后成片里混着带黑边的
	// 镜头,质检报告里一条 warning 都没有。
	ExpectWidth  int
	ExpectHeight int
	ExpectFPS    float64
}

// 时长对账容差(秒)。
//
// 本地引擎按 H3 的 17k+5 网格**向上**对齐,4 秒镜实际 4.46s(+0.67 上限);
// 云端按整秒抬升,4 秒镜实际 5s(+1.0)。所以:
//   - 短的容差很小:产物**不该**短于计划(对齐只会变长),短了就是截断或旧时长残留;
//   - 长的容差放到 0.8s:把云端的整秒抬升如实报出来,但不惊动本地那 0.67s 的对齐差。
//
// 【2026-10-11:短的那一侧要先减掉"平台自己剪的"】黑闪切除会主动剪掉 ≤0.6s 的镜首黑段 ——
// 那是平台的动作、不是"产物被截断",所以实测时长要先把 Params.StrippedSec **加回**再比
// (两个"短"判据都加),否则会把平台自己的剪辑报成故障、骗用户白重渲一镜;
// 两条"长"的判据仍按实测值判(剪完只会更短,不会因此多报"偏长")。
const (
	plannedShortfallTolerable = 0.15 // 容器取整的噪声
	plannedShortfallFail      = 0.5  // 明显短于计划 = 残片/旧产物
	plannedOverrunWarn        = 0.8  // 明显长于计划 = 云端抬时长或对齐口径不同
)

// speechWarnRatio 实测/应有 人声时长低于此比例即告警。
//
// 0.6 是刻意留松的:同一句话的语速会有 20-30% 的正常浮动,H3 也可能把两句之间的
// 停顿吃掉;低于六成就不像"语速快"而像"话没说完/整句没出声"。只是告警不阻断 ——
// 重渲同一提示词同一 seed 大概率复现,真正要改的是台词长度或拆镜。
const speechWarnRatio = 0.6

// darkVerdict 暗像素占比 → (不合格文案, 告警文案)（都为空 = 干净）。
//
// 【为什么单独一个纯函数】"暗调场景放宽到多少、真崩坏还抓不抓得住"必须能**逐条断言**，
// 而不是靠读代码推断（本仓纪律：一条规则一条检）。2026-10-07 真机的那组数是判据的依据：
// 5 镜被判"疑似黑屏"不合格（0.67–0.82）⇒ 整集质检未通过、批量产线停在第 1/88 集，
// 而同场景"三排灯全亮"那一镜只有 0.58 —— 真正的分界不在 0.6，而在"画面是不是几乎全黑"。
//
// 规则（两条线分开处置）：
//
//	· **不合格线**：普通镜照用户配置（默认 0.60）；`DarkScene`（场景卡光态自己写着暗调）时取
//	  `max(用户值, 0.90)` —— 即"取两者中较宽的那条线"：既不会比用户配的更严，也不会比 0.90 更松。
//	  真崩坏（整镜全黑、后半段全黑）实测贴近 1.0，落在 0.90 之上，**仍然判不合格**。
//	· **告警线**：**不动**（用户配的 0.45）。暗调镜照样要在报告里留一条"这一镜有多暗"的记账，
//	  只是文案说明"按暗调口径放行、不判不合格" —— 信息不丢，只是不再阻断产量。
func darkVerdict(worst float64, p Params) (problem, warning string) {
	fail := p.DarkRatioFail
	if p.DarkScene {
		fail = math.Max(fail, DarkSceneRatioFail)
	}
	switch {
	case fail > 0 && worst >= fail:
		msg := fmt.Sprintf("疑似黑屏:采样帧最暗之一达 %.0f%%(阈值 %.0f%%)", worst*100, fail*100)
		if p.DarkScene {
			msg += " —— 本镜是**计划声明的暗调场景**，不合格线已取较宽的 90%：达到这个比例意味着画面几乎全黑"
		}
		return msg, ""
	case p.DarkRatioWarn > 0 && worst >= p.DarkRatioWarn:
		msg := fmt.Sprintf("画面偏暗:采样帧最暗之一达 %.0f%%", worst*100)
		if p.DarkScene {
			msg += fmt.Sprintf("（本镜是计划声明的暗调场景，不合格线已放到 %.0f%% —— 这里只记账，不判不合格）",
				fail*100)
		}
		return "", msg
	}
	return "", ""
}

// DarkSceneRatioFail **计划声明的暗调场景**下的暗像素**不合格线**（与 Params.DarkScene 配套）。
//
// 取值依据（2026-10-07 真机实测）：被判"疑似黑屏"的 5 镜最暗采样帧是 0.67–0.82，而同一场景里
// "三排灯全亮"那一镜是 0.58 —— 差的是"有没有按剧情亮灯"，不是"画面崩没崩"。
// 真崩坏（整镜全黑、或后半段全黑那种）实测值贴近 1.0，所以 0.90 仍然抓得住它
// （对照 `TestDarkRatioSeesSecondHalf` 用的是 0.60/0.45 那组普通阈值，本改动不动它）。
// **告警线不跟着放**：暗调镜照样在报告里留一条记账（见 darkVerdict）。
const DarkSceneRatioFail = 0.90

// Check 检查单个视频文件。
func Check(ctx context.Context, f *ff.FF, shotID int, path string, p Params) *Report {
	r := &Report{ShotID: shotID, File: path, Problems: []string{}, Warnings: []string{}}
	info, err := f.Probe(path)
	if err != nil {
		r.Problems = append(r.Problems, "无法探测:"+err.Error())
		return r
	}
	r.Duration, r.Width, r.Height, r.HasAudio = info.Duration, info.Width, info.Height, info.HasAudio
	r.FPS = info.FPS
	if !info.HasAudio {
		r.Problems = append(r.Problems, "无音轨(H3 产物应自带 32kHz 立体声)")
	} else {
		// 人声完整性:实测语音时长 vs 台词应有口播时长(见 Params.ExpectedSpeechSec)
		if env, err := audio.Decode(ctx, f, path); err == nil && env != nil {
			r.SpeechSec = env.SpeechSeconds(env.Speech(), 0, env.Duration())
			r.ExpectedSpeechSec = p.ExpectedSpeechSec
			if p.ExpectedSpeechSec > 1.0 && r.SpeechSec < p.ExpectedSpeechSec*speechWarnRatio {
				r.Warnings = append(r.Warnings, fmt.Sprintf(
					"疑似配音不完整:实测人声 %.1fs,台词应有约 %.1fs(不足六成)——检查台词是否被截断或整句未出声",
					r.SpeechSec, p.ExpectedSpeechSec))
			}
		} else if err != nil {
			r.Warnings = append(r.Warnings, "人声时长分析失败:"+err.Error())
		}
	}
	// 黑闪切除是**平台自己**从这份产物上剪的（秒数唯一出处 compose.StrippedBlackSec）：
	// 剪掉的秒数必须**先加回**再判"短" —— 否则默认单段上限 0.6 秒一次就能剪掉 0.58 秒，
	// 会被下面两条判据报成"产物被截断、请重渲该镜"，用户白白重渲一镜（GPU 几分钟）。
	effDur := info.Duration + p.StrippedSec
	trimNote := ""
	if p.StrippedSec > 0 {
		r.StrippedSec = p.StrippedSec
		trimNote = fmt.Sprintf("(其中平台自己剪掉 %.2fs 黑闪,剪前 %.1fs)", p.StrippedSec, effDur)
	}
	if effDur < p.MinDuration {
		r.Problems = append(r.Problems, fmt.Sprintf("时长过短 %.1fs(应≥%.0fs)%s", effDur, p.MinDuration, trimNote))
	}
	if info.Duration > p.MaxDuration {
		r.Warnings = append(r.Warnings, fmt.Sprintf("时长偏长 %.1fs", info.Duration))
	}
	// 与**本镜计划**对账(审计 M29):全局阈值只拦得住 3s 以下的残片。
	if p.ExpectedDuration > 0 {
		r.ExpectedDuration = p.ExpectedDuration
		diff := effDur - p.ExpectedDuration
		switch {
		case diff < -plannedShortfallFail:
			r.Problems = append(r.Problems, fmt.Sprintf(
				"时长短于计划 %.1fs(计划 %.1fs,实测 %.1fs)%s:产物被截断,或仍是改动前的旧时长 —— 请重渲该镜",
				-p.ExpectedDuration+effDur, p.ExpectedDuration, info.Duration, trimNote))
		case diff < -plannedShortfallTolerable:
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"时长略短于计划 %.1fs(计划 %.1fs,实测 %.1fs)%s", -diff, p.ExpectedDuration, info.Duration, trimNote))
		case diff > plannedOverrunWarn:
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"时长超出计划 %.1fs(计划 %.1fs,实测 %.1fs):本地按 17k+5 帧对齐、云端按整秒抬升,超出部分会拉长整集节奏",
				diff, p.ExpectedDuration, info.Duration))
		}
	}
	// 成片规格核对(审计 M28):画幅/帧率不符时合成会 scale+pad 或强制 fps 静默兜住。
	if p.ExpectWidth > 0 && info.Width != p.ExpectWidth {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"画幅与成片规格不符:本镜 %d×%d,成片 %d×%d —— 合成会缩放并补黑边,成片里会出现带黑边的镜头",
			info.Width, info.Height, p.ExpectWidth, p.ExpectHeight))
	}
	if p.ExpectHeight > 0 && info.Height != p.ExpectHeight {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"画幅与成片规格不符:本镜 %d×%d,成片 %d×%d —— 合成会缩放并补黑边", info.Width, info.Height, p.ExpectWidth, p.ExpectHeight))
	}
	if p.ExpectFPS > 0 && info.FPS > 0 && math.Abs(info.FPS-p.ExpectFPS) > 0.05 {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"帧率与成片规格不符:本镜 %.2ffps,成片 %.2ffps —— 合成会强制改帧率", info.FPS, p.ExpectFPS))
	}

	samples, err := darkRatio(ctx, f, path, info.Duration)
	if err == nil {
		// 逐帧判定(审计 M27):汇总平均会把"只有一帧全黑"稀释到阈值以下。
		r.DarkRatios = samples
		worst := 0.0
		for _, d := range samples {
			if d > worst {
				worst = d
			}
		}
		r.DarkRatio = worst
		r.DarkRatio = worst
		// 判定收在一个**纯函数**里（阈值选择 + 文案一起），这样"暗调场景放宽到多少、真崩坏还抓不抓得住"
		// 可以逐条断言，而不是靠读代码推断。见 darkVerdict 的说明。
		if problem, warnMsg := darkVerdict(worst, p); problem != "" {
			r.Problems = append(r.Problems, problem)
		} else if warnMsg != "" {
			r.Warnings = append(r.Warnings, warnMsg)
		}
	} else {
		// 抽帧失败**必须是 Problem**:量不到就没资格说"通过"(旧实现把它降级成
		// Warning 后照常给出 darkRatio=0 的结论,于是"质检通过"是假的)。
		r.Problems = append(r.Problems, "黑屏检测不可信(抽帧失败):"+err.Error())
	}
	r.OK = len(r.Problems) == 0
	return r
}

// darkRatio 按**实测时长**取 10%/30%/50%/70%/90% 五个时间点抽帧,统计每帧的暗像素占比。
//
// 2026-09-24 修(审计 P0-3):旧实现固定抽 1s/3s/5s,与时长无关,于是
//   - 长镜只看得到前 5 秒:第 6 秒起全黑也判"通过";
//   - 4 秒镜头的第三个采样点越界,ffmpeg exit=0 但没有产物,被 `continue` 吞掉,
//     函数照样返回一个数字 —— 结论建立在"只看了一两帧"甚至"一帧都没看"之上。
//
// 2026-09-26 修(审计 M27):返回值从"三帧汇总平均"改成**逐帧**列表 ——
// 汇总平均会把只有一帧全黑的情况稀释到 warn 阈值以下(≈0.33 < 0.45),
// 而尾部黑/中段黑恰恰是 H3 最常见的失效形态。判定交给调用方(见 Check),
// 这里只负责如实给出每一帧的量。
//
// 现在:采样点按时长定位且保证落在片内;任何一帧抽不出来都直接返回错误,
// 由调用方计入 Problems(见 Check)。
func darkRatio(ctx context.Context, f *ff.FF, video string, duration float64) ([]float64, error) {
	if duration <= 0 { // 调用方没给时长(或探测失败)时自己再探一次
		if info, err := f.Probe(video); err == nil {
			duration = info.Duration
		}
	}
	if duration <= 0 {
		return nil, fmt.Errorf("读不到视频时长,无法定位抽帧点")
	}
	tmpDir, err := os.MkdirTemp("", "manjux-qc-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	positions := samplePositions(duration)
	ratios := make([]float64, 0, len(positions))
	for i, at := range positions {
		out := filepath.Join(tmpDir, fmt.Sprintf("f_%d.png", i))
		if _, err := f.Run(ctx, "-y", "-ss", fmt.Sprintf("%.3f", at), "-i", video,
			"-frames:v", "1", "-vf", "scale=160:-2", out); err != nil {
			return nil, fmt.Errorf("第 %d 帧(%.2fs)抽帧失败:%w", i+1, at, err)
		}
		d, n, err := pngDark(out)
		if err != nil {
			return nil, fmt.Errorf("第 %d 帧(%.2fs)不可读:%w", i+1, at, err)
		}
		if n == 0 {
			return nil, fmt.Errorf("第 %d 帧(%.2fs)没有像素", i+1, at)
		}
		ratios = append(ratios, float64(d)/float64(n))
	}
	if len(ratios) == 0 {
		return nil, fmt.Errorf("未成功抽帧")
	}
	return ratios, nil
}

// samplePositions 10% / 30% / 50% / 70% / 90% 五个时间点,并保证都落在片内:
// 末点回退 edge 秒 —— `-ss` 超过时长的常见后果是"exit 0 但没有输出文件",
// 那正是旧实现静默吞掉的那种失败。
//
// 三点扩成五点(审计 M27):三点下"中段某一段黑"很容易落在两个采样点之间,
// 五点覆盖到 90% 处,尾部黑几乎必然被抓到。
func samplePositions(duration float64) []float64 {
	const edge = 0.15
	last := duration - edge
	if last < 0 {
		last = 0
	}
	ps := []float64{duration * 0.1, duration * 0.3, duration * 0.5, duration * 0.7, duration * 0.9}
	for i := range ps {
		if ps[i] > last {
			ps[i] = last
		}
	}
	return ps
}

// pngDark 解码 PNG 统计暗像素。
func pngDark(path string) (dark, total int, err error) {
	fh, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer fh.Close()
	img, err := png.Decode(fh)
	if err != nil {
		return 0, 0, err
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			// RGBA() 返回 16 位,转 8 位灰度(Rec.601 近似)
			gray := (r>>8*299 + g>>8*587 + bl>>8*114) / 1000
			total++
			if gray < 40 {
				dark++
			}
		}
	}
	return dark, total, nil
}
