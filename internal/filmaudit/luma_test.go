package filmaudit

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/compose"
	"github.com/YiIimini/ManJuX/internal/project"
)

// luma_test.go 画面亮度/黑场体检的门。
//
// 【夹具纪律】下面每一段夹具都是**真机原话**（2026-10-11 在本机 ffmpeg 9.0.1 上，对
// 《星纪山海：我在废土养神兽》第01集 16 条片段 `clips/shot_001..016.mp4` 跑出来的逐帧 YAVG
// 与 blackdetect 日志）—— 连 `frame:/pts:` 行、键名、小数位都原样贴。假夹具会掩盖缺陷
// （第 54 轮为此重写过两处）：这里要咬的就是"解析与统计对真机输出成不成立"。

// realShot1YAVG 真机 shot_001.mp4 第 120~140 帧的 metadata 原话（含中间那处暗坑：f139=23.4305）。
const realShot1YAVG = `frame:120  pts:61440   pts_time:5
lavfi.signalstats.YAVG=90.78
frame:121  pts:61952   pts_time:5.041667
lavfi.signalstats.YAVG=90.3658
frame:122  pts:62464   pts_time:5.083333
lavfi.signalstats.YAVG=90.5607
frame:123  pts:62976   pts_time:5.125
lavfi.signalstats.YAVG=90.5214
frame:124  pts:63488   pts_time:5.166667
lavfi.signalstats.YAVG=90.4287
frame:125  pts:64000   pts_time:5.208333
lavfi.signalstats.YAVG=90.053
frame:126  pts:64512   pts_time:5.25
lavfi.signalstats.YAVG=89.9163
frame:127  pts:65024   pts_time:5.291667
lavfi.signalstats.YAVG=89.3822
frame:128  pts:65536   pts_time:5.333333
lavfi.signalstats.YAVG=88.9289
frame:129  pts:66048   pts_time:5.375
lavfi.signalstats.YAVG=88.3134
frame:130  pts:66560   pts_time:5.416667
lavfi.signalstats.YAVG=88.3146
frame:131  pts:67072   pts_time:5.458333
lavfi.signalstats.YAVG=88.1316
frame:132  pts:67584   pts_time:5.5
lavfi.signalstats.YAVG=88.2755
frame:133  pts:68096   pts_time:5.541667
lavfi.signalstats.YAVG=87.5487
frame:134  pts:68608   pts_time:5.583333
lavfi.signalstats.YAVG=87.6547
frame:135  pts:69120   pts_time:5.625
lavfi.signalstats.YAVG=77.6935
frame:136  pts:69632   pts_time:5.666667
lavfi.signalstats.YAVG=65.6969
frame:137  pts:70144   pts_time:5.708333
lavfi.signalstats.YAVG=54.2804
frame:138  pts:70656   pts_time:5.75
lavfi.signalstats.YAVG=42.2552
frame:139  pts:71168   pts_time:5.791667
lavfi.signalstats.YAVG=23.4305
frame:140  pts:71680   pts_time:5.833333
lavfi.signalstats.YAVG=31.5513
`

// realUnfilteredMetadata 真机**不带 key 过滤**的 metadata 原话（同一个文件、同一帧）：
// 用来咬"别的键不许被当成 YAVG"（`YMIN=`/`YLOW=` 与 `YAVG=` 挤在一起）。
const realUnfilteredMetadata = `frame:0    pts:0       pts_time:0
lavfi.signalstats.YMIN=12
lavfi.signalstats.YLOW=31
lavfi.signalstats.YAVG=101.171
`

