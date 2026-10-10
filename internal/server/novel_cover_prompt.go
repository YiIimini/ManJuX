// novel_cover_prompt.go 封面正向词的**唯一出处**（2026-10-10 用户：「平台有LLM功能啊」）。
//
// 上一版我拿模板句当封面词（书名 + 风格 + 构图口径拼一句），用户当场指出来：平台有 LLM。
// 对 —— 封面词是**创作**，该让 LLM 按这本书写；模板只配当**兜底**。
//
// 三档优先级（写死，先人的意图、再模型的、最后机器拼的）：
//
//	① 技能端留档 `封面/pos-qwen21.txt` —— 技能端已经写过一版，尊重它（也让它成为"不改就沿用"的语义）；
//	② **平台 LLM 按书名 + 本书风格 + 立项简介 + 正文开头写一份**（本文件的主角）；
//	③ `coverSeedPrompt` 的**模板词** —— 只在 ① ② 都给不出可用结果时兜底，
//	   并且**如实标注是兜底**（调用方按 source 播报，不许含糊成"AI 写的"）。
//
// ## 硬约束（两条都不是我编的，是平台自己的口径）
//
//  1. **正文必须是英文**：出图装配只吃英文提示词，汉字注解会被模型当成"要画的字"照抄
//     （`pipeline/artstyle.go` 里那条 StripCJK 注释）。
//  2. **要让书名出现在封面上，书名必须放进英文引号**：官方 ON-SCREEN TEXT 判据写着
//     「没被英文引号括起来的文字，就是模型会画成字形的来源」（`project/prompt_cjk.go` 顶上那段）。
//     于是判据很干脆：**词里引号之外不许出现任何汉字，引号内只放书名**。
//
// LLM 侧遵守不了这条时由平台收口（不是把违规喂回去重写：这里是出图前的一次性动作，
// 清洗 + 校验 + 兜底比多轮重写更快也更可判）：**清掉引号外的汉字**，清完若已经不成句
// （比如模型整段用中文写了），就直接走兜底模板 —— 三条路都有 source 可播报。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// coverPromptSys 封面词撰稿人的系统提示词。
//
// 三处硬要求都照平台自己的口径写（英文正文 / 引号里才画字 / 除书名外不许汉字），
// 因为这两条判据同时管着**技能侧**与**平台侧**（见 project/prompt_cjk.go 的"两侧同源"）。
const coverPromptSys = `你是短剧封面提示词的撰稿人。给你一本书的信息，你只输出**一段英文正向提示词**（供 Qwen-Image 出竖版 3:4 海报封面）。

硬要求（逐条都得满足）：
1. 只输出那一段英文提示词：不要解释、不要 markdown 围栏、不要用引号把整段包起来、不要编号。
2. 书名必须出现在提示词里、并**放进英文双引号**（官方 ON-SCREEN TEXT 的判据是「没被英文引号括起来的文字，
   就是模型会画成字形的来源」—— 引号是"要画在画面上的字"的唯一写法）。
3. 除引号里的书名之外，**整段不许出现任何汉字**（引号外的汉字会被模型当字形画出来，变成乱码）。
4. **封面上只许有书名这一处文字**，而且只能用**正向陈述**写 —— 说清"这一处文字是什么"，
   例如：The only lettering anywhere in the image is the cover title "《书名》", set as one single block of large elegant serif type in the lower third. 再说画面其余部分是纯影像。
   **不要写 no / without 这类否定句**，也**不要点名** subtitle / watermark / caption / logo / signature 这些词：
   出图侧是 cfg=1.0（**否定词不生效**），而提到的名词会被当成要画的东西 —— 实测写了 no other text 之后，
   书名下方照样多出一行乱码伪字（真机会被用户看见，见 docs 里平台自己的 VLM 原话）。
5. 构图固定：竖版 3:4、单一主视觉居中偏上、下三分之一留出标题位、电影级打光、浅景深、高细节。
6. 6-8 个英文短语，用逗号/句号分隔；不要写成一大段散文。

直接给出那段英文提示词本身。`

