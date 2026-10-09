package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/compose"
	"github.com/YiIimini/ManJuX/internal/filmaudit"
	"github.com/YiIimini/ManJuX/internal/project"
)

// film_audit.go 合片后的**开演前体检**：把 internal/filmaudit 的结论逐条播报出来。
//
// 【为什么要有（2026-10-09 用户：「问题好多啊」）】此前每一层都有自己的门（契约门 / 产出侧判据 /
// 质检），但**没有任何一处把"这一集到底行不行"合起来说一遍**：字幕挂满整镜、同场接缝全叠化、
// 说话人不在画面里还被标成画外音、该念的没念 —— 这些都能从盘上算出来，却要等用户打开成片才看见。
//
// 与 `cmd/auditfilm` 用**同一套判据**（internal/filmaudit）：一处判据两处消费，
// 免得"命令行说没问题、界面上没说"。全程确定性（读盘 + ffprobe），不改文件。
func (c *Coordinator) auditProducedFilm(ctx context.Context, proj *project.Project,
	plan *project.Plan, clips []string, res *compose.Result, outDir string) {
	if plan == nil || len(plan.Shots) == 0 || res == nil {
		return
	}
	in := filmaudit.Input{
		ID:         proj.ID,
		Plan:       plan,
		Transition: proj.Config.Params.Assemble.Transition,
		FadeSec:    proj.Config.Params.Assemble.FadeSec,
		// 本机这版方案（若刚从盘上读出来）里被纠正的误标句数 —— 就是"已提交的提示词带着旧标法"的那个数。
		RawInFrameAsOffscreen: plan.SpeakerOSFixed,
	}
	in.Durations = make([]float64, len(plan.Shots))
	in.ASRSpans = make([][]asr.Segment, len(plan.Shots))
	for i := range plan.Shots {
		if i >= len(clips) {
			break
		}
		clip := clips[i]
		if c.ff != nil {
			if info, err := c.ff.Probe(clip); err == nil && info != nil {
				in.Durations[i] = info.Duration
			}
		}
		if r, err := asr.Parse(filmaudit.ASRSidecarPath(clip)); err == nil && r != nil {
			in.ASRSpans[i] = r.Segments
			if in.Durations[i] == 0 && r.Duration > 0 {
				in.Durations[i] = r.Duration
			}
		}
	}
	if res.SRTPath != "" {
		if b, err := os.ReadFile(res.SRTPath); err == nil {
			in.Cues = filmaudit.ParseSRT(string(b))
		}
	}
	in.FinalDuration = res.Duration

	fs := filmaudit.Audit(in)
	fail, warn, info := filmaudit.CountLevels(fs)
	if fail+warn == 0 {
		c.emit(proj.ID, PhaseAssemble, 0, "success", 100,
			"✔ 开演前体检通过：字幕落点、说话人、接缝、结构四类都干净（%d 条如实告知）", info)
		return
	}
	c.emit(proj.ID, PhaseAssemble, 0, "warn", 100,
		"🩺 开演前体检：⛔%d 条必须处理 / ⚠%d 条该看一眼 —— 下面逐条点名（全表见 output/film_audit.md，"+
			"或随时 `go run ./cmd/auditfilm -project <书名> -md`）", fail, warn)
	shown := 0
	for _, f := range fs {
		if f.Level == "info" {
			continue
		}
		if shown >= 8 {
			c.emit(proj.ID, PhaseAssemble, 0, "info", 100, "…其余 %d 条同类结论见 film_audit.md", fail+warn-shown)
			break
		}
		level := "warn"
		if f.Level == "fail" {
			level = "error"
		}
		c.emit(proj.ID, PhaseAssemble, 0, level, 100, "%s [%s] %s",
			filmaudit.LevelEmoji(f.Level), f.Where, f.Msg)
		shown++
	}
	// 落一份 Markdown（与 cli 同格式）—— "界面上报了"和"留一份能翻的记录"是两件事。
	p := filepath.Join(outDir, "film_audit.md")
	if werr := os.WriteFile(p, []byte(filmAuditReport(in.ID, fs)), 0o644); werr != nil {
		c.emit(proj.ID, PhaseAssemble, 0, "warn", 100, "⚠ 体检报告没写出(%v) —— 结论已在上面的播报里", werr)
	}
}

// filmAuditReport 渲染 Markdown（与 cmd/auditfilm 的 report() 同一形状）。
func filmAuditReport(id string, fs []filmaudit.Finding) string {
	fail, warn, info := filmaudit.CountLevels(fs)
	s := fmt.Sprintf("# 成片体检 · %s\n\n结论：⛔ %d 条必须处理 · ⚠ %d 条该看一眼 · ·%d 条如实告知\n\n"+
		"| 级别 | 位置 | 结论 |\n|---|---|---|\n", id, fail, warn, info)
	for _, f := range fs {
		s += fmt.Sprintf("| %s | %s | %s |\n", f.Level, f.Where, f.Msg)
	}
	return s
}
