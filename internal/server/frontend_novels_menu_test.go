package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stripVueComments 剥掉 `<!-- … -->` 注释块。
//
// 源码闸门必须**只看代码**:沿革说明里当然会提到被移除入口的名字(那正是注释的用途),
// 拿整份文件做 Contains 会把说明文字当成代码命中。
func stripVueComments(s string) string {
	for {
		i := strings.Index(s, "<!--")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "-->")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+3:]
	}
}

// 小说库封面「作品菜单」的前端闸门(2026-09-25 用户要求)。
//
// 需求:封面右上角原来只有一个**删除**按钮,要改成菜单(视频制作 / 隐藏作品 / 删除作品)。
// 这里钉三件事 —— 菜单三项都在、删除**不再挂在封面第一格**、接口按隐藏口径调用。
// 判据收在源码文本上:这类"入口有没有接上"的行为,组件级测试跑不到,而漏了就是漏了。
func TestNovelsCoverMenuOffersThreeActions(t *testing.T) {
	path := filepath.Join("..", "..", "frontend", "src", "views", "NovelsView.vue")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 %s: %v", path, err)
	}
	src := string(raw)

	// ① 菜单容器与命令出口
	for _, want := range []string{"<el-dropdown", `@command="(c) => onCardCmd(c, n)"`, "function onCardCmd("} {
		if !strings.Contains(src, want) {
			t.Errorf("封面右上缺少作品菜单结构(%q)", want)
		}
	}
	// ② 三个动作都在,且都走同一个命令出口
	for _, cmd := range []string{`command="produce"`, `command="hide"`, `command="remove"`} {
		if !strings.Contains(src, cmd) {
			t.Errorf("菜单缺少动作 %s", cmd)
		}
	}
	for _, label := range []string{"视频制作", "隐藏作品", "删除作品"} {
		if !strings.Contains(src, label) {
			t.Errorf("菜单缺少文案 %q", label)
		}
	}
	// ③ 删除**不许**再挂在封面的裸按钮上 —— 不可逆的动作不该占最容易误点的一格
	if strings.Contains(src, `@click.stop="removeNovel(`) {
		t.Error("封面上仍有直接的删除点击入口:删除必须收进菜单(带二次确认的那条路)")
	}
	if !strings.Contains(src, "function removeNovel(") {
		t.Error("removeNovel 应当保留(菜单里仍要用它,且带二次确认)")
	}
	// ④ 隐藏走的是隐藏接口,不是删除接口
	if !strings.Contains(src, "api.novelHidden(") {
		t.Error("菜单里的隐藏必须调 api.novelHidden(只改界面状态,不动文件)")
	}
}

// TestNovelsApiWiresHiddenFlag 前端 api 层要能带 hidden 开关、且新接口已登记。
func TestNovelsApiWiresHiddenFlag(t *testing.T) {
	path := filepath.Join("..", "..", "frontend", "src", "store.js")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 %s: %v", path, err)
	}
	src := string(raw)
	for _, want := range []string{
		"'/api/novels' + (withHidden ? '?hidden=1' : '')",
		"novelHidden: (path, hidden) => req('POST', '/api/novels/hidden'",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("store.js 缺少 %q", want)
		}
	}
	// 路由与前端路径必须同名 —— 写错一个字母就是静默 404
	if !strings.Contains(src, "/api/novels/hidden") {
		t.Error("前端调用的隐藏接口路径与后端路由不一致")
	}
}

