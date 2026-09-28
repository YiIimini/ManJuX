// Package gatecat 生成《门目录》：把 plan_contract.go 里每一道契约门编号（G001…）、
// 标出级别与档位，并写成一页可检索的文档。
//
// 为什么要生成而不是手写：手写的清单一定会过期（这一周已经栽过"文档说某条门存在、代码里其实没有"
// 的坑）。这里把**源码当唯一事实源**：目录由工具扫 AST 得出，配套测试发现过期就红。
//
// 来源：开源参考实现 drama-skills(MIT) 的规则编号体系（SHT-01..29 / CON-01..07）与四档分类
// （structural_invariant / reviewed_invariant / craft_default / taste_option）。
// 平台化：档位按**级别**落两档 —— fail 属 structural（不修不许渲），warn 属 craft（建议，可放行）；
// 四档里另外两档（reviewed/taste）平台暂不区分，等到"用户说某条太严要关掉"时再拆。
package gatecat

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

// Gate 一道门。
type Gate struct {
	ID    string // G001…
	Level string // fail / warn
	Tier  string // structural / craft
	Where string // plan.shots / plan.props …
	What  string // 这道门在管什么（取 format 的首句）
}

// TierOf 级别 → 档位（平台两档制，见包注释）。
func TierOf(level string) string {
	if level == "fail" {
		return "structural"
	}
	return "craft"
}

// DefaultSources 门所在的源文件（**唯一出处**：工具、测试、文档都从这里取）。
//
// 【2026-09-29】原来是单个 `plan_contract.go` —— 而 `ValidatePlan` 拆分之后（审计 P2-11），
// 判据搬到了 `plan_checks.go`，所有写死旧路径的地方当场从"106 道门"变成"3 道门"。
//
// **这类"扫源码的工具"必须有一个共享的清单**：写死路径的地方每多一处，搬家时就多一个
// 安静失效的点。现在工具与测试都读这一份。
var DefaultSources = []string{
	"internal/project/plan_contract.go",
	"internal/project/plan_checks.go",
}

// Collect 扫源码，按出现顺序收集所有契约门。
//
// **可以传多个文件**（2026-09-29）：`ValidatePlan` 拆分后判据分布在
// `plan_contract.go`（入口）与 `plan_checks.go`（各分组），单文件签名会让这个工具
// 安静地少报（门目录从 106 道变成 3 道，而没有任何测试会因此变红 ——
// 除了 `TestCatalogueCoversRealGates`，那条门正是为这种情况写的）。
// 顺序 = 传入顺序 = 执行顺序，与门目录里的编号含义一致。
func Collect(srcPaths ...string) ([]Gate, error) {
	var gates []Gate
	for _, srcPath := range srcPaths {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, srcPath, nil, 0)
		if err != nil {
			return nil, err
		}
		// **编号跨文件连续**：把已有门的个数传进去当起点，否则每个文件都从 G001 重来
		// （门目录里会出现两个 G001，而"编号稳定"正是它被引用的前提 —— 注释里写
		// "见 G057"这种事全靠编号不漂）。
		gates = append(gates, collectFrom(fset, f, len(gates))...)
	}
	return gates, nil
}

// collectFrom 从单个已解析的文件里收门（`from` 是已收集的个数，用于续编号）。
func collectFrom(fset *token.FileSet, f *ast.File, from int) []Gate {
	var gates []Gate
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "add" || len(call.Args) < 3 {
			return true
		}
		level := litOrEmpty(call.Args[0])
		if level != "fail" && level != "warn" {
			return true
		}
		where := describeExpr(call.Args[1])
		if where == "?" || strings.TrimSpace(where) == "" {
			// 位置常由变量算出（where := fmt.Sprintf("shots[%d]", sh.ID)）—— 在**同一个函数内**
			// 往回找最近一次赋值，取出里头的字面量。跨函数不算（同名变量会串味）。
			where = resolveWhereVar(f, fset, call)
		}
		what := firstClause(describeExpr(call.Args[2]))
		// 兜底：格式串/位置由变量拼出来时，取该调用里**第一个字符串字面量**当说明
		// （比留空有用，也不会瞎编）。真取不到就如实写「见源码」。
		if strings.TrimSpace(what) == "" || what == "?" {
			// 只在**第三个参数之后**找：前两个是级别与位置，取到它们是错的（第一版就取了 "warn"）
			what = firstClause(firstStringIn(call.Args[2]))
		}
		if strings.TrimSpace(what) == "" {
			what = "（格式串由变量拼出，见源码对应位置）"
		}
		if strings.TrimSpace(where) == "" || where == "?" {
			// 位置由变量算出（如 where := fmt.Sprintf(...)）时如实写「动态」，不猜
			where = "（动态）"
		}
		gates = append(gates, Gate{
			ID:    fmt.Sprintf("G%03d", from+len(gates)+1),
			Level: level,
			Tier:  TierOf(level),
			Where: where,
			What:  what,
		})
		return true
	})
	return gates
}

