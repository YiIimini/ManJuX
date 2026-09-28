package project

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 资产来源词汇表的闸门(2026-09-24 用户报「资产库里的 Krea-2 字样写死了」)。
//
// 前端曾经在七处各自用三元表达式猜来源的中文名,只认三种值 —— Q 版定妆、
// 视频截帧、抽卡全被显示成"上传"(与事实不符),而且后端以后新增来源,
// 前端一律继续显示"上传"。收口之后,真正的护栏是这条:**后端每写一种 source,
// 词汇表里必须有它**,漏登记就变红,而不是等用户发现界面在说假话。
func TestEveryAssetSourceHasALabel(t *testing.T) {
	root := filepath.Join("..", "..")
	files := map[string]string{}
	for _, dir := range []string{"internal", "."} {
		_ = filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				switch info.Name() {
				case "node_modules", "third_party", "webdist", "build", ".git", "data":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			files[filepath.ToSlash(rel)] = string(raw)
			return nil
		})
	}
	if len(files) == 0 {
		t.Fatal("没扫到 Go 源码 —— 闸门失效")
	}

	// 采集:Source: "x" / Source = "x" / l.Put(..., "x") 里的来源字面量。
	// 只认"看起来像来源"的取值:小写、无空格、无路径分隔符 —— 避免把
	// 提示词或路径误当来源。
	re := regexp.MustCompile(`(?i)(?:source\s*[:=]\s*|,\s*)(?:")([a-z][a-z0-9-]{1,24})(?:")`)
	// .Put(...) 这行的**最后一个**字符串才是来源（签名：(kind, name, desc, prompt, img, 项目名, 来源)）。
	// 第一版把整行所有引号串都当来源，于是 `Put(..., "verify-project", "ace-step")` 里的
	// **项目名**被误判成未登记的来源（2026-09-28 被 tools/musicverify 撞出来）。
	// 守卫自己的注释早就写着"library.Put 的最后一个参数"——注释对、实现粗。
	putLastArg := regexp.MustCompile(`\.Put\([^)]*,\s*"([a-z][a-z0-9-]{1,24})"\s*\)`)
	known := map[string]bool{}
	for _, e := range assetSourceNames {
		known[e.Key] = true
	}
	known[""] = true

	var missing []string
	seen := map[string]string{}
	for path, src := range files {
		if path == "internal/project/source_label.go" {
			continue // 词汇表自己
		}
		for i, ln := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			// 只在"资产来源"语境里判:赋值给 Source 字段,或 library.Put 的最后一个参数
			isSourceLine := strings.Contains(strings.ToLower(ln), "source:") || strings.Contains(strings.ToLower(ln), "source =") ||
				strings.Contains(ln, ".Put(")
			if !isSourceLine {
				continue
			}
			// .Put(...) 这行只取**最后一个**字符串参数当来源（其余参数是 kind/名字/描述/提示词/图片/项目名）
			if strings.Contains(ln, ".Put(") {
				putSrc := ""
				if m := putLastArg.FindStringSubmatch(ln); m != nil {
					putSrc = m[1]
				}
				for _, m := range re.FindAllStringSubmatch(ln, -1) {
					v := m[1]
					if v != putSrc {
						continue // 不是最后一个参数 ⇒ 是项目名之类，不是来源
					}
					if !known[v] {
						if _, dup := seen[v]; !dup {
							seen[v] = path
							missing = append(missing, v+"  ("+path+":"+itoa4(i+1)+")")
						}
					}
				}
				continue
			}
			for _, m := range re.FindAllStringSubmatch(ln, -1) {
				v := m[1]
				// 过滤明显不是来源的取值(资产类别 / 引擎名等在别的语境里合法)
				switch v {
				case "color", "image", "audio", "video", "text", "auto", "comfy", "api",
					"characters", "scenes", "props", "library_portrait":
					continue
				}
				if known[v] {
					continue
				}
				if _, dup := seen[v]; dup {
					continue
				}
				seen[v] = path
				missing = append(missing, v+"  ("+path+":"+itoa4(i+1)+")")
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("这些资产来源没有登记中文名(前端会原样显示 key,或更糟——显示成「上传」):\n  %s\n"+
			"请在 internal/project/source_label.go 的 assetSourceNames 里补一行。",
			strings.Join(missing, "\n  "))
	}
	// 反例:视频截帧与 Q 版必须被认出来(它们是这次报障里被误标成"上传"的两个)
	if AssetSourceLabel("video-frame") == "video-frame" {
		t.Error("video-frame 必须有中文名(它是最容易被误标成「上传」的来源之一)")
	}
	if AssetSourceLabel("krea2-chibi") == "krea2-chibi" {
		t.Error("krea2-chibi 必须有中文名")
	}
	// 认不出的来源原样返回(诚实:让用户看出"这是另一种来源")
	if got := AssetSourceLabel("some-new-source"); got != "some-new-source" {
		t.Errorf("未知来源应原样返回,实际 %q", got)
	}
	if got := AssetSourceLabel(""); got != "未知" {
		t.Errorf("空来源应显示「未知」,实际 %q", got)
	}
}

func itoa4(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
