package server

import (
	"strings"
	"testing"
)

// 封面放大预览(2026-09-26 用户:「小说库中的封面重做新制作的封面可以放大预览啊」)。
//
// 钉的是**接线**而不是观感,而且钉的都是"少一行就静默坏掉、界面看着还挺正常"的地方:
//
//	① 候选图必须带点击且带上标号 —— 只点开第一张等于没做(挑图要看的是第 3 张);
//	② 预览器必须 teleported:候选图是在 el-dialog 里点开的,留在原地会被弹窗的层叠与
//	   遮罩困住(点得开、看不见);
//	③ 书卡上的放大钮必须 @click.stop —— 卡片主体点击是打开详情弹窗,不 stop 就是
//	   "点放大顺手把详情也拉起来",两个弹窗叠在一起;
//	④ 观感在 style.css 且用主题变量(裸色会被 theme 闸门拦)。
func TestNovelCoverZoomPreview(t *testing.T) {
	vue := readFrontend(t, "src/views/NovelsView.vue")
	css := readFrontend(t, "src/style.css")

	// ① 候选图:点开 → 从被点的那张开始,整批一起交给预览器(左右可翻)
	for _, want := range []string{
		`v-for="(c, i) in covCands"`,
		`@click="viewCands(i)"`,
		"function viewCands(i)",
		"covCands.value.map(c => candURL(c.file))",
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("候选封面放大预览缺少「%s」", want)
		}
	}
	// ② 预览器接线
	for _, want := range []string{
		"<el-image-viewer v-if=\"viewer\"",
		":url-list=\"viewer.list\"",
		":initial-index=\"viewer.index\"",
		"teleported",
		"@close=\"viewer = null\"",
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("预览器接线缺少「%s」", want)
		}
	}
	// ③ 书卡封面:放大钮必须在,且不能把卡片的"打开详情"一起触发
	if !strings.Contains(vue, `v-if="n.cover" class="cover-zoom"`) {
		t.Error("书卡缺少封面放大钮(v-if=n.cover:没封面时不该有个点不动的空钮)")
	}
	if !strings.Contains(vue, `@click.stop="viewCover(n)"`) {
		t.Error("放大钮必须 @click.stop —— 否则点放大连详情弹窗一起弹出")
	}
	// ④ 观感
	if !hasOwnBlock(css, ".cover-zoom") {
		t.Error("style.css 缺少 .cover-zoom 自己的定位块(它不在共享段里定位)")
	}
	if !strings.Contains(css, ".cover-del, .cover-menu, .cover-zoom {") {
		t.Error("放大钮必须与 .cover-del/.cover-menu 共用同一段配方(尺寸/底色只定义一次),别再手搓第三套")
	}
	idx := strings.Index(css, ".cand-item img {")
	if idx < 0 {
		t.Fatal("style.css 缺少 .cand-item img")
	}
	end := idx + 400
	if end > len(css) {
		end = len(css)
	}
	if !strings.Contains(css[idx:end], "cursor: zoom-in") {
		t.Error("候选图要 cursor: zoom-in —— 可点却没提示等于没人知道能点")
	}

	// ⑤ 蒙层要有"对比色"(用户 2026-09-26:「预览放大有个蒙层对比色吧!!」)。
	// Element Plus 默认 `.el-image-viewer__mask{opacity:.5;background:#000}` 是半透明纯黑薄纱:
	// 底层的小说库书名会透在图上,暗调封面压在黑纱上也没有边界。
	mask := cssBlockOf(t, css, ".el-image-viewer__mask {")
	if !strings.Contains(mask, "opacity: 1 !important") {
		t.Errorf("蒙层必须盖掉 EP 的 opacity:.5(半透明会让底层书名透在图上):\n%s", mask)
	}
	if !strings.Contains(mask, "var(--mx-bg0)") || !strings.Contains(mask, "var(--mx-amber)") {
		t.Errorf("蒙层颜色必须取自主题变量(bg0 深底 + 一丝 accent)——写死颜色换个肤就又糊了:\n%s", mask)
	}
	if !strings.Contains(mask, "backdrop-filter") {
		t.Errorf("蒙层要模糊底层,把它化成色块而不是透字:\n%s", mask)
	}
	// 覆盖 EP 默认样式必须带 !important:main.js 里 element-plus 的 CSS 在 style.css 之后引入
	if !strings.Contains(mask, "!important") {
		t.Error("覆盖在注释里(见 .cover-menu-wrap 那段)——不带 !important 会被 EP 后引入的同权重规则顶掉")
	}
	// 图本体要有 accent 细边/投影:暗调封面与深色蒙层之间就靠它分界
	img := cssBlockOf(t, css, ".el-image-viewer__img {")
	if !strings.Contains(img, "box-shadow") || !strings.Contains(img, "var(--mx-amber)") {
		t.Errorf("预览图要有 accent 细边 + 投影,否则暗调封面与蒙层分不开:\n%s", img)
	}
	// 工具条也得跟主题(EP 硬编码灰白 #fff / --el-text-color-regular)
	acts := cssBlockOf(t, css, ".el-image-viewer__actions,")
	if !strings.Contains(acts, "var(--mx-bg3)") {
		t.Errorf("缩放条/前后钮要随主题,别留着 EP 的灰白:\n%s", acts)
	}
}
