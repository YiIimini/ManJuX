package comfyengine

import (
	"os"
	"strings"
	"testing"
)

// TestAccelStepsIsSingleImplementation 档位→步数只能有一份实现。
//
// 渲染管线审查(2026-09-26)抓到:步数钳制原先只长在 buildRenderGraph 里,maskgraph
// (局部重绘)是照抄模型链 —— 加速 LoRA 那半收口了、步数这半没有,于是 refine 档在局部重绘里
// 变成"8 步 + 不挂蒸馏 LoRA",修补区与周围画质断层。这条同时钉行为与接线。
func TestAccelStepsIsSingleImplementation(t *testing.T) {
	cases := []struct {
		accel string
		in    int
		want  int
	}{
		{"refine", 8, 20},   // 精修档保底 20:低于它等于白关蒸馏
		{"refine", 24, 24},  // 用户设更高就用更高
		{"quality", 4, 8},   // Acc-8Step 锚定 8 步
		{"express", 100, 4}, // 极速档压到 4
		{"turbo", 8, 8},
		{"", 12, 12}, // 无档位:原样
		{"", 0, 8},   // 未填:兜底 8
	}
	for _, c := range cases {
		if got := accelSteps(c.accel, c.in); got != c.want {
			t.Errorf("accelSteps(%q, %d) = %d,期望 %d", c.accel, c.in, got, c.want)
		}
	}

	// 接线层:两张图都必须调它(照抄式重复实现最容易在这里复发)
	for _, f := range []string{"engine.go", "maskgraph.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		if !strings.Contains(string(src), "accelSteps(") {
			t.Errorf("%s 必须走 accelSteps(档位→步数只有一份实现)", f)
		}
	}
	// 且不许再出现"本地的 8 步兜底"(那就是又抄了一套)
	if src, err := os.ReadFile("maskgraph.go"); err == nil {
		if strings.Contains(string(src), "steps = 8") {
			t.Error("maskgraph.go 里又出现了本地的步数兜底 —— 档位表必须共用")
		}
	}
}

// TestAccelRefineDropsDistillLoRA 精修档。
//
// 用户 2026-09-26「视频渲染不能是电影级的画面吗?现在渲染的真的垃圾」——
// 这条钉的是其中一个"能从界面上操作、却完全不起作用"的坑:
// 项目参数里关掉 Turbo 只放开了步数钳制(stages_render 的 `if rp.Turbo`),
// 而 accelLoraFor **从不看那个开关**,蒸馏 LoRA 照样挂 —— 于是
// "关 Turbo 跑 20 步"实际是"挂着 4 步蒸馏 LoRA 跑 20 步",画质比 8 步还差,
// 而且从界面与日志上都看不出原因。
//
// 精修档因此必须做到两件事:① 一个蒸馏/加速 LoRA 都不挂;② 步数给足,不被 4/8 步锚定值压回。
func TestAccelRefineDropsDistillLoRA(t *testing.T) {
	c := accelSettings()

	ref := baseJob("ref2va")
	ref.Accel = "refine"
	if got := accelLoraFor(ref, c); got != "" {
		t.Errorf("精修档(ref2va)不许挂任何蒸馏/加速 LoRA,实际 %q", got)
	}
	fl := baseJob("t2va")
	fl.Accel = "refine"
	if got := accelLoraFor(fl, c); got != "" {
		t.Errorf("精修档(fl2va)不许挂任何蒸馏/加速 LoRA,实际 %q", got)
	}

	// 图里也必须真的没有 LoRA 节点,且步数按完整采样给足(默认 8 步 → 至少 20)
	ref.Steps = 8
	graph := buildRenderGraph(ref, c, "", []string{"a.png"}, nil, 768, 1344, 124)
	for id, raw := range graph {
		nd, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if nd["class_type"] == "LoraLoaderModelOnly" {
			t.Errorf("精修档的图里不该有 LoRA 节点(节点 %s 挂了 %v)", id, nd["inputs"])
		}
	}
	_, ks := findNode(t, graph, "BasicScheduler")
	steps, _ := ks["steps"].(int)
	if steps < 20 {
		t.Errorf("精修档步数应给足(≥20),实际 %v —— 那等于白关蒸馏", ks["steps"])
	}

	// 对照组:同一份参数走 turbo 档,必须挂 LoRA 且步数仍是 8 —— 不能把别的档带坏
	tb := baseJob("ref2va")
	tb.Accel = "turbo"
	tb.Steps = 8
	if got := accelLoraFor(tb, c); got != c.TurboLoRARef2VA {
		t.Errorf("turbo 档应照旧挂官方 Turbo LoRA,实际 %q", got)
	}
	g2 := buildRenderGraph(tb, c, "", []string{"a.png"}, nil, 768, 1344, 124)
	if _, ks2 := findNode(t, g2, "BasicScheduler"); ks2["steps"] != 8 {
		t.Errorf("turbo 档步数应保持 8,实际 %v", ks2["steps"])
	}
}
