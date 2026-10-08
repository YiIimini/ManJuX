package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// import_pkg_sanitize_test.go 项目包导入这条路上的**数据安全**判据。
//
// 【为什么单列一份】（2026-10-08 深度排查发现的 P0）
// 包清单来自**外部文件**（`manifest.json`，技能侧写、用户也能手改），而它的
// `book`/`episode`/`episode_title` 三段会被**直接拼进落盘路径**：
//
//	<projects 根>/<book>/<book·episode·title>/
//
// 平台此前只判了 `book != ""`。于是 `book: "..\\..\\x"` 能把整集文件写到数据根之外，
// 而同一个导入流程失败时的回滚是 `os.RemoveAll(dest)` —— 删的也是数据根之外。
// 这两条必须有机检：一等判据是 sanitizePkgNames，二等是 destUnderRoot（复制/删除前再挡一次）。

// TestSanitizePkgNamesBlocksPathEscape 名字里的分隔符/点号**一律拿不出来**，且改过要说清楚。
// 夹具照"真机会出现的形态"写：技能侧工具名带标点、章名即集名、书名字段被人手改过。
func TestSanitizePkgNamesBlocksPathEscape(t *testing.T) {
	cases := []struct{ name, book, ep, title string }{
		{"反斜杠上跳", `..\..\outside`, `第01集`, `我是代号`},
		{"正斜杠上跳", `../../outside`, `第01集`, `我是代号`},
		{"绝对路径(盘符)", `C:\Windows`, `第01集`, `我是代号`},
		{"集号本身就是上跳", `名册之外`, `..`, `..`},
		{"标题带分隔符与冒号", `名册之外`, `第01集`, `a/b:c`},
		{"UNC 形态", `\\server\share`, `第01集`, `我是代号`},
	}
	for _, c := range cases {
		m := pkgManifest{Book: c.book, Episode: c.ep, EpisodeTitle: c.title}
		note := sanitizePkgNames(&m)
		if note == "" {
			t.Errorf("%s：名字被改过却没说（改字必须如实播报，否则技能侧\"章名即集名\"对不上）", c.name)
		}
		got := map[string]string{"book": m.Book, "episode": m.Episode, "episode_title": m.EpisodeTitle}
		for field, v := range got {
			if v == "" {
				t.Errorf("%s：%s 被清成空串（应当折叠成可用名字，而不是丢掉）", c.name, field)
			}
			for _, bad := range []string{"/", `\`, "..", ":"} {
				if strings.Contains(v, bad) {
					t.Errorf("%s：%s 里仍留着 %q（%q）—— 它能被拼进落盘路径", c.name, field, bad, v)
				}
			}
		}
		// 落盘路径必须在根之内（一等判据过了，这里再确认二等判据对同一份名字也成立）。
		root := filepath.Join("D:", "data", "projects")
		dest := filepath.Join(root, m.Book, m.Book+"·"+m.Episode+"·"+orDefault(m.EpisodeTitle, "未命名"))
		if !destUnderRoot(root, dest) {
			t.Errorf("%s：净化后的落盘路径仍在根外：%s", c.name, dest)
		}
	}
}

// TestSanitizePkgNamesIsNoOpForCleanNames 干净的名字一个字都不许改（否则正常包的目录名会被改）。
func TestSanitizePkgNamesIsNoOpForCleanNames(t *testing.T) {
	m := pkgManifest{Book: "名册之外，八万零一号", Episode: "第01集", EpisodeTitle: "我是代号"}
	before := m
	if note := sanitizePkgNames(&m); note != "" {
		t.Errorf("干净的名字被改动了：%s", note)
	}
	if m != before {
		t.Errorf("干净的名字被改动了：%+v → %+v", before, m)
	}
}

// TestDestUnderRoot 第二道防线：判据是"清干净的根 + 分隔符是前缀"。
//
// 钉住那个经典写法错误：直接 `strings.HasPrefix(dest, root)` 会把**兄弟目录**误判为根内
// （root=D:\data\projects、dest=D:\data\projects_backup\x 也是前缀）。
func TestDestUnderRoot(t *testing.T) {
	root := filepath.Join("D:", "data", "projects")
	cases := []struct {
		dest string
		want bool
	}{
		{filepath.Join(root, "书", "书·第01集·我是代号"), true},
		{root, true},
		{filepath.Join(root, "..", "outside"), false},
		{filepath.Join("D:", "data", "projects_backup", "x"), false},
		{filepath.Join("D:", "data"), false},
		{filepath.Join("C:", "Windows", "System32"), false},
	}
	for _, c := range cases {
		if got := destUnderRoot(root, c.dest); got != c.want {
			t.Errorf("destUnderRoot(%q) = %v，想要 %v", c.dest, got, c.want)
		}
	}
}

// TestReadPackageSanitizesManifestNames 端到端：包里的 manifest.json 写 `..\..\outside`，
// 读出来的清单必须**已经净化**（并带说明）—— 判据在 readPackage 这一处，两个导入入口共用。
func TestReadPackageSanitizesManifestNames(t *testing.T) {
	dir := t.TempDir()
	man := map[string]any{
		"contract": "manjux.plan", "version": project.PlanContractVersion,
		"book": `..\..\outside`, "episode": `第01集`, "episode_title": `我是代号`,
	}
	rawMan, _ := json.Marshal(man)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), rawMan, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan", "plan.json"), []byte(`{"shots":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, _, err := readPackage(dir)
	if err != nil {
		t.Fatalf("readPackage 不该失败：%v", err)
	}
	if strings.Contains(got.Book, "..") || strings.ContainsAny(got.Book, `/\`) {
		t.Errorf("book 没被净化：%q —— 这个值会被拼进落盘路径", got.Book)
	}
	if got.SanitizeNote == "" {
		t.Error("净化过却没有说明（导入流程要靠它如实播报\"名字被改过\"）")
	}
}
