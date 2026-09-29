package project

import (
	"fmt"
	"regexp"
	"strings"
)

// proportion_anchor.go 角色卡的**头身比锚**自检（2026-09-29）。
//
// ## 为什么要有它
//
// 用户点名「碑生（丙十七）、钟离、碑生·Q版、崔无咎 这几个定妆照的比例就不对」。平台侧已把
// **取景尺度**补齐（头占画面约 1/4、肩宽于头），但**锚本身**没人检查：卡面出现
// 「约七头半身」配「矮小驼背」这种自相矛盾的组合时，取景怎么改都画不出对的东西。
//
// 锚来自技能侧交出的【人物外形锚表】（平台侧逐项照抄），所以这道门报出来之后
// **要改的是锚表原文** —— 门的作用是"把矛盾在出图前就暴露"，而不是替谁改数据。
//
// ## 判据（都写成能机检的形式）
//
//  1. 数值必须在合理区间：人形 5–9 头身、Q 版 2–3 头身；超出就是锚写错了（或写成了别的数）。
//  2. 与身形词**不许自相矛盾**：矮小/矮壮/驼背/畏缩 配 ≥7.5 头身、瘦高/高挑/修长 配 ≤6 头身。
//
// 只报事实与建议改法，不替用户改锚（锚是他的口径）。

