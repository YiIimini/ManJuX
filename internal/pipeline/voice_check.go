package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// voice_check.go **音色/语音核对 × 有界重出**的接线。
//
// 【这一层为什么必须存在】（2026-10-08 用户第二次报「女的配男音」+「重复配音」）
//
// 平台已经把"声线绑成角色卡原文"做到了（voice_rebind.go），可真机证明那还不够：
//
//	镜 5  提交的提示词逐字写着 `Mature mid-range female voice…`，念出来是 130 Hz 的**男声**
//	镜 6  逐字写着 `Soft young male voice…`，念出来是 180~266 Hz 的**女声**
//
// 也就是说 **H3 把声线文字当"音频条件"，不保证照做** —— 光核对提示词永远发现不了这类缺陷，
// 只有把**音轨量出来/转出来**才判得了（判据见 internal/qc/pitch.go 与 internal/qc/speech.go）。
// 同一集里"重复配音"（假起手：先含糊念一遍、再从头念对的）也是同一类问题：
// 真机镜 5 实测 `[1.46-4.96] 她就拆开,她熊居一射道…` + `[4.96-10.66] 她就拆开,一张都不超过一万…`。
//
// 判据来源刻意选"提交版提示词"而不是"卡片文本"：装配会把权威声线逐字写进每一句台词的
// 引述里（`名字 (S2) says in <声线>: <d>…</d>`），所以直接解析那段引述就能拿到**这一镜
// 真正被要求的**声线 —— 包括"认不出说话人而保留模型原话"的那些句子（那种情况下
// 模型自己写的字就是音频条件，拿它当期望值才是对的）。
//
// 处置（**有界重出**）：判负 ⇒ 换种子重出这一轮，最多 maxVoiceRetryPasses 轮。
// 这是本项目一贯的"生成 → 机器判定 → 把违规喂回去重来"闭环（见 internal/director/repair.go）；
// 有界是为了不把 GPU 时间无上限地烧在抽卡上 —— 用完了就把剩下的不符**如实摆出来**。
const (
	// maxVoiceRetryPasses 判负时最多再重出几轮（每轮对不合格的镜头换一次种子）。
	maxVoiceRetryPasses = 2
	// voiceRetrySeedStep 每轮重出在上一轮种子上加的步长（见 voiceRetryNextSeed）。
	voiceRetrySeedStep = 1017
	// voiceRetrySeedBase 没有上一轮种子（界面没填 seed）时的重出基数。
	voiceRetrySeedBase = 20261008
)

// voiceRetryNextSeed 下一轮重出用的种子：**必须与上一轮不同**（同一颗种子重出等于念同一遍）。
//
// 为什么不是简单写"固定基数 + 轮次"：界面上填的种子（或命令行 -seed）可能正好等于那个基数 ——
// 真机实测踩过：-seed 与重出基数都取了 20261008，重出那一轮算出的**指纹与上一轮逐字一致**
// ⇒ 平台判"产物有效,跳过(断点续跑)"，**一轮重出变成一次空转**（日志上却写着"已重出 1 轮"）。
// 所以：有上一轮种子就在它基础上加一个正步长（差值恒为正 ⇒ 指纹必然不同）；
// 没有（界面没填）才退到固定基数。
func voiceRetryNextSeed(prev int64, pass int) int64 {
	if prev > 0 {
		return prev + voiceRetrySeedStep
	}
	return voiceRetrySeedBase + int64(pass+1)*voiceRetrySeedStep
}

