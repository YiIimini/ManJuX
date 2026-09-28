package director

import (
	"github.com/YiIimini/ManJuX/internal/project"
	"math"
	"testing"
)

// 口径常量的契约门。
//
// 【为什么要有这些门】用户的质疑是「严重怀疑你没有深度分析这些文档」——
// 把口径写进常量还不够：得让**改错一个数**当场变红，否则下一次改提示词的人（包括我）
// 还是会凭印象拍参数，而"读过文档"又变成一句空话。

// 五格模式必须各自自洽（含：极快下限 ≤ 常规下限 ≤ 常规上限 ≤ 情绪放宽上限）。
func TestStoryboardModesAreSelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range AllModes {
		if seen[m.ID] {
			t.Fatalf("模式 id 重复：%s", m.ID)
		}
		seen[m.ID] = true
		if m.Src == "" {
			t.Errorf("%s：每条口径都要能回查出处（Src 不能空）", m.ID)
		}
		if err := m.Validate(); err != nil {
			t.Errorf("%s 不自洽：%v", m.ID, err)
		}
	}
	if len(AllModes) != 5 {
		t.Fatalf("参考目录里有 5 格分镜（3d10/3d15/sim10/sim15/test30），实际 %d 格", len(AllModes))
	}
}

// 风格轴不只是质感词：仿真人档删掉两个字段、镜头区间更紧。
//
// 这条钉住的是**容易被当成"只换风格词"**的那一批差异（子代理逐行核对时挑出来的）。
func TestSimRealTightensNumbersNotJustWording(t *testing.T) {
	if ModeSim10.ShotMax >= Mode3D10.ShotMax {
		t.Errorf("仿真人 10S 的单镜上限应比 3D 10S 更紧（1.5-4 vs 1.5-5），实际 %.1f vs %.1f",
			ModeSim10.ShotMax, Mode3D10.ShotMax)
	}
	if ModeSim10.LineExtend >= Mode3D10.LineExtend {
		t.Errorf("仿真人 10S 的台词延长阈值应更小（4 vs 5 秒），实际 %.1f vs %.1f",
			ModeSim10.LineExtend, Mode3D10.LineExtend)
	}
	if ModeSim10.SceneUnder <= Mode3D10.SceneUnder {
		t.Errorf("仿真人 10S 的整场景单片段阈值应更大（<10 vs <8 秒），实际 %.1f vs %.1f",
			ModeSim10.SceneUnder, Mode3D10.SceneUnder)
	}
	// 非末片段优先值也不同：3D 是 8/9/10，仿真人是 7/8/9/10
	if len(ModeSim10.ClipPref) != 4 || len(Mode3D10.ClipPref) != 3 {
		t.Errorf("非末片段优先取值应分别为 7-10 与 8-10，实际 %v / %v",
			ModeSim10.ClipPref, Mode3D10.ClipPref)
	}
}

// 只有 30 秒档带叙事节奏约束 —— 这正是治"拖沓"的量化抓手，别让谁顺手给它抹平。
func TestOnlyTest30CarriesNarrativeRhythm(t *testing.T) {
	for _, m := range AllModes {
		want := m.ID == "test30"
		if m.NarrativeRhythm != want {
			t.Errorf("%s 的 NarrativeRhythm 应为 %v（只有 30 秒档有那六条约束）", m.ID, want)
		}
	}
	// 黄金比例必须加起来是 1（铺垫 + 核心 + 余韵）
	sum := Rhythm30.SetupRatio[0] + Rhythm30.CoreRatio[0] + Rhythm30.TailRatio[0]
	if math.Abs(sum-0.9) > 0.11 { // 下界 0.25+0.40+0.20 = 0.85，上界 0.35+0.50+0.30 = 1.15
		t.Errorf("黄金比例的三段占比区间应互相覆盖，实际下界之和 %.2f", sum)
	}
	if Rhythm30.FirstLineWithin <= 0 || Rhythm30.MaxSilentRun <= 0 || Rhythm30.CoreEmotionMin <= 0 {
		t.Error("节奏约束的六条不能被清零（它们就是治拖沓的抓手）")
	}
}

// 语速五档是"一句台词占几秒"的唯一算法依据；五份文档逐字相同，不许被改。
func TestSpeechRatesMatchTheDocs(t *testing.T) {
	want := []struct {
		emotion  string
		min, max float64
	}{
		{"正常叙事/平静对话", 3.5, 4.5},
		{"轻松/日常/调侃", 4, 5},
		{"愤怒/激动/争吵", 5, 6.5},
		{"低沉/压抑/哽咽/犹豫", 2.5, 3.5},
		{"威严/宣告/战斗呐喊", 3, 4},
	}
	if len(SpeechRates) != len(want) {
		t.Fatalf("语速应恰好五档，实际 %d 档", len(SpeechRates))
	}
	for i, w := range want {
		got := SpeechRates[i]
		if got.Emotion != w.emotion || got.Min != w.min || got.Max != w.max {
			t.Errorf("第 %d 档应为 %s %.1f-%.1f，实际 %s %.1f-%.1f",
				i, w.emotion, w.min, w.max, got.Emotion, got.Min, got.Max)
		}
	}
	// 找不到档位时退回"正常叙事"，不 panic（调用方可能拿到脏情绪串）
	if r := SpeechRateOf("不存在的情绪"); r.Emotion != SpeechRates[0].Emotion {
		t.Errorf("未知情绪应退回正常叙事档，实际 %q", r.Emotion)
	}
}

