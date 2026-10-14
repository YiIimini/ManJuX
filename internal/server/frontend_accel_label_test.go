package server

import (
	"strings"
	"testing"
)

// frontend_accel_label_test.go 加速档**用户可见文案**不许把引擎行为说错（2026-10-14）。
//
// 【用户原话】「渲染的画面细节好拉啊」—— 排查出来一部分原因就是**界面把档位说错了**：
//
//   - 默认档写着「Turbo 均衡（默认，官方 4 步蒸馏）」，而引擎（`turboRecipeFor`）是
//     **按采样步数配族**：≥8 步挂 8 步族、低于 8 步才挂 4 步族；平台默认步数就是 **8**
//     ⇒ 默认档实际挂的是 **8 步族**。"官方 4 步蒸馏"这句话让人按 4 步去推理画质，结论必然错。
//   - 质量档写着「Acc-8Step，成片推荐」，而引擎在**量化底模**（本机就是 int8 底座）上挂不上
//     Acc-8Step，会**静默回退成默认档**（运行时有 `AccelTierNote` 播报）—— 界面上那句"成片推荐"
//     读起来像是"选它就更清晰"，实际与默认档一模一样。
//
// 【这条门钉什么、不钉什么（说清楚免得被当成万能的）】这里只钉**界面文案**：它必须与引擎口径一致地
// 说出"按步数配族"与"质量档会回退"这两件事，且旧的两句误导文案**不许回退**。
// "到底挂了哪一族、跑几步"是**行为**，由 `internal/engine/comfyengine` 的
// `turbo_anchor_test.go` / `accel_tier_note_test.go` 钉（那边用文件存在性真的跑选路）。
// 两处一起才关得死：这边防"界面又写错"，那边防"选路又跑偏"。
func TestAccelTierLabelsMatchEngineBehaviour(t *testing.T) {
	all := frontendSources(t)
	drawer, ok := all["components/ParamsDrawer.vue"]
	if !ok {
		t.Fatal("扫不到 components/ParamsDrawer.vue —— 闸门失效（路径或文件名变了？）")
	}

	// ① 默认档：必须说清"按步数配族"，且**不许**再说成固定 4 步蒸馏。
	if strings.Contains(drawer, "官方 4 步蒸馏") {
		t.Error("默认档文案又写成「官方 4 步蒸馏」了 —— 引擎是按步数配族（tab: turboRecipeFor），" +
			"平台默认 8 步挂的是 **8 步族**；这么写会让用户按 4 步去推理画质，结论必然错")
	}
	for _, want := range []string{"按步数自动配族", "8 步"} {
		if !strings.Contains(drawer, want) {
			t.Errorf("默认档文案里缺 %q —— 必须说清「≥8 步挂 8 步族、低于 8 步挂 4 步族」", want)
		}
	}

	// ② 质量档：必须点出"量化底座上会回退"（与运行时 `AccelTierNote` 的口径一致）。
	q := optionLine(drawer, `value="quality"`)
	if q == "" {
		t.Fatal("找不到 quality 那一行的选项文案 —— 闸门失效（选项被改名/挪走了？）")
	}
	for _, want := range []string{"非剪枝", "回退"} {
		if !strings.Contains(q, want) {
			t.Errorf("质量档文案里缺 %q：%s —— 量化底座（本机 int8）上它**挂不上 Acc-8Step、会回退成默认档**，"+
				"不写清楚就等于把「选它更清晰」的错觉留在界面上", want, q)
		}
	}
	if strings.Contains(q, "成片推荐") {
		t.Errorf("质量档不许再无条件下写「成片推荐」：本机量化底座上它与默认档出的图完全一样。%s", q)
	}

	// ③ 步数那一栏：必须点出"步数与所挂 LoRA 成对"（旧文案把 turbo 4-8 与基线 20 混在一句里）。
	if !strings.Contains(drawer, "与所挂蒸馏 LoRA 成对") {
		t.Error("采样步数那一栏要写明「步数与所挂蒸馏 LoRA 成对」—— 混着配会偏离训练锚点" +
			"（引擎侧 expressRecipe/turboRecipeFor 的注释与测试都是这条口径）")
	}
	if strings.Contains(drawer, `label="采样步数(turbo 4-8 / 基线 20)"`) {
		t.Error("旧的步数文案回退了（它把 turbo 的 4-8 与精修的基线 20 混成一句，读起来像是同一个档位的事）")
	}

	// ④ LoRA 目录说明：必须提到两族 + 按步数自动换族（用户翻到这一页时看得懂"为什么有两份权重"）。
	comfy, ok := all["views/ComfyView.vue"]
	if !ok {
		t.Fatal("扫不到 views/ComfyView.vue —— 闸门失效")
	}
	for _, want := range []string{"8 步族", "4 步族"} {
		if !strings.Contains(comfy, want) {
			t.Errorf("ComfyView 的 LoRA 目录说明里缺 %q —— 盘上有两族权重，页面上只说 4 步会让人以为另一份是多余的", want)
		}
	}
	if strings.Contains(comfy, "Turbo LoRA(4 步出图加速补丁)") {
		t.Error("ComfyView 又只写「4 步出图加速补丁」了 —— 引擎按步数在 4/8 两族之间自动切换")
	}
}

// optionLine 取某一行 el-option 的文案（含该行的全部文本）。找不到返回空串。
func optionLine(src, marker string) string {
	i := strings.Index(src, marker)
	if i < 0 {
		return ""
	}
	line := src[i:]
	if k := strings.IndexByte(line, '\n'); k >= 0 {
		line = line[:k]
	}
	return strings.TrimSpace(line)
}
