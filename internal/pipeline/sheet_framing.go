package pipeline

import "strings"

// 定妆照取景与"全身口径"比例的冲突处理。
//
// ## 现象与实测(2026-09-26)
//
// 用户报「渲染出来的角色比例那么奇怪」。查下来定妆照出的是**全身立绘**,而本片镜头
// 64 个里 61 个是近景/中景/特写(景别分布:近景 28 / 中景 20 / 特写 13 / 远景 3)——
// 一张全身像被要求"裁"出特写,头肩比、脸宽必然被模型重新诠释。
//
// 再往卡里看,原因很清楚:技能侧的定妆卡**同时**写着两句话——
//
//	…plain pure white seamless background, upper-body framing, front-facing…   (取景:半身)
//	…Proportions must be balanced and idealised: … height is seven and a half heads…(比例:全身)
//
// 半身画面里给全身比例是自相矛盾的,而模型选了"按比例画全身"(实测出图就是全身立绘)。
//
// ## 修法:只剥**全身口径**,不碰脸部比例
//
// 这条链上真正被近景/特写用得上的锚点是**脸**的比例(眼线在中线、眼距＝一眼宽、
// 鼻长 ⅓、嘴 ⅕、下巴宽 ⅔ 颧骨、颈细长)——它们全部保留;要剥掉的只有"整身"那几条
// (七头半、腰腿比例、full body / head-to-toe 一类取景短语),因为半身幅里它们既
// 不可验证、又和取景打架。
//
// 剥的是**平台职责内的取景一致性**,不是改技能侧的语义:技能侧给的脸部比例锚点一条没删,
// 只是把"整身"那部分留给全景/远景镜头(那些镜头另有按景别注入的通道,见 shot_proportion.go)。
//
// ## 为什么是"整项删除"而不是"删短语"(2026-09-26 二次修正)
//
// 第一版只做短语替换,于是技能侧的分号形态
//
//	; the figure is seven and a half heads tall with a slim waist and long legs.
//
// 被剥成 `; the figure is tall with a slim waist and long legs.` —— 剩下一个**平台自造**的
// `tall`,那是往技能侧的口径里塞词,正是本条要避免的事。技能侧的卡片有两种形态
// (分号一条一比例 / 逗号一条一比例),所以这里按 `,` `;` 和句末 `.` 切成小项:
// 命中整身片语的项**连尾部标点整项删掉**,两种形态都干净、都不留残迹。

// fullBodyItemPhrases 命中即**整项删除**的全身口径片语。
//
// 只列"除了整身取景/整身比例没有别的解读"的写法;`proportions`、`head-widths` 这类
// 通用词一概不碰(剥它们会连脸部比例句一起伤到)。
//
// 刻意**不含** `full length` / `full-length`:服装描述里的 `full-length robe`(长袍)
// 与取景无关,剥它会改掉技能侧的服装语义 —— 收益为零、风险实在。
var fullBodyItemPhrases = []string{
	// 七头半的几种写法(技能侧现行 + 常见变体)
	"seven and a half heads", "seven-and-a-half heads", "seven and a half head",
	"seven-and-a-half-heads", "seven and a half-heads", "seven and a half-head",
	"seven-and-a-half-heads-tall", "seven and a half heads tall",
	// 整身腰腿口径(半身幅里看不见;分号形态下与七头半同项,逗号形态下是独立项)
	"slim waist and long legs", "slim waist and legs", "long slim legs",
	// 整身取景短语(半身定妆照里出现即与取景打架)
	"full body", "full-body",
	"head to toe", "head-to-toe", "whole body", "whole-body",
}

// faceAnchorWords 脸部/上半身锚点的特征词:含它们的项**不整项删**。
//
// 这是防误伤的最后一道闸:万一某张卡把脸部比例与整身比例写在同一个逗号项里,
// 整项删会连着"眼线在中线"一起删掉 —— 那比留下残迹严重得多(残迹只是措辞难看,
// 删掉脸部锚点等于这张卡的契约被矮了一截)。这时候退化为短语删除。
var faceAnchorWords = []string{
	"eye line", "eye-width", "eyewidth", "nose", "chin", "mouth", "cheekbone",
	"neck", "shoulder", "eyebrow", "eyelid", "jaw",
}

// StripFullBodyProportions 从定妆照提示词里剥掉全身口径的比例/取景项。
//
// 幂等(剥过一次再剥结果不变);大小写不敏感(技能侧的写法在不同卡上不完全一致)。
// 剥完做标点折叠 —— 删整项会在句子里留下 `, ,` 或 `, and ,` 这类残迹。
func StripFullBodyProportions(prompt string) string {
	items := splitPromptItems(prompt)
	out := make([]string, 0, len(items))
	removed := false
	for _, it := range items {
		if hitsFullBodyItem(it) {
			// 整项删的两个前提:①项里没有脸/上半身锚点;②剥掉整身片语后**没有剩下别的实词**。
			// ②是防"整段被删光":卡里若写成一整句没有分隔符的长描述,整项删会把主角描述一起带走。
			if !hasFaceAnchor(it) && !hasMeaningfulResidual(it) {
				removed = true
				continue // 整项删除(含尾部标点)
			}
			// 混合项:只剥片语,保住项内的其它内容
			out = append(out, stripFullBodyPhrases(it))
			removed = true
			continue
		}
		out = append(out, it)
	}
	got := strings.Join(out, "")
	if removed {
		got = trimDanglingSeparator(got)
	}
	return foldProportionLeftovers(got)
}

