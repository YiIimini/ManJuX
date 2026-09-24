package version

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 版本三处同源的**唯一实现点**。
//
// 起因(2026-09-24 全项目审计 P0-4):旧版 scripts/build.ps1 用
// `git describe --tags` 反推版本基线再写回三个文件 —— 而 describe 给的是
// "最近 tag + 领先提交数"(v0.34.4-16-gabc123),在 HEAD 领先 tag 的仓库里,
// **单独跑一次 build.ps1 就把已经升到 v0.37.2 的三处文件改回 v0.34.4**,
// 同时把工作区弄脏。当时的规避办法是"每次都记得显式传 MANJUX_VERSION",
// 也就是把一个必然踩的坑留给人的记性。
//
// 现在的规则(不再有"记性"这一环):
//   - 真源 = internal/version/version.go 里的 var Version;
//   - 显式传入发布版本(release.ps1 的 -Version / MANJUX_VERSION)时才覆盖它;
//   - git describe 只用于 exe 内的显示串(ldflags),**永不写文件**。
//
// 逻辑放在 Go 而不是 PowerShell 里,是为了让它可被单元测试与闸门覆盖 ——
// 脚本里的正则改写没法测,这正是这个坑能存在这么久的原因。

// VersionString 归一化一个版本串:补 v 前缀、剥掉 "领先 tag 的 -N-gHASH" 后缀。
//
//	v0.37.2-16-g1525068 -> v0.37.2
//	0.37.2              -> v0.37.2
//	dev                 -> vdev
func VersionString(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '-'); i > 0 {
		// 仅当后缀形如 -16-g1525068(describe 的形态)时才剥,避免把
		// v1.0.0-beta 这类合法的预发布标识切掉。
		if regexp.MustCompile(`^-\d+-g[0-9a-fA-F]+$`).MatchString(s[i:]) {
			s = s[:i]
		}
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s
}

// Numeric 去掉 v 前缀(写进两个 JSON 的形态)。
func Numeric(v string) string { return strings.TrimPrefix(VersionString(v), "v") }

// versionFile 参与同源的一个文件。
type versionFile struct {
	rel   string
	read  func(raw string) (string, bool)
	write func(raw, num, ver string) string
}

var versionFiles = []versionFile{
	{
		// wails.json 把版本放在 info.productVersion(camelCase 的 V,必须 (?i))。
		rel: "wails.json",
		read: func(raw string) (string, bool) {
			m := regexp.MustCompile(`(?i)"(?:product)?version"\s*:\s*"([^"]*)"`).FindStringSubmatch(raw)
			if m == nil {
				return "", false
			}
			return m[1], true
		},
		write: func(raw, num, _ string) string {
			return regexp.MustCompile(`(?i)("(?:product)?version"\s*:\s*")[^"]*(")`).ReplaceAllString(raw, "${1}"+num+"${2}")
		},
	},
	{
		rel: "frontend/package.json",
		read: func(raw string) (string, bool) {
			m := regexp.MustCompile(`"version"\s*:\s*"([^"]*)"`).FindStringSubmatch(raw)
			if m == nil {
				return "", false
			}
			return m[1], true
		},
		write: func(raw, num, _ string) string {
			return regexp.MustCompile(`("version"\s*:\s*")[^"]*(")`).ReplaceAllString(raw, "${1}"+num+"${2}")
		},
	},
	{
		// version.go 用带 v 前缀的写法(与 git tag 一致),故单独一条规则。
		rel: "internal/version/version.go",
		read: func(raw string) (string, bool) {
			m := regexp.MustCompile(`(?m)^var Version = "([^"]*)"`).FindStringSubmatch(raw)
			if m == nil {
				return "", false
			}
			return m[1], true
		},
		write: func(raw, _, ver string) string {
			return regexp.MustCompile(`(?m)^(var Version = ")[^"]*(")`).ReplaceAllString(raw, "${1}"+ver+"${2}")
		},
	},
}

// Current 读出版本真源(internal/version/version.go 的 var Version)。
func Current(root string) (string, error) {
	rel := versionFiles[2].rel
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "", fmt.Errorf("读版本真源失败(%s):%w", rel, err)
	}
	v, ok := versionFiles[2].read(string(raw))
	if !ok {
		return "", fmt.Errorf("%s 里找不到 `var Version = \"...\"`", rel)
	}
	return VersionString(v), nil
}

// Sync 把三个文件统一到目标版本并返回**真正被改动的文件**。
//
// release 为空表示"没有发布版本要注入":此时以 version.go 现值为准 ——
// 三处本来就一致时**一个字节都不写**(这正是"独立跑一次构建不会弄脏工作区"的保证)。
func Sync(root, release string) ([]string, error) {
	target := VersionString(release)
	if target == "" {
		cur, err := Current(root)
		if err != nil {
			return nil, err
		}
		target = cur
	}
	if target == "" || target == "vdev" {
		return nil, fmt.Errorf("版本号无法解析(得到 %q):请显式传入 MANJUX_VERSION", target)
	}
	num := Numeric(target)

	var changed []string
	for _, vf := range versionFiles {
		path := filepath.Join(root, vf.rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			return changed, fmt.Errorf("读 %s 失败:%w", vf.rel, err)
		}
		want := num
		if strings.HasSuffix(vf.rel, "version.go") {
			want = target
		}
		if got, ok := vf.read(string(raw)); ok && strings.TrimPrefix(got, "v") == strings.TrimPrefix(want, "v") {
			continue // 已一致:不写盘,避免无谓的 mtime 变更与工作区脏
		}
		out := vf.write(string(raw), num, target)
		if out == string(raw) {
			continue
		}
		// 保持原有 BOM 状态(frontend/package.json 带 BOM 会让 pnpm 解析失败,
		// 而其它文件若本来带 BOM 也不该被我们悄悄去掉)。
		bom := strings.HasPrefix(string(raw), "\ufeff")
		data := []byte(out)
		if bom {
			data = append([]byte("\ufeff"), data...)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return changed, fmt.Errorf("写 %s 失败:%w", vf.rel, err)
		}
		changed = append(changed, vf.rel)
	}
	return changed, nil
}

// Check 校验三处版本是否同源,返回不一致的说明(空 = 全部一致)。
// 基准是**真源 version.go**,不是"第一个读到的文件"。
func Check(root string) []string {
	var problems []string
	want, err := Current(root)
	if err != nil {
		return []string{err.Error()}
	}
	for _, vf := range versionFiles {
		raw, rerr := os.ReadFile(filepath.Join(root, vf.rel))
		if rerr != nil {
			problems = append(problems, fmt.Sprintf("%s 读不到:%v", vf.rel, rerr))
			continue
		}
		got, ok := vf.read(string(raw))
		if !ok {
			problems = append(problems, fmt.Sprintf("%s 里找不到版本字段", vf.rel))
			continue
		}
		if strings.TrimPrefix(got, "v") != strings.TrimPrefix(want, "v") {
			problems = append(problems, fmt.Sprintf("版本不一致:%s=%s,而真源 version.go=%s(跑 scripts/build.ps1 即可同步三处)", vf.rel, got, want))
		}
	}
	return problems
}
