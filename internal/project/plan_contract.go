package project

import (
	"fmt"
	"strings"
)

// plan_contract.go 渲染脚本契约(技能端 ↔ 平台端共用的规则表)。
//
// ## 为什么有这一个文件
//
// 2026-09-26 起**技能端直接产出分镜方案**(plan.json),平台端只解析渲染 —— 好处是
// 平台原来那一步「LLM 直出分镜」(每集 2–6 分钟深度推理 + token 成本 + 让渲染端
// 拿一段源文本重新猜人设)被整段省掉。但两侧一旦分工,就出现一个新风险:
//
//	**技能端写出平台端从没见过的东西,渲染照跑,成片是垃圾 —— 然后没人知道该怪谁。**
//
// 所以契约必须是**可执行**的:同一张规则表,两侧各跑一遍。
//   - 技能侧:`scripts/manju_plan.py check`(产出前自检,不许把违规包交出去);
//   - 平台侧:本文件的 ValidatePlan(入口守门,拒收违规包并说清哪一条)。
//
// ## 权威与同步
//
// 权威文档 `docs/渲染脚本契约.md`(技能侧镜像 `references/渲染脚本契约.md`);
// 版本号 PlanContractVersion 两侧必须同号,**改规则 = 改版本 + 两侧同提交**。
// 规则表只写"渲染会因此出错"的事 —— 品味问题不进契约(误拦的规则比没有规则更糟)。

// PlanContractVersion 契约版本。技能端在 manifest.json 里写同名同值。
//
// v2(2026-09-26):语音层。v1 只有 dialogue[{speaker,text}],于是**画外音与画内对白
// 在提示词里长得一模一样** —— 技能端把 61% 的台词判成"旁白"(889 条里 543 条),
// 却仍然写成 <d>[Chinese]…</d> 塞在镜头描述里,模型只能找画面里的人来张嘴:
// 用户报的「系统旁白,主角动嘴」就是这么来的。v2 给每句台词加一个语义位
// (Line.OS:画外音,画面里的人不许张嘴),并把它写进规则表 —— 光靠提示词措辞
// 约束不住,机器可判的必须在契约里。
//
// **改版本 = 两侧同改同提交**;旧包(v1)在入口被拒收并提示重建(技能端 0.2 秒),
// 因为 v1 包的语音层本身就是错的,放进去等于把"主角替旁白动嘴"渲成成片。
const PlanContractVersion = 2

// 与 internal/director/dialogue.go 同源的常量。
//
// **为什么复制而不是 import**:project 是更底层的包,director 反过来 import project,
// 真 import 会成环。所以这里复制常量,并由 `plan_contract_ext_test.go`(外部测试包)
// 断言两者相等 —— 谁把其中一边改了就红。
const (
	PlanMinShotSeconds = 4
	// PlanMaxShotSeconds 单镜上限 **12 秒**（2026-09-29 用户：「分镜渲染时间不超过12S，
	// 最高建议10S左右最合适」）。
	//
	// 口径的三层，别混：
	//   · **硬上限 12 秒** —— 超过就是 fail（H3 官方支持到 15，我们取 12，留出安全余量）；
	//   · **建议值 10 秒左右**（`PlanRecommendedShotSeconds`）—— 非末尾片段仍按
	//     "7/8/9/10 秒" 规划（用户《额外·仿真人10S分镜头指令》原文），超过 10 只在
	//     "台词确实排不开" 时用，且给 warn 提醒；
	//   · **下限 4 秒** —— 低于它就是一次白给的生成。
	//
	// 【改这条要留痕】上一版是 10 秒（2026-09-27 用户拍板"维持 10 秒"）；这次用户明确
	// 放宽到 12、并重申"建议 10 秒左右最合适"。依据是**用户口径**，不是官方文档
	// （官方 shot-table-spec §5.5-② 允许 ≤15 —— 那条不能当依据，见 docs/与官方的有意分歧.md）。
	//
	// ⚠ **有意分歧 DEV-02（勿当 bug 修）**：官方要求单镜 ≤15 秒，平台取 **12**。
	// 理由全文见 docs/与官方的有意分歧.md。
	PlanMaxShotSeconds = 12
	// PlanRecommendedShotSeconds 单镜**建议**上限（10 秒）：超过它不算错，但要提醒 ——
	// 本地 H3 的单镜成本随帧数超线性增长（10 秒 ≈ 6 秒的 2.8 倍），且实测长片段尾部是崩坏高发区。
	PlanRecommendedShotSeconds = 10
	// PlanCharsPerSecond 台词**发声字数/秒**，用于把台词长度折算成时长预算。
	//
	// 【2026-09-28 校正 4.5 → 4.1】两处来源交叉验证后取 4.1：
	//   · 用户《【额外】仿真人10S分镜头指令》给的是区间（平静对话 3.5~4.5 字/秒）——4.1 在区间内；
	//   · H3 的公开输入契约文档（`drama-skills` 的 `references/minimax-h3.md`，MIT）写明
	//     "中文约 **4.1 可发声字/秒**" —— 那是**针对 H3 这个模型**实测出来的值。
	// 取偏小的值更安全：高估语速会让时长预算**偏少**，台词塞不进镜头（成片里表现为赶词/吞字）。
	PlanCharsPerSecond = 4.1
	// PlanSpeechPadSeconds 每段口播的收尾余量,免得"刚好卡在最后一个字"。
	PlanSpeechPadSeconds = 0.3
)