// ① 解析：真机原话 → 逐帧亮度；别的键（YMIN/YLOW/YDIF…）一律不许被当成 YAVG。
func TestParseYAVGReadsRealFFmpegOutput(t *testing.T) {
	ys := ParseYAVG(realShot1YAVG)
	if len(ys) != 21 {
		t.Fatalf("真机那段是 21 帧，解析出 %d 个值 —— 粒度/键名对不上就该当场红", len(ys))
	}
	if ys[0] != 90.78 || ys[20] != 31.5513 {
		t.Errorf("首尾对不上：%v … %v（真机是 90.78 … 31.5513）", ys[0], ys[20])
	}
	// 不带 key 过滤的真机输出里，YMIN/YLOW 与 YAVG 贴在一起 —— 只许取出那一个 YAVG。
	if got := ParseYAVG(realUnfilteredMetadata); len(got) != 1 || got[0] != 101.171 {
		t.Errorf("别的键被当成了 YAVG：%v（真机那 4 行里只有 1 个 YAVG=101.171）", got)
	}
}

// ② 统计：真机那 21 帧的画像必须逐项对得上（均值/最暗帧/95 分位/最大跳/大跳次数）。
//
// 期望值不是"看着像"，是把上面那段原话丢进同一套定义算出来的（2026-10-11）：
// 均值 77.813490、最暗 23.4305@f139（第 19 个样本）、95 分位 12.0252、最大跳 18.8247、≥3 的大跳 6 次。
func TestSummarizeLumaOnRealWindow(t *testing.T) {
	L := SummarizeLuma(ParseYAVG(realShot1YAVG))
	if L.Frames != 21 {
		t.Fatalf("帧数该是 21，实际 %d", L.Frames)
	}
	for _, c := range []struct {
		name string
		got  float64
		want float64
	}{
		{"Mean", L.Mean, 77.813490},
		{"Min", L.Min, 23.4305},
		{"P95Step", L.P95Step, 12.0252},
		{"MaxStep", L.MaxStep, 18.8247},
	} {
		if d := c.got - c.want; d > 0.0005 || d < -0.0005 {
			t.Errorf("%s = %.4f，真机那段该是 %.4f", c.name, c.got, c.want)
		}
	}
	if L.MinAt != 19 {
		t.Errorf("最暗帧该是第 19 个样本（真机 f139），实际 %d", L.MinAt)
	}
	if L.BigSteps != 6 {
		t.Errorf("≥%.0f 的大跳该是 6 次，实际 %d", lumaStepBig, L.BigSteps)
	}
	// P95 的下标口径（(n-1)*95/100，向下取整）也得对：21 帧 ⇒ 20 个差值 ⇒ 下标 18 ⇒ 12.0252。
	// 这里**再单独量一次差**（上面那轮只比数值）：口径改成"插值"或"取第 95 个百分位整数版"都会飘。
	if d := L.P95Step - 12.0252; d > 0.0005 || d < -0.0005 {
		t.Errorf("95 分位取的不是第 18 个差值（口径变了）：%.4f", L.P95Step)
	}
}

