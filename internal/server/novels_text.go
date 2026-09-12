// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

func failNote(n int) string {
	if n > 0 {
		return fmt.Sprintf("(另有 %d 集创建失败已跳过)", n)
	}
	return ""
}

func hasTxtExt(p string) bool {
	n := strings.ToLower(filepath.Ext(p))
	return n == ".md" || n == ".txt"
}

// projectConfigDefault 新项目默认参数(与创建项目接口一致:全局默认引擎优先)。
func (s *Server) projectConfigDefault() project.ProjectConfig {
	cfg := project.ProjectConfig{Params: project.DefaultStageParams()}
	switch s.cfg.Get().Render.DefaultEngine {
	case "auto", "comfy", "api":
		cfg.Params.Render.Engine = s.cfg.Get().Render.DefaultEngine
	}
	return cfg
}

// chunkNovelText 按章节标题把全书切成 ≤limit 字的段(无章节标题则按尺寸硬切)。
// 供分层拆解:每段一次 LLM 调用,输出体量可控,整本不丢内容。
func chunkNovelText(text string, limit int) []struct{ label, body string } {
	type piece struct{ head, body string }
	// 统一章头正则(认 markdown `#` 前缀,见 novel_format.go 的 chapterHeadRe):
	// 此前这里要求章头是裸行,`# 第NNN章` 的全本因此认不出章界,退化按尺寸硬切
	// (2026-09-12 缺章事故的根因之一)。
	locs := chapterHeadRe.FindAllStringSubmatchIndex(text, -1)
	var pieces []piece
	if len(locs) >= 8 { // 章节结构可信
		for i, loc := range locs {
			start := loc[0]
			end := len(text)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			pieces = append(pieces, piece{head: strings.TrimSpace(text[loc[0]:loc[1]]), body: text[start:end]})
		}
		if locs[0][0] > 0 { // 楔子/简介并入首段
			pre := text[:locs[0][0]]
			if strings.TrimSpace(pre) != "" && len(pieces) > 0 {
				pieces[0].body = pre + pieces[0].body
			}
		}
	} else { // 无章节结构:按尺寸硬切,段落边界优先
		runes := []rune(text)
		for start := 0; start < len(runes); {
			end := start + limit
			if end >= len(runes) {
				end = len(runes)
			} else {
				for end > start && end < len(runes) && runes[end] != '\n' {
					end--
				}
				if end == start {
					end = start + limit
				}
			}
			pieces = append(pieces, piece{head: fmt.Sprintf("第 %d 块", len(pieces)+1), body: string(runes[start:end])})
			start = end
		}
	}
	// 贪心合并相邻章成 ≤limit 的段
	var out []struct{ label, body string }
	var curLabel, curBody string
	flush := func() {
		if strings.TrimSpace(curBody) != "" {
			out = append(out, struct{ label, body string }{curLabel, curBody})
		}
		curLabel, curBody = "", ""
	}
	for _, pc := range pieces {
		switch {
		case curBody == "":
			curLabel, curBody = pc.head, pc.body
		case len([]rune(curBody))+len([]rune(pc.body)) <= limit:
			curBody += pc.body
		default:
			flush()
			curLabel, curBody = pc.head, pc.body
		}
	}
	flush()
	return out
}

// extractEpOriginal 从分段正文中按章节范围("第X章 标题~第Y章 标题")提取
// 本集覆盖的原文;匹配失败返回空(降级为仅大纲)。先整词前缀匹配,再退回
// "第X章"序号词,容忍 LLM 措辞偏差。
func extractEpOriginal(body, chapters string, maxOrig int) string {
	if maxOrig <= 0 {
		maxOrig = 9000
	}
	if strings.TrimSpace(chapters) == "" {
		return ""
	}
	parts := strings.Split(chapters, "~")
	startTok := strings.TrimSpace(parts[0])
	endTok := strings.TrimSpace(parts[len(parts)-1])
	re := regexp.MustCompile(`(?m)^\s{0,4}(第[0-9零〇一二三四五六七八九十百千两]+[章回节])[^\n]{0,50}`)
	locs := re.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 {
		return ""
	}
	head := func(i int) string { return strings.TrimSpace(body[locs[i][2]:locs[i][3]]) }
	find := func(tok string) int {
		if tok == "" {
			return -1
		}
		for i := range locs {
			if strings.HasPrefix(head(i), tok) || strings.HasPrefix(tok, head(i)) {
				return i
			}
		}
		if m := regexp.MustCompile(`第[0-9零〇一二三四五六七八九十百千两]+[章回节]`).FindString(tok); m != "" {
			for i := range locs {
				if strings.HasPrefix(head(i), m) {
					return i
				}
			}
		}
		return -1
	}
	si := find(startTok)
	if si < 0 {
		return ""
	}
	ei := find(endTok)
	if ei < si {
		ei = si
	}
	startAt, endAt := locs[si][0], len(body)
	if ei+1 < len(locs) {
		endAt = locs[ei+1][0]
	}
	orig := strings.TrimSpace(body[startAt:endAt])
	if r := []rune(orig); len(r) > maxOrig {
		orig = string(r[:maxOrig]) + "\n…(原文超长截断)"
	}
	return orig
}

