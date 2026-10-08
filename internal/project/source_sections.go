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
		if i := strings.Index(rest, m); i >= 0 && (end < 0 || i < end) {
			end = i
		}
	}
	if end < 0 {
		return len(rest)
	}
	return end
}

// EpisodeBrief 取【本集剧情】那一段（= 剧本）。没有这个标记时 ok=false。
func EpisodeBrief(text string) (string, bool) {
	i := strings.Index(text, EpisodeBriefSection)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(EpisodeBriefSection):]
	return strings.TrimSpace(rest[:EpisodeSectionEnd(rest)]), true
}

// EpisodeOriginal 取【本集原文】那一段（= 小说原文）。没有标记时**退回全文**。
//
// 退回全文是有意的：技能侧导入的包、或裁过的稿子都没有这个标记，那时整份文本就是全部材料 ——
// 退回全文让"没有标记"退化成本改动之前的行为，不会因为缺标记而把材料读空。
func EpisodeOriginal(text string) string {
	i := strings.Index(text, EpisodeSourceSection)
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
	return brief, orig, hasBrief, hasOrig
}

// episodeSectionIndex 这个标记在不在文本里（用于 hasOrig —— 注意 EpisodeOriginal 缺标记时退回全文，
// 所以不能用"返回非空"当判据）。
func episodeSectionIndex(text, marker string) (int, bool) {
	i := strings.Index(text, marker)
	return i, i >= 0
}
