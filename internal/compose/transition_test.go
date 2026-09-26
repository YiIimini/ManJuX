package compose

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 转场判据的契约测试(审计 2026-09-26:三套判据 + FadeSec 无上界)。
//
// 这里钉的是**不变式**而不是实现细节:
//   - 画面重叠 == 字幕起点偏移 == 音轨让出量,三处同源,不可能各说各话;
//   - 重叠不得超过相邻任一镜自身(xfade 能成立的必要条件);
//   - 非法转场/越界时长被改写后必须报出来(静默改参数与静默不生效是同一类缺陷)。

func TestEffectiveTransitionNormalizes(t *testing.T) {
	cases := map[string]string{
		"cut":      transCut,
		"fade":     transFade,
		"dissolve": transFade, // 存量参数:颗粒溶解已弃用,一律按平滑叠化执行
		"":         transCut,  // 缺键:旧实现让字幕按"有重叠"扣、画面却硬切
		"CUT":      transCut,
		"wipe":     transCut,
		"fade ":    transCut,
		"交叉溶解":     transCut,
	}
	for in, want := range cases {
		if got := effectiveTransition(in); got != want {
			t.Errorf("effectiveTransition(%q) = %q,期望 %q", in, got, want)
		}
	}
}

func TestClampFadeSecBounds(t *testing.T) {
	cases := []struct {
		name    string
		in      float64
		want    float64
		clamped bool
	}{
		{"0 = 用默认值", 0, defaultFadeSec, false},
		{"负值 = 未设置", -1, defaultFadeSec, false},
		{"NaN = 未设置", math.NaN(), defaultFadeSec, false},
		{"下界以下被钳住", 0.02, minFadeSec, true},
		{"区间内原样透传", 0.5, 0.5, false},
		{"下界本身合法", minFadeSec, minFadeSec, false},
		{"上界本身合法", maxFadeSec, maxFadeSec, false},
		{"上界以上被钳住", 600, maxFadeSec, true}, // 单位写错/复制粘贴的典型值
	}
	for _, c := range cases {
		got, clamped := clampFadeSec(c.in)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: clampFadeSec(%v) = %.3f,期望 %.3f", c.name, c.in, got, c.want)
		}
		if clamped != c.clamped {
			t.Errorf("%s: 改写标记 = %v,期望 %v(被改写就要播报)", c.name, clamped, c.clamped)
		}
	}
}

// TestSeamFadesNeverExceedShots 「短镜」那条:重叠不得超过相邻任一镜自身,
// 且被压到"半镜"以内 —— 否则一镜会被前后两条接缝吃光,在成片里被整镜跳过。
// 旧实现只在字幕那边判 `d > fd`,画面与音轨各按各的走 —— 短镜接缝上三者彻底分家。
func TestSeamFadesNeverExceedShots(t *testing.T) {
	durations := []float64{4, 0.6, 6, 0.3}
	fades := seamFades(transFade, durations, 2.0)
	if len(fades) != len(durations) {
		t.Fatalf("fades 应与 durations 等长(便于三处按下标取用),实际 %d vs %d", len(fades), len(durations))
	}
	if fades[len(fades)-1] != 0 {
		t.Errorf("最后一镜后面没有接缝,重叠应为 0,实际 %.3f", fades[len(fades)-1])
	}
	for i := 0; i+1 < len(durations); i++ {
		if fades[i] > durations[i]/2+1e-9 || fades[i] > durations[i+1]/2+1e-9 {
			t.Errorf("接缝 %d 的重叠 %.3f 超过相邻镜的一半(%.3f/%.3f):那一镜会被接缝吃光",
				i, fades[i], durations[i]/2, durations[i+1]/2)
		}
	}
	if math.Abs(fades[0]-0.3) > 1e-9 { // 0.6s 的短镜最多让出自己的一半
		t.Errorf("接缝 0 应被 0.6s 的短镜压到 0.3,实际 %.3f", fades[0])
	}
	// 正常参数(0.5s 转场、≥4s 镜头)必须与旧行为逐字一致:min 那一项不生效
	normal := seamFades(transFade, []float64{4, 4, 4}, 0.5)
	for i := 0; i < 2; i++ {
		if math.Abs(normal[i]-0.5) > 1e-9 {
			t.Errorf("常规参数下接缝 %d 应原样 0.5,实际 %.3f", i, normal[i])
		}
	}
	if got := seamFades(transCut, durations, 2.0); got[0] != 0 || got[1] != 0 {
		t.Errorf("硬切路径一个接缝都不该让出重叠,实际 %v", got)
	}
	// 退化源片(时长≈0)时下限不能反超可用的一半,否则 starts 会倒退
	degen := seamFades(transFade, []float64{0.02, 0.02}, 1.0)
	if degen[0] > 0.01+1e-9 {
		t.Errorf("退化源片(0.02s)的重叠 %.4f 超过了可用的一半", degen[0])
	}
}