// headToBodyRatio 从一段中文里取头身比（支持「七头半身」「约八头身」「7.5 头身」「六头身」）。
var (
	ratioRe   = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?|[一二三四五六七八九十半]+)\s*头(?:半)?身`)
	cnDigits  = map[rune]float64{'一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9, '十': 10}
	shortWord = []string{"矮小", "矮壮", "矮胖", "驼背", "畏缩", "侏儒"}
	tallWord  = []string{"瘦高", "高挑", "修长", "高瘦", "极高", "颀长"}
)

// HeadToBodyRatio 取文本里第一个头身比（取不到返回 0,false）。
// 「七头半身」按 7.5 算；「半」出现在数字之后即加 0.5。
func HeadToBodyRatio(text string) (float64, bool) {
	m := ratioRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	raw := m[1]
	// 「七头半身」的"半"被正则里的 `(?:半)?` 吃掉了（不在 raw 里），所以两个分支都要按
	// **整段匹配文本**判断有没有"半"。
	half := strings.Contains(m[0], "半")
	if f, err := parseFloatLoose(raw); err == nil {
		if half && !strings.Contains(raw, ".") {
			f += 0.5
		}
		return f, true
	}
	f := 0.0
	for _, r := range raw {
		if r == '半' {
			f += 0.5
			continue
		}
		v, ok := cnDigits[r]
		if !ok {
			return 0, false
		}
		f += v
	}
	if half && !strings.Contains(raw, "半") {
		f += 0.5
	}
	return f, true
}

func parseFloatLoose(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%g", &f)
	return f, err
}

// CharacterProportionIssue 单张角色卡的头身比锚自检（空串=没问题）。
//
// chibi 由调用方告知（平台的 IsChibiCard 口径），因为同一段文本在两种档位下判据不同。
func CharacterProportionIssue(name, desc string, chibi bool) string {
	ratio, ok := HeadToBodyRatio(desc)
	if !ok {
		return "" // 没写头身比：不判（老包大量没写，报了只是噪音）
	}
	if chibi {
		if ratio < 2 || ratio > 3 {
			return fmt.Sprintf("Q 版卡「%s」的头身比写的是 %.1f 头身 —— Q 版应当是 2–3 头身（大了就不是 Q 版，小了认不出人）",
				name, ratio)
		}
		return ""
	}
	if ratio < 5 || ratio > 9 {
		return fmt.Sprintf("角色卡「%s」的头身比写的是 %.1f 头身 —— 人形合理区间是 5–9 头身，超出这个范围出图没法按它画",
			name, ratio)
	}
	for _, w := range shortWord {
		if strings.Contains(desc, w) && ratio >= 7.5 {
			return fmt.Sprintf("角色卡「%s」自相矛盾：写了「%s」却又写 %.1f 头身（矮小/驼背的人不可能接近八头身）"+
				"—— 出图只会两不像，请把锚表改一致", name, w, ratio)
		}
	}
	for _, w := range tallWord {
		if strings.Contains(desc, w) && ratio <= 6 {
			return fmt.Sprintf("角色卡「%s」自相矛盾：写了「%s」却又写 %.1f 头身（高瘦的人不会只有六头身）"+
				"—— 请把锚表改一致", name, w, ratio)
		}
	}
	return ""
}

// ChibiFields 锚表里的 **Q 版档位**字段（技能侧 2026-09-29 主动提出要补进模板的那套）。
//
// 为什么需要这几个字段（而不是靠名字猜）：
//   - 平台侧要把 Q 版**由主卡定妆照图生图**产出（A6）—— 那就必须**确定性地**知道
//     "这个 Q 版是谁的"，靠名字前缀猜（`碑生·Q版`）在命名一变就断；
//   - 头身比要喂两道判据：出图提示词（Q 版是 2–3 头身）与 `ProportionAnchorIssues` 的区间门；
//   - 常驻位置（「总蹲在碑生左肩上」）要进分镜的空间锚 —— 实测这一条丢了，
//     Q 版在成片里变成"单独蹲在石台上"。
//
// 字段名与写法（技能侧模板照这个补）：
//
//	Q版形象：有|无
//	Q版本体：碑生（丙十七）        ← ref2img 拿谁的定妆照（写名字，平台按名字/去括注基名对卡）
//	Q版比例锚_机器：2.5 头身       ← 与 A7 门同源判据（2–3 头身）
//	Q版位置：总蹲在碑生左肩上      ← 进分镜的空间锚
type ChibiFields struct {
	Has      bool    // Q版形象：有
	Parent   string  // Q版本体：<原角色名>
	Ratio    float64 // Q版比例锚_机器：<数字> 头身
	Position string  // Q版位置：<常驻位置>
}

// ParseChibiFields 从一段锚表原文（或一张 Q 版卡自己的描述）里取 Q 版档位字段。
//
// 容错：中英文冒号都认；"有/无"缺省按"写了本体或比例"也算有。
func ParseChibiFields(text string) (ChibiFields, bool) {
	var f ChibiFields
	found := false
	for _, ln := range strings.Split(text, "\n") {
		k, v, ok := splitAnchorKV(ln)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(k, "Q版形象"):
			found = true
			f.Has = !strings.Contains(v, "无")
		case strings.HasPrefix(k, "Q版本体"):
			found = true
			f.Has = true
			f.Parent = strings.TrimSpace(v)
		case strings.HasPrefix(k, "Q版比例锚"):
			found = true
			f.Has = true
			if r, ok := HeadToBodyRatio(v); ok {
				f.Ratio = r
			}
		case strings.HasPrefix(k, "Q版位置"):
			found = true
			f.Has = true
			f.Position = strings.TrimSpace(v)
		}
	}
	return f, found
}

// splitAnchorKV 拆一行 `键：值`（中英文冒号、前后缀的 - / * / 空白都容忍）。
func splitAnchorKV(line string) (string, string, bool) {
	s := strings.TrimSpace(line)
	s = strings.TrimLeft(s, "-*# \t")
	for _, sep := range []string{"：", ":"} {
		if i := strings.Index(s, sep); i > 0 {
			k := strings.TrimSpace(s[:i])
			v := strings.TrimSpace(s[i+len(sep):])
			if k != "" && v != "" {
				return k, v, true
			}
		}
	}
	return "", "", false
}

// ProportionAnchorIssues 全方案的头身比锚问题（每条一句人话）。
func ProportionAnchorIssues(p *Plan) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, c := range p.Characters {
		chibi := IsChibiCard(c.Kind, c.ID) || strings.Contains(c.Name, "Q版") || strings.Contains(c.Name, "Q 版")
		if s := CharacterProportionIssue(c.Name, c.Description, chibi); s != "" {
			out = append(out, s)
		}
	}
	return out
}
