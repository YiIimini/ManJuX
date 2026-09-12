// novel_format_test.go 回归:分节定界协议解析器(2026-09-11 v0.33.16)。
// 样例含用户实炸的对话台词(任意引号)与标记变体。
package server

import (
	"strings"
	"testing"
)

const epBlockSample = `BOOK: 仙人渡劫先来我这报备
STYLE: ghostblade-tech style (semi-realistic CG thick-paint)
===EP 1===
TITLE: 报备
CHAPTERS: 第1章~第2章
BRIEF:
雷九赶紧把账本抽走擦干:「这本账不能湿。」计昭站在门口问:「记事的栏写上去,就是报了备。报备的东西,是要办的。你办不办?」雷九把木匣盖上,抬手压了两下,抬头看她,眼睛很亮:「办。」「按流程办。」
朵朵睡在旧账封面上下了小雨。
===EP 2===
TITLE: 旧账
CHAPTERS: 第3章
BRIEF:
隔壁老伯来串门,聊起十年前的旧事。"那时候规矩多,"他说,"现在倒好。"`

func TestParseEpBlocks(t *testing.T) {
	eps, book, style, ok := parseEpBlocks(epBlockSample)
	if !ok || len(eps) != 2 {
		t.Fatalf("应解析 2 集,ok=%v len=%d", ok, len(eps))
	}
	if book != "仙人渡劫先来我这报备" || !strings.HasPrefix(style, "ghostblade") {
		t.Fatalf("book/style 错误: %q %q", book, style)
	}
	if eps[0].Ep != 1 || eps[0].Title != "报备" || eps[0].Chapters != "第1章~第2章" {
		t.Fatalf("第1集字段错误: %+v", eps[0])
	}
	if !strings.Contains(eps[0].Brief, "「按流程办。」") || !strings.Contains(eps[0].Brief, "朵朵睡在旧账") {
		t.Fatalf("第1集 brief 内容丢失: %q", eps[0].Brief)
	}
	if !strings.Contains(eps[1].Brief, `"那时候规矩多,"`) {
		t.Fatalf("第2集 brief 半角引号内容丢失: %q", eps[1].Brief)
	}
}

func TestParseEpBlocksMarkerVariants(t *testing.T) {
	raw := `=== EP #5 ===
TITLE: 变体
CHAPTERS: 第9章
BRIEF: 同行起头正文
<<<EP 6>>>
标题: 变体二
章节: 第10章
大纲:
正文另起行`
	eps, _, _, ok := parseEpBlocks(raw)
	if !ok || len(eps) != 2 {
		t.Fatalf("标记变体应解析 2 集: ok=%v len=%d", ok, len(eps))
	}
	if eps[0].Ep != 5 || eps[0].Title != "变体" || eps[0].Brief != "同行起头正文" {
		t.Fatalf("=== EP #5 === 变体解析错误: %+v", eps[0])
	}
	if eps[1].Ep != 6 || eps[1].Title != "变体二" || eps[1].Chapters != "第10章" {
		t.Fatalf("中文标签变体解析错误: %+v", eps[1])
	}
}

// JSON 输入必须落回兜底路径(ok=false),不能误判成分节格式。
func TestParseEpBlocksJSONFallback(t *testing.T) {
	if _, _, _, ok := parseEpBlocks(`{"book":"书","episodes":[{"ep":1,"title":"t","chapters":"c","brief":"b"}]}`); ok {
		t.Fatalf("JSON 输入不应被当成分节格式")
	}
}

// 截断尾段:最后一集 brief 只有部分内容也应抢救出来。
func TestParseEpBlocksTruncatedTail(t *testing.T) {
	raw := `===EP 1===
TITLE: 全
CHAPTERS: 第1章
BRIEF:
完整内容。
===EP 2===
TITLE: 截
CHAPTERS: 第2章
BRIEF:
只写了一半就`
	eps, _, _, ok := parseEpBlocks(raw)
	if !ok || len(eps) != 2 || !strings.Contains(eps[1].Brief, "只写了一半") {
		t.Fatalf("截断尾段应抢救: ok=%v len=%d", ok, len(eps))
	}
}

