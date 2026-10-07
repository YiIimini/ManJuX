package project

import "strings"

// cast_source.go 从**小说/剧本原文**里取某个角色"首次出场"附近的外形材料。
//
// 【为什么需要它】（2026-10-07 用户问：「小说内容第一次出现角色没有描述角色形象吗？」）
//
// 小说**写了**，而且写得很具体 —— 真机《名册之外，八万零一号》里五个人的首次出场：
//
//	霜见（第013章）：他站起来。少年，清瘦，肩背不宽，外套的体积大于身体，袖口磨出了毛边。
//	  他没有束发，中长过耳直到下颌，额前蓬松微卷的刘海盖过眉毛，几缕碎发遮着眼。
//	  冷白的脸上，下颌线清楚。颈侧有一道黑色机械与皮肤交界的接缝纹，正面看得见。
//	  双耳各挂着一条细长的银坠，左耳那只歪了，像被人拽过一次。
//	墨洄（第022章）：黑高领，领口外面扣着一圈颈部金属环；黑色长直发及背，额前中分，
//	  发丝贴着两颊，像刚从水里出来。长菱形脸，颧骨微宽，下颌收得很窄；丹凤眼，眼尾上扬…
//	邵舷（第020章）：他走得不快，脚跟着地，肩是含着的；灰蓝长褂的领口扣到最上面一颗，
//	  袖口一道名册官袖章。他右手的中指上套着一枚灰银色金属指套，四十毫米长，前端磨得发亮。
//	尉迟彻（第026章）：只剩一道深灰的轮廓。深灰的值勤装甲，肩线笔直，两脚并得很齐。
//	断桅（第009章）：他背驼，走路右肩低半寸…（后文另补：右耳缺角、喉咙里有旧伤）
//
// 这些段落**本来就在②人物道具会话的输入里**（`director/sessions.go`：user = 设定集 + 已确立清单 +
// `【本集原文】`），模型却没把这五张卡的 description 写出来 —— 所以平台必须有一条**确定性**的兜底：
// 锚表里没有这个人时，就用小说里首次出场附近那段描写当材料（再由专职会话译成英文定妆提示词）。
func FirstAppearanceExcerpt(text, name string, otherNames []string) string {
	name = strings.TrimSpace(name)
	if name == "" || text == "" || !strings.Contains(text, name) {
		return ""
	}
	paras := splitParagraphs(text)
	// 按**出现顺序**试每一个"名字所在的段"：首次出场那句常常是对话（「剥线刀说：“霜见。”」），
	// 而外形描写在它前面的段里；这一处取不到材料就顺着试下一处提及，直到取到为止。
	// 判据是确定性的：**第一处能取到外形材料的地方**，而不是"名字第一次出现的地方"。
	for hit := range paras {
		if !strings.Contains(paras[hit], name) {
			continue
		}
		if got := excerptAround(paras, hit, name, otherNames); got != "" {
			return got
		}
	}
	return ""
}

// excerptAround 从"名字所在段"向**前**最多 maxBackParagraphs 段扫描，取第一段像描写外形的段落里的外形句。
//
// 为什么向前扫 8 段：真机墨洄的首次提及是「墨洄没有看人。」，而她的外形描写在**前面第 5 段**
// （中间夹着「她一进来…」「不是收进怀里…」两段叙述与两段对话）。扫深一点只是多看几段 ——
// 真正的两条把关是 `looksLikeDescription`（≥2 类外形词才认）与"别人的描写不许当他的"。
func excerptAround(paras []string, hit int, name string, otherNames []string) string {
	const maxBackParagraphs = 8
	for back := 0; back <= maxBackParagraphs && hit-back >= 0; back++ {
		p := paras[hit-back]
		// 这一段在写别人（出现了别的已知角色名、却没有本名）⇒ 跳过：别人的脸不许当他/她的。
		if !strings.Contains(p, name) && mentionsAny(p, otherNames) {
			continue
		}
		if !looksLikeDescription(p, name) {
			continue
		}
		if got := visualSentences(p); got != "" {
			return got
		}
	}
	return ""
}

