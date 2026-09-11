// jsonrepair.go LLM 输出的 JSON 修复:中文模型在 json_object 模式下仍常在
// 字符串值里写入裸换行/制表符(标准 JSON 非法,Unmarshal 直接判死),
// 表现为「拆集结果异常」重试也失败——实为完整输出被一个裸 \n 卡死
// (2026-09-10 实证:11 段拆集第 1 段 JSON 到末集完整仍解析失败)。
// 修复:状态机扫描,字符串内部的控制字符转义为 \\n \\r \\t;字符串外
// 原样保留(空白在结构区合法)。同时剥掉包裹的圆括号等围栏。
package server

import (
	"strings"
	"unicode"
)

// repairJSONStrings 修复 LLM 输出 JSON 中字符串内的裸控制字符 + 非法 UTF-8。
// 三层防御:①清洗非法 UTF-8 字节序列(API 代理/编码抖动产生的 è 等乱字,
// 2026-09-11 实证 invalid character 'è');②字符串内控制字符→合法转义;
// ③已有转义不双重。
func repairJSONStrings(raw string) string {
	// 先清洗非法 UTF-8(替换为 U+FFFD,json.Unmarshal 不再报 invalid character)
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
					b.WriteRune(' ') // 控制字符和乱码替换符→空格
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
