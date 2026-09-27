package comfyengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// accel_tier_note_test.go 「画质档静默回退」必须**可见**（ComfyUI 侧整改单 2026-09-27 · P0-2）。
//
// 背景：本机 4 个 H3 底模全是 int8/w4a8，而 quality 档要的 Acc-8Step 与量化底模不兼容
// （tensor 32 vs 1024）⇒ 用户选 quality **必然**回退 turbo，而回退是**静默**的：
// 界面上写着"画质档"，出来的图与默认档一模一样，事后只能翻代码。
//
// 这条口径只加**可见性**：回退门禁与既有测试（TestAccelQualityLoraGuard /
// …NoAccFile，见 accel_lora_test.go）原样保留 —— 这里断言的是"说得出为什么"。
func TestAccelTierNote(t *testing.T) {
	// 造一个"盘上真有 Acc-8Step"的假根目录：这样才能把**底模不兼容**那条分支单独逼出来
	// （不这么做，两个原因会混在一起说成"盘上没有"，测不出真正的分支）。
	root := t.TempDir()
	accDir := filepath.Join(root, "models", "loras")
	if err := os.MkdirAll(accDir, 0o755); err != nil {
		t.Fatal(err)
	}
	accFile := "MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy.safetensors"
	if err := os.WriteFile(filepath.Join(accDir, accFile), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	quant := config.ComfySettings{
		RootDir:    root,
		UNetRef2VA: "Minimax-h3_Singularity_ref2va_Pruned_v1.3_int8.safetensors",
	}
	// ① 量化底模 + quality ⇒ 有话说，并且说清"没生效 + 为什么 + 出图与默认档一样"
	note := AccelTierNote("quality", "ref2va", quant)
	if note == "" {
		t.Fatal("量化底模下选 quality 必须给出一行说明（否则用户以为画质档生效了）")
	}
	for _, want := range []string{"没有生效", "int8", "非剪枝"} {
		if !strings.Contains(note, want) {
			t.Errorf("说明里缺 %q：%s", want, note)
		}
	}
	// ② 其它档位不打扰（默认档/精修档/抽卡档都没有"回退"这回事）
	for _, accel := range []string{"turbo", "refine", "express", ""} {
		if n := AccelTierNote(accel, "ref2va", quant); n != "" {
			t.Errorf("档位 %q 不该有条目：%s", accel, n)
		}
	}
	// ③ 非量化底模 + 盘上没有 Acc-8Step ⇒ 同样要说（原因不同：缺文件，不是底模不兼容）
	heavy := config.ComfySettings{
		RootDir:    root,
		UNetRef2VA: "MiniMax-H3-Ref2VA-fp16.safetensors",
	}
	heavyNoLora := config.ComfySettings{
		RootDir:    t.TempDir(),
		UNetRef2VA: "MiniMax-H3-Ref2VA-fp16.safetensors",
	}
	// 非量化底模 + 盘上有 Acc-8Step ⇒ **不该**有条目（画质档真的生效了）
	if n := AccelTierNote("quality", "ref2va", heavy); n != "" {
		t.Errorf("非量化底模且文件在位时画质档生效，不该报：%s", n)
	}
	// 非量化底模 + 盘上没有 ⇒ 说"盘上没有"（原因不同，说法要分开）
	if n := AccelTierNote("quality", "ref2va", heavyNoLora); !strings.Contains(n, "盘上没有") {
		t.Errorf("缺 LoRA 文件时应说「盘上没有」：%s", n)
	}
	// ④ 判据与选路同源：回退发生时 accelLoraFor 必须真的给 turbo（两处一致）
	job := engine.Job{Accel: "quality", Mode: "ref2va"}
	got := accelLoraFor(job, quant)
	if got != quant.TurboLoRARef2VA {
		t.Errorf("回退时 accelLoraFor 应给 turbo(%q)，实际 %q", quant.TurboLoRARef2VA, got)
	}
	if AccelTierNote("quality", "ref2va", quant) == "" {
		t.Error("既然真的回退了，就必须有说明 —— 否则又回到「静默」")
	}
}