// ③ 判「闪」：**两批真机片段**的实测值逐条对（该报的报、不该报的不报），外加两条阈值的边界。
//
// 第一批＝平台的 16 条成品片段（`clips/shot_001..016.mp4`）；第二批＝ComfyUI 原档 22 条
// （`<ComfyUI>/output/manjux/*.mp4`，含同一镜的重出变体）。**两批都要过**，是因为阈值只在一批上
// 校准过（那是这一项当时的边界）—— 现在把第二批的真值也钉进门里。
//
// 第二批最有说服力的一条：镜 6 有三条变体 —— `_00001`（3.53/15）与 `_00002`（3.16/16）都判「闪」，
// 而最终留下的 `_00003`（1.64/2）不闪 ⇒ 判据与"平台自己重出到干净为止"这件事**对上了**。
func TestLumaFlickerMatchesRealClips(t *testing.T) {
	real := []struct {
		name        string
		p95, big    float64
		wantFlicker bool
	}{
		// 第一批：平台成品片段（16 条，逐帧 YAVG 直算）。
		{"clips/shot_001", 8.9332, 22, true},
		{"clips/shot_002", 0.8425, 4, false},
		{"clips/shot_003", 0.3244, 2, false},
		{"clips/shot_004", 0.9149, 2, false},
		{"clips/shot_005", 0.7269, 2, false},
		{"clips/shot_006", 1.6783, 2, false},
		{"clips/shot_007", 0.4012, 0, false},
		{"clips/shot_008", 0.9191, 2, false},
		{"clips/shot_009", 0.5691, 2, false},
		{"clips/shot_010", 0.6351, 2, false},
		{"clips/shot_011", 6.2709, 29, true},
		{"clips/shot_012", 0.5594, 2, false},
		{"clips/shot_013", 0.6562, 2, false},
		{"clips/shot_014", 0.7362, 2, false},
		{"clips/shot_015", 1.1322, 1, false},
		{"clips/shot_016", 0.9372, 1, false},
		// 第二批：ComfyUI 原档（22 条，含重出变体）—— 只列有代表性的 10 条（4 条判闪 + 6 条不闪）。
		{"raw/shot_1_00001_", 9.91, 26, true},
		{"raw/shot_6_00001_", 3.53, 15, true},
		{"raw/shot_6_00002_", 3.16, 16, true},
		{"raw/shot_6_00003_（重出到干净的那一条）", 1.64, 2, false},
		{"raw/shot_11_00001_", 6.21, 33, true},
		{"raw/shot_7_00001_", 0.40, 0, false},
		{"raw/shot_10_00001_", 0.63, 2, false},
		{"raw/shot_14_00002_", 0.96, 3, false},
		{"raw/shot_15_00001_", 1.13, 1, false},
		{"raw/shot_16_00001_", 0.94, 1, false},
	}
	for _, c := range real {
		got, why := LumaFlicker(ShotLuma{Frames: 200, P95Step: c.p95, BigSteps: int(c.big)})
		if got != c.wantFlicker {
			t.Errorf("%s（95 分位 %.4f、大跳 %.0f 次）判成了 %v，该是 %v", c.name, c.p95, c.big, got, c.wantFlicker)
		}
		if got && why == "" {
			t.Errorf("判「闪」却没说为什么（%s）—— 用户看不到依据", c.name)
		}
		if !got && why != "" {
			t.Errorf("没判「闪」却给了理由（%s）：%s", c.name, why)
		}
	}
	// 阈值边界：判据是「**超过**」（`>`），正好等于阈值的**不算**闪 —— 真机镜 3（大跳 2 次、
	// 95 分位 0.32）就在阈值下方，改动这里会让一批正常镜被误报。
	for _, c := range []struct {
		name  string
		L     ShotLuma
		want  bool
		whyZh string
	}{
		{"分位正好压线", ShotLuma{Frames: 200, P95Step: lumaFlickerP95, BigSteps: 0}, false, "等于阈值不算超"},
		{"分位刚过线", ShotLuma{Frames: 200, P95Step: lumaFlickerP95 + 0.01, BigSteps: 0}, true, "过线就该报"},
		{"次数正好压线", ShotLuma{Frames: 200, P95Step: 0, BigSteps: lumaFlickerBigCount}, false, "等于阈值不算超"},
		{"次数刚过线", ShotLuma{Frames: 200, P95Step: 0, BigSteps: lumaFlickerBigCount + 1}, true, "过线就该报"},
		{"只有一帧", ShotLuma{Frames: 1, P95Step: 99, BigSteps: 99}, false, "单帧量不出帧间差，不许报"},
	} {
		if got, _ := LumaFlicker(c.L); got != c.want {
			t.Errorf("%s：判成 %v，该是 %v（%s）", c.name, got, c.want, c.whyZh)
		}
	}
}

