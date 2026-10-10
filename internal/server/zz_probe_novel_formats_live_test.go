// zz_probe_novel_formats_live_test.go 真机探针（用户「小说库解析新增文本格式」，2026-10-10）。
//
// 拿**盘上那本真书**量一遍（默认 `data/小说/离婚后，千亿前夫跪求复合`，可用
// `MANJUX_NOVEL_PROBE_DIR` 指到别的书），要看到的是：
//
//	① 目录里到底是什么格式（有哪些平台读、哪些平台不读）；
//	② `collectManuscript` 收了几册、**顺序**是不是章号（一章一集的地基）；
//	③ 每册抽出来的正文字数、有没有章头行、`SplitChapters` 认出的章号；
//	④ 合并成一本书（ensureChapterHead + 换行 + NormalizeNovelText）之后，章头数对不对
//	   —— 这一步就是拆集 LLM 真正看到的那份文本；
//	⑤ "平台不读的格式"点名文案长什么样；
//	⑥ 书卡统计（scanNovelDir 的 Chapters / Chars）。
//
// 跑法：$env:MANJUX_LIVE_PROBE=1; go test ./internal/server/ -run TestZZProbeNovelFormats -v
package server

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestZZProbeNovelFormats(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") != "1" {
		t.Skip("设 MANJUX_LIVE_PROBE=1 才跑（读盘上真机那本书）")
	}
	book := strings.TrimSpace(os.Getenv("MANJUX_NOVEL_PROBE_DIR"))
	if book == "" {
		book = filepath.Join("..", "..", "data", "小说", "离婚后，千亿前夫跪求复合")
	}
	if fi, err := os.Stat(book); err != nil || !fi.IsDir() {
		t.Skipf("盘上没找到这本书（%v）：%s", err, book)
	}
	t.Logf("书目录：%s", book)

	// ① 扩展名分布（这就是"这本书到底是什么格式"的现场）
	counts := map[string]int{}
	var all []string
	_ = filepath.WalkDir(book, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := novelExt(p)
		if ext == "" {
			ext = "（无扩展名）"
		}
		counts[ext]++
		all = append(all, filepath.Base(p))
		return nil
	})
	sort.Strings(all)
	exts := make([]string, 0, len(counts))
	for e := range counts {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	var parts []string
	for _, e := range exts {
		mark := "不读"
		if isNovelTextFile("x" + e) {
			mark = "读"
		}
		parts = append(parts, e+" ×"+strconv.Itoa(counts[e])+"（"+mark+"）")
	}
	t.Logf("① 格式分布：%s", strings.Join(parts, "、"))
	t.Logf("   文件：%s", strings.Join(all, "、"))

	// ② 章节册清单
	paths, nChaps := collectManuscript(book)
	t.Logf("② collectManuscript：章号册 %d 册 / 共 %d 册", nChaps, len(paths))
	for i, p := range paths {
		t.Logf("   [%d] %s", i+1, filepath.Base(p))
	}
	if len(paths) == 0 {
		t.Errorf("盘上这本书一个正文册都收不到 —— 格式支持又漏了（文件列表见上）")
		return
	}

	// ③ 逐册抽正文
	var sb strings.Builder
	for _, p := range paths {
		text, err := readNovelTextFile(p)
		if err != nil {
			t.Errorf("   ✗ %s 读不出来：%v", filepath.Base(p), err)
			continue
		}
		chaps := SplitChapters(text)
		var nums []string
		for _, c := range chaps {
			nums = append(nums, strconv.Itoa(c.Num))
		}
		pre := strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " ")), " ")
		if r := []rune(pre); len(r) > 60 {
			pre = string(r[:60]) + "…"
		}
		t.Logf("   ✓ %s：%d 字（rune）｜章头 %d 行｜切出章号 %s｜首句 %q",
			filepath.Base(p), len([]rune(text)), countChapterHeads(text), strings.Join(nums, ","), pre)
		sb.WriteString(ensureChapterHead(filepath.Base(p), text))
		sb.WriteString("\n\n")
	}

	// ④ 合并成一本书之后（= 拆集真正看到的那份文本）
	merged := NormalizeNovelText([]byte(sb.String()))
	chaps := SplitChapters(merged)
	t.Logf("④ 合并后：%d 字（rune）｜章头 %d 行｜SplitChapters %d 章", len([]rune(merged)), countChapterHeads(merged), len(chaps))
	var seq []string
	for _, c := range chaps {
		seq = append(seq, strconv.Itoa(c.Num)+" "+c.Title)
	}
	t.Logf("   章序：%s", strings.Join(seq, "｜"))
	if len(chaps) < nChaps {
		t.Errorf("合并后只认出 %d 章，而章节册有 %d 册 —— 章头识别或抽取有问题（拆集会按错的章界切）", len(chaps), nChaps)
	}

	// ⑤ 不读的格式点名
	if rep := novelUnsupportedReport(book, 3); rep != "" {
		t.Logf("⑤ 点名：%s", rep)
	} else {
		t.Logf("⑤ 点名：这本书里没有「平台不读」的文档格式")
	}

	// ⑥ 书卡统计
	s := newTestServer(t)
	it := s.scanNovelDir(book, filepath.Base(book))
	t.Logf("⑥ 书卡：章数 %d｜字数估算 %d｜封面 %q", it.Chapters, it.Chars, it.Cover)
}
