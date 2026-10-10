package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	s := New(cfg, store, reg, coord, f, bus, lib)
	// 单元测试里绝不拉起真实产线:真实 startBatch 会起一条跑 ComfyUI/LLM、最后调 quitApp
	// (os.Exit)的 goroutine,而 batch 是**包级单例** —— 它会把测试进程掀掉,也会抢走别的
	// 用例的产线(2026-09-26 实测)。换成记录器后,被测的是"哪条入口请求了开跑、开的是哪本书"
	// (这正是当初漏掉的行为),真实启动留给真机端到端。
	s.startProdFn = func(string, []string) bool { return true }
	// 桌面壁纸（2026-10-10）：设系统壁纸是**有真副作用**的动作（改跑测试那台机器的桌面），
	// 所以默认换成「什么都不做」的替身；要断言行为的用例自己覆盖这两个字段。
	// 读回也默认回空串（＝「不知道」）—— 真机当前壁纸在哪是环境状态，不能拿来当判据。
	s.setDesktopWallpaper = func(string) error { return nil }
	s.getDesktopWallpaper = func() string { return "" }
	return s
}

// doReq 发一个请求并解析响应体为 map(空体返回 nil)。
//
// Host 必须显式设成回环地址:httptest.NewRequest 默认给的是 example.com,
// 而生产装配上套了来源守卫(guardAPI,审计 P0-10)—— 用默认 Host 会被 403,
// 于是"守卫不在"这件事反而看不出来。显式设回环 Host 本身就是守卫生效的反向证据:
// 谁把守卫摘掉,就会有专门的用例(TestAPIGuard*)变红。
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
	req.Host = "127.0.0.1:8790"
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

// TestHTTPContractShotUpscale 2× 放大入口的契约。
//
// 引擎层(comfyengine/upscalegraph.go)与它的图契约测试早已就绪,缺的一直是
// 上层入口 —— README 里明写「界面入口尚未接入」。本测试锁住三件事:
//
//	① 路由确实注册(而不是落到 404);
//	② 参数体检排在调引擎之前,且错误信息可执行(而不是让用户看到一句
//	   看不懂的节点缺失);
//	③ 项目/镜头不存在时给出准确的状态码;
//	④ 可省的参数真的可省 —— 不传 type/model/ltx_vae 时由引擎侧默认值补齐,
//	   前端因此不必让用户手打文件名(手打的打错了也校验不了)。
//
// 用例都不触碰外部资源:参数校验与项目校验都排在 ff.Probe 之前,
// 因此 newTestServer 的零值 ff 不会被触发。
func TestHTTPContractShotUpscale(t *testing.T) {
	s := newTestServer(t)

	cases := []struct {
		name     string
		path     string
		body     any
		want     int
		wantWord string
	}{
		{
			name: "放大方式非法要给出可选值",
			path: "/api/projects/p1/shot/1/upscale",
			body: map[string]any{"type": "bicubic"},
			want: 400, wantWord: "ltx",
		},
		{
			// 显式传了空白 = 用户明确要给一个空文件名,必须拦下;
			// 而**不传**是另一回事:引擎侧有默认值,应当放行(见下一条用例)。
			name: "ltx 传了空白 VAE 要拦下",
			path: "/api/projects/p1/shot/1/upscale",
			body: map[string]any{"type": "ltx", "model": "ltx-2-spatial-upscaler-x2-1.0.safetensors", "ltx_vae": "   "},
			want: 400, wantWord: "VAE",
		},
		{
			// 不传 ltx_vae 不再是错误:默认值由 engine.UpscaleOptionsFor 给出。
			// 这里用"项目不存在"的 404 作为放行证据 —— 参数体检若拦下它就会是 400。
			name: "ltx 不传 VAE 时用默认值放行",
			path: "/api/projects/p1/shot/1/upscale",
			body: map[string]any{"type": "ltx"},
			want: 404, wantWord: "项目",
		},
		{
			name: "模型名为空白要拦下",
			path: "/api/projects/p1/shot/1/upscale",
			body: map[string]any{"type": "esrgan", "model": "   "},
			want: 400, wantWord: "模型",
		},
		{
			name: "项目不存在",
			path: "/api/projects/不存在的项目/shot/1/upscale",
			body: map[string]any{"type": "esrgan", "model": "4x-UltraSharp.safetensors"},
			want: 404, wantWord: "项目",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, out := doReq(t, s, "POST", c.path, c.body)
			if got != c.want {
				t.Fatalf("状态码 want %d got %d(body=%v)", c.want, got, out)
			}
			msg, _ := out["error"].(string)
			if msg == "" {
				t.Fatalf("错误响应必须带 error 字段(前端直接展示它),实际 %v", out)
			}
			if c.wantWord != "" && !strings.Contains(msg, c.wantWord) {
				t.Errorf("错误信息应提到「%s」以便用户自查,实际:%s", c.wantWord, msg)
			}
		})
	}
}

// TestHTTPContractShotUpscaleRouteRegistered 路由必须在,而不是掉进 404。
//
// 与上一条的区别:上一条的 404 用例是"项目不存在"的语义 404,
// 这一条防的是"路由根本没注册"——那种情况下任何 body 都是 404,
// 只看状态码会误判成"参数没被拦下"。所以这里断言 400 系而不是 404。
func TestHTTPContractShotUpscaleRouteRegistered(t *testing.T) {
	s := newTestServer(t)
	got, out := doReq(t, s, "POST", "/api/projects/p1/shot/1/upscale", map[string]any{"type": "nope"})
	if got == 404 {
		t.Fatalf("路由未注册(POST /api/projects/{id}/shot/{shotID}/upscale 返回 404)—— 放大入口是本次补齐的能力,掉了会静默回到「界面入口尚未接入」")
	}
	if got != 400 {
		t.Fatalf("非法参数应返回 400,实际 %d(%v)", got, out)
	}
}

// TestHTTPContractMetaUpscaleDefaults /api/meta 必须下发放大链路的默认文件名。
//
// 前端不再自己记常量(此前那个 LTX VAE 文件名只活在用户手打的输入框里),
// 而是按这里的默认值回显、只让用户覆盖想改的那一项。这个字段掉了,前端就退回
// "空输入框 + 手打文件名",等于把刚补上的默认值又丢了。
func TestHTTPContractMetaUpscaleDefaults(t *testing.T) {
	s := newTestServer(t)
	got, out := doReq(t, s, "GET", "/api/meta", nil)
	if got != 200 {
		t.Fatalf("GET /api/meta want 200 got %d(%v)", got, out)
	}
	up, ok := out["upscale"].(map[string]any)
	if !ok {
		t.Fatalf("/api/meta 必须带 upscale 默认值(前端据此回显),实际 %v", out)
	}
	for _, k := range []string{"default_type", "default_model", "default_ltx_vae"} {
		v, _ := up[k].(string)
		if v == "" {
			t.Errorf("upscale.%s 不能为空,否则前端只能让用户手打文件名(实际 %v)", k, up)
		}
	}
	if v, _ := up["default_type"].(string); v != engine.DefaultUpscaleOptions().Type {
		t.Errorf("upscale.default_type 应与引擎默认一致,实际 %q", v)
	}
	if v, _ := up["default_ltx_vae"].(string); v != engine.DefaultLTXUpscaleVAE {
		t.Errorf("upscale.default_ltx_vae 应为 %q,实际 %q", engine.DefaultLTXUpscaleVAE, v)
	}
}
