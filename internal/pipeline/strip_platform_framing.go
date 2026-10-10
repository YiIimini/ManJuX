package pipeline

import "strings"

// strip_platform_framing.go —— 取景口径**只留一处**（幂等）。
//
// 【真机证据，2026-10-10（探针 `TestZZProbeSheetSlim` 读盘上 `asset_map.json` 的成品卡）】
// 盘上 `char:char_shenyan` 的 `image_prompt`（6827 字）里，"胸像 / 头占 1/4 / 裁到胸口"这件事出现在
// **平台自己的两句**里：
//
//	① 开头声明 `BUST PORTRAIT (…) SCALE (mandatory): the head is about ONE QUARTER … The picture
//	   is a CHEST-UP portrait: …` —— 一句话里就把同一件事写了三遍；
//	② 收尾纪律 `FRAMING (mandatory): a front-facing chest-up portrait … ONE QUARTER … CHEST-UP portrait …`。
//
// 探针数出来的份数：`chest-up portrait` ×3、`ONE QUARTER` ×2、裁切口径 ×2、`BUST PORTRAIT` ×1 ——
// 也就是"两句 + 每句内部重复"，**不是**三套独立口径。
// 这两句是 2026-09-27 实测出来的"开头声明 + 末尾收纪律"（只写末尾压不住卡里大段的全身描述，
// 见 `PortraitLeadFraming` 的对照实验），**本轮不动它们**（`portrait_framing_lead_test.go` 守着）。
//
// 本文件处理的是**卡自己带来的**取景句：新写的卡（LLM 把取景行写进 `image_prompt`）、旧措辞的存量句。
// 它们与平台自己的两句并存时，同一段提示词里就有两三套取景口径 —— 模型只会挑一套执行。
// 判据是**句首窗口**（取景标记出现在句子开头 80 字符内 ⇒ 这句就是平台自己的取景声明），
// 而不是"句子里出现过某个词" —— 后者会把含 "neck"/"shoulder" 的**外貌正文**一起吃掉
// （那是脸/上半身锚点，删了这张卡的契约就矮一截，见 sheet_framing.go 里 faceAnchorWords 的由来）。

// framingSentenceStartWindow 取景标记出现在句首这么多字符内，就认定整句是平台取景声明。
const framingSentenceStartWindow = 80

// StripPlatformFramingSentences 剥掉平台自己的取景声明句（**任意位置**，不只是开头；幂等）。
//
// 与 StripPortraitLeadFraming 的分工：那个只处理**前缀**（板子/多视图走的那条路），
// 这个处理卡正文里**混在中间**的取景句。
func StripPlatformFramingSentences(prompt string) string {
	s := strings.TrimSpace(prompt)
	if s == "" {
		return prompt
	}
	var out strings.Builder
	changed := false
	for n := 0; n < 40 && s != ""; n++ { // 上界：畸形卡不许无限循环
		i := firstSentenceEnd(s)
		seg := strings.TrimSpace(s[:i])
		rest := strings.TrimSpace(s[i:])
		if seg == "" {
			s = rest
			continue
		}
		if isPlatformFramingSentence(seg) {
			changed = true
			s = rest
			continue
		}
		out.WriteString(seg)
		out.WriteString(" ")
		s = rest
	}
	if !changed {
		return prompt // 没动过 ⇒ 原样返回（不重排标点，卡原文一个字不变）
	}
	return strings.TrimSpace(out.String())
}

// isPlatformFramingSentence 这句是不是平台自己的取景声明（判据只看**句首窗口**）。
func isPlatformFramingSentence(sentence string) bool {
	head := sentence
	if len(head) > framingSentenceStartWindow {
		head = head[:framingSentenceStartWindow]
	}
	return hitsFramingPhrase(head)
}

// faceIDSegmentHead 定妆照**收尾段**的开头（`PortraitFaceIDClause` 的标记）。
// 取景纪律要插在它**之前** —— 模型对提示词末尾最敏感，而"这张脸是谁"必须是最后一句
// （`cast_distinct_test.go::TestPortraitFaceIDClauseIsLast` 钉着这一点）。
const faceIDSegmentHead = "FACE ID"

