package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 版本同源闸门(审计 P0-4)。
//
// 旧版 scripts/build.ps1 用 `git describe --tags` 反推版本基线写回三个文件,
// 于是 HEAD 领先 tag 时,**按文档单独跑一次 build.ps1 就把 v0.37.2 改回 v0.34.4**
// 并弄脏工作区;规避办法是"记得显式传 MANJUX_VERSION",等于把坑留给记性。
// 现在真源是 version.go,写入逻辑在 Go 里(可测),describe 永不写文件。

func writeFixture(t *testing.T, dir, ver string) {
	t.Helper()
	files := map[string]string{
		"wails.json":                  `{"name":"ManJuX","productVersion":"` + strings.TrimPrefix(ver, "v") + `"}`,
		"frontend/package.json":       `{"name":"manjux","version":"` + strings.TrimPrefix(ver, "v") + `"}`,
		"internal/version/version.go": "package version\n\nvar Version = \"" + ver + "\"\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVersionStringNormalizes(t *testing.T) {
	cases := map[string]string{
		"v0.37.2":             "v0.37.2",
		"0.37.2":              "v0.37.2",
		"v0.34.4-16-g1525068": "v0.34.4", // git describe 的形态:剥成基线
		"v1.0.0-beta":         "v1.0.0-beta",
		"  v2.0.0  ":          "v2.0.0",
	}
	for in, want := range cases {
		if got := VersionString(in); got != want {
			t.Errorf("VersionString(%q) = %q,期望 %q", in, got, want)
		}
	}
	if Numeric("v0.37.2") != "0.37.2" {
		t.Errorf("Numeric 应去掉 v 前缀,实际 %q", Numeric("v0.37.2"))
	}
}

// TestSyncWithoutReleaseIsNoOp 没有发布版本要注入时,三处已一致就**一个字节都不许写**。
// 这条正是"独立跑一次 build.ps1 不再弄脏工作区"的保证 —— 用文件 mtime 变化来验证。
func TestSyncWithoutReleaseIsNoOp(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "v0.37.2")

	before := map[string]int64{}
	for _, rel := range []string{"wails.json", "frontend/package.json", "internal/version/version.go"} {
		st, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		before[rel] = st.ModTime().UnixNano()
	}

	changed, err := Sync(dir, "")
	if err != nil {
		t.Fatalf("同步失败:%v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("三处已一致时不该有改动,实际改了 %v", changed)
	}
	for rel, mt := range before {
		st, _ := os.Stat(filepath.Join(dir, rel))
		if st.ModTime().UnixNano() != mt {
			t.Errorf("%s 被无谓重写(内容没变却动了 mtime)—— 会让构建看起来改了文件", rel)
		}
	}
	if problems := Check(dir); len(problems) > 0 {
		t.Errorf("同源校验应通过,实际:%v", problems)
	}
}

// TestSyncUsesVersionGoAsTruth 真源是 version.go:两个 JSON 落后时按它纠正,
// 而 version.go 自己不被改动。
func TestSyncUsesVersionGoAsTruth(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "v0.37.2")
	// 把两个 JSON 退回到旧版本(模拟"手改过 / 上次构建写错")
	if err := os.WriteFile(filepath.Join(dir, "wails.json"), []byte(`{"productVersion":"0.34.4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend/package.json"), []byte(`{"version":"0.34.4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Sync(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("应纠正两个 JSON,实际 %v", changed)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "internal/version/version.go"))
	if !strings.Contains(string(raw), `var Version = "v0.37.2"`) {
		t.Errorf("version.go 是真源,不该被改动:%s", raw)
	}
	if problems := Check(dir); len(problems) > 0 {
		t.Errorf("同步后应同源,实际:%v", problems)
	}
}

// TestSyncReleaseStripsDescribeSuffix 发布注入带 describe 后缀时只写基线 ——
// 这是旧坑的另一半:v0.34.4-16-gabc 这种串写进 wails.json 会让安装包名与
// 文件属性一直显示"tag 后面的第几个提交"。
func TestSyncReleaseStripsDescribeSuffix(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "v0.37.2")

	changed, err := Sync(dir, "v0.38.0-3-gabcdef1")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 {
		t.Fatalf("三处都应变到新版本,实际 %v", changed)
	}
	for _, rel := range []string{"wails.json", "frontend/package.json"} {
		raw, _ := os.ReadFile(filepath.Join(dir, rel))
		if strings.Contains(string(raw), "-3-gabcdef1") {
			t.Errorf("%s 不该写入 describe 后缀:%s", rel, raw)
		}
		if !strings.Contains(string(raw), "0.38.0") {
			t.Errorf("%s 应写基线 0.38.0:%s", rel, raw)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "internal/version/version.go"))
	if !strings.Contains(string(raw), `var Version = "v0.38.0"`) {
		t.Errorf("version.go 应带 v 前缀且不含后缀:%s", raw)
	}
	if got, _ := Current(dir); got != "v0.38.0" {
		t.Errorf("真源应为 v0.38.0,实际 %q", got)
	}
}

// TestCheckReportsMismatch 闸门本体:不一致必须报出来(否则这层保护等于不存在)。
func TestCheckReportsMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "v0.37.2")
	if err := os.WriteFile(filepath.Join(dir, "wails.json"), []byte(`{"productVersion":"0.34.4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	problems := Check(dir)
	if len(problems) == 0 {
		t.Fatal("版本不一致时 Check 必须报错")
	}
	if !strings.Contains(strings.Join(problems, ";"), "wails.json") {
		t.Errorf("说明里应点名是哪个文件漂了:%v", problems)
	}
}

// TestRepoVersionFilesAgree 真实仓库的三处版本此刻必须同源。
//
// 这条让"某次手工改版本只改了两处"在 go test 里立刻变红,而不是等到发版才发现。
func TestRepoVersionFilesAgree(t *testing.T) {
	root := filepath.Join("..", "..")
	if problems := Check(root); len(problems) > 0 {
		t.Errorf("版本三处不同源:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestBuildScriptDoesNotDeriveVersionFromGitTag 构建脚本不得再用 git tag 反推版本。
//
// 静态判据刻意写得很具体(检查那几行赋值),因为这条规则一旦被"顺手改回去",
// 症状是"构建把版本号改回旧版",只有下一个人踩坑才会发现。
func TestBuildScriptDoesNotDeriveVersionFromGitTag(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "build.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "cmd/versync") {
		t.Error("build.ps1 必须通过 cmd/versync 同步版本(唯一实现点),否则又会分叉出一份 PowerShell 正则改写")
	}
	for _, bad := range []string{
		`(?m)^(var Version = ")`,
		`("(?:product)?version":\s*")`,
	} {
		if strings.Contains(src, bad) {
			t.Errorf("build.ps1 里又出现了自己改写版本文件的正则(%s)—— 改回 versync", bad)
		}
	}
	if strings.Contains(src, `$verBase = ($ver -split '-')[0]`) {
		t.Error("build.ps1 不应再从 git describe 串里切版本基线")
	}
}
