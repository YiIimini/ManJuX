package project

import (
	"regexp"
	"strconv"
	"strings"
)

// h3_format_extra.go 上面那批输出门用到的判据助手（2026-09-27 扩）。
//
// 一律**宽松**：有即可，不判措辞。这些是质量级问题（不阻断渲染），契约里按聚合 warn 报。

const (
	// h3MaxSoundscapeSentences 官方 §4.6：overall_soundscape 1–4 句。
	h3MaxSoundscapeSentences = 4
	// h3MaxMusicSentences 官方 §4.7：non_diegetic_music 1–3 句。
	h3MaxMusicSentences = 3
)

// timestampParts 一个带时间戳的镜头号。
type timestampParts struct {
	n   int
	raw string
	ms  int
}

// missingSections 六段/三段里缺哪几段（按名字判，宽松）。
func missingSections(h3 string, order []string) []string {
	var miss []string
	for _, m := range order {
		if !strings.Contains(h3, m) {
			miss = append(miss, strings.TrimSuffix(m, ":"))
		}
	}
	return miss
}

// firstOutOfOrder 段落的出现位置是否与官方顺序一致（返回第一个出问题的段名，空 = 顺序对）。
func firstOutOfOrder(h3 string, order []string) string {
	prev := -1
	for _, m := range order {
		idx := strings.Index(h3, m)
		if idx < 0 {
			continue
		}
		if idx < prev {
			return strings.TrimSuffix(m, ":")
		}
		prev = idx
	}
	return ""
}

// sentenceCount 段落的句数（按 . ! ? 结尾数；"N/A" 记 0 句）。宽松统计，用于句数上限。
func sentenceCount(seg string) int {
	s := strings.TrimSpace(seg)
	if s == "" || strings.EqualFold(s, "N/A") {
		return 0
	}
	n := 0
	for _, r := range s {
		if r == '.' || r == '!' || r == '?' {
			n++
		}
	}
	return n
}

// speakerTagOutsideDescription (Sx) 出现在正文段之外的第一个命中（空 = 没有）。
//
// 官方把说话人标记限定在 detailed_description 的台词句里：retention_analysis 里写就是错的
// （那一段只讲"参考图保留了什么"），内容/片段标题里写也一样。
func speakerTagOutsideDescription(h3 string) string {
	for _, marker := range []string{"retention_analysis:", "overall_soundscape:", "non_diegetic_music:", "summary:", "subject_definitions:"} {
		if seg, ok := sectionOf(h3, marker); ok {
			if m := speakerTagRe.FindString(seg); m != "" {
				return m
			}
		}
	}
	return ""
}

var (
	// h3ShotTimeRe `[Shot N] At MM:SS.mmm`
	h3ShotTimeRe = regexp.MustCompile(`\[Shot\s+(\d+)\]\s+At\s+(\d{1,2}):(\d{2})\.(\d{3})`)
)

// shotTimestamps 取出所有带时间戳的镜头（按出现顺序）。
func shotTimestamps(h3 string) []timestampParts {
	var out []timestampParts
	for _, m := range h3ShotTimeRe.FindAllStringSubmatch(h3, -1) {
		n, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		ss, _ := strconv.Atoi(m[3])
		ms, _ := strconv.Atoi(m[4])
		out = append(out, timestampParts{n: n, raw: m[2] + ":" + m[3] + "." + m[4], ms: mm*60000 + ss*1000 + ms})
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
