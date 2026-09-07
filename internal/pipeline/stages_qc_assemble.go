package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/YiIimini/ManJuX/internal/compose"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// ---------- 阶段四:qc ----------

func (c *Coordinator) runQC(ctx context.Context, proj *project.Project) error {
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		return fmt.Errorf("尚未生成分镜方案")
	}
	manifest, err := c.store.LoadManifest(proj.ID)
	if err != nil {
		return err
	}
	p := proj.Config.Params.QC
	if !p.Enabled {
		c.emit(proj.ID, PhaseQC, 0, "info", 100, "质检已关闭,跳过")
		return nil
	}
	reports := map[string]qc.Report{}
	failed := 0
	checked := 0
	for _, shot := range plan.Shots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		key := strconv.Itoa(shot.ID)
		m, ok := manifest[key]
		if !ok || m.Status != project.ShotDone || m.File == "" {
			continue
		}
		full := filepath.Join(c.store.Dir(proj.ID), m.File)
		if _, err := os.Stat(full); err != nil {
			continue
		}
		checked++
		r := qc.Check(ctx, c.ff, shot.ID, full, qc.Params{
			DarkRatioFail: p.DarkRatioFail, DarkRatioWarn: p.DarkRatioWarn,
			MinDuration: p.MinDuration, MaxDuration: p.MaxDuration,
		})
		reports[key] = *r
		pct := checked * 100 / max(1, len(plan.Shots))
		if r.OK {
			c.emit(proj.ID, PhaseQC, shot.ID, "info", pct, "镜头 %d 通过(暗像素 %.0f%%,%.1fs,音轨%v)",
				shot.ID, r.DarkRatio*100, r.Duration, audioLabel(r.HasAudio))
		} else {
			failed++
			c.emit(proj.ID, PhaseQC, shot.ID, "error", pct, "✕ 镜头 %d 不合格:%s", shot.ID, joinStrings(r.Problems, ";"))
		}
		for _, w := range r.Warnings {
			c.emit(proj.ID, PhaseQC, shot.ID, "warn", pct, "镜头 %d 警告:%s", shot.ID, w)
		}
	}
	raw, _ := json.MarshalIndent(reports, "", "  ")
	_ = os.MkdirAll(filepath.Join(c.store.Dir(proj.ID), "output"), 0o755)
	_ = os.WriteFile(filepath.Join(c.store.Dir(proj.ID), "output", "qc_report.json"), raw, 0o644)
	if checked == 0 {
		return fmt.Errorf("没有已完成的镜头可质检:请先运行「渲染出片」")
	}
	if failed > 0 {
		return fmt.Errorf("质检发现 %d 个不合格镜头(报告见 output/qc_report.json),请在故事板中单镜重渲", failed)
	}
	c.emit(proj.ID, PhaseQC, 0, "success", 100, "✔ 质检通过:%d 镜全部合格", checked)
	return nil
}

func audioLabel(has bool) string {
	if has {
		return "有"
	}
	return "无"
}

func joinStrings(ss []string, sep string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------- 阶段五:assemble ----------

func (c *Coordinator) runAssemble(ctx context.Context, proj *project.Project) error {
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		return fmt.Errorf("尚未生成分镜方案")
	}
	manifest, err := c.store.LoadManifest(proj.ID)
	if err != nil {
		return err
	}
	// 按镜头顺序收集已完成产物
	var clips []string
	for _, shot := range plan.Shots {
		key := strconv.Itoa(shot.ID)
		m, ok := manifest[key]
		if !ok || m.Status != project.ShotDone || m.File == "" {
			return fmt.Errorf("镜头 %d 尚未渲染完成:请先运行「渲染出片」", shot.ID)
		}
		full := filepath.Join(c.store.Dir(proj.ID), m.File)
		if _, err := os.Stat(full); err != nil {
			return fmt.Errorf("镜头 %d 产物缺失(%s):请重渲", shot.ID, m.File)
		}
		clips = append(clips, full)
	}
	p := proj.Config.Params.Assemble
	outDir := filepath.Join(c.store.Dir(proj.ID), "output")
	_ = os.MkdirAll(outDir, 0o755)
	c.emit(proj.ID, PhaseAssemble, 0, "info", 2, "开始合成(%d 镜,转场=%s,字幕=%v)", len(clips), p.Transition, p.BurnSubtitles)
	res, err := compose.Compose(ctx, c.ff, clips, plan, outDir, compose.Params{
		Width: proj.Config.Params.Render.Width, Height: proj.Config.Params.Render.Height,
		Transition: p.Transition, BurnSubtitles: p.BurnSubtitles,
		SubtitleSize: p.SubtitleSize, Loudness: p.Loudness, FadeSec: 0.5,
	}, func(pct int, note string) {
		c.emit(proj.ID, PhaseAssemble, 0, "info", pct, "%s", note)
	})
	if err != nil {
		return err
	}
	c.emit(proj.ID, PhaseAssemble, 0, "success", 100, "✔ 成片就绪:%s(%.0f 秒)", filepath.Base(res.FinalPath), res.Duration)
	return nil
}
