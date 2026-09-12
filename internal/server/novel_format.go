// novel_format.go 拆集输出协议 v2:分节定界纯文本(2026-09-11 v0.33.16 根治)。
//
// 背景:拆集 brief 携带大段中文台词,LLM 输出 JSON 时结构错误六类轮番出现
// (裸控制字符/非法字节/未转义内容引号/输出截断/pretty 盲区/收尾引号被
// 过度转义),逐类打补丁永远追不完——用户定调「深度排查彻底解决」。
// 根治=换协议:brief 是自由文本,唯一的结构是 ===EP N=== 分节标记与
// TITLE/CHAPTERS/BRIEF 行标签,台词用什么引号、什么括号都不可能破坏解析。
// JSON 路径降级为兜底(模型偶发按旧习惯输出 JSON 时仍能吃下)。
package server

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// epItem 拆集单集(分节格式与 JSON 兜底共用)。
type epItem struct {
	Ep       int    `json:"ep"`
	Title    string `json:"title"`
	Chapters string `json:"chapters"` // LLM 标注的章节范围(只作参考,不作真源)
	Brief    string `json:"brief"`
	// 以下两项由**平台**填写(2026-09-12 一章一集改造):集与章的对应关系不再依赖
	// LLM 写在 Chapters 里的字符串 —— 实测它会把"整段范围"抄进每一集,导致原文附着
	// 失败与覆盖核对误判(64 集被误删成 63 集的事故)。
	ChapNums []int  `json:"-"` // 本集覆盖的章号(平台按章切片算出的真源)
	Orig     string `json:"-"` // 本集原文(平台按章切片取,不依赖 LLM 的 Chapters)
}

var (
	// 分节标记:===EP 1=== / === EP #2 === / <<<EP 3>>> 等变体全放行
	epMarkerRe = regexp.MustCompile(`(?m)^[ \t]*(?:={2,}|<{3,})[ \t]*EP[ \t]*#?(\d+)[ \t]*(?:={2,}|>{3,})[ \t]*$`)
	epBookRe   = regexp.MustCompile(`(?m)^[ \t]*(?:BOOK|书名)[ \t]*[:：][ \t]*(.+)$`)
	epStyleRe  = regexp.MustCompile(`(?m)^[ \t]*(?:STYLE|风格)[ \t]*[:：][ \t]*(.+)$`)
	epTitleRe  = regexp.MustCompile(`(?m)^[ \t]*(?:TITLE|标题)[ \t]*[:：][ \t]*(.+)$`)
	epChapRe   = regexp.MustCompile(`(?m)^[ \t]*(?:CHAPTERS|章节)[ \t]*[:：][ \t]*(.+)$`)
	// BRIEF 行:冒号后可空(正文另起行)或直接同行起头
	epBriefRe = regexp.MustCompile(`(?m)^[ \t]*(?:BRIEF|大纲)[ \t]*[:：][ \t]*(.*)$`)
)

// parseEpBlocks 解析分节定界格式;检测不到分节标记时 ok=false(走 JSON 兜底)。
// 返回(集列表, 书名, 风格, 是否分节格式)。
func parseEpBlocks(raw string) ([]epItem, string, string, bool) {
	markers := epMarkerRe.FindAllStringSubmatchIndex(raw, -1)
	if len(markers) == 0 {
		return nil, "", "", false
	}
	book := strings.TrimSpace(firstLineValue(epBookRe, raw))
	style := strings.TrimSpace(firstLineValue(epStyleRe, raw))
	var eps []epItem
	for i, m := range markers {
		end := len(raw)
		if i+1 < len(markers) {
			end = markers[i+1][0]
		}
		block := raw[m[1]:end]
		ep, err := strconv.Atoi(strings.TrimSpace(raw[m[2]:m[3]]))
		if err != nil {
			continue
		}
		brief := ""
		if loc := epBriefRe.FindStringSubmatchIndex(block); loc != nil {
			// 从捕获组(同行起头内容)起点取到块尾:兼容「BRIEF: 同行正文」
			// 与「BRIEF:\n另起行正文」两种写法
			brief = strings.TrimSpace(block[loc[2]:])
		}
		if brief == "" {
			continue
		}
		eps = append(eps, epItem{
			Ep:       ep,
			Title:    strings.TrimSpace(firstLineValue(epTitleRe, block)),
			Chapters: strings.TrimSpace(firstLineValue(epChapRe, block)),
			Brief:    brief,
		})
	}
	if len(eps) == 0 {
		return nil, book, style, false
	}
	normalizeEpisodeTitles(eps)
	return eps, book, style, true
}

