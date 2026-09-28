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

// h3TraceTags 分镜/内部标签：官方 `qc-checklist.md` STEP 8 明说成片要"clean animation visuals
// and no storyboard traces, including no `[char:…] [scene:…] [shot:…]` labels"。
//
// 为什么会漏进正文：平台的规则段与 schema 里到处是 `char_xxx` / `scene_xxx` 这类占位写法，
// 模型有时会把它们当"标签"写进散文里 —— 那会被渲染成画面上的乱码字形（与汉字卫生同一类事故）。
var h3TraceTags = []string{"[char:", "[scene:", "[shot:", "[prop:", "<char:", "<scene:", "<prop:"}

// storyboardTraceIn 正文里出现的分镜/内部标签（空 = 没有）。
func storyboardTraceIn(h3 string) string {
	low := strings.ToLower(h3)
	for _, t := range h3TraceTags {
		if strings.Contains(low, t) {
			return t
		}
	}
	return ""
}

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
	// h3ShotHeadRe 行首的 `[Shot N]` 标题（`[Shot 1]` 不带时间戳也算一个镜头）
	h3ShotHeadRe = regexp.MustCompile(`(?m)^\s*\[Shot\s+\d+\]`)
)

// H3ClipCuts 一个片段正文里**有几个小镜头**（内部切点）。
//
// 【为什么需要它】用户 2026-09-28：「难怪渲染的视频总感觉拖沓，都没有抓重点」。
// 量出来的机制：平台的每镜正文里**只有一个 `[Shot 1]`** —— 一个 6–8 秒的片段从头到尾
// 演一个连续动作 = 全程慢动作。而用户自己的《【额外】仿真人10S分镜头指令》写着
// 「每个片段内拆成若干小镜头，每镜 1.5–4 秒」，H3 官方格式也支持一个片段里写多个
// `[Shot N] At MM:SS.mmm`（Ref2VA §5.2 示例就是多镜）。**片段内切镜**才是"快"的来源。
//
// 只在 detailed_description 段内数：subject_definitions/retention_analysis 里会出现
// `appears in [Shot 1]` 这种引用（第一版按全文数，得出"48/48 镜多段"的假结论，被自己推翻过）。
func H3ClipCuts(h3 string) int {
	seg, ok := sectionOf(h3, "detailed_description")
	if !ok {
		seg = h3
	}
	return len(h3ShotHeadRe.FindAllString(seg, -1))
}

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