// visualCues 外形线索词。两处用它：
//   - `looksLikeDescription` 数**命中种类数**（≥2 才算"这一段在描写人"；名字自己那一段只要 ≥1）；
//   - `visualSentences` 逐句筛（只保留写外形的句子，剧情与对话不进去）。
//
// 收词原则：**宁可漏，不可滥** —— "手""走""看"这类在剧情句里到处都有，收进来会把剧情当外形，
// 而翻译会话会把那份"材料"逐项译成外貌（那比空着更糟）。断桅那句「他背驼，走路右肩低半寸」
// 是**体态**，所以体态与记号类（背驼/肩低/缺角/旧伤/疤）也收。
var visualCues = []string{
	// 脸与五官
	"脸", "眉眼", "眼型", "瞳孔", "眸", "鼻", "唇", "嘴", "下巴", "下颌", "颧", "额头", "额角",
	// 头发
	"脸", "眉眼", "眼型", "瞳孔", "眸", "鼻", "唇", "嘴", "下巴", "下颌", "颧", "额头", "额角",
	// 眼/眉的**具体形态**（"丹凤眼""柳叶眼"是描写，"看一眼""抬眼"是剧情 ⇒ 不收单字"眼""眉"）
	"丹凤眼", "杏眼", "柳叶眼", "双眼皮", "单眼皮", "眼尾", "眼神", "眉毛", "剑眉", "眉峰", "眉形", "睫毛",
	// 头发
	"刘海", "发丝", "发型", "长发", "短发", "中长发", "束发", "银发", "黑发",
	// 皮肤
	"肤色", "肤质", "皮肤", "瓷白", "冷白",
	// 体态
	"身形", "肩线", "肩背", "背驼", "驼背", "肩低", "清瘦", "瘦削", "修长", "高挑",
	// 记号
	"缺角", "旧伤", "疤", "记号", "接缝纹", "缝线",
	// 服装与配饰
	"高领", "长褂", "外套", "围巾", "袖口", "毛边", "耳坠", "耳环", "银坠", "项圈", "指套",
	"装甲", "靴", "轮廓",
	// 年龄档（锚表与小说都用）
	"少年", "青年", "中年", "老年", "年纪",
}

// looksLikeDescription 这一段像不像"在写这个人长什么样"。名字出现在段里时门槛降到 1（自述段）。
func looksLikeDescription(para, name string) bool {
	n := len(visualCueHits(para))
	if strings.Contains(para, name) {
		return n >= 1
	}
	return n >= 2
}

// visualCueHits 命中了哪几类线索词（去重）。
func visualCueHits(s string) []string {
	var out []string
	for _, c := range visualCues {
		if strings.Contains(s, c) {
			out = append(out, c)
		}
	}
	return out
}

// visualSentences 段落里**写外形**的那些句子（其余是剧情/对话，不进材料）。
// 整段一句都不像外形 ⇒ 返回空串（宁可不填）。总长封顶 400 字。
func visualSentences(para string) string {
	var keep []string
	for _, sent := range splitSentences(para) {
		if len(visualCueHits(sent)) > 0 {
			keep = append(keep, sent)
		}
	}
	out := strings.TrimSpace(strings.Join(keep, ""))
	const maxRunes = 400
	if r := []rune(out); len(r) > maxRunes {
		out = strings.TrimSpace(string(r[:maxRunes]))
	}
	return out
}

// mentionsAny 这一段提到别的已知角色了吗（"别人的描写不许当他的"用它）。
func mentionsAny(para string, names []string) bool {
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && strings.Contains(para, n) {
			return true
		}
	}
	return false
}

// splitParagraphs 按空行切段（markdown 的分隔线 `---` 单独成段，会被线索词判据自动跳过）。
func splitParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	raw := strings.Split(text, "\n\n")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitSentences 按中英文句末标点切句（保留标点，拼回来仍然可读）。
func splitSentences(s string) []string {
	var out []string
	var cur strings.Builder
	for _, r := range s {
		cur.WriteRune(r)
		switch r {
		case '。', '！', '？', '；', '\n', '!', '?', ';':
			if t := strings.TrimSpace(cur.String()); t != "" {
				out = append(out, t)
			}
			cur.Reset()
		}
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		out = append(out, t)
	}
	return out
}