// Render 生成 Markdown 目录（可传多个源文件，见 Collect 的说明）。
func Render(srcPaths ...string) (string, error) {
	gates, err := Collect(srcPaths...)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# 门目录（契约门总表）\n\n")
	b.WriteString("> **本文件由 `tools/gatecat` 从 `internal/project/plan_contract.go` 生成，不要手改** ——\n")
	b.WriteString("> 有一条测试（`internal/gatecat`）发现它与源码不一致就会红。\n>\n")
	b.WriteString("> 用途：口头定位某道门（例：「G014 太严」），以及看清哪些是**不许渲**的硬门、哪些只是建议。\n\n")
	b.WriteString("档位口径（来源：开源参考实现 drama-skills 的四档分类，平台落两档）：\n\n")
	b.WriteString("- `structural`（**fail**）—— 结构性不变量：不满足就**不许进入渲染**，必须改方案。\n")
	b.WriteString("- `craft`（**warn**）—— 工艺建议：会播报出来，但**不阻断**；可放行、可事后返工。\n\n")
	fails, warns := 0, 0
	for _, g := range gates {
		if g.Level == "fail" {
			fails++
		} else {
			warns++
		}
	}
	fmt.Fprintf(&b, "共 **%d** 道门：structural %d 道、craft %d 道。\n\n", len(gates), fails, warns)
	b.WriteString("| 编号 | 档位 | 级别 | 位置 | 这道门在管什么 |\n|---|---|---|---|---|\n")
	for _, g := range gates {
		fmt.Fprintf(&b, "| `%s` | %s | %s | `%s` | %s |\n", g.ID, g.Tier, g.Level, g.Where, g.What)
	}
	return b.String(), nil
}

// Write 直接落盘（可传多个源文件）。
func Write(outPath string, srcPaths ...string) error {
	md, err := Render(srcPaths...)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(md), 0o644)
}

func litOrEmpty(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s := strings.Trim(lit.Value, "`")
	if strings.HasPrefix(s, `"`) {
		s = strings.Trim(s, `"`)
	}
	return s
}

// describeExpr where 参数可能被 Sprintf 包着（如 characters[%d]）—— 取里头的字面量。
func describeExpr(e ast.Expr) string {
	if s := litOrEmpty(e); s != "" {
		return s
	}
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) > 0 {
		return litOrEmpty(call.Args[0])
	}
	return "?"
}

// resolveWhereVar 在**同一个函数体内**往回找 `where := …` / `where = …` 的最近一次赋值，
// 并取其中第一个字符串字面量。找不到返回空串（调用方会写「动态」）。
//
// 为什么要限定函数：where 这个名字在多个函数里都用，跨函数找会把别处的值串过来（文档里
// 写着错的位置比写着「动态」更糟）。
func resolveWhereVar(f *ast.File, fset *token.FileSet, call *ast.CallExpr) string {
	callOff := fset.Position(call.Pos()).Offset
	best := -1
	found := ""
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		s := fset.Position(fn.Body.Pos()).Offset
		e := fset.Position(fn.Body.End()).Offset
		if callOff < s || callOff > e {
			return true // 不是这个函数
		}
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			as, ok := m.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			id, ok := as.Lhs[0].(*ast.Ident)
			if !ok || id.Name != "where" {
				return true
			}
			off := fset.Position(as.Pos()).Offset
			if off >= callOff || off < best {
				return true
			}
			if s := firstStringIn(as.Rhs[0]); s != "" {
				best, found = off, s
			}
			return true
		})
		return false
	})
	return found
}

// firstStringIn 取该调用里第一个字符串字面量（兜底用）。
func firstStringIn(node ast.Node) string {
	found := ""
	ast.Inspect(node, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s := strings.Trim(lit.Value, "`"); s != "" {
				found = s
			}
		}
		return true
	})
	return found
}

// firstClause 取一句话（到第一个句号/分号/换行为止），太长就截断。
func firstClause(s string) string {
	s = strings.TrimSpace(s)
	for _, sep := range []string{"。", "；", "\n", "; "} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
		}
	}
	s = strings.ReplaceAll(s, "|", "／")
	r := []rune(s)
	if len(r) > 60 {
		s = string(r[:60]) + "…"
	}
	return s
}

// IDs 返回全部编号（测试/诊断用）。
func IDs(srcPath string) ([]string, error) {
	gates, err := Collect(srcPath)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, g := range gates {
		out = append(out, g.ID)
	}
	sort.Strings(out)
	return out, nil
}
