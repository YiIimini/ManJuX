# 提示词正文（director 的全部提示词都在这里）

这些 `.md` 就是**发给模型的提示词正文**，由 `prompts.go` 用 `go:embed` 读进来，在 `director.go`
里按原有顺序拼接。改提示词 = 改这里，不用碰 Go 代码。

## 为什么不在 Go 里写（2026-09-28 根因治理）

提示词原本写成 `const sysXxx = <反引号>...<反引号>` 的 raw string —— Go 的 raw string 用反引号定界，
于是**正文里出现任何一个反引号**（写 `[Shot 1]`、写文件路径、写行内代码）都会截断字符串、编译报错。
那天为此栽了**七次**，每次都得中断思路去修一句本可以自由写的文字。

搬进 Markdown 之后：反引号自由、diff 干净、能像文章一样读，改提示词不再需要懂 Go 语法。

## 纪律

1. **正文原样进模型**：`.md` 内容不做二次加工（拼接顺序在 `director.go`，它是唯一的加工点）。
2. **文件名 = 声明名 + 标题短语**（2026-09-28 起）：`sysH3Format__H3_PROMPT_FORMAT_CRITICAL.md`。
   前半段是 Go 里的变量名（`grep -n 'sysH3Format' director.go` 就能找到调用点），
   后半段是第一行的标题短语；续段带 `_p2/_p3` 后缀，紧挨着父文件排。
   命名由 `tools/renameprompts` 生成（规则写在它的头注释里），不要手改名 ——
   改名前先想清楚：全仓的 `promptText("…")` 引用都要跟着动。
3. **不许在 `director.go` 里再写散文型 raw string**：守卫 `TestNoRawStringPromptsInDirector` 会红
   （判据：raw string 内容 ≥120 字符或含换行 = 散文；短单行的正则/格式串不算）。
4. **每段必须有正文**：守卫 `TestEveryPromptSegmentHasText` 会红。
5. 搬运工具：`tools/extractprompts`（一次性工具，留着是为了记录这件事是怎么做的、以及踩过的三个坑）。


