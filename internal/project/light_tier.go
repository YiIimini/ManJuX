package project

import "strings"

// light_tier.go 场景的**时间档**：白天 / 暗光 / 夜里。
//
// 【为什么会有这个文件（2026-10-08 真机《顺其自然（真实事迹短片）》的代价）】
// 原来判"是不是夜戏"用一张词表：`夜 / night / 月 / moon / 烛 / candle / 灯 / lamp / lantern`。
// 其中 **「月」与「灯」不是时间词** —— 一个是月份、一个是道具。于是真机那本书的 7 个场景里
// **5 个被判成夜戏**，全都写明白天：
//
//	老城邮局营业厅   清晨七点四十…柜台**暖橙吊灯**作补光        → 夜（错）
//	慈善总会财务窗口 次日上午…**台灯**暖光为次光                 → 夜（错）
//	慈善总会档案室   今年十一月**底**…**台灯**暖光为次光          → 夜（错，月+灯）
//	城市地铁站入口   冬日午后…站内暖橙**灯**为次光                → 夜（错）
//	街口募捐点       1999年12月**傍晚**…**路灯**暖黄光            → 夜（傍晚算"暗"可以，但这不是夜）
//
// 后果是**渲染提示词里被注入**：
//
//	"This is a NIGHT scene: a visible in-frame light source must be present …"
//
// ⇒ 这些**白天场景**被模型按夜景渲 ⇒ 真机成片里 4 镜被判黑屏（其中一镜实测 100% 暗）、
// 两句台词整镜没念（暗画面里模型干脆不说话）。用户报「视频看的莫名其妙的」。
//
// 判据改成**只认时间词**，并且分三档（夜 / 暗光 / 白天）：
//
//	· 夜 —— 需要"明确的光源"那句约束；
//	· 暗光（傍晚/黄昏/暮色/黎明）—— 不该被叫成"夜戏"，但也确实不该当白天处理（QC 的
//	  declared-dark 走的是另一套更严的词表，见 pipeline.darkSceneDeclared）；
//	· 白天 —— 有明确白天词；
//	· 未知 —— 两者都没有（**不当夜戏**：宁可少加一句，也不要把白天的戏按夜景渲）。
//
// 灯/烛/月/路灯这类**道具与月份一律不作为判据**（它们在白天与夜里都出现）。
type LightTier int

const (
	// LightUnknown 既没有白天词也没有夜/暗光词 —— 不按夜戏处理。
	LightUnknown LightTier = iota
	// LightDay 明确的白天（清晨/上午/午后/日光…）。
	LightDay
	// LightDim 暗光时段（傍晚/黄昏/暮色/黎明/dusk/twilight…）。
	LightDim
	// LightNight 明确的夜里（夜/深夜/午夜/night…）。
	LightNight
)

// lightNightWords 只列**时间**上的夜。灯/烛/月不进这张表（2026-10-08 事故）。
var lightNightWords = []string{
	"夜", "night", "midnight", "深夜", "午夜", "半夜", "夜晚", "入夜", "夜色", "夜间", "夜里",
}

// lightDimWords 暗光时段：不是白天，但也别叫"夜戏"。
var lightDimWords = []string{
	"傍晚", "黄昏", "暮色", "日暮", "薄暮", "华灯初上", "黎明", "拂晓", "凌晨", "破晓",
	"dusk", "twilight", "evening", "sunset", "sundown", "dawn",
}

// lightDayWords 明确的白天。
var lightDayWords = []string{
	"清晨", "清早", "早晨", "早上", "上午", "中午", "正午", "午后", "下午", "白天", "日间",
	"大白天", "晴天", "日光", "阳光", "日照", "朝阳", "旭日", "晴晨", "晨",
	"morning", "noon", "midday", "afternoon", "daylight", "sunlight", "daylight hours", "daytime", "day",
}

// lightConstraintMarkers 出现这些词的小句是**约束**而不是描述 —— 不参与时间判据。
//
// 【2026-10-08 真机代价】老城邮局营业厅的光态原文写着「相邻镜头不得跳到正午或夜晚」，
// 那是一句约束；按字面匹配到「夜晚」⇒ 整场被判夜戏 ⇒ 这一场每一镜的提示词里都被注入
// "This is a NIGHT scene"（见 pipeline.sceneLightingClause）。同一句的「清晨七点四十」才是描述。
var lightConstraintMarkers = []string{
	"不得", "不许", "不能", "不要", "不准", "切勿", "禁止", "避免", "勿", "别",
	"do not", "does not", "must not", "avoid", "never",
}

// stripLightConstraints 去掉约束小句，只留描述性小句。中文按标点切句（不切空格，英文整句保留）。
func stripLightConstraints(s string) string {
	segs := strings.FieldsFunc(s, func(r rune) bool {
		return strings.ContainsRune("，,、;；。！？!?\n\r", r)
	})
	kept := make([]string, 0, len(segs))
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		drop := false
		low := strings.ToLower(seg)
		for _, m := range lightConstraintMarkers {
			if strings.Contains(low, m) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, seg)
		}
	}
	return strings.Join(kept, " ")
}

// LightTierOf 判一个**时段/光态字符串**属于哪一档。
//
// 优先级：明确夜 > 暗光 > 白天 > 未知。这样「傍晚…夜里」这种混合写法按夜处理，
// 而「十一月底…台灯」既没有夜词也没有暗光词、却有白天词（日光柱）⇒ 白天 ✔。
//
// **约束小句先剔除**（见 stripLightConstraints）：真机邮局那条光态末尾写着
// 「相邻镜头不得跳到正午或夜晚」—— 那是约束，不是描述，按字面匹配会把白天判成夜戏。
func LightTierOf(s string) LightTier {
	l := strings.ToLower(stripLightConstraints(strings.TrimSpace(s)))
	if l == "" {
		return LightUnknown
	}
	for _, w := range lightNightWords {
		if strings.Contains(l, w) {
			return LightNight
		}
	}
	for _, w := range lightDimWords {
		if strings.Contains(l, w) {
			return LightDim
		}
	}
	for _, w := range lightDayWords {
		if strings.Contains(l, w) {
			return LightDay
		}
	}
	return LightUnknown
}

// IsNightLight 这一场到底算不算**夜戏**（驱动提示词里那句"必须是夜戏、要有可见光源"）。
//
// 只有明确夜才算；暗光（傍晚/黄昏）与未知都不算 —— 真机那本书 7 个场景里，
// 改前 5 个被判夜戏，改后 **0 个**（它们本来就是白天/傍晚的戏）。
func IsNightLight(s string) bool { return LightTierOf(s) == LightNight }

// IsDimLight 暗光时段（傍晚/黄昏/黎明…）：不是白天。
func IsDimLight(s string) bool { return LightTierOf(s) == LightDim }

// IsDayLight 明确的白天。
func IsDayLight(s string) bool { return LightTierOf(s) == LightDay }
