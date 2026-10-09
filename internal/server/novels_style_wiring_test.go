package server

import (
	"os"
	"strings"
	"testing"
)

// readPkgFile 读本包目录下的源文件（接线门用；测试的工作目录就是包目录）。
func readPkgFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", name, err)
	}
	return string(b)
}

// TestFrontendNovelProduceWiresStyle 接线门：**"选了风格"这条链必须真的接上**（2026-10-09）。
//
// 为什么单独一道：这一轮的缺陷形态不是"函数写错"，而是**入口缺一条线**
// （导入制作没有选择入口 / 选择只当提示词、被 enforceStyleLock 覆写回去）。
// 只有源码级接线门才咬得住"界面加了输入框、但请求体里没这个字段"这类断链
// （vite build 与 go test 都不会报错）。
func TestFrontendNovelProduceWiresStyle(t *testing.T) {
	store := readFrontend(t, "src/store.js")
	view := readFrontend(t, "src/views/NovelsView.vue")

	// ① store.js：请求体必须带 styles 与 style_hint（少一个字段 = 后端永远收到空选择）
	for _, want := range []string{
		"novelProduce: (path, max, styles, styleHint)",
		"styles: styles || []",
		"style_hint: styleHint || ''",
	} {
		if !strings.Contains(store, want) {
			t.Errorf("store.js 的 novelProduce 少了 %q —— 界面上选了也不会传出去", want)
		}
	}

	// ② NovelsView：必须**先弹风格选择框**再开跑（原来是一个 ElMessageBox 直接开跑）
	for _, want := range []string{
		"const produceDlg = ref(null)",
		"const producePicks = ref([])",
		"const produceHint = ref('')",
		"function openProduce(n)",
		"async function confirmProduce()",
		"function toggleProduceStyle(key)",
		"const styleOptions = computed(() => G.metaStyles || [])",
		"api.novelProduce(n.path, 0, styles || [], styleHint || '')",
		"@click=\"confirmProduce\"",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("NovelsView.vue 少了 %q —— 风格选择框没接上（或风格传不出去）", want)
		}
	}
	// 两个入口（卡片菜单 / 详情弹窗按钮）都必须走 openProduce，不许直接开跑
	if strings.Contains(view, "if (cmd === 'produce') return produce(n)") {
		t.Error("卡片菜单的「视频制作」还在直接开跑：风格选择框被跳过了")
	}
	if strings.Contains(view, "@click=\"dlg && produce(dlg.novel)\"") {
		t.Error("详情弹窗的「一键视频创作」还在直接开跑：风格选择框被跳过了")
	}
	// 风格清单以后端为单一真源：前端不许再抄一份预设表（两份必然漂移）
	if strings.Contains(view, "'guoman_2d':") || strings.Contains(view, "guofeng_cinematic:") {
		t.Error("NovelsView 里出现了硬编码的预设表：清单必须来自 G.metaStyles（后端 /api/meta 是单一真源）")
	}
}

// TestBackendStyleChoiceLandsInProject 选择必须落到两处，否则"选了没用"：
//
//	① 每集项目的参数（Script.Styles/StyleHint）—— 项目参数抽屉看得见、重跑分镜 hint 一致；
//	② 项目参数里改风格时，平台自有的「全局风格:」行要跟着走（否则 enforceStyleLock 用旧串覆写）。
func TestBackendStyleChoiceLandsInProject(t *testing.T) {
	produce := readPkgFile(t, "novels_produce.go")
	split := readPkgFile(t, "novels_split.go")
	handlers := readPkgFile(t, "handlers_project.go")

	for _, want := range []string{
		"Styles    []string `json:\"styles\"`",
		"StyleHint string   `json:\"style_hint\"`",
		"style := resolveImportStyle(artStyle, in.Styles, in.StyleHint)",
		"s.splitNovelToProjects(splitCtx, name, text, maxEp, style, sheet,",
	} {
		if !strings.Contains(produce, want) {
			t.Errorf("novels_produce.go 少了 %q —— 导入时的风格选择没进裁决/没传给拆集", want)
		}
	}
	for _, want := range []string{
		"cfg.Params.Script.Styles = style.Styles",
		"cfg.Params.Script.StyleHint = style.Hint",
		"if style.From == \"user\"",
	} {
		if !strings.Contains(split, want) {
			t.Errorf("novels_split.go 少了 %q —— 选择没写进每集项目参数 / 播报没分来源", want)
		}
	}
	if !strings.Contains(handlers, "s.syncGlobalStyleFromParams(id, in)") {
		t.Error("handlers_project.go 的 putParams 没调 syncGlobalStyleFromParams：项目参数里改了风格会被旧串覆写回去")
	}
	// 未知 key 必须如实点名（不许静默吞掉用户勾的那一项）
	if !strings.Contains(produce, "style.Unknown") {
		t.Error("novels_produce.go 没有对未知风格 key 做如实点名")
	}
}
