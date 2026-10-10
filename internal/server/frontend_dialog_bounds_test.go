// frontend_dialog_bounds_test.go 所有弹窗不许超出父级窗口（2026-10-10 用户：
// 「小说封面重制弹窗窗口固定大小，别超过父级窗口，所有的弹窗都需要这样要求」）。
//
// 判据钉在**全局样式**上（元素级 `.el-dialog` / `.el-drawer`），不是逐个弹窗加类名：
// 新增弹窗不需要谁"记得"加类名，也一样被夹住 —— 「所有人都记得」这种约定，漏一次就是用户窗口被顶爆一次
// （用户点名的那个封面重制弹窗就是固定 `width="880px"`，小窗口上直接顶出去）。
//
// 只夹宽度是不够的：**高度也要夹 + 内容在弹窗内部滚**，否则夹住高度会把内容静默裁掉（用户看不到按钮）。
package server

import (
	"strings"
	"testing"
)

// cssRuleBody 取某个选择器的声明块（从选择器后的第一个 `{` 到配对的 `}`）。
// 找不到返回空串 —— 判据会红，不会静默通过。
func cssRuleBody(css, selector string) string {
	i := strings.Index(css, selector)
	if i < 0 {
		return ""
	}
	open := strings.Index(css[i:], "{")
	if open < 0 {
		return ""
	}
	rest := css[i+open+1:]
	if end := strings.Index(rest, "}"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// clampEnd 切片上界不许越过长度（夹具短/长都不会 panic）。
func clampEnd(want, n int) int {
	if want > n {
		return n
	}
	return want
}

func TestAllDialogsAreBoundedByParentWindow(t *testing.T) {
	css := readFrontend(t, "src/style.css")

	// ① 全局：对话框宽高都被夹在视口内（各留余量），而不是"某个弹窗刚好没超"
	dialog := cssRuleBody(css, ".el-dialog {")
	for _, want := range []string{"max-width: calc(100vw", "max-height: calc(100vh"} {
		if !strings.Contains(dialog, want) {
			t.Errorf("全局 .el-dialog 缺少「不许超出父级窗口」的 %q（实际块：%q）—— "+
				"固定宽度的弹窗（封面重制 880px）在小窗口上会顶出去", want, dialog)
		}
	}
	// ② 高度夹住之后内容必须能在**弹窗内部滚**（否则按钮被裁掉，比顶出去更难查）
	if body := cssRuleBody(css, ".el-dialog__body {"); !strings.Contains(body, "overflow: auto") {
		t.Errorf("弹窗主体必须自己滚（%q）：夹住高度却不给滚动，内容会被静默裁掉", body)
	}
	if !strings.Contains(dialog, "overflow: hidden") || !strings.Contains(dialog, "flex-direction: column") {
		t.Errorf("弹窗要按「头/脚固定 + 主体滚」布局（实际：%q）", dialog)
	}
	// ③ 抽屉（从右侧拉出来的那一层）同样不许顶出窗口
	drawer := cssRuleBody(css, ".el-drawer {")
	if !strings.Contains(drawer, "max-width: calc(100vw") || !strings.Contains(drawer, "max-height: calc(100vh") {
		t.Errorf("抽屉也要夹在父级窗口内（实际：%q）", drawer)
	}
	// ④ 反面针：既有那条「日志窗」的夹法不许被删（它是同一条口径的既有实现）
	logPop := cssRuleBody(css, ".log-pop {")
	if !strings.Contains(logPop, "max-width: calc(100vw") {
		t.Errorf("日志窗的夹法（已有的同口径实现）不许回退：%q", logPop)
	}
}

// TestCoverRedoDialogKeepsFixedSize：用户要的是「**固定大小**，但不超过父级窗口」——
// 所以那条弹窗**保留定宽**（不是改成自适应），夹住由全局规则负责。
func TestCoverRedoDialogKeepsFixedSize(t *testing.T) {
	vue := readFrontend(t, "src/views/NovelsView.vue")
	i := strings.Index(vue, `v-model="covOpen"`)
	if i < 0 {
		t.Fatal("找不到封面重做弹窗（v-model=\"covOpen\"）")
	}
	seg := vue[i:clampEnd(i+400, len(vue))]
	if !strings.Contains(seg, `width="`) {
		t.Errorf("封面重做弹窗应保留**定宽**（用户口径：固定大小、不超父级），实际片段：%q", seg)
	}
}
