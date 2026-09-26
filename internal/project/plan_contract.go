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
const PlanContractVersion = 1

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
		for j, l := range sh.Dialogue {
			if strings.TrimSpace(l.Text) == "" {
				add("warn", fmt.Sprintf("%s.dialogue[%d]", where, j), "台词文本为空")
			}
			if strings.TrimSpace(l.Speaker) == "" {
				add("warn", fmt.Sprintf("%s.dialogue[%d]", where, j), "台词没有说话人")
			}
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
