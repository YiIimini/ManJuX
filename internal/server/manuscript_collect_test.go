package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 拆集源文本的收集口:**同一份输入必须同时满足两件事** —— 非正文文件不进正文册、
// 卷序按章号而不是按路径。两条都是 2026-09-29 真机实测踩出来的,夹具照真实形态摆
// (真实书名/卷名/章名,README 里那句真的能骗过章头正则)。
//
// 【实测现场】《名册之外，八万零一号》88 章 11 卷,拆出 90 集:
//
//	· 第 01 集「内有当场兑现、章末有倒计时钩子（详见·research·审校记录·md·二）。」
//	  —— 内容是**这本书自己的目录结构说明**(README 第 34 行被当成章头);
//	· 第 11 集是第 49 章、第 19 集是第 17 章 —— 卷八 < 卷二 < 卷十 的路径序把卷序打乱,
//	  真正的前两章被挤到第 03、04 集。
//
// 这两条只有把"集号 vs 章号"并排比对才看得出来:集名一个个都对,整本书的集序是错的。
func TestCollectManuscriptRealBookLayout(t *testing.T) {
	book := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(book, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// ---- 非正文文件:内容里就带着能匹配章头正则的行(照真实 README 抄)----
	write("README.md", "# 《名册之外，八万零一号》\n\n"+
		"**唯一剩下的提示级输出**是 `--check-opening` 对第 1 章的 INFO（要人判的两问），人工判读已过：\n"+
		"第 1 章内有当场兑现、章末有倒计时钩子（详见 `research/审校记录.md` §二）。\n")
	write("research/审校记录.md", "| 1 | 第 1 章章末铭牌数读起来是 2 枚（《数字锚表》应为 1） |\n")
	write("research/六张表-转漫剧交接.md", "- 第 78 章（全环同时收到）是全书单章兑现密度最高的一章（5 次）\n")
	write("tools/gate.cmd", "@echo off\r\nrem 闸门\r\n")
	write("封面/封面提示词.md", "封面提示词\n")
	write("全本/名册之外，八万零一号·全本.md", "# 第001章 重新检测到生命反应\n\n正文。\n")
	// ---- 正文册:卷名照真实摆(卷八/卷一/卷十一 —— 路径序会把它们排乱)----
	write("正文/卷八_潮汐之下/第057章_潮水里有三秒的静.md", "第057章 潮水里有三秒的静\n\n潮水里有三秒的静。\n")
	write("正文/卷八_潮汐之下/第058章_下潜.md", "第058章 下潜\n\n下潜。\n")
	write("正文/卷十一_应答/第081章_纪权.md", "第081章 纪权\n\n纪权。\n")
	write("正文/卷十一_应答/第088章_收到.md", "第088章 收到\n\n收到。\n")
	for _, name := range []string{
		"第001章_重新检测到生命反应", "第002章_一号之外", "第003章_我报地址不报名字",
		"第004章_第一枚铭牌认了主", "第005章_环是断的", "第006章_回收单当场作废",
	} {
		write("正文/卷一_名册之外/"+name+".md", fmt.Sprintf("%s\n\n正文。\n", name))
	}

	paths, nChaps := collectManuscript(book)
	if nChaps != 10 {
		t.Fatalf("章号册应为 10 册,实得 %d:%v", nChaps, paths)
	}
	if len(paths) != 10 {
		t.Fatalf("一章一档时只应收章号册(README/研究稿/封面/全本都不算),实得 %d 册:%v", len(paths), paths)
	}
	for _, p := range paths {
		rel, err := filepath.Rel(book, p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(filepath.ToSlash(rel), "正文/") {
			t.Errorf("非正文文件混进了正文册:%s —— 它内容里的「第 N 章…」会被当成章头,拆出一集自述文档", rel)
		}
	}
	// 顺序:**章号升序**(不是路径序)。卷八 < 卷一 < 卷十一 的字节序恰好会把 057 排到 001 前面
	want := []int{1, 2, 3, 4, 5, 6, 57, 58, 81, 88}
	got := make([]int, 0, len(paths))
	for _, p := range paths {
		got = append(got, chapterNumFromFilename(filepath.Base(p)))
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("正文册顺序必须是章号升序:\n got %v\nwant %v\n(路径序会把卷序打乱,拆集集序随之与故事顺序脱钩)", got, want)
	}
}

// 旧形态回归:没有「正文」子树的书写目录(整本/文件名不带章号)必须保持"全收 + 路径序"。
// 新规则只该收窄"一章一档"这一路,不许顺手改掉老书的行为。
func TestCollectManuscriptFallsBackToPathOrder(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"b-第二段.txt", "a-第一段.txt", "README.md"} {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte("正文。\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, nChaps := collectManuscript(dir)
	if nChaps != 0 {
		t.Fatalf("这些文件名没有章号,不该被当成章号册,实得 %d", nChaps)
	}
	var got []string
	for _, p := range paths {
		got = append(got, filepath.Base(p))
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{"a-第一段.txt", "b-第二段.txt"}) {
		t.Errorf("没有章号时按路径序全收(README 除外),实得 %v", got)
	}
}
