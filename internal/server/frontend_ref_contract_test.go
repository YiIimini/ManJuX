package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 前端 ref 解引用闸门。
//
// 起因(2026-09-24 全项目审计 P0-2):`HomeView.removeSeries` 里的守卫写成
//
//	if (batchStat && batchStat.prefix === prefix)   // batchStat 是 ref
//
// `batchStat.prefix` 恒为 undefined(Vue 的模板会自动解包 ref,**脚本里不会**),
// 于是"批量产线正在跑这个系列时不许删"这条守卫**永不触发** —— 用户能在整部系列
// 逐集渲染时把它一键删掉,连正在跑的集一起删。
//
// 这类 bug 的共同特征是:**语法合法、类型不报错、运行时不抛异常**,只是值恒为
// undefined。它不可能靠"更仔细地看代码"根除,只能靠闸门。所以这里扫 <script setup>
// 里的每一个 ref/computed 绑定,任何 `name.xxx` / `name?.xxx`(xxx ≠ value)都判失败。
//
// 判据只作用于脚本段:模板段里 Vue 会自动解包,写 `name.foo` 是对的。

var (
	reScriptBlock = regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	// 值包装声明:ref / shallowRef / computed / toRef / toRefs。reactive 不在列 ——
	// 它本身就是对象,`state.foo` 才是正确写法。
	reValueDecl = regexp.MustCompile(`(?m)^[\t ]*(?:const|let|var)[\t ]+([A-Za-z_$][\w$]*)[\t ]*=[\t ]*(?:ref|shallowRef|computed|toRef|toRefs)[\t ]*[<(]`)
	// 注释行(行注释与块注释起始)整行跳过:注释里常引用错误写法作为反面教材,
	// 那不是代码(与主题覆盖闸门同一处理)。
	reCommentLine = regexp.MustCompile(`^\s*(\*|/\*|//|<!--)`)
)

// refAllowlist 是**已确认无害**的例外,每条必须写明理由(与主题覆盖闸门同一纪律)。
// 键 = 相对路径,值 = 允许出现的"名字.属性"片段。
var refAllowlist = map[string][]string{}

func TestFrontendRefsAreDereferenced(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	var offenders []string
	files := 0

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
		if strings.ToLower(filepath.Ext(p)) != ".vue" {
			return nil
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		full := string(raw)

		loc := reScriptBlock.FindStringSubmatchIndex(full)
		if loc == nil {
			return nil // 无脚本段(纯展示组件)
		}
		files++
		script := full[loc[2]:loc[3]]
		base := loc[2] // 脚本内容在文件中的偏移(注释/行号换算用)

		// 逐行标记注释,避免把说明性文字当成代码。
		lines := strings.Split(full, "\n")
		commentLine := make([]bool, len(lines)+1)
		inBlock := false
		for i, ln := range lines {
			if inBlock {
				commentLine[i+1] = true
				if strings.Contains(ln, "*/") {
					inBlock = false
				}
				continue
			}
			if reCommentLine.MatchString(ln) {
				commentLine[i+1] = true
				if strings.Contains(ln, "/*") && !strings.Contains(ln, "*/") {
					inBlock = true
				}
			}
		}

		for _, d := range reValueDecl.FindAllStringSubmatch(script, -1) {
			name := d[1]
			// 前置字符守卫:前一个字符若是词字符或点,说明它是别的标识符的一部分
			// (如 `G.project?.project` 里被当成 ref 名 project),不是本次要判的访问。
			reUse := regexp.MustCompile(`(^|[^\w$.])` + regexp.QuoteMeta(name) + `\s*(?:\?\.|\.)\s*([A-Za-z_$][\w$]*)`)
			for _, u := range reUse.FindAllStringSubmatchIndex(script, -1) {
				prop := script[u[4]:u[5]]
				if prop == "value" {
					continue
				}
				// 同名局部变量遮蔽:命中点前 40 行内有**同名非值包装声明**
				// (`const plan = pj.value?.plan`),那是函数里的局部对象,`plan.shots`
				// 完全正确。闸门不做作用域分析,只能就近判断 —— 局部遮蔽几乎总是
				// 声明在用法附近,这一步换来的是"不误报"(误报的闸门会被关掉)。
				if declaredLocally(script, name, u[2]) {
					continue
				}
				line := 1 + strings.Count(full[:base+u[2]], "\n")
				if line <= len(commentLine) && commentLine[line] {
					continue // 注释里的反面教材不算违规
				}
				hit := name + "." + prop
				if allowedRef(rel, hit) {
					continue
				}
				offenders = append(offenders, rel+":"+strconv.Itoa(line)+"  "+hit+
					"   →  "+strings.TrimSpace(lineText(full, line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 frontend/src 失败: %v", err)
	}
	if files == 0 {
		t.Fatal("一个 .vue 文件都没扫到 —— 路径或解析规则失效了(闸门失效比 bug 更危险)")
	}
	if len(offenders) > 0 {
		t.Errorf("脚本段里直接读了 ref 的属性(值恒为 undefined,守卫/判断会静默失效);"+
			"请改用 .value:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func allowedRef(file, hit string) bool {
	for _, a := range refAllowlist[file] {
		if a == hit {
			return true
		}
	}
	return false
}

// declaredLocally 判断命中点之前 40 行内是否存在同名的**非值包装**声明。
func declaredLocally(script, name string, off int) bool {
	start := off
	for n := 0; n < 40 && start > 0; n++ {
		prev := strings.LastIndexByte(script[:start-1], '\n')
		if prev < 0 {
			start = 0
			break
		}
		start = prev + 1
	}
	win := script[start:off]
	re := regexp.MustCompile(`(?m)^[\t ]*(?:const|let|var)[\t ]+` + regexp.QuoteMeta(name) + `[\t ]*=[\t ]*`)
	for _, loc := range re.FindAllStringIndex(win, -1) {
		rhs := strings.TrimLeft(win[loc[1]:], " \t")
		switch {
		case strings.HasPrefix(rhs, "ref("), strings.HasPrefix(rhs, "ref<"),
			strings.HasPrefix(rhs, "shallowRef("), strings.HasPrefix(rhs, "computed("),
			strings.HasPrefix(rhs, "toRef("), strings.HasPrefix(rhs, "toRefs("):
			continue // 它自己就是值包装声明,不是遮蔽
		}
		return true
	}
	return false
}

func lineText(s string, n int) string {
	lines := strings.Split(s, "\n")
	if n >= 1 && n <= len(lines) {
		return lines[n-1]
	}
	return ""
}
