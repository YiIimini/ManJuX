// textnormalize.go 小说文本预处理:不管源文件什么编码/格式,先统一转成
// 干净 UTF-8 纯文本再喂 LLM(2026-09-11 用户定调:根源解决)。
// 处理:①编码检测(UTF-8/GBK/GB18030 自动识别)②BOM 剥除③行尾统一 LF
// ④不可见字符清理(零宽/控制符)⑤智能引号→直引号(防 LLM 输出串格式)
// ⑥Markdown 装饰符号剥离(标题 #/粗体 **/斜体 *)⑦连续空行压缩。
package server

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// NormalizeNovelText 文本预处理入口:输入任意编码的原始字节,输出干净 UTF-8。
func NormalizeNovelText(raw []byte) string {
	text := decodeText(raw)
	text = stripBOM(text)
	text = normalizeLineEndings(text)
	text = stripInvisible(text)
	text = normalizeQuotes(text)
	text = stripMarkdown(text)
	text = compressBlankLines(text)
	return strings.TrimSpace(text)
}

// decodeText 自动检测编码并转 UTF-8(已是 UTF-8 直接返回)。
func decodeText(raw []byte) string {
	// 先试 UTF-8(包括带 BOM 的)
	if isUTF8(raw) {
		return string(raw)
	}
	// 再试 GB18030(GBK 超集,覆盖 GB2312/GBK)
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw); err == nil && isUTF8(decoded) {
		return string(decoded)
	}
	// 最后试 GBK
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw); err == nil && isUTF8(decoded) {
		return string(decoded)
	}
	// 兜底:强制当 UTF-8(无效字节变 U+FFFD)
	return string(raw)
}

// isUTF8 判断字节序列是否为合法 UTF-8。
func isUTF8(b []byte) bool {
	for i := 0; i < len(b); {
		if b[i] < 0x80 {
			i++
			continue
		}
		var n int
		switch {
		case b[i]&0xE0 == 0xC0:
			n = 2
		case b[i]&0xF0 == 0xE0:
			n = 3
		case b[i]&0xF8 == 0xF0:
			n = 4
		default:
			return false
		}
		if i+n > len(b) {
			return false
		}
		for j := 1; j < n; j++ {
			if b[i+j]&0xC0 != 0x80 {
				return false
			}
		}
		i += n
	}
	return true
}

// stripBOM 剥除 UTF-8 BOM(EF BB BF)。
func stripBOM(s string) string {
	return strings.TrimPrefix(s, "\uFEFF")
}

// normalizeLineEndings CRLF/CR → LF。
func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// stripInvisible 清理不可见字符(零宽空格/方向标记/控制符,保留换行和Tab)。
func stripInvisible(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r):
			b.WriteRune(' ') // 其它控制符→空格
		case r == '\u200B' || r == '\u200C' || r == '\u200D' ||
			r == '\uFEFF' || r == '\u202A' || r == '\u202C':
			// 零宽字符/方向标记:删除
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeQuotes 智能引号→直引号(防 LLM 在 JSON 输出中沿用弯引号导致解析失败)。
func normalizeQuotes(s string) string {
	s = strings.ReplaceAll(s, "\u201C", "\"") // "
	s = strings.ReplaceAll(s, "\u201D", "\"") // "
	s = strings.ReplaceAll(s, "\u2018", "'")  // '
	s = strings.ReplaceAll(s, "\u2019", "'")  // '
	s = strings.ReplaceAll(s, "\u300C", "「")  // 『→「
	s = strings.ReplaceAll(s, "\u300D", "」")  // 』→」
	return s
}

// stripMarkdown 剥离 Markdown 装饰符号(标题#/粗体**/斜体*/链接[])。
func stripMarkdown(s string) string {
	// 标题 # → 纯文本(保留内容)
	s = regexp.MustCompile(`(?m)^#{1,6}\s*`).ReplaceAllString(s, "")
	// 粗体/斜体 **text** *text* → text
	s = regexp.MustCompile(`\*\*([^*]+)\*\*`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\*([^*]+)\*`).ReplaceAllString(s, "$1")
	// 代码块 ``` → 删除标记保留内容
	s = regexp.MustCompile("```[a-zA-Z]*\\n?").ReplaceAllString(s, "")
	// 水平线 --- → 删除
	s = regexp.MustCompile(`(?m)^-{3,}\s*$`).ReplaceAllString(s, "")
	return s
}

// compressBlankLines 连续空行压缩为一个。
func compressBlankLines(s string) string {
	return regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
}

// ensureUTF8Bytes 确保字节是 UTF-8(用于检测原始文件编码)。
func ensureUTF8Bytes(b []byte) []byte {
	if isUTF8(b) {
		return b
	}
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b); err == nil {
		return decoded
	}
	return bytes.ToValidUTF8(b, []byte(" "))
}
