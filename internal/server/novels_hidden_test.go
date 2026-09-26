package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 小说库作品菜单 + 隐藏状态闸门(2026-09-25 用户要求)。
//
// 需求原文:「小说管理页面封面右上角删除按钮需要新增功能菜单(视频制作、隐藏作品、删除作品)」。
// 三件事各有理由:① 视频制作是这部书最常见的下一步;② 隐藏比删除常用得多,而删除不可逆 ——
// 让不可逆的动作占着最容易误点的那一格本身就不对;③ 隐藏必须是**界面状态**,
// 不能真去动用户的小说文件。

// newHiddenTestServer 用现成的测试服务(DataDir 落在临时目录),
// 在它的小说库根下造两本书 —— 隐藏状态只依赖 NovelDir + DataDir,不必另搭一套装配。
func newHiddenTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	novelDir := s.novelRoot()
	if novelDir == "" {
		t.Fatal("测试服务的 NovelDir 为空(默认值应指向 DataDir/Novel)")
	}
	for _, n := range []string{"甲书", "乙书"} {
		// 每本书放一章正文,否则扫描会跳过空目录
		p := filepath.Join(novelDir, n, "正文")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "第001章_一.md"), []byte("正文若干字。"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s, novelDir
}

// TestNovelHiddenHidesFromListAndComesBack 隐藏 → 列表里消失(且计数 +1)→ ?hidden=1 请回来。
func TestNovelHiddenHidesFromListAndComesBack(t *testing.T) {
	s, novelDir := newHiddenTestServer(t)
	target := filepath.Join(novelDir, "甲书")

	// 初始:两本都可见,没有隐藏
	got := novelsOf(t, s, "")
	if len(got) != 2 || got[0].Hidden || got[1].Hidden {
		t.Fatalf("初始应列出 2 本且都未隐藏,实际 %+v", got)
	}

	// 隐藏甲书
	if _, err := s.setNovelHidden(target, true); err != nil {
		t.Fatalf("隐藏失败: %v", err)
	}
	got = novelsOf(t, s, "")
	if len(got) != 1 || got[0].Name != "乙书" {
		t.Fatalf("隐藏后列表应只剩乙书,实际 %+v", got)
	}
	if hc := hiddenCountOf(t, s, ""); hc != 1 {
		t.Errorf("hidden_count 应为 1,实际 %d", hc)
	}

	// ?hidden=1 把隐藏的请回来,并标记 hidden=true
	got = novelsOf(t, s, "?hidden=1")
	if len(got) != 2 {
		t.Fatalf("带 hidden=1 应列出 2 本,实际 %+v", got)
	}
	var hiddenOne *novelItem
	for i := range got {
		if got[i].Name == "甲书" {
			hiddenOne = &got[i]
		}
	}
	if hiddenOne == nil || !hiddenOne.Hidden {
		t.Errorf("甲书应带 hidden=true,实际 %+v", hiddenOne)
	}

	// 取消隐藏 → 回到默认列表
	if _, err := s.setNovelHidden(target, false); err != nil {
		t.Fatalf("取消隐藏失败: %v", err)
	}
	if got = novelsOf(t, s, ""); len(got) != 2 {
		t.Errorf("取消隐藏后应恢复 2 本,实际 %+v", got)
	}
	if hc := hiddenCountOf(t, s, ""); hc != 0 {
		t.Errorf("hidden_count 应回到 0,实际 %d", hc)
	}
}

// TestNovelHiddenTouchesNoNovelFile 隐藏只写 sidecar,**不许动小说目录里的任何文件**。
// 这是这个功能的底线:用户的书是用户的,界面状态不该往那儿塞东西。
func TestNovelHiddenTouchesNoNovelFile(t *testing.T) {
	s, novelDir := newHiddenTestServer(t)
	before := snapshotFiles(t, novelDir)
	if _, err := s.setNovelHidden(filepath.Join(novelDir, "甲书"), true); err != nil {
		t.Fatal(err)
	}
	after := snapshotFiles(t, novelDir)
	if len(before) != len(after) {
		t.Fatalf("隐藏动作改了小说目录的文件数:%d → %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("隐藏动作动了小说目录里的 %s", k)
		}
	}
	// sidecar 应落在 DataDir 下
	if _, err := os.Stat(s.hiddenNovelsPath()); err != nil {
		t.Errorf("隐藏状态没写进 DataDir 的 sidecar: %v", err)
	}
}

