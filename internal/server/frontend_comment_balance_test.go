package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 前端模板的**注释必须成对**（2026-10-10 用户：「显示『（后端 cover-redo 会回 400…）-->』干嘛？
// 鼠标移动上去在 tip 气泡里显示不行吗？」）。
//
// 【现场】我在 `NovelsView.vue` 里改菜单项时，把一段注释的**开头那行 `<!--` 一起删掉了**，
// 于是那几行说明**变成了模板里的正文**（末尾还留着一个孤立的 `-->`）—— 用户在卡片菜单里
// **直接看见了这段说明文字**。`vite build` **不会报这种错**（它只是一段文本节点），
// 七门自检也照样全过；只有人眼看得见。
//
// 判据两条（都要，缺一条就漏）：
//  1. `<!--` 与 `-->` 计数相等；
//  2. 从左往右扫，计数**不许中途变负**（"先出现 `-->` 再出现 `<!--`"这种错位也能咬住：
//     只有计数相等时它是发现不了的）。
//
// 为什么扫**所有** .vue/.js 而不是只扫我改过的那个文件：同型缺陷在任何模板里都成立，
// 而这条判据便宜（纯文本扫描）。
func TestFrontendCommentsAreBalanced(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	files := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".vue", ".js":
		default:
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files++
		if open, close_, wentNegative := countHTMLComments(string(raw)); open != close_ || wentNegative {
			t.Errorf("%s 的模板注释不成对：<!-- %d 个 / --> %d 个（中途变负=%v）—— "+
				"少一个 `<!--` 就会把说明文字**渲染到界面上**，而 vite build 不会报错；"+
				"要给人看的说明请写进 title 气泡", filepath.ToSlash(rel), open, close_, wentNegative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 frontend/src 失败：%v", err)
	}
	if files < 5 {
		t.Fatalf("只扫到 %d 个前端文件 —— 闸门失效（路径写错了？）", files)
	}
}

// countHTMLComments 数 `<!--` / `-->` 的个数，并报"从左往右扫时是否中途变负"。
func countHTMLComments(s string) (open, close_ int, wentNegative bool) {
	balance := 0
	for i := 0; i+3 < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			open++
			balance++
			i += 4
		case strings.HasPrefix(s[i:], "-->"):
			close_++
			balance--
			if balance < 0 {
				wentNegative = true
			}
			i += 3
		default:
			i++
		}
	}
	return open, close_, wentNegative
}
