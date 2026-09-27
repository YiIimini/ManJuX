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
	PlanMaxShotSeconds = 10
	PlanCharsPerSecond = 4.5
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
func ValidatePlan(p *Plan) []Issue {
	var out []Issue
	add := func(level, where, format string, a ...any) {
		out = append(out, Issue{Level: level, Where: where, Msg: fmt.Sprintf(format, a...)})
	}
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

	// 角色/场景 id 集合(悬空引用是最常见的"技能端写自己的、平台端认不得")
	charIDs := map[string]bool{}
	for i, c := range p.Characters {
		if strings.TrimSpace(c.ID) == "" {
			add("fail", fmt.Sprintf("characters[%d]", i), "角色 id 为空(镜头无法引用)")
		}
		if strings.TrimSpace(c.Name) == "" {
			add("warn", fmt.Sprintf("characters[%d]", i), "角色名为空(id=%s)", c.ID)
		}
		if strings.TrimSpace(c.ImagePrompt) == "" {
			add("warn", fmt.Sprintf("characters[%d]", i),
				"角色 %s 缺 image_prompt —— 定妆照只能靠描述现编,跨镜一致性会掉", c.Name)
		}
		charIDs[c.ID] = true
	}
	sceneIDs := map[string]bool{}
	for i, s := range p.Scenes {
		if strings.TrimSpace(s.ID) == "" {
			add("fail", fmt.Sprintf("scenes[%d]", i), "场景 id 为空(镜头无法引用)")
		}
		if strings.TrimSpace(s.ImagePrompt) == "" {
			add("warn", fmt.Sprintf("scenes[%d]", i), "场景 %s 缺 image_prompt —— 环境参考图只能现编", s.Name)
		}
		// ── 场景板(T2I)的汉字门（2026-09-27 技能侧整改单 §1.4 / 平台侧整改清单 §6.2）──────
		//
		// 场景板是文生图出的**环境参考图**，会被 H3 当成"这个地方本来就长这样"带进**每一镜** ——
		// 板上糊一个乱码字形，那一集每一镜都带着它（与"场景板里画了人 → 每镜都出现同一个陌生人"
		// 是同一条机理，见 pipeline.StripSceneCrowd）。
		//
		// 与 `<d>` 外汉字门**不同**的是这里的口径更严：**出现任何汉字即提示，不扣主体名**。
		// 扣减集合是给 h3_prompt 用的（官方要求用文字点名主体，名字里的汉字不可避免），
		// 而场景卡的 image_prompt 里没有"必须写中文名"的理由 —— 渲染期点名靠英文结构与
		// PICTURE MANIFEST。实测《求你们骂我》12 张场景卡每张都以中文名开头（合计 178 字），
		// 就是因为原先只有总阈值门、且把主体名算成合法。
		if n := CountCJK(s.ImagePrompt); n > 0 {
			add("warn", fmt.Sprintf("scenes[%d]", i),
				"场景 %s 的 image_prompt 里有 %d 个汉字 —— 场景板是 T2I 出的，汉字会被画成"+
					"乱码字形糊在环境板上，而这块板会被带进用到该场景的每一镜；"+
					"画面提示词一律写英文（平台装配时会自动清掉，但产出侧就该是干净的）", s.Name, n)
		}
		sceneIDs[s.ID] = true
	}

	// 汉字卫生门的**扣减集合**：主体名（角色 + 场景 + 道具）。
	//
	// 官方要求「用文字点名主体」（渲染期 PICTURE MANIFEST 靠它对编号），9 个角色同镜时
	// 名字本身就近百字，而且一个名字在提示词里会被反复提到 —— 那是**不可避免**的汉字。
	// 不扣会天天误报，真出问题时反而被淹掉（误拦的规则比没有规则更糟）。
	// 判据与阈值见 prompt_cjk.go（与技能侧 manju_plan.py 同源）。
	subjectNames := make([]string, 0, len(p.Characters)+len(p.Scenes)+len(p.Props))
	for _, c := range p.Characters {
		subjectNames = append(subjectNames, c.Name)
	}
	for _, s := range p.Scenes {
		subjectNames = append(subjectNames, s.Name)
	}
	for _, pr := range p.Props {
		subjectNames = append(subjectNames, pr.Name)
	}

	seen := map[int]bool{}
	for i, sh := range p.Shots {
		where := fmt.Sprintf("shots[%d](id=%d)", i, sh.ID)
		if sh.ID <= 0 {
			add("fail", where, "镜头 id 必须为正整数(它同时是产物文件名 NNN.mp4)")
		} else if seen[sh.ID] {
			add("fail", where, "镜头 id 重复 —— 后渲的会覆盖前一个的产物")
		}
		seen[sh.ID] = true

		if strings.TrimSpace(sh.Scene) == "" {
			add("warn", where, "未指定 scene —— 该镜拿不到环境参考图(空镜请确认是有意的)")
		} else if !sceneIDs[sh.Scene] {
			add("fail", where, "引用了不存在的场景 %q(参考图会挂空)", sh.Scene)
		}
		for _, cid := range sh.Characters {
			if !charIDs[cid] {
				add("fail", where, "引用了不存在的角色 %q(定妆照会挂空,跨镜换脸)", cid)
			}
		}

		if sh.Duration < PlanMinShotSeconds || sh.Duration > PlanMaxShotSeconds {
			add("fail", where, "镜头时长 %d 秒超出 H3 合法区间 %d–%d 秒",
				sh.Duration, PlanMinShotSeconds, PlanMaxShotSeconds)
		}
		if strings.TrimSpace(sh.H3Prompt) == "" {
			add("fail", where, "h3_prompt 为空 —— 该镜没有可提交的提示词")
		}
		switch sh.Mode {
		case "ref2va", "t2va", "":
		default:
			add("warn", where, "mode=%q 不是 ref2va/t2va（空值平台会自行推断）", sh.Mode)
		}
		if s := strings.TrimSpace(sh.ShotSize); s != "" {
			ok := false
			for _, v := range PlanShotSizes {
				if strings.Contains(s, v) {
					ok = true
					break
				}
			}
			if !ok {
				add("warn", where, "景别 %q 不在白名单(%s)—— 参考图拼合会按「保留其它角度」处理",
					s, strings.Join(PlanShotSizes, "/"))
			}
		}
		// ── 主视角（2026-09-27 技能侧整改单 §1.1）──────────────────────────
		//
		// 渲染真正读的是 h3_prompt，所以覆盖率判据以提示词为准（聚合在循环外报一次）；
		// 这里只查两件"精确且少见"的事：字段不在官方词表里、字段与提示词对不上。
		// 后者等于自检面板在骗人 —— 审计时看到 view 写了鸟瞰，成片却是平视。
		if v := strings.TrimSpace(sh.View); v != "" {
			known := false
			for _, want := range PlanViews {
				if v == want {
					known = true
					break
				}
			}
			if !known {
				add("warn", where, "view=%q 不是官方 9 视角词表里的串（必须逐字写）—— %s", v, PlanViewHint())
			} else if got, ok := PlanViewIn(sh.H3Prompt); ok && got != v {
				add("warn", where,
					"view 字段写的是 %q，而 h3_prompt 里出现的是 %q —— 两者必须一致（渲染读的是提示词）",
					v, got)
			}
		}
		// ── 尾帧状态里的听觉/氛围词（§1.2 只写看得见的）──────────────────────
		if hit := EndStateBannedIn(sh.EndState); len(hit) > 0 {
			add("warn", where,
				"end_state 里有听觉/氛围/情绪词 %s —— 尾帧状态只写看得见的：人物位置、姿态、"+
					"表情、视线方向、环境状态（下游画不出「寂静」「压抑」这类词）", strings.Join(hit, "、"))
		}
		// ── 语音层(v2)────────────────────────────────────────────────────
		//
		// 这三条是"配音错"的可判形态。用户报的是「系统旁白,主角动嘴?」——
		// 提示词措辞约束不住它(模型只看见一句 <d>),所以把机器判得出来的部分
		// 写成 fail:嘴巴开在谁身上,由"这句话是谁说的、他在不在画面里"决定。
		charInShot := map[string]bool{}
		for _, cid := range sh.Characters {
			charInShot[cid] = true
		}
		for j, l := range sh.Dialogue {
			where := fmt.Sprintf("%s.dialogue[%d]", where, j)
			if strings.TrimSpace(l.Text) == "" {
				add("warn", where, "台词文本为空")
				continue
			}
			name := strings.TrimSpace(l.Speaker)
			if name == "" {
				add("warn", where, "台词没有说话人")
				continue
			}
			if IsVoiceoverSpeaker(name) {
				// 画外音标签(旁白/系统/画外音/内心):必须是 os=true。
				// 否则平台按画内台词处理 → 模型在画面里找个角色张嘴说旁白。
				if !l.OS {
					add("fail", where,
						"说话人是画外音标签 %q 却没标 os=true —— 渲染会当画内台词处理,"+
							"让画面里的人张嘴替旁白说话(用户报的「系统旁白,主角动嘴」)", name)
				}
				continue
			}
			// 说话人是具体角色:必须在画面里(os=false 时)。
			if !l.OS {
				found := false
				for _, ch := range p.Characters {
					if ch.ID == name || ch.Name == name {
						found = true
						if !charInShot[ch.ID] {
							add("fail", where,
								"说话人「%s」不在本镜 characters 里 —— 画面里没有他,"+
									"模型会随便找个人动嘴(要么把他加进本镜,要么标 os=true 当画外音)", ch.Name)
						}
						break
					}
				}
				if !found {
					// 完全一致匹配是**有意保留**的口径（2026-09-27 技能侧整改单 §2.5 / 平台侧整改清单 §6.1）：
					// 技能侧 v2.9 起落盘即写卡名（`沈砚` → 该章生效的 `沈砚·七品`），实测两本
					// "说话人查不到卡 = 0 句"。平台若在这里**自动改写** speaker，字幕与台词归属
					// 就会与产出侧不一致 —— 所以只做诊断：家族基名命中时把"认得出是同一个人"
					// 这件事说清楚（`沈砚` vs `沈砚·七品`），让人一眼知道该改哪里，绝不动数据。
					if hit := familyMatchName(p, name); hit != "" {
						add("warn", where,
							"说话人 %q 与卡名「%s」不是完全一致，但家族基名一致（按 %s 判定为同一人）——"+
								"音色绑定靠这个名字连线，产出侧落盘时写卡名即可（技能侧 v2.9 已改）；"+
								"平台不改写 dialogue[].speaker（改写会让字幕/台词归属与产出侧不一致）",
							name, hit, CharFamilyBaseName(name))
					} else {
						add("warn", where, "说话人 %q 不在角色表里(只有画外音标签可以这么写)", name)
					}
				}
			}
		}
		// 有台词/旁白却连 <d> 都没有 → fail:平台后处理补不了(它不知道该往哪插 <d>),
		// H3 会即兴挑语言,成片是听不懂的鸟语。
		// 只是**缺 [Chinese] 标记**则归 warn —— 后处理 EnsureChineseDialogue 会把每个 <d>
		// 都补成 <d>[Chinese],那是修复得了的事,不该拒收(误拦比漏拦更糟)。
		if (len(sh.Dialogue) > 0 || strings.TrimSpace(sh.Narration) != "") &&
			!strings.Contains(sh.H3Prompt, "<d>") {
			add("fail", where,
				"本镜有台词/旁白,h3_prompt 里却没有任何 <d> 标记 —— 成片会说成听不懂的话")
		}
		// 画外音镜:提示词里必须有"嘴唇闭合"子句(平台后处理会补,但产出侧就该写对)。
		if HasVoiceover(sh) && !strings.Contains(sh.H3Prompt, VoiceoverClauseMarker) {
			add("warn", where,
				"本镜有画外音但 h3_prompt 没写「%s」—— 平台后处理会补齐,产出侧写对更稳",
				VoiceoverClauseMarker)
		}
		// ── 汉字卫生（2026-09-26 用户报障「成片出现无名字幕」）──────────────────
		//
		// 官方 ON-SCREEN TEXT 规则的判据是：**没被英文引号括起来的文字，就是模型会画成
		// 字形的来源**；而汉字合法的位置只有 `<d>[Chinese]…</d>`（官方「说」的通道，
		// 同时供 H3 出原生中文配音）与**主体名**（官方要求点名主体）。所以 `<d>` 外的
		// 可避免汉字 = 往画面上写字。实测《翻篇》第 01 集 `<d>` 外 7083 字：根因不是台词，
		// 是场景卡的中文美术卡整段被拼进了 subject_definitions。
		//
		// 阈值与扣减集合**与技能侧 manju_plan.py 同源**（>200 fail / >60 warn，扣主体名），
		// 见 prompt_cjk.go。技能侧在产出前跑同一条，平台侧在这里守门 —— 这样**别的书**与
		// 平台 LLM 直出路径也能被拦住，而不是只有技能侧新出的包干净。
		//
		// 为什么不顺手升 PlanContractVersion：技能侧 v2.3 是在**同一契约版本**下加的这条门，
		// 单侧升版本会把现网所有 v2 包判成「版本不符」而整批拒收 —— 那与本条的意图无关。
		if n := CJKOutsideDialogue(sh.H3Prompt, subjectNames); n > PlanCJKFailOutsideDialogue {
			add("fail", where,
				"h3_prompt 里 <d> 外有 %d 个**可避免的**汉字（主体名已扣除）—— 这就是成片画面上"+
					"冒出无名字幕/乱码字形的直接来源：英文结构散文里不许有汉字，中文美术卡别塞进提示词",
				n)
		} else if n > PlanCJKWarnOutsideDialogue {
			add("warn", where, "h3_prompt 里 <d> 外有 %d 个可避免的汉字（主体名已扣除）", n)
		}
		// keyframe_prompt 是**首帧 T2I 静帧**提示词：它没有 `<d>` 这条合法通道，而图生图
		// 模型画中文几乎必然成乱码 —— 这一镜的第一帧就带字。所以「出现任何汉字」都提示。
		if n := CountCJK(sh.KeyframePrompt); n > 0 {
			add("warn", where,
				"keyframe_prompt 里有 %d 个汉字 —— 首帧是 T2I 出的，汉字会被画成乱码字形"+
					"（这一镜的第一帧就带字）", n)
		}
		// 台词装不下:平台侧会抬时长(最多到上限),但抬到顶仍不够时成片会**截断**,
		// 这是"配音不完整"的老病根,所以在入口就如实报出来。
		if need := NeededShotSeconds(sh); need > PlanMaxShotSeconds {
			add("warn", where,
				"台词+旁白需约 %d 秒、超过单镜上限 %d 秒 —— 会被抬到上限并可能截断,建议拆镜",
				need, PlanMaxShotSeconds)
		}
	}

	// ── 覆盖率类判据（整改单 §1.1/§1.2/§1.5）────────────────────────────────
	//
	// 这三组都是"每镜都可能命中"的规则，**按计划聚合报一次**，不逐镜刷屏：
	// 一条 56 镜的方案若逐镜报"缺尾帧状态"，报告里 50 条噪音会把真正的那几条淹掉
	// （本包的老规矩：误拦/刷屏的规则比没有规则更糟，因为报告没人看）。
	// 每条都带上"命中几镜 / 共几镜"与示例镜号，够定位也不吵。
	if len(p.Shots) > 0 {
		// 2026-09-27 晚**裁定 A**（技能侧追补单订正版 N5，平台侧为裁定方）：
		// **允许留空**，只对"官方 9 条语义站得住"的镜要求必须有 —— 建立镜头与远景/全景。
		//
		// 为什么不再"每镜必填"：官方那 9 条是**大视角/机位表**（鸟瞰、平视深远、纵深压缩、
		// 条件性的仰视/窥视/强透视/长焦），里面**没有**"平视中近景"，也没有"俯拍"。
		// 一条"平视中景"或"俯拍特写"在表里找不到对应项 —— 硬填就是写错词（把特写写成
		// "超广角斜 45° 鸟瞰"），而**写错词比留空更糟**（留空只是少一句镜头语言，写错是把画面带偏）。
		// 技能侧的实测口径：2462 镜里 1693 镜有官方视角串、769 镜留空（都带 `view_note` 说明原因）。
		var noView []string
		viewSet := map[string]bool{}
		viewShots, optional := 0, 0
		for i, sh := range p.Shots {
			if v, ok := PlanViewIn(sh.H3Prompt); ok {
				viewSet[v] = true
				viewShots++
				continue
			}
			if strings.TrimSpace(sh.H3Prompt) == "" {
				continue
			}
			if ViewRequiredForShot(p.Shots, i) {
				noView = append(noView, fmt.Sprintf("#%d", sh.ID))
			} else {
				optional++
			}
		}
		if len(noView) > 0 {
			add("warn", "plan.shots",
				"%d 镜**该有**机位视角却没写（建立镜头与远景/全景必须写；共 %d 镜留空属正常）—— "+
					"这些镜是交代空间的主力，缺了整集会像用同一台机器拍完（%s；%s）",
				len(noView), optional, PlanViewHint(), strings.Join(firstN(noView, 6), " "))
		}
		// 视角单调：只有一两种视角同样是"同一台机器"的另一种形态。
		// 阈值取 6：整改单的验收是"camera 取值从 2 个涨到 ≥6 个"。
		// **只在"带视角的镜够多"时才判**（裁定 A 之后大量小景别合法留空，样本太薄就不该判单调 ——
		// 否则一条 60 镜里只有 5 镜带视角的方案也会被要求"铺开到 6 种"，那是无理要求）。
		if len(viewSet) < 6 && viewShots >= planViewVarietyMinShots {
			add("warn", "plan.shots",
				"整集只用了 %d 种机位视角（%s）—— 官方词表有 9 条，建议至少铺开到 6 种，"+
					"否则景别变化也救不回「镜头感」", len(viewSet), strings.Join(viewKeys(viewSet), " / "))
		}

		var establishingBad []string
		var establishingClose []string
		for i, sh := range p.Shots {
			if !IsEstablishingShot(p.Shots, i) || strings.TrimSpace(sh.H3Prompt) == "" {
				continue
			}
			if !strings.Contains(sh.H3Prompt, PlanEstablishingExtra) {
				establishingBad = append(establishingBad, fmt.Sprintf("场景 %s 的首镜 #%d", sh.Scene, sh.ID))
			}
			for _, bad := range PlanEstablishingBannedSizes {
				if strings.Contains(sh.ShotSize, bad) {
					establishingClose = append(establishingClose, fmt.Sprintf("场景 %s 的首镜 #%d 是「%s」", sh.Scene, sh.ID, sh.ShotSize))
				}
			}
		}
		if len(establishingBad) > 0 {
			add("warn", "plan.shots",
				"%d 个建立镜头没写建立镜头串 —— 观众第一次看见一个地点时得先交代地点："+
					"逐字写上 %q（%s）", len(establishingBad), PlanEstablishingExtra, strings.Join(establishingBad, "、"))
		}
		if len(establishingClose) > 0 {
			add("warn", "plan.shots",
				"建立镜头用了特写/小景别 —— %s；小景别当建立镜头等于没交代地点，换远景/全景/中景",
				strings.Join(establishingClose, "、"))
		}

		// ── 特写/近景却"没有参考卡的人"（2026-09-27 技能侧追补单 N1）──────────────
		//
		// 判据：`shot_size ∈ {特写, 近景}` 且 `characters` 为空。为什么要一条门：
		// 这类镜里**画面几乎只有一张脸**，而参考图集合是从 `characters` 派生的
		// （见 pipeline.RefPlan）—— 没有人就没有参考图，每次生成**换一张新脸**。
		// 这不是理论风险：技能侧全书 2462 镜里就有 3 镜这样，其中**第一条是全书第一镜、主角的脸**
		// （`第01集 S1 特写 :: 一筐烂菜叶砸在他脸上`，同镜锚点还写着 `empty frame, no people`，
		// 与正文"人群里一张脸被打"自相矛盾）。技能侧提这条时的原话很准：
		// **根因不是笔误，而是这类缺陷没有门** —— 谁写分镜都可能犯，LLM 直出更可能（它每集现写）。
		//
		// 与上面「建立镜头不许特写」形状一致（同样聚合报一条），但两者**不是一回事**：
		// 那条管"小景别当建立镜头"，这条管"有人脸却没卡"。
		var closeNoChar []string
		unbound := 0
		for _, sh := range p.Shots {
			if !IsCloseUpShot(sh.ShotSize) {
				continue
			}
			if len(sh.Characters) == 0 {
				// 技能侧**显式**写了"这个人故意不绑卡、只给背影/局部/人群"时不报 ——
				// 那种镜是"刻意不露脸"，与"忘了绑卡"是两回事，后者才是每次换一张脸的缺陷。
				// 判据优先用**字段**（`unbound` + `unbound_reason`，技能侧 09-27 §9 给的），
				// 再退回早先的 prose 写法（PlanUnboundFaceMarker）—— 字段是稳的，prose 会随措辞漂。
				if sh.Unbound || strings.Contains(sh.H3Prompt, PlanUnboundFaceMarker) {
					unbound++
					continue
				}
				closeNoChar = append(closeNoChar,
					fmt.Sprintf("#%d「%s」%s", sh.ID, strings.TrimSpace(sh.ShotSize), firstRunes(sh.Action, 18)))
			}
		}
		if len(closeNoChar) > 0 {
			extra := ""
			if unbound > 0 {
				// 别让例外静默：明确说出"另外 N 镜是刻意写清不绑卡/不露脸的，已排除"，
				// 否则读日志的人会以为门漏判了。
				extra = fmt.Sprintf("（另有 %d 镜在提示词里显式写了「%s」，属刻意不露脸，已排除）",
					unbound, PlanUnboundFaceMarker)
			}
			add("warn", "plan.shots",
				"%d 镜是特写/近景却没有出场角色（%s）—— 这一档画面里几乎只有一张脸，而没有角色就没有参考图，"+
					"**每次生成都是一张新脸**；要么给这些镜绑上角色卡，要么把景别改到不露脸（背影/局部/群演）"+
					"并把正文里的脸删掉（画面里有人时也不许写 `empty frame, no people` 这种自相矛盾的锚点）%s",
				len(closeNoChar), strings.Join(firstN(closeNoChar, 4), "、"), extra)
		}

		// 尾帧状态：只对"后面还有同一场景的镜头"的那些镜判 —— 跨场景本来就不要求动作连贯
		// （改用渐隐渐显 + 转场字幕），每段场景的末镜与全片末镜不写尾帧状态是对的。
		var noEnd []string
		for i, sh := range p.Shots {
			if i+1 >= len(p.Shots) {
				continue
			}
			next := p.Shots[i+1]
			if strings.TrimSpace(next.Scene) != strings.TrimSpace(sh.Scene) {
				continue // 跨场景：不要求接动作
			}
			if strings.TrimSpace(sh.EndState) == "" {
				noEnd = append(noEnd, fmt.Sprintf("#%d", sh.ID))
			}
		}
		if len(noEnd) > 0 {
			add("warn", "plan.shots",
				"%d 镜缺 end_state（尾帧状态）—— 它们后面紧跟着同一场景的下一镜，"+
					"没有尾帧状态时「接上一镜」只能靠模型猜（缺尾帧的镜：%s）",
				len(noEnd), strings.Join(firstN(noEnd, 6), " "))
		}

		// 连续同景别（整改单 §1.1 验收：连续同景别 ≤6 镜）
		if runSize, runStart, n := longestSameShotSizeRun(p.Shots); n > PlanMaxConsecutiveSameShotSize {
			add("warn", "plan.shots",
				"连续 %d 镜同一个景别「%s」（#%d 起）—— 上限 %d 镜；连续同景别是「画面单调」最直接的成因",
				n, runSize, runStart, PlanMaxConsecutiveSameShotSize)
		}

		// H3 官方格式（§1.5 的可机检三条）：聚合报一次，附前几条人话说明
		var fmtHits []string
		badShots := 0
		for _, sh := range p.Shots {
			if v := H3FormatViolations(sh.H3Prompt); len(v) > 0 {
				badShots++
				for _, s := range v {
					if len(fmtHits) < 4 {
						fmtHits = append(fmtHits, fmt.Sprintf("#%d %s", sh.ID, s))
					}
				}
			}
		}
		if badShots > 0 {
			add("warn", "plan.shots",
				"%d 镜的 h3_prompt 不符合官方格式（写错了不报错，只是成片变差）：%s",
				badShots, strings.Join(fmtHits, "；"))
		}

		// 混进"写给生成器的话"（2026-09-27 晚，验收技能侧重出的脚本时实测）：
		// 技能侧全书 2462 镜的 h3_prompt 都以 `REFERENCE ORDER: … characters array …` 开头，
		// 还夹着 `the renderer appends an authoritative PICTURE MANIFEST…` —— 这些是**写给
		// 拼提示词的人的规则**，官方仓一句都没有、共享契约也没定义。平台读取边界已经会剔掉
		// （project/h3_clean.go，一处清理四条消费路径），但**必须在导入时就告诉人**：
		// 上游不改，每次导出都会再带一遍。聚合报一次，别刷 2462 条。
		dirShots := 0
		var dirSample []string
		for _, sh := range p.Shots {
			if HasH3WriterDirectives(sh.H3Prompt) {
				dirShots++
				if len(dirSample) < 3 {
					dirSample = append(dirSample, fmt.Sprintf("#%d", sh.ID))
				}
			}
		}
		if dirShots > 0 {
			add("warn", "plan.shots",
				"%d 镜的 h3_prompt 里混着**生成器指令**（如 REFERENCE ORDER / PICTURE MANIFEST，%s…）："+
					"渲染时会自动剔除，但根治要脚本侧重新导出 —— 这些行不属于官方提示词结构，官方仓里没有这种写法",
				dirShots, strings.Join(dirSample, "、"))
		}

		// ── 官方「标准镜头信息表」STEP 5.5 的三条可机检自检（2026-09-27）──────────────
		// 依据：`skills/3d-animation-short-generator/references/shot-table-spec.md`。
		// 平台 plan.json 就是那张表（六列 ↔ 我们的字段见 shot_table.go 的对照），
		// 所以官方的强制自检可以直接落在契约上 —— "这一集是不是平/有没有一镜塞太多人"
		// 从"靠感觉说"变成可判。
		//
		// 检查 1：钩子密度。每镜都要有 hook_type；每连续三镜至少一个 reveal/reversal/callback；
		// 开篇镜与收尾镜必须是强钩子。
		var noHook, badHook []string
		for _, sh := range p.Shots {
			h := hookTypeOf(sh)
			if h == "" {
				noHook = append(noHook, fmt.Sprintf("#%d", sh.ID))
			} else if !IsKnownHookType(h) {
				badHook = append(badHook, fmt.Sprintf("#%d=%s", sh.ID, h))
			}
		}
		if len(noHook) > 0 {
			add("warn", "plan.shots",
				"%d/%d 镜没有 hook_type —— 官方镜头表要求每镜一个钩子类型（词表：%s；缺钩子的镜：%s）",
				len(noHook), len(p.Shots), strings.Join(PlanHookTypes, "/"), strings.Join(firstN(noHook, 6), " "))
		}
		if len(badHook) > 0 {
			add("warn", "plan.shots", "hook_type 不在官方受控词表里（%s）：%s",
				strings.Join(PlanHookTypes, "/"), strings.Join(badHook, " "))
		}
		if len(p.Shots) >= PlanHookDensityWindow {
			var flat []string
			for i := 0; i+PlanHookDensityWindow <= len(p.Shots); i++ {
				hit := false
				for _, sh := range p.Shots[i : i+PlanHookDensityWindow] {
					if InList(hookTypeOf(sh), PlanHookDensityTypes) {
						hit = true
						break
					}
				}
				if !hit {
					flat = append(flat, fmt.Sprintf("#%d–#%d", p.Shots[i].ID, p.Shots[i+PlanHookDensityWindow-1].ID))
				}
			}
			if len(flat) > 0 {
				add("warn", "plan.shots",
					"%d 个「连续三镜」里一个 %s 都没有 —— 官方密度自检要求每三镜至少一个，否则这一段是平的（%s）",
					len(flat), strings.Join(PlanHookDensityTypes, "/"), strings.Join(firstN(flat, 5), "、"))
			}
		}
		first, last := p.Shots[0], p.Shots[len(p.Shots)-1]
		for _, edge := range []struct {
			label string
			sh    Shot
		}{{"开篇镜", first}, {"收尾镜", last}} {
			h := hookTypeOf(edge.sh)
			if h != "" && !InList(h, PlanHookStrongTypes) {
				add("warn", "plan.shots",
					"%s #%d 的钩子是 %q —— 官方要求开篇与收尾都落在强钩子上（%s）",
					edge.label, edge.sh.ID, h, strings.Join(PlanHookStrongTypes, "/"))
			}
		}

		// 检查 3：单镜主要角色数 ≤3（有画面动作或说台词的角色）。
		// 超了注意力会被摊薄，每个人都画不好，也更容易互相换脸。
		var crowded []string
		for _, sh := range p.Shots {
			if n := len(sh.Characters); n > PlanMaxImportantCharacters {
				crowded = append(crowded, fmt.Sprintf("#%d(%d 人)", sh.ID, n))
			}
		}
		if len(crowded) > 0 {
			add("warn", "plan.shots",
				"%d 镜的主要角色超过 %d 个（官方自检：一个镜头里带动作或台词的角色不超过 3 个）—— %s；"+
					"人多的场面请拆镜，或把不参与动作的人留成环境描述",
				len(crowded), PlanMaxImportantCharacters, strings.Join(crowded, " "))
		}
	}

	// 检查 3'（场景卡侧）：官方 STEP 4 要求场景卡写"连续性地标"与"光态"。
	// 这两项是**跨镜空间一致**的锚点：地标换位置、光态跳时间，观众立刻觉得"不是同一个地方"。
	if len(p.Scenes) > 0 {
		var missing []string
		for _, s := range p.Scenes {
			var lack []string
			if strings.TrimSpace(s.Landmarks) == "" {
				lack = append(lack, "landmarks")
			}
			if strings.TrimSpace(s.LightState) == "" {
				lack = append(lack, "light_state")
			}
			if len(lack) > 0 {
				missing = append(missing, fmt.Sprintf("%s(%s)", s.Name, strings.Join(lack, "+")))
			}
		}
		if len(missing) > 0 {
			add("warn", "plan.scenes",
				"%d/%d 个场景卡缺「连续性地标 landmarks」或「光态 light_state」—— 官方场景卡规范要求两项都写；"+
					"它们是跨镜空间与时间一致的锚点（%s）",
				len(missing), len(p.Scenes), strings.Join(firstN(missing, 5), "、"))
		}
	}

	// 镜头号连续性:平台按 id 落产物文件名,断号不致命但会让"缺镜"看起来像故障。
	maxID := 0
	for _, sh := range p.Shots {
		if sh.ID > maxID {
			maxID = sh.ID
		}
	}
	if maxID > 0 && len(seen) != maxID {
		add("warn", "plan.shots", "镜头号不连续(1..%d 共 %d 个)—— 缺号会让产物清单看起来像丢镜",
			maxID, len(seen))
	}
	return out
}
