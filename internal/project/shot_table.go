package project

import "strings"

// shot_table.go 官方「标准镜头信息表」的受控词表与自检阈值
// （依据 MiniMax-AI/MiniMax-H3 `skills/3d-animation-short-generator/references/shot-table-spec.md`
// STEP 5 六列镜头表 + STEP 5.5 强制自检）。
//
// ## 为什么平台要照着官方镜头表来
//
// 平台的 `plan.json` 就是这张镜头表：官方六列 ↔ 我们的字段是一一对应的 ——
//
//	Shot ID & Duration            → id / duration
//	Continuity Handoff            → end_state（尾帧状态）
//	Reference Anchors             → scene(地标/光态) + characters(身份) + refs
//	Hook Type                     → hook_type（本文件）
//	Shot Description (per-second) → action + h3_prompt
//	Audio & Dialogue Track        → dialogue / narration / h3_prompt 的 <d>
//
// 官方 STEP 5.5 是**强制**自检（不通过不许进下一步），其中三条机器判得出来，落在契约里：
// ①钩子密度 ②单镜主要角色数 ≤3 ③场景卡必须写"连续性地标 + 光态"。另外三条（时长上限、
// 每秒钟指令覆盖、跨镜连贯链）分别由 duration 的硬区间、end_state 判据、以及装配点的
// 提示词规则承担 —— 详见 docs/渲染脚本契约.md 的说明。

// PlanHookTypes 钩子类型**受控词表**（官方 STEP 5：one short label from a controlled vocabulary）。
//
// **官方是 10 个**（`3d-animation-short-generator/references/storyboard-guidelines.md`：
// "one of the controlled vocabulary: setup / visual-joke / reversal / reveal / callback /
// suspense / tender / chase / expression-beat / climax"）。
// 2026-09-27 晚更正：此前只收 8 个 —— 因为抄的是**另一份**官方文件
// `shot-table-spec.md` 那句，而那句写的是 "e.g." 只举了 8 个例子（还被我注释成"逐字照抄官方示例"）。
// 代价是**把技能侧合法的取值判成违规**：技能侧 `qa_check.py` 的 HOOK_WORDS 正是官方那 10 个，
// 而《求你们骂我》第 01 集 64 镜里 `setup` 就用了 13 次 —— 一导入就会刷一屏"词表外"的假告警。
// 教训：**受控词表只能从"controlled vocabulary"那句抄**，不能从带 "e.g." 的举例句抄。
var PlanHookTypes = []string{
	"setup", "visual-joke", "reversal", "reveal", "callback",
	"suspense", "tender", "chase", "expression-beat", "climax",
}

// PlanHookDensityTypes 密度自检认的三种"强钩子"（官方 STEP 5.5 检查 1：每连续三镜至少一个 reveal/reversal/callback）。
var PlanHookDensityTypes = []string{"reveal", "reversal", "callback"}

// PlanHookStrongTypes 开篇镜与收尾镜必须带的强钩子（官方 STEP 5.5 检查 1 后半句）。
var PlanHookStrongTypes = []string{"visual-joke", "reversal", "reveal", "suspense", "tender"}

// 单集节奏公式的门槛（行业实测，见 plan_contract.go 的引注）。
const (
	// PlanOpeningSetupSeconds 开场多少秒内不许连续铺垫（实测：短剧不存在慢开场）。
	PlanOpeningSetupSeconds = 10
	// PlanFirstPayoffSeconds 前多少秒内必须出现第一次反转/爽点。
	PlanFirstPayoffSeconds = 30
	// PlanBeatGapSeconds 情绪节点的最大间隔（实测口径：每 20–30 秒一处）。
	PlanBeatGapSeconds = 30
)

// PlanEmotionMinSeconds 承载**核心情绪**的镜头时长下限（用户《【额外】测试版30S分镜指令》：
// 「承载核心情绪反应的镜头（特写面部微表情、沉默对峙、泪目、咬牙、握拳颤抖等），
// 其镜头时长不得少于 **5 秒**。这类镜头是"戏眼"，快切会直接消解情感浓度」）。
//
// 【2026-09-29】这条口径此前只记在 `director/mode.go` 的 `Rhythm30` 表里（30 秒档），
// 而**平台一条判据都没有** —— 60 秒档与仿真人档的用户文件里写着同一件事
// （"给足情绪发酵时间，宁愿压缩前面的动作铺垫"），不止 30 秒档。
// 判据本身是**判得了**的（情绪镜由钩子类型 + 景别识别），所以它不是"结构不支持"那一类，
// 是单纯没做。见 `EmotionShotIssues`。
const PlanEmotionMinSeconds = 5

