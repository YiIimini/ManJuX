// jsonx.go LLM 结构化输出的**提取与诊断**(与 jsonrepair.go 分工:
// 那边修字符串内容,这边负责"从一段自由文本里把 JSON 对象找出来")。
//
// 为什么单独成文件:分镜方案/分镜批次/规划轮三处都要做这件事,而旧实现是
// 各写一段"首个 { 到最后一个 }"的截取 —— 模型只要在 JSON 前写一句带花括号的
// 说明(或输出被 max_tokens 截断),截出来的就不是一个对象,报错还统一是
// "解析失败",查起来只能靠猜。2026-09-24 用户 48 集产线卡在第 1 集,
// 报的就是这种无从下手的错。这里把它做成:① 配对扫描找候选;② 逐候选试解析;
// ③ 截断时尽力补全;④ 失败时给出**可诊断**的原因。
package director

import (
	"encoding/json"
	"strconv"
	"strings"
)

// candidateObjects 列出所有"从某个 { 起、括号配平"的候选片段(按出现顺序)。
// 配不平(截断)时把剩余部分交出去,由 closeTruncatedJSON 补。
// 上限 64 个起点:正常输出第一个就命中,防病态输入把扫描拖长。
func candidateObjects(s string) []string {
	var out []string
	for i := 0; i < len(s) && len(out) < 64; i++ {
		if s[i] != '{' {
			continue
		}
		if frag, ok := balancedFrom(s, i); ok {
			out = append(out, frag)
			// 已配平的候选内部必然还含嵌套 {,它们是它的子集、只会更差:
			// 直接跳过整个片段,既省时间也避免把嵌套对象当候选
			i += len(frag) - 1
		} else {
			out = append(out, s[i:]) // 截断的尾巴
			break
		}
	}
	return out
}

// balancedFrom 从 s[start]('{')起做括号配对扫描(字符串与转义感知),
// 返回配平的片段;未配平返回 false。
func balancedFrom(s string, start int) (string, bool) {
	depth := 0
	inStr, esc := false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// 字符串内的花括号不算结构
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// extractJSONObject 从模型输出里取出最可能的 JSON 对象。
//
// 判据顺序:① 含 "segments"/"shots" 且合法 —— 直接命中,不用猜;
// ② 其余合法对象里最长的;③ 都不合法 → 返回第一个候选 + false,
// 由调用方决定补全(截断)还是报错。
func extractJSONObject(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.Trim(raw, "`"))
	if raw == "" {
		return "", false
	}
	cands := candidateObjects(raw)
	best := ""
	for _, c := range cands {
		if !json.Valid([]byte(repairJSONStrings(c))) {
			continue
		}
		if strings.Contains(c, `"segments"`) || strings.Contains(c, `"shots"`) {
			return c, true
		}
		if len(c) > len(best) {
			best = c
		}
	}
	if best != "" {
		return best, true
	}
	if len(cands) > 0 {
		return cands[0], false
	}
	return "", false
}

// closeTruncatedJSON 尽力补全被 max_tokens 截断的 JSON:
// 回退到**最后一个完整元素之后**的边界,丢掉悬挂的半个元素与尾逗号,
// 再按括号栈补上未闭合的 } / ]。
//
// 保守原则:只在"能明确判定边界"时才回退;补出来的对象仍可能缺字段,
// 所以调用方必须继续走字段校验,不能因为补全成功就当真。
func closeTruncatedJSON(s string) (string, bool) {
	var stack []byte
	inStr, esc := false, false
	safe := -1 // 最后一个"刚好闭合一个元素"的位置(不在字符串里)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{' || c == '[':
			stack = append(stack, c)
		case c == '}' || c == ']':
			if len(stack) == 0 {
				continue
			}
			stack = stack[:len(stack)-1]
			if len(stack) > 0 {
				safe = i + 1
			}
		}
	}
	if len(stack) == 0 {
		return s, false // 本来就配平,不需要补
	}
	body := s
	if inStr {
		// 结尾处在字符串里:退到最后一个安全边界(半个字符串只能丢)
		if safe < 0 {
			return "", false
		}
		body = s[:safe]
	}
	if inStr {
		body = strings.TrimRight(body, " \t\r\n")
	} else {
		body = strings.TrimRight(body, " \t\r\n")
		body = strings.TrimRight(body, ",")
	}
	// 重新算一遍栈(可能截断过),补齐
	stack = stack[:0]
	inStr, esc = false, false
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{' || c == '[':
			stack = append(stack, c)
		case c == '}' || c == ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if inStr {
		body += `"`
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			body += "}"
		} else {
			body += "]"
		}
	}
	return body, json.Valid([]byte(repairJSONStrings(body)))
}

// allObjects 列出 s 内**所有**配平的 JSON 对象片段(含嵌套),按出现顺序。
// 用途:模型常把结果包一层({"plan":{…}} / {"result":{…}} / {"data":{…}}),
// 直接按头部结构解析会"缺 title",拆一层就对了 —— 比让模型重答便宜得多。
func allObjects(s string) []string {
	var out []string
	for i := 0; i < len(s) && len(out) < 32; i++ {
		if s[i] != '{' {
			continue
		}
		if frag, ok := balancedFrom(s, i); ok {
			out = append(out, frag)
		}
	}
	return out
}

// snippet 摘录模型原文的两端(截断的线索在尾部,格式问题的线索在头部)。
func snippet(s string, n int) string {
	r := []rune(s)
	if len(r) <= 2*n {
		return string(r)
	}
	return string(r[:n]) + " …〔中略 " + strconv.Itoa(len(r)-2*n) + " 字〕… " + string(r[len(r)-n:])
}
