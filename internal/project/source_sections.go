package project

import "strings"

// source_sections.go 本集 `source.txt` 的**段落结构**与唯一读取边界。
//
// 一集的 source.txt 有三种材料（拆集那一步写的，见 server/novels_split.go）：
//
//	《书名》系列短视频 第 01 集《集名》
//	全局风格:…
//
//	【本集剧情】   ← **剧本**：平台的专职编剧会话产出（【开场爆点】+ 逐场「目的/冲突/转折/爽点/钩子/正文」）
//	【本集原文】   ← **小说原文**：只是素材（补细节用）
//
// 【为什么这条边界要收进 project】（2026-10-08 用户：「文章内容转剧本，需要的是 LLM 提取内容分析总结后
// 输出爽文爆点剧本（根据剧本内容再完善输出分镜脚本），**而不是去要求原文内容怎样怎样**」）
//
// 剧本与原文是**两种材料、两种角色**：剧本是主源（分镜按它的场次/爽点/钩子排），原文是参考（补细节）。
// 此前这条边界只活在 server 侧（`episodeBriefOf`/`episodeOrigOf`），**管线侧拿到的是整团文本** ——
// 于是分镜阶段只能把两者当同一份"原文"用（提示词还写着"从原文提炼"），剧本等于没被当作主源。
// 边界收进 project 之后，server（写剧本的那一侧）与 pipeline（读剧本的那一侧）看的是同一份判据；
// 它同时是**写入**边界（novels_split.go 回填剧本时按它切旧稿）。
//
// 【节标记必须"独占行首"才算边界】（2026-10-08 深度排查发现）
// 模型会把平台发给它的**抬头原样抄进正文**（`novels_jobs.go` 那条 user 段就是
// "【本集原文】\n"+原文），于是剧本正文里可能出现"【本集原文】"这几个字。
// 纯 strings.Index 会就地切断剧本：真机形态实测 brief 只剩前半段；若回显落在开头还
// 会得到 ok=true 但 brief=""，接着写回把这一版当"新剧本"，文件里标记出现两次、每轮重跑
// 都重写整集（就是 2026-10-07 那次"51 字"的同一条链）。所以只认**行首**（前面只有空白）的标记。
const (
	// EpisodeBriefSection 剧本段的起始标记。
	EpisodeBriefSection = "【本集剧情】"
	// EpisodeSourceSection 小说原文段的起始标记。
	EpisodeSourceSection = "【本集原文】"
	// episodePrevSection 建集时写的前情段：它是剧本段的**前一个**边界。
	episodePrevSection = "【前情提要】"
)

// EpisodeSectionEnd 段落结束下标（不含终止标记）；无标记 ⇒ 到文末。
//
// 【不能用"第一个空行"当边界】（2026-10-07 真机实测，代价最大的一处）
// 模型写的剧本本来就有空行（【开场爆点】那一行后面就有一个），用空行切时 `episodeBriefOf`
// 只取回 **51 字**（而整段剧本是 2090 字）⇒ ① 完整性判据永远判"不可用" ② 每跑一次这一集都重写剧本、
// 覆盖改过的稿子 ③ 连备份都轮不到（门槛里写着字数）。现在只看**平台自己写的节标记**。
func EpisodeSectionEnd(rest string) int {
	end := -1
	for _, m := range []string{EpisodeSourceSection, EpisodeBriefSection, episodePrevSection} {
		if i := sectionIndex(rest, m); i >= 0 && (end < 0 || i < end) {
			end = i
		}
	}
	if end < 0 {
		return len(rest)
	}
	return end
}

// EpisodeBrief 取【本集剧情】那一段（= 剧本）。没有这个标记、或**标记在但内容是空的**，
// 都返回 ok=false。
//
// "空壳"也判 false 的理由（2026-10-08 深度排查发现）：拆集那一步是无条件写标记的
// （`novels_split.go`），所以建集之初就是"标记在、内容空"。此前 hasBrief=true 会让
// director.sourceRoleClause 照样宣称"【本集剧情】是剧本，逐场按它组织、不得用原文替换"，
// 而 pipeline 的剧本阶段同时判"本集没有剧本" —— **同一份提示词里两句话打架**，模型拿到
// 空脚本却被禁止用唯一的素材。空壳一律走"没有剧本"那一条。
func EpisodeBrief(text string) (string, bool) {
	i := sectionIndex(text, EpisodeBriefSection)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(EpisodeBriefSection):]
	brief := strings.TrimSpace(rest[:EpisodeSectionEnd(rest)])
	if brief == "" {
		return "", false
	}
	return brief, true
}

// EpisodeOriginal 取【本集原文】那一段（= 小说原文）。没有标记时**退回全文**。
//
// 退回全文是有意的：技能侧导入的包、或裁过的稿子都没有这个标记，那时整份文本就是全部材料 ——
// 退回全文让"没有标记"退化成本改动之前的行为，不会因为缺标记而把材料读空。
func EpisodeOriginal(text string) string {
	i := sectionIndex(text, EpisodeSourceSection)
	if i < 0 {
		return text
	}
	return strings.TrimSpace(text[i+len(EpisodeSourceSection):])
}

