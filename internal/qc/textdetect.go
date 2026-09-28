// 画面文字检测:抽帧 → 视觉大模型判「这一镜的画面里有没有被模型烧进去的字」。
//
// 为什么需要它(技能侧《平台侧整改清单》§3.1a,2026-09-26):
// 技能侧 v2.3 已把提示词里 `<d>` 块**之外**的汉字清到 0,唯一还没验过的假设是 ——
// `<d>` 里的台词原文(官方要求逐字保留,H3 拿它做原生中文配音)会不会**同时**被画成
// 字幕/字形。这件事技能侧自证不了:摘掉 `<d>` 就没有原生中文配音了,不能靠"少写点"
// 解决。只能靠"出一集实拍 + 抽帧看图判字"。本文件就是那条通路的自动化版本 ——
// 便宜、可重复,而且顺带成为「字幕回归」的长期监控,不依赖任何人记得去查。
//
// 为什么在**镜级原始产物**上做,而不是合成之后的成片:
// assemble 会按 `BurnSubtitles` 把台词字幕烧进成片 —— 那是设计的一部分。检测器一旦
// 看的是成片,"画面里有字"必然命中,这个信号就彻底失去意义。镜级产物是 H3 直出、
// 未叠加任何图层的东西,它上面出现的可读文字只能来自模型自己。
//
// 复用既有件,不另起一套:
//   - 抽帧:ff.FF.Run + 本包 samplePositions(与黑屏检测同一套采样点,于是"哪几帧有字"
//     和报告里的暗像素是同一批时刻);
//   - 视觉调用:llm.Client.ChatVision(与 VLMJudge / IdentityJudge 同一个客户端、同一份
//     设置页 VLM 配置);
//   - 压图口径:JPEG q3 + 缩放到 540 宽,沿用 internal/server/library_ops.go:imageDataURL
//     既有的"送视觉模型前压图"口径(那里记过原因:原图 PNG 约 2MB,base64 近 3MB,
//     缩小既省额度也避开部分平台的单图体积上限)。本检测一次要送**多帧**,单帧体积
//     直接乘帧数,所以跟着既有口径走而不是自己放大。
//
// 为什么不直接在 VLMJudge 上加一个判据:它的目的是"打分/是否崩坏",输出形状
// (pass/score/advice)与"哪几帧有字"完全不同;它的抽帧还写死了 1s/3s 两个时刻
// (对 4-5 秒的短镜看不到尾部)。所以复用的是**同一套基础设施** —— 同一个 ff 执行器、
// 同一个 samplePositions、同一个 llm.Client.ChatVision 与同一份设置页 VLM 配置 ——
// 而不是把两个判据塞进同一个函数。
//
// 判定策略(清单 §3.1a 给了二选一,这里选"命中即标记"):
//   - 单镜命中 → warn(不 fail):模型把场景里的招牌/书页/屏幕画出来是合理内容;
//   - 集级命中率超阈值 → **显式告警**,见 TextHitAlert 的判据说明。
package qc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/llm"
)

// VisionClient 本包做画面文字检测所需的**最小**视觉能力。
//
// 为什么取接口而不是直接吃 *llm.Client:这条链的判据(有字→告警 / 无字→静默 /
// 模型没回答→不算通过)全是纯逻辑,抽象出一行方法就能用假客户端把它们钉死在单测里,
// 不必依赖真机与额度。*llm.Client 天然满足它(与 vlm.go / identity.go 用的是同一个客户端)。
type VisionClient interface {
	ChatVision(ctx context.Context, system string, turns []llm.VisionContent, jsonMode bool) (string, error)
}

// 集级判据阈值。
const (
	// TextHitRatioAlarm 集级命中率阈值:命中镜数 / **实际检测过**的镜数 > 此值 → 显式告警。
	//
	// 10% 取自技能侧《平台侧整改清单》§3.1a 给出的口径("同一集命中镜数超过阈值(如 >10%)")。
	TextHitRatioAlarm = 0.10
	// TextHitMinShots 命中镜数下限:少于 2 镜不升级为集级告警。
	//
	// 为什么在比例之外再加一条计数闸门:分母小的时候(短集、或大部分镜还没渲完)
	// 1 个命中就能越过 10%,那是统计噪声而不是"字幕回归"。单镜那条 warn 照旧会发,
	// 只是不把整集的结论拉响。
	TextHitMinShots = 2
	// textFrameWidthDefault 抽帧宽度默认值(见 TextDetectOptions.FrameWidth)。
	textFrameWidthDefault = 540
)