// TestShotStartsMatchPictureOverlap 字幕起点必须与画面接缝位置同源:
//
//	starts[i] == Σdurations[<i] − Σfades[<i]
//
// 这条一旦破,全片字幕会逐镜提前 i×FadeSec(旧实现的实际故障)。
func TestShotStartsMatchPictureOverlap(t *testing.T) {
	durations := []float64{4, 0.6, 6, 3.3}
	for _, fd := range []float64{0.1, 0.5, 2.0, 3.0} {
		fades := seamFades(transFade, durations, fd)
		starts := shotStarts(durations, fades)
		var sumD, sumF float64
		for i := range durations {
			if i > 0 {
				sumD += durations[i-1]
				sumF += fades[i-1]
			}
			if math.Abs(starts[i]-(sumD-sumF)) > 1e-9 {
				t.Errorf("fd=%.1f 镜 %d:starts=%.3f,期望 Σd−Σfade=%.3f", fd, i, starts[i], sumD-sumF)
			}
		}
		// 末镜起点 + 末镜时长 == 成片总长
		last := starts[len(starts)-1] + durations[len(durations)-1]
		if math.Abs(filmDuration(durations, fades)-last) > 1e-9 {
			t.Errorf("fd=%.1f 成片总长 %.3f ≠ 末镜起点+末镜时长 %.3f", fd, filmDuration(durations, fades), last)
		}
		// 起点必须单调推进(接缝位置随成片往前走)
		for i := 1; i < len(starts); i++ {
			if starts[i] <= starts[i-1] {
				t.Errorf("fd=%.1f 镜 %d 的起点 %.3f 未推进(前一镜 %.3f)", fd, i, starts[i], starts[i-1])
			}
		}
	}
}

// TestSeamAudioWindowsMatchPictureOverlap 音轨让出的音频必须与画面让出的重叠**等长**:
//
//	(durations[i]-tails[i]) + heads[i+1] == fades[i]
//
// 旧实现在短镜接缝上整个跳过(两镜音频都保持整段),不变式因此破了 ——
// 音轨比画面长,且越往后累积越多,全程还不报错。
func TestSeamAudioWindowsMatchPictureOverlap(t *testing.T) {
	durations := []float64{4, 0.6, 6}
	fades := seamFades(transFade, durations, 1.0)
	cuts := []float64{fades[0] / 3, fades[1] / 2, 0}
	heads, tails := seamAudioWindows(durations, fades, cuts)
	for i := 0; i+1 < len(durations); i++ {
		gave := (durations[i] - tails[i]) + heads[i+1]
		if math.Abs(gave-fades[i]) > 1e-9 {
			t.Errorf("接缝 %d:音轨让出 %.3f ≠ 画面重叠 %.3f(音画从此错位)", i, gave, fades[i])
		}
	}
}

// TestSeamAudioWindowsClampCuts 越界切点被夹回窗口内,否则尾巴会算出负数、
// 音轨比画面短。硬切接缝(fades=0)必须整段保留 —— 硬切不丢任何一个字。
func TestSeamAudioWindowsClampCuts(t *testing.T) {
	durations := []float64{4, 4}
	fades := []float64{0.5, 0}
	for _, cut := range []float64{-1, 0.5, 2} {
		heads, tails := seamAudioWindows(durations, fades, []float64{cut, 0})
		if tails[0] < heads[0] || tails[0] > durations[0] {
			t.Errorf("cut=%.2f 时第一镜保留区间 [%.3f,%.3f] 非法", cut, heads[0], tails[0])
		}
		if heads[1] < 0 || heads[1] > fades[0] {
			t.Errorf("cut=%.2f 时后镜头部 %.3f 越出窗口 [0,%.2f]", cut, heads[1], fades[0])
		}
	}
	heads, tails := seamAudioWindows(durations, []float64{0, 0}, []float64{0, 0})
	if heads[0] != 0 || tails[0] != durations[0] || heads[1] != 0 || tails[1] != durations[1] {
		t.Errorf("硬切(无重叠)必须两镜整段保留,实际 heads=%v tails=%v", heads, tails)
	}
}

// TestTransitionNotesReportRewrites 非法转场/越界时长的改写必须播报,合法时保持安静 ——
// "悄悄改了参数"与"参数不生效"是同一类缺陷的两面(审计 P1-1 的镜像)。
func TestTransitionNotesReportRewrites(t *testing.T) {
	if got := transitionNotes("fade", 0.5, 0.5, false); len(got) != 0 {
		t.Errorf("合法参数不该播报,实际 %v", got)
	}
	if got := transitionNotes("cut", 0.5, 0.5, false); len(got) != 0 {
		t.Errorf("cut 合法,不该播报,实际 %v", got)
	}
	if got := transitionNotes("", 0.5, 0.5, false); len(got) != 1 {
		t.Errorf("非法转场值必须播报一条,实际 %v", got)
	}
	if got := transitionNotes("fade", 600, maxFadeSec, true); len(got) != 1 {
		t.Errorf("越界时长必须播报一条,实际 %v", got)
	}
	if got := transitionNotes("wipe", 600, maxFadeSec, true); len(got) != 2 {
		t.Errorf("非法转场 + 越界时长应各播报一条,实际 %v", got)
	}
}

