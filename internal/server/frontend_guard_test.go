package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 前端"取来源字段必须防空"闸门(2026-09-25 用户报「界面错误:Cannot read properties
// of undefined (reading 'source')」)。
//
// 起因是我自己的回归:把标签里的**字面量**换成 `sourceLabel(c.asset.source)` 时,
// 忘了 `c.asset` 可能压根不存在(资产缺失的镜头),而那个 `v-else` 分支恰恰包含
// "没有资产"的情形 —— 字面量不会报错、取属性会,于是整个界面炸成一行红字。
//
// 判据刻意收得很窄:只看 `sourceLabel(…)` / `sourceTone(…)` 的实参 ——
//
//	① 里面出现 `.source`(在取来源字段)
//	② 且没有用 `?.`(没防空)
//
// 就报错。窄判据的价值在于**不会因为 Vue 的块级 v-if 作用域误伤**(gate 读不懂模板
// 作用域,宽判据会把 `v-if="c.asset"` 里的合法访问全报出来,最后被关掉 —— 那等于没有)。
var (
	reSourceCall = regexp.MustCompile(`source(?:Label|Tone)\(([^)]*)\)`)
)

func TestFrontendGuardsSourceAccess(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	scanned := 0
	var offenders []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == "wailsjs" {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".vue", ".js":
		default:
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		scanned++
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		for i, ln := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "<!--") {
				continue
			}
			if !sourceAccessUnsafe(ln) {
				continue
			}
			offenders = append(offenders, rel+":"+itoa6(i+1)+"  "+trimmed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描前端失败:%v", err)
	}
	if scanned == 0 {
		t.Fatal("没扫到前端文件 —— 闸门失效")
	}
	// 闸门自检:判据必须真的能识别出坏写法(否则它只是装饰)
	if !sourceAccessUnsafe("sourceLabel(c.asset.source)") {
		t.Fatal("判据失效:漏报 sourceLabel(c.asset.source)(资产缺失时整页报错的那种写法)")
	}
	if sourceAccessUnsafe("sourceLabel(c.asset?.source)") {
		t.Fatal("判据误报:已用 ?. 防空却被判不安全")
	}
	if sourceAccessUnsafe("sourceLabel(e.source)") {
		t.Fatal("判据误报:循环项/局部变量的一层访问不该被判不安全(会让闸门被关掉)")
	}
	if len(offenders) > 0 {
		t.Errorf("这些地方从可能不存在的资产对象取来源字段(资产缺失时整页报 Cannot read properties of undefined):\n  %s\n  请写成 ?.source",
			strings.Join(offenders, "\n  "))
	}
}

// sourceAccessUnsafe 判据本体(抽出来供闸门自检)。
//
// 只把**嵌套访问**判为不安全:`X.y.z`(两层以上属性)一旦中间那层是 undefined 就炸;
// 而 `e.source`(循环变量/局部变量,一层)按约定是存在的 —— 把这种也算进来,
// 闸门就会对满屏正常代码报警,最后被人关掉。
func sourceAccessUnsafe(line string) bool {
	for _, m := range reSourceCall.FindAllStringSubmatch(line, -1) {
		arg := strings.TrimSpace(m[1])
		if !strings.Contains(arg, ".source") || strings.Contains(arg, "?.") {
			continue
		}
		if strings.Count(arg, ".") < 2 {
			continue // 一层访问:对象本身是局部变量/循环项,按约定存在
		}
		if strings.Contains(line, "?") && strings.Contains(line, ":") {
			continue
		}
		return true
	}
	return false
}

func itoa6(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