// VoiceLinesFromPrompt 解析**提交版提示词**里每句台词的"说话人 + 声线"。
//
// 句式是平台自己写的（voiceClause）：`<名字> (S2) says [off-screen] in <声线>: <d>…</d>`；
// 认不出说话人的句子保留模型原话 ⇒ 这里也会解析到模型写的声线（那正是音频条件）。
// 解析不到就跳过这一句（不猜）。
func VoiceLinesFromPrompt(prompt string) []qc.VoiceLine {
	ms := reDialogueTag.FindAllStringSubmatchIndex(prompt, -1)
	var out []qc.VoiceLine
	for _, m := range ms {
		raw := prompt[m[2]:m[3]]
		if !hasSpokenText(raw) {
			continue // 空标签/占位/规则正文里的示例标签
		}
		head := voiceClauseHead(prompt, m[0])
		speaker, voice, ok := parseVoiceClause(head)
		if !ok {
			continue
		}
		// 台词只留内容本身（语言标签不是台词的一部分，与 project.PlanSpokenTexts 同口径）。
		text := strings.TrimSpace(reLangTag.ReplaceAllString(strings.TrimSpace(raw), ""))
		out = append(out, qc.VoiceLine{Speaker: speaker, Voice: voice, Text: text})
	}
	return out
}

// parseVoiceClause 从 `… says [off-screen] in <声线>: ` 里取出说话人名与声线文本。
func parseVoiceClause(head string) (speaker, voice string, ok bool) {
	marker := " says off-screen in "
	i := strings.LastIndex(head, marker)
	if i < 0 {
		marker = " says in "
		i = strings.LastIndex(head, marker)
	}
	if i < 0 {
		return "", "", false
	}
	voice = strings.TrimRight(strings.TrimSpace(head[i+len(marker):]), ": \t")
	if voice == "" {
		return "", "", false
	}
	before := head[:i]
	// 引述头里可能还带着**上一刀**的尾巴（台词标签/小镜头标记）或画面描述：
	// 说话人只取最近一个边界（`</d>`、`<d>`、`[Shot`、换行）之后的那一段。
	for _, sep := range []string{"</d>", "<d>", "[Shot", "\n"} {
		if k := strings.LastIndex(before, sep); k >= 0 {
			before = before[k+len(sep):]
		}
	}
	before = reSpeakerID.ReplaceAllString(strings.TrimSpace(before), " ")
	// 余下常是"…画面描述, 韩沿"：说话人取最后一个分隔符之后的那段。
	if k := strings.LastIndexAny(before, ",;，；."); k >= 0 {
		before = before[k+1:]
	}
	return strings.TrimSpace(before), voice, true
}

// submittedVoiceLines 本镜"提交版提示词"的声线清单。
//
// 装配是**纯函数式**的，但它会累计 plan 上的播报计数（VoiceRebound 之类）——
// 这里只是"拿提交版提示词看一眼"，不该改动交付数字：快照后还原。
func submittedVoiceLines(proj *project.Project, plan *project.Plan, shot *project.Shot) []qc.VoiceLine {
	if plan == nil || shot == nil {
		return nil
	}
	rb, un, sp := plan.VoiceRebound, plan.VoiceUnresolved, plan.SpeechPlacementSkipped
	vcs := plan.VoiceClashStripped
	out := ShotRenderPrompt(proj, plan, shot)
	plan.VoiceRebound, plan.VoiceUnresolved, plan.SpeechPlacementSkipped = rb, un, sp
	plan.VoiceClashStripped = vcs
	return VoiceLinesFromPrompt(out)
}

// clipPathOf 清单里记的产物路径（相对项目目录）→ 绝对路径；没产物/占位片段返回空串。
func clipPathOf(dir string, m project.ClipManifest) string {
	if strings.TrimSpace(m.File) == "" || isPlaceholderClip(m) {
		return ""
	}
	return filepath.Join(dir, filepath.FromSlash(m.File))
}