// coverTextClause 封面上**那一处文字**该怎么写 —— 全平台唯一出处
// （2026-10-10 用户「封面文字内容版本需要优化」，口径定为「**封面上只许有书名一处文字**」）。
//
// ## 真机取证（先有证据再改，不是口味）
//
// 平台自己的 VLM 读那张真机封面（`data/小说/离婚后，千亿前夫跪求复合/封面/封面.png`，16:16 生成）原话：
//
//  1. 有文字。在下三分之一、居中；主标题约占宽85%、高20%，**下方另有一行极小字**。
//  2. 主标题：《离婚后，千亿前夫跪求复合》；**下方小字：乱码/糊成一团**。
//  7. **不能**：主标题正确清晰，但底部乱码小字必须删掉或重做后才可用。
//
// 那份词里写着 `no other text, no subtitles, no watermark, no signature` —— 为什么没用：
//
//   - 出图侧是 Qwen-Image，**cfg=1.0 ⇒ 否定词不生效**（`pipeline/artstyle.go` 顶上那条平台口径）；
//   - 而且**提到的名词会被当成要画的东西**（2026-09-12「每个定妆照肩膀上都多了三个 Q 型小人」
//     就是同型事故：提示词里写了"别人/巴掌大的小人"，模型就照着画）。所以 subtitle / watermark /
//     caption / logo / signature 这些词**一个都不许出现在图像提示词里**。
//
// "只许有一处文字"只能用**正向陈述**表达：这一处**就是**唯一的一处，其余是纯影像。
// 书名的写法由官方 ON-SCREEN TEXT 决定 ——「**没被英文引号括起来的文字，就是模型会画成字形的来源**」
// （`project/prompt_cjk.go` 顶上那段）⇒ 想画上去的字必须放进英文引号。
//
// **必须是"一句话"**（句内不含句号）：收口是**按句**摘掉再贴这一句的，口径自身若由两句组成，
// 贴回来时第二句会被当成"画面描述"留下 ⇒ 越收越长（`TestCleanCoverPromptEnforcesOneTextClause`
// 的幂等断言当场红，见本轮文档"被自己的门咬出"）。
func coverTextClause(bookName string) string {
	name := strings.TrimSpace(bookName)
	return fmt.Sprintf("The only lettering anywhere in the image is the cover title \"《%s》\", "+
		"one single block of large elegant serif type in the lower third, centered and clearly legible, "+
		"with every other part of the frame staying pure photographic imagery.", name)
}

// coverTextSentenceHints 句子触发词：某一句里出现任何一个 ⇒ 这句在**讲画面上的文字**，由平台统一改写。
// 判据故意窄（宁少勿多）：只认明确指出"这是画面上的字"的词，免得把剧情/构图句一起摘掉。
var coverTextSentenceHints = []string{
	"title text", "lettering", "typeset", "typesetting", "on-screen text", "on screen text",
	"watermark", "subtitle", "subtitles", "caption", "captions", "logo", "signature",
	"text on the poster", "text in the image", "text on the cover", "text across the",
}

// coverNegationTextRe 否定式文字条款（"no other text" 这一类）：
// 它们既拦不住模型（cfg=1.0），又把名词喂了进去 —— 必须摘掉，并能单独点名。
var coverNegationTextRe = regexp.MustCompile(
	`(?i)\b(no|without|avoid|free of)\b[^.]{0,80}\b(text|subtitle|subtitles|watermark|signature|logo|caption|captions|lettering|words)\b`)

// enforceCoverText 让一份封面词满足「封面上只许有书名这一处文字」——**平台保证，不靠模型自觉**：
//  1. **逐句**处理：讲画面文字的整句（命中触发词或否定式）**整句摘掉**；
//  2. 其余句子里**抹掉书名那一处**（书名只由下面贴的那一句承担 —— 一处文字，不是两处）；
//  3. 末尾贴上 `coverTextClause`。
//
// 注意第 2 条**不能**写成"含书名的整句一起摘掉"：真机那份词的第一句就是
// `Vertical 3:4 poster key visual for the Chinese short drama "《书名》", single hero subject,
// dramatic cinematic lighting, highly detailed.` —— 整句摘掉会把**构图与打光**一起丢掉
// （`TestCoverPromptEndpointForcesLLM` 当场红），而它只是**提到**了书名，不是在讲画面上的字。
//
// 返回 (新词, 摘掉的句数, 抹掉的书名处数)；**幂等**（再跑一次结果不变）。
func enforceCoverText(prompt, bookName string) (string, int, int) {
	name := strings.TrimSpace(bookName)
	body := make([]string, 0, 8)
	dropped, untitled := 0, 0
	for _, s := range splitSentences(prompt) {
		if isCoverTextSentence(s) {
			dropped++
			continue
		}
		var n int
		s, n = removeQuotedTitle(s, name)
		untitled += n
		s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
		if s = strings.Trim(s, " ,，。.;:"); s != "" {
			body = append(body, s)
		}
	}
	clause := coverTextClause(name)
	if len(body) == 0 {
		return clause, dropped, untitled
	}
	return strings.Join(body, ". ") + ". " + clause, dropped, untitled
}

