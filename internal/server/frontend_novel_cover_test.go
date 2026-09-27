package server

import (
	"strings"
	"testing"
)

// 封面放大 / 对比预览的前端闸门。
//
// 两轮用户要求叠在一起,这条把它们一起钉住:
//
//	2026-09-26 ①「小说库中的封面重做新制作的封面可以放大预览啊」
//	2026-09-26 ②「重做的封面放大预览左侧需要显示旧的,右侧显示新的,这样才好对比」
//
// ②把 ① 的形态改掉了:候选不再走 el-image-viewer(单张查看器回答不了"它比现在这张好吗"),
// 改成自建的并排对比视图(CoverCompare.vue);el-image-viewer 留给书卡封面的单张放大。
//
// 钉的都是"少一行就静默坏掉、界面看着还挺正常"的地方:
//   - 候选图必须带点击且带上标号 —— 只点开第一张等于没做(要比的是第 3 张);
//   - 对比的两侧必须共用同一套缩放/平移 —— 各缩各的,还得靠眼睛自己换算,那就不叫对比;
//   - 对比视图必须 Teleport:它是在 el-dialog 里打开的,留在原地会被弹窗层级与 overflow 困住;
//   - 书卡上的放大钮必须 @click.stop —— 卡片主体点击是打开详情弹窗,不 stop 就是两个弹窗叠着;
//   - 观感一律走主题变量(裸色会被 theme 闸门拦)。
func TestNovelCoverZoomPreview(t *testing.T) {
	vue := readFrontend(t, "src/views/NovelsView.vue")
	css := readFrontend(t, "src/style.css")

	// ① 候选图:点第 i 张 → 打开对比视图并从第 i 张看起
	for _, want := range []string{
		`v-for="(c, i) in covCands"`,
		`@click="openCompare(i)"`,
		"function openCompare(i)",
		"const cmpOpen = ref(false)",
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("候选封面点开对比缺少「%s」", want)
		}
	}
	if strings.Contains(vue, "viewCands(") {
		t.Error("候选还挂在旧的单张预览(viewCands)上 —— 单张看不出「比原来好在哪」,正是这轮要改的")
	}
	// ② 对比视图接线:左旧(现封面)右新(候选);关掉封面重做弹窗时一并收起
	for _, want := range []string{
		"<CoverCompare v-if=\"cmpOpen\"",
		`:old-url="oldCoverURL()"`,
		`:items="cmpItems"`,
		`@close="cmpOpen = false"`,
		`@apply="onCompareApply"`,
		`append-to-body @close="cmpOpen = false"`, // 弹窗关了别留一个对着空列表的对比视图
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("对比视图接线缺少「%s」", want)
		}
	}
	// ③ 已有候选要消费掉(config 早就返回它了):否则每开一次弹窗都得重新出图才能比
	if !strings.Contains(vue, "covCands.value = c.candidates || []") {
		t.Error("打开封面重做弹窗时应带出已有候选 —— 后端在 cover-config 里返回它,前端丢掉就等于每次都白出一次图")
	}
	// ④ el-image-viewer 只留给书卡的单张放大
	for _, want := range []string{
		"<el-image-viewer v-if=\"viewer\"",
		":url-list=\"viewer.list\"",
		"teleported",
		"@close=\"viewer = null\"",
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("书卡放大预览接线缺少「%s」", want)
		}
	}
	// ⑤ 书卡封面:放大钮必须在,且不能把卡片的"打开详情"一起触发
	if !strings.Contains(vue, `v-if="n.cover" class="cover-zoom"`) {
		t.Error("书卡缺少封面放大钮(v-if=n.cover:没封面时不该有个点不动的空钮)")
	}
	if !strings.Contains(vue, `@click.stop="viewCover(n)"`) {
		t.Error("放大钮必须 @click.stop —— 否则点放大连详情弹窗一起弹出")
	}
	// ⑥ 观感
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

	// ⑦ 蒙层要有"对比色"(用户 2026-09-26:「预览放大有个蒙层对比色吧!!」)。
	// Element Plus 默认 `.el-image-viewer__mask{opacity:.5;background:#000}` 是半透明纯黑薄纱:
	// 底层的小说库书名会透在图上,暗调封面压在黑纱上也没有边界。
	mask := cssBlockOf(t, css, ".el-image-viewer__mask {")
	if !strings.Contains(mask, "opacity: 1 !important") {
		t.Errorf("蒙层必须盖掉 EP 的 opacity:.5(半透明会让底层书名透在图上):\n%s", mask)
	}
	// 蒙层颜色必须取自主题变量 —— 写死颜色换个肤就又糊了（09-26 的原始理由，仍然成立）。
	// 2026-09-27 起颜色经 **--mx-scrim\* 变量**间接取（数值与"只压暗不遮挡"的口径在那一条里，
	// 定义本身仍是 `color-mix(... var(--mx-bg0) NN%, transparent)`，见 frontend_overlay_test.go
	// 对"必须由主题底色派生"的断言）—— 断言跟着这层间接改，要求的东西没变：
	// ① 不写死颜色；② 仍混一丝 accent 当对比色。
	if !strings.Contains(mask, "var(--mx-scrim") || !strings.Contains(mask, "var(--mx-amber)") {
		t.Errorf("蒙层颜色必须取自主题变量(经 --mx-scrim* 由 bg0 派生 + 一丝 accent)——写死颜色换个肤就又糊了:\n%s", mask)
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

// TestCoverCompareSideBySide 对比视图本身:左旧右新,两侧共用一套缩放/平移。
//
// 这条盯的是"并排"这个形态本身 —— 用户要的是能一眼看出差别的比较,不是再给一个看图的地方:
// 少了任一侧、或者两侧各缩各的,这个视图就没有存在意义。
func TestCoverCompareSideBySide(t *testing.T) {
	cmp := readFrontend(t, "src/components/CoverCompare.vue")

	// ① 并排:两侧都在,且各有标签(看得见"哪个是旧的")
	for _, want := range []string{"旧封面", "候选", "这本书还没有封面"} {
		if !strings.Contains(cmp, want) {
			t.Errorf("对比视图缺少「%s」", want)
		}
	}
	// ② 同一套视图变换应用到两张图:各缩各的就不是对比了
	if n := strings.Count(cmp, `:style="viewStyle"`); n < 2 {
		t.Errorf("左右两侧必须共用同一个 viewStyle(共用的缩放/平移),实际只用了 %d 处", n)
	}
	if !strings.Contains(cmp, "const viewStyle = computed") {
		t.Error("缺少共用的 viewStyle(缩放/平移)")
	}
	// ③ 挂载方式:在 el-dialog 里打开,必须 Teleport 到 body,否则被弹窗层级/overflow 困住
	if !strings.Contains(cmp, `<Teleport to="body">`) {
		t.Error("对比视图必须 Teleport 到 body —— 它在封面重做弹窗里打开")
	}
	// ④ 交互四件套:换候选(左右固定、新的那侧翻)、缩放、复位、直接定稿
	for _, want := range []string{
		"function step(d)", // ← → 换候选
		"function zoomBy(f)",
		"function reset()",
		`@click="emit('apply', cur)"`, // 比完就能直接换,不必关掉再回弹窗找
		"'Escape'", "ArrowLeft", "ArrowRight",
	} {
		if !strings.Contains(cmp, want) {
			t.Errorf("对比视图缺少「%s」", want)
		}
	}
	// ⑤ 观感走主题变量(裸色由 theme 闸门管,这里只确认没写成死色)
	if !strings.Contains(cmp, "var(--mx-amber)") || !strings.Contains(cmp, "var(--mx-bg0)") {
		t.Error("对比视图的蒙层/边线要用主题变量")
	}
	// ⑥ 量尺寸时传的是侧别字符串,不是 ref。
	// 模板里 setup 的 ref 会被自动解包:measure($event, oldSize) 传进去的是空字符串,
	// 函数里再写 .value 就抛「Cannot create property 'value' on string」—— 图照常显示、
	// 界面看着没事,只在控制台静默报错(2026-09-26 真机实测栽过一次)。
	if !strings.Contains(cmp, `measure($event, 'old')`) || !strings.Contains(cmp, `measure($event, 'new')`) {
		t.Error("量尺寸要传侧别字符串 measure($event, 'old'|'new')")
	}
	if strings.Contains(cmp, `@load="measure($event, oldSize)"`) || strings.Contains(cmp, `@load="measure($event, newSize)"`) {
		t.Error("别把 ref 传进模板里的函数 —— 它会被自动解包成值,写 .value 会抛错(控制台静默报错那种)")
	}
}

// TestSeedFromCandName 候选文件名 → seed。
//
// 弹窗重开时要"接着挑已有候选",而挑的时候第一眼看的正是 seed;种子只在文件名里
// (`c<seed>-<毫秒>.png`),解析写错不会报错、只会静静显示成空 —— 所以钉住。
func TestSeedFromCandName(t *testing.T) {
	cases := map[string]int64{
		"c1084184436-100.png": 1084184436,
		"c1-1.png":            1,
		"封面.png":              0, // 不合规则:返回 0,前端显示「seed —」,不假装知道
		"c-1.png":             0,
		"cabc-1.png":          0,
		"c586536020-999.png":  586536020,
	}
	for name, want := range cases {
		if got := seedFromCandName(name); got != want {
			t.Errorf("seedFromCandName(%q) = %d,期望 %d", name, got, want)
		}
	}
}
