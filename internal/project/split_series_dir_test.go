package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 「同一本书裂成多个书名目录」的检测（2026-09-29 数据完整性审计 #2）。
//
// ## 为什么是"检测"而不是"自动合并"
//
// 书名目录经历过两次命名规范化（全角逗号 → 间隔号 `·`；随后又放行全角标点），
// 于是同一本书可能留下两个目录。**实盘证据**：`data/projects/` 下同时存在
// `补碑人改命，守钟女逆时`（17:04）与 `补碑人改命·守钟女逆时`（15:24）。
//
// 裂开的后果是覆盖核对 / 自动补缺 / 按名复用**只看到一半** —— 缺章误报、
// "重拆不复用旧集"、封面只刷新一半。
//
// 但**合并要改目录名、要在两边的同名集之间取舍** —— 那是不可逆的数据搬迁，
// 不该由平台替用户决定。所以这里做的是"检测 + 如实播报"，测试也按这个契约写。
func TestSplitSeriesDirDetection(t *testing.T) {
	root := t.TempDir()
	mk := func(name string) {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mkProject := func(name string) { // 带 project.json 的是**单集项目**，不是书名目录
		mk(name)
		if err := os.WriteFile(filepath.Join(root, name, "project.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 实盘形态：同一本书的全角逗号版与间隔号版
	mk("补碑人改命，守钟女逆时")
	mk("补碑人改命·守钟女逆时")
	// 只有一份的书（不该报）
	mk("名册之外，八万零一号")
	// 单集项目恰好叫得像同一本书 —— 有 project.json，不算书名目录，不参与比较
	mkProject("补碑人改命·守钟女逆时·第01集")

	s, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	reports := s.splitSeriesDirReports()

	if len(reports) != 1 {
		t.Fatalf("该报 1 组（只有那本书裂成两份），实际 %d 组：%v", len(reports), reports)
	}
	msg := reports[0]
	for _, want := range []string{"补碑人改命，守钟女逆时", "补碑人改命·守钟女逆时"} {
		if !strings.Contains(msg, want) {
			t.Errorf("播报里必须点名两个目录，缺 %q：%s", want, msg)
		}
	}
	// 必须说明"为什么不自动合并"以及"用户该怎么做"——否则只是一句抱怨
	if !strings.Contains(msg, "不自动合并") {
		t.Error("播报要讲清平台为什么不自动合并（避免用户以为这是 bug）")
	}
	if !strings.Contains(msg, "资源管理器") {
		t.Error("播报要给出下一步动作（在哪里并、保留哪个名字）")
	}
	// 单集项目名与书名同族时不该把它算进来
	if strings.Contains(msg, "第01集") {
		t.Error("带 project.json 的单集项目不是书名目录，不该参与同名比较")
	}

	// 名字归一化：全角逗号/顿号/空格/中点都算同一个分隔符
	same := [][2]string{
		{"补碑人改命，守钟女逆时", "补碑人改命·守钟女逆时"},
		{"a b", "a·b"},
		{"a、b", "a,b"},
		{"a　b", "a b"}, // 全角空格
	}
	for _, c := range same {
		if a, b := normalizeBookPunct(c[0]), normalizeBookPunct(c[1]); a != b {
			t.Errorf("「%s」与「%s」该归一成同一个键，实际 %q vs %q", c[0], c[1], a, b)
		}
	}
	// 不同书名必须不同（别把两本书并成一组）
	if normalizeBookPunct("甲书") == normalizeBookPunct("乙书") {
		t.Error("不同书名不该归一成同一个键")
	}
	// 纯标点的目录名归一成空串（调用方跳过），不参与比较
	if normalizeBookPunct("，、·") != "" {
		t.Errorf("纯标点该归一成空串，实际 %q", normalizeBookPunct("，、·"))
	}
}
