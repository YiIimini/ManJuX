package project

import "strings"

// spec_checks.go 技能侧《平台侧生成规范》里几条**词表类**判据的落点（§6.2 / §2.4 / §4.1 / §6.9）。
//
// 单独一位的理由：这些都是"照词表判"的东西，词表本身就是口径 ——
// 与实现放一起，改词表时不必去 1000 行的契约文件里翻。

// ── §6.2 机位角度词（中文短标签，必填项）────────────────────────────────────
//
// 运镜白名单（静止/镜头慢慢向前推进…）在这里不管：平台侧以官方 12 种运动写法进正文，
// 由 H3 格式那套门盯着；这一位只管"角度词写没写对"。组合写法（"慢动作，中景，低角度仰拍"）
// 里含这四个词之一即算命中，所以判据是**包含**而不是相等。
var PlanAngleWords = []string{"平视", "仰拍", "俯拍", "鸟瞰"}

// AngleWordOK 这个角度标签是不是词表里的（含组合写法）。
func AngleWordOK(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true // 空值合法（§8 第 6 条按 WARN，不拒收）
	}
	for _, w := range PlanAngleWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// ── §2.4 平台敏感词禁止表 ＋ 安全替代（**逐字**来自《人物道具提取指令》）────────
//
// 为什么要有这道门：这些词**云端会拦**（换后端才发现就晚了），本地也照写；
// 而正/负向提示词都在平台手里，生成后才发现只能整批重出。判据是"命中就报"，
// 并**顺手给出规范里的安全替代**（不是让人自己想办法）。
//
// 一个例外要处理：**否定语境**。`no bare feet` / `禁止赤足` / `不许露肩` 里的词是被否掉的，
// 报了就是误伤 —— 判据里带一道否定前缀检查（见 bannedWordingIn）。
var PlanBannedWording = []struct {
	Word string // 禁用词
	Hint string // 安全替代（规范原文给的写法）
}{
	{"赤足", "必须穿鞋：cloth shoes, mud-caked hem"},
	{"光脚", "必须穿鞋：cloth shoes"},
	{"裸足", "必须穿鞋：cloth shoes"},
	{"露趾", "必须穿鞋（不露趾的鞋面）"},
	{"深V", "高领/小立领/层叠交领：high collar / small stand collar / layered lapels"},
	{"低胸", "一字领(非低胸)/小立领"},
	{"露乳", "禁止任何暴露胸部的设计（高领/立领）"},
	{"领口大开", "小立领 / 层叠交领"},
	{"性感暴露", "写剪裁与材质：立体剪裁、4 种材质混搭"},
	{"裸露", "写服装结构而不是「露」：opaque matte fabric with a woven texture"},
	{"半裸", "同上：写完整服装结构"},
	{"超模身材", "写头身比＋肩宽：lean, straight-backed, seven and a half heads tall"},
	{"大长腿", "写比例：seven and a half heads tall with a slim waist and long legs"},
	{"巨乳", "删；用气质与剪裁描述代替"},
	{"翘臀", "删；用剪裁与体态描述代替"},
	{"蜜桃臀", "删；用剪裁与体态描述代替"},
	{"半透", "opaque matte fabric with a woven texture（并明确写「非透视」）"},
	{"透视装", "opaque matte fabric（明确非透视）"},
	{"网衣", "opaque woven textile"},
	{"链式衣饰", "删；换成常规配饰（材质对撞写够 4 种）"},
	{"蛛网衣", "opaque woven textile"},
	{"镂空", "删大面积镂空；需要装饰就用刺绣/暗纹"},
	{"毛孔可见", "matte skin with a soft natural finish（不得写实毛孔）"},
	{"真实毛孔", "matte skin with a soft natural finish"},
	{"汗毛", "matte skin with a soft natural finish"},
	{"血管可见", "删；必要时 faint blue veins at the temple, subtle"},
	{"暗示性动作", "把动作写成身体语言（看表/握拳/垂肩）"},
	{"不雅姿态", "站姿/坐姿写成生活里常见的样子"},
	{"低俗图案", "装饰用纹样：云纹/回纹/缠枝"},
	{"low-cut", "high collar / small stand collar"},
	{"barefoot", "cloth shoes"},
	{"see-through", "opaque matte fabric with a woven texture"},
	{"visible pores", "matte skin with a soft natural finish"},
	{"supermodel", "seven and a half heads tall, lean, straight-backed"},
}

