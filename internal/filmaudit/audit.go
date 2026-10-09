// Package filmaudit 对**已产出的成片/半成品**做一轮**确定性体检**：读盘即可判，不用模型、不编视频。
//
// 【为什么要有它（2026-10-09 用户：「问题好多啊」，此前连续五轮都是用户自己看出来的）】
// 平台此前每一层都有自己的门（契约门 / 产出侧判据 / 质检），**但没有任何一处把"这一集到底行不行"
// 合起来说一遍**。后果是：字幕挂满整镜、同场接缝全叠化、说话人不在画面里还被标成画外音……
// 这些都能从盘上的文件算出来，却要等用户打开成片才看见。
//
// 体检只做四类**可确定性判定**的事，且每条都点名到"镜号 + 差多少 + 该怎么办"：
//
//	A. 字幕落点 vs **ASR 实测发声段**（偏差多少秒、还是压根没人声）
//	B. 说话人：台词的说话人在不在本镜画面里（不在 ⇒ 画外音，提示词里必须有"画内闭嘴"约束）
//	C. 接缝：同场连续动作有几处（叠化会把两帧糊成重影）
//	D. 结构：把 project.ValidatePlan 的既有判据**汇总进同一份报告**（不重复实现）
//
// 刻意**不做**：观感评分、VLM 判词、任何需要模型的事 —— 那些是 qc/cmd/imagecheck 的活。
package filmaudit

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/project"
)

// Cue 一条字幕（来自已合成的 final.srt / .ass）。
type Cue struct {
	Start, End float64
	Text       string
}

// Input 一集体检需要的全部输入（全部是**盘上已有**的东西）。
type Input struct {
	ID string
	// Plan 已按读取边界自愈（project.HealPlan）的那份方案。
	Plan *project.Plan
	// Durations / ASRSpans 与 plan.Shots 同序；缺失项留零值/nil。
	Durations []float64
	ASRSpans  [][]asr.Segment
	// Cues 已合成时的字幕（没合过片就留空 —— 字幕检查会自动跳过并说明）。
	Cues []Cue
	// Transition / FadeSec 项目参数（判"这份成片是按哪种转场行为合的"）。
	Transition string
	FadeSec    float64
	// FinalDuration 成片时长（秒，0 = 未知）。用来分辨成片是按"全叠化"还是"同场硬切"合的。
	FinalDuration float64
	// ClipMissing 找不到剪辑的镜号（诊断用：这些镜的时长与 ASR 都无从谈起）。
	ClipMissing []int
	// RawInFrameAsOffscreen 「说话人**就在本镜出场名单里**、却被标成 os=true」的句数（**自愈前**数）。
	//
	// 【为什么要在自愈前数（2026-10-09 真机）】读取边界（project.HealPlan）会按出场名单把这种误标
	// 纠正成 os=false —— 于是自愈之后「全是画外音」这个形态**看不见了**，可它造成的后果还在：
	// 那一版提示词是**照着旧标法**装配并提交的（真机 19 句全被标成画外音 ⇒ 镜 5 的真画外音反而
	// 没拿到"画内闭嘴"约束）。体检要说的正是这句话：已提交的提示词可能不对，重渲那几镜。
	RawInFrameAsOffscreen int
}

// Finding 一条体检结论。Level: fail（必须处理）/ warn（该看一眼）/ info（如实告知）。
type Finding struct {
	Level string
	Where string
	Msg   string
}

// 阈值：字幕起点比实测发声段早/晚多少就算"对不上"。
//
// 取 0.6 秒：H3 的片段内部有切点，人在第 N 刀开口是常事；0.6 秒以内观众不会觉得"字幕在飘"，
// 超过就是真的对不上（真机那一次镜 1 差了 2.7 秒）。
const cueSlackSec = 0.6

// 一条字幕挂多久算"太久"（观众会读着读着发现声音早没了）。
const cueTooLongSec = 9.0

