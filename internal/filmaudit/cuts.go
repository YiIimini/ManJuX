package filmaudit

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// cuts.go 逐镜的「**小分镜有没有在片段里兑现**」判据（2026-10-14）。
//
// 【为什么要有（用户 2026-10-14：「分镜脚本中的小分镜到底有没有做好？」）】
// 计划里的每一镜都带 2–4 个内切（正文的 `[Shot N] At MM:SS.mmm`，结构字段 `shot.cuts`），
// 契约门也**全过了**（每刀 1.5–4 秒、首刀为 0、各刀之和 = 片段时长）—— 但那些判据只看
// **计划的时间点**，一个字都没问过「渲出来的片段里到底切了没有」。
//
// 真机取证（`人间收件箱·第01集·雨夜叩门` 已渲的三镜，本机 ffmpeg scdet / showinfo）：
//
//	镜 1  计划 3.20s / 5.50s   →  实测内容切点只有 5.04s 一处
//	镜 2  计划 3.00s / 5.40s   →  **一处都没有**
//	镜 3  计划 3.00s / 5.20s   →  实测 1.875s / 3.875s（位置整体偏 1.1–1.3 秒）
//
// 也就是说：**小分镜写在计划里了，但没兑现到画面上**（观众看到的是"一镜到底"或者切在别处）。
// 这与本包已经记过的那类事故同型（"计划说切了 2-4 刀，35/35 镜全都没切"）—— 教训是
// **数计划里的刀数没用，必须量产物**。
//
// 【与 `StdFindings` 的 `film.cuts` 不是一回事】那一条量的是**整片**的硬切点数、判的是
// 「同场接缝是不是被叠化了」；这一条量的是**逐镜**的内容切点、判的是「计划的内切有没有出现」。
//
// 【判据只报"少了"，不报"多了"】内容切点（`select='gt(scene,0.3)'`）对**快速运动、闪光、
// 大范围明暗变化**同样敏感 —— 真机镜里"雨点扫过灯下""水汽升起"都能顶到阈值以上。所以
// "实测比计划多"多半是噪声，报了会把真问题淹掉；反过来"实测比计划少"是**漏报方向**的，
// 它只在"画面里根本看不出这一刀"时才响 —— 那种情况观众也看得出来。这条口径写在这里，
// 免得下一个改代码的人以为漏了一半。
//
// 【动作】点名镜号 + 重出该镜；提示词层面则是把每一刀的**取景差异**写大（换景别/换机位/
// 换主体），"同一机位同一景别只挪一点时间"的写法模型兑现不出来。
//
// 【当天就咬出并修掉了我自己这个量尺的一个真缺陷（同一轮，真机）】第一版跑真机时，
// 镜 2 那条**最该报的结论**（计划 2 刀 / 画面里一刀都没有）没出来，只印了一句「没量到」——
// 根因：`select='gt(scene,0.3)'` 在"一帧都没选中"时输出 0 帧，`ParseCutTimes` 于是返回 **nil**，
// 而 nil 与"没量到"在 Go 里**同形** ⇒ 已经量出来的失败被印成了"没验过"（三态反着用）。
// 更糟的是单测当时是**手写 `[]float64{}` 直接喂判据**，没走真入口 ⇒ 全绿（摆设守卫的教科书形态）。
// 修法：解析收成一格 `cutsFromLog`（单测与真机走同一条），一个点都没有时返回**非 nil 空切片**，
// `MeasureCuts` 再把 nil 统一成空切片 —— 空切片 = 量到 0 刀，nil = 没量到。

// CutSceneFilter 内容切点的量尺。**与 `ffcheck.go` 的 CutLog 同一口径**（同一条滤镜、
// 同一个解析函数 `ParseCutTimes`）—— 两处各写一条滤镜，阈值一漂就没人判得清对错。
const CutSceneFilter = "select='gt(scene,0.3)',showinfo"

// CutFn 一格片段的「内容切点」量尺（返回秒）。
//
// 【三态唯一出处在**错误值**上】`err != nil` = 这一格**没量到**（没有 ffmpeg、解码失败…），
// 判据跳过并点名；`err == nil` = 量到了，**返回空切片也算量到**（= 一处内容切点都没有）。
// 实现在"没量到"时**必须**给错误 —— 别用 nil/空切片去表达"没量到"（那正是下面那个缺陷）。
type CutFn func(path string) ([]float64, error)

// CutsFromFFmpeg 造一个逐片段的内容切点量尺。
func CutsFromFFmpeg(ffmpegPath string) CutFn {
	return func(path string) ([]float64, error) {
		if ffmpegPath == "" {
			return nil, fmt.Errorf("没有 ffmpeg 路径")
		}
		// `-v info`：`showinfo` 的逐帧日志只在 info 级写，`-v error` 会把它整段吞掉
		// （与本包 luma.go 记过的 blackdetect 同一条坑）。
		cmd := sysexec.Hide(exec.Command(ffmpegPath, "-v", "info", "-i", path, "-an",
			"-vf", CutSceneFilter, "-f", "null", "-"))
		var log bytes.Buffer
		cmd.Stderr = &log
		if _, err := cmd.Output(); err != nil {
			return nil, err
		}
		return cutsFromLog(log.String())
	}
}

