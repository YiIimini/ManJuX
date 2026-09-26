package server

import (
	"os"
	"path/filepath"
	"strings"
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

// TestCoverRedoConfigAndApplySteps 可配置版的三段式(2026-09-26 用户:
// 「做成一个用户自定义配置弹窗啊,重做需要点改变的啊」)必须真的分开:
// 配置读初始值、生成只写候选不动现封面、挑定才落盘。
func TestCoverRedoConfigAndApplySteps(t *testing.T) {
	s := newTestServer(t)
	root := s.novelRoot()
	book := filepath.Join(root, "书J")
	writeFile(t, filepath.Join(book, "封面", coverRedoPromptFile), []byte("A cinematic key visual of a river city"))
	writeFile(t, filepath.Join(book, "封面", "封面.png"), []byte("CURRENT-COVER"))

	// ① 配置:给弹窗初始值(正词 + 尺寸预设 + 现封面尺寸)
	code, out := doReq(t, s, "GET", "/api/novels/cover-config?path="+urlEnc(book), nil)
	if code != 200 {
		t.Fatalf("配置接口应 200,实际 %d(%v)", code, out)
	}
	if p, _ := out["prompt"].(string); !strings.Contains(p, "river city") {
		t.Errorf("应回带留档正向词,实际 %q", p)
	}
	if sizes, _ := out["sizes"].([]any); len(sizes) < 3 {
		t.Errorf("应给若干尺寸预设,实际 %v", out["sizes"])
	}

	// ② 生成:引擎不可用时 503(测试环境没装 comfy),且**不许**动现封面
	code, out = doReq(t, s, "POST", "/api/novels/cover-redo",
		map[string]any{"path": book, "prompt": "改过的词", "width": 1024, "height": 1024, "count": 2})
	if code != 503 {
		t.Fatalf("引擎不可用应 503,实际 %d(%v)", code, out)
	}
	if b := readFile(t, filepath.Join(book, "封面", "封面.png")); string(b) != "CURRENT-COVER" {
		t.Error("生成阶段不许改现封面 —— 候选与定稿必须分开")
	}

	// ③ 落盘:只认本书封面目录下的候选
	outside := filepath.Join(t.TempDir(), "x.png")
	writeFile(t, outside, []byte("HACK"))
	if code, _ := doReq(t, s, "POST", "/api/novels/cover-apply",
		map[string]any{"path": book, "file": outside}); code == 200 {
		t.Error("书目录之外的图片不许设为封面")
	}
	cand := filepath.Join(book, "封面", coverCandidatesDir, "c1-1.png")
	writeFile(t, cand, []byte("NEW-COVER"))
	if code, out := doReq(t, s, "POST", "/api/novels/cover-apply",
		map[string]any{"path": book, "file": cand}); code != 200 {
		t.Fatalf("挑定候选应 200,实际 %d(%v)", code, out)
	}
	if b := readFile(t, filepath.Join(book, "封面", "封面.png")); string(b) != "NEW-COVER" {
		t.Errorf("落盘后封面应是挑中的那张,实际 %q", string(b))
	}
}

// TestNormCoverSize 尺寸规整:夹在 [512,2048] 并对齐 16;缺省回技能端口径。
func TestNormCoverSize(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{0, 0, 896, 1344},
		{1024, 1024, 1024, 1024},
		{100, 100, 512, 512},
		{4000, 4000, 2048, 2048},
		{900, 1350, 896, 1344}, // 900→896(16 的倍数向下),1350→1344
	}
	for _, c := range cases {
		if w, h := normCoverSize(c.w, c.h); w != c.wantW || h != c.wantH {
			t.Errorf("normCoverSize(%d,%d) = %d×%d,期望 %d×%d", c.w, c.h, w, h, c.wantW, c.wantH)
		}
	}
}

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
