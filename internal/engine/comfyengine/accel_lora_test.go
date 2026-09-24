package comfyengine

import (
	"os"
	"path/filepath"
	"testing"
)

// 加速 LoRA × 底模量化格式 的兼容性守卫。
//
// 背景(知识库 2026-09 实测结论):质量档默认挂 Kijai 的 Acc-8Step 蒸馏 LoRA,
// 但它挂不上**低位量化底模** —— int8 底模上张量形状对不上(32 vs 1024),
// 官方修复未合并主线。挂错的症状不是报错,而是出图损坏(灰片/噪声),
// 排查成本极高;所以这里把「量化底模 → 自动回退官方 Turbo LoRA @ 8 步」
// 定成契约,并在图组装阶段就断言住。
//
// 两层断言:
//
//	isQuantizedBase  —— 判据层(含大小写、边界:fp8_scaled 是原厂格式,不得误判)
//	buildRenderGraph —— 接线层(质量档下 LoraLoaderModelOnly 实际吃的是哪条 LoRA)

// accRef2VA / accFL2VA 与 engine.go 中 buildRenderGraph 的常量保持一致。
const (
	accRef2VA = "MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy.safetensors"
	accFL2VA  = "MiniMax-H3-FL2VA-Acc-8Step_pruned_comfy.safetensors"
)

func TestIsQuantizedBase(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"minimax_h3_ref2va_pruned_int8_convrot.safetensors", true},
		{"minimax_h3_ref2va_pruned_INT8.safetensors", true},
		{"MiniMax-H3_Ref2VA_Int8.safetensors", true},
		{"minimax_h3_fl2va_w4a8.safetensors", true},
		{"minimax_h3_ref2va_pruned_nvfp4.safetensors", true},
		{"minimax_h3_ref2va_pruned_mxfp8.safetensors", true},
		// 原厂发布的 fp8_scaled 底模可以正常挂 Acc,不得误判(误判=白白丢掉质量档)
		{"minimax_h3_ref2va_pruned_fp8_scaled.safetensors", false},
		{"minimax_h3_ref2va_pruned_fp16.safetensors", false},
		{"minimax_h3_ref2va_pruned_bf16.safetensors", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isQuantizedBase(tc.name); got != tc.want {
			t.Errorf("isQuantizedBase(%q) = %v,期望 %v", tc.name, got, tc.want)
		}
	}
}

// touchLora 在测试用的 ComfyUI/models/loras 下放一个占位 LoRA 文件,
// 让 modelFileExists 的文件守卫为真(守卫本身另有 preflight 测试覆盖)。
func touchLora(t *testing.T, root string, names ...string) {
	t.Helper()
	dir := filepath.Join(root, "ComfyUI", "models", "loras")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("placeholder"), 0o644); err != nil {
			t.Fatalf("写占位 LoRA 失败: %v", err)
		}
	}
}

// loraOfGraph 取出图里 LoraLoaderModelOnly 实际挂载的 LoRA 名(无节点返回 "")。
func loraOfGraph(t *testing.T, graph map[string]any) string {
	t.Helper()
	_, inputs := findNode(t, graph, "LoraLoaderModelOnly")
	if inputs == nil {
		return ""
	}
	name, _ := inputs["lora_name"].(string)
	return name
}

