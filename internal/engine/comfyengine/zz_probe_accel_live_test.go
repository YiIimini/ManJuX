package comfyengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// zz_probe_accel_live_test.go 真机探针：拿**真机 settings.json** 算「这一镜实际挂哪条 LoRA / 跑几步」。
//
// 【为什么留着】2026-10-14 排查「画面细节好拉」时，靠它当场证明了两件事：① 默认档在
// `steps=8` 下挂的是 **8 步族**（不是界面上写的"官方 4 步蒸馏"）；② `quality` 档在**量化底模**
// 上挂不上 Acc-8Step，实际回退成与默认档一样。引擎行为由 accel_tier_note_test.go /
// turbo_anchor_test.go 钉住；这一份只负责在**这台机器的真实配置**上如实播报。
//
// 判据：读不到 `data/settings.json` 就跳过（别的机器上没有这份配置，不许假装验过）。
func TestZZProbeAccelReal(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "data", "settings.json"))
	if err != nil {
		t.Skip("读不到 data/settings.json")
	}
	var full struct {
		Comfy config.ComfySettings `json:"comfy"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatal(err)
	}
	c := full.Comfy
	t.Logf("base   ref2va = %s", c.UNetRef2VA)
	t.Logf("loRA   配置里那条 = %s", c.TurboLoRARef2VA)
	t.Logf("root_dir = %s", c.RootDir)
	for _, d := range ModelDirs(c.RootDir, "loras") {
		t.Logf("  loras 搜索目录: %s", d)
	}
	t.Logf("8 步族文件在盘上？%v（%s）", modelFileExists(c, turbo8StepRef2VA, []string{"loras"}), turbo8StepRef2VA)

	for _, steps := range []int{4, 8, 20} {
		for _, accel := range []string{"", "quality", "refine", "express"} {
			job := engine.Job{Mode: "ref2va", Accel: accel, Steps: steps}
			t.Logf("accel=%-8q steps=%-2d ⇒ 实际挂 %-70q 实跑 %d 步",
				accel, steps, accelLoraFor(job, c), accelSteps(job, c, steps))
		}
	}
	for _, steps := range []int{4, 8} {
		t.Logf("AccelTierNote(\"\", ref2va, %d) = %q", steps, AccelTierNote("", "ref2va", steps, c))
	}
	t.Logf("AccelTierNote(\"quality\"…) = %q", AccelTierNote("quality", "ref2va", 8, c))
}