// splitPromptItems 把提示词切成"最小项":以 `,` `;` 和**句末** `.`(后跟空白或结尾)
// 为界,分隔符附在**前一项**的尾部。
//
// 分隔符跟着前项走是有意的:整项删除时它会一起消失,不会在前一项末尾留下孤立逗号。
func splitPromptItems(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		cut := false
		switch c {
		case ',', ';':
			cut = true
		case '.':
			// 只有句末的 `.` 才算断点(`1.5`、`U.S.` 一类不许断)
			if i+1 >= len(s) {
				cut = true
			} else {
				switch s[i+1] {
				case ' ', '\t', '\n', '\r':
					cut = true
				}
			}
		}
		if cut {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// hitsFullBodyItem 这一项是不是全身口径。
func hitsFullBodyItem(item string) bool {
	l := strings.ToLower(item)
	for _, p := range fullBodyItemPhrases {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// hasFaceAnchor 这一项里有没有脸/上半身的锚点词 —— 有就不许整项删。
func hasFaceAnchor(item string) bool {
	l := strings.ToLower(item)
	for _, w := range faceAnchorWords {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// stripFullBodyPhrases 混合项的退路:只剥片语,不删整项。
func stripFullBodyPhrases(item string) string {
	out := item
	for _, p := range fullBodyItemPhrases {
		out = replaceAllFold(out, p, "")
	}
	return out
}

// fullBodyStopWords 整身口径项里"剥掉片语后剩下的都是废话"的判定词。
//
// 这些词单独留着没有信息量(`the`, `is`, `height`, `figure`…),所以整项可以删;
// 一旦剩下表外的词,那一项里就还有别的内容,只能退化为片语删除。
var fullBodyStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "being": true, "with": true, "and": true, "of": true, "to": true, "in": true,
	"on": true, "over": true, "his": true, "her": true, "its": true, "their": true,
	"figure": true, "height": true, "tall": true, "body": true, "whole": true, "full": true,
	"shot": true, "framed": true, "shown": true, "visible": true, "here": true, "there": true,
	"slim": true, "waist": true, "long": true, "legs": true, "leg": true,
	"proportions": true, "proportion": true, "idealised": true, "idealized": true, "balanced": true,
	"total": true, "overall": true, "complete": true, "completely": true,
	"this": true, "that": true, "very": true, "so": true, "such": true,
}

// hasMeaningfulResidual 剥掉整身片语之后,这一项里还有没有别的实词。
//
// 非 ASCII(中文/全角)一律当作实词 —— 宁可少删一项,也不要把中文描述整句带走。
func hasMeaningfulResidual(item string) bool {
	out := stripFullBodyPhrases(item)
	var b strings.Builder
	for _, r := range out {
		switch {
		case r > 127:
			return true // 中文/全角:保守,直接判为有内容
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	for _, tok := range strings.Fields(b.String()) {
		if !fullBodyStopWords[strings.ToLower(tok)] {
			return true
		}
	}
	return false
}

// trimDanglingSeparator 删项后若整串(或最后一行)以孤立分隔符收尾,收成句号。
//
// 例:`…, the shoulders about two head-widths,` 后面那一项被删 → 落回 `…head-widths,`。
func trimDanglingSeparator(s string) string {
	trimmed := strings.TrimRight(s, " \t")
	for _, sfx := range []string{", and", ",", ";"} {
		if strings.HasSuffix(trimmed, sfx) {
			trimmed = strings.TrimSuffix(trimmed, sfx)
			trimmed = strings.TrimRight(trimmed, " \t")
			if trimmed == "" {
				return ""
			}
			return trimmed + "."
		}
	}
	return s
}

// foldProportionLeftovers 收拾剥短语留下的连接词与标点残迹。
//
// 例:`…slim waist and long legs, seven and a half heads, …` 剥掉片语后会剩下
// `…slim waist and long legs, , …`,再折叠成 `…slim waist and long legs, …`。
//
// **只压行内空白**:提示词是多段装配的产物 —— 段与段靠空行分开,块内还有行首缩进。
// 早期版本在这里做一次全局 `strings.Fields`,把整段压成了一行、把行首缩进也吃掉了,
// 于是同一个输入第二次装配的结果与第一次不同(块间的空行与 `\n Pose:` 的缩进没了),
// `TestPortraitPromptIsIdempotent` 当场抓住。行首空白一律原样保留。
func foldProportionLeftovers(s string) string {
	out := s
	for i := 0; i < 4; i++ {
		before := out
		for _, pair := range [][2]string{
			{", ,", ","}, {",,", ","}, {" ,", ","}, {", and ,", ","}, {", and,", ","},
			{"; ;", ";"}, {",;", ","}, {";,", ","}, {", .", "."}, {". ,", "."},
		} {
			out = strings.ReplaceAll(out, pair[0], pair[1])
		}
		if out == before {
			break
		}
	}
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		lead := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
		rest := strings.TrimRight(strings.TrimLeft(ln, " \t"), " \t")
		lines[i] = lead + strings.Join(strings.Fields(rest), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
