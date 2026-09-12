package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

// HTTP 层契约测试。
//
// 此前 HTTP 层**零测试**:60+ 条路由只靠前端手写 URL 对齐,改一处路由或
// 响应字段名,只有到界面上点出来才会发现。这里为"不依赖外部资源"的核心
// 路由建最小契约网 —— 也是拆分 server.go(1360 行)之前的安全网。

// newTestServer 按 main.go 的装配顺序构造真实依赖(不做 mock)。
//
// 期间被测试逼出三处遗漏,值得记下来 —— 它们说明"HTTP 契约"实际牵动多少依赖:
//   - status 读 s.ff.Ffmpeg        → 需要 ff 实例
//   - createProject 走 s.opLog     → 需要事件总线 bus
//   - listProjects 逐项目查 s.pipe → 需要完整的 Coordinator
//
// 因此这里照搬 main.go 的装配链,而不是传一堆 nil —— 真装配下的契约才可信。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.NewManager(filepath.Join(dir, "settings.json"), dir)
	if err != nil {
		t.Fatalf("构造 config.Manager 失败: %v", err)
	}
	store, err := project.NewStore(filepath.Join(dir, "projects"))
	if err != nil {
		t.Fatalf("构造 project.Store 失败: %v", err)
	}
	lib, err := library.New(filepath.Join(dir, "assets_library"))
	if err != nil {
		t.Fatalf("构造 library 失败: %v", err)
	}
	reg := engine.NewRegistry()
	f := new(ff.FF) // 零值即可:被测路由只读 Ffmpeg 字段,不触发 ffprobe
	bus := pipeline.NewBus()
	coord := pipeline.New(store, cfg, reg, f, bus, lib)
	return New(cfg, store, reg, coord, f, bus, lib)
}

// doReq 发一个请求并解析响应体为 map(空体返回 nil)。
func doReq(t *testing.T, s *Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestHTTPContractCoreRoutes 核心路由必须注册正确、返回合法 JSON。
func TestHTTPContractCoreRoutes(t *testing.T) {
	s := newTestServer(t)

	cases := []struct {
		name   string
		method string
		path   string
		want   int
		check  func(t *testing.T, out map[string]any)
	}{
		{
			name: "健康检查", method: "GET", path: "/api/health", want: 200,
		},
		{
			name: "状态含版本", method: "GET", path: "/api/status", want: 200,
			check: func(t *testing.T, out map[string]any) {
				if v, _ := out["version"].(string); v == "" {
					t.Error("status 必须返回非空 version(前端底栏与报障都依赖它)")
				}
			},
		},
		{
			name: "设置三类密钥字段齐备", method: "GET", path: "/api/settings", want: 200,
			check: func(t *testing.T, out map[string]any) {
				for _, k := range []string{"llm", "minimax", "vlm"} {
					sub, ok := out[k].(map[string]any)
					if !ok {
						t.Errorf("settings 缺少 %s 段", k)
						continue
					}
					if _, ok := sub["api_key"]; !ok {
						t.Errorf("settings.%s 缺少 api_key", k)
					}
				}
			},
		},
		{
			name: "元信息含阶段与风格", method: "GET", path: "/api/meta", want: 200,
			check: func(t *testing.T, out map[string]any) {
				phases, _ := out["phases"].([]any)
				styles, _ := out["styles"].([]any)
				if len(phases) == 0 {
					t.Error("meta.phases 不应为空(前端阶段 UI 的单一真源)")
				}
				if len(styles) == 0 {
					t.Error("meta.styles 不应为空(前端风格 chips 的单一真源)")
				}
			},
		},
		{
			name: "项目列表", method: "GET", path: "/api/projects", want: 200,
		},
		{
			// 错误路径:不存在的项目必须 404 而不是 200 或 500
			name: "不存在的项目返回 404", method: "GET", path: "/api/projects/__no_such_project__", want: 404,
		},
		{
			// 现状记录(非 bug):未匹配的路径落到 SPA 兜底,返回 index.html 与 200。
			// 副作用是前端把 /api 路径拼错时**不会得到 404**,而是拿到 HTML,
			// JSON 解析失败后报错比较含糊。如实锁定该行为 —— 若将来改成
			// 「/api/* 未匹配即 404」,这条用例就是它的回归点。
			name: "未知路径落到 SPA 兜底", method: "GET", path: "/api/definitely-not-a-route", want: 200,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := doReq(t, s, tc.method, tc.path, nil)
			if code != tc.want {
				t.Fatalf("%s %s 期望 HTTP %d,实际 %d", tc.method, tc.path, tc.want, code)
			}
			if tc.check != nil {
				tc.check(t, out)
			}
		})
	}
}

