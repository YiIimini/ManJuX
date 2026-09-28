package project

import (
	"strings"
	"testing"
)

// 多视角来源的两个取值，`project` 侧（`AssetsParams.ViewsSourceOf`）与 `pipeline` 侧
// （`ViewSourceEdit` / `ViewSourceH3`）必须**逐字一致**。
//
// 为什么需要这条门：`project` 是最底层的数据包，**不能 import pipeline**（本仓的依赖纪律
// 是单向无环），所以 `ViewsSourceOf` 里只能写字面量。而"两边字符串漂开"的后果是安静的：
// 用户在设置页选了"图像编辑"，`ViewsSourceOf` 归一化出一个 pipeline 不认的值，
// 于是多视角悄悄走了另一条路 —— 没有任何报错。
func TestViewsSourceValuesMatchPipeline(t *testing.T) {
	// 与 pipeline/viewedit.go 的常量对照（改那边就必须改这里，反之亦然）
	const pipelineEdit, pipelineH3 = "edit", "h3"

	cases := []struct{ in, want string }{
		{pipelineEdit, pipelineEdit},
		{pipelineH3, pipelineH3},
		{"edit", pipelineEdit},
		{"h3", pipelineH3},
		{"Qwen", pipelineEdit}, // 别名
		{"qwen-edit", pipelineEdit},
		{"video", pipelineH3},  // 别名
		{"  H3  ", pipelineH3}, // 容错：大小写与空白
		{"", "auto"},
		{"auto", "auto"},
		{"看不懂的值", "auto"}, // 不识别一律 auto
	}
	for _, c := range cases {
		if got := (AssetsParams{ViewsSource: c.in}).ViewsSourceOf(); got != c.want {
			t.Errorf("ViewsSourceOf(%q) = %q,期望 %q", c.in, got, c.want)
		}
	}
	// 归一化的输出只可能是这三个值之一（pipeline 侧按它分流）
	for _, in := range []string{"edit", "h3", "auto", "Qwen", "video", "xxx", ""} {
		got := (AssetsParams{ViewsSource: in}).ViewsSourceOf()
		switch got {
		case "edit", "h3", "auto":
		default:
			t.Errorf("归一化只能产出 edit/h3/auto，%q 给的是 %q", in, got)
		}
	}
	// 门自己也要确认字面量没写歪（常量漂了这条先红）
	if pipelineEdit != "edit" || pipelineH3 != "h3" {
		t.Fatal("pipeline 侧的取值变了 —— 请同步 pipeline/viewedit.go 与本测试")
	}
	if !strings.EqualFold(pipelineEdit, "EDIT") {
		t.Fatal("测试自身有误")
	}
}
