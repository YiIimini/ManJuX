// jsonrepair.go LLM 输出的 JSON 修复:中文模型在 json_object 模式下仍常在
// 字符串值里写入裸换行/制表符/未转义引号(标准 JSON 非法,Unmarshal 直接判死),
// 表现为「拆集结果异常」重试也失败——实为完整输出被一个裸字符卡死。
// 2026-09-10 实证:裸 \n 卡死整体解析(已修:控制字符转义);
// 2026-09-11 实证:brief 照抄小说对话台词时未转义半角引号("谢了。""下周日
// 面摊老位置。"婚后…),字符串提前终止、后续内容变结构区垃圾,整体与逐集
// 解析全灭(已修:引号后瞻判定,内容引号转义为 \")。
package server

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// repairJSONStrings 修复 LLM 输出 JSON 中字符串内的裸控制字符、
// 未转义的内容引号 + 非法 UTF-8。
func repairJSONStrings(raw string) string {
	raw = strings.ToValidUTF8(raw, "\uFFFD")
	rs := []rune(raw)
	var b strings.Builder
	b.Grow(len(raw) + 64)
	inStr, esc := false, false
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case esc:
			b.WriteRune(r)
			esc = false
		case r == '\\' && inStr:
			b.WriteRune(r)
			esc = true
		case r == '"':
			if !inStr {
				b.WriteRune(r)
				inStr = true
				continue
			}
			// 字符串内遇到引号:真终止符还是未转义的内容引号?
			// 真终止符后面(跳过空白含换行)必然是 , } ] : 之一。
			if quoteCloses(rs, i) {
				b.WriteRune(r)
				inStr = false
			} else {
				b.WriteString(`\"`)
			}
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

// quoteCloses 判定 rs[i](字符串内的 ")是否为字符串终止符:
// 跳过其后空白(空格/Tab/换行)后,紧跟 } ] : 之一 → 终止符;
// 紧跟 , 时还需逗号后(跳空白)是 " } ] 才算(否则是中文内容里的引号+
// 半角逗号,如 他喊"停",然后);其余(普通文字等)→ 内容引号。
// 截断尾(后面没有字符了)按终止符处理,保留逐集提取的截断语义。
func quoteCloses(rs []rune, i int) bool {
	j := i + 1
	for j < len(rs) && (rs[j] == ' ' || rs[j] == '\t' || rs[j] == '\n' || rs[j] == '\r') {
		j++
	}
	if j >= len(rs) {
		return true
	}
	switch rs[j] {
	case '}', ']', ':':
		return true
	case ',':
		k := j + 1
		for k < len(rs) && (rs[k] == ' ' || rs[k] == '\t' || rs[k] == '\n' || rs[k] == '\r') {
			k++
		}
		if k >= len(rs) {
			return true
		}
		return rs[k] == '"' || rs[k] == '}' || rs[k] == ']'
	default:
		return false
	}
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
	// 找所有 {"ep": 开头的对象块(允许 { 与 "ep" 之间有空白:pretty 多行
	// 格式里 { 后是换行+缩进,2026-09-11 实证逐集提取对 pretty JSON 全盲)
	// 用贪心匹配到对应的 } (处理嵌套引号内的 })
	re := regexp.MustCompile(`\{\s*"ep"\s*:\s*\d+[^{}]*(?:\{[^{}]*\}[^{}]*)*\}`)
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
