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
