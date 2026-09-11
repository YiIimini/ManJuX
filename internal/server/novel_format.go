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
	Chapters string `json:"chapters"` // 覆盖章节范围(定位原文用)
	Brief    string `json:"brief"`
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

// chapterHeadRe 章头行(预处理后为裸行)。切块/章计数/覆盖核对共用。
var chapterHeadRe = regexp.MustCompile(`(?m)^[ \t]{0,4}第[0-9零〇一二三四五六七八九十百千两]+[章回节][^\n]{0,50}\s*$`)

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