// Audit 跑一遍体检，返回按严重度排序的结论。
func Audit(in Input) []Finding {
	var out []Finding
	if in.Plan == nil || len(in.Plan.Shots) == 0 {
		return []Finding{{Level: "fail", Where: "plan", Msg: "读不到方案或没有镜头 —— 体检无从谈起"}}
	}
	p := in.Plan
	if len(in.ClipMissing) > 0 {
		out = append(out, Finding{Level: "fail", Where: "clips",
			Msg: fmt.Sprintf("第 %s 镜找不到剪辑文件 —— 这一集合不出来（先把这几镜渲出来）",
				joinInts(in.ClipMissing))})
	}

	// ── A. 字幕落点 vs ASR 实测发声段 ────────────────────────────────────────────
	cuesByShot, srtNotes := splitCuesByShot(p, in.Durations, in.Cues)
	out = append(out, srtNotes...)
	starts := shotStarts(in.Durations)
	notSpoken, drifted, unmatch := 0, 0, 0
	for i, sh := range p.Shots {
		n := len(shotSubtitles(sh))
		if n == 0 {
			continue
		}
		segs := segmentsOf(in.ASRSpans, i)
		shotID := i + 1
		if len(segs) == 0 {
			notSpoken++
			if len(in.Cues) > 0 {
				out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("shot #%d", shotID),
					Msg: fmt.Sprintf("计划里的 %d 句台词在音轨里**没有任何人声**（ASR 0 段），而字幕照挂 —— "+
						"这几句很可能根本没被念出来（语音对账那份报告里同样会点名）", n)})
			} else {
				out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("shot #%d", shotID),
					Msg: fmt.Sprintf("计划里的 %d 句台词在音轨里**没有任何人声**（ASR 0 段）—— 这一镜要重渲", n)})
			}
			continue
		}
		if len(segs) == n && len(cuesByShot[i]) == n {
			// 逐句对齐：字幕起点应当贴在 ASR 段起点上。
			for j := range segs {
				want := starts[i] + segs[j].Start
				got := cuesByShot[i][j].Start
				if d := math.Abs(got - want); d > cueSlackSec {
					drifted++
					out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("shot #%d 第 %d 句", shotID, j+1),
						Msg: fmt.Sprintf("字幕起点 %.2fs，实测发声在 %.2fs（差 %.2fs）—— %s",
							got, want, d, cueHint(got, want))})
				}
			}
			continue
		}
		// 段数与句数不符：做不到逐句，但至少字幕不许跑出人声区间。
		lo := starts[i] + segs[0].Start
		hi := starts[i] + segs[len(segs)-1].End
		for j, c := range cuesByShot[i] {
			if c.Start < lo-cueSlackSec || c.End > hi+cueSlackSec {
				unmatch++
				out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("shot #%d 第 %d 句", shotID, j+1),
					Msg: fmt.Sprintf("人声区间是 %.2f–%.2fs（ASR 共 %d 段、台词 %d 句），而字幕挂 %.2f–%.2fs —— "+
						"段数与句数不符时**做不到逐句对齐**，但至少该把窗口收进人声区间",
						lo, hi, len(segs), n, c.Start, c.End)})
				break
			}
		}
	}

	if in.RawInFrameAsOffscreen > 0 {
		out = append(out, Finding{Level: "warn", Where: "plan.dialogue",
			Msg: fmt.Sprintf("模型把 %d 句**画面里的人在说**的台词标成了画外音（os=true）—— "+
				"平台读取时会按出场名单纠正，但**已经提交的那版提示词带着旧标法**：那一镜里「谁开口」"+
				"没被约束住，模型可能安排别的人动嘴；若成片里确实看到别人动嘴，请重渲这几镜",
				in.RawInFrameAsOffscreen)})
	}
	if notSpoken+drifted+unmatch == 0 && len(in.Cues) > 0 {
		out = append(out, Finding{Level: "info", Where: "subtitle",
			Msg: "字幕落点与逐镜 ASR 实测发声段一致（没有超过 0.6 秒的偏差，也没有越出人声区间）"})
	}

	// ── B. 说话人：在不在画面里 ─────────────────────────────────────────────────
	out = append(out, speakerFindings(p)...)

	// ── C. 接缝：同场连续动作有几处 ──────────────────────────────────────────────
	out = append(out, seamFindings(p, in)...)

	// ── D. 结构：把既有判据汇总进同一份报告 ────────────────────────────────────
	//
	// 【2026-10-09 修：`fail` 不许被截掉】第一版把非 info 的结论**一刀切留前 12 条**，
	// 而列表顺序是 `ValidatePlan` 的产出顺序 —— 真机上就出现了 ⛔ 被 12 条 ⚠ 挤到窗口之外：
	// 体检跑出来 ⛔ 少了一条，用户看到的"必须处理"比实际少。现在分两遍：
	// **⛔ 一条不少**，只对 ⚠ 截断（并在 info 里如实说少了几条）。
	var hardFails, softWarns []Finding
	for _, it := range project.ValidatePlan(p) {
		switch it.Level {
		case "info":
			continue
		case "fail":
			hardFails = append(hardFails, Finding{Level: it.Level, Where: it.Where, Msg: it.Msg})
		default:
			softWarns = append(softWarns, Finding{Level: it.Level, Where: it.Where, Msg: it.Msg})
		}
	}
	out = append(out, hardFails...)
	const maxSoftWarns = 12
	if len(softWarns) > maxSoftWarns {
		out = append(out, softWarns[:maxSoftWarns]...)
		out = append(out, Finding{Level: "info", Where: "plan",
			Msg: fmt.Sprintf("另有 %d 条契约判据结论（同类聚合见「剧本分镜」阶段的播报）",
				len(softWarns)-maxSoftWarns)})
	} else {
		out = append(out, softWarns...)
	}
	sortFindings(out)
	return out
}

