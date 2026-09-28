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
	// PlanMaxShotSeconds 单镜上限 **10 秒**。
	//
	// 官方 `skills/h3-prompt-writing` 支持 **4–15 秒**（并要求"描述覆盖的总时长 == 请求时长"），
	// 技能侧整改单 §2.1 建议实测 12/15 秒后再定上限。**2026-09-27 用户拍板：维持 10 秒**，
	// 不做那轮实测 —— 依据是负载而不是时长本身：本地 H3 的单镜成本随帧数**超线性**增长
	// （同一张卡上 10 秒 ≈ 6 秒的 2.8 倍），而上限放宽只对"台词确实排不开"的少数镜有用，
	// 那类镜头现在的正确处置是**拆镜**（见 NeededShotSeconds 的 warn）。
	// 这条注释就是"已经定过、别再问"的凭据：要改它必须先有 12/15 秒的实测结论。
	// PlanMaxShotSeconds 单镜上限 **10 秒**。
	//
	// ⚠ **有意分歧 DEV-02（勿当 bug 修）**：官方要求单镜 ≤15 秒（shot-table-spec.md §5.5-②），
	// 平台取 10 —— 依据是**用户口径**：分镜文件写"每个片段 7–10 秒、非末尾优先 7/8/9/10 秒"，
	// 且实测长片段尾部是崩坏高发区。要放宽必须用户发话，别拿官方文档当依据改。
	// 理由全文见 docs/与官方的有意分歧.md。
	PlanMaxShotSeconds = 10
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
var VoiceoverSpeakers = []string{"旁白", "系统", "画外音", "内心"}

// IsVoiceoverSpeaker 说话人写的是"画外音标签"而不是某个角色名。
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
	return false
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

// PlanFails 只取 fail 级(入口拒收的判据)。
func PlanFails(issues []Issue) []Issue {
	out := make([]Issue, 0, len(issues))
	for _, it := range issues {
		if it.Level == "fail" {
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
// 拆分前它是一个 **1545 行**的函数，107 道门挤在一条直线过程里 —— 改一处要滚半天、
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
	return out
}
