package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	btn := cssBlockOf(t, css, ".cover-menu {")
	if strings.Contains(btn, "position:") {
		t.Errorf(".cover-menu 自己不许再定位 —— 这次就是栽在这里(相对 .el-dropdown 的 0 宽盒子):\n%s", btn)
	}
}

// TestCoverMenuSharesBaseStyleWithDeleteButton 菜单钮必须**沿用删除钮那套观感**。
//
// 2026-09-26 用户两次点我:先是我给菜单钮另写了一套"改进配方"(全站共用的删除钮也被我
// 一起改了外观),他说「原来的删除按钮更换这个菜单按钮,调用函数更换成对的不就行了」——
// 需求只是把删除钮换成菜单钮,不是重做控件。这条把"同源"钉住:
// 基础样式写成 `.cover-del, .cover-menu { … }` 并列选择器、不许给 .cover-menu 另开样式块。
func TestCoverMenuSharesBaseStyleWithDeleteButton(t *testing.T) {
	cssPath := filepath.Join("..", "..", "frontend", "src", "style.css")
	raw, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("读不到 %s: %v", cssPath, err)
	}
	css := string(raw)
	if !strings.Contains(css, ".cover-del, .cover-menu {") {
		t.Error("菜单钮必须与删除钮并列共用基础样式(.cover-del, .cover-menu { … })")
	}
	if strings.Contains(css, "\n.cover-menu {") {
		t.Error("出现了 .cover-menu 的独立样式块 —— 一旦分家,两钮观感迟早漂开;基础样式请写进并列选择器")
	}
	base := cssBlockOf(t, css, ".cover-del, .cover-menu {")
	for _, want := range []string{"width: 27px", "height: 27px", "background:", "box-shadow:", "color:"} {
		if !strings.Contains(base, want) {
			t.Errorf("共享基础样式缺少 %q(尺寸/底色/描边/图标色必须一起定义):\n%s", want, base)
		}
	}
	// 共享段里不许有 position:菜单钮的定位必须留在 wrapper 上,否则又被 .el-dropdown 接管
	if strings.Contains(base, "position:") {
		t.Errorf("共享基础样式里不该出现 position —— 菜单钮会跟着被 .el-dropdown 接管:\n%s", base)
	}
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

// TestNovelsCoverMenuHasScriptEntry 小说库「脚本创作」入口(2026-09-26 用户要求新增)。
//
// 这个入口的意义是**省掉 LLM 拆集**:技能端已直出脚本,平台解析到就导包。
// 钉三件事:菜单里有它、只在有脚本包时可用(**没有包就不给点**,避免用户以为能用)、
// 走的是专用接口而不是复用它旁边的「视频制作」(后者是 LLM 那条路)。
func TestNovelsCoverMenuHasScriptEntry(t *testing.T) {
	vuePath := filepath.Join("..", "..", "frontend", "src", "views", "NovelsView.vue")
	vue, err := os.ReadFile(vuePath)
	if err != nil {
		t.Fatalf("读不到 %s: %v", vuePath, err)
	}
	src := string(vue)
	for _, want := range []string{
		`command="script"`,        // 菜单项
		"脚本创作",                    // 文案
		`:disabled="!n.script"`,   // 没有包就不给点
		"api.novelProduceScript(", // 走专用接口
		"function produceScript(", // 命令出口
		"n.script.episodes",       // 角标/菜单显示集数
	} {
		if !strings.Contains(src, want) {
			t.Errorf("脚本创作入口缺少 %q", want)
		}
	}
	// 「视频制作」必须还在(它是 LLM 那条路,两个入口并存,不是替换)
	if !strings.Contains(src, `command="produce"`) || !strings.Contains(src, "视频制作") {
		t.Error("「视频制作」入口不该被脚本创作取代")
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
