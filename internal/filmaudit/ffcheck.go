package filmaudit

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ffcheck.go 成片体检里的**广播级标准项**：静音 / 冻帧 / 响度（EBU R128）/ 硬切点。
//
// 【为什么要有它（2026-10-09 用户「问题好多啊」那一轮之后）】
// audit.go 判的是「方案与成片对不对得上」（字幕落点、说话人、接缝、契约），
// 而**成片自身的技术形态**此前没有任何一处看过：一段既没人声也没有环境声的断音、
// H3 吐出来的冻帧、响度偏离平台目标、该硬切的地方糊成一片……
// 这些 ffmpeg 一条命令就能给出确定答案，却要等用户戴着耳机看完成片才发现。
//
// 【口径来源（照平台既有口径，不许另定一套）】
//   - 响度目标 -16 LUFS / 真峰值上限：见 internal/compose 的两遍 loudnorm（`I=-16`；真峰值 `TP=-1.5`，
//     项目参数 assemble.peak 默认 0.92 线性 ≈ -0.72 dBTP）。
//   - 冻帧是标准 QC 项（`freezedetect`），H3 会输出冻帧。
//   - 静音阈值 2 秒：短于它的停顿是正常语流间隙，超过就是观众听到的「断音」。
//
// 【三态纪律（与体检其余部分同一条）】
// 这一层只做**纯解析 + 判定**：ffmpeg 由调用方跑、**日志原文**塞进 FFAnalysis。
// 某一项日志是空串（或压根没有结论行，比如 ebur128 还没走到 Summary 就断了）
// ⇒ **一个字都不说**，由调用方如实播报「没验过」；
// **绝不许**把「没采到」当成「合格」—— 那是体检最容易骗自己的地方。

// FFAnalysis 一次成片体检里，由调用方用 ffmpeg 采到的**日志原文**（合并 stderr）。
// 空串 = 这一项没采到（调用方会如实播报「没验过」），判据一律**跳过**而不是猜。
type FFAnalysis struct {
	Silence  string // ffmpeg -af silencedetect=n=-50dB:d=1.5 -f null -
	Freeze   string // ffmpeg -vf freezedetect=n=-60dB:d=1.5 -f null -
	Loudness string // ffmpeg -af ebur128=peak=true -f null -
	CutLog   string // ffmpeg -vf "select='gt(scene,0.3)',showinfo" -f null -
}

// 阈值：连续静音多久算「断音」。
//
// 取 2.0 秒：正常语流里的停顿（换气、动作留白）都在 1 秒级，
// 超过 2 秒观众听到的就是「这一段断了」——既没人说话、也没有环境声，
// 而这两样在 H3 的片段里本来应该总有一样在响。
const silenceWarnSec = 2.0

// 阈值：画面冻结多久算冻帧（标准 QC 项 freezedetect 的口径）。
//
// 取 1.0 秒：H3 偶尔会吐出一段完全不变的帧（引擎侧的失效），
// 短于 1 秒还在「一个动作的停顿」范围内，超过就是观众看得见的卡住。
const freezeWarnSec = 1.0

const (
	// loudnessTargetLUFS 平台响度目标（LUFS，与 compose 的 loudnorm I=-16 同一口径）。
	loudnessTargetLUFS = -16.0
	// loudnessToleranceLU 允许的偏差（LU）。1.5 LU 以内人耳听不出「这一集和别的集不一样」，
	// 超过就是整集偏响/偏轻（同一个账号里会被观众当成「音量忽大忽小」）。
	loudnessToleranceLU = 1.5
	// truePeakWarnDBTP 真峰值上限（dBTP）。超过就是有削顶风险（平台回放/转码时裁掉波峰 = 尖锐失真）。
	truePeakWarnDBTP = -1.0
	// cutMergeSec 相邻这么近的两个 pts_time 当作**同一个硬切点**。
	//
	// 一帧 1/24 ≈ 0.042 秒，但 `select` 在同一个切点上会连着选好几帧（阈值松一点时尤其明显）
	// —— 不并的话，同一个切点会被数成好几处，切点数就永远对不上方案的场次边界。
	cutMergeSec = 0.2
	// cutListMax 文案里最多列几个切点时刻（列不下就只报总数）。
	cutListMax = 12
)

