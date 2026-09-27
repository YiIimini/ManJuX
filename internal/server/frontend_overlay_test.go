package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// frontend_overlay_test.go 弹层蒙层的口径：**只压暗 + 柔化，不遮挡**。
//
// 用户 2026-09-27 报障原话：「预览弹窗层级下面的窗口不要给黑色背景啊，后面的主应用窗口都看不到了」。
// 根因：三处全屏蒙层都写成了近乎不透明的纯黑（放大预览 .88/.95、全屏媒体预览 .82、封面对比 .88/.95）
// —— 这是**桌面应用**，盖掉之后整块屏就是黑的，看着像"窗口没了"。
//
// 为什么值得一条测试：这个数值被两个方向拉扯过 ——
//
//	2026-09-26 用户说「预览放大有个蒙层对比色吧!!」（嫌看不清图与底的边界）→ 当时把蒙层提到 .88/.95；
//	2026-09-27 用户说「后面的主应用窗口都看不到了」（嫌太黑）。
//
// 两次都对：**对比感该由"图本体的 accent 细边 + 大投影"给，不该由蒙层黑给**（09-26 那次自己写的结论）。
// 所以口径钉两条：① 蒙层走 --mx-scrim* 变量；② 蒙层自身的不透明度不许 ≥70%。
//
// ⚠ 只扫**蒙层那几条规则块**：style.css 里 `color-mix(...var(--mx-bg0) 78%…)` 这种数值到处都是
// （阴影、hover 底、浮窗底、`.bg-black/40` 兼容类），它们**不是蒙层**、也不该被这条口径管
// —— 第一版测试就是整文件扫，报了 11 处假阳性（教训：守卫要认得自己管的是哪一块）。
func TestOverlayScrimStaysTransparent(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("读 %s 失败：%v", rel, err)
		}
		return string(raw)
	}
	// ruleBlock 取 `selector {` 到配对的 `}` 之间的内容（这些蒙层块里没有嵌套花括号）。
	ruleBlock := func(src, selector string) string {
		i := strings.Index(src, selector+" {")
		if i < 0 {
			return ""
		}
		rest := src[i+len(selector)+2:]
		j := strings.Index(rest, "}")
		if j < 0 {
			return ""
		}
		return rest[:j]
	}

	// ① 变量必须在 :root 里定义（三处蒙层共用一套数值，要调只改一处），
	//    而且**必须由主题底色派生**（`color-mix(... var(--mx-bg0) NN%, transparent)`）——
	//    写死 #000 的话，换到非纯黑底的皮肤上蒙层就不跟色了（09-26 的老教训，
	//    由 frontend_novel_cover_test.go 与这条一起钉住）。
	css := read("style.css")
	for _, v := range []string{"--mx-scrim:", "--mx-scrim-strong:", "--mx-scrim-filter:"} {
		if !strings.Contains(css, v) {
			t.Errorf("style.css 的 :root 里应有 %s（三处蒙层共用的蒙层口径）", v)
		}
	}
	for _, v := range []string{"--mx-scrim:", "--mx-scrim-strong:"} {
		i := strings.Index(css, v)
		if i < 0 {
			continue
		}
		line := css[i:]
		if j := strings.Index(line, "\n"); j > 0 {
			line = line[:j]
		}
		if !strings.Contains(line, "var(--mx-bg0)") {
			t.Errorf("%s 必须由主题底色派生（var(--mx-bg0)），写死颜色换肤会不跟：%s", v, strings.TrimSpace(line))
		}
	}

	// ② 三处蒙层：都必须读变量、且自身不透明度过不了 70%
	type mask struct {
		file, selector string
	}
	masks := []mask{
		{"style.css", ".el-image-viewer__mask"},
		{"components/MediaViewer.vue", ".mv-mask"},
		{"components/CoverCompare.vue", ".cc-mask"},
	}
	pctRe := regexp.MustCompile(`(?i)color-mix\(\s*in srgb\s*,\s*(?:var\(--mx-bg\d\)|#[0-9a-f]{3,8}|black|#000)\s+(\d{1,3})%`)
	rgbaRe := regexp.MustCompile(`(?i)rgba?\(\s*0\s*,\s*0\s*,\s*0\s*,\s*(0?\.\d+|1(?:\.0+)?)\s*\)`)
	for _, m := range masks {
		blk := ruleBlock(read(m.file), m.selector)
		if blk == "" {
			t.Fatalf("找不到蒙层规则 %s（%s）—— 选择器改名了就把这条测试一起改", m.selector, m.file)
		}
		if !strings.Contains(blk, "var(--mx-scrim") {
			t.Errorf("%s 的 %s 应读 var(--mx-scrim*)（三处蒙层一套数值，别再各写一套）", m.file, m.selector)
		}
		for _, mm := range pctRe.FindAllStringSubmatch(blk, -1) {
			if n, err := strconv.Atoi(mm[1]); err == nil && n >= 70 {
				t.Errorf("%s 的 %s 里蒙层不透明度 %d%%（≥70%% 在桌面壳里等于把主窗口盖掉）；"+
					"对比感交给图本体的 accent 细边 + 大投影，蒙层用 --mx-scrim*", m.file, m.selector, n)
			}
		}
		for _, mm := range rgbaRe.FindAllStringSubmatch(blk, -1) {
			if f, err := strconv.ParseFloat(mm[1], 64); err == nil && f >= 0.7 {
				t.Errorf("%s 的 %s 里蒙层 alpha %.2f（≥0.7 同上）", m.file, m.selector, f)
			}
		}
	}

	// ③ EP 弹窗那一层（.el-overlay 读 --el-overlay-color-lighter，EP 默认 50% 纯黑）也收一档，
	//    且必须写在 themes.js 的 applyElementPlusVars 里 —— 那是本仓库覆盖 EP 变量的**唯一**生效点
	//    （index.html 写死 class="dark"，html.dark 的特异性压过 :root，见 theme.css 顶部的定案说明）。
	js := read("themes.js")
	if !strings.Contains(js, "'--el-overlay-color-lighter'") {
		t.Error("themes.js 的 applyElementPlusVars 里应设 --el-overlay-color-lighter（写在 :root 里不生效）")
	}
}