// TestHTTPContractProjectLifecycle 项目生命周期契约:
// 创建 → 列表可见 → 详情可读 → 参数保存 → 删除后 404。
//
// 参数保存这一步同时是分镜数量语义的回归点:保存后 max_shots=0(内容驱动)
// 必须原样保留,不能被 Sanitize 钳成 1。
func TestHTTPContractProjectLifecycle(t *testing.T) {
	s := newTestServer(t)

	code, out := doReq(t, s, "POST", "/api/projects", map[string]any{"name": "契约测试项目"})
	if code != 200 {
		t.Fatalf("创建项目应 200,实际 %d (%v)", code, out)
	}
	proj, _ := out["project"].(map[string]any)
	if proj == nil {
		// 有的实现直接返回项目本体,两种都接受,只要 id 拿得到
		proj = out
	}
	id, _ := proj["id"].(string)
	if id == "" {
		t.Fatalf("创建响应缺少项目 id: %v", out)
	}

	// 列表可见
	code, _ = doReq(t, s, "GET", "/api/projects", nil)
	if code != 200 {
		t.Fatalf("项目列表应 200,实际 %d", code)
	}

	// 详情可读
	code, detail := doReq(t, s, "GET", "/api/projects/"+id, nil)
	if code != 200 {
		t.Fatalf("项目详情应 200,实际 %d", code)
	}
	if detail["project"] == nil {
		t.Error("详情响应应含 project 字段")
	}

	// 参数保存:显式传 0(内容驱动)必须保持 0
	code, _ = doReq(t, s, "PUT", "/api/projects/"+id+"/params", map[string]any{
		"script": map[string]any{"max_shots": 0, "total_seconds": 0},
	})
	if code != 200 {
		t.Fatalf("保存参数应 200,实际 %d", code)
	}
	_, detail = doReq(t, s, "GET", "/api/projects/"+id, nil)
	if pj, ok := detail["project"].(map[string]any); ok {
		if params, ok := pj["params"].(map[string]any); ok {
			if script, ok := params["script"].(map[string]any); ok {
				if v, ok := script["max_shots"].(float64); ok && v != 0 {
					t.Errorf("max_shots=0(内容驱动)保存后应保持 0,实际 %v —— 该值被钳制会让分镜退化成 1 镜", v)
				}
			}
		}
	}

	// 删除后应 404
	if code, _ = doReq(t, s, "DELETE", "/api/projects/"+id, nil); code != 200 {
		t.Fatalf("删除项目应 200,实际 %d", code)
	}
	if code, _ = doReq(t, s, "GET", "/api/projects/"+id, nil); code != 404 {
		t.Errorf("已删除的项目应返回 404,实际 %d", code)
	}
}

