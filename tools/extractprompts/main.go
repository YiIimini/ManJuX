package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// proseLike 是不是"散文型"（与 director_spec_test.go 的守卫同一判据）。
func proseLike(body string) bool { return len(body) >= 120 || strings.Contains(body, "\n") }

func main() {
	src, outDir := os.Args[1], os.Args[2]
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		panic(err)
	}
	text := string(raw)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}
	type named struct {
		pos  int
		name string
	}
	var names []named
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.GenDecl:
			for _, spec := range v.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
					names = append(names, named{fset.Position(vs.Pos()).Offset, vs.Names[0].Name})
				}
			}
		case *ast.FuncDecl:
			names = append(names, named{fset.Position(v.Pos()).Offset, v.Name.Name})
		}
	}
	nameAt := func(off int) string {
		best := "prompt"
		for _, n := range names {
			if n.pos <= off {
				best = n.name
			}
		}
		return best
	}
	type edit struct {
		start, end int
		repl       string
	}
	var edits []edit
	seq := map[string]int{}
	n := 0
	ast.Inspect(f, func(nd ast.Node) bool {
		lit, ok := nd.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || !strings.HasPrefix(lit.Value, "\x60") {
			return true
		}
		body := strings.TrimSuffix(strings.TrimPrefix(lit.Value, "\x60"), "\x60")
		if !proseLike(body) {
			return true
		}
		start := fset.Position(lit.Pos()).Offset
		base := nameAt(start)
		seq[base]++
		seg := base
		if seq[base] > 1 {
			seg = fmt.Sprintf("%s_%d", base, seq[base])
		}
		if err := os.WriteFile(filepath.Join(outDir, seg+".md"), []byte(body), 0o644); err != nil {
			panic(err)
		}
		edits = append(edits, edit{start, fset.Position(lit.End()).Offset, "promptText(" + strconv.Quote(seg) + ")"})
		n++
		return false
	})
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		text = text[:e.start] + e.repl + text[e.end:]
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 {
				continue
			}
			name := vs.Names[0].Name
			text = strings.ReplaceAll(text, "const "+name+" = promptText(", "var "+name+" = promptText(")
		}
	}
	if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("搬出 %d 段散文型提示词到 %s\n", n, outDir)
}
