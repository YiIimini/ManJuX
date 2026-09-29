package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 资产详情弹窗**只能有一份**，且资产库与工作台共用（2026-09-29 用户：
// 「工作台里的资产封面同理，详细弹窗不用单独做啊（没有适配），直接资产库里的详情弹窗拿来用啊」）。
//
// 起因：我先在工作台另写了一份"只读呈现"，样式与行为跟资产库那份不一样 —— 用户一眼看出没适配。
// 判据因此写成结构性的：**只有 LibAssetDialog.vue 这一个详情弹窗组件**，
// 两个视图都引用它，且旧的独立组件必须已经删掉（留着就还会有人接错）。
func TestAssetDetailDialogIsSharedBetweenLibraryAndWorkbench(t *testing.T) {
	front := filepath.Join("..", "..", "frontend", "src")
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(front, rel))
		if err != nil {
			t.Fatalf("读 %s 失败：%v", rel, err)
		}
		return string(raw)
	}
	if _, err := os.Stat(filepath.Join(front, "components", "AssetDetailDialog.vue")); err == nil {
		t.Error("工作台那份单独的详情弹窗还在（AssetDetailDialog.vue）—— 两份必然漂移，用户已经报过「没有适配」")
	}
	dlg := read(filepath.Join("components", "LibAssetDialog.vue"))
	lib := read(filepath.Join("views", "LibraryView.vue"))
	wb := read(filepath.Join("views", "ProjectView.vue"))

	// ① 共用：两个视图都渲染同一个组件
	for _, v := range []struct{ name, src string }{{"资产库", lib}, {"工作台", wb}} {
		if !strings.Contains(v.src, "<LibAssetDialog") {
			t.Errorf("%s没有渲染 LibAssetDialog —— 详情弹窗必须是同一份", v.name)
		}
	}
	// ② 那一份要带齐两边的能力：库侧运营操作 + 工作台的"这条不在库里"兜底
	for _, want := range []struct{ needle, why string }{
		{"api.libraryDetail", "要读库条目（图片墙来自条目的 images）"},
		{"api.libraryRegen", "抽卡"},
		{"api.libraryViews", "补全多视角"},
		{"api.libraryDescribe", "AI 生成描述/提示词"},
		{"api.librarySetMeta", "保存描述/提示词"},
		{"api.libraryDelete", "删除"},
		{"api.fileURL", "库里没有这条时退回项目内的图（工作台手动上传的资产）"},
		{"这条资产不在资产库里", "库里没有时要如实说明，而不是给一排点了报错的按钮"},
	} {
		if !strings.Contains(dlg, want.needle) {
			t.Errorf("共用的详情弹窗缺少 %q —— %s", want.needle, want.why)
		}
	}

	// ③ 封面角标：分类名 + 模型名放在封面上，不再和资产名挤一行
	css := read(filepath.Join("..", "..", "frontend", "src", "style.css"))
	_ = css
	if !strings.Contains(read(filepath.Join("style.css")), ".cov-badge") {
		t.Error("style.css 缺少 .cov-badge —— 角标样式必须是共用的一份（两个视图各写一套就又是两份）")
	}
	if !strings.Contains(lib, "cov-badge") || !strings.Contains(wb, "cov-badge") {
		t.Error("资产库与工作台的封面都要有角标（分类 + 模型名）")
	}
	if n := strings.Count(wb, `class="cov-badge tl"`); n != 3 {
		t.Errorf("工作台三类资产（角色/场景/道具）的封面都要有分类角标，实得 %d 处", n)
	}
	for _, w := range []struct{ needle, why string }{
		{"sourceLabel(row.c.asset?.source)", "角色封面要标出模型/来源"},
		{"sourceLabel(s.asset?.source)", "场景封面要标出模型/来源"},
		{"sourceLabel(p.asset?.source)", "道具封面要标出模型/来源"},
	} {
		if !strings.Contains(wb, w.needle) {
			t.Errorf("工作台缺少 %q —— %s", w.needle, w.why)
		}
	}
	// 名字那一行不该再堆来源标签（那正是"挤一块"）
	if strings.Contains(wb, `库复用</el-tag>`) {
		t.Error("资产名那一行还留着来源标签 —— 用户要的是把它挪到封面上")
	}
}