// speakerFindings 说话人在不在本镜画面里（B）。
func speakerFindings(p *project.Plan) []Finding {
	var out []Finding
	inFrameLines, offLines := 0, 0
	for i := range p.Shots {
		sh := &p.Shots[i]
		off := project.OffScreenSpeakers(p, *sh)
		if len(off) > 0 {
			offLines += len(off)
			haveClause := strings.Contains(sh.H3Prompt, project.VoiceoverClauseMarker)
			level, extra := "info", ""
			if !haveClause {
				level = "warn"
				extra = "；**提示词里没有**\"画内的人保持闭嘴\"那条强制约束（渲染时平台会补上；" +
					"若成片里仍看到画面里的人替他把嘴动了，请重渲这一镜）"
			}
			out = append(out, Finding{Level: level, Where: fmt.Sprintf("shot #%d", i+1),
				Msg: fmt.Sprintf("台词是 %s 说的，而**不在本镜画面里** ⇒ 画外音%s",
					strings.Join(off, "、"), extra)})
		}
		for _, l := range sh.Dialogue {
			if strings.TrimSpace(l.Text) == "" || project.IsVoiceoverSpeaker(l.Speaker) {
				continue
			}
			if l.OS {
				offLines++
			} else {
				inFrameLines++
			}
		}
	}
	if inFrameLines == 0 && offLines >= 3 {
		out = append(out, Finding{Level: "warn", Where: "plan",
			Msg: fmt.Sprintf("整集 %d 句台词**全是画外音**，没有一句是画面里的人在说 —— "+
				"观众一直看不到谁在开口，声音与口型必然对不上（模型把画内台词也标成画外音时就是这个形态）",
				offLines)})
	}
	return out
}

// seamFindings 接缝（C）：同场连续动作有几处 + 这份成片是按哪种行为合的。
func seamFindings(p *project.Plan, in Input) []Finding {
	var out []Finding
	same := project.SceneSeamCounts(p) // (同场接缝数, 总接缝数)
	if same[1] == 0 {
		return nil
	}
	trans := strings.TrimSpace(in.Transition)
	out = append(out, Finding{Level: "info", Where: "seams",
		Msg: fmt.Sprintf("%d 处接缝里 **%d 处是同一场次的连续动作**（叠化会把两帧不同姿态的人像糊成重影；"+
			"平台口径：叠化只用在换场处，同场只留 1 帧）", same[1], same[0])})
	// 成片时长能分辨"全叠化"还是"同场硬切"：两者相差 (同场接缝数) × 叠化时长。
	if in.FinalDuration > 0 && same[0] > 0 && strings.EqualFold(trans, "fade") {
		fd := in.FadeSec
		if fd <= 0 {
			fd = 0.5
		}
		sum := 0.0
		for _, d := range in.Durations {
			sum += d
		}
		allFade := sum - float64(same[1])*fd
		sameCut := sum - float64(same[1]-same[0])*fd
		if math.Abs(in.FinalDuration-allFade) < math.Abs(in.FinalDuration-sameCut) {
			out = append(out, Finding{Level: "warn", Where: "seams",
				Msg: fmt.Sprintf("这份成片时长 %.1fs ≈ **每一处接缝都叠化**的算法（%.1fs），而不是"+
					"同场硬切（%.1fs）—— 说明它是**旧行为**合的；重跑「合片」即按新口径重合成（不用重渲）",
					in.FinalDuration, allFade, sameCut)})
		}
	}
	return out
}

// cueHint 给"字幕比声音早/晚"一句人话。
func cueHint(got, want float64) string {
	if got < want {
		return "字幕比声音**早出**（观众先看到字、后听到话）"
	}
	return "字幕比声音**晚出**（话都说出来了字才跟上）"
}