// TestCoverMenuPositioningLivesOnTheWrapper 封面菜单的定位必须在**外层 wrapper** 上。
//
// 2026-09-26 实测踩坑:Element Plus 的 .el-dropdown 自带 position:relative,按钮上的
// absolute 会改为相对那个 0 宽度的 dropdown 盒子定位 → right:8px 落到封面边界之外,
// 被 overflow:hidden 裁掉,现象就是"按钮不见了"。这条把修法钉住:
// 模板里必须用 .cover-menu-wrap 包着,样式里那个类必须有 position:absolute,
// 而 .cover-menu 自己**不许**再出现 position。
func TestCoverMenuPositioningLivesOnTheWrapper(t *testing.T) {
	vuePath := filepath.Join("..", "..", "frontend", "src", "views", "NovelsView.vue")
	vue, err := os.ReadFile(vuePath)
	if err != nil {
		t.Fatalf("读不到 %s: %v", vuePath, err)
	}
	if !strings.Contains(string(vue), `class="cover-menu-wrap"`) {
		t.Error("el-dropdown 上必须挂 .cover-menu-wrap(定位由外层承担)")
	}
	if !strings.Contains(string(vue), `class="cover-menu"`) {
		t.Error("触发按钮仍应是 .cover-menu")
	}

	cssPath := filepath.Join("..", "..", "frontend", "src", "style.css")
	raw, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("读不到 %s: %v", cssPath, err)
	}
	css := string(raw)
	wrap := cssBlockOf(t, css, ".cover-menu-wrap")
	if !strings.Contains(wrap, "position: absolute") {
		t.Errorf(".cover-menu-wrap 必须自己绝对定位(否则 dropdown 的 relative 会接管):\n%s", wrap)
	}
	if !strings.Contains(wrap, "top: 8px") || !strings.Contains(wrap, "right: 8px") {
		t.Errorf(".cover-menu-wrap 应贴封面右上 8px:\n%s", wrap)
	}
	// 权重必须压过 Element Plus 的 .el-dropdown(main.js 把它的样式放在 style.css 之后,
	// 同权重时后写的胜)—— 2026-09-26 实测:只写一遍类名时那段 absolute 被顶成 relative,
	// 钮跑到封面左上角。判据直接看选择器本体:类名必须写两遍。
	if !strings.Contains(css, ".cover-menu-wrap.cover-menu-wrap {") {
		t.Error(".cover-menu-wrap 的选择器必须把类名写两遍(0,2,0)以压过 Element Plus 的 .el-dropdown —— 只写一遍会被它的 position:relative 顶掉,钮跑到左上角")
	}
	// 菜单钮自己不许再定位(定位必须留在 wrapper 上)。
	// 2026-09-26 加入封面放大钮 .cover-zoom 后,基础样式段成了三个类的并列选择器,原来那句
	// 字面 `.cover-menu {` 再也匹配不到(并列段里的子串是 `.cover-menu, .cover-zoom {`)。
	// 判据改成"行首独立块":并列段不算,真另开一块才算 —— .cover-del/.cover-zoom 各有自己的
	// 定位块(它们不在 .el-dropdown 里,不受那条 relative 影响),而 .cover-menu 在,所以只有它
	// 不许有。
	if hasOwnBlock(css, ".cover-menu") {
		t.Error(".cover-menu 不许另开样式块 —— 它长在 .el-dropdown 里,absolute 会被对方的 position:relative 接管(钮跑到左上角);定位请留在 .cover-menu-wrap 上")
	}
}

// TestCoverMenuSharesBaseStyleWithDeleteButton 菜单钮必须**沿用删除钮那套观感**。
//
// 2026-09-26 用户两次点我:先是我给菜单钮另写了一套"改进配方"(全站共用的删除钮也被我
// 一起改了外观),他说「原来的删除按钮更换这个菜单按钮,调用函数更换成对的不就行了」——
// 需求只是把删除钮换成菜单钮,不是重做控件。这条把"同源"钉住:
// 基础样式写成并列选择器、不许给 .cover-menu 另开样式块。
//
// 2026-09-26 补:封面放大钮 .cover-zoom 入列(用户要求封面能放大预览),并列段变成三个类。
// 规矩不变 —— 尺寸/底色/描边/图标色仍**只定义一次**,新钮跟着共享段走,不手搓第三套配方;
// 它自己那块只允许写定位与光标。
func TestCoverMenuSharesBaseStyleWithDeleteButton(t *testing.T) {
	cssPath := filepath.Join("..", "..", "frontend", "src", "style.css")
	raw, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("读不到 %s: %v", cssPath, err)
	}
	css := string(raw)
	const shared = ".cover-del, .cover-menu, .cover-zoom {"
	if !strings.Contains(css, shared) {
		t.Error("三个封面角标钮(删除/菜单/放大)必须并列共用基础样式(.cover-del, .cover-menu, .cover-zoom { … })")
	}
	if hasOwnBlock(css, ".cover-menu") {
		t.Error("出现了 .cover-menu 的独立样式块 —— 一旦分家,两钮观感迟早漂开;基础样式请写进并列选择器")
	}
	base := cssBlockOf(t, css, shared)
	for _, want := range []string{"width: 27px", "height: 27px", "background:", "box-shadow:", "color:"} {
		if !strings.Contains(base, want) {
			t.Errorf("共享基础样式缺少 %q(尺寸/底色/描边/图标色必须一起定义):\n%s", want, base)
		}
	}
	// 共享段里不许有 position:菜单钮的定位必须留在 wrapper 上,否则又被 .el-dropdown 接管
	if strings.Contains(base, "position:") {
		t.Errorf("共享基础样式里不该出现 position —— 菜单钮会跟着被 .el-dropdown 接管:\n%s", base)
	}
	// 放大钮自己的块只做定位:再抄一遍尺寸/底色就等于配方分家(这正是上一轮被骂的写法)
	if !hasOwnBlock(css, ".cover-zoom") {
		t.Fatal("放大钮需要自己的定位块(.cover-zoom)—— 共享段不带 position")
	}
	zoom := cssBlockOf(t, css, "\n.cover-zoom {")
	for _, forbid := range []string{"width:", "height:", "background:", "box-shadow:", "color:", "border-radius:"} {
		if strings.Contains(zoom, forbid) {
			t.Errorf("放大钮的独立块里出现了 %q —— 观感必须只来自共享段,别手搓第二套配方:\n%s", forbid, zoom)
		}
	}
}