// isCoverTextSentence 这一句在不在讲"画面上的文字"：命中触发词、或是否定式文字条款。
// **只看"有没有在讲文字"**，不看"有没有提到书名"（提到书名只抹掉书名那一处，见 enforceCoverText）。
func isCoverTextSentence(s string) bool {
	low := strings.ToLower(s)
	for _, h := range coverTextSentenceHints {
		if strings.Contains(low, h) {
			return true
		}
	}
	return coverNegationTextRe.MatchString(s)
}

// splitSentences 按句号切句（保留句子内容，不保留句号）——够用即可：
// 封面词是短句清单，没有缩写/小数会踩到这条。
func splitSentences(s string) []string {
	out := make([]string, 0, 8)
	for _, part := range strings.Split(s, ".") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// removeQuotedTitle 抹掉句子里"书名本身"的那一处（"《名》"/“名”/「《名》」/ 裸《名》都算），
// 返回 (新串, 抹掉几处)。目的：正文里不许再有第二处书名 —— 书名只由 coverTextClause 那一句承担。
func removeQuotedTitle(s, bookName string) (string, int) {
	name := strings.TrimSpace(bookName)
	if name == "" {
		return s, 0
	}
	pats := []string{
		"《" + name + "》", "\"" + name + "\"", "\"《" + name + "》\"",
		"“" + name + "”", "“《" + name + "》”",
		"「" + name + "」", "「《" + name + "》」",
	}
	n := 0
	for _, p := range pats {
		for {
			i := strings.Index(s, p)
			if i < 0 {
				break
			}
			// 去掉后可能留下 ` ,` / ` , ` 这类残迹：连着清理，免得词看起来像坏的。
			s = strings.Replace(s, p, "", 1)
			n++
		}
	}
	s = strings.ReplaceAll(s, " ,", ",")
	s = strings.ReplaceAll(s, " .", ".")
	return s, n
}

// coverPromptSource 封面词的来源（**必须能播报**：用户要能看出这行词是谁写的）。
type coverPromptSource struct {
	Prompt string // 正向词（一定是可用的那一份）
	From   string // 人读的来源说明，例如「留档 封面/pos-qwen21.txt」「平台 LLM 按书名写」「模板兜底（LLM 没参与）」
	Note   string // 附加说明（清洗掉了几个引号外汉字 / LLM 为什么没用上），可为空
}

// coverPromptFor 「这本书该用哪一份封面正向词」——三档优先级见文件头。
//
// 不返回 error：出封面这件事必须有词可用（拿不到 LLM 就退模板），
// 但**降级一定体现在 From/Note 里**，由调用方如实播报（不许静默降级）。
func (s *Server) coverPromptFor(bookDir string) coverPromptSource {
	// ① 技能端留档（有就用它，且**不调 LLM**：技能端写过一版就尊重那一版）
	if raw, err := os.ReadFile(coverPromptPath(bookDir)); err == nil {
		if p := strings.TrimSpace(string(raw)); p != "" {
			return coverPromptSource{Prompt: p, From: "留档 封面/" + coverRedoPromptFile}
		}
	}
	// ② 平台 LLM
	p, note, err := s.llmCoverPrompt(bookDir)
	if err == nil {
		return coverPromptSource{Prompt: p, From: "平台 LLM 按书名写", Note: note}
	}
	// ③ 模板兜底（如实标注，并把 LLM 为什么没用上带出去）
	seed := coverSeedPrompt(bookDir, filepath.Base(bookDir))
	return coverPromptSource{Prompt: seed, From: "模板兜底（LLM 没参与）",
		Note: strings.TrimSpace("LLM 没用上：" + err.Error())}
}

// coverPromptContext 给 LLM 的"这本书是什么"材料：全部来自这本书自己（不猜）：
//
//	书名 · 风格串（唯一出处 scanNovelStyle） · 立项.json 的题材/简介类字段 · 正文开头（有限长度）
//
// 正文开头最有价值（封面画的是什么人/什么场景，答案就在里面），但**不许把整本书塞进去**：
// 取首册前 coverPromptExcerptChars 字，够让模型抓住主角与基调即可。
func coverPromptContext(bookDir string) string {
	name := filepath.Base(bookDir)
	var b strings.Builder
	fmt.Fprintf(&b, "书名：《%s》\n", name)
	if st := scanNovelStyle(bookDir); st.Found() {
		fmt.Fprintf(&b, "本书的视频画面风格串（封面要与正片同一口径）：%s\n", strings.TrimSpace(st.Style))
	}
	if raw, err := os.ReadFile(filepath.Join(bookDir, "立项.json")); err == nil {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			for _, k := range []string{"题材", "类型", "一句话简介", "简介", "logline", "synopsis", "主角", "protagonist"} {
				if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
					fmt.Fprintf(&b, "%s：%s\n", k, clampRunes(strings.TrimSpace(v), 300))
				}
			}
		}
	}
	if paths, _ := collectManuscript(bookDir); len(paths) > 0 {
		if text, err := readNovelTextFile(paths[0]); err == nil {
			if r := []rune(strings.TrimSpace(text)); len(r) > 0 {
				if len(r) > coverPromptExcerptChars {
					r = r[:coverPromptExcerptChars]
				}
				fmt.Fprintf(&b, "正文开头（仅供你判断人物与基调）：\n%s\n", string(r))
			}
		}
	}
	return b.String()
}