// cutsFromLog 把一份完整的 `select,showinfo` 日志变成量尺的返回值。
//
// **唯一的解析入口**：单测喂真机日志、`CutsFromFFmpeg` 喂本地进程输出，都走这一格 ——
// 免得再出现"单测喂手写切片、真入口走另一条"（2026-10-14 那个缺陷就这么漏过去的）。
//
// 三态：日志里没有 `Parsed_showinfo` ⇒ 滤镜根本没初始化 ⇒ 错误（没量到）；
// 初始化了（ffmpeg 一定会打 `config in time_base` 那两行，**哪怕一帧都没选中** ——
// 真机 `testdata/ff_cuts_clip002_real.txt` 就是"2 行初始化 + 0 行 pts_time"）⇒ 量到了，
// 一个点都没有时返回**非 nil 的空切片**。
func cutsFromLog(s string) ([]float64, error) {
	if !cutLogUsable(s) {
		return nil, fmt.Errorf("showinfo 没跑起来（日志里没有 Parsed_showinfo）")
	}
	ts := ParseCutTimes(s)
	if ts == nil {
		ts = []float64{}
	}
	return ts, nil
}

// MeasureCuts 逐片段量内容切点（与 clips 同序；量不到的镜返回 nil 并记进 missing）。
//
// 量到但一处都没切的镜返回**空切片**（不是 nil）—— 这是"量到 0 刀"与"没量到"的唯一分界，
// 判据正是靠它区分（见 shotCutTimes）。
func MeasureCuts(fn CutFn, clips []string) ([][]float64, []int) {
	if fn == nil {
		return nil, nil
	}
	out := make([][]float64, len(clips))
	var missing []int
	for i, c := range clips {
		if strings.TrimSpace(c) == "" {
			continue
		}
		ts, err := fn(c)
		if err != nil {
			missing = append(missing, i)
			continue
		}
		if ts == nil {
			// 自定义量尺把"0 处"写成了 nil —— 这里统一成空切片，免得它又被当成"没量到"。
			ts = []float64{}
		}
		out[i] = ts
	}
	return out, missing
}

// shotCutTimes 第 i 镜量到的内容切点。
//
// 第二个返回值是三态的分界：`true` = **量到了**（`ts` 是空切片表示一处内容切点都没有），
// `false` = 没量到（没有片段 / 没有 ffmpeg / 解码失败）—— 后者判据一个字都不说，但调用方要点名。
func shotCutTimes(ts [][]float64, i int) ([]float64, bool) {
	if i < 0 || i >= len(ts) || ts[i] == nil {
		return nil, false
	}
	return ts[i], true
}

// plannedCutPoints 这一镜**计划里的内切点数**（`[Shot 2]` 起算，首刀 `[Shot 1]` 是片段开头、
// 不是"切一刀"）。
func plannedCutPoints(sh project.Shot) int {
	if len(sh.Cuts) < 2 {
		return 0
	}
	return len(sh.Cuts) - 1
}

// cutPointCount 量到的切点里**真正算切**的个数：丢掉贴近 0 的那个（片段开头自己那一帧，
// `showinfo` 有时会把它选进来），其余按 `ParseCutTimes` 的去重结果数。
func cutPointCount(ts []float64) int {
	n := 0
	for _, t := range ts {
		if t > 0.2 {
			n++
		}
	}
	return n
}

// CutDeliveryNote 逐镜的判据本体：返回 (级别, 人话)；空串 = 没什么可说的。
//
// `ok=false`（没量到）时**一个字都不说** —— 三态口径：没验过不等于合格，但也不编一条结论。
func CutDeliveryNote(sh project.Shot, ts []float64, ok bool) (string, string) {
	if !ok {
		return "", ""
	}
	want := plannedCutPoints(sh)
	if want == 0 {
		return "", "" // 这一镜本来就没计划内切（或只有一刀）
	}
	got := cutPointCount(ts)
	if got >= want {
		return "", ""
	}
	// 实测切点的那一段：一处都没选中时**明说**（真机镜 2 就是这个形态，也正是最该被看见的那种）——
	// 别留一个空括号"（实测切点 ）"（那看着像模板坏了，反倒不像结论）。
	// 括号在这里自己拼：`cutTimesClause` 自带一对括号，套进模板会变成双括号。
	measured := "（滤镜一处都没选中：画面里一刀都没切）"
	if len(ts) > 0 {
		measured = "（实测切点 " + formatCutTimes(ts) + "）"
	}
	return "warn", fmt.Sprintf("计划 %d 个内切，片段里**只看出 %d 个**%s —— "+
		"小分镜没兑现：观众看到的是一镜到底或者切在别处。改法：① 重出这一镜；"+
		"② 提示词里把每一刀的**取景差异写大**（换景别/换机位/换主体）——"+
		"同一机位同一景别只挪一点时间的写法，模型兑现不出来。"+
		"（本判据量的是**内容切点**：同景别小角度切换可能量不到，属漏报方向；"+
		"实测比计划多时**不报**，因为运动/闪光也会顶到阈值）",
		want, got, measured)
}

// MissingCutShots 哪几镜**没量到**内容切点（三态里的第二、三态要点名）。
func MissingCutShots(in Input) []int {
	if in.Plan == nil {
		return nil
	}
	var out []int
	for i := range in.Plan.Shots {
		if _, ok := shotCutTimes(in.ShotCuts, i); !ok {
			out = append(out, in.Plan.Shots[i].ID)
		}
	}
	return out
}

// cutDeliveryFindings 把逐镜判据落成体检结论。
func cutDeliveryFindings(in Input) []Finding {
	p := in.Plan
	if p == nil {
		return nil
	}
	var out []Finding
	for i := range p.Shots {
		sh := p.Shots[i]
		ts, ok := shotCutTimes(in.ShotCuts, i)
		level, msg := CutDeliveryNote(sh, ts, ok)
		if msg == "" {
			continue
		}
		out = append(out, Finding{Level: level, Where: fmt.Sprintf("shot #%d", sh.ID), Msg: msg})
	}
	return out
}
