package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 工作台「资产」页的两条（2026-09-29 用户：
// 「工作台中的资产里的卡片文字描述精简，目前内容超出异常排版，点击图片直接弹窗详情页里显示，
// 可以直接使用资产库里的详细弹窗页面」＋「新的弹窗的窗口层级是最高的」）。
//
// 判据分三件事，缺一条用户就会说"还是乱的"：
//
//	① 卡片上的描述**必须截断**（角色卡描述几百字，整段铺在卡上会把网格撑乱）；
//	② **点图开详情弹窗**（三种资产都要接上），弹窗取库条目的**全部图片**（多视角/三视图板）；
//	③ 弹窗必须**压得住日志悬浮窗** —— Element 弹层计数器从 2000 起，而日志窗原来是 2600，
//	   于是用户点开详情只看到日志窗盖在上面（这是"层级最高"那条的真身）。
func TestWorkbenchAssetCardsAreTrimmedAndOpenDetail(t *testing.T) {
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("读 %s 失败：%v", rel, err)
		}
		return string(raw)
	}
	pv := read(filepath.Join("frontend", "src", "views", "ProjectView.vue"))
	dlg := read(filepath.Join("frontend", "src", "components", "AssetDetailDialog.vue"))
	css := read(filepath.Join("frontend", "src", "style.css"))

	// ① 描述截断：类名 + 两行 clamp 都要在
	for _, want := range []struct{ needle, why string }{
		{"asset-desc", "卡片描述要有统一的截断类（不能整段铺在卡上）"},
		{"-webkit-line-clamp: 2", "截断到两行（描述动辄几百字，会把网格撑乱）"},
	} {
		if !strings.Contains(pv, want.needle) {
			t.Errorf("ProjectView.vue 缺少 %q —— %s", want.needle, want.why)
		}
	}
	if n := strings.Count(pv, `class="text-xs asset-desc"`); n != 3 {
		t.Errorf("三类资产（角色/场景/道具）的卡片描述都要截断，实得 %d 处", n)
	}

	// ② 点图开详情：三类资产都要接上，且弹窗组件要真被渲染
	for _, kind := range []string{"'characters'", "'scenes'", "'props'"} {
		if !strings.Contains(pv, "openAssetDetail("+kind) {
			t.Errorf("资产 %s 的卡片图没有接上详情弹窗（点图还是只会触发上传）", kind)
		}
	}
	if !strings.Contains(pv, "<AssetDetailDialog") {
		t.Error("AssetDetailDialog 组件没有渲染进模板 —— 接了函数也弹不出来")
	}
	// 弹窗要取**库条目的全部图片**（多视角/三视图板都是库条目里的 images），
	// 并且库查不到时能退回项目内那张图（否则手动上传的资产点开是空的）
	for _, want := range []struct{ needle, why string }{
		{"api.libraryDetail", "详情要读库条目（图片墙来自条目的 images）"},
		{"lib.value?.images", "图片墙要用条目的 images（只显示主图=多视角图看不到）"},
		{"api.fileURL", "库里没有这条时要退回项目内的图"},
	} {
		if !strings.Contains(dlg, want.needle) {
			t.Errorf("AssetDetailDialog.vue 缺少 %q —— %s", want.needle, want.why)
		}
	}

	// ③ 层级：日志悬浮窗必须在 Element 弹层（从 2000 起）之下
	block := regexp.MustCompile(`(?s)\.log-pop\s*\{(.*?)\}`).FindStringSubmatch(css)
	if block == nil {
		t.Fatal("style.css 里找不到 .log-pop 规则（层级判据失效）")
	}
	zi := regexp.MustCompile(`z-index:\s*(\d+)`).FindStringSubmatch(block[1])
	if zi == nil {
		t.Fatal(".log-pop 没有 z-index —— 它会跟着 DOM 顺序乱飘")
	}
	n, _ := strconv.Atoi(zi[1])
	if n >= 2000 {
		t.Errorf("日志悬浮窗 z-index=%d，而 Element 弹层从 2000 起 ⇒ 它会盖住所有弹窗"+
			"（用户报的「新的弹窗的窗口层级是最高的」就是这条）；应降到 2000 以下", n)
	}
}