// qualityRetryTargets 这一轮里"音色或语音与权威文本不符"的镜头。
//
// 两类判据都挂在这一个收口上：
//
//	① 音色（基频，见 qc/pitch.go）：不需要任何外部工具；
//	② 语音（ASR 对账，见 qc/speech.go）：只在装了 ASR（且项目开了语音核对）时跑，
//	   而且**只认"念重了/多念了"**（repeat / extra）—— 那正是用户报的「重复配音」；
//	   `missing`（漏念）刻意不在此重出：它多半是镜头时长不够/台词被截断，换种子抽卡解决不了，
//	   先如实标记（与质检同一口径）。
//
// 只查**本次请求范围内的镜头**（用户点了哪几镜就查哪几镜）：全片自动扫一遍虽然"更聪明"，
// 但会让"只重渲一镜"变成"顺手重渲另外三镜"—— 那是用户没点的 GPU 时间。
// 存量镜头的问题由质检报告摆出来（同一套判据）。
//
// 判负的文案逐镜 emit（说清哪一镜、卡上是什么、实测多少 Hz / 音轨里多念了什么）。
func (c *Coordinator) qualityRetryTargets(ctx context.Context, proj *project.Project, plan *project.Plan,
	shots []project.Shot, manifest map[string]project.ClipManifest) []int {
	if plan == nil || proj == nil {
		return nil
	}
	p := proj.Config.Params.QC
	asrTool := ""
	if p.ASROn() {
		asrTool = asr.Locate()
	}
	dir := c.store.Dir(proj.ID)
	var bad []int
	for i := range shots {
		sh := shots[i]
		m := manifest[strconv.Itoa(sh.ID)]
		clip := clipPathOf(dir, m)
		if clip == "" {
			continue // 没产物/占位片段：交给失败链路，不是"质量重出"的事
		}
		reasons := 0
		// 音色核对（基频）也受 `qc.enable_pitch` 管：关掉它连**重出**都不该发生
		//（否则用户关了核对，渲染阶段照样为它换种子重出、白烧 GPU）。
		if c.ff != nil && p.PitchOn() {
			rep := qc.MeasureVoicePitch(ctx, c.ff, clip, submittedVoiceLines(proj, plan, &sh))
			if rep != nil && rep.Bad() {
				reasons++
				c.emit(proj.ID, PhaseRender, sh.ID, "warn", 0, "%s", qc.VoicePitchWarnText(rep.Findings[0]))
			}
		}
		if asrTool != "" {
			sctx, cancel := context.WithTimeout(ctx, qc.SpeechBudget(float64(sh.Duration)))
			srep := qc.CheckSpeech(sctx, asrTool, p.ASRModelOr(), clip, project.PlanSpokenTexts(&sh))
			cancel()
			// 没跑成必须留痕：ASR 挂了/超时 = **这一镜的语音没验**，不是"验过且干净"，
			// 更不据此重出（判据不成立就不动手）。判据与质检阶段同一条（SpeechReport.Error）。
			if srep.Error != "" {
				c.emit(proj.ID, PhaseRender, sh.ID, "warn", 0,
					"镜头 %d 语音核对未跑成(不当作通过,也不据此重出):%s", sh.ID, srep.Error)
			}
			for _, f := range srep.Findings {
				if f.Kind != "repeat" && f.Kind != "extra" {
					continue
				}
				reasons++
				c.emit(proj.ID, PhaseRender, sh.ID, "warn", 0, "%s", qc.SpeechWarnText(f))
			}
		}
		if reasons > 0 {
			bad = append(bad, sh.ID)
		}
	}
	return bad
}

// voicePitchForQC 质检侧的同一套判据（在**镜级原始产物**上量，与语音核对同一位置）。
func voicePitchForQC(ctx context.Context, c *Coordinator, proj *project.Project, plan *project.Plan,
	shot *project.Shot, clip string) *qc.VoicePitchReport {
	if c.ff == nil {
		return qc.VoicePitchSkipped("本机没有 ffmpeg（量不了基频）")
	}
	return qc.MeasureVoicePitch(ctx, c.ff, clip, submittedVoiceLines(proj, plan, shot))
}

// qualityRetryNote 重出轮次的播报文案（判据与文案同源，便于测试钉住）。
func qualityRetryNote(round, total int, ids []int) string {
	return fmt.Sprintf("🎙 有 %d 镜的**音色或语音**与方案不符（第 %d/%d 轮）：%s —— "+
		"**换种子重出这一轮**（提示词里的声线/台词是逐字写对的，是 H3 没照做；"+
		"重出用的种子是确定的，不受界面 seed 影响）", len(ids), round, total, shotIDList(ids))
}