// TestAccelQualityLoraGuard 质量档接线层:量化底模必须回退官方 Turbo LoRA,
// 非量化底模必须挂上 Acc-8Step(否则质量档名不副实)。
func TestAccelQualityLoraGuard(t *testing.T) {
	root := t.TempDir()
	touchLora(t, root, accRef2VA, accFL2VA)

	cases := []struct {
		name    string
		mode    string
		unet    string
		wantAcc string // 期望挂上的 Acc 文件;空表示期望回退官方 Turbo
	}{
		{
			name: "fp8_scaled 底模挂 Acc(ref2va)", mode: "ref2va",
			unet: "minimax_h3_ref2va_pruned_fp8_scaled.safetensors", wantAcc: accRef2VA,
		},
		{
			name: "fp16 底模挂 Acc(ref2va)", mode: "ref2va",
			unet: "minimax_h3_ref2va_pruned_fp16.safetensors", wantAcc: accRef2VA,
		},
		{
			name: "fp8_scaled 底模挂 Acc(fl2va/t2va)", mode: "t2va",
			unet: "minimax_h3_fl2va_pruned_fp8_scaled.safetensors", wantAcc: accFL2VA,
		},
		{
			name: "int8 底模回退官方 Turbo", mode: "ref2va",
			unet: "minimax_h3_ref2va_pruned_int8_convrot.safetensors",
		},
		{
			name: "w4a8 底模回退官方 Turbo", mode: "ref2va",
			unet: "minimax_h3_ref2va_pruned_w4a8.safetensors",
		},
		{
			name: "nvfp4 底模回退官方 Turbo", mode: "ref2va",
			unet: "minimax_h3_ref2va_pruned_nvfp4.safetensors",
		},
		{
			name: "mxfp8 底模回退官方 Turbo", mode: "ref2va",
			unet: "minimax_h3_ref2va_pruned_mxfp8.safetensors",
		},
		{
			name: "INT8 大写同样回退(ref2va)", mode: "ref2va",
			unet: "MiniMax-H3_Ref2VA_INT8.safetensors",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testComfySettings()
			c.RootDir = root
			c.UNetRef2VA = tc.unet
			c.UNetFL2VA = tc.unet

			job := baseJob(tc.mode)
			job.Accel = "quality"

			graph := buildRenderGraph(job, c, "", []string{"a.png"}, nil, 768, 1344, 124)
			assertGraphContract(t, graph)

			got := loraOfGraph(t, graph)
			want := c.TurboLoRARef2VA
			if tc.mode != "ref2va" {
				want = c.TurboLoRAFL2VA
			}
			if tc.wantAcc != "" {
				want = tc.wantAcc
			}
			if got != want {
				t.Errorf("质量档应挂 %q,实际 %q", want, got)
			}
		})
	}
}

// TestAccelQualityLoraGuardNoAccFile Acc 未安装时,即使是可兼容的底模也必须回退
// 官方 Turbo —— 否则图里会引用一个不存在的 LoRA,渲染时才炸。
func TestAccelQualityLoraGuardNoAccFile(t *testing.T) {
	c := testComfySettings()
	c.RootDir = t.TempDir() // 空目录:没有任何 LoRA 文件
	c.UNetRef2VA = "minimax_h3_ref2va_pruned_fp8_scaled.safetensors"

	job := baseJob("ref2va")
	job.Accel = "quality"

	graph := buildRenderGraph(job, c, "", []string{"a.png"}, nil, 768, 1344, 124)
	assertGraphContract(t, graph)

	if got := loraOfGraph(t, graph); got != c.TurboLoRARef2VA {
		t.Errorf("Acc 文件缺失时应回退官方 Turbo %q,实际 %q", c.TurboLoRARef2VA, got)
	}
}

// TestAccelTurboNotAffectedByQuantization 非质量档不受本守卫影响:
// turbo 档始终挂官方 Turbo LoRA,量化与否都一样。
func TestAccelTurboNotAffectedByQuantization(t *testing.T) {
	c := testComfySettings()
	c.RootDir = t.TempDir()
	c.UNetRef2VA = "minimax_h3_ref2va_pruned_int8_convrot.safetensors"

	for _, accel := range []string{"turbo", "express"} {
		t.Run(accel, func(t *testing.T) {
			job := baseJob("ref2va")
			job.Accel = accel
			graph := buildRenderGraph(job, c, "", []string{"a.png"}, nil, 768, 1344, 124)
			if got := loraOfGraph(t, graph); got != c.TurboLoRARef2VA {
				t.Errorf("%s 档应挂官方 Turbo %q,实际 %q", accel, c.TurboLoRARef2VA, got)
			}
		})
	}
}
