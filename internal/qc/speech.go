// speech.go 一镜的**语音核对**（ASR 转写 + 与计划台词对账）。
//
// 【为什么质检里必须有这一条】（用户 2026-10-07：「配音存在乱配音，需要排查处理」）
// H3 是**音画同生**模型：台词说不说、说什么、有没有重复，全都发生在音轨里，而字幕是平台
// 后期自己烧上去的 —— 两者是两回事。此前质检能验的只有"音轨里有没有人声/人声有多长"
// （qc.SpeechSec 与 ExpectedSpeechSec 那一对），量不到"念的是不是那句台词"。
// 有了 ASR 之后这条才成为可能：真机第01集 13 镜里当场抓到 3 类硬伤
// （镜 3 整句没念 + 凭空多出一串、镜 4/6/7 同句念 2~3 遍、镜 1/11 多出剧本里没有的话）。
//
// 【处置：只标记、不拦批次、不自动重渲】（与 VLM 审片、画面文字检测同一原则）
// 判据是**听出来的**：ASR 的同音字误差与转写幻觉都存在（真机镜 11 的那串胡话到底是 H3 编的
// 还是 Whisper 听岔，得人听一遍才能定），而重渲一镜要十几分钟 GPU。所以这里只把线索摆出来，
// 由用户看着镜号决定。**warn 级**——上一轮暗调误判把批量产线卡在第 1/88 集的教训还在。
//
// 【没装 ASR 就说没验过】报告里写 skipped 原因，不假装验过、也不当成不合格（见 pipeline 的
// planSpeechCheck 与 stages_qc_assemble.go 的接线）。
package qc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/asr"
)

// SpeechReport 一镜的语音核对结果。Checked=false 时 Skipped/Error 里写着为什么没验。
type SpeechReport struct {
	Checked  bool            `json:"checked"`
	Skipped  string          `json:"skipped,omitempty"` // 没验的原因（开关关掉时连条目都不写）
	Error    string          `json:"error,omitempty"`   // 验了但失败（模型/文件/超时）
	Tool     string          `json:"tool,omitempty"`    // 用的哪个 ASR 入口（诊断用）
	Model    string          `json:"model,omitempty"`
	Segments int             `json:"segments"`           // 转写出的语音段数（0 = 整镜没人说话）
	Text     string          `json:"text,omitempty"`     // ASR 转写全文（音轨里到底说了什么）
	Lines    []asr.LineCheck `json:"lines,omitempty"`    // 逐句：与计划台词的相似度/出现段数（**数据**，不判负）
	Findings []asr.Finding   `json:"findings,omitempty"` // 对账结论（repeat / extra / missing）
}

// SpeechSkipped 构造"没验"的结论（开关关掉时不该用它 —— 那种情况连条目都不写）。
func SpeechSkipped(reason string) *SpeechReport {
	return &SpeechReport{Skipped: reason}
}

// SpeechFailed 构造"验了但失败"的结论。
func SpeechFailed(err error) *SpeechReport {
	msg := "未知错误"
	if err != nil {
		msg = err.Error()
	}
	return &SpeechReport{Error: msg}
}

// SpeechWarnText 把一条对账结论翻成给人看的告警文案。
//
// 【为什么要把结论分三类说】三类问题的返工方式完全不同：重复/自造是**提示词没约束住**
// （见 pipeline 的 SPOKEN LINES ONLY 条款），漏念多半是**镜头时长不够说这句话**或台词被截断；
// 混成一句"配音有问题"用户没法动手。
func SpeechWarnText(f asr.Finding) string {
	switch f.Kind {
	case "repeat":
		return fmt.Sprintf("🔊 配音疑似重复：%s（可以听一遍确认；同一 prompt 重渲大概率复现，要改就得改提示词或换 seed）",
			briefLine(f.Line)+f.Detail)
	case "extra":
		// 不加前缀：Detail 本身就写清了「音轨里说了计划之外的话：…（也可能是 ASR 听岔）」，
		// 再加一层「出现了剧本里没有的话」只会变成叠字。
		return "🔊 " + f.Detail
	case "missing":
		return fmt.Sprintf("🔊 配音疑似漏念：%s —— %s", briefLine(f.Line), f.Detail)
	default:
		return "🔊 语音对账：" + f.Detail
	}
}

// briefLine 把台词缩到一句话能放下的长度（告警行不该被一句长台词撑爆）。
func briefLine(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= 16 {
		return "「" + s + "」 "
	}
	return "「" + string(r[:16]) + "…」 "
}

// SpeechSummary 一镜核对结果的**一行摘要**（日志/阶段详情用）。没验过就如实说没验过。
func SpeechSummary(s *SpeechReport) string {
	if s == nil {
		return ""
	}
	switch {
	case s.Skipped != "":
		return "语音核对跳过：" + s.Skipped
	case s.Error != "":
		return "语音核对失败（未判定，不当作通过）：" + s.Error
	}
	if len(s.Findings) == 0 {
		return fmt.Sprintf("语音核对通过：ASR 转出 %d 段，与剧本 %d 句对得上", s.Segments, len(s.Lines))
	}
	return fmt.Sprintf("语音核对发现 %d 处问题：%s", len(s.Findings), findingKinds(s.Findings))
}

// findingKinds 把结论按类型汇总成「重复 2 处、自造 1 处」。
func findingKinds(fs []asr.Finding) string {
	n := map[string]int{}
	var order []string
	for _, f := range fs {
		if _, seen := n[f.Kind]; !seen {
			order = append(order, f.Kind)
		}
		n[f.Kind]++
	}
	label := map[string]string{"repeat": "疑似重复", "extra": "计划外的话", "missing": "疑似漏念"}
	var parts []string
	for _, k := range order {
		name := label[k]
		if name == "" {
			name = k
		}
		parts = append(parts, fmt.Sprintf("%s %d 处", name, n[k]))
	}
	return strings.Join(parts, "、")
}

// CheckSpeech 跑一次语音核对：转写这一镜的音轨，再与**计划台词**对账。
//
// planned 用 project.PlanSpokenTexts 取（台词原文按口播顺序 + 旁白）。
// 转写失败不返回 error 而是返回带 Error 的报告：调用方一律把它写进 qc_report.json ——
// "没验过"与"验过且干净"必须在报告里分得开（这是这一整套代码里最容易被做坏的一点）。
//
// 超时：CPU medium 实测约每 10 秒素材 12~15 秒（含首次加载模型约 10 秒），这里按素材时长给
// 20 倍 + 120 秒的余量；超时也算"没验过"，不静默。
func CheckSpeech(ctx context.Context, tool, model, clip string, planned []string) *SpeechReport {
	r, err := asr.Transcribe(ctx, tool, clip, model)
	if err != nil {
		return SpeechFailed(err)
	}
	v := asr.Compare(planned, r)
	return &SpeechReport{
		Checked:  true,
		Tool:     r.Tool,
		Model:    r.Model,
		Segments: len(r.Segments),
		Text:     r.Text(),
		Lines:    v.Lines,
		Findings: v.Findings,
	}
}

// SpeechBudget ASR 转写的超时预算（素材时长 → 允许等多久）。
func SpeechBudget(durationSec float64) time.Duration {
	if durationSec <= 0 {
		durationSec = 8 // 单镜计划时长的下限
	}
	d := time.Duration(durationSec*20) * time.Second
	if d < 120*time.Second {
		d = 120 * time.Second
	}
	return d
}
