package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/compose"
	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/fsutil"
	"github.com/YiIimini/ManJuX/internal/library"
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
	landmarkFlagged := 0 // 空间锚点对不上的镜数（官方 STEP 9 第 2 条）
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
		// **空间锚点核查**（官方 qc-checklist STEP 9 第 2 条：终检要核对"地标是否落在表上写的位置"；
		// 同一条在 fallback-policy 里对应"Fix spatial anchor drift across clips"）。
		//
		// 平台此前只在**输入侧**要求场景卡写 landmarks（契约门管"写了没有"），成片侧从不核对 ——
		// 于是"同一场景里门槛在东、下一镜跑到西边"这类空间漂移一路无声进成片。
		//
		// 只查**每个场景的建立镜**：地标是在那一镜定下来的（后续镜头多半只拍局部、看不到全部地标），
		// 查它既最有效、又把视觉模型预算压到"一集几个场景就几次"。
		if vlmClient != nil && r.OK {
			if sc, ok := sceneByID(plan, shot.Scene); ok && strings.TrimSpace(sc.Landmarks) != "" &&
				shotEstablishesScene(plan, shot) {
				at := r.Duration / 2
				if at < 0.4 {
					at = 0.4
				}
				tmp := filepath.Join(c.store.Dir(proj.ID), "qc", "frames")
				if frame, ferr := qc.SampleFrameAt(ctx, c.ff, full, at, tmp, fmt.Sprintf("landmark-%d.jpg", shot.ID)); ferr != nil {
					c.emit(proj.ID, PhaseQC, shot.ID, "warn", 0,
						"镜头 %d 空间锚点未核查(抽帧失败,不当作通过):%v", shot.ID, ferr)
				} else if v, verr := qc.DetectLandmarks(ctx, vlmClient, frame, sc.Landmarks); verr != nil {
					c.emit(proj.ID, PhaseQC, shot.ID, "warn", 0,
						"镜头 %d 空间锚点未核查(不当作通过):%v", shot.ID, verr)
				} else if v != nil && len(v.Missing) > 0 {
					landmarkFlagged++
					c.emit(proj.ID, PhaseQC, shot.ID, "warn", checked*100/max(1, len(plan.Shots)),
						"%s", qc.LandmarkWarnText(shot.ID, sc.Name, sc.Landmarks, v))
				}
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
	// **人脸一致性测量**（2026-09-28）：把主角的定妆照与它在几镜里的画面比一遍，把数字摆出来。
	//
	// 第一版**只测量、不拦人**：阈值 0.85 来自开源参考实现，但在本平台画风上还没做跨镜标定
	// （实测只标定了两头：同一张脸 1.000、同一画面里不同的人 0.329）。判据不成熟就拦人只会
	// 制造假告警。攒够真实数据再定阈值 —— 见 qc/face.go 的说明。
	//
	// 预算：最多 3 个角色 × 每人最多 3 帧，一次 Python 调用（模型加载约 5 秒），不随镜头数线性膨胀。
	c.measureFaceConsistency(ctx, proj, plan, manifest)

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
	// 按镜头顺序收集产物。
	//
	// **失败镜的黑场占位也收进来**（2026-09-28 修自己上轮留下的半成品）：渲染阶段在阶梯用尽后
	// 会写一个黑场静音占位并标记 `placeholder: missing clip`（官方 fallback 第 4 级：
	// "Skip the shot and add a `placeholder: missing clip` note for downstream review"）——
	// 它的**全部意义**就是"保住时间轴、让缺口看得见、成片照出"。但这里此前死认 ShotDone，
	// 于是一个镜头失败就**整片合不出来**，那段占位代码等于白写：用户既拿不到成片，也看不到缺口。
	//
	// 现在的判据：ShotDone ⇒ 正常片段；ShotFailed 且带占位标记 ⇒ 也收（下面会**大声播报**有几个）。
	// 其余情况（没渲、产物丢了）仍然拒绝合成 —— 那不是"带瑕疵出片"，那是"还没做"。
	var clips []string
	placeholders := 0
	for _, shot := range plan.Shots {
		key := strconv.Itoa(shot.ID)
		m, ok := manifest[key]
		if !ok || m.File == "" || (m.Status != project.ShotDone && !isPlaceholderClip(m)) {
			return fmt.Errorf("镜头 %d 尚未渲染完成:请先运行「渲染出片」", shot.ID)
		}
		full := filepath.Join(c.store.Dir(proj.ID), m.File)
		if _, err := os.Stat(full); err != nil {
			return fmt.Errorf("镜头 %d 产物缺失(%s):请重渲", shot.ID, m.File)
		}
		if isPlaceholderClip(m) {
			placeholders++
		}
		clips = append(clips, full)
	}
	if placeholders > 0 {
		c.emit(proj.ID, PhaseAssemble, 0, "warn", 1,
			"⚠ 成片里有 **%d 个占位片段**（黑场静音）—— 这些镜头渲染失败、已按官方降级阶梯记为 "+
				"`%s`。占位保住了时长与对位，但成片里那几段是没有画面的；"+
				"重渲这些镜头后**必须重合成**（清单里查得到是哪几镜）",
			placeholders, project.PlaceholderMarker)
	}
	// **合成前的新旧资产混用核对**（官方 qc-checklist 重生成纪律：用户重做产物后下游必须全用
	// 最新版本，且"不得在最终拼接里静默混用新旧资产"）。
	//
	// 真实事故长这样：用户把某角色定妆照重生成（换了脸），只重渲了前 5 镜，剩下 20 镜还是旧脸
	// 渲的 —— 合成照跑、阶段照报成功，成片里同一个人前后两张脸而无人过问。
	// 这里用**当前方案**重算每镜的资产指纹并与渲染时记下的比对：对不上就是旧资产渲的。
	// 只播报、不拦人：官方纪律是"不许**静默**混用"，让人看见并决定重渲即可。
	// 【2026-09-28 审计 A13】`assetMap, _ :=` 的后果不是"少一次核对"，而是**把病因叙述错**：
	// 读不到资产表时核对退化成"全部 unknown"，界面于是告诉用户"这些片段是老清单渲的"——
	// 用户照着去重渲，而真毛病（asset_map.json 坏了）一个字没提，重渲完还是同样报。
	// 现在把两种情形分开说：**资产表读不出**与**真的是旧资产**是两回事。
	assetMap, aerr := c.store.LoadAssets(proj.ID)
	if aerr != nil {
		c.emit(proj.ID, PhaseAssemble, 0, "warn", 1,
			"⚠ **读不出资产表**(%v)——本次「是否混用新旧资产」的核对无法进行（这不是「这些是老清单渲的」，"+
				"是核对本身跑不了）。请先修 asset_map.json，再重跑合成", aerr)
	}
	stale, unknown := StaleAssetClips(plan, manifest, assetMap)
	if len(stale) > 0 {
		c.emit(proj.ID, PhaseAssemble, 0, "warn", 1,
			"⚠ 这 %d 个片段是**旧资产**渲的（%s）：角色定妆照/场景板/道具卡在它们渲完之后被改过 —— "+
				"成片里会出现同一个人前后不一致。官方纪律：不得在新旧资产混用的状态下合成成片。"+
				"建议对这些镜头点「重渲」后再合成（本次仍按现有片段合成，缺口在成片里可见）",
			len(stale), joinShotIDs(stale, 8))
	} else if len(unknown) > 0 {
		c.emit(proj.ID, PhaseAssemble, 0, "info", 1,
			"提示：%d 个片段没有资产批次记录（是加这一列之前渲的），本次无法核对新旧 —— 重渲一次即可纳入核对",
			len(unknown))
	}
	p := proj.Config.Params.Assemble
	// 配乐三级解析（显式文件 ＞ 资产库同需求条目 ＞ 无），并把结果**如实播报**：
	// "用了哪来的配乐"必须让人看见 —— 配乐是最容易被静默忽略的一环（有成片 vs 没配乐，
	// 听感差别巨大，而阶段日志此前不说这件事）。
	bgm, bgmNote := c.bgmPathOf(proj, plan)
	if bgmNote != "" {
		lvl := "info"
		if bgm == "" {
			lvl = "warn"
		}
		c.emit(proj.ID, PhaseAssemble, 0, lvl, 2, "♪ 配乐:%s", bgmNote)
	}
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
		// 单条 BGM（项目参数 audio.bgm）：官方 qc-checklist STEP 8 要求成片一条连续配乐，
		// 而不是每镜各带一段（平台此前恰是后者，30+ 片段拼起来每刀都在换调）。
		BGM:          bgm,
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

// bgmPathOf 取该集的单条配乐路径（项目参数 audio.bgm；相对路径按项目目录解析）。
//
// 空 = 不铺配乐（compose 会如实播报"成片未铺 BGM"，见 compose.BGMNote）。
// 相对路径按**项目目录**解析：用户把音乐文件丢进那一集的目录即可，不必写绝对路径。
// bgmPathOf 解析这一集的配乐文件，按**三级优先级**：
//
//	① 显式文件 `assemble.bgm`（绝对路径，或相对**项目目录**的相对路径）—— 人指定的一律优先；
//	② 资产库里**同需求**的配乐条目（`assemble.bgm_description` 归一后当条目名）——
//	   这是 2026-09-28 用户要求的复用链：生成的配乐进库按需求管理，同样的需求直接拿现成的；
//	③ 都没有 ⇒ 空（compose 会如实播报"成片未铺 BGM"，不假装有）。
//
// 【为什么复用放在②而不是"每次都重新生成"】配乐是**一次产出、反复可用**的东西：同一部戏的
// 多集、同一系列的多个项目，需求描述往往一模一样。每次都重新生成既是浪费，又会让"同一部戏
// 各集配乐不一样"—— 那恰恰是成片听感散掉的原因之一。
//
// 命中的来源会**如实播报**（复用 / 指定文件 / 无），不静默。
func (c *Coordinator) bgmPathOf(proj *project.Project, plan *project.Plan) (string, string) {
	p := proj.Config.Params.Assemble
	if raw := strings.TrimSpace(p.BGM); raw != "" {
		if filepath.IsAbs(raw) {
			return raw, "项目参数指定的配乐文件"
		}
		// 相对路径按**项目目录**解析；拿不到项目目录时（裸造 Coordinator）如实说明，
		// 不 panic、也不假装能解析 —— 与 emit 的 nil 宽容同一考虑。
		if dir := c.projectDir(proj.ID); dir != "" {
			return filepath.Join(dir, raw), "项目参数指定的配乐文件"
		}
		return "", "配乐写的是相对路径(" + raw + ")，但拿不到项目目录，无法解析 —— 请改填绝对路径"
	}
	brief := strings.TrimSpace(p.BGMDescription)
	if brief == "" || c.lib == nil {
		return "", ""
	}
	name := library.MusicEntryName(brief)
	if name == "" {
		return "", ""
	}
	if e, img, ok := c.lib.Match("music", name); ok && e != nil {
		c.lib.BumpUsed("music", name)
		return img, fmt.Sprintf("资产库复用配乐「%s」（同需求免重生成）", name)
	}
	// ③ 库里没有 ⇒ **现场生成**（本地 ACE-Step），生成成功就**入库**供下次复用。
	//
	// 这一步是用户 2026-09-28 那条要求的闭环："生成的背景音乐也要做成资产库里进行管理，
	// 后续存在相同的可以直接复用"。生成器可注入（musicGenHook），便于在没有模型的机器上
	// 测"生成→入库→复用"这条链本身。
	path, gnote := c.generateMusic(proj, plan, name, brief)
	if path != "" {
		return path, gnote
	}
	return "", fmt.Sprintf("资产库没有这份配乐需求「%s」，且%s", name, gnote)
}

// generateMusic 现场生成配乐并入库，返回(项目内绝对路径, 说明)。失败时返回("", 原因)。
//
// 时长取"成片的 1/3 且不超过 180 秒"：铺底会自动循环（compose/bgm.go），
// 生成 3 分钟的曲子去铺 6 分钟的片子完全够用，而生成时间与时长近似成正比 —— 没必要等更久。
func (c *Coordinator) generateMusic(proj *project.Project, plan *project.Plan, name, brief string) (string, string) {
	total := 0
	if plan != nil {
		for _, sh := range plan.Shots {
			total += sh.Duration
		}
	}
	seconds := float64(total) / 3
	if seconds < 60 {
		seconds = 60
	}
	if seconds > 180 {
		seconds = 180
	}
	// tags 里必须钉住"纯器乐"：H3 片段自带同期声与台词，配乐带唱词会打架（与官方 STEP 8 同一口径）
	tags := strings.TrimSpace(brief) + ", instrumental only, no vocals, background score"
	ctx := context.Background()
	gen := c.musicGen
	if gen == nil {
		if c.reg == nil {
			// 裸造 Coordinator（测试/离线工具）没有引擎注册表：如实说"没有能力"，不 panic。
			return "", "没有可用的本地引擎（配乐生成需要 ComfyUI）"
		}
		eng, err := c.reg.Get("comfy")
		if err != nil || eng == nil {
			return "", "本机没有可用的本地引擎（配乐生成需要 ComfyUI）"
		}
		if !engine.CapsOf(eng.Name()).Music {
			return "", fmt.Sprintf("当前引擎(%s)不支持配乐生成", eng.Name())
		}
		gen = func(ctx context.Context, tags string, sec float64) (string, error) {
			outDir := filepath.Join(c.store.Dir(proj.ID), "assets", "music")
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return "", err
			}
			return eng.GenerateMusic(ctx, engine.MusicJob{
				Tags: tags, Seconds: sec,
				OutputDir: outDir, OutputName: "bgm_" + director.SanitizeForCache(name),
			})
		}
	}
	c.emit(proj.ID, PhaseAssemble, 0, "info", 2,
		"♪ 资产库没有这份配乐，开始生成（%.0f 秒，纯器乐）—— 生成后自动入库，下次同需求直接复用", seconds)
	path, err := gen(ctx, tags, seconds)
	if err != nil || strings.TrimSpace(path) == "" {
		why := "生成失败"
		if err != nil {
			why = err.Error()
		}
		return "", "现场生成也不成：" + why
	}
	// **响度归一**（2026-09-28 实测驱动，别删）：ACE-Step 直出的曲子**极轻** ——
	// 实测 60 秒的产物 mean_volume -63 dB / max_volume -53 dB（正常混音在 -30～-10），
	// 也就是"文件是好的、听起来等于没有"。铺到成片里会被母轨彻底压住，等于没配乐。
	// 这里按与母轨**同一套** loudnorm 口径拉齐（I=母轨目标、TP=-1.5、LRA=11），
	// 并**测量前后响度**：拉不上来就如实报错，不把一个听不见的文件塞进成片。
	if c.ff != nil {
		before, after, nerr := normalizeBGM(ctx, c.ff, path)
		if nerr != nil {
			c.emit(proj.ID, PhaseAssemble, 0, "warn", 2,
				"⚠ 配乐响度归一失败（该曲子可能极轻，铺进成片会像没有配乐）:%v", nerr)
		} else {
			c.emit(proj.ID, PhaseAssemble, 0, "info", 2,
				"♪ 配乐响度已归一：%.1f dB → %.1f dB（ACE-Step 直出极轻，不归一等于没配乐）", before, after)
			if after < -30 {
				c.emit(proj.ID, PhaseAssemble, 0, "warn", 2,
					"⚠ 配乐归一后仍然很轻（%.1f dB），铺进成片可能听不出来 —— 建议换一个配乐需求描述重生成", after)
			}
		}
	}

	// 入库（按需求命名，来源记成 ace-step，便于日后追它是谁生成的）
	if c.lib != nil {
		if _, perr := c.lib.Put("music", name, brief, tags, path, proj.ID, "ace-step"); perr != nil {
			c.emit(proj.ID, PhaseAssemble, 0, "warn", 2, "⚠ 配乐已生成但入库失败（下次还得重新生成）:%v", perr)
		}
	}
	return path, fmt.Sprintf("已生成配乐「%s」并入库（下次同需求直接复用）", name)
}

// shotEstablishesScene 这一镜是不是它所在场景的**建立镜**（该场景第一次出现的镜）。
//
// 空间锚点核查只在建立镜上做：地标是在那一镜定下来的，后续镜头多半只拍局部、根本看不到全部地标；
// 查建立镜既最有效，又把视觉模型调用压到"一集几个场景就几次"。
func shotEstablishesScene(plan *project.Plan, shot project.Shot) bool {
	if plan == nil {
		return false
	}
	for _, sh := range plan.Shots {
		if sh.ID >= shot.ID {
			break
		}
		if strings.EqualFold(strings.TrimSpace(sh.Scene), strings.TrimSpace(shot.Scene)) {
			return false
		}
	}
	return true
}

// joinShotIDs 把镜号列表拼成 "#3、#7、…"（最多显示 n 个，其余给计数）。
func joinShotIDs(ids []int, n int) string {
	parts := make([]string, 0, n+1)
	for i, id := range ids {
		if i >= n {
			parts = append(parts, fmt.Sprintf("…等 %d 处", len(ids)))
			break
		}
		parts = append(parts, "#"+strconv.Itoa(id))
	}
	return strings.Join(parts, "、")
}

// measureFaceConsistency 人脸一致性测量（只测量、只播报，不阻断）。
//
// 为什么值得单独做一条：平台此前判断"是不是同一个人"只有 VLM 判定（有随机性）与"撞脸自检"
// （比的是资产卡之间，不是成片镜头）—— 缺一个**确定性**的数字。这条给出"定妆照 vs 各镜画面"
// 的余弦相似度，把"这一镜是不是换了张脸"从感觉变成可比较的量。
//
// 【预算与取样】最多 3 个角色（按出场镜头数排序）× 每人最多 3 镜（均匀取样），一次 Python 调用。
// 为什么要限：模型加载约 5 秒，逐镜调用会让质检阶段慢到没人愿意开；均匀取样已经能看出漂移。
//
// 【没验过就说没验过】环境不齐（没 Python / 没装 buffalo_l / 抽帧失败）一律如实播报，
// 不沉默（沉默会被读成通过）。
func (c *Coordinator) measureFaceConsistency(ctx context.Context, proj *project.Project, plan *project.Plan, manifest map[string]project.ClipManifest) {
	if plan == nil || len(plan.Shots) == 0 {
		return
	}
	assets, err := c.store.LoadAssets(proj.ID)
	if err != nil {
		return
	}
	cfg := qc.FaceConfigFromComfyRoot(c.cfg.Get().Comfy.RootDir)
	if ok, why := cfg.Ready(); !ok {
		c.emit(proj.ID, PhaseQC, 0, "info", 100, "人脸一致性未验（%s）—— 不当作通过", why)
		return
	}
	// 统计出场镜头：角色 → 镜号列表（按出场次数取前 3 个角色）
	byChar := map[string][]project.Shot{}
	order := []string{}
	for _, sh := range plan.Shots {
		for _, cid := range sh.Characters {
			if _, seen := byChar[cid]; !seen {
				order = append(order, cid)
			}
			byChar[cid] = append(byChar[cid], sh)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return len(byChar[order[i]]) > len(byChar[order[j]]) })
	if len(order) > 3 {
		order = order[:3]
	}
	frameDir := filepath.Join(c.store.Dir(proj.ID), "qc", "frames")
	for _, cid := range order {
		shots := byChar[cid]
		a, ok := assets[cid]
		if !ok || strings.TrimSpace(a.File) == "" {
			continue
		}
		ref := filepath.Join(c.store.Dir(proj.ID), a.File)
		if _, err := os.Stat(ref); err != nil {
			continue
		}
		// 均匀取样最多 3 镜
		picks := pickEvenly(len(shots), 3)
		var frames []string
		for _, i := range picks {
			sh := shots[i]
			m, ok := manifest[itoa(sh.ID)]
			if !ok || m.File == "" {
				continue
			}
			clip := filepath.Join(c.store.Dir(proj.ID), m.File)
			at := float64(sh.Duration) / 2
			if at < 0.4 {
				at = 0.4
			}
			fp, ferr := qc.SampleFrameAt(ctx, c.ff, clip, at, frameDir,
				fmt.Sprintf("face-%s-%d.jpg", strings.TrimPrefix(cid, "char_"), sh.ID))
			if ferr != nil {
				continue
			}
			frames = append(frames, fp)
		}
		if len(frames) == 0 {
			continue
		}
		rep := qc.MeasureFaceConsistency(ctx, cfg, ref, frames)
		if msg := qc.FaceSummary(rep, a.Name); msg != "" {
			c.emit(proj.ID, PhaseQC, 0, "info", 100, "%s", msg)
		}
	}
}

// pickEvenly 从 n 个里均匀取最多 k 个下标（n<=k 时全取）。
func pickEvenly(n, k int) []int {
	if n <= 0 {
		return nil
	}
	if n <= k {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	out := make([]int, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, i*(n-1)/(k-1))
	}
	return out
}

// isPlaceholderClip 这一镜的产物是不是**失败镜的占位片段**。
//
// 判据用清单里的标记（渲染阶段写入 project.PlaceholderMarker），而不是只看状态码：
// 只有"确实写了占位"的失败镜才允许进合成 —— 别的失败（没渲、产物丢了）仍然拒绝，
// 那不是"带瑕疵出片"，那是"还没做"。
func isPlaceholderClip(m project.ClipManifest) bool {
	return m.Status == project.ShotFailed && strings.Contains(m.Error, project.PlaceholderMarker)
}

// projectDir 项目目录（store 为空时返回空串）。
//
// 为什么要这层：Coordinator 在很多地方是"裸造"的（测试、离线工具），没有 store；
// 直接 c.store.Dir(...) 会整进程崩，而崩的位置离原因很远。事件与路径解析都属于
// "拿不到就降级"，不该让业务挂掉。
func (c *Coordinator) projectDir(projectID string) string {
	if c.store == nil {
		return ""
	}
	return c.store.Dir(projectID)
}