// SilenceRun 一段连续静音（秒）。三段数字都取自日志本身（ffmpeg 给的时间点 + 它自己算的时长）。
//
// 时长无从得知时 Dur 记 0（**不编造**）：日志被截断、或者收尾行没到，那一段就不参与判定。
type SilenceRun struct{ Start, End, Dur float64 }

// FreezeRun 一段画面冻结（秒）。语义同 SilenceRun。
type FreezeRun struct{ Start, End, Dur float64 }

// Loudness 成片响度实测（EBU R128）。
//
// OK 表示**这一项真的采到了**（日志里有 ebur128 的 Summary 块，三个数都解析出来）；
// OK=false 时三个数字都没有意义，调用方必须按「没验过」处理，不许当成「合格」。
type Loudness struct {
	Integrated   float64 // 集成响度（LUFS，ebur128 的 I）
	LRA          float64 // 响度范围（LU）
	TruePeakDBTP float64 // 真峰值（dBTP；ebur128 的 Summary 里写作 dBFS，含义相同）
	OK           bool
}

var (
	silenceStartRe = regexp.MustCompile(`silence_start:\s*(-?[0-9.]+)`)
	// silencedetect 的收尾行形如 `silence_end: 15.272604 | silence_duration: 1.718104`
	// —— 时长就在同一行，优先用它（那是 ffmpeg 自己算的数，用它才谈得上「解出的数字与日志一致」）。
	silenceEndRe = regexp.MustCompile(`silence_end:\s*(-?[0-9.]+)(?:\s*\|\s*silence_duration:\s*(-?[0-9.]+))?`)

	// freezedetect 是三行一组：start / duration / end（分别是三行，与 silencedetect 不同）。
	freezeStartRe = regexp.MustCompile(`freeze_start:\s*(-?[0-9.]+)`)
	freezeDurRe   = regexp.MustCompile(`freeze_duration:\s*(-?[0-9.]+)`)
	freezeEndRe   = regexp.MustCompile(`freeze_end:\s*(-?[0-9.]+)`)

	// ebur128 的 Summary 块（只有这一块里的数才是整集结论，逐帧行的 I/LRA 是**运行值**）。
	ebIntegratedRe = regexp.MustCompile(`I:\s*(-?[0-9.]+)\s+LUFS`)
	ebLRArrRe      = regexp.MustCompile(`LRA:\s*(-?[0-9.]+)\s+LU`)
	ebTruePeakRe   = regexp.MustCompile(`Peak:\s*(-?[0-9.]+)\s+dBFS`)

	// showinfo 打在 stderr 上的行：`... n:   5 pts: 210944 pts_time:17.166667 duration: 512 ...`
	ptsTimeRe = regexp.MustCompile(`pts_time:\s*(-?[0-9.]+)`)
)

// ParseSilence 从 silencedetect 的日志里取出静音段（纯函数，便于用**真机日志原文**当夹具）。
func ParseSilence(log string) []SilenceRun {
	var out []SilenceRun
	var start float64
	on := false
	for _, ln := range strings.Split(log, "\n") {
		if m := silenceStartRe.FindStringSubmatch(ln); m != nil {
			st, err := parseSec(m[1])
			if err != nil {
				continue
			}
			// 同一个起点重复出现（收尾时再打一行）—— 还是那一段，不许数成两段。
			if on && math.Abs(st-start) <= 1e-6 {
				continue
			}
			// 上一段没有收尾行（日志被截断）：时长无从得知 ⇒ 记 Dur=0，不编造。
			if on {
				out = append(out, SilenceRun{Start: start, End: start})
			}
			start, on = st, true
			continue
		}
		if m := silenceEndRe.FindStringSubmatch(ln); m != nil {
			if !on {
				continue // 只有收尾行、没有起点（日志被截了头）—— 拼不出段，丢掉
			}
			en, err := parseSec(m[1])
			if err != nil {
				continue
			}
			dur, derr := parseSec(m[2])
			if derr != nil || dur <= 0 {
				dur = en - start // 没有 duration 字段时用两个时间点推
			}
			if dur > 0 {
				out = append(out, SilenceRun{Start: start, End: start + dur, Dur: dur})
			}
			on = false
		}
	}
	if on {
		out = append(out, SilenceRun{Start: start, End: start})
	}
	return out
}