// TextFrameHit 单帧的画面文字判定结果。
type TextFrameHit struct {
	Index      int     `json:"index"`      // 1 起,与采样顺序一致
	AtSec      float64 `json:"at_sec"`     // 该帧的采样时间点(秒)
	HasText    bool    `json:"has_text"`   // 这一帧里有没有可读文字
	Confidence int     `json:"confidence"` // 0-100:模型对**本帧**这个判断的把握
	Note       string  `json:"note"`       // 中文:看到的文字内容与位置(无则空)
}

// TextDetectReport 单镜画面文字检测结论。
//
// Checked / Skipped / Error 三个字段是刻意分开的:它们共同回答"这一镜到底看没看",
// 而 HasText 只回答"看到了什么"。理由是 —— 只看 HasText 会把**压根没看**读成
// "干净通过",那正是本清单 §3.1a 要防的"没验过却看起来像验过了"。
type TextDetectReport struct {
	Checked       bool           `json:"checked"`                  // 是否真的看了画面
	Skipped       string         `json:"skipped,omitempty"`        // 没看的原因(开关关闭/未配置视觉模型/机械质检未通过)
	Error         string         `json:"error,omitempty"`          // 看了但失败(抽帧/调用/解析)
	HasText       bool           `json:"has_text"`                 // 是否命中(仅在 Checked=true 时有意义)
	SampledFrames int            `json:"sampled_frames,omitempty"` // 本镜采了几帧
	HitFrames     []int          `json:"hit_frames,omitempty"`     // 命中的帧序号
	MaxConfidence int            `json:"max_confidence,omitempty"` // 命中帧里最高的置信度
	Frames        []TextFrameHit `json:"frames,omitempty"`         // 逐帧结论(含未命中帧,便于复核)
	Note          string         `json:"note,omitempty"`           // 模型给的中文总体描述
}

// TextAlert 集级画面文字告警(超阈值时才有)。
type TextAlert struct {
	Examined int     `json:"examined"`  // 实际做过检测的镜数(分母)
	HitShots []int   `json:"hit_shots"` // 命中的镜号
	Ratio    float64 `json:"ratio"`     // 命中率
	Msg      string  `json:"msg"`       // 中文结论(阶段级告警文案)
}

// TextDetectOptions 画面文字检测的可调项(零值 = 全部用默认)。
type TextDetectOptions struct {
	// FrameWidth 抽帧缩放后的宽度(像素);<=0 取 540(见文件头的压图口径说明)。
	// 留成可调:真机验证若发现小字漏检,调大它即可,不必改代码。
	FrameWidth int
}

// DetectOnScreenText 抽帧送视觉模型,判这一镜里有没有被烧进画面的可读文字。
//
// duration 为实测时长(<=0 时自己 ffprobe);采样点复用 samplePositions —— 与黑屏检测
// 用同一套 10%/30%/50%/70%/90%,于是"哪几帧有字"与报告里 DarkRatios 的采样时刻对齐。
//
// 返回的 error 一律由调用方**如实写进报告**(见 TextDetectFailed):抽不出帧、模型没
// 回答、输出不可解析,都等于"没验过",不许当成"没有字"。
func DetectOnScreenText(ctx context.Context, client VisionClient, f *ff.FF, videoPath string, duration float64, opt TextDetectOptions) (*TextDetectReport, error) {
	if client == nil {
		return nil, fmt.Errorf("未配置视觉模型")
	}
	if f == nil {
		return nil, fmt.Errorf("%w（无法抽帧）", ff.Missing("ffmpeg"))
	}
	if duration <= 0 {
		info, err := f.Probe(videoPath)
		if err != nil {
			return nil, fmt.Errorf("读不到视频时长,无法定位抽帧点:%w", err)
		}
		duration = info.Duration
	}
	if duration <= 0 {
		return nil, fmt.Errorf("读不到视频时长,无法定位抽帧点")
	}
	width := opt.FrameWidth
	if width <= 0 {
		width = textFrameWidthDefault
	}
	frames, err := sampleTextFrames(ctx, f, videoPath, samplePositions(duration), width)
	if err != nil {
		return nil, err
	}
	return judgeTextFrames(ctx, client, frames)
}

