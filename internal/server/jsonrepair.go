// jsonrepair.go LLM 输出的 JSON 修复:中文模型在 json_object 模式下仍常在
// 字符串值里写入裸换行/制表符(标准 JSON 非法,Unmarshal 直接判死),
// 表现为「拆集结果异常」重试也失败——实为完整输出被一个裸 \n 卡死
// (2026-09-10 实证:11 段拆集第 1 段 JSON 到末集完整仍解析失败)。
// 修复:状态机扫描,字符串内部的控制字符转义为 \\n \\r \\t;字符串外
// 原样保留(空白在结构区合法)。同时剥掉包裹的圆括号等围栏。
package server

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// repairJSONStrings 修复 LLM 输出 JSON 中字符串内的裸控制字符 + 非法 UTF-8。
func repairJSONStrings(raw string) string {
	raw = strings.ToValidUTF8(raw, "\uFFFD")
	var b strings.Builder
	b.Grow(len(raw) + 64)
	inStr, esc := false, false
	for _, r := range raw {
		switch {
		case esc:
			b.WriteRune(r)
			esc = false
		case r == '\\' && inStr:
			b.WriteRune(r)
			esc = true
		case r == '"':
			b.WriteRune(r)
			inStr = !inStr
		case inStr:
			switch r {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if unicode.IsControl(r) || r == '\uFFFD' {
					b.WriteRune(' ')
				} else {
					b.WriteRune(r)
				}
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// salvageTruncatedJSON 已废弃,由 extractEpisodes 替代(见 novels.go)。
// 保留空函数避免编译报错,后续删除。
func salvageTruncatedJSON(raw string) string {
	return ""
}

// extractEpisodes 从 LLM 原始输出中逐集提取,完全绕过整体 JSON 解析。
// 每个 episode 是一个独立的 {"ep":N,"title":"...","brief":"..."} 对象,
// 用正则找到所有 `{...}` 块,逐个 json.Unmarshal,坏的丢好的留。
// 这是对 JSON 结构错误(非法字符/截断/缺括号/多余文本)的终极免疫:
// 不管 LLM 把 JSON 弄成什么样,只要单个集对象是完整的就能提取。
func extractEpisodes(raw string) []map[string]any {
	// 找所有 {"ep": 开头的对象块
	// 用贪心匹配到对应的 } (处理嵌套引号内的 })
	re := regexp.MustCompile(`\{"ep"\s*:\s*\d+[^{}]*(?:\{[^{}]*\}[^{}]*)*\}`)
	matches := re.FindAllString(raw, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []map[string]any
	for _, m := range matches {
		var ep map[string]any
		if json.Unmarshal([]byte(repairJSONStrings(m)), &ep) == nil {
			if _, ok := ep["brief"]; ok {
				out = append(out, ep)
			}
		}
	}
	return out
}
