package comfyengine

import (
	"testing"
)

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
