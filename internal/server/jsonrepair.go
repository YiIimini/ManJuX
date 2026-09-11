// jsonrepair.go LLM 输出的 JSON 修复:中文模型在 json_object 模式下仍常在
// 字符串值里写入裸换行/制表符(标准 JSON 非法,Unmarshal 直接判死),
// 表现为「拆集结果异常」重试也失败——实为完整输出被一个裸 \n 卡死
// (2026-09-10 实证:11 段拆集第 1 段 JSON 到末集完整仍解析失败)。
// 修复:状态机扫描,字符串内部的控制字符转义为 \\n \\r \\t;字符串外
// 原样保留(空白在结构区合法)。同时剥掉包裹的圆括号等围栏。
package server

import (
	"encoding/json"
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

// salvageTruncatedJSON 抢救被 max_tokens 截断的 JSON:
// 找到最后一个完整的 episode 对象(以 "}," 结尾),丢弃残余,
// 手动闭合 episodes 数组和根对象。返回抢救后的完整 JSON(空串=无法抢救)。
// 典型场景:每段 10-20 集×700 字 brief + thinking ≈ 50-60K token,
// 偶尔撞 65536 上限(2026-09-11 实证第 8/14 段 unexpected end of JSON input)。
func salvageTruncatedJSON(raw string) string {
	// 先修控制字符
	raw = repairJSONStrings(raw)
	// 已完整闭合(JSON 尾部是 ]} 或 }) 不需要抢救
	trimmed := strings.TrimSpace(raw)
	if strings.HasSuffix(trimmed, "]}") || strings.HasSuffix(trimmed, "}") {
		// 尾部有 } 但可能缺 ] → 尝试解析,能过就不动
		var probe json.RawMessage
		if json.Unmarshal([]byte(trimmed), &probe) == nil {
			return raw // 完整 JSON,原样返回
		}
	}
	// 找最后一个完整的 "}," 作为 episode 对象结尾
	last := strings.LastIndex(raw, `"},`)
	if last < 0 {
		return ""
	}
	salvaged := raw[:last+2]
	salvaged += "]}"
	return salvaged
}