// TestHTTPContractSettingsMaskRoundTrip 设置保存的掩码往返契约:
// 前端拿到的是掩码,原样回存不得覆盖真实 key(否则"点一次保存就丢 key")。
func TestHTTPContractSettingsMaskRoundTrip(t *testing.T) {
	s := newTestServer(t)

	// 写入真实 key
	code, settings := doReq(t, s, "GET", "/api/settings", nil)
	if code != 200 {
		t.Fatalf("读取设置应 200,实际 %d", code)
	}
	if llm, ok := settings["llm"].(map[string]any); ok {
		llm["api_key"] = "sk-contract-test-1234567890"
	}
	if code, _ = doReq(t, s, "PUT", "/api/settings", settings); code != 200 {
		t.Fatalf("保存设置应 200,实际 %d", code)
	}

	// 再次读取:必须拿到掩码而非明文
	_, masked := doReq(t, s, "GET", "/api/settings", nil)
	llmOut, _ := masked["llm"].(map[string]any)
	key, _ := llmOut["api_key"].(string)
	if key == "sk-contract-test-1234567890" {
		t.Fatal("GET /api/settings 不得回传明文 key")
	}
	if key == "" || key[:2] != "**" {
		t.Fatalf("应返回掩码,实际 %q", key)
	}

	// 把掩码原样存回 → 真 key 必须保住
	if code, _ = doReq(t, s, "PUT", "/api/settings", masked); code != 200 {
		t.Fatalf("回存掩码应 200,实际 %d", code)
	}
	_, again := doReq(t, s, "GET", "/api/settings", nil)
	llm2, _ := again["llm"].(map[string]any)
	if key2, _ := llm2["api_key"].(string); key2 != key {
		t.Errorf("回存掩码后掩码应不变(说明真 key 未被覆盖),期望 %q 实际 %q", key, key2)
	}
}

// TestLibraryOpsRoutes 资产库运营路由契约:补全多视角 / AI 生成描述提示词 /
// 手动编辑描述与提示词。
//
// 这三条是详情弹窗的"完善资产"入口 —— 路由没注册或语义写反(比如场景也允许
// 生成转身参考),界面上只会看到一个含糊的失败提示,所以这里把状态码钉住。
func TestLibraryOpsRoutes(t *testing.T) {
	s := newTestServer(t)
	src := filepath.Join(t.TempDir(), "p.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.lib.Put("scenes", "契约场景", "旧描述", "old prompt", src, "proj", "upload"); err != nil {
		t.Fatalf("准备库条目失败: %v", err)
	}
	if _, err := s.lib.Put("characters", "契约角色", "旧描述", "old prompt", src, "proj", "upload"); err != nil {
		t.Fatalf("准备库条目失败: %v", err)
	}

	// ① 编辑描述与提示词:成功 + 字段真的写进去了
	if code, out := doReq(t, s, "PUT", "/api/library/scenes/契约场景", map[string]any{
		"description": "新描述", "image_prompt": "new prompt",
	}); code != 200 {
		t.Fatalf("编辑描述应 200,实际 %d %v", code, out)
	} else if out["description"] != "新描述" || out["image_prompt"] != "new prompt" {
		t.Fatalf("编辑后字段未生效: %v", out)
	}
	// ② 不存在的条目必须 404(而不是静默 200)
	if code, _ := doReq(t, s, "PUT", "/api/library/characters/无此角色", map[string]any{"description": "x"}); code != 404 {
		t.Fatalf("编辑不存在的条目应 404,实际 %d", code)
	}
	if code, _ := doReq(t, s, "POST", "/api/library/characters/无此角色/views", nil); code != 404 {
		t.Fatalf("给不存在的角色补多视角应 404,实际 %d", code)
	}
	if code, _ := doReq(t, s, "POST", "/api/library/characters/无此角色/describe", nil); code != 404 {
		t.Fatalf("给不存在的角色生成描述应 404,实际 %d", code)
	}
	// ③ 场景没有"转身"语义:补多视角必须被拒(别浪费一次数分钟的渲染)
	if code, _ := doReq(t, s, "POST", "/api/library/scenes/契约场景/views", nil); code != 400 {
		t.Fatalf("场景补多视角应 400,实际 %d", code)
	}
	// ④ 角色补多视角:测试环境无本地 ComfyUI 且根目录不存在 → 503 并给出人话提示
	code, out := doReq(t, s, "POST", "/api/library/characters/契约角色/views", nil)
	if code != 503 {
		t.Fatalf("无本地引擎时补多视角应 503,实际 %d %v", code, out)
	}
	// ⑤ AI 生成描述:测试环境 LLM/VLM 都没配 key → 400(而不是 500 或空 200)
	if code, _ := doReq(t, s, "POST", "/api/library/scenes/契约场景/describe", nil); code != 400 {
		t.Fatalf("未配置模型时生成描述应 400,实际 %d", code)
	}
}