// ParseFreeze 从 freezedetect 的日志里取出冻结段（三行一组：start / duration / end）。
func ParseFreeze(log string) []FreezeRun {
	var out []FreezeRun
	var start, dur float64
	on := false
	flush := func() {
		if !on {
			return
		}
		if dur <= 0 {
			// 收尾行没到（日志被截断）：时长无从得知 ⇒ 记 Dur=0，不编造、也不据此报警。
			out = append(out, FreezeRun{Start: start, End: start})
		} else {
			out = append(out, FreezeRun{Start: start, End: start + dur, Dur: dur})
		}
		on, dur = false, 0
	}
	for _, ln := range strings.Split(log, "\n") {
		if m := freezeStartRe.FindStringSubmatch(ln); m != nil {
			st, err := parseSec(m[1])
			if err != nil {
				continue
			}
			if on && math.Abs(st-start) <= 1e-6 {
				continue
			}
			flush()
			start, on = st, true
			continue
		}
		if m := freezeDurRe.FindStringSubmatch(ln); m != nil {
			if d, err := parseSec(m[1]); err == nil && d > 0 {
				dur = d
			}
			continue
		}
		if m := freezeEndRe.FindStringSubmatch(ln); m != nil {
			if !on {
				continue
			}
			if dur <= 0 {
				if en, err := parseSec(m[1]); err == nil && en > start {
					dur = en - start
				}
			}
			flush()
		}
	}
	flush()
	return out
}

// ParseLoudness 从 ebur128 的日志里取整集响度。
//
// **只认 Summary 块里的数**：逐帧行里也有 `I:` / `LRA:` 字样，但那是**运行值**
// （最后一行看着和结论差不多，前面却全是过程中间值）——按逐帧行读会把「采到一半就断」的日志当成合格。
// 没有 Summary 块（日志被截断、或者根本不是 ebur128 的输出）⇒ OK=false，判据整项跳过。
func ParseLoudness(log string) Loudness {
	i := strings.LastIndex(log, "Summary:")
	if i < 0 {
		return Loudness{}
	}
	sum := log[i:]
	mI := ebIntegratedRe.FindStringSubmatch(sum)
	mLRA := ebLRArrRe.FindStringSubmatch(sum)
	mTP := ebTruePeakRe.FindStringSubmatch(sum)
	if mI == nil || mLRA == nil || mTP == nil {
		return Loudness{} // 三个数缺一个就不算采到（缺的那个没法判，也没法用别的数顶）
	}
	integrated, e1 := parseSec(mI[1])
	lra, e2 := parseSec(mLRA[1])
	tp, e3 := parseSec(mTP[1])
	if e1 != nil || e2 != nil || e3 != nil {
		return Loudness{}
	}
	return Loudness{Integrated: integrated, LRA: lra, TruePeakDBTP: tp, OK: true}
}

// ParseCutTimes 从 `select='gt(scene,0.3)',showinfo` 的日志里取出**硬切点**时刻（秒）。
//
// 相邻不足 cutMergeSec 的点并成一个（同一个切点上 `select` 会连着选好几帧）；
// 日志按解码顺序打，时间递增，所以只跟「上一个留下的点」比。
func ParseCutTimes(log string) []float64 {
	var out []float64
	for _, m := range ptsTimeRe.FindAllStringSubmatch(log, -1) {
		t, err := parseSec(m[1])
		if err != nil {
			continue
		}
		if n := len(out); n > 0 && t-out[n-1] < cutMergeSec {
			continue
		}
		out = append(out, t)
	}
	return out
}

