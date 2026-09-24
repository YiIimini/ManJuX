package comfyengine

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// 加速档"只有一张表"闸门(审计 P2)。
//
// 回归场景:整镜渲染有"quality 档换 Acc-8Step、量化底模不兼容就回退 Turbo"这套逻辑,
// 而局部重绘/音频驱动的图**复制了模型链却漏了这段** —— 同一个项目里主渲染用
// Acc-8Step、修补过的那一镜用 Turbo,画质基线就与周围镜头不同(人物偏糊),
// 而两边代码看起来都对。判据因此落在"两个图挂的 LoRA 必须相同"上。

func accelSettings() config.ComfySettings {
	c := testComfySettings()
	c.TurboLoRARef2VA = "turbo_ref2v.safetensors"
	c.TurboLoRAFL2VA = "turbo_fl2v.safetensors"
	return c
}

// TestAccelLoraPerTier 三档语义。
func TestAccelLoraPerTier(t *testing.T) {
	c := accelSettings()

	turboJob := baseJob("ref2va")
	turboJob.Accel = "turbo"
	if got := accelLoraFor(turboJob, c); got != c.TurboLoRARef2VA {
		t.Errorf("turbo 档应用官方 Turbo LoRA,实际 %q", got)
	}

	// quality 档:本机没有 Acc-8Step 权重(测试机必然没有)→ 回退 Turbo,不能返回空
	q := baseJob("ref2va")
	q.Accel = "quality"
	if got := accelLoraFor(q, c); got == "" {
		t.Error("quality 档在缺 Acc 权重时应回退 Turbo,而不是不挂任何加速")
	}

	// FL2VA 用另一个 LoRA
	fl := baseJob("t2va")
	fl.Mode = "t2va"
	fl.Accel = "turbo"
	if got := accelLoraFor(fl, c); got != c.TurboLoRAFL2VA {
		t.Errorf("非 ref2va 应用 FL2VA 的 LoRA,实际 %q", got)
	}
}

// TestAccelLoraSkipsQuantizedBaseWithAcc 量化底模 + quality 档不得挂 Acc-8Step。
//
// 这是"漏判的后果是出图直接损坏,而不是报错"那条:Acc-8Step 与 int8 量化底模
// tensor 形状不兼容(tensor 32 vs 1024)。
func TestAccelLoraSkipsQuantizedBaseWithAcc(t *testing.T) {
	for _, name := range []string{
		"MiniMax_H3_ref2va_pruned_int8_convrot.safetensors",
		"model_w4a8.safetensors", "base_nvfp4.safetensors", "x_mxfp8.safetensors",
	} {
		if !isQuantizedBase(name) {
			t.Errorf("%s 应被判定为量化底模(大小写不敏感)", name)
		}
	}
	for _, name := range []string{"fp8_scaled.safetensors", "model_fp16.safetensors"} {
		if isQuantizedBase(name) {
			t.Errorf("%s 不该被判定为量化底模(会白白丢掉 quality 档)", name)
		}
	}
}

// TestMaskGraphDoesNotPickItsOwnLora 源码级判据:maskgraph 不得自己决定挂哪个 LoRA。
//
// 为什么还要这一条:上面那个"两图 LoRA 相同"的断言只在**本机装了 Acc-8Step 权重**
// 时才能分辨出差异(没装时两条链都回退 Turbo,看起来一致)。所以真正的护栏是
// "maskgraph 里不许出现 Turbo/Aux LoRA 字段名" —— 它与机器上装了什么无关。
func TestMaskGraphDoesNotPickItsOwnLora(t *testing.T) {
	raw, err := os.ReadFile("maskgraph.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, ln := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, field := range []string{"TurboLoRARef2VA", "TurboLoRAFL2VA", "Acc-8Step"} {
			if strings.Contains(ln, field) {
				t.Errorf("maskgraph.go:%d 自己选了加速 LoRA(%s)—— 必须走 accelLoraFor,否则与主渲染的画质基线会分叉",
					i+1, field)
			}
		}
	}
}

// TestMaskAndRenderGraphsShareAccelTier 契约:同一个 job 下,两条链挂的加速 LoRA
// 必须完全一致 —— 这正是"修补过的那一镜与周围镜头画质一致"的全部含义。
func TestMaskAndRenderGraphsShareAccelTier(t *testing.T) {
	c := accelSettings()
	for _, accel := range []string{"turbo", "quality", "express", ""} {
		job := baseJob("ref2va")
		job.Accel = accel

		render := buildRenderGraph(job, c, "", []string{"a.png"}, nil, 768, 1344, 124)
		mask := buildMaskGraph(job, c, MaskOptions{
			SourceVideo: "shot.mp4", Prompt: "p", Target: "the bronze sparrow", ObjectIndex: "0",
		}, 768, 1344, 124)

		_, rl := findNode(t, render, "LoraLoaderModelOnly")
		_, ml := findNode(t, mask, "LoraLoaderModelOnly")
		rn, mn := "", ""
		if rl != nil {
			rn, _ = rl["lora_name"].(string)
		}
		if ml != nil {
			mn, _ = ml["lora_name"].(string)
		}
		if rn != mn {
			t.Errorf("档位 %q:主渲染挂 %q、局部重绘挂 %q —— 同一项目里画质基线必须一致", accel, rn, mn)
		}
		if rn == "" {
			t.Errorf("档位 %q:两条链都没挂加速 LoRA(应回退 Turbo 而不是不挂)", accel)
		}
		_ = strings.TrimSpace
		_ = engine.Job{}
	}
}
