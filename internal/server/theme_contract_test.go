package server

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 主题配色契约测试。
//
// 为什么放在 server 包里:前端产物是**本包 embed 并对外提供的**(webdist),
// 而主题数据(frontend/src/themes.js)与首屏兜底(style.css 的 :root)一旦漂移,
// 症状只在界面颜色上 —— 属于"没人会想到去跑"的那类回归。这里把它变成闸门。
//
// 守的是一条长期约定(2026-09-24 用户两次报障后的定调):
//   **主题色相必须出现在"面"上**,不能只活在按钮/激活态里 —— 否则大面积的
//   卡片区(数据卡/书卡/面板)看起来就是一片灰黑,等于主题没生效。
// 断言分三组:
//   A. 可读性 —— 调色之后文字三级对其实际所在的"卡片面"仍达下限(暗色 UI 最容易在这里翻车);
//   B. 可辨识 —— 卡片描边与角部辉光**确实带上了色相**(不是"更暗一点的灰");
//   C. 首屏 —— style.css 的 :root 必须与默认主题同值(否则启动闪色)。

type themePalette struct {
	id       string
	bg0      rgb
	bg2      rgb
	line     rgb
	amber    rgb
	text     rgb
	text2    rgb
	text3    rgb
	panelRGB rgb
	tint     float64 // 底色里强调色的占比;0 = 无彩主题(不吃强调色)
}

type rgb struct{ r, g, b float64 }

// 卡片面上各层的比例,必须与实现一致:
//
//	themes.js  applyTheme:  材质 = color-mix(amber tint%, panelRGB);glaze 顶层 = amber 12%
//	style.css  :root:       --mx-card-bd = amber 26% + line;--mx-sheen 顶层 = amber 15%
const (
	defaultTintRatio = 0.10
	glazeBottomAlpha = 0.10
	borderAccent     = 0.26
	sheenAlpha       = 0.15
	// glassMaxAlpha 是玻璃透明度滑块的上限(themes.js 里 clamp 到 0.88)——
	// 面板越不透明、面色越亮,对"浅字压深底"越不利,所以拿它当最坏情况。
	glassMaxAlpha = 0.88
)

var (
	reThemeID = regexp.MustCompile(`id:\s*'([^']+)'`)
	// 引号可选:themes.js 里是 `'--mx-bg0': '#000000'`,style.css 里是 `--mx-bg0: #000000;`
	reHexVar = regexp.MustCompile(`'?--mx-(bg0|bg2|line|amber|text|text2|text3)'?\s*:\s*'?(#[0-9a-fA-F]{6})'?`)
	rePanel  = regexp.MustCompile(`panelRGB:\s*'(\d+),\s*(\d+),\s*(\d+)'`)
	reTint   = regexp.MustCompile(`tint:\s*([0-9.]+)`)
)

// parseThemes 从 themes.js 解析全部主题。按 id 切块:每块从 `id: '...'` 到下一个。
func parseThemes(t *testing.T, js string) []themePalette {
	t.Helper()
	idx := reThemeID.FindAllStringSubmatchIndex(js, -1)
	if len(idx) < 2 {
		t.Fatalf("themes.js 里只解析到 %d 个主题,解析器可能失配", len(idx))
	}
	var out []themePalette
	for i, m := range idx {
		end := len(js)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := js[m[0]:end]
		p := themePalette{id: js[m[2]:m[3]], tint: defaultTintRatio}
		// 注意:#000000 与"解析失败"的零值同形,所以用出现过与否来判定,不能拿零值当缺失
		seen := map[string]bool{}
		for _, v := range reHexVar.FindAllStringSubmatch(block, -1) {
			c := mustRGB(t, v[2])
			seen[v[1]] = true
			switch v[1] {
			case "bg0":
				p.bg0 = c
			case "bg2":
				p.bg2 = c
			case "line":
				p.line = c
			case "amber":
				p.amber = c
			case "text":
				p.text = c
			case "text2":
				p.text2 = c
			case "text3":
				p.text3 = c
			}
		}
		if pm := rePanel.FindStringSubmatch(block); pm != nil {
			seen["panelRGB"] = true
			p.panelRGB = rgb{atof(pm[1]), atof(pm[2]), atof(pm[3])}
		}
		if tm := reTint.FindStringSubmatch(block); tm != nil {
			p.tint = atof(tm[1])
		}
		for _, name := range []string{"bg0", "bg2", "line", "amber", "text", "text2", "text3", "panelRGB"} {
			if !seen[name] {
				t.Errorf("主题 %s 缺少 %s(解析失败)", p.id, name)
			}
		}
		out = append(out, p)
	}
	return out
}

func themesSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "themes.js"))
	if err != nil {
		t.Fatalf("读 themes.js 失败: %v", err)
	}
	return string(b)
}