// bannedNegations 否定前缀：命中词紧跟在这些词后面时**不算违规**（`no bare feet` 是对的写法）。
//
// 为什么值得列全：误报会逼着人把**正确**的写法删掉（"avoid low-cut collars" 本来就是规范要的），
// 所以英文的 avoid / free of / non- 与中文的禁止/不许/避免/杜绝都要认。
var bannedNegations = []string{
	"no ", "not ", "never ", "without ", "avoid", "free of", "non-", "non ",
	"禁止", "不许", "不得", "不要", "避免", "杜绝", "非", "杜绝",
}

// bannedWordingIn 文本里命中的禁用词与建议替代（空 = 没有）。返回(禁用词, 替代写法)。
//
// 否定语境跳过：命中位置往前看 12 个字符内有没有否定前缀（中英文各一套）。
func bannedWordingIn(s string) (string, string) {
	if strings.TrimSpace(s) == "" {
		return "", ""
	}
	low := strings.ToLower(s)
	for _, b := range PlanBannedWording {
		w := strings.ToLower(b.Word)
		idx := strings.Index(low, w)
		if idx < 0 {
			continue
		}
		if negatedBefore(low, idx) {
			continue
		}
		return b.Word, b.Hint
	}
	return "", ""
}

// hasAnyMarker 文本里有没有词表里的任一项（忽略大小写）。
func hasAnyMarker(s string, markers []string) bool {
	low := strings.ToLower(s)
	for _, m := range markers {
		if strings.Contains(low, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// ── 2026-09-28 实测驱动：两条"生成必崩"的组合 ──────────────────────────────
//
// 依据：真渲 48 镜的第一集，VLM 自检中位 74 分，而**最低的四镜（28/58/58/58）失败方式完全一样**：
//
//	「手指数量与关节异常，手掌与刀柄融合成一团肉块」「多指或融合」「与工具粘连」
//
// 四镜全是**手部 ＋ 工具**的近景/特写；而规范里"手指细小动作不要写"那条早就写在提示词里 ——
// 提示词不够，必须有机检。附带的伪汉字问题同理：角色卡的印记写着 `reading "丙十七"`，
// 于是额头上被画成一团糊字（同一本书的碑面也被画成"丙十十"这种无意义重复）。
var (
	// handToolWords 手部/工具精细动作（中英）。**只收"带工具的精细活"**——
	// 试跑真方案时发现：把 `刀`/`finger`/`thumb` 这种泛词收进来，会把「杠子滑下一寸」
	// 这类正常近景也算进来（48 镜里报 16 个，噪音压过信号）。判据要的是
	// "镜头贴着手和工具"这一种，所以中英各收**指向工具动作**的词。
	handToolWords = []string{
		"刻刀", "刀尖", "刀口", "凿子", "錾", "字口", "刻痕", "添刻", "撬开", "指节", "指尖", "描红",
		"chisel", "carv", "engrav", "blade", "whittle", "knife-edge", "fingertip",
	}
	// closeFramings 小景别（手部在这些景别里会占满画面）。
	closeFramings = []string{"特写", "近景", "close-up", "closeup"}
	// bodyTextWords 把**可读文字**放到皮肤/衣物/器物上的写法。
	//
	// ⚠ 只在**卡片文本**与**本镜动作**上判，**不扫整段正文**：正文每镜都会复述角色卡的身份锁
	// （`a pale-edged old branding scar reading "丙十七"` 会出现在 48 个镜里），扫正文会得到
	// 20+ 条同源误报 —— 真正要改的是那张卡，报一次就够。
	bodyTextWords = []string{
		`reading "`, "reads ", "scar reading", "brand reading",
		"刻字", "刻着", "刻上", "刻下", "凿字", "碑文", "字样",
		"characters carved", "lettered",
	}
)

// handToolCloseUp 这一镜是不是"小景别 ＋ 手部工具精细动作"（生成必崩的组合）。
//
// 为什么判"景别 + 词汇"两条同时命中：单独任何一条都不成问题（中景刻碑是正常戏，
// 特写一只空手也正常）——**两条叠在一起**才是模型必崩的构图。
func handToolCloseUp(shotSize, text string) bool {
	if !hasAnyMarker(shotSize, closeFramings) {
		return false
	}
	return hasAnyMarker(text, handToolWords)
}

// bodyReadableText 文本里把"可读文字"写到皮肤/衣物/器物上的说法（返回命中的词）。
func bodyReadableText(text string) string {
	low := strings.ToLower(text)
	for _, w := range bodyTextWords {
		if strings.Contains(low, strings.ToLower(w)) {
			return w
		}
	}
	return ""
}

// ── H3 正文词数（官方 `h3-prompt-writing/references/ref-en.txt` §5.2）────────────────
//
// 官方原文：生成任务的 `detailed_description` 通常 **350–500 英文词**，"A single shot does not
// automatically justify a shorter description"（单镜也不能因此写短）。
//
// 【2026-09-28 实测同步】平台此前把这条写成"目标而非配额"，于是真方案里 48 镜的正文落在
// **633–1407 词（中位 ~990）**，约为官方上限的两倍 —— 超长会把注意力摊薄在无关句子上，
// 该重的地方反而糊过去。所以这里加一条**双向**门：太少 = 描写不足，太多 = 稀释注意力。
const (
	H3WordsFloor = 300 // 官方下限 350，留一点余量（对白密集时官方允许优先排下整条台词线）
	H3WordsCeil  = 600 // 官方上限 500，留余量；实测中位 990 是明显超标
)

// H3DescriptionWords 正文的英文词数（只数含拉丁字母的词，中文标点/数字不算）。
func H3DescriptionWords(prompt string) int {
	n := 0
	for _, w := range strings.Fields(prompt) {
		for _, r := range w {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				n++
				break
			}
		}
	}
	return n
}

// ── 场景板"贫瘠"检测（用户 2026-09-28：「解析生成的场景简陋单一导致分镜内容一点也不理想」）──
//
// 判据按《四、场景提示词指令》的意图**翻译成可机检的三件**（不是把它的中文词表搬进来 ——
// 那套是即梦的写法，平台这边是 H3 的英文散文格式）：
//
//	① 建立镜头串（官方硬要求，缺了就不是"建立图"）；
//	② 五项大景元素里**至少两项**（天空/地平线/远山/巨大建筑群/开阔地貌）；
//	③ 光的质感（不是"白天"这种时间词，而是光的性质：黄昏死寂金光/黎明蓝调/丁达尔/体积光…）。
//
// 三条里缺任何一条都报"贫瘠"。宁可少报：词表写宽了会让人不再看这条告警。
var (
	// PlanSceneGrandElements 大景五要素。
	// 词表按**真实写法**放宽（第一版收 "distant mountain" 这种整短语，遇到
	// "distant blue-grey mountain ridges" 就匹配不上 —— 实测夹具先被自己卡住）。
	// 放宽的代价只是漏报，而"至少两项"这条约束仍然拦得住只有一面墙的贫瘠板。
	PlanSceneGrandElements = []string{
		// 注意**不收** "wall"：单面墙不算"巨大建筑群"，收了它这条门就废了（实测被自己的用例抓到）
		"sky", "horizon", "mountain", "ridge", "peak", "architecture", "building", "tower", "rooftop",
		"terrain", "valley", "plain", "field", "sea", "ocean", "river", "cityscape", "skyline",
		"天空", "地平线", "远山", "山脊", "建筑", "城", "开阔", "原野", "海",
	}
	// PlanSceneLightWords 光的质感（不是时间词）。中英都收，因为卡片可能是任一种写法。
	PlanSceneLightWords = []string{
		"light", "sunlight", "sunbeam", "god ray", "volumetric", "rim light", "backlight", "gold", "amber",
		"dusk", "dawn", "blue hour", "twilight", "glow", "shadow", "shine", "gleam", "luminous",
		"光", "丁达尔", "耶稣光", "逆光", "轮廓光", "体积光", "暮", "晨", "昏", "辉光", "光影",
	}
)

// ScenePlateThin 场景板是否贫瘠：返回缺的东西（空 = 不贫瘠）。
//
// 只在**有内容**的提示词上判（空串交给"缺提示词"那条门报，不在这里重复）。
func ScenePlateThin(prompt string) []string {
	s := strings.TrimSpace(prompt)
	if s == "" {
		return nil
	}
	low := strings.ToLower(s)
	var lack []string
	if !containsAny(s, PlanEstablishingExtra) {
		lack = append(lack, "建立镜头串")
	}
	grand := 0
	for _, w := range PlanSceneGrandElements {
		if strings.Contains(low, strings.ToLower(w)) {
			grand++
		}
	}
	if grand < 2 {
		lack = append(lack, "大景元素（天空/地平线/远山/建筑群/开阔地貌 至少两项）")
	}
	if !hasAnyMarker(s, PlanSceneLightWords) {
		lack = append(lack, "光的质感（不是「白天」这种时间词）")
	}
	return lack
}

// negatedBefore 命中位置之前一小段里有没有否定词。
func negatedBefore(low string, idx int) bool {
	start := idx - 12
	if start < 0 {
		start = 0
	}
	head := low[start:idx]
	for _, n := range bannedNegations {
		if strings.Contains(head, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// ── §4.1 场景命名：四字以上、带修饰词、严禁单一名词 ─────────────────────────
const PlanMinSceneNameRunes = 4

// ── §6.9 三人以上同框必须给拆解（谁在前景、谁被挡、镜头怎么切）───────────────
//
// 判据是"有没有指明前后关系"，不是"有没有用某个词"：中英文各认几种写法即可
// （写宽的代价只是漏报，写窄的代价是把写对的方案也报出来）。
var PlanCrowdOrderMarkers = []string{
	"前景", "背景", "中景", "遮挡", "挡住", "身后", "背后", "前方", "身后侧", "侧后方", "画左", "画右",
	"foreground", "background", "behind", "in front of", "left of frame", "right of frame", "occlud",
}

// ── 关键道具：分镜动作里出现器物却没有 prop_ids ────────────────────────────────
//
// 判据是"动作里点名了一件**应当成卡的器物**"：兵器、法器、书信、信物、器皿、绳索布匹。
// 词表按真实项目校准过（那一集里反复出现的是：碑刀、烙铁、名册板、腕上拓布、朱砂）。
// 宁可漏报——多报会让人不再看这条告警。
var PlanPropWords = []string{
	"刀", "匕", "剑", "杖", "棍", "锤", "烙铁", "印", "符", "铃", "簪", "镜", "灯",
	"册", "书", "信", "契", "簿", "帖", "囊", "袋", "绳", "索", "布", "帕", "令牌", "匣", "盒",
	"knife", "blade", "sword", "dagger", "staff", "ledger", "scroll", "letter", "talisman", "lantern", "mirror",
}

// firstPropWord 动作里命中的第一个器物词（给人看的告警里指个位置）。
func firstPropWord(s string) string {
	low := strings.ToLower(s)
	for _, w := range PlanPropWords {
		if strings.Contains(low, strings.ToLower(w)) {
			return w
		}
	}
	return ""
}
