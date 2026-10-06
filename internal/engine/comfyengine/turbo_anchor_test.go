package comfyengine

import (
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// 默认（turbo）档的加速 LoRA 必须与**请求步数**成对（2026-09-30 深度分析）。
//
// 【现场】平台默认 `render.steps = 8`，而 `accelLoraFor` 一直返回配置里那条 **4 步**蒸馏 LoRA
// ⇒ "挂 4 步族跑 8 步"。本仓自己写过这条纪律（engine.go 里 expressRecipe 上方那段：
// "LoRA 与步数必须成对决定……混着配会偏离训练锚点"），却唯独默认档没遵守。
// 而官方推荐的 LoRA 发布源（`lightx2v/Minimax-h3-Turbo`）**同时提供 4 步与 8 步两族** ——
// 盘上那份 8 步权重从下载那天起就没被挂过。
//
// 判据两条：
//
//	① 请求 ≥8 步且盘上有 8 步族 ⇒ 挂 8 步族；请求 <8 步 ⇒ 配置里那条 4 步族；缺文件 ⇒ 退回 4 步族（旧行为）。
//	② **不变式**：平台默认步数下，LoRA 文件名里内嵌的步数必须等于 `accelSteps` 的结论
//	   （三档都要满足：express 3 步 / turbo 8 步 / quality 8 步）。
func TestTurboLoraPairsWithRequestedSteps(t *testing.T) {
	root := t.TempDir()
	const cfg4Ref = "cfg_turbo_ref2v.safetensors"
	const cfg4FL = "cfg_turbo_fl2v.safetensors"
	touchLora(t, root, turbo8StepRef2VA, turbo8StepFL2VA, cfg4Ref, cfg4FL)
	c := config.ComfySettings{
		RootDir:         root,
		TurboLoRARef2VA: cfg4Ref,
		TurboLoRAFL2VA:  cfg4FL,
	}

	jobAt := func(mode string, steps int) engine.Job {
		j := baseJob(mode)
		j.Steps = steps
		j.Accel = "" // 默认档：参数里 accel 留空就是 turbo
		return j
	}

	// ① 平台默认（8 步）⇒ 8 步族，且 accelSteps 也是 8（锚定吻合）
	j8 := jobAt("ref2va", turbo8StepSteps)
	if got := accelLoraFor(j8, c); got != turbo8StepRef2VA {
		t.Errorf("请求 %d 步时应挂 8 步族 %q，实际 %q（挂 4 步族跑 8 步 = 偏离训练锚点）",
			turbo8StepSteps, turbo8StepRef2VA, got)
	}
	if got := accelSteps(j8, c, j8.Steps); got != turbo8StepSteps {
		t.Errorf("请求 %d 步时 accelSteps 应为 %d，实际 %d", turbo8StepSteps, turbo8StepSteps, got)
	}

	// ② 请求 4 步 ⇒ 配置里那条 4 步族（两边锚定吻合，不动）
	j4 := jobAt("ref2va", turbo4StepSteps)
	if got := accelLoraFor(j4, c); got != cfg4Ref {
		t.Errorf("请求 %d 步时应挂配置里那条 4 步族 %q，实际 %q", turbo4StepSteps, cfg4Ref, got)
	}

	// ③ 请求 12 步 ⇒ 仍属 8 步族（≥8 就用 8 步族）；步数本身保持 12（accelSteps 语义未改：
	//    它只负责"档位→步数"，不负责把用户的步数改小，既有测试表就是这么钉的）
	j12 := jobAt("ref2va", 12)
	if got := accelLoraFor(j12, c); got != turbo8StepRef2VA {
		t.Errorf("请求 12 步时应走 8 步族 %q，实际 %q", turbo8StepRef2VA, got)
	}
	if got := accelSteps(j12, c, 12); got != 12 {
		t.Errorf("accelSteps 不该改动用户步数（既有语义），实际 %d", got)
	}

	// ④ 非 ref2va（FL2VA）走另一条 8 步族
	jFL := jobAt("fl2va", turbo8StepSteps)
	if got := accelLoraFor(jFL, c); got != turbo8StepFL2VA {
		t.Errorf("FL2VA 请求 %d 步应挂 %q，实际 %q", turbo8StepSteps, turbo8StepFL2VA, got)
	}

	// ⑤ 盘上没有 8 步族 ⇒ 退回配置里那条（与旧版行为一致），**但要有话说**
	no8 := config.ComfySettings{RootDir: t.TempDir(), TurboLoRARef2VA: cfg4Ref, TurboLoRAFL2VA: cfg4FL}
	if got := accelLoraFor(jobAt("ref2va", turbo8StepSteps), no8); got != cfg4Ref {
		t.Errorf("没有 8 步族时应退回配置里那条 %q，实际 %q", cfg4Ref, got)
	}
	if note := AccelTierNote("", "ref2va", turbo8StepSteps, no8); note == "" {
		t.Error("缺 8 步族却挂 4 步族跑 8 步时必须有播报 —— 否则用户只看到\"成品不理想\"，查不出原因")
	}

	// ⑥ 不变式：**平台默认步数**下，LoRA 名里的步数 == accelSteps 结论（三档都要成立）
	qroot := t.TempDir()
	touchLora(t, qroot, accRef2VA, accFL2VA, expressTaoMateLoRA, turbo8StepRef2VA, cfg4Ref)
	withAll := config.ComfySettings{
		RootDir:         qroot,
		UNetRef2VA:      "MiniMax-H3-Ref2VA-fp16.safetensors", // 非量化 ⇒ quality 档能真挂上
		TurboLoRARef2VA: cfg4Ref,
	}
	for _, accel := range []string{"", "turbo", "express", "quality"} {
		job := baseJob("ref2va")
		job.Accel = accel
		job.Steps = turbo8StepSteps
		lora := accelLoraFor(job, withAll)
		got := accelSteps(job, withAll, turbo8StepSteps)
		if lora == "" {
			t.Errorf("档位 %q：默认步数下必须挂一条加速 LoRA（回退也不许不挂）", accel)
			continue
		}
		if want, ok := loraStepTag(lora); ok && want != got {
			t.Errorf("档位 %q：挂的是 %q（锚定 %d 步）却按 %d 步跑 —— 步数与 LoRA 必须成对",
				accel, lora, want, got)
		}
	}
}

// loraStepTag 从 LoRA 文件名里取内嵌的锚定步数（`…_4step_…` / `…-8Step-…` / `…8step…`）。
// 名字里没有步数标记时返回 ok=false（那种 LoRA 不宣称锚点，判据无从可判）。
func loraStepTag(name string) (int, bool) {
	m := regexp.MustCompile(`([0-9]+)\s*[Ss]tep`).FindStringSubmatch(filepath.Base(name))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// loraStepTag 的自检：判据函数本身写错会让上面那条门永远绿（本项目记过"判据写歪"的亏）。
func TestLoraStepTagParsesRealNames(t *testing.T) {
	cases := []struct {
		name string
		want int
		ok   bool
	}{
		{"minimax_h3_ref2v_turbo_4step_v0.1_comfyui_bf16.safetensors", 4, true},
		{"minimax_h3_ref2v_turbo_8step_v1.0_768p_comfyui_bf16.safetensors", 8, true},
		{"MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy.safetensors", 8, true},
		{"TaoMate-H3-3step-ComfyUI.safetensors", 3, true},
		{"some_lora_without_step_tag.safetensors", 0, false},
	}
	for _, tc := range cases {
		got, ok := loraStepTag(tc.name)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("loraStepTag(%q) = (%d,%v)，期望 (%d,%v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}
