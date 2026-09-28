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

// AnchorChar 锚表里的一个角色：名字 + 那一整块描述（逐项照抄用的原文）。
type AnchorChar struct {
	Name string
	Desc string
}

// ParseCastAnchor 从源文本里解析【人物外形锚表】；没有这张表就返回 nil。
//
// 解析纪律：① 源文本里必须**出现"人物外形锚表"字样**（否则不认，避免把普通文本里
// 偶然的"某某（定位：…）"当角色）；② 每个角色取"标题行 + 其后到下一个标题之前"的整块，
// 那就是要逐项照抄的原文；③ 同名只保留第一次出现的那块。
func ParseCastAnchor(src string) []AnchorChar {
	if !strings.Contains(src, "人物外形锚表") {
		return nil
	}
	hits := anchorTitleRe.FindAllStringSubmatchIndex(src, -1)
	if len(hits) == 0 {
		return nil
	}
	var out []AnchorChar
	seen := map[string]bool{}
	for i, m := range hits {
		name := strings.TrimSpace(src[m[2]:m[3]])
		if name == "" || seen[name] {
			continue
		}
		end := len(src)
		if i+1 < len(hits) {
			end = hits[i+1][0]
		}
		block := strings.TrimSpace(src[m[0]:end])
		if len([]rune(block)) < 20 {
			continue // 太短的块不是角色小节（防误命中）
		}
		seen[name] = true
		out = append(out, AnchorChar{Name: name, Desc: block})
	}
	return out
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
	var added []string
	for _, a := range anchors {
		if have[normCastName(a.Name)] {
			continue
		}
		plan.Characters = append(plan.Characters, project.Character{
			ID:          AnchorCardID(a.Name),
			Name:        a.Name,
			Description: a.Desc,
		})
		have[normCastName(a.Name)] = true
		added = append(added, a.Name)
	}
	return added
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
