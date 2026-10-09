package server

import (
	"strings"
	"testing"
)

// style_medium_wiring_test.go 「多风格组合」的**写法与互斥提示**这条链接上了没有（2026-10-09）。
//
// 用户原话：「H3 不是可以多风格组合吗？」—— 查官方原文后：组合允许（官方风格句就是并列：
// `Live-action, cinematic`），但平台自己的规则要求风格串**不含互斥词组**。
// 判定要有、要说清、要在**用户勾选的那一刻**看得见（只写日志/只写在后端 = 等于没做）。
func TestStyleMediumWiring(t *testing.T) {
	settings := readPkgFile(t, "handlers_settings.go")
	produce := readPkgFile(t, "novels_produce.go")
	view := readFrontend(t, "src/views/NovelsView.vue")
	drawer := readFrontend(t, "src/components/ParamsDrawer.vue")

	// ① /api/meta 必须下发介质族与其中文名（界面据此判定与显示；中文名只此一份，别在 JS 再抄表）
	for _, want := range []string{
		`json:"medium"`,
		`json:"medium_label"`,
		"pipeline.MediumLabel(",
	} {
		if !strings.Contains(settings, want) {
			t.Errorf("handlers_settings.go 少了 %q —— 界面拿不到介质族，互斥判定就无从谈起", want)
		}
	}
	// ② 导入时的矛盾组合必须**如实点名**（照用户选的做，但不许沉默）。
	// 断言**条件整行**而不是 `style.Conflicts` 这个标识符：标识符在（被 `if false` 罩住的）死代码里
	// 也会出现 —— 那正是"注释掉调用也绿"的摆设守卫（反向验证当场咬出）。
	if !strings.Contains(produce, "if len(style.Conflicts) > 0 {") {
		t.Error("novels_produce.go 里没有生效的矛盾播报分支（`if len(style.Conflicts) > 0 {`）")
	}
	// ③ 两处界面都要在勾选那一刻提示。
	// 断言**渲染出来的那一行**（带 `<b>` 与 `{{ }}`），不是"文件里出现过这句中文" ——
	// 注释里也有这句中文，断言"出现过"是摆设守卫（反向验证当场咬出）。
	const rendered = "⚠ <b>介质互相矛盾</b>：{{ styleConflict }}"
	for _, f := range []struct{ name, src string }{
		{"NovelsView.vue", view},
		{"ParamsDrawer.vue", drawer},
	} {
		if !strings.Contains(f.src, "styleConflict") || !strings.Contains(f.src, "medium_label") {
			t.Errorf("%s 里没有 styleConflict/medium_label —— 勾了互斥组合当场看不到提示", f.name)
		}
		if !strings.Contains(f.src, rendered) {
			t.Errorf("%s 缺少渲染出来的互斥提示行：%q", f.name, rendered)
		}
	}
}