// 原创路线的「单段台词 ≤15 字」必须正好装进镜头时长区间 —— 两条口径要能对上账。
//
// 作者《一、高品质AI原创小说指令》要求人物每段对话 ≤15 字；分镜口径要求含台词的镜头
// 时长 ≥ 台词读完时间、且单镜常规 1.5-5 秒。这两条如果对不上，就说明有一边的口径被改了。
func TestDialogueBudgetFitsShotWindow(t *testing.T) {
	const maxLineRunes = 15 // 《一、高品质AI原创小说指令》：人物每段对话不超过 15 字
	normal := SpeechRateOf("正常叙事/平静对话")
	sec := DialogueSeconds(normal, maxLineRunes)
	if sec < 3.3 || sec > 4.4 { // 15 ÷ 4.5 = 3.33；15 ÷ 3.5 = 4.29
		t.Errorf("15 字按正常语速应占 3.3-4.4 秒，实际 %.2f 秒", sec)
	}
	if sec > Mode3D10.ShotMax {
		t.Errorf("15 字台词（%.2f 秒）超过了单镜上限 %.1f 秒 —— 台词与镜头两条口径对不上账",
			sec, Mode3D10.ShotMax)
	}
	// 最慢的一档（低沉压抑 2.5 字/秒）下，15 字会顶到 6 秒 —— 超过常规上限，
	// 这正是"台词主导镜头可延长"那条规则存在的理由。
	slow := SpeechRateOf("低沉/压抑/哽咽/犹豫")
	if got := DialogueSeconds(slow, maxLineRunes); got <= Mode3D10.ShotMax {
		t.Errorf("低沉档 15 字应超过常规镜头上限（说明确实需要延长规则），实际 %.2f 秒", got)
	}
	if DialogueSeconds(slow, 0) != 0 {
		t.Error("空台词应返回 0 秒")
	}
}

// 仿真人 10S 那一格的数值**必须与 project.Plan* 完全一致**。
//
// 【为什么这条门最关键】2026-09-28 审计发现：平台早就有 `project.Plan*` 一套口径常量
// （plan_contract.go / shot_table.go，每条带出处与 DEV 分歧登记），而我这一轮又新建了
// mode.go —— **两套并行口径**，正是用户要消灭的口径散落。
// 现在 mode.go 引用 project 的值，这道门负责钉住"引用不许退化成各写一遍"。
func TestSim10MirrorsProjectConstants(t *testing.T) {
	if ModeSim10.ClipMax != project.PlanMaxShotSeconds {
		t.Errorf("片段上限应为 project.PlanMaxShotSeconds=%v，实际 %v（两套口径又分叉了）",
			project.PlanMaxShotSeconds, ModeSim10.ClipMax)
	}
	if ModeSim10.ClipMin != project.PlanMinShotSeconds {
		t.Errorf("片段下限应为 project.PlanMinShotSeconds=%v，实际 %v",
			project.PlanMinShotSeconds, ModeSim10.ClipMin)
	}
	if ModeSim10.ShotMax != float64(project.PlanOneTakeSeconds) {
		t.Errorf("单镜（内部切镜）上限应为 project.PlanOneTakeSeconds=%v，实际 %v",
			project.PlanOneTakeSeconds, ModeSim10.ShotMax)
	}
	if len(ModeSim10.ClipPref) == 0 || ModeSim10.ClipPref[0] != project.PlanPreferredShotSeconds {
		t.Errorf("非末片段的首选时长应为 project.PlanPreferredShotSeconds=%v，实际 %v",
			project.PlanPreferredShotSeconds, ModeSim10.ClipPref)
	}
}

// 用**文档里的真实示例**验时长守恒（口径：片段内所有镜头时长之和必须精确等于片段时长）。
//
// 夹具直接取自参考文件，不是我自己编的数字：
//   - 10 秒档示例：片段 10 秒 = 4 镜头 × 2.5 秒
//   - 15 秒档示例：片段 15 秒 = 镜头 0-3 / 3-6 / 6-9.5 / 9.5-12 / 12-15
func TestSumMatchesClipOnRealExamples(t *testing.T) {
	// 10 秒档示例（仿真人 10S 完整示例）
	ten := []float64{2.5, 2.5, 2.5, 2.5}
	if !SumMatchesClip(ten, 10) {
		t.Errorf("10 秒片段 = 4×2.5 秒应守恒，实际和 %.2f", SumSeconds(ten))
	}
	if SumSeconds(ten) != 10 {
		t.Errorf("求和应为 10，实际 %.2f", SumSeconds(ten))
	}

	// 15 秒档示例（3D 漫剧 15 秒完整示例）：3 / 3 / 3.5 / 2.5 / 3
	fifteen := []float64{3, 3, 3.5, 2.5, 3}
	if !SumMatchesClip(fifteen, 15) {
		t.Errorf("15 秒片段 = 3+3+3.5+2.5+3 应守恒，实际和 %.2f", SumSeconds(fifteen))
	}

	// 20 段累计误差不该被容差放过（容差只服务小数秒的浮点误差）
	drift := []float64{0.5, 0.5, 0.5}
	if SumMatchesClip(drift, 10) {
		t.Error("差 8.5 秒不该判为守恒 —— 容差只服务小数秒的浮点误差")
	}
	// 小数秒的正常误差要放行
	if !SumMatchesClip([]float64{3.3, 3.3, 3.4}, 10) {
		t.Error("3.3+3.3+3.4 = 10.0 应判守恒")
	}
}