// TextDetectSkipped 构造"没做"的结论(开关关闭 / 未配置视觉模型 / 机械质检未通过)。
//
// 为什么要专门一个构造器:未判定必须能在报告里被认出来。若只写一个
// `HasText=false` 的条目,后面读报告的人(和人写的复盘)会把它当成"验过了,干净" ——
// 而真实情况是"这一镜没人看过"。
func TextDetectSkipped(reason string) *TextDetectReport {
	return &TextDetectReport{Checked: false, Skipped: reason}
}

// TextDetectFailed 构造"看了但失败"的结论(抽帧/调用/解析失败)。
func TextDetectFailed(err error) *TextDetectReport {
	msg := "未知错误"
	if err != nil {
		msg = err.Error()
	}
	return &TextDetectReport{Checked: false, Error: msg}
}

// sampledFrame 一帧已抽好、已编码成 data URL 的采样帧。
type sampledFrame struct {
	Index int     // 1 起
	AtSec float64 // 采样时间点(秒)
	Image string  // data:image/jpeg;base64,…
}

// sampleTextFrames 在给定时间点抽帧并压成 data URL。
//
// 任何一帧抽不出来都直接报错(不 continue 跳过):检测的意义就是"把这些时刻都看过",
// 少看一帧却照样给出"没有字"的结论,正是本项目反复修的那类"报成功其实没做"
// (对照 qc.go 里 darkRatio 的同族注释)。
func sampleTextFrames(ctx context.Context, f *ff.FF, video string, positions []float64, width int) ([]sampledFrame, error) {
	tmpDir, err := os.MkdirTemp("", "manjux-textdet-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	out := make([]sampledFrame, 0, len(positions))
	for i, at := range positions {
		p := filepath.Join(tmpDir, fmt.Sprintf("f%d.jpg", i))
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-ss", fmt.Sprintf("%.3f", at), "-i", video,
			"-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:-2", width), "-q:v", "3", p); err != nil {
			return nil, fmt.Errorf("第 %d 帧(%.2fs)抽帧失败:%w", i+1, at, err)
		}
		raw, err := os.ReadFile(p)
		if err != nil || len(raw) == 0 {
			// `-ss` 超过时长的典型后果是"ffmpeg exit 0 但没有产物"(见 samplePositions
			// 的注释),所以"没产物"必须单独判一次。
			return nil, fmt.Errorf("第 %d 帧(%.2fs)抽帧无产物", i+1, at)
		}
		out = append(out, sampledFrame{
			Index: i + 1, AtSec: at,
			Image: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未成功抽帧")
	}
	return out, nil
}

// judgeTextFrames 把已抽好的帧交给视觉模型逐帧判定。
func judgeTextFrames(ctx context.Context, client VisionClient, frames []sampledFrame) (*TextDetectReport, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("没有可判定的帧")
	}
	imgs := make([]string, 0, len(frames))
	when := make([]string, 0, len(frames))
	for i, fr := range frames {
		imgs = append(imgs, fr.Image)
		when = append(when, fmt.Sprintf("image %d = t≈%.2fs", i+1, fr.AtSec))
	}
	out, err := client.ChatVision(ctx, textDetectSystem, []llm.VisionContent{{
		Role: "user",
		Text: fmt.Sprintf("These %d images are frames sampled from ONE shot of an AI-generated animation, in order (%s). "+
			"For EVERY image, decide whether readable text is rendered in the picture. Return the JSON.",
			len(imgs), strings.Join(when, ", ")),
		Images: imgs,
	}}, true)
	if err != nil {
		return nil, err
	}
	judged, note, err := parseTextDetect(out, frames)
	if err != nil {
		return nil, err
	}
	return buildTextReport(judged, note, len(frames)), nil
}

// textDetectOut 模型原始输出(只取需要的字段)。
type textDetectOut struct {
	Frames []struct {
		Index      int    `json:"index"`
		HasText    bool   `json:"has_text"`
		Confidence int    `json:"confidence"`
		Note       string `json:"note"`
	} `json:"frames"`
	Note string `json:"note"`
}