// 章界工具:文件名注入 + 章头计数(2026-09-11 实证全本 md 无章头断链)。
func TestChapterHeadHelpers(t *testing.T) {
	if h := chapterHeadFromFilename("第001章_叁佰玖拾玖.md"); h != "第001章 叁佰玖拾玖" {
		t.Fatalf("文件名解析错误: %q", h)
	}
	if h := chapterHeadFromFilename("随便什么.md"); h != "" {
		t.Fatalf("非章文件应返回空: %q", h)
	}
	// 内容无章头 → 注入;有 → 不动
	injected := ensureChapterHead("第002章_雷泽集.md", "雷泽集的清晨\n")
	if !strings.HasPrefix(injected, "第002章 雷泽集\n") {
		t.Fatalf("应注入章头: %q", injected[:30])
	}
	kept := ensureChapterHead("第002章_雷泽集.md", "第002章 雷泽集\n\n正文")
	if kept != "第002章 雷泽集\n\n正文" {
		t.Fatalf("已有章头不应重复注入: %q", kept)
	}
	if n := countChapterHeads("第001章 甲\n正文\n第002章 乙\n第003章_丙\n普通行"); n != 3 {
		t.Fatalf("章头计数错误: %d", n)
	}
}

// 目录剥离:全本 md 目录区(≥6 行列表)整块删除,正文不受影响。
func TestStripChapterTOC(t *testing.T) {
	src := "# 书\n\n## 目录\n\n- 第001章 甲\n- 第002章 乙\n- 第003章 丙\n- 第004章 丁\n- 第005章 戊\n- 第006章 己\n\n## 正文\n\n第001章 甲\n\n正文开始。\n"
	got := stripChapterTOC(src)
	if strings.Contains(got, "- 第00") {
		t.Fatalf("目录区应被剥除: %q", got)
	}
	if !strings.Contains(got, "第001章 甲\n\n正文开始") {
		t.Fatalf("正文章头应保留: %q", got)
	}
	// 少于 6 行的类似列表不误删
	keep := "引言:\n- 第一章 起点\n- 第二章 转折\n\n正文"
	if stripChapterTOC(keep) != keep {
		t.Fatalf("短列表不应误删: %q", stripChapterTOC(keep))
	}
}

// TestSplitChaptersAcceptsMarkdownHeads 章头识别必须认 markdown 的 `# 第NNN章`。
//
// 回归 2026-09-12(用户实证「为什么还是解析的全本」+ 缺章 9/10/11):全本 md 的章头
// 写作 `# 第009章 洗出来的字`,而旧正则要求章头是**裸行**,于是一本 64 章的书只认出
// 3 个章头 —— 切块/章计数/覆盖核对/原文提取四个环节同时失准,最终"自动补缺"误判缺章、
// 删掉了本来正确的分集(64 集 → 63 集,第 009 章内容真丢)。
func TestSplitChaptersAcceptsMarkdownHeads(t *testing.T) {
	text := "# 第008章 夜市最后一夜\n\n第三天上午九点。\n\n# 第009章 洗出来的字\n\n灯下看字。\n\n第010章 万宝行\n\n柜台很高。\n\n## 第011章 死当\n\n封条。\n"
	ch := SplitChapters(text)
	if len(ch) != 4 {
		t.Fatalf("应认出 4 章(markdown 与裸章头混排),实际 %d:%+v", len(ch), ch)
	}
	want := []struct {
		num   int
		title string
	}{
		{8, "夜市最后一夜"}, {9, "洗出来的字"}, {10, "万宝行"}, {11, "死当"},
	}
	for i, w := range want {
		if ch[i].Num != w.num || ch[i].Title != w.title {
			t.Errorf("第 %d 章切片不符: got num=%d title=%q, want num=%d title=%q",
				i+1, ch[i].Num, ch[i].Title, w.num, w.title)
		}
	}
	// 正文必须带上章头行(分镜 LLM 需要看到章界)且不含下一章内容
	if !strings.Contains(ch[1].Text, "第009章") || strings.Contains(ch[1].Text, "万宝行") {
		t.Errorf("第 9 章正文边界不对:%q", ch[1].Text)
	}
}

// TestChapterTitlesFromFiles 一章一档的文件名解析(第001章_三百块的活儿.md)。
func TestChapterTitlesFromFiles(t *testing.T) {
	got := ChapterTitlesFromFiles([]string{
		"第001章_三百块的活儿.md", "第009章_洗出来的字.md", "第010章 万宝行.md", "不是章节.md",
	})
	if got[1] != "三百块的活儿" || got[9] != "洗出来的字" || got[10] != "万宝行" {
		t.Fatalf("文件名解析不符:%v", got)
	}
	if _, ok := got[0]; ok {
		t.Error("非章节文件不应被解析")
	}
}

// TestChineseNum 章号解析(阿拉伯与中文数字)。
func TestChineseNum(t *testing.T) {
	cases := map[string]int{"001": 1, "9": 9, "九": 9, "十": 10, "十一": 11, "二十三": 23, "一百二十": 120, "": 0, "abc": 0}
	for in, want := range cases {
		if got := chineseNum(in); got != want {
			t.Errorf("chineseNum(%q) = %d, want %d", in, got, want)
		}
	}
}
