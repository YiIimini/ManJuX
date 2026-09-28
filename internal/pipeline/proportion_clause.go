package pipeline

import (
	"regexp"
	"strings"
)

// proportion_clause.go 技能侧「人体比例锚点」在平台侧的处理(技能侧《平台侧整改清单》§5.1)。
//
// 背景(2026-09-26):用户报「《求你们骂我》主角和女角的人物渲染比例不是黄金比例、太丑了」。
// 技能侧定位到的病根是旧口径**只写风格词、没有任何比例**(`Clean anime-style face,
// clear-cut jawline, large bright eyes`),模型于是各按各的审美发挥:脸被拉长、三庭不匀、
// 眼距失衡、头身比失调。技能侧为此新增 `HUMAN_PROPORTION_CLAUSE`(人体卡自动带),
// 本机落盘原文(摘自 data/projects/求你们骂我 的真实角色卡 image_prompt):
//
//	Proportions must be balanced and idealised: the eye line sits at the vertical middle of
//	the head; the eyes are one eye-width apart; the nose is about one third of the face length
//	and the mouth about one fifth of it; the chin is about two thirds as wide as the cheekbones;
//	the neck is slim and long; the shoulders are about two head-widths wide; the figure is
//	seven and a half heads tall with a slim waist and long legs. Never distort these ratios,
//	never lengthen the face and never enlarge the eyes.
//
// 平台侧在本条里的分工只有两件(清单 §5.1):**别覆盖它** + **测与报**。
// 本文件是第①件 —— 识别"这段提示词里已经有技能侧比例句",好让平台自己那套比例措辞让位;
// 第②件(定妆照出图后的比例自检)见 internal/qc/proportion.go 与 stages_assets.go 的接线。
//
// 【为什么必须让位,而不是并排放着】同一段提示词里两套比例,模型只会取中值 ——
// 平台侧写"二到三头身"、技能侧写"七个半头",画出来既不是 Q 版也不是真人比例。
// 这正是"丑"的来源之一(技能侧的原话:同一段里两套比例＝模型取中值)。
//
// 【平台侧到底有哪些比例/脸型措辞】(2026-09-26 逐处核对,清单猜的"介质块/取景块"其实不带):
//   - `styleExpand` 的 hasQ 分支:术语 "cute chibi proportions"(**比例**措辞);
//   - `entityProfiles[KindChibi].SheetLook`:原先那句 "Two-to-three-head-tall proportions"
//     (**比例**措辞,已拆到 SheetProportions 以便让位);
//   - `identityFidelityBlock`(人类 SheetIdentity):只写"脸型/下颌/眉形照简报抄、不得美化/
//     放大眼睛",是**身份忠实度**条款,不含任何比值 —— 与技能侧比例句不构成两套比例,
//     且它是全项目唯一防"美人先验"的注入点,故**保留不动**(删它会引入新问题);
//   - 介质块(animatedMediumImageBlock / animatedFaceImageBlock):只有"手绘线稿 + 赛璐璐
//     平涂 + 哑光",且 artstyle_test.go 已钉住"不得出现五官形状词" —— 无需改动;
//   - 取景重写(rewriteSheetFraming):**人类不走这条**(prof.RewriteSheetFraming=false),
//     所以它不会去剥人类卡上的比例句(该不变量由 proportion_clause_test.go 钉住);
//   - 转身/局部重绘/白底文件里的 "body proportions must stay exactly the same as <Picture 1>"
//     是**照抄参考图**的身份条款,同样不含比值。
//
// 判据为什么按**特征短语**而不是整段相等:技能侧的比例句会随版本微调措辞(它自己的
// 交付说明里就写着"缺了就 validate_plan 报提示"),逐字比对会在下一次改词时静默失效 ——
// 而失效的后果正是本条要防的"两套比例同现"。所以取若干**稳定特征**,任一命中即判定。
var skillProportionSignatures = []string{
	// 英文(技能侧现行口径,特征取"只可能出现在比例句里"的短语)
	"eye line sits at the vertical middle",
	"one eye-width apart",
	"about one third of the face length",
	"two thirds as wide as the cheekbones",
	"seven and a half heads",
	// 【2026-09-28 审计实测 · 识别漏了】平台自己的提示词（`sysFaceVocabRules`）要求模型写
	// "eight and a half heads tall"，而上面那张签名表只认 7.5 —— 于是模型**按平台要求写对了**，
	// 这里却识别不到"已有比例句"，平台随后再注入自己那套 ⇒ **两套比例同现、模型取中值**。
	// 这正是本文件开头说的"丑"的来源，而且形态最隐蔽：不是漏在"没写比例"，是漏在"写了却不认"。
	//
	// 头身数的口径统一是用户的决定（四方不一致：技能侧锚表写「7.5 / 8 两档，不许第三种」、
	// 用户《二》写九头身、《【额外】》写 8.5、平台此前自己拍过 7.5），已在回复里请他拍板；
	// 但"**识别不许漏**"是确定的工程问题 —— 先把 8.5 与锚表的两档写法一并收进来。
	"eight and a half heads",
	"eight heads tall",
	"seven point five heads",
	// 中文（锚表的记述形态：`比例锚_机器：8 头身`）
	"八个半头",
	"八头身",
	"never distort these ratios",
	// 中文(清单 §5.1 的记述形态;技能侧若改出中文卡也能认出来)
	"眼线在头部垂直中线",
	"两眼间距＝一个眼宽",
	"七个半头",
}

