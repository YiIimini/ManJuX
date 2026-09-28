// mode.go 作者口径的**模式常量**：分镜是「渲染风格 × 片段时长上限」的正交格子。
//
// ## 来源与纪律
//
// 全部数值取自 `D:\Ai\Datas\漫剧制作` 的 11 份参考文件，**逐条落数值、逐条带行号溯源**；
// 完整口径表见 `docs/漫剧制作口径总表.md`。
//
// 【为什么必须变成常量】用户当天质疑「严重怀疑你每次都没有深度去分析这个目录里的文档内容」——
// 事实是口径只留在我的回复和脑子里，改提示词的人（包括下一次的我）还会凭印象拍参数。
// 变成常量 + 契约门之后，"读过文档"才是可执行、可回归的东西；改一个数就要说清出处。
//
// 【文档里的两处口径冲突】**本文件不替作者选**，而是在下面的常量注释里点名，
// 用 `TODO(口径)` 标出，等作者拍板（见 docs 总表 §四）。
package director

import (
	"fmt"

	"github.com/YiIimini/ManJuX/internal/project"
)

// StoryboardStyle 分镜的渲染风格轴。它改的不只是质感词 —— 仿真人档**删掉了
// 【全局风格】与【转场说明】两个字段**，负向词也多出"塑料感、虚假光泽"。
type StoryboardStyle string

const (
	Style3DCG    StoryboardStyle = "3d_cg"    // 3D CG 国漫（【全局风格】固定串在）
	StyleSimReal StoryboardStyle = "sim_real" // 写实仿真人
)

// StoryboardMode 一格分镜模式（风格 × 片段时长上限）。
//
// 所有秒数都按文档原值；`Src` 记的是文件 + 行号，改参数时先回去看那几行。
type StoryboardMode struct {
	ID    string
	Label string
	Style StoryboardStyle
	Src   string // 出处（文件 + 行号）

	// ── 片段级 ──────────────────────────────────────────────
	ClipMax    float64   // 单片段上限（引擎一次生成的最长时长）
	ClipMin    float64   // 片段下限（**取规则段的值**，见文末 TODO(口径) ①）
	ClipPref   []float64 // 非末尾片段的优先取值
	SceneUnder float64   // 整场景总时长低于它就只出一个片段
	LineSplit  float64   // 连续台词超过它就拆片段

	// ── 镜头级 ──────────────────────────────────────────────
	ShotMin    float64 // 单镜头建议下限
	ShotMax    float64 // 单镜头建议上限
	ShotFast   float64 // 极快闪切的下限
	ShotSlow   float64 // 情绪极致渲染的上限
	LineExtend float64 // 镜内台词超过它，镜头可延长到读完
	Freeze     float64 // 无自然停顿时的"定格"缓冲上限
	Overlap    float64 // 运动切割的重叠补偿（重复上一片段结尾的秒数）

	// ── 该档是否带"叙事节奏约束"（只有 30 秒档有，见 docs 总表）──
	NarrativeRhythm bool
}

