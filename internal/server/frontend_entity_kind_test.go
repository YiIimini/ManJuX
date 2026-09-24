package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 前端"不得再持有一份实体类型推断表"闸门(审计 P1-2 / 根因 R1)。
//
// 起因:角色卡上显示的类型由前端**自己**按关键词推断(与后端 ResolveEntityKind
// 是两份表),而两份表已经漂移过 —— 界面显示"器物"、渲染按"人"走(或相反),
// 用户看到的就是"我把类型改了却没生效"。类型决定整套渲染方法(定妆照取景、
// 多视角方式、镜头身份锁),只能有一个真源:后端在项目投影里下发 char_kinds。
//
// 判据:frontend/src 里不得出现 ① `inferKind`(那份推断函数)② 后端关键词语料里
// **只在推断表里才会出现**的特征词(说明又抄了一份表)。注释里提到历史不算 ——
// 按注释行跳过。
//
// 语料只收"除了推断表没有别的理由出现"的词:`傀儡` 这类会出现在类型说明文案里
// (「器物/机关/傀儡/雕像等非生命体」),不算证据 —— 闸门误报多了就会被关掉。
var entityKindCorpus = []string{
	"铜雀", "提线", "marionette", "automaton", "clockwork", "golem", "super deformed",
}

func TestFrontendHasNoEntityKindTable(t *testing.T) {
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
		inBlock := false
		for i, ln := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(ln)
			if inBlock {
				if strings.Contains(ln, "*/") {
					inBlock = false
				}
				continue
			}
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
				strings.HasPrefix(trimmed, "<!--") || strings.HasPrefix(trimmed, "/*") {
				if strings.Contains(ln, "/*") && !strings.Contains(ln, "*/") {
					inBlock = true
				}
				continue
			}
			if strings.Contains(ln, "inferKind") {
				offenders = append(offenders, rel+":"+itoa(i+1)+"  inferKind(...) —— 前端不该自己推断实体类型")
			}
			for _, kw := range entityKindCorpus {
				if strings.Contains(ln, kw) {
					offenders = append(offenders, rel+":"+itoa(i+1)+"  出现关键词语料「"+kw+"」—— 像是又抄了一份推断表")
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 frontend/src 失败:%v", err)
	}
	if scanned == 0 {
		t.Fatal("一个前端文件都没扫到 —— 路径失效,闸门形同不存在")
	}
	if len(offenders) > 0 {
		t.Errorf("实体类型只允许后端一个真源(char_kinds 投影):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestProjectProjectionCarriesCharKinds 后端必须下发每个角色的生效类型 ——
// 前端删掉推断表之后,这是它唯一的类型来源(契约测试:字段没了界面就会退回"人")。
func TestProjectProjectionCarriesCharKinds(t *testing.T) {
	s := newTestServer(t)
	code, out := doReq(t, s, "POST", "/api/projects", map[string]any{"name": "投影契约"})
	if code != 200 {
		t.Fatalf("创建项目失败:%d %v", code, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		// 有的实现把项目放在 data 里
		if d, ok := out["data"].(map[string]any); ok {
			id, _ = d["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("拿不到项目 id:%v", out)
	}
	code, proj := doReq(t, s, "GET", "/api/projects/"+id, nil)
	if code != 200 {
		t.Fatalf("读取项目失败:%d", code)
	}
	if _, ok := proj["char_kinds"]; !ok {
		// 尚未生成方案时 char_kinds 允许为空 map,但**必须有这个键**:
		// 缺键会让前端一路走兜底,而兜底恰恰是我们要删掉的那份表。
		t.Fatalf("项目投影缺少 char_kinds 键:%v", keysOf(proj))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// itoa 小工具(避免为一个用例引入 strconv 之外的依赖面)。
func itoa(n int) string {
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

// apiRoutePatterns 从 server.go 的注册表里抽出全部路由(METHOD + 模式)。
func apiRoutePatterns(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("server.go"))
	if err != nil {
		t.Fatalf("读 server.go 失败:%v", err)
	}
	re := regexp.MustCompile(`m\.HandleFunc\("(GET|POST|PUT|DELETE|PATCH) (/[^"]*)"`)
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		out[m[1]+" "+normalizeRoutePattern(m[2])] = true
	}
	if len(out) == 0 {
		t.Fatal("没解析出任何路由 —— 闸门本身失效")
	}
	return out
}

// normalizeRoutePattern 把路径参数统一成 {} 占位,便于与前端 URL 比对。
func normalizeRoutePattern(p string) string {
	re := regexp.MustCompile(`\{[^}]*\}`)
	return re.ReplaceAllString(p, "{}")
}