// reHeadRatio 阿拉伯数字的头身比写法。
//
// 【为什么子串不够，非要一条正则】技能侧锚表里每个角色的比例锚是**带数字的**：
// `比例锚_机器：8 头身` / `7.5 头身` / `6.5 头身` —— 一部戏里从 6 到 8 头身都有值，
// 把每一种数字都列进签名表既列不全、也会随下一部戏再漏一次。
// 正则认的是"**数字 + 头身/heads tall**"这个形态本身。
var reHeadRatio = regexp.MustCompile(`\d+(?:\.\d+)?\s*(?:头身|heads?[-\s]tall)`)

// HumanProportionClausePresent 该提示词里是否已有技能侧的人体比例锚点句。
//
// 只用于**平台侧自己的比例措辞让位**(见 styleExpandFor 与 SheetProportions),
// **不用于改写技能侧的文本**:平台不删、不改、不重写它 —— 那一段是技能侧的定义权
// (清单 §5.1 末段:平台只做"测与报",不自己另定一套比例标准)。
func HumanProportionClausePresent(prompt string) bool {
	if strings.TrimSpace(prompt) == "" {
		return false
	}
	low := strings.ToLower(prompt)
	for _, sig := range skillProportionSignatures {
		if strings.Contains(low, strings.ToLower(sig)) {
			return true
		}
	}
	// 带数字的头身比（锚表的 `8 头身`、卡里的 `7.5 heads tall`）—— 见 reHeadRatio 的说明：
	// 数字取值随角色而变，列不完，只能按形态认。
	return reHeadRatio.MatchString(low)
}

// PortraitProportionCheckable 这张定妆照该不该做人体比例自检(给 qc 侧的比例自检用)。
//
// 只对**人**做:器物/灵宠/妖兽/无实体没有人体比例可言,拿"七个半头"去量一只铜雀
// 只会得到一堆假告警。
//
// Q 版默认**不做**:它故意就是二到三头身,用人体锚点去量必然"明显偏离"(那是它的设计,
// 不是缺陷)。但若这张卡自带技能侧的人体比例句(技能侧把某一档写成人体卡的变体),
// 那张卡的契约就是人体比例 —— 照测。
func PortraitProportionCheckable(kind EntityKind, prompt string) bool {
	if NormalizeEntityKind(string(kind)) == KindHuman {
		return true
	}
	return HumanProportionClausePresent(prompt)
}