// PlanShotSizes 景别白名单(中文)。平台按 `strings.Contains` 判,未知景别按"保留其它角度"
// 处理(见 pipeline.wantsAnglesStrip),所以未知只 warn 不 fail。
var PlanShotSizes = []string{"远景", "全景", "中景", "中近景", "近景", "特写"}

// VoiceoverSpeakers 画外音的"说话人"写法(无人称旁白、系统音、纯画外)。
//
// 与 internal/compose 的字幕逻辑同源:它把 speaker=="旁白" 的行按旁白排版(不写"旁白:")。
// 这里扩到一组:系统提示音/画外音同样不是画面里的某个人在说话。
//
// 【2026-09-30 用户：「视频配音什么鬼？系统一次，主角一次？」】实测《名册之外》第 01 集：
// 系统播报的说话人写的是 **`舱体主控`**（不是"系统"）—— 于是判据漏掉它 ⇒ 平台按**画内台词**
// 处理：找一张叫「舱体主控」的角色卡（不存在）、把系统播报排进角色的音色表，
// 模型就用**角色的嗓子**把系统的话又念了一遍 ⇒ 听到的就是"系统一次、主角一次"。
//
// 所以判据从"精确等于几个标签"改成**标签 + 系统音词表**（主控/广播/AI/终端/机械音…），
// 与 §系统音镜（vo_kind=system）那条路对齐。
var VoiceoverSpeakers = []string{"旁白", "系统", "画外音", "内心"}

// systemVoiceWords 系统音/播报类说话人的词表（出现在说话人名里就按系统音处理）。
//
// 只收**明确的非人声源**：主控/系统/广播/播报/提示音/电子音/机械音/AI/终端/扬声器/舱内语音…
// 不收"管理员""值班员"这类**可能是真人**的称呼（那种要按角色处理，不能一律当机器音）。
var systemVoiceWords = []string{
	"主控", "系统", "广播", "播报", "提示音", "电子音", "机械音", "合成音", "语音助手",
	"扬声器", "喇叭", "对讲", "电台", "终端", "面板", "ai", "AI", "机器音", "自动回复",
}