// 四格 + 30 秒测试档。数值逐条对过原文件，行号可回查。
var (
	// 3D CG 国漫 × 10 秒 —— 平台主用档（渲染端 H3 一次生成 10 秒）。
	Mode3D10 = StoryboardMode{
		ID: "3d10", Label: "3D 国漫 · 10 秒", Style: Style3DCG,
		Src:     "五、最新豆包十秒指令.txt L2/L9/L10/L11",
		ClipMax: 10, ClipMin: 4, ClipPref: []float64{8, 9, 10}, SceneUnder: 8, LineSplit: 10,
		ShotMin: 1.5, ShotMax: 5, ShotFast: 1.2, ShotSlow: 6, LineExtend: 5, Freeze: 1, Overlap: 2,
	}
	// 3D CG 国漫 × 15 秒。
	Mode3D15 = StoryboardMode{
		ID: "3d15", Label: "3D 国漫 · 15 秒", Style: Style3DCG,
		Src:     "五、3D漫剧15秒分镜指令.txt L2/L8-L11",
		ClipMax: 15, ClipMin: 4, ClipPref: []float64{12, 13, 14, 15}, SceneUnder: 12, LineSplit: 15,
		ShotMin: 1.5, ShotMax: 5, ShotFast: 1.2, ShotSlow: 6, LineExtend: 5, Freeze: 1, Overlap: 2,
	}
	// 写实仿真人 × 10 秒 —— **不是"3D10 换个质感词"**：数值整体更紧（镜头 1.5-4、
	// 台词延长阈值 4 秒、非末片段 7-10、整场景阈值 <10），且删掉两个字段。
	//
	// ⚠ **这一格的数值不在这里定义**：平台的真源是 `project.Plan*`
	// （plan_contract.go / shot_table.go，那里每条都带出处与 DEV 分歧登记）。
	// 本文件只**引用**它 —— 2026-09-28 审计发现"平台早就有 Plan* 一套口径，而我又新建了
	// mode.go"，那正是用户要消灭的口径散落。两边由 mode_test.go 的
	// TestSim10MirrorsProjectConstants 钉住，谁改一边就红。
	ModeSim10 = StoryboardMode{
		ID: "sim10", Label: "仿真人 · 10 秒", Style: StyleSimReal,
		Src:        "【额外】仿真人10S分镜头指令.txt L2/L10/L11/L26/L27（数值真源见 project.Plan*）",
		ClipMax:    project.PlanMaxShotSeconds,
		ClipMin:    project.PlanMinShotSeconds,
		ClipPref:   []float64{project.PlanPreferredShotSeconds, 8, 9, 10},
		SceneUnder: 10, LineSplit: 10,
		ShotMin: 1.5, ShotMax: float64(project.PlanOneTakeSeconds), ShotFast: 1.2, ShotSlow: 5,
		LineExtend: 4, Freeze: 1, Overlap: 2,
	}
	// 写实仿真人 × 15 秒。
	ModeSim15 = StoryboardMode{
		ID: "sim15", Label: "仿真人 · 15 秒", Style: StyleSimReal,
		Src:     "【额外】即梦仿真人15S分镜头指令.txt L2/L10/L11/L26/L27",
		ClipMax: 15, ClipMin: 4, ClipPref: []float64{12, 13, 14, 15}, SceneUnder: 15, LineSplit: 15,
		ShotMin: 1.5, ShotMax: 5, ShotFast: 1.2, ShotSlow: 6, LineExtend: 5, Freeze: 1, Overlap: 2,
	}
	// 30 秒测试档 —— **唯一带叙事节奏约束的一档**（黄金比例、首句台词锚点、无台词段上限…）。
	ModeTest30 = StoryboardMode{
		ID: "test30", Label: "测试版 · 30 秒", Style: Style3DCG,
		Src:     "【额外】测试版30S分镜指令.txt L2/L10/L11/L26",
		ClipMax: 30, ClipMin: 4, ClipPref: []float64{25, 26, 27, 28, 29, 30}, SceneUnder: 20, LineSplit: 30,
		ShotMin: 1.5, ShotMax: 6, ShotFast: 1.2, ShotSlow: 8, LineExtend: 8, Freeze: 1, Overlap: 2,
		NarrativeRhythm: true,
	}
)

// AllModes 全部模式（供遍历式契约门与前端下拉用）。
var AllModes = []StoryboardMode{Mode3D10, Mode3D15, ModeSim10, ModeSim15, ModeTest30}

// NarrativeRhythm30 30 秒档独有的**叙事节奏约束**（10/15 秒档一条都没有）。
//
// 这是全目录里最该被"按档缩放"移植到短档的一组量化规则 ——
// 作者反复说过"拖沓"，而抓手一直在这里（见 docs 总表 §二·⑤′）。
//
// ## 现状与去向（2026-09-29 审计 #6，**结论写在这里免得下次又当成"已落地"**）
//
// 九项里**五项已经落地**，只是落地成 `plan_contract.go` 的手写常量而不是引用本表：
// 首句台词 12 秒、锚点插入 8–12 秒、连续无台词上限、核心情绪镜头 ≥5 秒、
// 以及"前 30 秒必须给一次情绪释放"。**两套常量说同一件事**本身是债
// （改一处漏一处），但那是"收敛常量"的活，不是"补功能"。
//
// **两项没有落地，而且是判不了的**：
//
//	· `SetupRatio` / `CoreRatio` / `TailRatio`（铺垫 / 核心 / 余韵的时长占比）——
//	  要判它，镜头必须先有"段位"这个字段（这一镜属于铺垫还是核心？）。
//	  现在没有；**不靠 `HookType` 去猜**（两者不是一回事：一个 `reveal` 可以落在铺垫段）。
//	· `LongTakeSplitMeter`（长跟随运镜跨度超 30 米就拆镜）——
//	  要判它得有"这一镜里摄影机移动了多少米"，而平台的结构里**根本没有距离量**，
//	  只在正文散文里可能有。靠文本正则去捞"移动了多少米"是假判据。
//
// 所以本表的**真实用途**是：记录 30 秒档的原口径（`Src` 指向用户文件），
// 供以后真做"段位字段"或"运镜距离"时按图施工。**不要**把它当成"已经生效的规则"引用。
type NarrativeRhythm struct {
	FirstLineWithin    float64 // 首句有意义的台词必须在此秒数内出现
	AnchorInsertFrom   float64 // 否则在这个区间插锚点台词
	AnchorInsertTo     float64
	MaxSilentRun       float64    // 连续无台词段落上限
	CoreEmotionMin     float64    // 核心情绪镜头下限
	SetupRatio         [2]float64 // 铺垫占比区间
	CoreRatio          [2]float64 // 核心台词与互动占比区间
	TailRatio          [2]float64 // 情绪反应与余韵占比区间
	LongTakeSplitMeter float64    // 长跟随运镜起止点跨度超过此米数就拆镜头
	Src                string
}