// TestSeamFeedbackCountsOnlyEvaluatedSeams M37:分母只能是**真正求解过**的接缝,
// 否则"9 个接缝全退回了中点切"会被显示成「0/9 处完全落在静音…人声损失已最小化」。
func TestSeamFeedbackCountsOnlyEvaluatedSeams(t *testing.T) {
	// 3 个接缝,只有 1 个求解成功、其中 0 个落在静音
	notes := seamFeedback(0, 1, 3, []int{2, 5})
	joined := strings.Join(notes, "|")
	if !strings.Contains(joined, "0/1 处完全落在静音") {
		t.Errorf("分母应为已求解的接缝数 1,实际播报:%q", joined)
	}
	if strings.Contains(joined, "/3 处") {
		t.Errorf("未评估的接缝不能算进分母:%q", joined)
	}
	if !strings.Contains(joined, "2 个接缝未做静音求解") {
		t.Errorf("应明说有几个接缝没评估:%q", joined)
	}
	if !strings.Contains(joined, "2、5") {
		t.Errorf("应点出是哪几镜的音轨分析失败:%q", joined)
	}

	// 全部求解成功时只留一条正常回显,不制造噪声
	notes = seamFeedback(2, 2, 2, nil)
	if len(notes) != 1 || !strings.Contains(notes[0], "2/2 处完全落在静音") {
		t.Errorf("全部求解成功时应只有一条回显,实际 %v", notes)
	}
	// 单镜(没有接缝)时分母兜底为 1,不出 NaN/除零
	if notes := seamFeedback(0, 0, 0, nil); !strings.Contains(notes[0], "0/1") {
		t.Errorf("无接缝时也应给出可读回显,实际 %v", notes)
	}
}

// TestSubtitleWindowSharedBySRTAndASS M36:同一句话在烧录的 ASS 与外发的 SRT 里
// 必须落在同一个时间窗内(旧实现一个 8%~88%、一个 10%~90%,作者拿 SRT 去平台对轨
// 会发现对不齐)。
func TestSubtitleWindowSharedBySRTAndASS(t *testing.T) {
	const d = 10.0
	s, e := subtitleWindow(d)
	if math.Abs(s-1.0) > 1e-9 || math.Abs(e-9.0) > 1e-9 {
		t.Fatalf("窗口应为镜头头尾各留 10%%:[%.2f, %.2f]", s, e)
	}

	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Duration: 10, Dialogue: []project.Line{{Speaker: "甲", Text: "只有一句"}}},
	}}
	starts := []float64{0}
	durations := []float64{d}

	srt, err := srtWindow(t, BuildSRT(plan, starts, durations))
	if err != nil {
		t.Fatal(err)
	}
	ass, err := assWindow(t, BuildASS(plan, starts, durations, 4, 768, 1344))
	if err != nil {
		t.Fatal(err)
	}
	// SRT 每条前后各留 50ms(避免相邻条目贴边),所以允许这点小差;
	// 旧实现两边的**基准窗口**就差 2%(0.8s vs 1.0s),差距是它的四倍。
	if math.Abs(srt[0]-ass[0]) > 0.06 || math.Abs(srt[1]-ass[1]) > 0.06 {
		t.Errorf("SRT 与 ASS 的字幕窗口应一致(仅允许 SRT 的 50ms 行内留白):SRT [%.2f,%.2f] vs ASS [%.2f,%.2f]",
			srt[0], srt[1], ass[0], ass[1])
	}
	if math.Abs(srt[0]-1.0) > 0.06 {
		t.Errorf("窗口起点应约 1.0s(10s × 10%%),实际 %.2f", srt[0])
	}
	if math.Abs(srt[1]-9.0) > 0.06 {
		t.Errorf("窗口终点应约 9.0s(10s × 90%%),实际 %.2f", srt[1])
	}
}

// srtWindow 解析 SRT 第一条字幕的时间区间(秒)。
func srtWindow(t *testing.T, srt string) ([2]float64, error) {
	t.Helper()
	for _, ln := range strings.Split(srt, "\n") {
		if !strings.Contains(ln, " --> ") {
			continue
		}
		f := strings.Split(ln, " --> ")
		return [2]float64{srtSeconds(t, strings.TrimSpace(f[0])), srtSeconds(t, strings.TrimSpace(f[1]))}, nil
	}
	return [2]float64{}, errNoCue
}

// assWindow 解析 ASS 第一条 Dialogue 的时间区间(秒)。
func assWindow(t *testing.T, ass string) ([2]float64, error) {
	t.Helper()
	for _, ln := range strings.Split(ass, "\n") {
		if !strings.HasPrefix(ln, "Dialogue:") {
			continue
		}
		parts := strings.Split(ln, ",")
		if len(parts) < 4 {
			continue
		}
		return [2]float64{assSeconds(parts[1]), assSeconds(parts[2])}, nil
	}
	return [2]float64{}, errNoCue
}

// assSeconds 把 "0:00:01.00" 解析成秒。
func assSeconds(s string) float64 {
	var h, m int
	var sec float64
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d:%f", &h, &m, &sec); err != nil {
		return -1
	}
	return float64(h*3600+m*60) + sec
}

var errNoCue = fmt.Errorf("字幕里没有可解析的时间轴")