// realShots 真机第01集 16 镜的「场次 + 平均亮度」（场次 id 与 plan.json 逐字一致）。
func realShots() []project.Shot {
	scenes := []string{
		"scene_scrap_market", "scene_scrap_market", "scene_scrap_market", "scene_scrap_market", "scene_scrap_market",
		"scene_warehouse_dusk", "scene_warehouse_dusk", "scene_warehouse_dusk", "scene_warehouse_dusk", "scene_warehouse_dusk",
		"scene_warehouse_night", "scene_warehouse_night", "scene_warehouse_night", "scene_warehouse_night",
		"scene_dragon_bone_cavern", "scene_dragon_bone_cavern",
	}
	out := make([]project.Shot, len(scenes))
	for i, s := range scenes {
		out[i] = project.Shot{ID: i + 1, Scene: s, Duration: 8}
	}
	return out
}

// realMeans 真机 16 镜的平均亮度（逐帧 YAVG 直算，与 shot_00N.mp4 一一对应）。
var realMeans = []float64{88.5597, 73.1167, 66.5361, 92.1762, 67.7053, 63.1552, 72.1747, 91.7435, 86.327, 82.152, 52.9516, 62.1594, 66.355, 64.3827, 40.1896, 48.1049}

// realLuma 把真机那 16 镜拼成体检入参（黑场只有镜 11/15 有，与 blackdetect 报的一字不差）。
//
// 【2026-10-11 注】这两处（0.5417s / 0.5833s）**已经落在默认单段上限（0.6 秒）之内** ⇒ 按现口径是
// "渲染期本该被剪掉"；落盘的片段里还留着它们，说明那是剪之前渲的（镜 15 连 .noblack 标记都没有）。
// 体检因此走"去跑 clipfix"那一支，见 ⑤。
func realLuma() []ShotLuma {
	ls := make([]ShotLuma, len(realMeans))
	for i, m := range realMeans {
		ls[i] = ShotLuma{Frames: 200, Mean: m, Min: m, P95Step: 0.5}
	}
	// blackdetect 在真机 16 镜里只报了这两处（都在镜首）：镜 11 0.541667s、镜 15 0.583333s。
	ls[10].Blacks = []compose.BlackRun{{Start: 0, End: 0.541667}}
	ls[14].Blacks = []compose.BlackRun{{Start: 0, End: 0.583333}}
	return ls
}

// ④ 同场相邻镜亮度差：真机的 4 处超阈必须报，**跨场次的 2 处不许报**（夜戏接日戏本来就该不一样）。
//
// 这四条是本轮在真机上抓到的：镜 1→2（88.6→73.1，差 15.4）、3→4（66.5→92.2，差 25.6）、
// 4→5（92.2→67.7，差 24.5）、7→8（72.2→91.7，差 19.6）。
// 而 10→11（82.2→53.0，差 29.2）与 14→15（64.4→40.2，差 24.2）**换了场次**，一条都不许出现。
func TestLumaFindingsReportsSameSceneOnly(t *testing.T) {
	in := Input{Plan: &project.Plan{Shots: realShots()}, Luma: realLuma()}
	var pairs []string
	for _, f := range lumaFindings(in) {
		if strings.Contains(f.Msg, "平均亮度差") {
			pairs = append(pairs, f.Where)
		}
	}
	want := []string{"shot 1,2", "shot 3,4", "shot 4,5", "shot 7,8"}
	if strings.Join(pairs, " ") != strings.Join(want, " ") {
		t.Fatalf("同场相邻亮度差该报这 4 处 %v，实际 %v", want, pairs)
	}
	for _, bad := range []string{"shot 10,11", "shot 14,15"} {
		for _, got := range pairs {
			if got == bad {
				t.Errorf("%s 跨越了场次（夜戏接日戏），不该报 —— 那是内容不是故障", bad)
			}
		}
	}

	// 第二批（ComfyUI 原档）同一份方案的实测均值：**同一对镜、两次渲染，判据跟着数据走** ——
	// 原档里镜 1→2 只差 13.6（不报），而平台成品版是 15.4（报）。这条一起钉住，
	// 免得哪天有人把阈值改成"看着差不多"。
	rawMeans := []float64{86.71, 73.12, 66.54, 92.18, 67.71, 63.46, 72.17, 91.74, 86.33, 76.11, 52.05, 62.16, 66.35, 67.30, 40.19, 48.10}
	raw := make([]ShotLuma, len(rawMeans))
	for i, m := range rawMeans {
		raw[i] = ShotLuma{Frames: 200, Mean: m, Min: m, P95Step: 0.5}
	}
	var rawPairs []string
	for _, f := range lumaFindings(Input{Plan: &project.Plan{Shots: realShots()}, Luma: raw}) {
		if strings.Contains(f.Msg, "平均亮度差") {
			rawPairs = append(rawPairs, f.Where)
		}
	}
	if got, wantRaw := strings.Join(rawPairs, " "), "shot 3,4 shot 4,5 shot 7,8"; got != wantRaw {
		t.Errorf("原档那批该报 3 处（镜 1→2 只差 13.6，不该报）%q，实际 %q", wantRaw, got)
	}
}