// PlanEmotionHookTypes 承载核心情绪的钩子类型（`tender` 情绪戏 / `climax` 高潮戏眼）。
var PlanEmotionHookTypes = []string{"tender", "climax"}

// PlanPreferredShotSeconds 非末尾片段的**期望时长下限**（用户《【额外】仿真人10S分镜头指令》：
// 「非最后一个片段时长优先在 7、8、9、10 秒中选择，以结束于动作自然停顿点为准」）。
const PlanPreferredShotSeconds = 7

// PlanShotSecondsRef 推算"这一集该有多少镜"用的参考时长（7-10 秒的中位 ≈ 8.5）。
// 镜头数 ≈ 本集总秒数 ÷ 这个值 —— 实测这一条最能说明问题：真方案 239 秒给了 35 镜，
// 而按口径 28 镜就够，多出的 7 镜是 7 次纯属多余的 GPU 生成。
const PlanShotSecondsRef = 8.5

// PlanShortClipSharePct 非末尾片段里"短于 7 秒"的占比超过它 ⇒ 报"镜头过碎"。
// 25% 的由来：真方案的 41% 一眼就知道碎，而 0-10% 属于正常波动（个别镜确实该短）。
const PlanShortClipSharePct = 25

// PlanOneTakeSeconds 超过这个时长的片段**必须**在正文里切内部小镜头（否则报"一镜到底"）。
//
// 【2026-09-28 校正 5 → 4 秒】依据就是用户文件本身：《【额外】仿真人10S分镜头指令》
// 「每个片段内拆成若干小镜头，每镜 **1.5–4 秒**」—— 既然单镜上限是 4 秒，那么"超过 4 秒
// 还只有一个 [Shot 1]"就已经违反该文件了，5 秒这条线是我的判断、比用户文件松。
// 分界值取用户文件的上限，规则才与出处一致（这也是对账表里那行"没有出处"的消除方式：
// 不是去给它找借口，而是**按出处取值**）。
const PlanOneTakeSeconds = 4

// PlanPayoffMaxRunes 爽点密度上限（用户《一、网文小说转剧本提示词指令》：
// 「每 80 字 1 个小爽点，每 200 字 1 个大爽点」）。按**台词字数**累计，
// 连续超过这个数还没有强节拍 ⇒ 这一段是平的。
const PlanPayoffMaxRunes = 200

// isPayoffHook 这个钩子算不算"爽点级"强节拍（逆袭/打脸/揭秘/升阶）。
func isPayoffHook(h string) bool {
	switch strings.TrimSpace(h) {
	case "reveal", "reversal", "climax":
		return true
	}
	return false
}

// PlanHookDensityWindow 钩子密度的滑动窗口（官方：at least one of every three consecutive shots）。
const PlanHookDensityWindow = 3

// PlanMaxImportantCharacters 单镜主要角色上限（官方 STEP 5.5 检查 3：no shot contains more than
// three important characters — 有画面动作或台词的角色）。超了模型会把注意力摊薄，
// 每个人都画不好，也更容易互相换脸。
const PlanMaxImportantCharacters = 3

// IsKnownHookType 钩子类型是否在受控词表里（空值不算，由调用方单独报"缺"）。
func IsKnownHookType(s string) bool {
	if s == "" {
		return false
	}
	for _, h := range PlanHookTypes {
		if h == s {
			return true
		}
	}
	return false
}

// hookTypeOf 取归一化后的钩子类型（去空白）。
func hookTypeOf(sh Shot) string { return strings.TrimSpace(sh.HookType) }

// InList 判断 s 是否在 list 里（钩子词表判据的小工具，避免各调用点各写一遍循环）。
func InList(s string, list []string) bool {
	for _, v := range list {
		if s == v {
			return true
		}
	}
	return false
}
