// jsonrepair.go LLM 输出的 JSON 修复(director 侧共用):中文模型在
// json_object 模式下仍常在字符串值里写入裸换行/制表符,标准 JSON 非法。
// 修复:状态机扫描,字符串内部的控制字符转义为 \n \r \t;字符串外原样。
package director

import (
	"strings"
	"unicode"
)

// repairJSONStrings 修复 LLM 输出 JSON 中字符串内的裸控制字符。
func repairJSONStrings(raw string) string {
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
				if unicode.IsControl(r) {
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
