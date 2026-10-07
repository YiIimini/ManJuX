package pipeline

import (
	"crypto/md5"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// cast_anchor.go **人物外形锚表对账**：源文本里列了几个角色，方案里就该有几个。
//
// ## 由来（2026-09-28 用户报「6 个角色，LLM 解析出来咋只有一个角色」）
//
// 作者/技能侧给的源文本里带着一张【人物外形锚表 · 硬约束】，逐项写明每个角色的外形，
// 并明确写着「平台直出人物卡时逐项照抄」。而分镜提示词只把源文本**整段**丢给模型，
// **没有把这张锚表当成"必须出卡的角色清单"** —— 模型按"本集出场"挑了 1 个
// （实测那一集：锚表 6 个、方案 1 个，模型还跑了 2000 多秒），而平台**一声不吭**：
// 资产阶段照样报「资产就绪 11/11(角色 1 位)」、渲染时"参考图 0 张角色"、
// 跨集复用更谈不上 —— 直到人眼看着画面才发现少了五个人。
//
// 所以这里做两件事：**解析锚表** + **与方案对账并补齐**。
// 能确定性做到的事不要让模型自觉（这是本项目一贯的口径）：锚表本身就是"硬约束"，
// 里面每个角色的外形描述都是现成的，补一张卡不需要再问模型。

// anchorTitleRe 锚表里每个角色的小节标题：`名字（定位：…）`。
//
// 收紧到「名字 + （定位：」这一种形态：作者的表就是这么写的，而普通正文里很少出现，
// 这样才不会把叙述文本里偶然的括号当成角色表。
var anchorTitleRe = regexp.MustCompile(`(?m)^([^\r\n（(]{1,14})（定位[:：]`)

// ParseCastAnchor 从**源文本**里解析【人物外形锚表】；没有这张表就返回 nil。
//
// 解析本体与设定集读取**共用 `project.ParseCharAnchor`**：同一张表有两种出现位置 ——
// 技能侧的 `设定集/人物外形锚表.md`，以及被作者整段塞进 `source.txt` 的那一份 ——
// 两处格式相同，**不能再各写一套解析**（写两套的后果是改了一处、另一处静默失效；
// 平台已经因为"两套口径"栽过好几次，见 docs/审计-2026-09-28）。
//
// 解析纪律（都在 project.ParseCharAnchor 里）：源文本必须**出现"人物外形锚表"字样**；
// 只认 `名字（定位：…）` 这种小节标题，正文里的普通括号不算；同名只留第一次出现的那块。
func ParseCastAnchor(src string) []project.SettingChar {
	return project.ParseCharAnchor(src)
}

// AnchorCardID 锚表补出来的角色卡 id：`char_` + 名字哈希。
//
// 为什么不用拼音、也不用序号：锚表里的角色**没有技能侧卡片 id**，而 id 必须
// **跨集、跨项目稳定**（它是资产库复用的关联真源，见 library 的 ID 口径）——
// 名字哈希满足这一点；序号会随锚表顺序变化，一变就等价于换了一个人。
func AnchorCardID(name string) string {
	sum := md5.Sum([]byte(strings.TrimSpace(name)))
	return "char_" + hex.EncodeToString(sum[:])[:8]
}

// EnsureAnchorCast 锚表 ↔ 方案对账：方案里缺的角色**补进 plan.Characters**
// （用锚表原文当角色描述），返回补进去的名字（按锚表顺序）。
//
// 判据是**归一化后的名字相等**：模型写的名字可能与锚表差一个空格或标点（那是同一张卡，
// 不该补出第二张）。已经是同一个人的（名字或 id 命中）一律不动。
func EnsureAnchorCast(plan *project.Plan, src string) []string {
	if plan == nil {
		return nil
	}
	anchors := ParseCastAnchor(src)
	if len(anchors) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, ch := range plan.Characters {
		have[normCastName(ch.Name)] = true
		have[normCastName(strings.TrimPrefix(ch.ID, "char_"))] = true
		have[normCastName(ch.ID)] = true
	}
	// **给已存在、但没有英文画图提示词的卡补上**（不覆盖模型已经写好的那些）。
	//
	// 为什么这一步必须有：锚表补齐是**后来才加的**，而它在第一版里只搬了中文 `description` ——
	// 那批已经生成好的卡至今 `image_prompt` 还是空的，出图时模型拿不到任何外貌依据
	// （用户 2026-09-28 报的「中年人渲染出来跟个小年轻一样」就是这么来的）。
	// 放在这里而不是写个一次性脚本：**下一次跑分镜就会自动修好**，不必等谁记得去修数据。
	byName := map[string]project.SettingChar{}
	for _, a := range anchors {
		byName[normCastName(a.Name)] = a
	}
	for i := range plan.Characters {
		ch := &plan.Characters[i]
		if strings.TrimSpace(ch.ImagePrompt) != "" {
			continue // 已有的英文提示词是模型写的，比这里能拼的更细 —— 一个字都不覆盖
		}
		if a, ok := byName[normCastName(ch.Name)]; ok {
			ch.ImagePrompt = anchorImagePrompt(a)
		}
	}
	var added []string
	for _, a := range anchors {
		if have[normCastName(a.Name)] {
			continue
		}
		plan.Characters = append(plan.Characters, project.Character{
			ID:          AnchorCardID(a.Name),
			Name:        a.Name,
			Kind:        anchorCardKind(a),
			Description: a.Raw,
			// **英文画图提示词**：没有它，出图时模型拿不到任何外貌依据（用户报的
			// 「中年人渲染出来跟个小年轻一样」就是这么来的）。见 anchorImagePrompt 的说明。
			ImagePrompt: anchorImagePrompt(a),
		})
		have[normCastName(a.Name)] = true
		added = append(added, a.Name)
	}
	return added
}

// FillAnchorDescriptions 把锚表原文补进**已存在但描述为空**的角色卡，返回补了哪几个（按方案顺序）。
//
// 【为什么必须有这一步】（2026-10-07 用户问：「人物生成的角色提示词去哪了？墨洄、尉迟彻、断桅、
// 邵舷、霜见等不是主角的角色都随机生成的吗？」）
//
// 真机第01集现场：这五张卡 `description` 是**空的**、`image_prompt` 是锚表兜底串
// （`[ANCHOR-STUB] a Chinese period-film character, aged 58 to 66`）。后果是三连：
//
//	① 专职翻译会话（NeedsPromptTranslation 要求"有描述可翻"）**永远不会**为它们启动；
//	② 出图装配只拿到"一个古装人、58–66 岁" ⇒ 脸是现编的，跨镜/跨集都不一致；
//	③ 撞脸门当场报警（真机日志：「墨洄」与「霜见」长得像同一个人，重出一次仍像，已收手）。
//
// 而锚表里这五个人的外形是**逐项写全**的（脸型/眉眼/鼻唇/肤色/记号/发型/服装，每人 275~471 字）。
// 上游为什么漏了：EnsureAnchorCast 只在"**新增卡片**"那条路写 Description —— 名字已经存在于方案里时
// （②人物道具会话照名字出了卡、但没写描述），它只补英文兜底串，**从不把锚表原文补进 description**，
// 于是"硬约束"到了卡上就断了。
//
// 判据与 EnsureAnchorCast 同一套：按**归一化名字**匹配；**只填空的** —— 已有的描述一个字都不覆盖
// （那是作者/模型的口径）。幂等：补过之后再调返回空。
func FillAnchorDescriptions(plan *project.Plan, src string) []string {
	if plan == nil {
		return nil
	}
	anchors := ParseCastAnchor(src)
	if len(anchors) == 0 {
		return nil
	}
	byName := map[string]project.SettingChar{}
	for _, a := range anchors {
		if k := normCastName(a.Name); k != "" {
			byName[k] = a
		}
	}
	var filled []string
	for i := range plan.Characters {
		ch := &plan.Characters[i]
		if strings.TrimSpace(ch.Description) != "" {
			continue // 已有描述：不覆盖
		}
		a, ok := byName[normCastName(ch.Name)]
		if !ok || strings.TrimSpace(a.Raw) == "" {
			continue
		}
		ch.Description = a.Raw
		if strings.TrimSpace(ch.ImagePrompt) == "" {
			// 兜底串（只有年龄+头身比）——它同时是"待专职翻译"的标记，见 AnchorStubTag。
			ch.ImagePrompt = anchorImagePrompt(a)
		}
		filled = append(filled, ch.Name)
	}
	return filled
}

// TruncatedAnchorSections 锚表里**被剧本正文截断**的小节（源文本本身不全）——如实点名用。
//
// 真机：断桅那一节只写到 `- 身形：瘦，背驼，走路` 就接上了【本集剧情】。平台不会替作者编外形，
// 但必须说清"这一张的身份真源不全"，否则用户只会看到"这张脸跟设定对不上"。
func TruncatedAnchorSections(src string) []string {
	var out []string
	for _, a := range ParseCastAnchor(src) {
		if a.Cut {
			out = append(out, a.Name)
		}
	}
	return out
}

// anchorCardKind 锚表补出来的卡片的**实体类型**。
//
// 【2026-09-29 真机实测】锚表是《**人物**外形锚表》，但补出来的卡没有 kind 字段，
// 于是出图时按整段描述推断 —— 而描述里写着「义体接缝纹 / 黑色机械护颈项圈 / 机能外套」，
// 命中 `objectStrongWords` 里的「机械」⇒ **第二主角霜见被判成器物**：定妆照走器物那套
// 取景（"a clean three-quarter product view of the object"，没有脸的身份条款），
// 实测出图是个全身立绘、与本人设定完全不是一回事。
//
// 判据：只看**名字与定位行**（锚表每个小节标题括号里那句「定位：…」）。
// 表里的人是"人"，除非它自己写着别的（器物 / 灵体 / 妖兽 / 灵宠 / 无实体…）；
// 说不清就返回空串，让常规推断接手（那时它会用完整描述）。
func anchorCardKind(a project.SettingChar) string {
	hay := strings.TrimSpace(a.Name) + "\n" + strings.TrimSpace(a.Note)
	if hay == "" {
		return ""
	}
	if InferEntityKind(hay, "", "") == KindHuman {
		return string(KindHuman)
	}
	return ""
}

// anchorImagePrompt 从锚表字段拼出**英文**画图提示词（确定性翻译，不调模型）。
//
// 【2026-09-28 用户报「中年人渲染出来跟个小年轻一样」—— 根因就在这里】
// 上一版 `EnsureAnchorCast` 补出来的卡**只有中文 description**，`image_prompt` 是空的 ——
// 而出图装配只吃英文提示词：模型拿不到任何外貌依据（年龄、头身比全没有），于是自由发挥，
// 把 58–66 岁的老拾荒人画成了小年轻。同一份 plan.json 里两类卡一眼可分：
//
//	· LLM 直出的卡    → "a stocky man **in his forties**" ✓
//	· 锚表补齐的卡    → image_prompt 空 ❌
//
// 锚表的这几个字段是**受控取值**（年龄给岁数区间、比例锚给"8 头身"），所以能确定性译成英文；
// 余下的自由文本字段（脸型/眉眼/服装…）保留在中文 `description` 里，由后续的
// 「②人物道具提取」专职会话译写 —— 那一步才是给它们做跨语言改写的地方，
// 这里只保证"出图起码知道这个人多大、多高"。
// AnchorStubTag 锚表兜底英文提示词的**标记**。
//
// 【为什么要标记】`anchorImagePrompt` 只能确定性翻译"年龄 + 头身比"这两项，其余（脸型/眉眼/
// 发型/服装/记号）是自由文本、翻不了 —— 于是它产出的是 `a Chinese period-film character,
// aged 18 to 20` 这种**只有年龄的兜底串**。而专职翻译会话（`TranslateCardPrompts`）的判据是
// "ImagePrompt 为空才翻" ⇒ 兜底串一填进去，翻译就永远不会发生，图像模型只拿到"一个古装人、
// 18-20 岁"。
//
// 真机现场（2026-09-29 第 01 集）：②人物道具会话的产出被解析器丢掉（另一处已修），6 个角色
// 全部来自锚表兜底卡 ⇒ **六张定妆照长得几乎一样**，白瓷装甲的女主角被画成普通古装女子
// （用户报「角色咋都生成一样的了，机器人也按照人来生成？」）。
//
// 有了标记：翻译会话把它当"没有提示词"补写；装配点把标记**剥掉**（绝不能把标记本身
// 发给图像模型 —— 那又会变成画面上的字）。
const AnchorStubTag = "[ANCHOR-STUB] "

// StripAnchorStubTag 剥掉兜底标记（幂等；没有就原样返回）。
func StripAnchorStubTag(s string) string {
	return strings.ReplaceAll(s, AnchorStubTag, "")
}

func anchorImagePrompt(ch project.SettingChar) string {
	var parts []string
	if name := strings.TrimSpace(ch.Name); name != "" {
		parts = append(parts, "a Chinese period-film character")
	}
	if age := anchorAgePhrase(ch.Field("年龄")); age != "" {
		parts = append(parts, age)
	}
	if ratio := strings.TrimSpace(ch.Field("比例锚_机器")); ratio != "" {
		parts = append(parts, headsTallPhrase(ratio))
	}
	if len(parts) == 0 {
		return ""
	}
	return AnchorStubTag + strings.Join(parts, ", ")
}

// reAgeRange 锚表里的年龄区间（"58–66 岁"、"17–19 岁"；连字符可能是 – — ~ -）。
var reAgeRange = regexp.MustCompile(`(\d{1,3})\s*[–—~\-]\s*(\d{1,3})`)

// reAgeSingle 单值年龄（"35 岁上下"、"四十上下"没有数字，那种走词表）。
var reAgeSingle = regexp.MustCompile(`(\d{1,3})\s*岁`)

// anchorAgePhrase 把锚表的"年龄"字段译成英文短语；取不到数字时退回年龄档位词。
func anchorAgePhrase(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if m := reAgeRange.FindStringSubmatch(raw); m != nil {
		return "aged " + m[1] + " to " + m[2]
	}
	if m := reAgeSingle.FindStringSubmatch(raw); m != nil {
		return "aged about " + m[1]
	}
	// 没有数字（"中年偏青"、"老年"）：按档位词给一个**明确的年龄尺度**，
	// 否则模型会按训练集里最省事的"年轻好看"回落 —— 那正是用户报的问题。
	switch {
	case strings.Contains(raw, "老年") || strings.Contains(raw, "花甲"):
		return "an elderly person, clearly past sixty"
	case strings.Contains(raw, "中年"):
		return "a middle-aged person, around forty or older"
	case strings.Contains(raw, "青年"):
		return "a young adult in their twenties"
	case strings.Contains(raw, "少年") || strings.Contains(raw, "少女"):
		return "a teenager"
	case strings.Contains(raw, "儿童") || strings.Contains(raw, "孩童"):
		return "a child"
	}
	return ""
}

// reHeads 锚表比例锚的取值形态（"8 头身"、"7.5 头身"）。
var reHeads = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*头身`)

// headsTallPhrase 把"7.5 头身"译成 "seven and a half heads tall" / "eight heads tall"。
//
// 保留中文原文里的**头身数**，不做四舍五入 —— 头身比是"这个人看起来多高"的硬指标，
// 而锚表明写"全书统一用 7.5 / 8 两档，不许第三种"。
func headsTallPhrase(raw string) string {
	m := reHeads.FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	en := map[string]string{
		"6": "six", "6.5": "six and a half", "7": "seven", "7.5": "seven and a half",
		"8": "eight", "8.5": "eight and a half", "9": "nine",
	}[m[1]]
	if en == "" {
		return m[1] + " heads tall"
	}
	return en + " heads tall"
}

// normCastName 名字归一（去空白与常见分隔标点），只用于"是不是同一个角色"的对账。
func normCastName(s string) string {
	s = strings.TrimSpace(s)
	rep := strings.NewReplacer(
		" ", "", "\u3000", "", "·", "", "・", "", "-", "", "_", "",
		"（", "", "(", "", "）", "", ")", "",
		"「", "", "」", "", "“", "", "”", "", "、", "", ",", "", "，", "",
	)
	return rep.Replace(s)
}