// epChapterTitleRe 从 CHAPTERS 字段里取「第一个章头 + 其标题」。
// CHAPTERS 形如「第003章 器不可以成道」「第1章~第2章」,后者无标题。
var epChapterTitleRe = regexp.MustCompile(`第\s*[0-9零〇一二三四五六七八九十百千两]+\s*[章回节]\s*([^\s~至到\-—<>]{1,24})`)

// normalizeEpisodeTitles 集名兜底:**章名即集名**。
//
// 为什么不能只靠提示词:提示词已明确要求「TITLE 直接取章标题原文」,但 LLM 仍会
// 自作主张起文艺标题 —— 实测第001章《第一万三千七百四十一号》被起成《七文一斤》。
// 集名一旦脱离章名,「章→集」就失去可对账性,章节覆盖核对随之误报缺章
// (2026-09-12 实测:误报缺第 1、2 章,而内容其实都在)。
// 因此在 CHAPTERS 能解析出章标题时,一律用章名覆盖 TITLE;解析不出(如只写
// 「第1章~第2章」)则保留 LLM 的标题,不强行清空。
func normalizeEpisodeTitles(eps []epItem) {
	for i := range eps {
		if m := epChapterTitleRe.FindStringSubmatch(eps[i].Chapters); len(m) > 1 {
			if title := strings.TrimSpace(m[1]); title != "" {
				eps[i].Title = title
			}
		}
	}
}

// checkOneChapterPerEpisode 检测「一章被拆成多集」(应为一章一集)。
// 返回被拆分的章号列表(升序去重),仅用于告警 —— 不自动合并:
// 合并要重排 Brief 与集号,风险高于收益,由用户决定是否重拆。
func checkOneChapterPerEpisode(eps []epItem) []int {
	numRe := regexp.MustCompile(`第\s*0*([0-9]{1,4})\s*[章回节]`)
	seen := map[int]int{}
	var dup []int
	for _, ep := range eps {
		nums := map[int]bool{}
		for _, m := range numRe.FindAllStringSubmatch(ep.Chapters, -1) {
			if v, e := strconv.Atoi(m[1]); e == nil {
				nums[v] = true
			}
		}
		// 单章集才计入计数;跨章集(如无标题的「第1章~第2章」)不参与
		if len(nums) == 1 {
			for v := range nums {
				seen[v]++
			}
		}
	}
	for ch, n := range seen {
		if n > 1 {
			dup = append(dup, ch)
		}
	}
	sort.Ints(dup)
	return dup
}

// firstLineValue 取正则第一个匹配的捕获组;无匹配返回空。
func firstLineValue(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
}

// chapterHeadRe 章头行。切块/章计数/覆盖核对/原文切片**共用这一个正则**。
//
// 必须容忍 markdown 的 `#` 前缀与全角空格:全本 md 的章头常写作 `# 第009章 洗出来的字`,
// 旧版要求"裸行",导致一本 64 章的书只认出 3 个章头 —— 切块退化成按尺寸硬切、章计数
// 与覆盖核对全体失准,最终「自动补缺」误判缺章、删掉了本来正确的分集(2026-09-12 事故:
// 64 集被删成 63 集、第 009 章内容真丢)。
var chapterHeadRe = regexp.MustCompile(`(?m)^[ \t]{0,4}(?:#{1,6}[ \t]*)?第\s*([0-9零〇一二三四五六七八九十百千两]+)\s*[章回节][^\n]{0,50}$`)

// countChapterHeads 统计文本中的章头行数(≥8 视为章节结构可信)。
func countChapterHeads(s string) int {
	return len(chapterHeadRe.FindAllString(s, -1))
}

