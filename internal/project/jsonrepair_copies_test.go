package project

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// `repairJSONStrings` / `quoteCloses` 在 `director` 与 `server` 两个包里各有一份，
// **必须逐字相同**（2026-09-29 代码健康审计 P1-9）。
//
// ## 为什么需要这条守卫
//
// 它们是"模型吐出的 JSON 里字符串没闭合 → 尽力修"的兜底，挂在方案解析、
// 方案头解析、分镜解析、小说拆集四条链上。两份是历史原因（两个包先后需要它），
// 而 `server` 那份有测试、`director` 那份**零测试** —— 于是在 server 那份上修一个
// 引号后瞻的 bug（正是 2026-09-11 实证过的形态：brief 照抄台词里的半角引号
// 把 JSON 提前截断），director 那份不会跟着修。
//
// 坏的表现是"生成分镜偶发失败"，而 server 的测试全绿 —— 静默、且发生在最难查的位置。
//
// ## 为什么不抽包
//
// 抽到 `internal/jsonx` 是正解，但那是一次跨包重构（要同时改四条调用链），
// 按本仓纪律该单独一批做。在那之前，这条守卫保证"两份不会悄悄漂开"：
// **任何一份改了而另一份没改，测试立刻红**，并告警"要么同步，要么去抽包"。
//
// 用 AST 比**函数体**而不是比整文件：两个文件的注释措辞本来就有差异（那是允许的），
// 而函数体（真正会执行的部分）必须一模一样。
func TestJSONRepairCopiesStayIdentical(t *testing.T) {
	root := repoRootForTest(t)
	type fn struct{ file, name string }
	pairs := []fn{
		{"internal/director/jsonrepair.go", "repairJSONStrings"},
		{"internal/server/jsonrepair.go", "repairJSONStrings"},
		{"internal/director/jsonrepair.go", "quoteCloses"},
		{"internal/server/jsonrepair.go", "quoteCloses"},
	}

	// 按函数名收集两份的函数体源码（AST 取位置区间，注释与格式差异不受影响）
	bodies := map[string]map[string]string{} // 函数名 → 文件 → body 源码
	for _, p := range pairs {
		full := filepath.Join(root, p.file)
		src, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("读不到 %s（路径变了要同步本测试）: %v", p.file, err)
		}
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, full, src, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", p.file, err)
		}
		found := false
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != p.name || fd.Body == nil {
				continue
			}
			// 用 `printer` 把 AST 打印出来比 —— 这比"取源码区间"更准确：
			// 源码区间里含**内联注释**，而两份的注释措辞本来就有差异（那是允许的，
			// 审计的原话是"逐字核对：`quoteCloses` 两处字节相同；`repairJSONStrings`
			// 逻辑相同，只有注释措辞不同"）。要守的是**逻辑**逐字相同。
			var buf bytes.Buffer
			if err := printer.Fprint(&buf, fset, fd.Body); err != nil {
				t.Fatalf("打印 %s 的函数体失败: %v", p.file, err)
			}
			if bodies[p.name] == nil {
				bodies[p.name] = map[string]string{}
			}
			bodies[p.name][p.file] = buf.String()
			found = true
		}
		if !found {
			t.Errorf("%s 里找不到函数 %s —— 函数被改名/移走了，本守卫需要跟着更新", p.file, p.name)
		}
	}

	for name, byFile := range bodies {
		if len(byFile) != 2 {
			continue // 上面已经报过"找不到"
		}
		var files, srcs []string
		for f, s := range byFile {
			files = append(files, f)
			srcs = append(srcs, s)
		}
		if srcs[0] != srcs[1] {
			t.Errorf("`%s` 的两份实现**已经漂开**：\n  %s\n  %s\n"+
				"两份必须逐字相同（改一份就要改另一份），否则在某一份上修的 bug 不会传播到另一条链 —— "+
				"而那正是 2026-09-11 那次「brief 照抄台词里的半角引号把 JSON 提前截断」踩过的坑。\n"+
				"正解是抽到共用包（如 internal/jsonx）并删掉另一份。",
				name, files[0], files[1])
		}
	}
}

// repoRootForTest 从测试文件位置往上找仓库根（有 go.mod 的那层）。
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("找不到仓库根（向上没有 go.mod）")
	return ""
}
