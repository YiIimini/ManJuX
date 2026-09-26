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
		sceneIDs[s.ID] = true
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
					add("warn", where, "说话人 %q 不在角色表里(只有画外音标签可以这么写)", name)
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
		// 台词装不下:平台侧会抬时长(最多到上限),但抬到顶仍不够时成片会**截断**,
		// 这是"配音不完整"的老病根,所以在入口就如实报出来。
		if need := NeededShotSeconds(sh); need > PlanMaxShotSeconds {
			add("warn", where,
				"台词+旁白需约 %d 秒、超过单镜上限 %d 秒 —— 会被抬到上限并可能截断,建议拆镜",
				need, PlanMaxShotSeconds)
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
