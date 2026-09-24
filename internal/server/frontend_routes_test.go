package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 前端 URL ↔ 后端路由表闸门(审计 P1-3)。
//
// 起因:store.js 里的 URL 与 server.go 的路由表**没有任何断言**,而漏接线已经复发两次
// (store.js:71-77、111-114)—— 症状是"界面上点了没反应/一直转圈",而两端各自看都
// 没错。这里把两张清单对上:前端每个请求都必须能在后端路由表里找到对应的
// (方法 + 路径模式),反之不要求(有些路由只给桌面模式或外部工具用)。
func TestFrontendURLsMatchBackendRoutes(t *testing.T) {
	routes := apiRoutePatterns(t)

	raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "store.js"))
	if err != nil {
		t.Fatalf("读 store.js 失败:%v", err)
	}
	// 匹配 req('GET', '/api/...' 或 req('POST', `/api/...${x}`)
	// 反引号分支必须允许内部再出现单引号(模板串里常有 `?online=1` 这类写法)。
	re := regexp.MustCompile("req\\(\\s*'(GET|POST|PUT|DELETE)'\\s*,\\s*(?:`([^`]*)`|'([^']*)')")
	matches := re.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatal("没解析出任何前端请求 —— 闸门本身失效(store.js 的写法变了?)")
	}

	var missing []string
	for _, m := range matches {
		method, url := m[1], m[2]
		if url == "" {
			url = m[3]
		}
		if !strings.HasPrefix(url, "/api/") {
			continue // 绝对地址/静态资源不归路由表管
		}
		// 模板占位要分两种:
		//   路径段里的 ${id}  → 归一成 {} 参与匹配;
		//   紧跟字面量之后的 ${…}(典型是 `${online ? '?online=1' : ''}` 这种查询串后缀)
		//   → 直接丢掉,否则会把一条正常路由判成 404。
		norm := regexp.MustCompile(`\$\{[^}]*\}`).ReplaceAllString(url, "\x00")
		if i := strings.IndexByte(norm, 0); i > 0 && norm[i-1] != '/' {
			norm = norm[:i]
		}
		norm = strings.ReplaceAll(norm, "\x00", "{}")
		norm = strings.SplitN(norm, "?", 2)[0] // 查询串不参与路由匹配
		norm = normalizeRoutePattern(norm)
		// /files/{path...} 这类通配:去掉尾部通配段再比
		key := method + " " + norm
		if routes[key] {
			continue
		}
		if matchWildcardRoute(routes, method, norm) {
			continue
		}
		missing = append(missing, key)
	}
	if len(missing) > 0 {
		t.Errorf("前端在调这些 URL,但后端没有注册对应路由(点了会 404):\n  %s\n"+
			"store.js 与 server.go 必须同步改 —— 这正是「漏接线」复发两次的地方。",
			strings.Join(missing, "\n  "))
	}
}

// matchWildcardRoute 处理 `{...}` 的通配段(如 /files/{path...})。
func matchWildcardRoute(routes map[string]bool, method, norm string) bool {
	for k := range routes {
		if !strings.HasPrefix(k, method+" ") {
			continue
		}
		pattern := strings.TrimPrefix(k, method+" ")
		if routeMatches(pattern, norm) {
			return true
		}
	}
	return false
}

// routeMatches 段对段比较:模式里的 {…} 匹配任意一段(含最后一段的通配形式)。
func routeMatches(pattern, url string) bool {
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	us := strings.Split(strings.Trim(url, "/"), "/")
	if len(ps) != len(us) {
		return false
	}
	for i := range ps {
		if strings.HasPrefix(ps[i], "{") {
			continue
		}
		if ps[i] != us[i] {
			return false
		}
	}
	return true
}