// splitCuesByShot 把全片字幕按镜归位（按各镜在成片时间轴上的区间切）。
// 顺带做两条与镜无关的检查：单条挂太久、相邻重叠。
func splitCuesByShot(p *project.Plan, durations []float64, cues []Cue) ([][]Cue, []Finding) {
	var out []Finding
	byShot := make([][]Cue, len(p.Shots))
	if len(cues) == 0 {
		return byShot, out
	}
	starts := shotStarts(durations)
	for idx, c := range cues {
		if c.End-c.Start > cueTooLongSec {
			out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("cue %d", idx+1),
				Msg: fmt.Sprintf("这条字幕挂了 %.1f 秒（%.2f–%.2f）—— 一句话不会说这么久，多半是落点"+
					"退回了「整镜窗口」（按整镜时长铺开）；观众会看着字幕等声音，而声早说完了",
					c.End-c.Start, c.Start, c.End)})
		}
		if idx > 0 && cues[idx-1].End > c.Start+0.02 {
			out = append(out, Finding{Level: "warn", Where: fmt.Sprintf("cue %d", idx+1),
				Msg: fmt.Sprintf("与上一条**重叠** %.2fs —— 两句同屏叠字",
					cues[idx-1].End-c.Start)})
		}
		// 归位：字幕中心落在哪一镜的区间里
		mid := (c.Start + c.End) / 2
		for i := range p.Shots {
			lo := starts[i]
			hi := lo + dur(durations, i)
			if mid >= lo && mid < hi {
				byShot[i] = append(byShot[i], c)
				break
			}
		}
	}
	return byShot, out
}

// shotStarts 与 compose 同一套几何：各镜累加、再减去接缝重叠。
//
// 体检不重算转场策略（那是 compose 的事），只按**实际给了多少重叠**推起点 ——
// 所以这里用"每处接缝都按参数设的叠化"推：真机那一集正是这种情况，
// 而"这份成片按哪种行为合的"由 seamFindings 单独判。
func shotStarts(durations []float64) []float64 {
	starts := make([]float64, len(durations))
	var acc float64
	for i := range durations {
		starts[i] = acc
		acc += durations[i]
	}
	return starts
}

func dur(durations []float64, i int) float64 {
	if i < len(durations) && durations[i] > 0 {
		return durations[i]
	}
	return 7.0
}

func segmentsOf(spans [][]asr.Segment, i int) []asr.Segment {
	if i < len(spans) {
		return spans[i]
	}
	return nil
}

// shotSubtitles 该镜的字幕条目（与 compose/director 的口径一致：逐句台词 + 旁白）。
func shotSubtitles(sh project.Shot) []string {
	return director.ShotSubtitleParts(sh)
}

func sortFindings(fs []Finding) {
	rank := func(l string) int {
		switch l {
		case "fail":
			return 0
		case "warn":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(fs, func(i, j int) bool { return rank(fs[i].Level) < rank(fs[j].Level) })
}

func joinInts(xs []int) string {
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, fmt.Sprintf("#%d", x))
	}
	return strings.Join(parts, " ")
}

// ASRSidecarPath 某镜的 ASR 侧车文件路径（`<clip>.asr.txt`）。
func ASRSidecarPath(clip string) string {
	return strings.TrimSuffix(clip, filepath.Ext(clip)) + ".asr.txt"
}

// CountLevels 统计各档条数（给 CLI 打一行总结）。
func CountLevels(fs []Finding) (fail, warn, info int) {
	for _, f := range fs {
		switch f.Level {
		case "fail":
			fail++
		case "warn":
			warn++
		default:
			info++
		}
	}
	return
}

// countInFrameMarkedOffscreen 数「说话人**就在本镜出场名单里**、却被标成 os=true」的句数。
//
// 只看方案自己的两个字段（`characters` 出场 + `offscreen` 已出画），与 project.markOffscreenSpeakers
// 同一口径 —— 差别是这里**不做纠正**，只把"模型标错了多少句"数出来（体检要报的是这个数）。
func countInFrameMarkedOffscreen(p *project.Plan) int {
	if p == nil {
		return 0
	}
	nameByID := map[string]string{}
	isKnown := map[string]bool{}
	for _, ch := range p.Characters {
		nameByID[strings.TrimSpace(ch.ID)] = strings.TrimSpace(ch.Name)
		isKnown[strings.TrimSpace(ch.ID)] = true
		isKnown[strings.TrimSpace(ch.Name)] = true
	}
	n := 0
	for i := range p.Shots {
		sh := &p.Shots[i]
		if len(sh.Dialogue) == 0 {
			continue
		}
		onScreen := map[string]bool{}
		for _, ref := range sh.Characters {
			ref = strings.TrimSpace(ref)
			onScreen[ref] = true
			if nm := nameByID[ref]; nm != "" {
				onScreen[nm] = true
			}
		}
		off := map[string]bool{}
		for k := range sh.Offscreen {
			off[strings.TrimSpace(k)] = true
			if nm := nameByID[strings.TrimSpace(k)]; nm != "" {
				off[nm] = true
			}
		}
		for _, l := range sh.Dialogue {
			sp := strings.TrimSpace(l.Speaker)
			if sp == "" || !l.OS || project.IsVoiceoverSpeaker(sp) || !isKnown[sp] {
				continue
			}
			if onScreen[sp] && !off[sp] {
				n++
			}
		}
	}
	return n
}
