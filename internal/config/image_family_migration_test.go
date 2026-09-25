package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 图像模型家族的迁移闸门(2026-09-25 用户定调弃用 Krea-2)。
//
// 这里钉的是一次**真实踩过的坑**:迁移条件原本写成"image_family 字段为空",
// 而加载时先 `s := Default()` 已经把该字段填成新默认值,老文件反序列化后它永远非空
// —— 于是家族名变了、模型文件名没变,按 Qwen-Image-2.1 的连线去加载 Krea-2 的编码器,
// ComfyUI 报 "Krea2 expects conditioning with 12x2560=30720 features ... but got 2560"。
// 判据必须是"**落盘文件里有没有这个键**"。

func writeSettings(t *testing.T, dir string, body string) string {
	t.Helper()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLegacyKrea2SettingsMigrateToQwen21 老文件(没有 image_family 键 + 旧三件套):
// 家族与三件套必须**一起**换掉 —— 只换一半就是上面那个报错。
func TestLegacyKrea2SettingsMigrateToQwen21(t *testing.T) {
	dir := t.TempDir()
	legacy := map[string]any{
		"comfy": map[string]any{
			"image_unet": "krea2_turbo_fp8_scaled.safetensors",
			"image_clip": "qwen3vl_4b_fp8_scaled.safetensors",
			"image_vae":  "qwen_image_vae.safetensors",
		},
	}
	raw, _ := json.Marshal(legacy)
	m, err := NewManager(writeSettings(t, dir, string(raw)), dir)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Get().Comfy
	if c.ImageFamily != "qwen_image_2_1" {
		t.Errorf("家族应迁移为 qwen_image_2_1,实际 %q", c.ImageFamily)
	}
	def := Default().Comfy
	if c.ImageUNet != def.ImageUNet || c.ImageCLIP != def.ImageCLIP || c.ImageVAE != def.ImageVAE {
		t.Errorf("三件套必须与家族一起迁移,实际 unet=%s clip=%s vae=%s(期望 %s / %s / %s)",
			c.ImageUNet, c.ImageCLIP, c.ImageVAE, def.ImageUNet, def.ImageCLIP, def.ImageVAE)
	}
}

// TestExplicitImageChoiceIsNeverOverwritten 已经写过 image_family 的文件一个字不动 ——
// 用户可能就是想留在 Krea-2 复现旧结果,或已经手工换成 Z-Image。
func TestExplicitImageChoiceIsNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	pinned := map[string]any{
		"comfy": map[string]any{
			"image_family": "krea2",
			"image_unet":   "krea2_turbo_fp8_scaled.safetensors",
			"image_clip":   "qwen3vl_4b_fp8_scaled.safetensors",
			"image_vae":    "qwen_image_vae.safetensors",
		},
	}
	raw, _ := json.Marshal(pinned)
	m, err := NewManager(writeSettings(t, dir, string(raw)), dir)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Get().Comfy
	if c.ImageFamily != "krea2" || c.ImageUNet != "krea2_turbo_fp8_scaled.safetensors" {
		t.Errorf("显式选择不得被覆盖,实际 family=%q unet=%q", c.ImageFamily, c.ImageUNet)
	}
}

// TestCustomImageModelsSurviveLegacyMigration 没写过 family、但**换过模型**:
// 三件套不等于旧默认 → 不迁移(否则会把用户自己挑的模型冲掉)。
func TestCustomImageModelsSurviveLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	custom := map[string]any{
		"comfy": map[string]any{
			"image_unet": "z_image_turbo_bf16.safetensors",
			"image_clip": "qwen_3_4b.safetensors",
			"image_vae":  "ae.safetensors",
		},
	}
	raw, _ := json.Marshal(custom)
	m, err := NewManager(writeSettings(t, dir, string(raw)), dir)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Get().Comfy
	if c.ImageUNet != "z_image_turbo_bf16.safetensors" || c.ImageVAE != "ae.safetensors" {
		t.Errorf("手工换过的模型不得被迁移冲掉,实际 %s / %s", c.ImageUNet, c.ImageVAE)
	}
}

// TestLegacyEditSettingsMigrateToQwen21 编辑侧一起迁移(用户定调「都使用 2.1」)。
//
// 只迁生成侧会出现"生成用 Qwen-Image-2.1、编辑用 Qwen-Image-Edit-2511"的两套画风,
// 而多视角图会作为角色的第二张参考图喂进渲染 —— 两套画风在那里直接打架。
// 两个 2511 专用 LoRA 也必须一并清空:它们是给 2511 训的,套在 2.1 上不成立。
func TestLegacyEditSettingsMigrateToQwen21(t *testing.T) {
	dir := t.TempDir()
	legacy := map[string]any{
		"comfy": map[string]any{
			"image_unet":       "krea2_turbo_fp8_scaled.safetensors",
			"image_clip":       "qwen3vl_4b_fp8_scaled.safetensors",
			"image_vae":        "qwen_image_vae.safetensors",
			"edit_unet":        "qwen_image_edit_2511_fp8mixed.safetensors",
			"edit_clip":        "qwen_2.5_vl_7b_fp8_scaled.safetensors",
			"edit_vae":         "qwen_image_vae.safetensors",
			"edit_turbo_lora":  "Qwen-Image-Edit-2511-Lightning-4steps-V1.0-bf16.safetensors",
			"edit_angles_lora": "qwen-image-edit-2511-multiple-angles-lora.safetensors",
		},
	}
	raw, _ := json.Marshal(legacy)
	m, err := NewManager(writeSettings(t, dir, string(raw)), dir)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Get().Comfy
	def := Default().Comfy
	if c.EditUNet != def.EditUNet || c.EditCLIP != def.EditCLIP || c.EditVAE != def.EditVAE {
		t.Errorf("编辑三件套应迁到 2.1,实际 unet=%s clip=%s vae=%s", c.EditUNet, c.EditCLIP, c.EditVAE)
	}
	if c.EditTurboLoRA != "" || c.EditAnglesLoRA != "" {
		t.Errorf("2511 专用 LoRA 必须清空(套在 2.1 上不成立),实际 %q / %q", c.EditTurboLoRA, c.EditAnglesLoRA)
	}
}

// TestLegacyImageButCustomEditKeepsEdit 生成侧是旧默认、但**编辑侧被手工改过**时,
// 只迁生成侧 —— 别把用户挑的编辑模型冲掉。
func TestLegacyImageButCustomEditKeepsEdit(t *testing.T) {
	dir := t.TempDir()
	mixed := map[string]any{
		"comfy": map[string]any{
			"image_unet": "krea2_turbo_fp8_scaled.safetensors",
			"image_clip": "qwen3vl_4b_fp8_scaled.safetensors",
			"image_vae":  "qwen_image_vae.safetensors",
			"edit_unet":  "my_own_edit_model.safetensors",
			"edit_clip":  "my_clip.safetensors",
			"edit_vae":   "my_vae.safetensors",
		},
	}
	raw, _ := json.Marshal(mixed)
	m, err := NewManager(writeSettings(t, dir, string(raw)), dir)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Get().Comfy
	if c.EditUNet != "my_own_edit_model.safetensors" || c.EditCLIP != "my_clip.safetensors" {
		t.Errorf("手工挑的编辑模型不得被迁移冲掉,实际 %s / %s", c.EditUNet, c.EditCLIP)
	}
	if c.ImageUNet == "krea2_turbo_fp8_scaled.safetensors" {
		t.Error("生成侧仍应迁移到新默认")
	}
}
