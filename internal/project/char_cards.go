package project

import (
	"regexp"
	"strings"
)

// char_cards.go 读技能侧**素材三卡**里的角色定妆串（`素材/角色卡.md`）。
//
// ## 为什么读它（2026-09-27 技能侧追补单 N4）
//
// 技能侧给每个角色出了两个提示词：**① 定妆（身份源）**、② 三视图细节板（一致性校对）。
// 平台出参考图用的是自己 LLM 写的 `image_prompt`（pipeline/entity_prompts.go 的
// TurnaroundPrompt 那一套）——两边都声称以《人物外形锚表》为准，但**没有对账机制**，
// 于是同一张脸可以有两套说法。N4 的建议是"平台读技能侧定妆串，单一真源"（推荐①）。
//
// ## 为什么必须**剥掉卡片自己的风格句**
//
// 卡片的定妆串里带着一段 `GUOFENG ANIMATED-FEATURE ART DIRECTION …`。而平台装配提示词时
// **只让位"比例"、不让位"介质/风格"**（见 pipeline/styleExpandFor 的注释：介质是平台的职责）。
// 若把整段卡片串当 image_prompt，最终提示词里就会出现**两段风格句** —— 同一段提示词里
// 两套口径，模型只会取中值，这正是本项目反复记过的那类"丑"的来源。
// 所以这里只取**风格句之前**的部分（身份、外形、服装、取景、白光底那些）交出去，
// 风格仍由平台按全片口径注入。
//
// ## 边界
//
// 解析失败就返回空表（**不报错**）：这是技能侧的可选交付物，缺了只是"少一个真源"，
// 不该让资产阶段开不了工。

// cardStyleMarker 卡片定妆串里**风格句**的起始标记（平台自己那段介质/风格块的抬头）。
// 与 pipeline/artstyle.go 用的是同一句话，所以拿它当切点两边不会走散。
const cardStyleMarker = "GUOFENG ANIMATED-FEATURE ART DIRECTION"

var (
	// cardSectionRe 卡片里的角色小节：`### 沈砚（主角 · 灾星）`
	cardSectionRe = regexp.MustCompile(`(?m)^###[ \t]+(.+?)[ \t]*$`)
	// cardPortraitRe 小节里的「① 定妆」抬头（后面紧跟一个 ```text 块）
	cardPortraitRe = regexp.MustCompile("(?m)^\\*\\*①[^\\n]*\\*\\*[ \t]*$")
	// cardFenceRe fenced 代码块（```text … ```）
	cardFenceRe = regexp.MustCompile("(?s)```(?:text)?[ \t]*\r?\n(.*?)```")
)

// CardCharacterName 从卡片小节标题里取角色名：`沈砚（主角 · 灾星）` → `沈砚`。
func CardCharacterName(title string) string {
	t := strings.TrimSpace(title)
	if i := strings.IndexAny(t, "（("); i > 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

// StripCardStyleClause 去掉定妆串里的风格句（保留身份/外形/服装/取景/白光底）。
//
// 切点在风格句抬头，切完把尾部空白与多余的逗号收干净 —— 交出去的是"平台拼上自己的风格块
// 就完整"的那部分。没有风格句时原样返回。
func StripCardStyleClause(prompt string) string {
	i := strings.Index(prompt, cardStyleMarker)
	if i < 0 {
		return strings.TrimSpace(prompt)
	}
	head := strings.TrimSpace(prompt[:i])
	head = strings.TrimRight(head, " \t\r\n,，")
	return strings.TrimSpace(head)
}

// ParseCharacterCards 解析 `素材/角色卡.md` → 角色名 → **定妆串**（已剥风格句）。
//
// 只认「① 定妆」那一段的代码块（② 三视图板不在这里用：板上会要求写角色名标签，
// 而平台的三视图板明确不许画字，两条口径不同，不能混用）。
func ParseCharacterCards(md string) map[string]string {
	out := map[string]string{}
	sections := cardSectionRe.FindAllStringSubmatchIndex(md, -1)
	for si, m := range sections {
		title := md[m[2]:m[3]]
		name := CardCharacterName(title)
		if name == "" {
			continue
		}
		// 本节范围：到下一个 `###` 为止
		end := len(md)
		if si+1 < len(sections) {
			end = sections[si+1][0]
		}
		body := md[m[1]:end]
		pm := cardPortraitRe.FindStringIndex(body)
		if pm == nil {
			continue
		}
		fence := cardFenceRe.FindStringSubmatch(body[pm[1]:])
		if fence == nil {
			continue
		}
		if p := StripCardStyleClause(fence[1]); p != "" {
			out[name] = p
		}
	}
	return out
}

// MatchCardPrompt 按角色名找卡片定妆串。名字可能带**造型档后缀**（`沈砚·一品` 是同一角色的
// 另一套服装），所以先精确命中，再退到"点名相同"（`·` 之前那段）—— 但**只在唯一命中时**才退，
// 有歧义就返回 false（宁可不用卡片，也不要把另一个人的脸贴上来）。
func MatchCardPrompt(name string, cards map[string]string) (string, bool) {
	n := strings.TrimSpace(name)
	if n == "" || len(cards) == 0 {
		return "", false
	}
	if p, ok := cards[n]; ok {
		return p, true
	}
	base := n
	if i := strings.Index(base, "·"); i > 0 {
		base = strings.TrimSpace(base[:i])
	}
	if p, ok := cards[base]; ok {
		return p, true
	}
	// 前缀匹配：`沈砚·一品` 找 `沈砚`；命中多于一个就不认（歧义）
	hit, n2 := "", 0
	for k, v := range cards {
		if strings.HasPrefix(n, k) || strings.HasPrefix(k, n) {
			hit, n2 = v, n2+1
		}
	}
	if n2 == 1 {
		return hit, true
	}
	return "", false
}