// EpisodeSourceParts 一次取两段 + 两个"有没有"标记。
//
// 调用方据此决定该怎么用（见 director.sourceRoleClause 与 pipeline 的剧本阶段）：
// 有剧本 ⇒ 按剧本排分镜、原文只作参考；没剧本 ⇒ **如实说"本集没有剧本"**，而不是静默按原文硬跑。
func EpisodeSourceParts(text string) (brief, orig string, hasBrief, hasOrig bool) {
	brief, hasBrief = EpisodeBrief(text)
	orig = EpisodeOriginal(text)
	_, hasOrig = episodeSectionIndex(text, EpisodeSourceSection)
	// 标记在、但原文段是空的 ⇒ 当作"没有原文"（否则调用方会以为手上有素材可喂）。
	hasOrig = hasOrig && orig != ""
	return brief, orig, hasBrief, hasOrig
}

// episodeSectionIndex 这个标记（行首的那种）在不在文本里。
//
// 用于 hasOrig —— 注意 EpisodeOriginal 缺标记时退回全文，所以不能用"返回非空"当判据。
func episodeSectionIndex(text, marker string) (int, bool) {
	i := sectionIndex(text, marker)
	return i, i >= 0
}

// sectionIndex 找标记位置，只认**行首独立成节**的那一次（前面只有空白）；找不到返回 -1。
//
// 为什么不是纯 strings.Index：见文件头"节标记必须独占行首才算边界"。
// 注意"行首"允许前导空白（模型偶尔缩进），但不允许任何可见字符 ——
// 这正是"模型把标记抄进正文句子中间"与"平台自己写的节标记"之间的确定区别。
func sectionIndex(text, marker string) int {
	for from := 0; from < len(text); {
		i := strings.Index(text[from:], marker)
		if i < 0 {
			return -1
		}
		at := from + i
		if atLineStart(text, at) {
			return at
		}
		from = at + len(marker)
	}
	return -1
}

// atLineStart at 处是不是行首（前面只有空白字符）。
//
// 注意只认 ASCII 空白：text[i] 是**字节**，全角空格是三个字节，放进 byte 的 case 里编译不过
// （首版就栽在这）。平台自己写节标记时用的是纯 ASCII 换行 + 空行，所以够用；模型若把标记
// 缩进成全角空格，按"不是行首"处理 —— 宁可判它"不是节边界"，也不会误切正文。
func atLineStart(text string, at int) bool {
	for i := at - 1; i >= 0; i-- {
		switch text[i] {
		case '\n':
			return true
		case ' ', '\t', '\r':
			continue
		default:
			return false
		}
	}
	return true
}

// TrimEchoedSections 清掉**模型把材料抬头照抄回来**的那几个节标记 —— 回填剧本之前必须过这一道。
//
// 【为什么要在写入侧也清】（2026-10-08 深度排查发现，两个后果都实测过）
// 平台发给编剧会话的 user 段就是 "【本集原文】\n" + 原文（novels_jobs.go），模型偶尔把这行
// 连带抄回自己的输出里。于是：
//
//	① 回填后的文件里 【本集剧情】 出现两次 ⇒ 读回时第一段是空的 ⇒ **剧本被判成"没有"**，
//	   每一轮都重写整集（就是 2026-10-07 那次"51 字"的同一条链）；
//	② 正文里夹着行首的 【本集原文】 ⇒ 读回时剧本在那里被就地截断，而盘上留着的是完整稿 ——
//	   **写出去的与读回来的不是同一份**。
//
// 返回清过之后的正文 + 清掉了哪些（调用方如实播报，不静默改字）。
func TrimEchoedSections(body string) (string, []string) {
	var stripped []string
	rest := strings.TrimSpace(body)
	// ① 开头的标记行：模型把 user 段的抬头原样抄回来。
	for {
		head := strings.TrimLeft(rest, " \t\r\n\u3000")
		hit := ""
		for _, m := range []string{EpisodeBriefSection, EpisodeSourceSection, episodePrevSection} {
			if strings.HasPrefix(head, m) {
				hit = m
				break
			}
		}
		if hit == "" {
			break
		}
		stripped = append(stripped, hit)
		rest = head[len(hit):]
	}
	// ② 正文里再现（行首的）【本集原文】/【前情提要】 ⇒ 从这里截断（理由见上）。
	if i := earliestLineStart(rest, EpisodeSourceSection, episodePrevSection); i >= 0 {
		stripped = append(stripped, "正文中的节标记（其后内容已截掉）")
		rest = rest[:i]
	}
	return strings.TrimSpace(rest), stripped
}

// earliestLineStart 这几个标记里**行首**出现得最早的那个下标；都没有返回 -1。
func earliestLineStart(text string, markers ...string) int {
	best := -1
	for _, m := range markers {
		if i := sectionIndex(text, m); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	return best
}

// SectionIndex 供 server 侧定位**平台自己写的**节标记（只认行首那种）；找不到返回 -1。
// 判据与读取侧同一份（sectionIndex）—— 写入侧的"从哪儿切旧稿"必须与读回侧一致，
// 否则写出去的与读回来的不是同一份（2026-10-08 排查时正是这个形态）。
func SectionIndex(text, marker string) int { return sectionIndex(text, marker) }