// ⑤ 黑场：真机那两处（0.5417s / 0.5833s）**落在默认单段上限 0.6 秒之内** ⇒ 走"去跑 clipfix"那一支；
// 比默认更长的那种（真机这一集没有 >0.6s 的黑场 ⇒ 分支夹具）走"要么重出、要么显式放宽"那一支。
func TestLumaFindingsNamesBlackRunsWithComposeVerdict(t *testing.T) {
	blackMsgs := func(ls []ShotLuma) map[string]string {
		got := map[string]string{}
		for _, f := range lumaFindings(Input{Plan: &project.Plan{Shots: realShots()}, Luma: ls}) {
			if strings.Contains(f.Msg, "纯黑") {
				got[f.Where] = f.Msg
			}
		}
		return got
	}
	// 真机 16 镜里只有镜 11/15 有纯黑（blackdetect 报的）。
	real := blackMsgs(realLuma())
	if len(real) != 2 || real["shot 11"] == "" || real["shot 15"] == "" {
		t.Fatalf("真机 16 镜里只有镜 11/15 有纯黑，实际报了 %v", real)
	}
	// 理由串必须**就是** compose 那条口径给的，而且必须是「够短 ⇒ 本该剪掉」那一支：
	// 把 compose 的单段上限调回 0.5 秒，这两段就翻成「太长 ⇒ 不会剪」⇒ 这道门必须变红。
	// （原来这里断言的是子串 "0.5" —— 播报里的 "0.54s" 正好把它蒙过去，反向验证当场咬出那处摆设。）
	for i, r := range []compose.BlackRun{{Start: 0, End: 0.541667}, {Start: 0, End: 0.583333}} {
		who := []string{"shot 11", "shot 15"}[i]
		msg := real[who]
		strippable, why := compose.BlackRunVerdict(r)
		if !strippable {
			t.Fatalf("compose 把 %.2fs 判成「不会自动剪」—— 默认单段上限（%.2fs）该把它剪掉",
				r.Dur(), compose.MaxBlackRunSec)
		}
		if why == "" {
			t.Fatal("compose.BlackRunVerdict 没给理由 —— 体检就只能编一句")
		}
		if !strings.Contains(msg, why) {
			t.Errorf("%s 的黑场理由不是 compose 那条口径给的\n  实际：%s\n  该含：%s", who, msg, why)
		}
		// 这一支的话术：平台本该剪掉它 ⇒ 给一条能直接粘的命令。不许要用户放宽上限（那等于说默认坏了），
		// 也不许让他重出这一镜（那不是引擎的问题，是没剪）。
		if !strings.Contains(msg, "cmd/clipfix") || !strings.Contains(msg, "-apply") {
			t.Errorf("%s 该告诉用户跑一次 clipfix 就能修好：%s", who, msg)
		}
		if strings.Contains(msg, "-max-black") {
			t.Errorf("%s 落在默认上限之内，不该要用户放宽上限：%s", who, msg)
		}
		if strings.Contains(msg, "重出这一镜") {
			t.Errorf("%s 不该让用户重出（那不是引擎的问题，是没剪）：%s", who, msg)
		}
	}

	// 短黑闪（真机 2026-10-10 镜 1：black_start:5.291667 black_end:5.458333，0.1667s）走的是同一支。
	ls := realLuma()
	ls[0].Blacks = []compose.BlackRun{{Start: 5.291667, End: 5.458333}}
	msg := blackMsgs(ls)["shot 1"]
	if msg == "" {
		t.Fatal("短黑闪没被报出来（真机镜 1 那一段就是这么漏掉的）")
	}
	if !strings.Contains(msg, "clipfix") {
		t.Errorf("短黑闪该告诉用户去跑 clipfix（平台本该剪掉它），实际：%s", msg)
	}
	if strings.Contains(msg, "-max-black") {
		t.Errorf("短黑闪不该要用户放宽上限（平台本来就该剪掉它），实际：%s", msg)
	}

	// 比默认单段上限更长的段（**分支夹具**：真机这一集没有 >0.6s 的黑场，最长的是 0.5833s）⇒
	// 走"显式放宽"那一支（上限往上报一格：0.8167 ⇒ 0.9）。
	ls = realLuma()
	ls[0].Blacks = []compose.BlackRun{{Start: 0, End: 0.816667}}
	msg = blackMsgs(ls)["shot 1"]
	if msg == "" {
		t.Fatal("0.82s 的黑段没被报出来")
	}
	if !strings.Contains(msg, "-max-black 0.9") || !strings.Contains(msg, "cmd/clipfix") {
		t.Errorf("比默认上限长的黑段该给显式放宽到 0.9 秒的命令：%s", msg)
	}
	// 超过**累计**上限的段（真机没遇到，但形态合法）：平台任何档位都剪不了 ⇒ 不许给那条命令（给了也剪不掉）。
	ls = realLuma()
	ls[0].Blacks = []compose.BlackRun{{Start: 0, End: 1.2}} // 1.2s > 累计上限 1.0s
	var huge string
	for _, f := range lumaFindings(Input{Plan: &project.Plan{Shots: realShots()}, Luma: ls}) {
		if f.Where == "shot 1" && strings.Contains(f.Msg, "纯黑") {
			huge = f.Msg
		}
	}
	if huge == "" {
		t.Fatal("1.2 秒的黑段没被报出来")
	}
	if strings.Contains(huge, "-max-black") {
		t.Errorf("这一段超过**累计**上限（%.1f 秒），平台任何档位都剪不了 —— 不该给 -max-black 那条命令（给了也剪不掉）：%s",
			compose.MaxBlackTotalSec, huge)
	}
	if !strings.Contains(huge, "累计") {
		t.Errorf("超累计上限的那一段该说清是累计上限卡住的：%s", huge)
	}
}

