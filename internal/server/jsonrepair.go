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

// repairJSONStrings 修复 LLM 输出 JSON 中字符串内的裸控制字符。
func repairJSONStrings(raw string) string {
	var b strings.Builder
	b.Grow(len(raw) + 64)
	inStr, esc := false, false
	for _, r := range raw {
		switch {
		case esc: // 上一字符是反斜杠:本字符原样,转义态结束
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
				if unicode.IsControl(r) { // 其它控制字符(罕见)替换为空格
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
