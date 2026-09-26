package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// 单镜云端 2K 产物导出入口的契约测试(待决项 乙7,选项 A:只导出)。
//
// 这批文件的处境是"付了云额度却在应用里看不见":upgrade2k.go 把它们落在
// <项目>/clips/2k/shot_NNN_2k.mp4,而合成只收 manifest.File —— 既不进镜头清单,
// 也不参与合成,界面上更没有入口。补的入口必须同时做到三件事,本文件逐条钉住:
//
//	① 列得出:目录不存在/没生成过 = 空列表(200),不是错误;
//	② 拿得走:按文件名取回,预览/下载可用,字节与原文件一致;
//	③ 越不出去:名字只接受白名单形状,任何上跳/其它扩展名/其它目录一律拒绝 ——
//	   这条是**安全边界**,不是体验问题(name 直接来自 URL)。
//
// 用的是真装配服务器(http_contract_test.go 的 newTestServer + doReq)。

// doRaw 发一个请求并返回原始响应体与状态码。
//
// 与 doReq 的分工:doReq 把响应解成 map,而列表接口返回的是**数组** ——
// 解成 map 只会得到空 map,于是"返回了空数组"和"返回了 SPA 的 HTML"看起来一样。
func doRaw(t *testing.T, s *Server, method, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "127.0.0.1:8790" // 来源守卫要求回环 Host(见 guardAPI)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

// newProject 建一个真项目并返回 id。
func newProject(t *testing.T, s *Server, name string) string {
	t.Helper()
	code, out := doReq(t, s, "POST", "/api/projects", map[string]any{"name": name})
	if code != 200 {
		t.Fatalf("创建项目应 200,实际 %d(%v)", code, out)
	}
	proj, _ := out["project"].(map[string]any)
	if proj == nil {
		proj = out
	}
	id, _ := proj["id"].(string)
	if id == "" {
		t.Fatalf("创建响应缺少项目 id:%v", out)
	}
	return id
}

// writeShot2K 在项目里造一个单镜 2K 产物文件。
func writeShot2K(t *testing.T, s *Server, id, name string, size int) {
	t.Helper()
	dir := filepath.Join(s.store.Dir(id), "clips", "2k")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, size)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

type shot2kItem struct {
	Name      string  `json:"name"`
	Rel       string  `json:"rel"`
	Shot      int     `json:"shot"`
	SizeMB    float64 `json:"size_mb"`
	UpdatedAt string  `json:"updated_at"`
}

// TestShot2KOutputsList 列出接口:只列白名单形状的成品,按镜号升序。
func TestShot2KOutputsList(t *testing.T) {
	s := newTestServer(t)
	id := newProject(t, s, "2K导出契约")

	// ① 一个都没生成时:200 + 空数组(不是 404,也不是 SPA 的 HTML)
	code, raw := doRaw(t, s, "GET", "/api/projects/"+id+"/outputs/2k")
	if code != 200 {
		t.Fatalf("空目录应 200,实际 %d(body=%.120s)", code, raw)
	}
	var empty []shot2kItem
	if err := json.Unmarshal(raw, &empty); err != nil {
		t.Fatalf("响应必须是 JSON 数组(路由没注册时会落到 SPA 兜底返回 index.html):%v,body=%.120s", err, raw)
	}
	if len(empty) != 0 {
		t.Fatalf("还没生成过任何 2K 产物时列表应为空,实际 %v", empty)
	}

	// ② 造两个成形产物 + 三种"不该被列出来"的干扰项
	writeShot2K(t, s, id, "shot_002_2k.mp4", 2048)
	writeShot2K(t, s, id, "shot_001_2k.mp4", 1024)
	dir := filepath.Join(s.store.Dir(id), "clips", "2k")
	// 半截文件(没下完)、无关文件、0 字节残骸 —— 摆出来只会让用户下载到垃圾
	for _, junk := range []string{"shot_003_2k.mp4.part", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, junk), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "shot_004_2k.mp4"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	code, raw = doRaw(t, s, "GET", "/api/projects/"+id+"/outputs/2k")
	if code != 200 {
		t.Fatalf("列出应 200,实际 %d", code)
	}
	var items []shot2kItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("解析列表失败:%v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应只列出 2 个成形产物(半截/无关/0 字节都不算),实际 %d:%v", len(items), items)
	}
	// 按镜号升序:用户是"按镜头找文件",不是按文件名或落盘时间
	if items[0].Shot != 1 || items[1].Shot != 2 {
		t.Errorf("列表应按镜号升序,实际 %d → %d", items[0].Shot, items[1].Shot)
	}
	if items[0].Name != "shot_001_2k.mp4" || items[0].Rel != "clips/2k/shot_001_2k.mp4" {
		t.Errorf("列表项的名字/相对路径不对:%+v", items[0])
	}
	if items[0].SizeMB <= 0 || items[0].UpdatedAt == "" {
		t.Errorf("列表项必须带体积与落盘时间(界面直接展示),实际 %+v", items[0])
	}

	// ③ 不存在的项目:404(而不是空列表 —— 那是"没有产物",语义不同)
	if code, _ := doRaw(t, s, "GET", "/api/projects/__no_such_project__/outputs/2k"); code != 404 {
		t.Errorf("不存在的项目应 404,实际 %d", code)
	}
}

