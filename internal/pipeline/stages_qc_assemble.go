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
	// notChecked 本应质检、却因为没有可用产物而根本没被看的镜头(未渲染/失败/文件丢了)。
	//
	// 为什么要单独数:旧实现只在 checked==0 时报错,一旦有**一部分**镜头缺席就照旧
	// 打「✔ 质检通过:N 镜全部合格」并写一份干净的 qc_report.json —— 一集 20 镜里
	// 挂了 5 镜,用户看到的是"这一集没问题",只有全流程跑到合成阶段才会以
	// 「镜头 N 尚未渲染完成」炸掉。这是典型"报成功其实没做"。
	var notChecked []int
	vlmFlagged := 0 // VLM 审片未通过的镜头数(只标记,不自动重渲)

	// 本集画面文字检测的账(技能侧清单 §3.1a):textExamined = **真的做过检测**的镜数
	// (分母),textHitShots = 命中的镜号。分母只算真看过的镜,见 qc.TextHitAlert 的判据说明。
	textExamined := 0
	var textHitShots []int
	// 三态开关:未表态 = 开(老项目 json 里没有这个键,不能被读成"显式关闭")
	textDetectOn := p.TextDetectOn()

	// VLM 智能审片 + 画面文字检测:共用同一台视觉模型(设置页 VLM)。
	var vlmClient *llm.Client
	if p.EnableVLM || textDetectOn {
		g := c.cfg.Get()
		if g.VLM.BaseURL != "" && g.VLM.APIKey != "" && g.VLM.Model != "" {
			vlmClient = llm.New(g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model, 120)
		} else {
			if p.EnableVLM {
				c.emit(proj.ID, PhaseQC, 0, "warn", 0, "VLM 审片已开启但视觉模型未配置(设置页填写),跳过智能判分")
			}
			if textDetectOn {
				c.emit(proj.ID, PhaseQC, 0, "warn", 0,
					"画面文字检测已开启但视觉模型未配置(设置页填写):本集未做该检测,报告里每镜如实标注「未配置视觉模型」")
			}
		}
	}

	for _, shot := range plan.Shots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		key := strconv.Itoa(shot.ID)
		m, ok := manifest[key]
		if !ok || m.Status != project.ShotDone || m.File == "" {
			notChecked = append(notChecked, shot.ID)
			continue
		}
		full := filepath.Join(c.store.Dir(proj.ID), m.File)
		if _, err := os.Stat(full); err != nil {
			notChecked = append(notChecked, shot.ID)
			continue
		}
		checked++
		r := qc.Check(ctx, c.ff, shot.ID, full, qc.Params{
			DarkRatioFail: p.DarkRatioFail, DarkRatioWarn: p.DarkRatioWarn,
			MinDuration: p.MinDuration, MaxDuration: p.MaxDuration,
			// 台词应有口播时长:与渲染时长同源(镜头时长已按台词做过预算),这里
			// 用同一套语速常量核对成片里到底说没说够 —— 词与曲同源才比得准。
			ExpectedSpeechSec: compose.ShotSpeechBudget(shot),
			// 与**本镜计划**对账(审计 M29/M28):全局 3–18s 拦不住 3.0–3.9s 的残片
			// (计划下限是 4s),画幅/帧率此前只被"记录"、从不与规格核对 ——
			// 合成对画幅差异是 scale+pad(补黑边)、对帧率是强制 fps=24,全是静默兜底。
			ExpectedDuration: float64(shot.Duration),
			ExpectWidth:      proj.Config.Params.Render.Width,
			ExpectHeight:     proj.Config.Params.Render.Height,
			ExpectFPS:        24, // 渲染帧数口径即 shot.Duration*24,见 stages_render.go 的 shotFrames
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
		// 画面文字检测(清单 §3.1a):必须在**镜级原始产物**上做 —— 合成会按 BurnSubtitles
		// 把台词字幕烧进成片,那时"画面有字"是设计的一部分,检测就失去意义了。
		// 处置由纯函数决定(开关 / 视觉模型 / 机械质检),便于穷举测试。
		if pl := planTextDetect(textDetectOn, vlmClient != nil, r.OK); !pl.Run {
			// 开关关闭时 pl.Skipped 为空 → **连条目都不写**(关掉的功能不该在报告里留痕,
			// 否则"关掉"看起来就像"每镜都验过且干净");未配置模型 / 机械质检未通过则
			// 必须留痕 —— 那是"没验过",不是"验过且干净"。
			if pl.Skipped != "" {
				r.TextDetect = qc.TextDetectSkipped(pl.Skipped)
			}
		} else {
			tr, terr := qc.DetectOnScreenText(ctx, vlmClient, c.ff, full, r.Duration, qc.TextDetectOptions{})
			if terr != nil {
				// 失败如实记,不当作通过(抽不出帧/模型没回答都只是"没验过")。
				r.TextDetect = qc.TextDetectFailed(terr)
				c.emit(proj.ID, PhaseQC, shot.ID, "warn", 0,
					"镜头 %d 画面文字检测失败(未判定,不当作通过):%v", shot.ID, terr)
			} else {
				r.TextDetect = tr
				textExamined++
				if tr.HasText {
					textHitShots = append(textHitShots, shot.ID)
					r.Warnings = append(r.Warnings, qc.TextWarnText(tr))
				}
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
	if len(notChecked) > 0 {
		// 缺镜必须说出来:报告里不塞合成条目(前端把 qc_report.json 的每个键都当成
		// 一份镜头报告来渲染,见 ProjectView/PipelineBar),而是走事件 + 阶段结论。
		c.emit(proj.ID, PhaseQC, 0, "warn", 0,
			"⚠ 有 %d 个镜头没有可质检的产物,未纳入本次质检:%s",
			len(notChecked), shotIDList(notChecked))
	}
	// 集级判据:命中镜数占**实际检测过**镜数的比例(判据与理由见 qc.TextHitAlert)。
	// 超阈值时这里先显式告警一次,再并入阶段结论 —— 结论不许再说"全部合格"。
	textAlert := qc.TextHitAlert(textExamined, textHitShots)
	if textAlert != nil {
		c.emit(proj.ID, PhaseQC, 0, "warn", 100, "⚠ %s", textAlert.Msg)
	}
	v := qcVerdictOf(len(plan.Shots), checked, failed, notChecked, vlmFlagged, textAlert)
	if v.Msg != "" {
		c.emit(proj.ID, PhaseQC, 0, v.Level, 100, "%s", v.Msg)
	}
	return v.Err
}

// qcVerdict 质检阶段的结论(纯函数,便于穷举)。
type qcVerdict struct {
	Level string // success / warn / error
	Msg   string
	Err   error // 非 nil = 阶段失败(Level=error)
}

// qcVerdictOf 由几个计数折出结论:total 方案总镜数、checked 实际检查过、failed 不合格、
// notChecked 根本没产物因而没被看、vlmFlagged VLM 未通过、textAlert 画面文字检测的集级告警
// (nil = 未超阈值;判据见 qc.TextHitAlert)。
//
// 为什么值得抽成纯函数:旧实现的判据只有 checked==0 与 failed>0 —— **缺镜既不影响结论
// 也不进文案**,于是一集 20 镜里挂了 5 镜也能得到「✔ 质检通过:15 镜全部合格」和一份
// 干净的 qc_report.json;"全部合格"这句话本身就是在骗人(剩下 5 镜根本没被看过)。
// 现在缺镜一律判不合格(文案带上镜号),只有"确实检查过的镜全部合格且没有缺镜"才是 success。
//
// textAlert 同样是为了堵这一类的洞:画面文字检测命中率超阈值 = 疑似「字幕回归」,
// 那种情况下**不许**再打「✔ 质检通过:N/N 镜全部合格」(清单 §3.1a 要防的就是
// "以为已经解决了"),而要转成点名命中镜号的 warn。它**不升级为 error**:
// 这一步是监控,单镜误报本来就允许(场景招牌),且 error 会让批产线整集停住、
// 逼人逐镜复核 —— 重渲与否应当由人看着命中镜号来定(与 VLM 审片同一处置原则)。
func qcVerdictOf(total, checked, failed int, notChecked []int, vlmFlagged int, textAlert *qc.TextAlert) qcVerdict {
	missing := len(notChecked)
	switch {
	case checked == 0:
		return qcVerdict{Level: "error", Err: fmt.Errorf("没有已完成的镜头可质检:请先运行「渲染出片」")}
	case failed > 0:
		if missing > 0 {
			msg := fmt.Sprintf("质检发现 %d 个不合格镜头、另有 %d 个镜头尚无产物(未质检;报告见 output/qc_report.json),请在故事板中补齐/单镜重渲", failed, missing)
			return qcVerdict{Level: "error", Msg: msg, Err: fmt.Errorf("%s", msg)}
		}
		return qcVerdict{Level: "error",
			Err: fmt.Errorf("质检发现 %d 个不合格镜头(报告见 output/qc_report.json),请在故事板中单镜重渲", failed)}
	case missing > 0:
		msg := fmt.Sprintf("质检只覆盖 %d/%d 镜:%d 镜尚无可用产物(未质检:%s)请先「渲染出片」补齐后再质检/合成",
			checked, total, missing, shotIDList(notChecked))
		return qcVerdict{Level: "error", Msg: msg, Err: fmt.Errorf("%s", msg)}
	case vlmFlagged > 0 || textAlert != nil:
		// 只标记、不自动重渲:同一 prompt 同一 seed 重跑大概率复现同样结果,
		// 真正的返工要改提示词或换 seed,由用户看过标记后自行决定(2026-09-11 定调)。
		var parts []string
		if vlmFlagged > 0 {
			parts = append(parts, fmt.Sprintf("%d 镜 VLM 审片未通过", vlmFlagged))
		}
		if textAlert != nil {
			parts = append(parts, textAlert.Msg)
		}
		return qcVerdict{Level: "warn", Msg: fmt.Sprintf(
			"⚠ 质检完成:%d 镜机械指标合格,但%s(详情见 output/qc_report.json 或阶段详情),未自动重渲",
			checked, strings.Join(parts, ";"))}
	default:
		return qcVerdict{Level: "success", Msg: fmt.Sprintf("✔ 质检通过:%d/%d 镜全部合格", checked, total)}
	}
}

// textDetectPlan 本镜的画面文字检测该怎么走(纯函数,便于穷举)。
type textDetectPlan struct {
	Run     bool   // 真的去抽帧 + 调视觉模型
	Skipped string // 不跑的原因(写进报告;空串 = 连条目都不写)
}

// 不跑画面文字检测时的三种原因。
const (
	// textSkipDisabled 开关关闭:连报告条目都不写 —— 关掉的功能不该在报告里留痕,
	// 否则"关掉它"看起来就像"每一镜都验过且干净"。
	textSkipDisabled = ""
	// textSkipNoClient 视觉模型未配置:如实降级(报告里写"未做画面文字检测:未配置视觉模型"),
	// 不假装通过。
	textSkipNoClient = "未做画面文字检测:未配置视觉模型"
	// textSkipMechanical 机械质检已判不合格(黑屏/残片)的镜头不送检出:那画面本来就要
	// 重渲,再花一次视觉调用没有新信息。它也不进集级命中率的分母(见 qc.TextHitAlert)。
	textSkipMechanical = "未做画面文字检测:机械质检未通过(该镜本就要重渲)"
)

// planTextDetect 由(开关、视觉模型可用性、机械质检结论)决定本镜怎么处置。
//
// 抽成纯函数是为了让"开关真的能关""没配模型不假装验过"这两条可被穷举验证 ——
// 这类缺陷(功能开着却静默跳过、或报告里看不出到底看没看)不会报错,只有针对性断言能钉住。
func planTextDetect(enabled, haveClient, mechOK bool) textDetectPlan {
	switch {
	case !enabled:
		return textDetectPlan{Run: false, Skipped: textSkipDisabled}
	case !haveClient:
		return textDetectPlan{Run: false, Skipped: textSkipNoClient}
	case !mechOK:
		return textDetectPlan{Run: false, Skipped: textSkipMechanical}
	default:
		return textDetectPlan{Run: true}
	}
}

// shotIDList 把镜号缩成一行(缺镜提示用):「3、7、12」;过长时折叠成「…等 N 个」。
func shotIDList(ids []int) string {
	const maxShown = 12
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		if i >= maxShown {
			break
		}
		parts = append(parts, strconv.Itoa(id))
	}
	out := strings.Join(parts, "、")
	if len(ids) > maxShown {
		out += fmt.Sprintf(" …等 %d 个", len(ids))
	}
	return out
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

// gradeAutoNote 自动降档时的**播报文案**(空串 = 不是自动降档,不播报)。
//
// 与接缝的 seamBlockNote 同一取向:判据与文案同源(都来自 project.EffectiveGrade 的
// 第二个返回值),且降档必须**可见** —— 否则用户只会看到"这版调色怎么变淡了",既不知道
// 是平台按风格自动降的,也不知道去哪儿改回来。显式选了档位的项目不许被这条播报打扰
// (那会让人以为自己设的档被改掉了)。
func gradeAutoNote(grade string, autoDowngraded bool) string {
	if !autoDowngraded {
		return ""
	}
	return fmt.Sprintf("本片风格为动画电影,调色档默认用 %s;可在项目参数里改", grade)
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
	// 调色档(甲1 / 技能侧清单 §2.1):**最终档位只在这里解析一次**(project.EffectiveGrade),
	// compose 只认结果 —— 判据只留一处,免得"参数默认"和"装载后兜底"两处各判一次而漂移。
	// 未表态时按 plan.Style 选档:动画电影类风格串用 soft(film 的压饱和 + 暗角 + 颗粒
	// 叠在动画赛璐璐上偏重),其余用 film;自动降档时如实播报一次并给出改回去的办法。
	grade, gradeAuto := p.EffectiveGrade(plan.Style)
	if note := gradeAutoNote(grade, gradeAuto); note != "" {
		c.emit(proj.ID, PhaseAssemble, 0, "info", 2, "%s", note)
	}
	res, err := compose.Compose(ctx, c.ff, clips, plan, outDir, compose.Params{
		Width: proj.Config.Params.Render.Width, Height: proj.Config.Params.Render.Height,
		// 交付分辨率与渲染尺寸解耦(红果对标 G1):渲染尺寸受 H3 的短边 768 上限约束,
		// 而交付是 1080×1920。0/0 = 按画幅自动(compose.deliverSize 是唯一判据点)。
		DeliverWidth: p.DeliverWidth, DeliverHeight: p.DeliverHeight,
		Transition: p.Transition, BurnSubtitles: p.BurnSubtitles,
		SubtitleSize: p.SubtitleSize, Loudness: p.Loudness, FadeSec: fade,
		// 字幕下边距(避开平台底部 UI 安全区):吃的是**交付高度**,
		// 因为字幕是在交付缩放之后烧的。
		SubtitleMarginV: p.SubtitleMarginV,
		// 峰值归一:同一个"假开关"的另一半 —— 参数一直存在,而真正生效的
		// loudnorm TP 写死 -1.5(审计 P1-1)。
		Peak:    p.Peak,
		Denoise: p.DenoiseOn(),
		// 调色档(甲1):不接这一行的后果与 P1-1 那批假开关完全一样 ——
		// assemble.grade 有界面、能保存、Sanitize 也在归一,而成片里毫无变化。
		// 这里传的是上面解析过的**最终**档位(不是未表态的 p.Grade)。
		Grade: grade,
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
	// 成片信息报**实测值**:交付分辨率与渲染尺寸解耦之后,"成片多大"不再是配置里那个数
	// (可能被放大到交付档、可能按画幅自动取标准档)—— 报实测值,用户不必去追配置推断。
	if res.Width > 0 && res.Height > 0 {
		c.emit(proj.ID, PhaseAssemble, 0, "success", 100, "✔ 成片就绪:%s(%.0f 秒 · %d×%d)", filepath.Base(res.FinalPath), res.Duration, res.Width, res.Height)
	} else {
		c.emit(proj.ID, PhaseAssemble, 0, "success", 100, "✔ 成片就绪:%s(%.0f 秒)", filepath.Base(res.FinalPath), res.Duration)
	}

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
