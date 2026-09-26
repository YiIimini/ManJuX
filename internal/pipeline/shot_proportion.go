package pipeline

import "strings"

// shot_proportion.go 镜头(h3_prompt)侧的「人体比例锚点」注入 —— **按景别发子集**。
//
// 【起因】用户报「渲染出来的角色比例那么奇怪」+「人物渲染比例太丑了」。实测定妆照那条链
// **有**技能侧的比例锚点(`HUMAN_PROPORTION_CLAUSE`,见 proportion_clause.go 顶部抄的
// 本机真实角色卡原文),而镜头提示词里**一条都没有**:逐镜核对 64 个镜头,含比例锚点的 0 个。
// 后果不是"少写一句",而是 H3 生成镜头时对头身比/眼距**没有任何约束** ——
// 每个镜头各按各的审美发挥,而且**跨镜不一致**:同一张定妆照锁住的脸,到了某一镜被拉长、
// 眼距被改宽。定妆照锁得住脸,锁不住"这一镜怎么摆比例"。
//
// 【为什么按景别发子集,而不是一律塞全套】"七个半头"写进一个特写镜头毫无意义 ——
// 那镜头根本看不见腰和腿,写了只是稀释注意力(镜头提示词本身已是 4k 字符量级)。
// 只写**画面上真的看得见**的那几条,是唯一能让它被读进去的写法:
//
//	特写 / 近景        → 只有脸:①眼线中线 ②眼距＝一眼宽 ③鼻长 ⅓ ④嘴 ⅕ ⑤下巴 ⅔ 颧骨
//	中景 / 中近景      → 脸的简版(眼距 + 鼻 ⅓ / 嘴 ⅕)⑥颈细长 ⑦肩宽≈两头宽
//	全景 / 远景 / 全身 → 全套八条(含 ⑧七头半、腰细腿长)
//	认不出的景别       → 不注入
//
// 【口径只有一份】三个档位的正文全部是技能侧比例句的**逐字子集** ——
// 一条都不另编比值。这不是省事:少一个数就少一条约束,多一个数就是第二个口径,
// 而同一段提示词里两套比例时模型只会取中值(技能侧清单 §5.1 的原话,见
// proportion_clause.go 顶部)—— 那正是"丑"的来源之一。所以这里连"黄金比例"这类
// 技能侧没用过的说法都不许出现(测试显式拒绝)。
//
// 【判据复用】"这段提示词里已经有技能侧比例句了"只认 proportion_clause.go 的
// HumanProportionClausePresent(它认的是技能侧的特征短语),不在这里另写一套:
// 同时它也是本文件幂等性的来源 —— 注入的正文本身就带着那些特征短语,
// 所以任何"已经带锚点的提示词"再进这条路都会被认出来而不再叠加。

// shotProportionMarker 注入块的抬头,同时是幂等/覆盖率的判据串
// (改文案必须原样保留它,否则"这一镜有没有注入过"就没法查了)。
const shotProportionMarker = "PROPORTIONS ANCHOR (from the character sheet, identical in every shot): "

// 三档正文。除抬头外**逐字**取自技能侧 HUMAN_PROPORTION_CLAUSE(见 proportion_clause.go
// 顶部原文),不新增任何比值、不改写任何措辞。
//
// 每档都是"一句比例 + 一句禁令":禁令照抄技能侧那句(`Never … never lengthen the face
// and never enlarge the eyes`),它是防"美人先验"把脸拉长/眼睛放大的那一半 ——
// 只有比值而没有禁令时,模型完全可以在"符合比例"的同时把整张脸往网红审美上挪。
const (
	// 脸档:画面里只有脸,颈肩腰腿一概看不见,所以只写脸的五条。
	shotProportionFaceBody = "the eye line sits at the vertical middle of the head; " +
		"the eyes are one eye-width apart; " +
		"the nose is about one third of the face length and the mouth about one fifth of it; " +
		"the chin is about two thirds as wide as the cheekbones. " +
		"Never lengthen the face and never enlarge the eyes."
	// 中景档:脸的简版(眼距 + 三庭)加看得见的颈与肩;头身比仍不写(腰腿还看不见)。
	shotProportionMediumBody = "the eyes are one eye-width apart; " +
		"the nose is about one third of the face length and the mouth about one fifth of it; " +
		"the neck is slim and long; " +
		"the shoulders are about two head-widths wide. " +
		"Never distort these ratios, never lengthen the face and never enlarge the eyes."
	// 全身档:八条齐发。
	shotProportionFullBody = "the eye line sits at the vertical middle of the head; " +
		"the eyes are one eye-width apart; " +
		"the nose is about one third of the face length and the mouth about one fifth of it; " +
		"the chin is about two thirds as wide as the cheekbones; " +
		"the neck is slim and long; " +
		"the shoulders are about two head-widths wide; " +
		"the figure is seven and a half heads tall with a slim waist and long legs. " +
		"Never distort these ratios, never lengthen the face and never enlarge the eyes."
)

// shotProportionLevel 景别 → 该写哪一档比例。
type shotProportionLevel int