func mustRGB(t *testing.T, hex string) rgb {
	t.Helper()
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		t.Fatalf("不是 6 位 hex: %q", hex)
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		t.Fatalf("hex 解析失败 %q: %v", hex, err)
	}
	return rgb{float64(n >> 16 & 0xff), float64(n >> 8 & 0xff), float64(n & 0xff)}
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// mix 复刻 CSS color-mix(in srgb, a p%, b):在**编码后的 sRGB** 通道上线性插值。
func mix(a, b rgb, p float64) rgb {
	return rgb{p*a.r + (1-p)*b.r, p*a.g + (1-p)*b.g, p*a.b + (1-p)*b.b}
}

// over 复刻 alpha 合成:top 以 alpha 盖在 bot 上。
func over(top, bot rgb, alpha float64) rgb {
	return rgb{alpha*top.r + (1-alpha)*bot.r, alpha*top.g + (1-alpha)*bot.g, alpha*top.b + (1-alpha)*bot.b}
}

func channel(c float64) float64 {
	v := c / 255
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func luminance(c rgb) float64 {
	return 0.2126*channel(c.r) + 0.7152*channel(c.g) + 0.0722*channel(c.b)
}

func contrast(a, b rgb) float64 {
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// chroma 用最大最小通道差当"有没有颜色"的粗略判据(0 = 纯灰)。
func chroma(c rgb) float64 {
	return math.Max(c.r, math.Max(c.g, c.b)) - math.Min(c.r, math.Min(c.g, c.b))
}

// cardSurface 复刻"卡片面"的颜色 —— 也就是文字真正压在哪儿:
//
//	材质   = color-mix(amber tint%, panelRGB)                  (themes.js 的 tint)
//	玻璃开 = 该材质按上限 alpha 0.88 盖在 bg0 上                 (玻璃只会让面更暗,故取亮的一侧)
//	涂层   = --mx-glaze 的顶层(amber 12%,**自带 alpha 不吃玻璃透明度**)
//
// 刻意**不含白纱**(veil):那是玻璃的"材质高光",只覆盖卡片顶部一小段渐变,
// 而卡片里的文字(标题/字数)坐在下半部 —— 拿顶部高光处算下限会得出
// "连没调色时的原设计都不达标"的结论,那不是契约、是误判。
// 同理涂层只算它压在文字带上的那一档(12%),不加上角部 sheen —— sheen 在另一头。
func cardSurface(p themePalette) rgb {
	flat := mix(p.amber, p.panelRGB, p.tint)
	surface := over(flat, p.bg0, glassMaxAlpha)
	if p.tint > 0 {
		surface = over(p.amber, surface, glazeBottomAlpha)
	}
	return surface
}

// TestThemeTextContrast 可读性:三级文字对卡片面的对比度不得低于约定下限。
func TestThemeTextContrast(t *testing.T) {
	themes := parseThemes(t, themesSource(t))
	// 下限见 themes.js 头部第 5 条:text ≥10 / text2 ≥4.5 / text3 ≥3.0。
	// (text 从 16 放到 10 是因为卡片面参与调色后必然变亮;10:1 仍远高于 WCAG AAA 的 7:1)
	for _, p := range themes {
		surface := cardSurface(p)
		for _, v := range []struct {
			name string
			c    rgb
			min  float64
		}{
			{"--mx-text", p.text, 10.0},
			{"--mx-text2", p.text2, 4.5},
			{"--mx-text3", p.text3, 3.0},
		} {
			got := contrast(v.c, surface)
			t.Logf("%-16s %-11s 对卡片面 rgb(%.0f,%.0f,%.0f) 对比度 %.2f(下限 %.1f)",
				p.id, v.name, surface.r, surface.g, surface.b, got, v.min)
			if got < v.min {
				t.Errorf("主题 %s 的 %s 对卡片面对比度只有 %.2f,低于下限 %.1f —— "+
					"面板调色把面提亮了,要么降 tint,要么提亮该级文字(两者都在 themes.js 里)",
					p.id, v.name, got, v.min)
			}
		}
	}
}

// TestThemeSurfaceCarriesHue 可辨识:卡片**描边与角部辉光**必须带上主题色相。
// 这是用户两次报障("数据卡片没有体现主题配色")的回归守卫 ——
// 灰黑卡片会被这条断言拦下来。
//
// 为什么盯描边与辉光而不是底色:底色是深色,10% 的暖强调色调进冷底之后
// 通道差可能只剩个位数(实测奶油金青只有 2.3),单靠它撑不起"看得出主题";
// 而描边(26%)与辉光(15% 自成一层、不吃玻璃透明度)是稳的,且眼睛最先读到。
func TestThemeSurfaceCarriesHue(t *testing.T) {
	themes := parseThemes(t, themesSource(t))
	for _, p := range themes {
		surface := cardSurface(p)
		// --mx-card-bd  = color-mix(in srgb, var(--mx-amber) 26%, var(--mx-line))
		border := mix(p.amber, p.line, borderAccent)
		// --mx-sheen 顶层 = color-mix(in srgb, var(--mx-amber) 15%, transparent) 压在面上
		sheen := over(p.amber, surface, sheenAlpha)
		t.Logf("%-16s 面色 rgb(%.0f,%.0f,%.0f) 色度 %.0f | 描边 色度 %.0f | 辉光 色度 %.0f",
			p.id, surface.r, surface.g, surface.b, chroma(surface), chroma(border), chroma(sheen))

		// 无彩主题(mono-noir,tint=0,swatch 就是银白)不参与色相断言:
		// 它要的是"纯黑高对比 + 银白描边",给它上色反而违背用户定的调子。
		// 也要保证它**确实**没被上色 —— 否则"无彩"名不副实。
		if p.tint == 0 {
			if chroma(surface) > 3 {
				t.Errorf("主题 %s 声明为无彩(tint=0),面色却带色(色度 %.1f)", p.id, chroma(surface))
			}
			continue
		}
		if chroma(surface) < 8 {
			t.Errorf("主题 %s 的卡片面色色度只有 %.1f(近灰)—— --mx-glaze 没把主题色留在文字带上", p.id, chroma(surface))
		}
		if chroma(border) < 12 {
			t.Errorf("主题 %s 的卡片描边色度只有 %.1f(近灰)—— --mx-card-bd 的强调色占比太低", p.id, chroma(border))
		}
		if chroma(sheen) < 12 {
			t.Errorf("主题 %s 的卡片角部辉光色度只有 %.1f(近灰)—— --mx-sheen 压不住主题色", p.id, chroma(sheen))
		}
		// 面色不能亮到丢层级(与上一条互补:那条查对比度下限,这条防"为了上色把面提亮")
		if luminance(surface) > 0.05 {
			t.Errorf("主题 %s 的卡片面亮度过高(%.3f),暗色 UI 的层级会垮", p.id, luminance(surface))
		}
	}
}

// TestRootMirrorsDefaultTheme style.css 的 :root 是"JS 应用主题前的首屏兜底",
// 必须与 themes.js 的默认主题同值 —— 否则启动会先闪一下别的配色。
// (这条曾经真的踩过:默认主题换成极简墨黑时漏改 :root 就是一次闪色。)
func TestRootMirrorsDefaultTheme(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "style.css"))
	if err != nil {
		t.Fatalf("读 style.css 失败: %v", err)
	}
	src := string(css)
	rootEnd := strings.Index(src, "}")
	if rootEnd < 0 {
		t.Fatal("style.css 里找不到 :root 块")
	}
	rootBlock := src[:rootEnd]

	var def themePalette
	for _, p := range parseThemes(t, themesSource(t)) {
		if p.id == "mono-noir" {
			def = p
		}
	}
	if def.id == "" {
		t.Fatal("themes.js 里找不到默认主题 mono-noir")
	}

	want := map[string]rgb{
		"bg0": def.bg0, "bg2": def.bg2, "line": def.line, "amber": def.amber,
		"text": def.text, "text2": def.text2, "text3": def.text3,
	}
	seen := map[string]bool{}
	for _, v := range reHexVar.FindAllStringSubmatch(rootBlock, -1) {
		name, hex := v[1], v[2]
		w, ok := want[name]
		if !ok {
			continue
		}
		seen[name] = true
		if got := mustRGB(t, hex); got != w {
			t.Errorf("style.css :root 的 --mx-%s = %s,与默认主题 mono-noir 的 %s 不一致(首屏会闪色)",
				name, hex, hexOf(w))
		}
	}
	for name, c := range want {
		if !seen[name] {
			t.Errorf("style.css :root 缺少 --mx-%s(默认主题是 %s)", name, hexOf(c))
		}
	}
}