// cutLogUsable 这份切点日志到底是不是 `select,showinfo` 真的跑过？
//
// 【为什么必须有这一句（与另外三项的区别）】切点这一项报的是一个**数**，而"没采到"时那个数也正好是 0
// —— 不分辨的话，一段垃圾日志（命令写错了、或者调用方塞错了别的滤镜的输出）就会变成一条
// 「实测 0 处硬切」的假结论，等于把"没验过"印成"验过"。
//
// 判据取自真机日志：只要滤镜真的初始化了，ffmpeg 一定会打两行
// `[Parsed_showinfo_N @ ...] config in time_base: ...`（**哪怕一帧都没选中**，实测如此）；
// 反过来，日志里连这个标记都没有（或者一处 pts_time 都解不出来）⇒ 这一项当作没采到，一个字都不说。
//
// 已知边界：如果调用方恰好**只截到**标记之后、showinfo 帧行之前（日志被生切一段），
// 这里的 0 会被当成"全片一处硬切都没有"报出去；这种形态在正常的整段收集里不会出现
// （要判"日志是不是被截断了"需要的东西这一层没有，也不该在这一层猜）。
func cutLogUsable(log string) bool {
	return strings.Contains(log, "Parsed_showinfo") || len(ParseCutTimes(log)) > 0
}

// StdFindings 把标准项落成体检结论。wantSceneCuts < 0 表示「不知道该有几处切点」（不判）。
//
// 只有**采到了**的那几项会出声：没采到的一项一个字都不说（调用方播报「没验过」）；
// 采到了但合格的项也不出声（调用方自己知道它验过了）—— 免得「没验过」与「合格」在报告里长得一样。
func StdFindings(a FFAnalysis, wantSceneCuts int) []Finding {
	var out []Finding

	// ── 1. 静音：连续 ≥2 秒 ⇒ 断音 ───────────────────────────────────────────────
	// 真机（2026-10-09《被开除一年后…·第01集》）量出 10 段 ≥1.5 秒的静音，其中 7 段超过 2 秒
	// —— 这是观众最容易听出来的一类缺陷（声音还在走、画面还在动，然后突然没声了）。
	if strings.TrimSpace(a.Silence) != "" {
		for _, r := range ParseSilence(a.Silence) {
			if r.Dur < silenceWarnSec {
				continue
			}
			out = append(out, Finding{Level: "warn", Where: "film.silence",
				Msg: fmt.Sprintf("连续静音 %.2f–%.2fs（%.2f 秒，超过 2 秒）—— 这一段既没人说话、也没有环境声，"+
					"观众听到的是**断音**（ffmpeg silencedetect -50dB 实测）；先看这一段的配音/环境声是不是没生成",
					r.Start, r.End, r.Dur)})
		}
	}

	// ── 2. 冻帧：画面冻结 ≥1 秒 ⇒ warn ───────────────────────────────────────────
	if strings.TrimSpace(a.Freeze) != "" {
		for _, r := range ParseFreeze(a.Freeze) {
			if r.Dur < freezeWarnSec {
				continue
			}
			out = append(out, Finding{Level: "warn", Where: "film.freeze",
				Msg: fmt.Sprintf("画面冻结 %.2f–%.2fs（%.2f 秒）—— 画面卡住不动而声音还在走"+
					"（H3 会输出冻帧；标准 QC 项 freezedetect）；这一镜要重渲", r.Start, r.End, r.Dur)})
		}
	}

	// ── 3. 响度：比对平台目标 -16 LUFS 与不削顶上限 ──────────────────────────────
	if strings.TrimSpace(a.Loudness) != "" {
		if l := ParseLoudness(a.Loudness); l.OK {
			if d := l.Integrated - loudnessTargetLUFS; math.Abs(d) > loudnessToleranceLU {
				out = append(out, Finding{Level: "warn", Where: "film.loudness",
					Msg: fmt.Sprintf("实测 %.1f LUFS / 目标 %.0f LUFS（差 %.1f LU，超过 %.1f LU）—— %s",
						l.Integrated, loudnessTargetLUFS, math.Abs(d), loudnessToleranceLU, loudnessHint(d))})
			}
			if l.TruePeakDBTP > truePeakWarnDBTP {
				out = append(out, Finding{Level: "warn", Where: "film.loudness",
					Msg: fmt.Sprintf("真峰值 %.1f dBTP，超过 -1.0 dBTP 的不削顶上限 —— 平台回放/转码时会**削顶**"+
						"（听到的是尖锐失真，不是响）；母轨要么重新做一次响度归一，要么把 BGM 床底降一点",
						l.TruePeakDBTP)})
			}
		}
	}

	// ── 4. 切点：实测硬切点数 vs 方案里的场次边界数 ──────────────────────────────
	// 这条判据防的是「同场接缝仍被叠化」：观众看到的是**一糊而过的转场**，
	// 而方案里这一处本来就该是硬切（同场连续动作被叠化会把两帧不同姿态的人像糊成重影）。
	//
	// 这一项与另外三项不同：它报的是**一个数**，而"没采到"时那个数正好也是 0，
	// 所以必须先确认 showinfo 真的跑过（cutLogUsable），否则一段垃圾日志/写错的命令
	// 会变成一条"实测 0 处硬切"的假结论 —— 那就是把没验过印成了验过。
	if cutLogUsable(a.CutLog) {
		cuts := ParseCutTimes(a.CutLog)
		switch {
		case wantSceneCuts < 0:
			out = append(out, Finding{Level: "info", Where: "film.cuts",
				Msg: fmt.Sprintf("实测 %d 处硬切点（ffmpeg select gt(scene,0.3)）%s —— "+
					"这次没拿到方案里的场次边界数，只报实测值、不判对错", len(cuts), cutTimesClause(cuts))})
		case len(cuts) < wantSceneCuts:
			out = append(out, Finding{Level: "warn", Where: "film.cuts",
				Msg: fmt.Sprintf("实测 %d 处硬切%svs 方案里的场次边界 %d 处 —— **少了 %d 处**："+
					"说明同场接缝仍在叠化（观众看到的是一糊而过的转场；平台口径是同场只留 1 帧硬切）",
					len(cuts), cutTimesClause(cuts), wantSceneCuts, wantSceneCuts-len(cuts))})
		case len(cuts) > wantSceneCuts:
			out = append(out, Finding{Level: "warn", Where: "film.cuts",
				Msg: fmt.Sprintf("实测 %d 处硬切%svs 方案里的场次边界 %d 处 —— **多了 %d 处**："+
					"说明同一场里出现了意外硬切（同一场是连续动作，多出来的切点是引擎自己断的）",
					len(cuts), cutTimesClause(cuts), wantSceneCuts, len(cuts)-wantSceneCuts)})
		}
	}

	return out
}

