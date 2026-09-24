package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"strings"

	"github.com/YiIimini/ManJuX/internal/compose"
	"github.com/YiIimini/ManJuX/internal/fsutil"
	"github.com/YiIimini/ManJuX/internal/llm"
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
	vlmFlagged := 0 // VLM 审片未通过的镜头数(只标记,不自动重渲)

	// VLM 智能审片(可选):抽帧判分 + 重渲建议
	var vlmClient *llm.Client
	if p.EnableVLM {
		g := c.cfg.Get()
		if g.VLM.BaseURL != "" && g.VLM.APIKey != "" && g.VLM.Model != "" {
			vlmClient = llm.New(g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model, 120)
		} else {
			c.emit(proj.ID, PhaseQC, 0, "warn", 0, "VLM 审片已开启但视觉模型未配置(设置页填写),跳过智能判分")
		}
	}

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
			// 台词应有口播时长:与渲染时长同源(镜头时长已按台词做过预算),这里
			// 用同一套语速常量核对成片里到底说没说够 —— 词与曲同源才比得准。
			ExpectedSpeechSec: compose.ShotSpeechBudget(shot),
		})
		if vlmClient != nil && r.OK {
			brief := fmt.Sprintf("%s/%s/角色:%v", shot.ShotSize, shot.Camera, shot.Characters)
			if vr, verr := qc.VLMJudge(ctx, vlmClient, c.ff.Ffmpeg, full, brief); verr == nil {
				r.VLM = vr
				if !vr.Pass {
					vlmFlagged++
					c.emit(proj.ID, PhaseQC, shot.ID, "warn", checked*100/max(1, len(plan.Shots)),
						"镜头 %d VLM 判分 %d(%s)建议:%s", shot.ID, vr.Score, strings.Join(vr.Issues, ";"), vr.Advice)
				}
			} else {
				c.emit(proj.ID, PhaseQC, shot.ID, "warn", 0, "镜头 %d VLM 审片失败:%v(不影响机械质检)", shot.ID, verr)
			}
		}
		reports[key] = *r
		pct := checked * 100 / max(1, len(plan.Shots))
		if r.OK {
			score := ""
			if r.VLM != nil {
				score = fmt.Sprintf(",VLM %d 分", r.VLM.Score)
			}
			c.emit(proj.ID, PhaseQC, shot.ID, "info", pct, "镜头 %d 通过(暗像素 %.0f%%,%.1fs,音轨%v%s)",
				shot.ID, r.DarkRatio*100, r.Duration, audioLabel(r.HasAudio), score)
		} else {
			failed++
			c.emit(proj.ID, PhaseQC, shot.ID, "error", pct, "✕ 镜头 %d 不合格:%s", shot.ID, strings.Join(r.Problems, ";"))
		}
		for _, w := range r.Warnings {
			c.emit(proj.ID, PhaseQC, shot.ID, "warn", pct, "镜头 %d 警告:%s", shot.ID, w)
		}
	}
	raw, _ := json.MarshalIndent(reports, "", "  ")
	reportPath := filepath.Join(c.store.Dir(proj.ID), "output", "qc_report.json")
	// 报告写入失败必须让用户看见:全流程模式下质检错误已被降级为 warn,若报告
	// 再写不进去,用户就只剩一句「质检通过」可看,问题彻底隐形。
	if werr := fsutil.WriteFileAtomic(reportPath, raw); werr != nil {
		c.emit(proj.ID, PhaseQC, 0, "warn", 0, "⚠ 质检报告写入失败(%v),详细结果仅在本次会话可见", werr)
	}
	if checked == 0 {
		return fmt.Errorf("没有已完成的镜头可质检:请先运行「渲染出片」")
	}
	if failed > 0 {
		return fmt.Errorf("质检发现 %d 个不合格镜头(报告见 output/qc_report.json),请在故事板中单镜重渲", failed)
	}
	if vlmFlagged > 0 {
		// 只标记、不自动重渲:同一 prompt 同一 seed 重跑大概率复现同样结果,
		// 真正的返工要改提示词或换 seed,由用户看过标记后自行决定(2026-09-11 定调)。
		c.emit(proj.ID, PhaseQC, 0, "warn", 100,
			"⚠ 质检完成:%d 镜机械指标合格,其中 %d 镜 VLM 审片未通过(详情见 output/qc_report.json 或阶段详情),未自动重渲",
			checked, vlmFlagged)
		return nil
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

// joinStrings / max 两个手写实现已移除:前者等价于 strings.Join(且原实现在
// 循环内 += 拼接是 O(n²)),后者是 Go 1.21+ 的内建 max —— 项目用 Go 1.25,
// 自己再定义一份只会遮蔽内建、并与其它包的同名实现分叉。

// ---------- 阶段五:assemble ----------

// resolveFadeSec 转场时长的取值顺序:项目参数 > 全局设置 > 兜底 0.5。
//
// 抽成纯函数是为了让"设置页到底算不算数"可被穷举验证 —— 这类缺陷(字段有界面、
// 能保存、而实际生效处写死常量)不会报错、不会有日志,只有针对性断言能钉住。
func resolveFadeSec(projectSec, globalSec float64) float64 {
	if projectSec > 0 {
		return projectSec
	}
	if globalSec > 0 {
		return globalSec
	}
	return 0.5
}

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
	// 转场时长:项目参数优先 → 设置页 render.fade_sec → 兜底 0.5(见 resolveFadeSec)
	fade := resolveFadeSec(p.FadeSec, c.cfg.Get().Render.FadeSec)
	c.emit(proj.ID, PhaseAssemble, 0, "info", 2, "开始合成(%d 镜,转场=%s %.1fs,字幕=%v)", len(clips), p.Transition, fade, p.BurnSubtitles)
	res, err := compose.Compose(ctx, c.ff, clips, plan, outDir, compose.Params{
		Width: proj.Config.Params.Render.Width, Height: proj.Config.Params.Render.Height,
		Transition: p.Transition, BurnSubtitles: p.BurnSubtitles,
		SubtitleSize: p.SubtitleSize, Loudness: p.Loudness, FadeSec: fade,
		// 峰值归一:同一个"假开关"的另一半 —— 参数一直存在,而真正生效的
		// loudnorm TP 写死 -1.5(审计 P1-1)。
		Peak:    p.Peak,
		Denoise: p.DenoiseOn(),
	}, func(pct int, note string) {
		c.emit(proj.ID, PhaseAssemble, 0, "info", pct, "%s", note)
	})
	if err != nil {
		return err
	}
	// 音频实况如实回显:接缝是否切在静音、响度是两遍还是降级,用户一眼能判断
	// 「配音不完整/有杂音」这类问题这次到底修没修到。
	for _, note := range res.AudioNotes {
		c.emit(proj.ID, PhaseAssemble, 0, "info", 96, "🔊 %s", note)
	}
	c.emit(proj.ID, PhaseAssemble, 0, "success", 100, "✔ 成片就绪:%s(%.0f 秒)", filepath.Base(res.FinalPath), res.Duration)

	// 云端 2K 定稿(可选,消耗云额度)
	if p.Upgrade2K {
		c.emit(proj.ID, PhaseAssemble, 0, "info", 100, "按配置自动升级 2K…")
		if err := c.Upgrade2K(ctx, proj.ID, 0); err != nil {
			// 2K 失败不回滚成片,仅提示
			c.emit(proj.ID, PhaseAssemble, 0, "warn", 100, "⚠ 自动 2K 定稿失败:%v(768P 成片已保留)", err)
		}
	}
	return nil
}
