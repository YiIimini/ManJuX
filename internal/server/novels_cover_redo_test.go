package server

import (
	"os"
	"path/filepath"
	"testing"
)

// 小说库「封面重做」入口的闸门(2026-09-26 用户要求)。
//
// 真机已验证的那条链(触发 → 出图 → 备份 → 刷新 40 个集项目封面)需要真引擎,
// 单元测试里只钉**不需要 GPU 就能判错**的几条:越界、缺正向词、引擎不可用。
// 这三条正是"用户点了没反应还看不出为什么"的形态,必须在界面上有一句明确的话。
func TestNovelCoverRedoGuards(t *testing.T) {
	s := newTestServer(t)
	root := s.novelRoot()
	book := filepath.Join(root, "书F")
	if err := os.MkdirAll(filepath.Join(book, "封面"), 0o755); err != nil {
		t.Fatal(err)
	}

	// ① 缺 path
	if code, _ := doReq(t, s, "POST", "/api/novels/cover-redo", map[string]any{}); code != 400 {
		t.Errorf("缺 path 应 400,实际 %d", code)
	}

	// ② 越界
	outside := t.TempDir()
	if code, _ := doReq(t, s, "POST", "/api/novels/cover-redo", map[string]any{"path": outside}); code != 403 {
		t.Errorf("小说库之外的路径应 403,实际 %d", code)
	}

	// ③ 没有正向词:要说清"需要哪个文件",而不是含糊失败
	code, out := doReq(t, s, "POST", "/api/novels/cover-redo", map[string]any{"path": book})
	if code != 400 {
		t.Fatalf("没有封面正向词应 400,实际 %d(%v)", code, out)
	}
	msg, _ := out["error"].(string)
	if !contains(msg, coverRedoPromptFile) {
		t.Errorf("报错要点名缺哪个文件(%s),实际 %q", coverRedoPromptFile, msg)
	}

	// ④ 有正向词但引擎没装配(测试环境):503 且说明是引擎的问题
	if err := os.WriteFile(filepath.Join(book, "封面", coverRedoPromptFile), []byte("a key visual"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := doReq(t, s, "POST", "/api/novels/cover-redo", map[string]any{"path": book}); code != 503 {
		t.Errorf("引擎不可用应 503(而不是 500/静默),实际 %d(%v)", code, out)
	}
}

// TestRefreshProjectsCoverOnlyTouchesCopies 刷新集项目封面时**只覆盖"从旧封面拷来的副本"**,
// 用户自己换过的封面一个都不许动 —— "我明明换过封面,它又变回去了"是最难查的一类。
func TestRefreshProjectsCoverOnlyTouchesCopies(t *testing.T) {
	s := newTestServer(t)
	old := []byte("OLD-COVER")
	neo := []byte("NEW-COVER")

	// 项目 A:封面就是旧封面的副本 → 应被刷新
	a := newProjectForTest(t, s, "书G·第01集·甲")
	writeFile(t, filepath.Join(s.store.Dir(a), "assets", "cover.png"), old)
	// 项目 B:用户自己换过 → 不许动
	b := newProjectForTest(t, s, "书G·第02集·乙")
	writeFile(t, filepath.Join(s.store.Dir(b), "assets", "cover.png"), []byte("MY-OWN-COVER"))
	// 项目 C:还没有封面 → 应写入
	c := newProjectForTest(t, s, "书G·第03集·丙")

	oldPath := filepath.Join(t.TempDir(), "封面.旧.png")
	writeFile(t, oldPath, old)
	newPath := filepath.Join(t.TempDir(), "封面.png")
	writeFile(t, newPath, neo)

	if n := s.refreshProjectsCover("书G", newPath, oldPath); n != 2 {
		t.Errorf("应刷新 2 个(副本 + 空封面),实际 %d", n)
	}
	if got := readFile(t, filepath.Join(s.store.Dir(a), "assets", "cover.png")); string(got) != string(neo) {
		t.Errorf("副本应被刷新为封面,实际 %q", string(got))
	}
	if got := readFile(t, filepath.Join(s.store.Dir(b), "assets", "cover.png")); string(got) != "MY-OWN-COVER" {
		t.Errorf("用户自己换的封面被覆盖了: %q", string(got))
	}
	if got := readFile(t, filepath.Join(s.store.Dir(c), "assets", "cover.png")); string(got) != string(neo) {
		t.Errorf("没有封面的集应写入新封面,实际 %q", string(got))
	}
	// 不知道旧封面长什么样时:**一个都不动**(无法区分"我们的副本"与"用户自己换的",
	// 宁可少刷一集,也不要把用户的选择覆盖掉)。
	if n := s.refreshProjectsCover("书G", newPath, ""); n != 0 {
		t.Errorf("旧封面未知时应一律不动,实际动了 %d 个", n)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", path, err)
	}
	return b
}