// ⑥ 三态：没量到 ⇒ 一条都不报（也不许编一条「合格」）。
func TestLumaFindingsIsSilentWhenNotMeasured(t *testing.T) {
	// 方案本身就是"到处都超标"的那份（真机 16 镜）：只有把量到的画像撤掉，才说明沉默来自三态。
	empty := Input{Plan: &project.Plan{Shots: realShots()}}
	if got := lumaFindings(empty); len(got) != 0 {
		t.Fatalf("一镜都没量到时不该有任何结论，实际 %d 条：%v", len(got), got)
	}
	// 只量到一半：量到的那几镜照报，没量到的镜一个字不提。
	half := realLuma()
	half[0].Blacks = nil
	half[10] = ShotLuma{} // 镜 11 没量到（Frames=0 ⇒ lumaAt 判 false）
	half[14] = ShotLuma{}
	for _, f := range lumaFindings(Input{Plan: &project.Plan{Shots: realShots()}, Luma: half}) {
		if f.Where == "shot 11" || f.Where == "shot 15" {
			t.Errorf("没量到的镜 %s 被报了 —— 没验过不许当成不合格（三态）：%s", f.Where, f.Msg)
		}
	}
}

// ⑦ 量尺本身：全量不到 ⇒ 返回 nil（调用方据此说"这次没量"），量不到的镜**点名**。
func TestMeasureLumaIsThreeState(t *testing.T) {
	clips := []string{"a.mp4", "b.mp4", "c.mp4"}
	if got, missing := MeasureLuma(nil, clips); got != nil || len(missing) != 3 {
		t.Errorf("没有量尺时该回 (nil, 三镜全缺)，实际 (%v, %v)", got, missing)
	}
	ok := func(string) (ShotLuma, error) { return ShotLuma{Frames: 200, Mean: 50}, nil }
	got, missing := MeasureLuma(ok, clips)
	if len(got) != 3 || len(missing) != 0 {
		t.Errorf("三镜都量到 ⇒ 不该有缺失项，实际 missing=%v", missing)
	}
	// 一半失败（解码失败 / 路径空）= 半个三态：量到的照用，量不到的点名。
	half := func(p string) (ShotLuma, error) {
		if p == "b.mp4" {
			return ShotLuma{}, errStub{}
		}
		return ShotLuma{Frames: 200, Mean: 50}, nil
	}
	got, missing = MeasureLuma(half, clips)
	if len(got) != 3 || len(missing) != 1 || missing[0] != 1 {
		t.Errorf("只有 b.mp4 该算没量到，实际 missing=%v", missing)
	}
	if _, missing = MeasureLuma(ok, []string{"a.mp4", "", "c.mp4"}); len(missing) != 1 || missing[0] != 1 {
		t.Errorf("空路径（占位镜）也算没量到，实际 missing=%v", missing)
	}
}