// chapterHeadFromFilename 从分章文件名解析标准章头行:
// 第001章_叁佰玖拾玖.md → "第001章 叁佰玖拾玖"。解析失败返回空。
func chapterHeadFromFilename(base string) string {
	base = strings.TrimSuffix(base, filepath.Ext(base))
	m := regexp.MustCompile(`^(第[0-9零〇一二三四五六七八九十百千两]+[章回节])[_. \t-]+(.+)$`).
		FindStringSubmatch(strings.TrimSpace(base))
	if m == nil {
		return ""
	}
	return m[1] + " " + m[2]
}

// ensureChapterHead 分章文件内容若开头无章头行,从文件名注入:
// 合并正文时保证每册有章界(2026-09-11 实证:全本 md 正文无章头导致
// 章界识别失败→硬切大段→CHAPTERS 失准→【本集原文】附着断链)。
func ensureChapterHead(base, body string) string {
	head := body
	if r := []rune(head); len(r) > 300 {
		head = string(r[:300])
	}
	if countChapterHeads(head) > 0 {
		return body
	}
	if h := chapterHeadFromFilename(base); h != "" {
		return h + "\n\n" + body
	}
	return body
}

// parseChapNums 从 LLM 标注的 chapters 字符串里解析章号列表(升序去重)。
//
// 容忍各种写法:第9章~第11章 / 第9-11章 / 第9、10章 / 第9至11章 / 9-11。
// 解析不出返回 nil —— 此时调用方按"顺序对齐"兜底,绝不据此判定内容缺失。
func parseChapNums(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// 先把分隔符统一:范围符换成 ~,顿号/逗号换成空格
	for _, r := range []string{"至", "到", "--", "—", "－", "-", "→"} {
		s = strings.ReplaceAll(s, r, "~")
	}
	for _, r := range []string{"、", ",", "，", " "} {
		s = strings.ReplaceAll(s, r, " ")
	}
	numRe := regexp.MustCompile(`[0-9零〇一二三四五六七八九十百千两]+`)
	var out []int
	seen := map[int]bool{}
	add := func(v int) {
		if v > 0 && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	// 先处理 "A~B" 区间
	for _, part := range strings.Split(s, "~") {
		nums := numRe.FindAllString(part, -1)
		if len(nums) == 0 {
			continue
		}
		if len(nums) >= 2 {
			a, b := chineseNum(nums[0]), chineseNum(nums[len(nums)-1])
			if a > 0 && b >= a && b-a <= 200 {
				for v := a; v <= b; v++ {
					add(v)
				}
				continue
			}
		}
		add(chineseNum(nums[0]))
	}
	sort.Ints(out)
	return out
}

// alignEpsToChapters 把 LLM 返回的 brief **按章**对齐成本段的一集一章列表。
//
// 这是"一章一集"的强制点(2026-09-12 用户定调「一章就是一集」):集名取章名、
// 集与章一一对应、原文直接取该章切片 —— LLM 只负责写 brief,不再有机会跳章/并章
// (实测它会把两章并进一集、或把整段范围抄进每一集的 chapters 字段)。
// brief 找不到对应章时:先按顺序对齐(LLM 通常是逐章给的),仍缺的留空由调用方兜底。
func alignEpsToChapters(chaps []ChapterSlice, eps []epItem) []epItem {
	if len(chaps) == 0 {
		return eps
	}
	byChap := map[int]string{}
	var loose []string // 章号解析不出的 brief,按顺序兜底
	for _, e := range eps {
		nums := parseChapNums(e.Chapters)
		if len(nums) == 0 {
			if strings.TrimSpace(e.Brief) != "" {
				loose = append(loose, e.Brief)
			}
			continue
		}
		for _, n := range nums {
			if _, ok := byChap[n]; !ok && strings.TrimSpace(e.Brief) != "" {
				byChap[n] = e.Brief
			}
		}
	}
	out := make([]epItem, 0, len(chaps))
	looseIdx := 0
	for _, ch := range chaps {
		brief := byChap[ch.Num]
		if brief == "" && looseIdx < len(loose) {
			brief = loose[looseIdx]
			looseIdx++
		}
		out = append(out, epItem{
			Title:    ch.Title,
			Chapters: fmt.Sprintf("第%d章", ch.Num),
			Brief:    brief,
			ChapNums: []int{ch.Num},
			Orig:     ch.Text,
		})
	}
	return out
}