// coverPromptExcerptChars 给 LLM 的正文开头长度上限（rune）。
// 800 字足够看出主角是谁、什么题材、什么基调；再长只是在烧 token，与封面无关。
const coverPromptExcerptChars = 800

// llmCoverPrompt 让平台 LLM 按书名与这本书的材料写一份封面正向词，并做**平台侧收口**：
// 剥围栏/标签 → 清掉引号外的汉字 → 补书名标题句（模型漏了就补）→ 校验够不够成句。
// 返回 (提示词, 附加说明, 错误)。错误只在"LLM 给不出可用结果"时返回（调用方转兜底模板）。
func (s *Server) llmCoverPrompt(bookDir string) (string, string, error) {
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		return "", "", fmt.Errorf("平台 LLM 未配置（设置里填 LLM 地址/密钥/模型）")
	}
	name := filepath.Base(bookDir)
	user := coverPromptContext(bookDir) +
		"\n请按上面的信息写一段英文封面正向提示词（书名 " + name + " 放进英文双引号里）。"
	timeout := g.LLM.Timeout
	if timeout <= 0 {
		timeout = 120
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: coverPromptSys},
		{Role: "user", Content: user},
	}, false)
	if err != nil {
		return "", "", fmt.Errorf("LLM 调用失败：%w", err)
	}
	prompt, note, ok := cleanCoverPrompt(raw, name)
	if !ok {
		return "", "", fmt.Errorf("LLM 给的词清洗后不成句（整段像中文说明）")
	}
	return prompt, note, nil
}

// cleanCoverPrompt 把模型输出收成"能直接出图"的一行英文词：返回值第 3 位=false 表示不可用。
//
// 收口四步（每一步都是平台自己的规矩，不是口味）：
//  1. 去 markdown 围栏与常见前缀（"Prompt:" / "提示词："）；
//  2. 压成一行；
//  3. 清掉**引号外的汉字**（引号内的书名是"要画上去的字"，必须留）；
//  4. 模型漏了书名标题句就由平台补一句（封面没有书名就不像封面），最后校验句子里还有没有
//     足够的英文内容（洗成空壳就判不可用 ⇒ 调用方走兜底）。
func cleanCoverPrompt(raw, bookName string) (prompt, note string, ok bool) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 { // 围栏整段剥掉
		parts := strings.Split(s, "```")
		best := ""
		for _, p := range parts {
			p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "text"))
			if len([]rune(p)) > len([]rune(best)) {
				best = p
			}
		}
		s = best
	}
	for _, pre := range []string{"Prompt:", "Prompt：", "提示词：", "提示词:", "英文提示词：", "英文提示词:"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, pre))
	}
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	cleaned, removed := stripCJKOutsideQuotes(s)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if removed > 0 {
		note = fmt.Sprintf("清掉了引号外 %d 个汉字（引号外的汉字会被模型当字形画出来）", removed)
	}
	// 先判"**模型自己那段**还成不成句"：清洗只保证引号外没有汉字，**不保证模型给的是词** ——
	// 它可能整段用中文写的，洗完成了一个空壳（只剩标点）。这种必须判不可用、让调用方走兜底模板，
	// **不许**靠下面补一句书名标题把小壳救成"能用"（那样出图会得到一张没有主体的封面）。
	//
	// 这一条是**门咬出来的**：第一版的顺序是"先补书名句、再判成句"，于是整段中文的输入
	// 被补成「， ， 。. The cover title text "《书名》" typeset clearly on the poster.」——
	// 英文单词数够 6 个、长度够 60 字符，判成了"可用"（`TestCoverPromptForFallsBackWithHonestNote` 当场红）。
	if !coverPromptUsable(cleaned) {
		return "", note, false
	}
	// **画面文字条款由平台唯一写一份**（2026-10-10 用户「封面文字内容版本需要优化」）：
	// 模型（或技能端留档）自己写的那些画面文字句**整段摘掉**，尤其是 "no other text" 这类否定式 ——
	// 出图侧 cfg=1.0 下拦不住，还会把 subtitle/watermark 这些**名词**喂进模型；
	// 然后贴上 coverTextClause 那一句。于是「封面上只许有书名一处文字」是**平台保证的**。
	before := cleaned
	cleaned, dropped, untitled := enforceCoverText(cleaned, bookName)
	if dropped > 0 || untitled > 0 {
		why := make([]string, 0, 2)
		if dropped > 0 {
			why = append(why, fmt.Sprintf("摘掉模型写的画面文字句 %d 处（否定式在 cfg=1.0 下不生效）", dropped))
		}
		if untitled > 0 {
			why = append(why, fmt.Sprintf("正文里另有书名 %d 处已去掉（书名只由平台那一句承担）", untitled))
		}
		note = appendCoverNote(note, strings.Join(why, "、"))
	} else if !hasCoverTitle(before, bookName) {
		note = appendCoverNote(note, "模型没写书名句，平台按口径补上")
	}
	// 补完再核一次（书名句本身也得成句）；任一步不过 ⇒ 不可用
	if !coverPromptUsable(cleaned) {
		return "", note, false
	}
	return cleaned, note, true
}