// parseTextDetect 解析模型输出(纯函数,可穷举)。
//
// 三条纪律,与 identity.go「以逐帧分数为准重算 pass,不信模型自己的汇总字段」同源:
//  1. 只认**采过的那几帧**:模型自造的帧号(比如只发了 5 帧却回答 index 7)直接丢弃 ——
//     它没看过那一帧;
//  2. 逐帧结论才算数,模型 note 里的"总体没问题"不参与判定;
//  3. 一帧都没回答 / 帧号全对不上 = **失败**(未判定),不许当成"画面没有字"。
func parseTextDetect(raw string, frames []sampledFrame) ([]TextFrameHit, string, error) {
	s := stripJSONFence(raw)
	var o textDetectOut
	if err := json.Unmarshal([]byte(s), &o); err != nil {
		return nil, "", fmt.Errorf("画面文字检测输出解析失败: %w", err)
	}
	if len(o.Frames) == 0 {
		return nil, "", fmt.Errorf("模型没有逐帧回答(未判定,不当作通过)")
	}
	at := make(map[int]float64, len(frames))
	for _, fr := range frames {
		at[fr.Index] = fr.AtSec
	}
	judged := make([]TextFrameHit, 0, len(o.Frames))
	for _, fr := range o.Frames {
		sec, ok := at[fr.Index]
		if !ok {
			continue
		}
		judged = append(judged, TextFrameHit{
			Index: fr.Index, AtSec: sec, HasText: fr.HasText,
			Confidence: clampConfidence(fr.Confidence), Note: strings.TrimSpace(fr.Note),
		})
	}
	if len(judged) == 0 {
		return nil, "", fmt.Errorf("模型回答的帧号与采样帧对不上(未判定,不当作通过)")
	}
	sort.Slice(judged, func(i, j int) bool { return judged[i].Index < judged[j].Index })
	return judged, strings.TrimSpace(o.Note), nil
}

// buildTextReport 由逐帧结论折出单镜结论(纯函数,可穷举)。
//
// 刻意**不设置信度门槛**:哪怕模型只给 10 分的把握也算命中。因为这一条是"标记给人
// 看一眼"的监控信号,不是自动重渲的判据 —— 多标一次的成本是看一眼,漏掉一次的成本是
// 整集带字幕发出去(那正是本次要查的事)。
func buildTextReport(judged []TextFrameHit, note string, sampled int) *TextDetectReport {
	r := &TextDetectReport{Checked: true, SampledFrames: sampled, Frames: judged, Note: note}
	for _, f := range judged {
		if !f.HasText {
			continue
		}
		r.HitFrames = append(r.HitFrames, f.Index)
		if f.Confidence > r.MaxConfidence {
			r.MaxConfidence = f.Confidence
		}
	}
	r.HasText = len(r.HitFrames) > 0
	if len(judged) < sampled {
		// 覆盖率如实留痕:模型只回答了一部分帧时,结论的分量本来就更轻。
		extra := fmt.Sprintf("模型仅逐帧回答了 %d/%d 帧", len(judged), sampled)
		if r.Note == "" {
			r.Note = extra
		} else {
			r.Note += "(" + extra + ")"
		}
	}
	return r
}

// TextWarnText 命中时的告警文案(空串 = 不告警,调用方据此决定是否 append)。
func TextWarnText(r *TextDetectReport) string {
	if r == nil || !r.Checked || !r.HasText {
		return ""
	}
	detail := r.Note
	if detail == "" {
		detail = "模型未给出具体文字内容"
	}
	return fmt.Sprintf("画面疑似有烧进画面的文字:帧 %s(共 %d/%d 帧命中,最高置信度 %d%%;%s)—— 这是标记不是判负:场景里的招牌/书页/屏幕属合理内容;但若**台词被画成了字幕**,请先改提示词再重渲",
		intList(r.HitFrames), len(r.HitFrames), r.SampledFrames, r.MaxConfidence, detail)
}

