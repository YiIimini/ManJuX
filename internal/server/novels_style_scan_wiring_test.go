package server

import (
	"strings"
	"testing"
)

// novels_style_scan_wiring_test.go 「导入的小说要解析风格字段」这条链必须接上（2026-10-09）。
//
// 用户原话：「另外导入的小说也需要解析是否存在视频风格相关字段」。
// 判据：解析 → 用于裁决 → **看得见**（界面）→ 播报说清取自哪个字段。少任何一环，解析等于没做。
func TestNovelStyleScanWiring(t *testing.T) {
	produce := readPkgFile(t, "novels_produce.go")
	style := readPkgFile(t, "novels_style.go")
	server := readPkgFile(t, "server.go")
	store := readFrontend(t, "src/store.js")
	view := readFrontend(t, "src/views/NovelsView.vue")

	for _, want := range []string{
		"scan = scanNovelStyle(in.Path)", // 导入时真的扫（而不是只看 art_style）
		"artStyle := scan.Style",         // 扫到的串真的进裁决
		"scan.Field",                     // 播报要说清取自哪个字段
		"scan.Card",                      // 风格卡存在要报出来
		"这本书里没有解析到视频风格字段", // 扫不到**如实说**（不许假装有）
	} {
		if !strings.Contains(produce, want) {
			t.Errorf("novels_produce.go 少了 %q —— 风格字段解析没接上或没播报", want)
		}
	}
	if !strings.Contains(style, "func scanNovelStyle(") {
		t.Error("novels_style.go 里没有 scanNovelStyle：小说自带风格字段的解析器不见了")
	}
	if !strings.Contains(server, `m.HandleFunc("GET /api/novels/style", s.novelStyle)`) {
		t.Error("server.go 没注册 GET /api/novels/style —— 界面拿不到解析结果")
	}
	if !strings.Contains(store, "novelStyle: (path) =>") {
		t.Error("store.js 没有 novelStyle 接口封装")
	}
	for _, want := range []string{
		"const produceScan = ref(null)",
		"api.novelStyle(n.path)",
		"<b>原书风格</b>", // 断言**渲染出来的**标签，不是注释里的同名字（注释命中=摆设守卫）
		"这本书里没有解析到视频风格字段",
		"另发现风格卡",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("NovelsView.vue 少了 %q —— 解析结果没显示给用户看（只写日志等于没解析）", want)
		}
	}
	// 「文学风格」字段（风格：主/次/与上一本的差异）**不许**进画风裁决 —— 这一条靠"不把那个键传进 pick"实现，
	// 所以判据必须盯**传参**（盯输出是摆设守卫：dict 本来就不是字符串，传了也取不到值 ⇒ 改坏照样绿）。
	for _, bad := range []string{`pick("画风", "风格")`, `pick("风格")`} {
		if strings.Contains(style, bad) {
			t.Errorf("novels_style.go 里出现了 %q —— 文学风格会被当成画面风格串（把「极致爽点」塞进提示词）", bad)
		}
	}
}
