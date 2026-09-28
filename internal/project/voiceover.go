package project

import "strings"

// voiceover.go **画外演出**的两条口径（技能侧 2026-09-27 交付 · 用户定调）。
//
//	内心活动：由该角色的 **Q 版形象**在画面里演出这段心思，**本人闭嘴、不演这段**；
//	系统    ：科幻悬浮**全息面板**（道具）＋ 可爱的 **Q 版小机器人**（人物）同框，
//	          人闭嘴、不念系统台词。
//
// 用户原话：「正角内心活动需要是 Q 版形象角色来展示，系统 需要科幻悬浮全息面板配一个可爱的
// Q 版小机器人（QQ 版形象为人物，科幻渲染全息面板为道具）」。
//
// 为什么在**契约层**（而不是只在渲染层）：这两类镜的参考图完全不同（见 pipeline/refs.go），
// 字段写错/引用悬空的后果是"该出 Q 版却出了本人"——画面错了，而日志里一条错都没有。
// 契约门在**导入时**就能拦住这类数据错（悬空 = fail），并提示演出句有没有写进正文。
const (
	// VoKindInner 内心活动镜：Q 版演出。
	VoKindInner = "inner"
	// VoKindSystem 系统镜：全息面板（道具）＋ Q 版机器人（人物）演出。
	VoKindSystem = "system"
	// VoKindSpeech 画面里**有人在说**（技能侧生成规范 §6.6 五值之一）。
	//
	// 它是最容易被搞错的一档：这一镜**不许**写闭嘴句 —— 画面里有人张嘴说话却写了
	// "lips remain completely closed"，渲出来就是**哑剧**（口型不动，观众看到的是"人在演但没声"）。
	// 反过来，只有**真画外**（inner/system/narration，或 os=true 的台词）才写那句。
	// 契约门对这两个方向各有一条（见 plan_contract.go 的闭嘴句一致性检查）。
	VoKindSpeech = "speech"
	// VoKindNarration 旁白（画外音）—— 技能侧 §4 提到的第三种取值，**不要求**演出句：
	// 这一镜本身就是"画外音在说、画面里的人不说话"，平台原有通路足够
	// （EnsureVoiceoverClause 补闭嘴句、compose 按旁白排版、说话人「旁白/系统/内心」都算画外音标签）。
	//
	// 2026-09-27 实测：《求你们骂我》2462 镜里 **739 镜标了 narration** ——
	// 门若不认它，导入时就会一口气报 739 条「取值不认识」，门自己变成噪音源。
	// 教训：**门认的取值表要照真实数据校一遍**，不能只照文档写（他们 §0 的表只列了 inner/system，
	// 第三种在 §4 的一句里提过，字段却真的在用）。
	VoKindNarration = "narration"
)

// PlanInnerPerformanceMarker / PlanSystemPerformanceMarker 演出句的关键片段。
//
// 与技能侧的英文口径**逐字对齐**（他们写进 h3_prompt 的就是这两句）：
//
//	inner : "The inner monologue is performed by the CHIBI (Q-version) figure of <角色> …"
//	system: "The system interface is performed by a floating translucent HOLOGRAPHIC PANEL (prop)
//	         accompanied by a cute CHIBI ROBOT companion …"
//
// 判据取其中的**稳定片段**（大小写不敏感），不取整句：角色名与附加措辞会变，句式不会。
// 与 S2/S3 同一条纪律 —— **字段填了不算，演出句必须逐字写进正文**。
const (
	PlanInnerPerformanceMarker  = "performed by the CHIBI"
	PlanSystemPerformanceMarker = "HOLOGRAPHIC PANEL"
)

// PlanInnerPerformanceOK 这一镜的正文有没有写"由 Q 版形象演出"那句。
func PlanInnerPerformanceOK(h3Prompt string) bool {
	return containsFold(h3Prompt, PlanInnerPerformanceMarker)
}

// PlanSystemPerformanceOK 这一镜的正文有没有写"全息面板＋Q 版机器人演出"那句。
func PlanSystemPerformanceOK(h3Prompt string) bool {
	return containsFold(h3Prompt, PlanSystemPerformanceMarker)
}

// HasVoiceoverShots 方案里有没有画外演出镜（没有就整段检查都不跑，不刷无意义的门）。
func (p *Plan) HasVoiceoverShots() bool {
	if p == nil {
		return false
	}
	for _, sh := range p.Shots {
		if strings.TrimSpace(sh.VoKind) != "" {
			return true
		}
	}
	return false
}

// HasCharacterID / HasPropID 方案里有没有这张卡（引用完整性用）。
func (p *Plan) HasCharacterID(id string) bool {
	if p == nil {
		return false
	}
	id = strings.TrimSpace(id)
	for _, c := range p.Characters {
		if strings.TrimSpace(c.ID) == id {
			return true
		}
	}
	return false
}

func (p *Plan) HasPropID(id string) bool {
	if p == nil {
		return false
	}
	id = strings.TrimSpace(id)
	for _, pr := range p.Props {
		if strings.TrimSpace(pr.ID) == id {
			return true
		}
	}
	return false
}

// IsChibiCard 这张角色卡是不是 **Q 版形象**（技能侧 `kind: "chibi"`；id 约定 `chibi_<原角色id>`）。
//
// 【两套命名都要认】技能侧用**前缀**（`chibi_char_01` = 沈砚·Q版），平台自己早先派生 Q 版时
// 用的是**后缀**（`char_01_chibi`）—— 判据只认一套就会漏：实测撞脸自检因此把**本人和它自己的
// Q 版**拿去互比（Q 版本来就是同一个人 ⇒ 必然报"长得像同一个人"，用户 2026-09-28 报障）。
// 所以这个助手同时认 kind / 前缀 / 后缀，**全平台只有这一处判据**。
//
// 为什么要一个判据：Q 版**不是造型档**。造型档是"同一个人按场换服装"，而 Q 版是
// "同一个人的另一种画法"，观众靠它认"这段是心里话"。两者混在一起的两个后果：
//   - 界面上把 Q 版算成"沈砚的 2 个造型档"（用户看不懂多出来的那张是什么）；
//   - 撞脸自检拿本人和它自己的 Q 版互比（比了必然"像"）。
func IsChibiCard(kind, id string) bool {
	if strings.EqualFold(strings.TrimSpace(kind), "chibi") {
		return true
	}
	s := strings.TrimSpace(id)
	return strings.HasPrefix(s, "chibi_") || strings.HasSuffix(s, "_chibi")
}

func containsFold(s, sub string) bool {
	if sub == "" {
		return false
	}
	return strings.Contains(strings.ToUpper(s), strings.ToUpper(sub))
}

func hasString(list []string, v string) bool {
	v = strings.TrimSpace(v)
	for _, s := range list {
		if strings.TrimSpace(s) == v {
			return true
		}
	}
	return false
}