// TextHitAlert 集级命中率判据(纯函数,可穷举)。
//
// 判据:命中镜数 / **实际检测过**的镜数 > TextHitRatioAlarm(10%)且命中镜数 >= TextHitMinShots(2)。
//
//   - 分母为什么用"实际检测过"而不是"本集总镜数":没被看过的镜塞进分母会把命中率
//     稀释掉 —— 与"缺镜把质检结论稀释成全部合格"是同一类错误(见 qcVerdictOf 注释)。
//   - 为什么升级成**显式告警**而不是 fail(清单允许二选一):这一步是监控,单镜误报本来
//     就允许(场景招牌/书页),而 fail 会让批产线整集停住、逼人逐镜复核;真正的重渲决定
//     应当由人看着命中镜号来下。
//   - 为什么必须是"显式":超阈值时阶段结论**不许再说「✔ 质检通过:N/N 镜全部合格」** ——
//     那正是本次整改要防的"以为已经解决了"。调用方把它并入质检结论(见 pipeline.qcVerdictOf)。
func TextHitAlert(examined int, hitShots []int) *TextAlert {
	if examined <= 0 || len(hitShots) < TextHitMinShots {
		return nil
	}
	ratio := float64(len(hitShots)) / float64(examined)
	if ratio <= TextHitRatioAlarm {
		return nil
	}
	return &TextAlert{
		Examined: examined,
		HitShots: append([]int(nil), hitShots...),
		Ratio:    ratio,
		Msg: fmt.Sprintf("画面文字检测命中 %d/%d 镜(%.0f%%,超过 %.0f%% 阈值),疑似「台词被模型烧成字幕」回归 —— 命中镜:%s;建议先核对提示词(<d> 块与 PICTURE MANIFEST)再批量重渲",
			len(hitShots), examined, ratio*100, TextHitRatioAlarm*100, intList(hitShots)),
	}
}

// clampConfidence 把模型给的置信度钳到 0-100(它偶尔会给出 120 或负数)。
func clampConfidence(v int) int {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}

// intList 把镜号/帧号缩成一行「3、7、12」。
func intList(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return strings.Join(parts, "、")
}

// stripJSONFence 从模型输出里取出那个 JSON 对象:去掉 ```json 围栏,并裁掉前后
// 的解释性文字(VLM 即使开了 json_object 也常把 JSON 包在散文里)。
// stripJSONFence 剥掉模型输出外面可能套着的 ```json 围栏，并裁到最外层 `{...}`。
//
// **这是本包唯一的实现**（2026-09-29 代码健康审计 P1-10 合并）。
// 此前 `vlm.go` 与 `identity.go` 各自内联了一份，注释里还写着"那两处刻意没动，
// 所以这里是第三份" —— 而三份已经悄悄漂移：本函数末尾有一次 `TrimSpace`，
// 两个内联版没有。VLM 判分与身份闸门走的正是内联那两份，也就是说**漂移发生在关键路径上**。
// 现在两处都改为调用这里，那份"刻意没动"的注释也删了。
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	if a := strings.Index(s, "{"); a >= 0 {
		if b := strings.LastIndex(s, "}"); b > a {
			s = s[a : b+1]
		}
	}
	return strings.TrimSpace(s)
}

// textDetectSystem 系统提示词(英文,与本包既有的 vlmSystem / identitySystem 同一口径)。
//
// 判据写死三条:①只看画面里**真的渲染出来**的字,不靠剧情猜;②糊到读不出、纯装饰纹样
// 不算字;③要报出看到的字与位置 —— 因为下一步是人拿这条去对提示词,只说"有字"没法定位。
const textDetectSystem = `You are a strict QC spotter for text baked into AI-generated footage of an animated short drama.
You are given several frames sampled from ONE generated shot. For EVERY frame, decide whether the picture contains ANY readable text:
subtitles or captions, titles, watermarks, shop signs, posters, banners, book covers, lettering on clothing, licence plates,
on-screen UI or HUD elements, floating or ghostly lettering, and any glyph-like marking a human could actually read
(any language, including Chinese characters).
Rules:
- Judge ONLY what is visibly rendered in the image. Never infer text from the story, the shot description or what you expect a drama to contain.
- A smear or an abstract decorative pattern that cannot be read is NOT text. A single clearly readable character IS text.
- Report briefly what the text says and roughly where it sits, so a human can match it back to the prompt.
Return STRICT JSON ONLY:
{"frames":[{"index":1,"has_text":true/false,"confidence":0-100,"note":"中文:看到的文字内容与位置;没有则留空"}],"note":"中文一句话总体结论"}
"confidence" is how sure you are about THIS frame's has_text answer.`
