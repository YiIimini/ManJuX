package server

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

// 来源守卫的契约测试(审计 P0-10)。
//
// 背景:这个服务监听本机端口,而所有路由原先**没有任何来源校验**。任何网页只要诱导
// 浏览器发请求就能打到副作用端点(/api/quit、/api/comfy/stop、/api/cleanup、
// /api/open-url),而"text/plain + JSON body"这种**不触发 CORS 预检**的简单请求
// 一定能送到服务端 —— 不能指望浏览器替我们拦。
//
// 这组用例同时是"守卫还在不在"的探针:守卫一旦被摘掉,下面的 403/415 断言会立刻变红。

// guardReq 直接打装配好的 handler(与生产同一条路径)。
func guardReq(t *testing.T, s *Server, method, path, host, origin, referer, contentType, body string) int {
	t.Helper()
	var rdr *bytes.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w.Code
}

func TestAPIGuardAllowsOwnLoopbackRequests(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		name              string
		host, origin, ct  string
		method, path, bod string
	}{
		{"独立 HTTP 服务:同源 GET", "127.0.0.1:8790", "http://127.0.0.1:8790", "", "GET", "/api/health", ""},
		{"独立 HTTP 服务:同源写", "127.0.0.1:8790", "http://127.0.0.1:8790", "application/json", "PUT", "/api/settings", "{}"},
		{"Wails 桌面模式:页面来源是 wails.localhost", "127.0.0.1:8790", "http://wails.localhost", "application/json", "PUT", "/api/settings", "{}"},
		{"IPv6 回环", "[::1]:8790", "", "", "GET", "/api/health", ""},
		{"原生调用不带 Origin/Content-Type(非浏览器发起)", "localhost:8790", "", "", "POST", "/api/quit", ""},
		{"multipart 上传(浏览器模式的封面/资产)", "127.0.0.1:8790", "http://127.0.0.1:8790", "multipart/form-data; boundary=x", "POST", "/api/projects/p/cover", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code := guardReq(t, s, c.method, c.path, c.host, c.origin, "", c.ct, c.bod)
			if code == 403 || code == 415 {
				t.Fatalf("自家请求不该被守卫拦下(HTTP %d)—— 把应用自己挡在门外比不设防更糟", code)
			}
		})
	}
}

func TestAPIGuardBlocksCrossSiteOrigin(t *testing.T) {
	s := newTestServer(t)
	for _, origin := range []string{
		"https://evil.example.com",
		"http://192.168.1.9:8790", // 局域网来源同样不是本机
		"null",                    // 某些沙箱 iframe 的 Origin
	} {
		code := guardReq(t, s, "POST", "/api/quit", "127.0.0.1:8790", origin, "", "", "")
		if code != 403 {
			t.Errorf("Origin=%s 应被拒绝,实际 HTTP %d", origin, code)
		}
	}
}

func TestAPIGuardBlocksCrossSiteReferer(t *testing.T) {
	s := newTestServer(t)
	// 没有 Origin 但有跨站 Referer(部分老浏览器/表单提交):同样要拦
	code := guardReq(t, s, "POST", "/api/cleanup", "127.0.0.1:8790", "", "https://evil.example.com/x.html", "", "")
	if code != 403 {
		t.Errorf("跨站 Referer 应被拒绝,实际 HTTP %d", code)
	}
}

func TestAPIGuardBlocksDNSRebindingHost(t *testing.T) {
	s := newTestServer(t)
	// DNS rebinding:攻击者把自己的域名解析到 127.0.0.1,于是浏览器认为同源
	for _, host := range []string{"evil.example.com", "manjux.attacker.test", "10.0.0.5:8790"} {
		code := guardReq(t, s, "GET", "/api/health", host, "", "", "", "")
		if code != 403 {
			t.Errorf("Host=%s 应被拒绝(挡 DNS rebinding),实际 HTTP %d", host, code)
		}
	}
}

// TestAPIGuardBlocksSimpleRequestWrites 核心用例:不触发 CORS 预检的"简单请求"
// 不能写。浏览器只允许简单请求带 text/plain、application/x-www-form-urlencoded、
// multipart/form-data 三种 Content-Type,所以"必须声明 JSON"这一步就把跨站写挡住了。
func TestAPIGuardBlocksSimpleRequestWrites(t *testing.T) {
	s := newTestServer(t)
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded"} {
		code := guardReq(t, s, "POST", "/api/quit", "127.0.0.1:8790",
			"http://127.0.0.1:8790", "", ct, `{"x":1}`)
		if code != 415 {
			t.Errorf("Content-Type=%s 的写请求应被拒绝(415),实际 HTTP %d", ct, code)
		}
	}
	// 读操作不受 Content-Type 约束(SSE/静态资源也不带)
	if code := guardReq(t, s, "GET", "/api/health", "127.0.0.1:8790", "http://127.0.0.1:8790", "", "text/plain", ""); code == 415 {
		t.Error("GET 不该受 Content-Type 校验影响")
	}
}

// TestAPIGuardCoversBothAssemblies 守卫必须在**两条装配路径**上都在:
// 独立 HTTP 服务(routes → s.mux)与 Wails 桌面模式(APIHandler)。
// 只在前者加守卫是典型的分叉 —— 桌面模式恰恰是主要使用方式。
func TestAPIGuardCoversBothAssemblies(t *testing.T) {
	s := newTestServer(t)
	h := s.APIHandler()
	req := httptest.NewRequest("POST", "/api/quit", strings.NewReader(""))
	req.Host = "evil.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("APIHandler(桌面模式)也必须套守卫,实际 HTTP %d", w.Code)
	}
}