// IsVoiceoverSpeaker 说话人写的是"画外音标签/系统音"而不是某个角色名。
func IsVoiceoverSpeaker(speaker string) bool {
	s := strings.TrimSpace(speaker)
	if s == "" {
		return false
	}
	for _, v := range VoiceoverSpeakers {
		if s == v {
			return true
		}
	}
	// 系统音：说话人名里带系统音词（如「舱体主控」「维修终端」「广播」）
	for _, w := range systemVoiceWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// IsSystemVoiceSpeaker 说话人是不是**系统音**（不是旁白/内心那种"人的声音"）。
//
// 用途：系统音要用系统那套参考图（机器/面板，见 refs.go 的 vo_kind=system 路径）与
// 机器音色；旁白/内心则不是。两者都属于"不是画面里的人在说"。
func IsSystemVoiceSpeaker(speaker string) bool {
	s := strings.TrimSpace(speaker)
	if s == "" {
		return false
	}
	for _, w := range systemVoiceWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return strings.TrimSpace(s) == "系统"
}

// VoiceoverClauseMarker 画外音子句的幂等判据(改文案必须保留这个串,否则会重复叠加)。
//
// 措辞照抄 internal/director 的官方写法(它已按 MiniMax 官方 §4.4 校过):
// 「The narrator (S1) says in an off-screen voiceover: <d>[Chinese]…</d> while the
// on-screen characters' lips remain completely closed.」——两侧写同一句,
// 免得"技能端说嘴唇闭合、平台说别的"。
const VoiceoverClauseMarker = "lips remain completely closed"

// Issue 一条契约问题。Level: "fail"(拒收) / "warn"(收但提示)。
type Issue struct {
	Level string `json:"level"`
	Where string `json:"where"`
	Msg   string `json:"msg"`
}

func (i Issue) String() string { return fmt.Sprintf("[%s] %s: %s", i.Level, i.Where, i.Msg) }

// FatalPlanIssues 取**致命**级（`fatal`）：这些会让一次渲染必然白烧或产出废片，
// 所以**必须拦在启动之前**（2026-09-30 用户：「执行」—— 定级口径：
// 只有致命项拦、其余照跑并记账）。
//
// 什么算致命（判据在各自的门里，值就是 `fatal`）：
//   - `h3_prompt 为空`：引擎收到空提示词 ⇒ 出一帧近全黑（第 02 集镜 15 实测 94%）；
//   - `台词文本为空`：模型没有台词可念 ⇒ 自己瞎念（实测 ASR 转出来不是话）。
//
// 为什么不按告警文案过滤：文案会改、判据不会（本包老规矩）—— 所以值写进 Level 里。
func FatalPlanIssues(issues []Issue) []Issue {
	out := make([]Issue, 0, len(issues))
	for _, it := range issues {
		if it.Level == "fatal" {
			out = append(out, it)
		}
	}
	return out
}

// PlanFails 只取 fail 级(入口拒收的判据)。致命级也算 fail（它同样是"不能就这么跑"）。
func PlanFails(issues []Issue) []Issue {
	out := make([]Issue, 0, len(issues))
	for _, it := range issues {
		if it.Level == "fail" || it.Level == "fatal" {
			out = append(out, it)
		}
	}
	return out
}

// PlanIssuesText 把问题表压成一行行人类可读文本(进日志/HTTP 错误体)。
func PlanIssuesText(issues []Issue) []string {
	out := make([]string, 0, len(issues))
	for _, it := range issues {
		out = append(out, it.String())
	}
	return out
}

// OffScreenSpeakers 本镜里**说话的人不在画面内**的说话人（去重，保序）。
//
// 【2026-09-29 用户：「镜头外的说话声音不应该镜头内人动嘴唇」】
// `HasVoiceover` 覆盖的是"这一镜本来就是画外演出"（旁白/内心/系统音）——
// 它**漏掉**了另一种同样常见的情形：台词是**画面外的人**说的（说话人有角色卡，
// 但这一镜没让他出场）。此时 HasVoiceover 为 false ⇒ 不写闭嘴句 ⇒ H3 只看见一句
// `<d>[Chinese]…</d>`，就让画面里的角色张嘴把别人的台词念了出来。
//
// 判据：逐条台词看说话人是否在**本镜的出场名单**里（名单在方案里是 id，说话人是名字，
// 所以两边都要能对上：id / 名字 / 去括注的基名）。
//
// 【2026-10-07 修（第一半）：不再把 os 行整个甩给 HasVoiceover】原来这里有 `if l.OS … continue`，
// 理由是"os 标记交给 HasVoiceover 那条路管" —— 而 `HasVoiceover` 在 `vo_kind=speech` 时
// **直接 return false**（那是 2026-09-29 为治"哑剧"定的：speech 镜里画面里有人在说）。
// 两者叠起来就是一个洞：真机第01集 12 句「舱内AI」的台词都标着 os=true、说话人不在本镜，
// 却既不进 HasVoiceover、也不进这里 ⇒ **一句"闭嘴"约束都没有**，全靠模型自己在英文正文里
// 写一句 off-screen（那一集它写了，但平台不能指望这个）。
// 现在 os 只是"模型自己标的画外"，**判定权在平台**（说话人在不在出场名单里）。
//
// 【2026-10-09 修（第二半，真机复现）：上半那次只改了注释，代码里的 `l.OS` 还留着】——
// 真机《我捡的团子·第01集》19 句台词**全被模型标成 os=true**（schema 里写的是 os:false），
// 于是 `if l.OS … continue` 让这条判据对**每一镜**都返回空：镜 5 的台词是小灯说的、而它
// 根本不在本镜出场名单里（真画外音），平台却**没补**那句强制的"画内的人保持闭嘴"⇒
// 用户看到的就是「另一人说话、其它角色动嘴」。⇒ os 从此**只影响措辞归属，不再参与跳过**：
// 说话人在不在这一镜，是方案自己（`characters` + `offscreen` 字段）能确定性判定的事实。
func OffScreenSpeakers(plan *Plan, sh Shot) []string {
	if plan == nil {
		return nil
	}
	onScreen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		onScreen[strings.ToLower(s)] = true
	}
	for _, ref := range sh.Characters {
		add(ref)
		add(strings.TrimPrefix(ref, "char:"))
		for _, c := range plan.Characters {
			if c.ID == ref || c.Name == ref {
				add(c.ID)
				add(strings.TrimPrefix(c.ID, "char:"))
				add(c.Name)
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range sh.Dialogue {
		name := strings.TrimSpace(l.Speaker)
		if name == "" || strings.TrimSpace(l.Text) == "" {
			continue
		}
		// 【2026-10-09】**不许拿 `l.OS` 当跳过条件**：那是模型的自述，真机 19 句**全是 true**
		// （见上面第二半）。旁白/系统音这类**标签说话人**照旧跳过（它们由 HasVoiceover 管）。
		if IsVoiceoverSpeaker(name) {
			continue
		}
		// 【2026-10-07 不改这里的原因】曾想把它改成"系统音也算画面外说话人"，
		// 但那样会把「系统音不在本镜」报成"画面里没这个人"（system_voice_test ⑤ 明确要求不许），
		// 而真正缺的那一份约束已经由**逐句声线绑定**补上了：
		// pipeline.BindLineVoices 会把画外那句写成 "<说话人> (Sx) says off-screen in <权威声线>: "，
		// 逐句、确定性；不依赖 vo_kind 短路后的 HasVoiceover。
		if onScreen[strings.ToLower(name)] {
			continue // 他就在画面里，正常对口型
		}
		// 名字带括注（「祁七（主角）」）时按基名再对一次
		base := strings.ToLower(strings.TrimSpace(StripBracketedName(name)))
		if base != "" && onScreen[base] {
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// StripBracketedName 去掉名字里的括注（与 entity_refs 的基名口径一致）。
//
// 导出给 pipeline 用：锚表的 Q版本体 常写成「碑生（丙十七）」这种带括注的形式，
// 而找主卡要的是基名「碑生」（CharFamilyBaseName 只切 · 档名，不切括注）。
func StripBracketedName(s string) string {
	for _, pair := range [][2]string{{"（", "）"}, {"(", ")"}, {"【", "】"}, {"[", "]"}} {
		for {
			i := strings.Index(s, pair[0])
			if i < 0 {
				break
			}
			j := strings.Index(s[i:], pair[1])
			if j < 0 {
				break
			}
			s = s[:i] + s[i+j+len(pair[1]):]
		}
	}
	return strings.TrimSpace(s)
}

// HasVoiceover 本镜是否有画外音(旁白标签 / os=true / narration 非空)。
func HasVoiceover(sh Shot) bool {
	// **vo_kind 优先**（技能侧生成规范 §6.6）：`speech` 是"画面里有人在说" ——
	// 这时哪怕 narration 字段非空（旧管线那个字段大量是"没归到角色卡的话语"），
	// 也不是画外音，**不该**写闭嘴句。反过来 inner/system/narration 一定是真画外。
	// 只有 vo_kind 为空（老方案/技能侧没标）时才退回下面那套字段启发式。
	switch strings.TrimSpace(sh.VoKind) {
	case VoKindSpeech:
		return false
	case VoKindInner, VoKindSystem, VoKindNarration:
		return true
	}
	for _, l := range sh.Dialogue {
		if l.OS || IsVoiceoverSpeaker(l.Speaker) {
			return true
		}
	}
	return strings.TrimSpace(sh.Narration) != ""
}

// NeededShotSeconds 该镜把台词(含旁白)完整说清所需秒数(向上取整,带下限)。
// 与 director.NeededDuration 同规则 —— 契约要求**技能端产出时就已算过**,
// 所以平台侧拿到合格的包,FitDialogueDurations 通常是空转(不静默改用户的东西)。
func NeededShotSeconds(sh Shot) int {
	sec := 0.0
	count := func(s string) {
		n := 0
		for _, r := range s {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u3000' {
				continue
			}
			n++
		}
		if n > 0 {
			sec += float64(n)/PlanCharsPerSecond + PlanSpeechPadSeconds
		}
	}
	for _, l := range sh.Dialogue {
		count(l.Text)
	}
	count(sh.Narration)
	need := int(sec + 0.999) // 向上取整
	if need < PlanMinShotSeconds {
		need = PlanMinShotSeconds
	}
	return need
}

// familyMatchName 说话人的"家族基名"命中了哪张卡（没有则返回空串）。
//
// 用于把 warn 说得更准：技能侧 v2.9 前落盘的是本名（`沈砚`），而卡名可能是档名（`沈砚·七品`），
// 于是"查不到卡"的根因被一句笼统的"不在角色表里"盖住。这里只做**识别**：
// 认出是同一个人就把该改哪里说清楚，**绝不改写** dialogue[].speaker（见调用点注释）。
func familyMatchName(p *Plan, name string) string {
	base := CharFamilyBaseName(name)
	if base == "" {
		return ""
	}
	for _, ch := range p.Characters {
		if ch.Name == "" || ch.Name == name {
			continue
		}
		if CharFamilyBaseName(ch.Name) == base {
			return ch.Name
		}
	}
	return ""
}

// ValidatePlan 契约校验。返回全部问题(按镜头顺序),空数组 = 合规。
// ValidatePlan 契约校验。返回全部问题(按镜头顺序),空数组 = 合规。
//
// ## 结构（2026-09-29 代码健康审计 P2-11）
//
// 这里只做**入口与串联**：空方案守卫 + 按域依次调用判据分组（实现在 `plan_checks.go`）。
// 拆分前它是一个 **1545 行**的函数，一百多道门挤在一条直线过程里 —— 改一处要滚半天、
// 没法按门隔离测试，也藏住了"某一道门的触发分支从没被跑过"这种事
// （G057 就是这么被发现的：整文件 94.5% 覆盖率，单道门的反例 0 次执行）。
//
// **分组顺序就是行为**：判据按原文顺序执行，`plan_checks.go` 里的每一组都是原样搬运
// （连注释一起、一个字符不改）。顺序变了日志读起来就不一样，所以
// `validate_behavior_lock_test.go` 也把顺序锁住了。
func ValidatePlan(p *Plan) []Issue {
	var out []Issue
	add := func(level, where, format string, a ...any) {
		out = append(out, Issue{Level: level, Where: where, Msg: fmt.Sprintf(format, a...)})
	}
	// 下面三条是**门之前**的守卫：它们决定"要不要继续往下判"，不属于任何一组
	if p == nil {
		add("fail", "plan", "方案为空")
		return out
	}
	if len(p.Shots) == 0 {
		add("fail", "plan.shots", "方案没有 shots 数组(渲染无从下手)")
	}
	if strings.TrimSpace(p.Style) == "" {
		add("warn", "plan.style", "视觉风格为空 —— 每镜提示词会缺风格句,画风可能漂")
	}

	// —— 判据分组（顺序即行为，逐组见 plan_checks.go）——
	checkPlanBasics(p, add)
	checkCoverage(p, add)
	checkVoiceover(p, add)
	checkNarrativeQuota(p, add)
	checkVocabGates(p, add)
	checkCardSupplements(p, add)
	checkMeasuredGates(p, add)
	checkCardCompleteness(p, add)
	checkShotCount(p, add)
	checkNoBgmPerShot(p, add)
	checkPositionsAndExit(p, add)
	checkCoreBeats(p, add)
	checkKeyProps(p, add)
	checkSceneBoardRich(p, add)
	checkH3WordCount(p, add)
	// **新增的门一律接在最后**：门编号按源码顺序生成（tools/gatecat），插在中间会让
	// 它后面所有门的编号整体后移 —— 而口头定位（「G014 太严」）与文档引用用的正是编号。
	checkEntityRefs(p, add)
	checkSpokenTextMatchesDialogue(p, add)
	return out
}
