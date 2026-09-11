// jsonrepair.go LLM 输出的 JSON 修复(director 侧,与 server/jsonrepair.go
// 同源):字符串内裸控制字符转义 + 未转义的内容引号后瞻判定转义 + 非法
// UTF-8 清洗。分镜/镜头 JSON 里照抄台词是常态,半角引号不修必炸。
package director

import (
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
			// 过度转义收尾:LLM 把字符串的收尾定界引号也转义成 \"
			// (2026-09-11 实证)。若 \" 后面是终止符格局,去掉反斜杠
			// 按正常收尾处理。
			if i+1 < len(rs) && rs[i+1] == '"' && quoteCloses(rs, i+1) {
				b.WriteRune('"')
				inStr = false
				i++
				continue
			}
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
// 紧跟 , 时还需逗号后(跳空白)是 " } ] 才算;其余 → 内容引号。
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