// TestNovelHiddenRejectsOutsideRoot 小说库之外/空路径不许写(防穿越)。
func TestNovelHiddenRejectsOutsideRoot(t *testing.T) {
	s, _ := newHiddenTestServer(t)
	for _, p := range []string{"", "   ", `C:\Windows\System32`, filepath.Join(filepath.Dir(s.novelRoot()), "外面")} {
		if _, err := s.setNovelHidden(p, true); err == nil {
			t.Errorf("路径 %q 不该被接受", p)
		}
	}
}

// TestNovelHiddenKeyNormalizesSeparators 键必须归一(Windows 上反斜杠/正斜杠混写会存成两条)。
func TestNovelHiddenKeyNormalizesSeparators(t *testing.T) {
	s, novelDir := newHiddenTestServer(t)
	a := s.novelHiddenKey(filepath.Join(novelDir, "甲书"))
	b := s.novelHiddenKey(strings.ReplaceAll(filepath.Join(novelDir, "甲书"), `\`, `/`))
	if a == "" || a != b {
		t.Errorf("同一部作品的键必须一致: %q / %q", a, b)
	}
	if strings.Contains(a, `\`) {
		t.Errorf("键里不该出现反斜杠: %q", a)
	}
}

// TestNovelHiddenRouteRegisteredAndValidates 路由存在、非法请求给 400(而不是静默 200)。
func TestNovelHiddenRouteRegisteredAndValidates(t *testing.T) {
	s, novelDir := newHiddenTestServer(t)

	// 缺 path → 400
	rec := httptest.NewRecorder()
	s.handleNovelHidden(rec, httptest.NewRequest(http.MethodPost, "/api/novels/hidden",
		strings.NewReader(`{"hidden":true}`)))
	if rec.Code != 400 {
		t.Errorf("缺 path 应 400,实际 %d", rec.Code)
	}

	// 非法 JSON → 400
	rec = httptest.NewRecorder()
	s.handleNovelHidden(rec, httptest.NewRequest(http.MethodPost, "/api/novels/hidden",
		strings.NewReader(`{`)))
	if rec.Code != 400 {
		t.Errorf("非法 JSON 应 400,实际 %d", rec.Code)
	}

	// 正常 → 200 且带回计数
	body := `{"path":` + jsonStr(filepath.Join(novelDir, "甲书")) + `,"hidden":true}`
	rec = httptest.NewRecorder()
	s.handleNovelHidden(rec, httptest.NewRequest(http.MethodPost, "/api/novels/hidden",
		strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("正常请求应 200,实际 %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK          bool `json:"ok"`
		Hidden      bool `json:"hidden"`
		HiddenCount int  `json:"hidden_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || !out.Hidden || out.HiddenCount != 1 {
		t.Errorf("响应不对: %+v", out)
	}
}

// ---------- 小工具 ----------

func novelsOf(t *testing.T, s *Server, query string) []novelItem {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleNovels(rec, httptest.NewRequest(http.MethodGet, "/api/novels"+query, nil))
	if rec.Code != 200 {
		t.Fatalf("列表接口 %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Novels      []novelItem `json:"novels"`
		HiddenCount int         `json:"hidden_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Novels
}

func hiddenCountOf(t *testing.T, s *Server, query string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleNovels(rec, httptest.NewRequest(http.MethodGet, "/api/novels"+query, nil))
	var out struct {
		HiddenCount int `json:"hidden_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.HiddenCount
}

func snapshotFiles(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		out[p] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
