package compose

import "strings"

// ass_escape.go 写进 ASS 的**每一段文字都必须先转义 `{` `}`**。
//
// 【为什么必须做（技能侧 2026-09-28 通知 + 我方实证）】
// ASS 格式里 `{ }` 是**覆写标签块**（`\an5`、`\pos(...)` 这类），而块内**不识别的内容会被静默忽略**
// —— 依据：Subtitle Edit《ASSA Override Tags Reference》：
// "Override tags are enclosed in curly braces `{}`" / "Any unrecognized text within override
// blocks is silently ignored"。⇒ 一句台词或场次字幕里只要有一个 `{`，从那里到 `}` 的**字就凭空消失**，
// 而且**不报错**（成片里表现为"字幕少了半句"，最难查的那类）。
//
// 平台的字幕链路（`buildASS`）此前直接把原文 `%s` 拼进 `Dialogue:` 行 —— 也就是把这句话的风险
// 原样收下了。角色名/场景名/台词都可能带 `{`（用户与模型都可能写），所以转义写在这一处：
// **唯一写 ASS 文本的地方**，谁都绕不过去。
//
// 为什么是转义而不是"替换成别的字"：技能侧明确要求**不要静默改字**（章名是集名，改字会让成片与
// 集号记录不一致）。`\{` `\}` 在 libass 里就渲染成 `{` `}` 本身 —— **屏幕上看到的仍是原文**，
// 只是不再被当成标签块。这是"显示不变、语义安全"的唯一做法。
//
// 反斜杠本身不用动：ASS 的覆写标签只在 `{}` **内部**生效，块外的 `\N` 之类是普通字符
// （不转义也不会被解释）。只治真正会吞字的那个字符对。

// assEscape 转义写进 ASS 的文本：`{` → `\{`，`}` → `\}`。
func assEscape(s string) string {
	if !strings.ContainsAny(s, "{}") {
		return s // 绝大多数台词没有花括号，省掉两次扫描
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '{', '}':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
