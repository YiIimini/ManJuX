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
	// 步数**故意给 4**（<8）：这一条只测"档位回退"这件事；步数锚点那件事在下面单独测，
	// 两件事混在一起会让断言分不清是哪一条在说话。
	note := AccelTierNote("quality", "ref2va", turbo4StepSteps, quant)
	if note == "" {
		t.Fatal("量化底模下选 quality 必须给出一行说明（否则用户以为画质档生效了）")
	}
	for _, want := range []string{"没有生效", "int8", "非剪枝"} {
		if !strings.Contains(note, want) {
			t.Errorf("说明里缺 %q：%s", want, note)
		}
	}
	// ② 不该被噪音打扰的档位：精修档（不挂蒸馏 LoRA，没有锚点这回事）、极速档（自己的配方）、
	// 以及**默认档但步数低于 8**（4 步族锚定吻合，没什么好说的）
	for _, accel := range []string{"refine", "express", "turbo", ""} {
		if n := AccelTierNote(accel, "ref2va", turbo4StepSteps, quant); n != "" {
			t.Errorf("档位 %q（%d 步）不该有条目：%s", accel, turbo4StepSteps, n)
		}
	}
	// ②b 默认档请求 ≥8 步、而盘上没有 8 步族 ⇒ **必须**说话（2026-09-30 新增的那一半）：
	// 这正是"挂 4 步族跑 8 步"的真实情形：用户抱怨"成品不理想"时，这行字给出可执行答案。
	anchorNote := AccelTierNote("turbo", "ref2va", turbo8StepSteps, quant)
	if !strings.Contains(anchorNote, "8 步族") {
		t.Errorf("请求 8 步但盘上没有 8 步族时要说清该补哪个文件：%q", anchorNote)
	}
	if !strings.Contains(anchorNote, turbo8StepRef2VA) {
		t.Errorf("说明里要给出**文件名**（照它下载即可）：%q", anchorNote)
	}
	// ②c 8 步族在位 ⇒ 不再吵（真的配上了）
	with8 := quant
	with8.RootDir = root
	if err := os.WriteFile(filepath.Join(root, "ComfyUI", "models", "loras", turbo8StepRef2VA), []byte("stub"), 0o644); err != nil {
		// 目录不存在就建出来（模型目录解析走 comfydirs 的多布局，这里补标准布局即可）
		if mk := os.MkdirAll(filepath.Join(root, "ComfyUI", "models", "loras"), 0o755); mk != nil {
			t.Fatal(mk)
		}
		if err := os.WriteFile(filepath.Join(root, "ComfyUI", "models", "loras", turbo8StepRef2VA), []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := AccelTierNote("turbo", "ref2va", turbo8StepSteps, with8); n != "" {
		t.Errorf("8 步族在位时不该再报锚点问题：%s", n)
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
	if n := AccelTierNote("quality", "ref2va", turbo4StepSteps, heavy); n != "" {
		t.Errorf("非量化底模且文件在位时画质档生效，不该报：%s", n)
	}
	// 非量化底模 + 盘上没有 ⇒ 说"盘上没有"（原因不同，说法要分开）
	if n := AccelTierNote("quality", "ref2va", turbo4StepSteps, heavyNoLora); !strings.Contains(n, "盘上没有") {
		t.Errorf("缺 LoRA 文件时应说「盘上没有」：%s", n)
	}
	// ④ 判据与选路同源：回退发生时 accelLoraFor 必须真的给 turbo（两处一致）
	job := engine.Job{Accel: "quality", Mode: "ref2va"}
	got := accelLoraFor(job, quant)
	if got != quant.TurboLoRARef2VA {
		t.Errorf("回退时 accelLoraFor 应给 turbo(%q)，实际 %q", quant.TurboLoRARef2VA, got)
	}
	if AccelTierNote("quality", "ref2va", turbo4StepSteps, quant) == "" {
		t.Error("既然真的回退了，就必须有说明 —— 否则又回到「静默」")
	}
}