// TightenSheetFraming 把**存量成品**里多余的取景声明收成一处（幂等；非人类型原样返回）。
//
// 【为什么还需要它（2026-10-10 用户「定妆照提示词瘦身」）】`PortraitPrompt` 里那条"新建卡"的路径
// 只在**卡还没有平台标记**时跑（`prof.SheetMarker` 判幂等）；而库里已入库的提示词是**成品**，
// 里面同时躺着：
//
//	① 卡自己带来的平台取景句（新写的卡 / 旧措辞的存量句）；
//	② 装配点追加的收尾纪律（**旧措辞**时它也成了"多余的取景句"）。
//
// 真机咬出来的正是这一条：Q 版卡是被人像那套装配的（标记 = IDENTITY FIDELITY），而重新生成走的是
// CHIBI FIDELITY 那条分支 ⇒ 卡里那份人像收尾纪律与新的 Q 版取景纪律**并存**
// （探针实测：6664 字 → 9554 字、`FRAMING (mandatory)` ×2）。
//
// 判据与新建卡那条路径**同一份**（`isPlatformFramingSentence`）：保留当前措辞的收尾纪律
// （没有就补在 FACE ID 之前），其余平台取景句一律删；开头声明由调用方补。
//
// 逐句只删**多余的**，其余字节原样保留（不重排标点、不重新拼接）。
//
// 【为什么不做「拆句子再 Join」】第一版就是那么写的，结果**不幂等**：收尾纪律本身是 2~3 句
// （`FRAMING (mandatory): … backdrop. The head takes about ONE QUARTER … The picture is a CHEST-UP
// portrait: …`），逐句比对永远匹配不上整串 clause ⇒ 被当"多余取景句"删掉、再在 FACE ID 前补一份
// —— 内容看着一样、字节被重排（真机探针咬出：8347 字 → 8347 字，但两次不相等）。
// 改成**按命中区间删**：`body` 里当前纪律占的那一段整体受保护。
func TightenSheetFraming(body string, kind EntityKind, width, height int) string {
	// **只对人像两档（人 / Q 版）**：它们的收尾纪律就是 `PortraitFramingClause`，判据唯一。
	//
	// 人形机械**不在内**：它自己那份取景（`androidSheetFraming()`）里同样写着
	// `FRAMING (mandatory) … cropped at mid-chest … pure white seamless backdrop` ——
	// 拿本判据去"收口"会把它**自己那份**当多余取景句删掉（平台自己写的取景被吃，最不该发生的事）。
	// 器物的 `prop render` 等取景纪律同理（`PortraitFramingClause` 对它们返回空串，本来就不进来）。
	if kind != KindHuman && kind != KindChibi {
		return body
	}
	clause := strings.TrimSpace(PortraitFramingClause(kind, width, height))
	if clause == "" || strings.TrimSpace(body) == "" {
		return body
	}
	clauseStart := strings.Index(body, clause)
	clauseEnd := -1
	if clauseStart >= 0 {
		clauseEnd = clauseStart + len(clause)
	}
	var out strings.Builder
	dropped := false // 有没有删掉多余的取景句（没有就一个字都不动 —— 幂等的关键）
	filled := clauseStart >= 0
	off := 0
	for n := 0; n < 400 && off < len(body); n++ { // 上界：畸形卡不许无穷循环
		i := firstSentenceEnd(body[off:])
		raw := body[off : off+i] // 原样（含尾随空白）
		seg := strings.TrimSpace(raw)
		// 句子的**内容**区间（去掉首尾空白）—— 不能拿含 ". " 尾随空格的 raw 去比，
		// 收尾纪律最后一句会因此比 `clause` 多出 1 个空格、判成"不受保护"而被误删
		// （真机探针当场咬出：卡里那份 cropCue 句被当"多余取景句"删掉）。
		lead := len(raw) - len(strings.TrimLeft(raw, " \t\r\n"))
		segStart := off + lead
		segEnd := segStart + len(strings.TrimRight(seg, " \t\r\n"))
		off += i
		if seg == "" {
			out.WriteString(raw)
			continue
		}
		if clauseStart < 0 && strings.HasPrefix(seg, faceIDSegmentHead) {
			out.WriteString(clause) // 收尾纪律补在 FACE ID 之前
			out.WriteString(" ")
			filled, clauseStart, clauseEnd = true, -2, -2 // 已补过，别重复补
		}
		// 当前措辞的收尾纪律整段受保护（它自己就是 2~3 句，逐句判会被误删）。
		protected := clauseStart >= 0 && segStart >= clauseStart && segEnd <= clauseEnd
		if !protected && isPlatformFramingSentence(seg) {
			dropped = true
			continue // 多余的取景句：连同它后面的空白一起丢
		}
		out.WriteString(raw)
	}
	if !dropped {
		return body
	}
	res := strings.Join(strings.Fields(out.String()), " ")
	if !filled {
		res = strings.TrimSpace(res) + " " + clause
	}
	return strings.TrimSpace(res)
}