// TestShot2KOutputsServe 取回接口:字节与原文件一致,缺文件 404。
func TestShot2KOutputsServe(t *testing.T) {
	s := newTestServer(t)
	id := newProject(t, s, "2K取回契约")
	writeShot2K(t, s, id, "shot_007_2k.mp4", 4096)

	code, raw := doRaw(t, s, "GET", "/api/projects/"+id+"/outputs/2k/shot_007_2k.mp4")
	if code != 200 {
		t.Fatalf("取回已存在的 2K 产物应 200,实际 %d", code)
	}
	want, err := os.ReadFile(filepath.Join(s.store.Dir(id), "clips", "2k", "shot_007_2k.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != len(want) {
		t.Fatalf("取回内容长度不符:want %d got %d", len(want), len(raw))
	}
	for i := range want {
		if raw[i] != want[i] {
			t.Fatalf("取回内容与盘上文件不一致(第 %d 字节)", i)
		}
	}

	// 形状合法但文件不存在 → 404(与"名字非法"的 400 区分开)
	if code, _ := doRaw(t, s, "GET", "/api/projects/"+id+"/outputs/2k/shot_099_2k.mp4"); code != 404 {
		t.Errorf("不存在的产物应 404,实际 %d", code)
	}
	// 项目不存在 → 404
	if code, _ := doRaw(t, s, "GET", "/api/projects/__no_such_project__/outputs/2k/shot_007_2k.mp4"); code != 404 {
		t.Errorf("不存在的项目应 404,实际 %d", code)
	}
}

// TestShot2KOutputsRejectsTraversal 安全边界:名字只接受白名单形状。
//
// 为什么这条必须穷举而不是"看一眼觉得没问题":name 直接来自 URL,而取回实现要拿它
// 拼路径。**黑名单挡穿越是"漏一种写法就漏一个洞"** —— Windows 上 `..\`、
// URL 编码、盘符、NTFS 数据流各有各的写法。这里的方向反过来:先验形状,
// 形状之外连拼路径的机会都没有。
//
// 断言分两类,因为请求的**去向**本来就分两类:
//
//	A. 单个路径段内形状不符 —— 正常落到 handler,必须精确 400;
//	B. 段内藏着上跳/绝对路径(URL 编码后) —— Go 1.22+ 的 ServeMux 会在**匹配之前**
//	   先把路径清理掉并发 3xx,请求根本到不了本 handler。这类不能拿"状态码是不是 400"
//	   当判据(那是拿框架行为糊弄自己),要断言的是**真正的性质**:跟随重定向之后,
//	   目标文件的内容也不能以任何形式回到请求方 —— 这个口不是"读项目文件"的旁路。
//
// 顺带钉住第二件事:**768p 的原始镜头不能从这个口出去** —— 它是另一条链路
// (manifest.File)的产物,从这个"2K 导出"口被取走会让用户以为那就是 2K 版。
func TestShot2KOutputsRejectsTraversal(t *testing.T) {
	s := newTestServer(t)
	id := newProject(t, s, "2K越界契约")

	// 准备好"如果越界成功就会泄露"的目标文件(内容用醒目标记,便于在响应体里搜)
	const marker = "SECRET_MARKER_MUST_NOT_LEAK"
	root := s.store.Dir(id)
	if err := os.WriteFile(filepath.Join(root, "project.json"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "clips"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clips", "shot_001.mp4"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	// A. 单段内形状不符 → 精确 400
	shapeCases := []struct {
		desc string
		name string
	}{
		{"同目录下无关文件", "notes.txt"},
		{"768p 原始镜头(另一条链路的产物)", "shot_001.mp4"},
		{"反斜杠上跳(Windows 写法,单段)", "..%5C..%5Cproject.json"},
		{"形状相近但多一段", "shot_001_2k_extra.mp4"},
		{"形状相近但少一段", "shot_01_2k.mp4"},
		{"大小写不符(落盘命名是固定小写)", "SHOT_001_2K.MP4"},
	}
	for _, c := range shapeCases {
		t.Run(c.desc, func(t *testing.T) {
			code, raw := doRaw(t, s, "GET", "/api/projects/"+id+"/outputs/2k/"+c.name)
			if code != 400 {
				t.Errorf("非白名单形状应 400(形状不符,不是「找不到」),实际 %d", code)
			}
			if bytes.Contains(raw, []byte(marker)) {
				t.Errorf("响应体里出现了项目文件内容 —— 这个口成了读文件的旁路")
			}
		})
	}

	// B. 编码后的上跳/绝对路径:跟随重定向之后也不能拿到目标文件
	srv := httptest.NewServer(s.mux)
	defer srv.Close()
	client := &http.Client{} // 默认跟随重定向,模拟浏览器/下载器的真实行为
	for _, p := range []string{"..%2f..%2fproject.json", "%2e%2e%2fproject.json", "%2f..%2fproject.json"} {
		t.Run("编码路径 "+p, func(t *testing.T) {
			resp, err := client.Get(srv.URL + "/api/projects/" + id + "/outputs/2k/" + p)
			if err != nil {
				t.Fatalf("请求失败:%v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if bytes.Contains(body, []byte(marker)) {
				t.Fatalf("跟随重定向后拿到了目标文件(HTTP %d)—— 越界防护失效", resp.StatusCode)
			}
			if resp.StatusCode == 200 && !bytes.Contains(body, []byte("<")) {
				t.Errorf("200 且内容不是网页兜底,来路可疑:%d 字节", len(body))
			}
		})
	}
}

// TestShot2KOutputPatternMatchesPipeline 闸门:白名单正则必须与落盘命名一致。
//
// 这条防的是"两边一起改才生效"的那类失效:管线改了命名(比如改成 shot_0001_2k.mp4),
// 前端与接口都还在找 shot_001_2k.mp4 —— 界面永远显示"还没有 2K 产物",
// 而文件其实就躺在盘上。
func TestShot2KOutputPatternMatchesPipeline(t *testing.T) {
	// 与 pipeline/upgrade2k.go 的 fmt.Sprintf("shot_%03d_2k.mp4", shotID) 对齐
	for _, ok := range []string{"shot_001_2k.mp4", "shot_007_2k.mp4", "shot_120_2k.mp4"} {
		if !shot2kFileRe.MatchString(ok) {
			t.Errorf("%q 是管线会落盘的命名,白名单却拒收 —— 用户会看不到自己的付费产物", ok)
		}
	}
	// 落盘目录也必须与管线一致,否则列表永远是空的
	if filepath.ToSlash(shot2kDirName) != "clips/2k" {
		t.Errorf("产物目录常量应为 clips/2k(与 upgrade2k.go 落盘位置一致),实际 %q", shot2kDirName)
	}
}
