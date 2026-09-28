// Command renameprompts 把 internal/director/prompts/*.md 从"声明名＋序号"改成**可读名**。
//
// 【为什么做】这些文件名原来是 `sysH3Format_3.md` 这种 —— 从 Go raw string 里搬出来时按
// "定义处声明名 + 序号"自动生成的。问题在于：**改提示词在这个项目里极其频繁**，每次都要在
// 30 个不透明文件名里找"那段讲排除句的在哪"，可读名让每次改动都在回本。
//
// 【命名规则】`<声明名>__<标题短语>.md`；续段（原来的 _2/_3）继承父标题并加 `_p2/_p3`，
// 于是它们在同一目录里**紧挨着父文件**，扫一眼就知道是同一段的续写。
// 声明名前缀保留：`promptText("…")` 的引用点仍然一眼能看出它取自哪个 Go 变量。
//
// 【为什么用工具而不是手改】30 个文件重命名 + 全部引用同步，手改必错；且改名后
// `go test` 里的内容断言（逐个检查提示词正文）、每段非空守卫、载体恰好一份守卫都会立刻验证。
//
// 用法：go run ./tools/renameprompts          （打印计划，不落盘）
//
//	go run ./tools/renameprompts -apply   （真正执行）
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// slugOverrides 少数首行是纯中文的段，手工给一个英文短语（不硬凑拼音）。
var slugOverrides = map[string]string{
	"sysHighlightRules": "GRAB_THE_POINT", // 「抓重点纪律」
}

var (
	apply  = flag.Bool("apply", false, "真正执行（默认只打印计划）")
	reWord = regexp.MustCompile(`[A-Za-z0-9]+`)
)

func main() {
	flag.Parse()
	root := "internal/director"
	dir := filepath.Join(root, "prompts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && e.Name() != "README.md" {
			names = append(names, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(names)

	// 解析"声明名 + 序号"
	type seg struct {
		old, decl string
		part      int
		slug      string
	}
	var segs []seg
	slugOf := map[string]string{} // decl -> 首段 slug（续段继承）
	rePart := regexp.MustCompile(`^(.*)_(\d+)$`)
	for _, n := range names {
		decl, part := n, 1
		if m := rePart.FindStringSubmatch(n); m != nil {
			decl, part = m[1], atoi(m[2])
		}
		slug := slugOf[decl]
		if slug == "" {
			slug = slugFromFile(filepath.Join(dir, n+".md"), decl)
			slugOf[decl] = slug
		}
		segs = append(segs, seg{old: n, decl: decl, part: part, slug: slug})
	}
	// 先按 decl 分组定序，保证续段拿到父段的 slug
	sort.SliceStable(segs, func(i, j int) bool {
		if segs[i].decl != segs[j].decl {
			return segs[i].decl < segs[j].decl
		}
		return segs[i].part < segs[j].part
	})

	rename := map[string]string{}
	for _, s := range segs {
		newName := s.decl + "__" + s.slug
		if s.part > 1 {
			newName += fmt.Sprintf("_p%d", s.part)
		}
		rename[s.old] = newName
		fmt.Printf("  %-28s → %s.md\n", s.old, newName)
	}

	if !*apply {
		fmt.Println("\n（这是计划。加 -apply 执行）")
		return
	}
	// ① 改文件名
	for old, nw := range rename {
		if err := os.Rename(filepath.Join(dir, old+".md"), filepath.Join(dir, nw+".md")); err != nil {
			panic(err)
		}
	}
	// ② 同步所有 .go 里的 promptText("旧名") 引用
	changed := 0
	filepath.Walk(".", func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		src := string(raw)
		orig := src
		for old, nw := range rename {
			src = strings.ReplaceAll(src, `"`+old+`"`, `"`+nw+`"`)
		}
		if src != orig {
			if werr := os.WriteFile(p, []byte(src), 0o644); werr != nil {
				panic(werr)
			}
			changed++
			fmt.Printf("  引用已更新: %s\n", p)
		}
		return nil
	})
	fmt.Printf("\n完成：%d 个文件改名，%d 个 .go 文件的引用已更新\n", len(rename), changed)
}

// slugFromFile 从首行生成短语：去 markdown 记号，取前 4 个 ASCII 词。
func slugFromFile(path, decl string) string {
	if s, ok := slugOverrides[decl]; ok {
		return s
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "PART"
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#*-`> "))
		if line == "" {
			continue
		}
		words := reWord.FindAllString(line, -1)
		if len(words) == 0 {
			return "ZH" // 纯中文首行且没有手工映射：留个可辨认的占位
		}
		if len(words) > 4 {
			words = words[:4]
		}
		return strings.ToUpper(strings.Join(words, "_"))
	}
	return "PART"
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 1
		}
		n = n*10 + int(r-'0')
	}
	return n
}