var Rhythm30 = NarrativeRhythm{
	FirstLineWithin: 12, AnchorInsertFrom: 8, AnchorInsertTo: 12, MaxSilentRun: 12, CoreEmotionMin: 5,
	SetupRatio: [2]float64{0.25, 0.35}, CoreRatio: [2]float64{0.40, 0.50}, TailRatio: [2]float64{0.20, 0.30},
	LongTakeSplitMeter: 30,
	Src:                "【额外】测试版30S分镜指令.txt（节奏约束段）",
}

// SpeechRate 情绪档 → 字/秒。
type SpeechRate struct {
	Emotion  string
	Min, Max float64
}

// SpeechRates 语速五档 —— **五份分镜文档逐字相同**，是全目录最统一的一条口径，
// 也是"一句台词要占几秒"的唯一算法依据（镜头时长 = MAX(台词物理时长, 动作时长)）。
var SpeechRates = []SpeechRate{
	{"正常叙事/平静对话", 3.5, 4.5},
	{"轻松/日常/调侃", 4, 5},
	{"愤怒/激动/争吵", 5, 6.5},
	{"低沉/压抑/哽咽/犹豫", 2.5, 3.5},
	{"威严/宣告/战斗呐喊", 3, 4},
}

// SpeechRateOf 按档位名字取语速（找不到时返回正常叙事档，不 panic）。
func SpeechRateOf(emotion string) SpeechRate {
	for _, r := range SpeechRates {
		if r.Emotion == emotion {
			return r
		}
	}
	return SpeechRates[0]
}

// DialogueSeconds 一段台词按该情绪档朗读所需的**最短**秒数。
//
// 用该档的**最慢**语速（Min）算：口径反复强调"绝不允许加速台词或切碎台词"，
// 台词必须读得完，所以估时取保守值。
func DialogueSeconds(r SpeechRate, runes int) float64 {
	if runes <= 0 {
		return 0
	}
	return float64(runes) / r.Min
}

// SumSeconds 一批镜头的时长之和。口径要求它**精确等于**片段时长
// （五份分镜文档都有这条硬约束，见 SumMatchesClip）。
func SumSeconds(durations []float64) float64 {
	var sum float64
	for _, d := range durations {
		sum += d
	}
	return sum
}

// SumMatchesClip 镜头时长之和是否与片段时长一致（口径硬约束，容差 0.05 秒用于小数秒的浮点误差）。
func SumMatchesClip(durations []float64, clipSeconds float64) bool {
	diff := SumSeconds(durations) - clipSeconds
	if diff < 0 {
		diff = -diff
	}
	return diff <= 0.05
}

// Validate 模式自洽（供契约门与前端调参时兜底）。
func (m StoryboardMode) Validate() error {
	switch {
	case m.ClipMin <= 0 || m.ClipMax <= 0:
		return fmt.Errorf("%s: 片段时长必须为正（%v-%v）", m.ID, m.ClipMin, m.ClipMax)
	case m.ClipMin > m.ClipMax:
		return fmt.Errorf("%s: 片段下限 %v 大于上限 %v", m.ID, m.ClipMin, m.ClipMax)
	case m.ShotMin <= 0 || m.ShotMax <= 0:
		return fmt.Errorf("%s: 镜头时长必须为正（%v-%v）", m.ID, m.ShotMin, m.ShotMax)
	case m.ShotMin > m.ShotMax:
		return fmt.Errorf("%s: 镜头下限 %v 大于上限 %v", m.ID, m.ShotMin, m.ShotMax)
	case m.ShotFast > m.ShotMin:
		return fmt.Errorf("%s: 极快闪切下限 %v 不该高于常规下限 %v", m.ID, m.ShotFast, m.ShotMin)
	case m.ShotSlow < m.ShotMax:
		return fmt.Errorf("%s: 情绪放宽上限 %v 不该低于常规上限 %v", m.ID, m.ShotSlow, m.ShotMax)
	case m.SceneUnder > m.ClipMax:
		return fmt.Errorf("%s: 单片段阈值 %v 不该超过片段上限 %v", m.ID, m.SceneUnder, m.ClipMax)
	}
	for _, p := range m.ClipPref {
		if p < m.ClipMin || p > m.ClipMax {
			return fmt.Errorf("%s: 优先时长 %v 落在区间 %v-%v 之外", m.ID, p, m.ClipMin, m.ClipMax)
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// TODO(口径)：文档里有两处冲突，**留给作者拍板**，此处不替选（详见 docs 总表 §四）
//
// ① 仿真人两份**文件内部自相矛盾**：使用说明写「1-10 秒 / 1-15 秒」，
//    而【片段时长规则】段写「必须 4 秒～10 秒 / 4 秒～15 秒」。
//    本文件取的是**规则段**的 4 秒（与 3D 两档一致）；若作者要 1 秒，改 ClipMin。
//
// ② 「整场景总时长不足 → 只出一个片段」的阈值四档各不相同：
//    3D·10s <8、3D·15s <12（都是"上限 − 2 秒"）；仿真人·10s <10、仿真人·15s <15
//    （都等于上限本身）。本文件按各自文件原值填，未统一。
// ─────────────────────────────────────────────────────────────