// hasOwnBlock 判断某个类是否**另开了独立样式块**(行首选择器,后跟 `{`)。
// 并列选择器(如 `.cover-del, .cover-menu, .cover-zoom {`)不以行首 `.cover-menu {` 出现,故不算 ——
// 这两条断言要区分的是"共用配方"与"自己另来一套",不是"提到过类名"。
func hasOwnBlock(css, sel string) bool {
	for _, line := range strings.Split(css, "\n") {
		s := strings.TrimSpace(line)
		if s == sel+" {" || s == sel+"{" {
			return true
		}
	}
	return false
}

// cssBlockOf 取某个选择器后面的第一对花括号内容(够用于本文件的两条断言)。
func cssBlockOf(t *testing.T, css, selector string) string {
	t.Helper()
	i := strings.Index(css, selector)
	if i < 0 {
		t.Fatalf("样式里找不到 %s", selector)
	}
	open := strings.Index(css[i:], "{")
	if open < 0 {
		t.Fatalf("%s 后面没有 {", selector)
	}
	rest := css[i+open+1:]
	end := strings.Index(rest, "}")
	if end < 0 {
		t.Fatalf("%s 的样式块没有收尾", selector)
	}
	return rest[:end]
}

// TestNovelsMenuHasScriptEntry 小说库「脚本创作」入口**必须在**（2026-09-27 晚按用户要求回归）。
//
// 沿革:2026-09-26 新增它（技能端已直出脚本 → 平台解析到就导包，省掉 LLM 拆集）；
// 2026-09-27 白天按用户要求隐藏（技能侧停了渲染包产出）；**当晚技能侧恢复脚本交付
// （v2.14：素材三卡 ＋ 分镜 markdown）后，用户要求这个功能回归**。
//
// 钉四件事：①菜单里有它、带集数、没有包时禁用；②技能侧把包标废弃时界面要说清"历史包"；
// ③「视频制作」（LLM 直出，正式渲染那条路）必须还在；④页签要能列出并点开技能侧**新交付物**
// （`素材/*.md`、`分镜/*.md`）—— 这正是"功能看起来没了"的那个缺口（原先只认渲染包）。
func TestNovelsMenuHasScriptEntry(t *testing.T) {
	vuePath := filepath.Join("..", "..", "frontend", "src", "views", "NovelsView.vue")
	vue, err := os.ReadFile(vuePath)
	if err != nil {
		t.Fatalf("读不到 %s: %v", vuePath, err)
	}
	// 判据只看**代码**：注释里写着沿革，那里当然也会出现这些名字。
	src := stripVueComments(string(vue))
	for _, want := range []string{
		`command="script"`, "脚本创作", "function produceScript(", "api.novelProduceScript(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("「脚本创作」入口应已回归，却缺少 %q", want)
		}
	}
	// 入口的可用条件是"**有脚本产物**"—— 2026-09-27 晚起首选是结构化明细
	// （`分镜/分镜明细.json`），所以判据从 `!n.script` 变成 `!n.script && !n.detail`：
	// 只写 `!n.script` 会把"只有明细、没有旧包"的书永远禁用掉（而那正是新交付形态）。
	if !strings.Contains(src, `:disabled="!n.script && !n.detail"`) {
		t.Error("入口禁用条件必须同时看明细与旧包（只有明细的书也要能导）")
	}
	// 「历史包」标签只在**没有明细**时出现：有明细时它是新产物，不该背历史的锅
	if !strings.Contains(src, "!n.detail && n.script?.deprecated") {
		t.Error("「历史包」标签应只在没有结构化明细时出现")
	}
	if !strings.Contains(src, "deprecated") || !strings.Contains(src, "历史包") {
		t.Error("技能侧把包标废弃时，界面必须如实提示「导入的是历史包」")
	}
	// 「视频制作」必须还在（它是 LLM 直出那条路，也是现在的正式渲染入口）
	if !strings.Contains(src, `command="produce"`) || !strings.Contains(src, "视频制作") {
		t.Error("「视频制作」入口必须保留")
	}
	// 技能侧新交付物（素材/分镜 markdown）必须在页签里可见、可读
	for _, want := range []string{"deliverables", "openDeliverable(", "novelRead("} {
		if !strings.Contains(src, want) {
			t.Errorf("剧本脚本页签缺少技能侧交付物的展示/阅读接线：%q", want)
		}
	}
}

// TestStoreWiresProduceScript 前端 api 层:路径必须与后端路由同名(写错一个字母就是静默 404)。
func TestStoreWiresProduceScript(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "store.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "/api/novels/produce-script") {
		t.Error("store.js 缺少 produce-script 接口")
	}
	if !strings.Contains(src, "novelProduceScript:") {
		t.Error("store.js 缺少 novelProduceScript 包装")
	}
}
