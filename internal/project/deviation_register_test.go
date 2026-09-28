package project

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 「与官方的有意分歧」登记表与代码标记必须**两边一致**。
//
// 【为什么要这条守卫】项目里有一批"官方要求 X、平台故意做 Y"的地方（失败镜不拆新镜、单镜上限 10 秒、
// 字幕默认烧、只走 Ref2VA、每镜配乐写 N/A）。它们最容易出的两种事故：
//
//	① 有人读到官方文档，以为平台实现是 bug，顺手"修"回去 ⇒ 把一个有理由的设计决定推翻；
//	② 情况变了（比如用户改口径），代码改了但表没改 ⇒ 表变成**假文档**，下一个人照它做错事。
//
// 代码里带 `有意分歧 DEV-NN` 标记、表里逐条写理由，这条守卫把两边钉在一起：
// 表里列了的、代码里必须标；代码里标了的、表里必须列。
//
// 完整理由见 docs/与官方的有意分歧.md（守卫只查"两边有没有对上"，不查理由写得好不好）。
func TestDeviationRegisterMatchesCode(t *testing.T) {
	root := filepath.Join("..", "..")
	docPath := filepath.Join(root, "docs", "与官方的有意分歧.md")
	docRaw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("读不到分歧登记表（它是防「有意为之被当 bug 修」的唯一出处）: %v", err)
	}
	doc := string(docRaw)

	docIDs := map[string]bool{}
	for _, m := range regexp.MustCompile(`DEV-\d+`).FindAllString(doc, -1) {
		docIDs[m] = true
	}
	if len(docIDs) == 0 {
		t.Fatal("登记表里一个 DEV-NN 都没有 —— 表被清空了？")
	}

	// 扫代码里的标记（只扫 internal/，且跳过本文件：它自己就含 DEV-\d+ 这个模式）
	codeIDs := map[string]string{}
	internal := filepath.Join(root, "internal")
	_ = filepath.Walk(internal, func(p string, info os.FileInfo, werr error) error {
		if werr != nil || info == nil || info.IsDir() || !strings.HasSuffix(p, ".go") ||
			strings.HasSuffix(p, "deviation_register_test.go") {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "有意分歧") {
				continue
			}
			if m := regexp.MustCompile(`DEV-\d+`).FindString(line); m != "" {
				if _, dup := codeIDs[m]; !dup {
					codeIDs[m] = filepath.ToSlash(p)
				}
			}
		}
		return nil
	})

	// ① 代码里标的，表里必须有
	var missingInDoc []string
	for id, where := range codeIDs {
		if !docIDs[id] {
			missingInDoc = append(missingInDoc, id+" ("+where+")")
		}
	}
	sort.Strings(missingInDoc)
	if len(missingInDoc) > 0 {
		t.Errorf("代码里标了「有意分歧」但登记表里没有（补进 docs/与官方的有意分歧.md）：\n  %s",
			strings.Join(missingInDoc, "\n  "))
	}
	// ② 表里列了的，代码里必须有标记（否则理由漂在空中，没人会在读到实现时被告知）
	var missingInCode []string
	for id := range docIDs {
		if _, ok := codeIDs[id]; !ok {
			missingInCode = append(missingInCode, id)
		}
	}
	sort.Strings(missingInCode)
	if len(missingInCode) > 0 {
		t.Errorf("登记表里列了但没有代码标记（在对应实现处加「有意分歧 %s（勿当 bug 修）」）：%v",
			missingInCode[0], missingInCode)
	}
	// ③ 每条都必须写清"什么时候该改回去"——只写"我们不一样"是不够的
	for id := range docIDs {
		idx := strings.Index(doc, id)
		if idx < 0 {
			continue
		}
		rest := doc[idx:]
		if end := strings.Index(rest, "\n## "); end > 0 {
			rest = rest[:end]
		}
		if !strings.Contains(rest, "什么时候该改回去") {
			t.Errorf("%s 缺少「什么时候该改回去」——分歧条目必须写清什么条件下该改，否则下一个人无从判断", id)
		}
	}
}
