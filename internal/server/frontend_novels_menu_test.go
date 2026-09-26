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