// ---------- 章节切片与"一章一集"的地基(2026-09-12) ----------
//
// 事故背景:章头正则**不认 markdown 的 `# 第NNN章`**,而全本 md 正是这种写法 ——
// 切块、章计数、覆盖核对、原文提取四个环节同时失准。实测同一本全本:旧正则只认出
// 3 个章头,允许 `#` 后认出 67 个。带来的连锁反应:①第09/10集没附上【本集原文】;
// ②它们的覆盖信息对核对不可见 → 被误判"缺第 9/10/11 章";③"自动补缺"据此删掉本来
// 正确的分集并从第 9 章重拆,64 集变成 63 集、第 009 章内容真的丢了。
//
// 因此:章头识别收敛到**一个**正则(chapterHeadLineRe),所有环节共用;并且集与章的
// 对应关系由**平台**从正文/分章文件直接算出,不再依赖 LLM 写在 chapters 字段里的字符串。

// ChapterSlice 一章的切片(章号 + 标题 + 该章正文)。
type ChapterSlice struct {
	Num   int    // 章号(001 → 1)
	Title string // 章标题(去掉「第N章」前缀)
	Text  string // 该章正文(含章头行,便于分镜 LLM 看到章界)
}

// chineseNum 解析章号:支持 001 与 一/十/二十三 等写法(解析失败返回 0)。
func chineseNum(tok string) int {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return 0
	}
	if v, err := strconv.Atoi(tok); err == nil {
		return v
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
		'五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int{'十': 10, '百': 100, '千': 1000}
	total, cur := 0, 0
	for _, r := range tok {
		if d, ok := digits[r]; ok {
			cur = d
			continue
		}
		if u, ok := units[r]; ok {
			if cur == 0 {
				cur = 1
			}
			total += cur * u
			cur = 0
			continue
		}
		return 0
	}
	return total + cur
}

// SplitChapters 把整段正文切成章切片(章头行 + 到下一章头之前的正文)。
// 找不到章头时返回 nil(调用方自行降级)。
func SplitChapters(text string) []ChapterSlice {
	locs := chapterHeadRe.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}
	out := make([]ChapterSlice, 0, len(locs))
	for i, loc := range locs {
		start := loc[0]
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		headLine := strings.TrimSpace(text[loc[0]:loc[1]])
		num := chineseNum(text[loc[2]:loc[3]])
		if num == 0 {
			continue
		}
		// 标题:去掉 markdown 前缀与「第N章」标记后的剩余部分
		title := headLine
		title = regexp.MustCompile(`^#{1,6}[ \t]*`).ReplaceAllString(title, "")
		title = regexp.MustCompile(`^第\s*[0-9零〇一二三四五六七八九十百千两]+\s*[章回节][ \t:：、.．-]*`).ReplaceAllString(title, "")
		out = append(out, ChapterSlice{Num: num, Title: strings.TrimSpace(title), Text: text[start:end]})
	}
	return out
}

// ChapterTitlesFromFiles 从分章文件名解析「章号 → 标题」:第001章_三百块的活儿.md → (1,"三百块的活儿")。
// 用于「一章一档」的数据源:章号与标题由文件名给定,不依赖正文里的章头写法。
func ChapterTitlesFromFiles(names []string) map[int]string {
	out := map[int]string{}
	re := regexp.MustCompile(`^第\s*([0-9零〇一二三四五六七八九十百千两]+)\s*[章回节][_. \t-]*(.*)$`)
	for _, n := range names {
		base := strings.TrimSuffix(filepath.Base(n), filepath.Ext(n))
		m := re.FindStringSubmatch(strings.TrimSpace(base))
		if m == nil {
			continue
		}
		if num := chineseNum(m[1]); num > 0 {
			out[num] = strings.TrimSpace(m[2])
		}
	}
	return out
}
