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

// FillVoiceoverAssets 补全画外演出镜的**素材引用**，返回补了几镜。
//
// 【2026-09-28 实测驱动】一次真跑里整个方案的唯一 fail 是
// 「#11 vo_kind=inner 但 chibi_ids 为空」：模型标了"这是内心镜"，却没写演出素材 id。
// 口径是"字段标了不算、素材必须真在"，所以契约必须 fail —— 但用户看到的是"又说不能渲"，
// 而**素材本来就是按角色自动生成的**（资产阶段给每个角色同步出 Q 版），缺的只是"对上号"。
//
// 所以这里确定性补全（幂等，可重复调用）：
//   - inner 镜：为出场角色各建一张 Q 版**卡**（技能侧命名 `chibi_<原角色id>`，kind=chibi），
//     并把 id 写进 chibi_ids。为什么不指望模型写：Q 版不是创作内容，是"内心戏由 Q 版演出"
//     这条口径的执行体，模型漏一次就 fail 一次。
//   - system 镜：bot 挂在 `chibi_sys_01`（没有就建一张系统机器人卡），panel 指向一件道具卡
//     （没有就按该场建一件"面板"道具，1:1 白底提示词），并把它并进 prop_ids。
func FillVoiceoverAssets(plan *Plan) int {
	if plan == nil {
		return 0
	}
	n := 0
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		switch sh.VoKind {
		case VoKindInner:
			if len(sh.ChibiIDs) == 0 {
				var ids []string
				for _, cid := range sh.Characters {
					cid = strings.TrimSpace(cid)
					if cid == "" {
						continue
					}
					chibi := ensureChibiCard(plan, cid)
					if chibi != "" {
						ids = append(ids, chibi)
					}
				}
				if len(ids) > 0 {
					sh.ChibiIDs = ids
					n++
				}
			}
		case VoKindSystem:
			if sh.SystemAssets == nil {
				sh.SystemAssets = map[string]string{}
			}
			changed := false
			if strings.TrimSpace(sh.SystemAssets["bot"]) == "" {
				if id := ensureSystemBotCard(plan); id != "" {
					sh.SystemAssets["bot"] = id
					changed = true
				}
			}
			if strings.TrimSpace(sh.SystemAssets["panel"]) == "" {
				if id := ensurePanelProp(plan, sh); id != "" {
					sh.SystemAssets["panel"] = id
					if !hasStr(sh.PropIDs, id) {
						sh.PropIDs = append(sh.PropIDs, id)
					}
					changed = true
				}
			}
			if changed {
				n++
			}
		}
	}
	return n
}

// ensureChibiCard 确保 `chibi_<baseID>` 这张 Q 版卡存在，返回它的 id（建不出来返回空）。
//
// 卡的提示词**从主卡派生**：Q 版的身份必须来自主卡（同一张脸的另一套画法），
// 而不是让模型重新描述一个人 —— 这里只补"Q 版画法"的取景与头身比，身份照抄主卡的 IDENTITY LOCK。
func ensureChibiCard(plan *Plan, baseID string) string {
	id := "chibi_" + baseID
	if plan.HasCharacterID(id) {
		return id
	}
	base, ok := findCharacter(plan, baseID)
	if !ok {
		return ""
	}
	lock := identityLockOf(base.ImagePrompt)
	prompt := "chibi / super-deformed version of the SAME character, 2.5-3 head-tall proportions, " +
		"big head, small body, same face features, same hair, same costume palette and same signature marks, " +
		"full body on plain white background, soft even light, no text, no watermark. " + lock
	plan.Characters = append(plan.Characters, Character{
		ID:          id,
		Name:        base.Name + "·Q版",
		Kind:        "chibi",
		Description: "同一角色的 Q 版形象（内心活动镜由它演出）",
		ImagePrompt: prompt,
		Voice:       base.Voice,
	})
	return id
}

// ensureSystemBotCard 确保系统镜的机器人卡存在（技能侧约定 `chibi_sys_01`）。
func ensureSystemBotCard(plan *Plan) string {
	const id = "chibi_sys_01"
	if plan.HasCharacterID(id) {
		return id
	}
	plan.Characters = append(plan.Characters, Character{
		ID:          id,
		Name:        "系统机器人",
		Kind:        "chibi",
		Description: "半透明发光的小型机器人化身（系统提示的演出体）",
		ImagePrompt: "a small translucent glowing chibi robot mascot, rounded body, soft cyan light, " +
			"full body on plain white background, even light, no text, no watermark",
	})
	return id
}

// ensurePanelProp 确保系统镜的"面板"道具卡存在（1:1 白底单件，与道具链同一套要求）。
func ensurePanelProp(plan *Plan, sh *Shot) string {
	id := "prop_panel_" + strings.TrimPrefix(strings.TrimSpace(sh.Scene), "scene_")
	if plan.HasPropID(id) {
		return id
	}
	name := "系统面板"
	if sc, ok := findScene(plan, sh.Scene); ok && strings.TrimSpace(sc.Name) != "" {
		name = sc.Name + "·系统面板"
	}
	plan.Props = append(plan.Props, Prop{
		ID:          id,
		Name:        name,
		EN:          "a floating holographic panel with a thin glowing frame",
		Description: "悬浮的半透明发光面板，边框细亮线，**面板上不出现任何文字**（文字进画面必糊成伪汉字）",
		ImagePrompt: "a floating holographic panel with a thin glowing frame, blank surface " +
			"(absolutely no letters, no numbers, no UI text), 1:1 square, isolated on plain white background",
	})
	return id
}

func findCharacter(plan *Plan, id string) (Character, bool) {
	for _, ch := range plan.Characters {
		if ch.ID == id {
			return ch, true
		}
	}
	return Character{}, false
}

func findScene(plan *Plan, id string) (Scene, bool) {
	for _, sc := range plan.Scenes {
		if sc.ID == id {
			return sc, true
		}
	}
	return Scene{}, false
}

// identityLockOf 取卡片里的 IDENTITY LOCK 段（没有就退回整条提示词的首句）。
func identityLockOf(prompt string) string {
	if i := strings.Index(prompt, "IDENTITY LOCK"); i >= 0 {
		rest := prompt[i:]
		if j := strings.IndexAny(rest, "\r\n"); j > 0 {
			rest = rest[:j]
		}
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(prompt)
}

func hasStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

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

// SceneCaptionFor 第 i 镜要不要打**场次字幕**，要打就打什么字（空串 = 不打）。
//
// 判据：首镜或**场次相对上一镜发生变化**才打。来源是用户《【额外】仿真人10S分镜头指令》与
// 《五、3D漫剧15秒分镜指令》："不同场景间通过渐隐渐显转场，如需地点时间信息，可在画面描述中
// 以字幕形式自然呈现"。
//
// 【放在 project 而不是 pipeline】compose 要用它（字幕轨），而 compose 被 pipeline 依赖 ——
// 判据放这里，pipeline 与 compose 共用一份，不会出现"提示词那边判换场、字幕那边判法不同"。
func SceneCaptionFor(plan *Plan, i int) string {
	if plan == nil || i < 0 || i >= len(plan.Shots) {
		return ""
	}
	sh := plan.Shots[i]
	if i > 0 && strings.EqualFold(strings.TrimSpace(plan.Shots[i-1].Scene), strings.TrimSpace(sh.Scene)) {
		return ""
	}
	for _, sc := range plan.Scenes {
		if sc.ID == sh.Scene && strings.TrimSpace(sc.Name) != "" {
			return strings.TrimSpace(sc.Name)
		}
	}
	return ""
}
