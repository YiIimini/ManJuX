// Package textutil 跨包共用的文本工具。
package textutil

// Truncate 按**字符**(而非字节)截断,超出时追加省略号。
//
// 按 rune 截断是有意为之:中文字符在 UTF-8 下占 3 字节,按字节截会把一个汉字
// 劈成半个、产出乱码(项目曾因此出现 U+FFFD 替换符)。此函数此前在 llm /
// comfyengine / server / h3api 四个包里各有一份**逐字相同**的实现
// (名字分别为 truncate / truncateStr / trunc),现收敛为本函数。
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