// loudnessHint 给「整集比目标响/轻」一句人话（含该怎么办）。
func loudnessHint(diff float64) string {
	if diff > 0 {
		return "整集比平台目标**更响**：合成那一步的 loudnorm 没生效、或后期又叠了一层增益" +
			"（手机/耳机上会显得刺，动态也被压掉）"
	}
	return "整集比平台目标**更轻**：观众得开大音量，而平台回放会自动把它拉响、把底噪一起抬起来"
}

// cutTimesClause 把切点时刻拼成一句话的尾巴（空列表就不加）。
func cutTimesClause(cuts []float64) string {
	if len(cuts) == 0 {
		return ""
	}
	return "（" + formatCutTimes(cuts) + "）"
}

// formatCutTimes 列切点时刻，超过 cutListMax 个只列前几个 + 总数。
func formatCutTimes(cuts []float64) string {
	parts := make([]string, 0, len(cuts)+1)
	for i, t := range cuts {
		if i == cutListMax {
			parts = append(parts, fmt.Sprintf("…等 %d 处", len(cuts)))
			break
		}
		parts = append(parts, fmt.Sprintf("%.2fs", t))
	}
	return strings.Join(parts, "、")
}

// parseSec 解析日志里的一个数字（**允许负数**：响度 -16.3 LUFS、真峰值 -1.0 dBTP 都是正常值）；
// 解析失败返回错误，调用方按「日志里没这个数」处理 —— 不拿默认值顶上（那就是编数）。
func parseSec(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}
