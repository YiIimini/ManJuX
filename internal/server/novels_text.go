// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"fmt"
	"path/filepath"
	"regexp"
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
	re := regexp.MustCompile(`(?m)^\s{0,4}(第[0-9零〇一二三四五六七八九十百千两]+[章回节])[^\n]{0,50}\s*$`)
	locs := re.FindAllStringSubmatchIndex(text, -1)
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