func hexOf(c rgb) string {
	return fmt.Sprintf("#%02X%02X%02X", int(c.r), int(c.g), int(c.b))
}

// TestSuggestText3 是一次性诊断(不是契约):打印"要让 text3 对当前卡片面达到 3.05,
// 各主题的 text3 至少要亮到哪个 hex"。它只在重新推导调色板时手动跑,不进闸门。
//
//	go test ./internal/server/ -run TestSuggestText3 -v
func TestSuggestText3(t *testing.T) {
	if os.Getenv("MANJUX_SUGGEST") == "" {
		t.Skip("诊断用:设 MANJUX_SUGGEST=1 才跑")
	}
	for _, p := range parseThemes(t, themesSource(t)) {
		surface := cardSurface(p)
		want := 3.05*(luminance(surface)+0.05) - 0.05
		// 保持色相:把 text3 逐通道往白推,直到亮度达标
		best := p.text3
		for i := 0; i < 100; i++ {
			cand := over(rgb{255, 255, 255}, p.text3, float64(i)/100)
			if luminance(cand) >= want {
				best = cand
				break
			}
		}
		t.Logf("%-16s 面色 rgb(%.0f,%.0f,%.0f) 需亮度≥%.3f → text3 %s(现 %s,对比度 %.2f)",
			p.id, surface.r, surface.g, surface.b, want, hexOf(best), hexOf(p.text3), contrast(p.text3, surface))
	}
}