const (
	shotProportionNone   shotProportionLevel = iota // 认不出的景别:宁可不加
	shotProportionFace                              // 特写 / 近景:画面里只有脸
	shotProportionMedium                            // 中景 / 中近景:看得见颈与肩
	shotProportionFull                              // 全景 / 远景 / 全身:人整个在画面里
)

// shotProportionLevelOf 景别 → 比例档位。
//
// 分族口径与 refs.go 的 wantsAnglesStrip **同一套**:项目里的景别由技能侧自由书写,
// 白名单是 远景/全景/中景/中近景/近景/特写,而"大特写""中景偏全景"这类同族词同样合法
// (project 侧的校验判据就是 Contains),所以这里也按 Contains 分族,不做相等匹配 ——
// 相等匹配会把真实数据里的同族词静默漏掉,而漏掉的正是本该被约束的那些镜头。
//
// 两条容易写错的顺序:
//   - "中近景"必须**先于**"近景"判(它的字面里带"近景"),否则中近景会被当成脸档;
//   - 英文 "medium close-up" 必须**先于** "close-up" 判(同理)。
//
// 中近景归**中景档**:它看得见颈与肩(⑥⑦ 有用),看不见腰腿(⑧ 用不上)——
// 与 refs.go 把中近景当"身体仍有一部分可见"处理保持一致。
//
// 认不出的景别返回 none:加错的后果不是"没效果",而是把"七个半头"塞进一个特写 ——
// 提示词与画面对不上,模型只会把两者折中(脸被画小、构图变形)。
func shotProportionLevelOf(shotSize string) shotProportionLevel {
	s := strings.ToLower(strings.TrimSpace(shotSize))
	if s == "" {
		return shotProportionNone
	}
	switch {
	// —— 英文(技能侧/上游偶尔用英文写景别)——
	// 先判 "medium close":它同时含 medium 与 close,归中景档(理由同中近景)。
	case containsAny(s, "medium close", "mid close"):
		return shotProportionMedium
	case containsAny(s, "extreme close", "close-up", "close up", "closeup",
		"tight shot", "head shot", "headshot", "bust shot"):
		return shotProportionFace
	case containsAny(s, "medium shot", "mid shot", "cowboy shot", "medium"):
		return shotProportionMedium
	case containsAny(s, "wide", "long shot", "full shot", "full-body", "full body", "full figure", "establishing"):
		return shotProportionFull

	// —— 中文(含同族词)——
	case strings.Contains(s, "特写"):
		return shotProportionFace
	case strings.Contains(s, "中近景"):
		return shotProportionMedium
	case strings.Contains(s, "近景"):
		return shotProportionFace
	case strings.Contains(s, "中景"):
		return shotProportionMedium
	case strings.Contains(s, "全景"), strings.Contains(s, "远景"), strings.Contains(s, "全身"):
		return shotProportionFull
	}
	return shotProportionNone
}

// containsAny s 是否含 needles 中的任意一个(已小写化的 s)。
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// shotProportionClause 该景别要注入的比例锚点句(**纯函数**);认不出的景别返回空串。
func shotProportionClause(shotSize string) string {
	var body string
	switch shotProportionLevelOf(shotSize) {
	case shotProportionFace:
		body = shotProportionFaceBody
	case shotProportionMedium:
		body = shotProportionMediumBody
	case shotProportionFull:
		body = shotProportionFullBody
	default:
		return ""
	}
	return shotProportionMarker + body
}

// shotProportionBlock 本镜实际要追加的比例锚点块(不该加时返回空串,含尾随换行)。
//
// 两道闸,缺一不可:
//
//   - **景别**:认不出就不加(见 shotProportionLevelOf)。
//   - **主体**:本镜有**人类**角色,且没有 Q 版角色。比例锚点写的是人体(眼线/肩宽/
//     七头半),发给纯器物镜头等于把铜雀往人形上拽 —— 与 entity_prompts 的
//     ARTEFACT CLAUSE、anchorPrompt 的【外貌不变项】是同一道闸(用户 2026-09-24 报过
//     「阿凿是一只铜雀却渲染成人物」)。Q 版则相反:它的比例**就是它这个类型本身**
//     (二到三头身,见 EntityProfileOf(KindChibi).SheetProportions),同镜再写
//     "七个半头"就是两套比例,模型取中值 —— 正是技能侧清单 §5.1 记的病根。
func shotProportionBlock(shotSize string, shotKinds []EntityKind) string {
	if !wantsHumanProportion(shotKinds) {
		return ""
	}
	clause := shotProportionClause(shotSize)
	if clause == "" {
		return ""
	}
	return clause + "\n"
}

// wantsHumanProportion 本镜该不该发人体比例锚点:有**人类**角色,且没有 Q 版角色。
//
// 只按"镜头里是谁"判,不看角色的 description 是否为空 —— 比例是关于人体的,
// 与平台手上有没有写角色描述无关(有描述的角色走别的条款)。
func wantsHumanProportion(shotKinds []EntityKind) bool {
	human := false
	for _, k := range shotKinds {
		switch NormalizeEntityKind(string(k)) {
		case KindHuman:
			human = true
		case KindChibi:
			// Q 版的比例归它自己的类型(二到三头身):同镜两套比例＝模型取中值。
			return false
		}
	}
	return human
}