// appendCoverNote 把一句说明接在 Note 后面（空的时候不加分隔符）——同一处收口，免得每种情况各拼一遍。
func appendCoverNote(note, add string) string {
	add = strings.TrimSpace(add)
	if add == "" {
		return note
	}
	if strings.TrimSpace(note) == "" {
		return add
	}
	return note + "；" + add
}

// hasCoverTitle 词里有没有「引号里的书名」（官方口径下"要画在画面上的字"的唯一写法）。
func hasCoverTitle(prompt, bookName string) bool {
	name := strings.TrimSpace(bookName)
	if name == "" {
		return false
	}
	for _, q := range []string{"\"", "“", "「"} {
		if strings.Contains(prompt, q+name) || strings.Contains(prompt, q+"《"+name+"》") {
			return true
		}
	}
	return false
}

// coverPromptUsable 清洗后还成不成句：至少 6 个 ASCII 单词、总长 ≥ 60 字符。
// 判据故意粗（这个位置不需要精细审美）：它只是拦住「整段中文被洗成空壳」这一类。
func coverPromptUsable(prompt string) bool {
	if len([]rune(prompt)) < 60 {
		return false
	}
	words := 0
	for _, w := range strings.FieldsFunc(prompt, func(r rune) bool { return r < 'A' || (r > 'Z' && r < 'a') || r > 'z' }) {
		if len(w) >= 2 {
			words++
		}
	}
	return words >= 6
}

// coverPromptQuotedRe 引号区间（英文直引号/花引号/书名号写的标题都算"要画上去的字"）。
var coverPromptQuotedRe = regexp.MustCompile(`"[^"]*"|“[^”]*”|「[^」]*」`)

// stripCJKOutsideQuotes 删掉**引号之外**的汉字，返回（结果, 删掉的字数）。
//
// 为什么只删引号外的：引号里那串就是官方 ON-SCREEN TEXT 通道里的"要画的字"（我们的用法里只有书名），
// 引号外的汉字则会被模型当字形来源一起画上去（`project/prompt_cjk.go` 的判据）。
func stripCJKOutsideQuotes(s string) (string, int) {
	var b strings.Builder
	removed := 0
	last := 0
	for _, loc := range coverPromptQuotedRe.FindAllStringIndex(s, -1) {
		chunk := s[last:loc[0]]
		kept, n := stripCJK(chunk)
		b.WriteString(kept)
		removed += n
		b.WriteString(s[loc[0]:loc[1]]) // 引号整段原样保留
		last = loc[1]
	}
	kept, n := stripCJK(s[last:])
	b.WriteString(kept)
	removed += n
	return b.String(), removed
}

// stripCJK 把一段文本里的汉字换成空格（与 project.StripCJK 同口径：宁可留空格也不留字形来源）。
func stripCJK(s string) (string, int) {
	var b strings.Builder
	removed := 0
	for _, r := range s {
		if project.CountCJK(string(r)) > 0 {
			b.WriteByte(' ')
			removed++
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), removed
}

// llmReady 平台 LLM 是否已配置 —— 封面弹窗里那个「AI 按书名写词」按钮据此置灰，
// 别让用户点了才看见"LLM 未配置"。
func (s *Server) llmReady() bool {
	g := s.cfg.Get()
	return llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout).Configured()
}