type errStub struct{}

func (errStub) Error() string { return "stub decode failure" }

// ⑧ 口径唯一：黑场那条滤镜串只能从 compose 拿（两处各写一遍迟早一严一松）。
func TestBlackDetectFilterComesFromCompose(t *testing.T) {
	src := lumaSrc(t)
	if !strings.Contains(src, "compose.BlackDetectFilter()") {
		t.Error("黑场用的不是 compose 那条滤镜串 —— 体检与渲染期黑闪切除会各判一套口径")
	}
	if strings.Contains(src, "blackdetect=d=") {
		t.Error("luma.go 里又写了一遍 blackdetect 滤镜串 —— 该走 compose.BlackDetectFilter()（唯一出处）")
	}
	if !strings.Contains(src, "compose.ParseBlackRuns(") {
		t.Error("黑场日志不是用 compose 的解析器读的 —— 两处解析迟早分叉")
	}
	if !strings.Contains(src, "compose.BlackRunVerdict(") {
		t.Error("黑场结论没问 compose 的口径 —— 阈值会变成第二份")
	}
	// 量尺必须跑在 **info 级别**：blackdetect 的日志行是在 info 级别写的，`-v error` 会把它
	// 整段吞掉（2026-10-11 实测：同一条命令 `-v error` 拿到 0 行 black_start、`-v info` 拿到 1 行）——
	// 那样黑场那一项会**永远"没问题"**，正是"看着有门、实际什么都没拦"的形态。
	if !strings.Contains(src, "\"-v\", \"info\"") {
		t.Error("量尺没跑在 info 级别 —— blackdetect 的日志会被吞掉，黑场永远报不出来")
	}
}

// lumaSrc 读 luma.go 的源码（口径门要咬「这一行到底怎么写的」，不是「文件里提到过这几个字」）。
func lumaSrc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("luma.go")
	if err != nil {
		t.Fatalf("读不到 luma.go：%v", err)
	}
	return string(b)
}
