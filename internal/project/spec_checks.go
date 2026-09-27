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
