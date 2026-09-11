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
	"regexp"
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
	return eps, book, style, true
}

// firstLineValue 取正则第一个匹配的捕获组;无匹配返回空。
func firstLineValue(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
}
