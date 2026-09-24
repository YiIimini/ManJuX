package comfyengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// 权重存在判据的布局解析(审计 P1-2)。
//
// 回归场景:`modelFileExists` 原先只猜 `<root>/ComfyUI/models` 与 `<root>/shared/models`,
// 漏了本机实际在用的 `<root>/ComfyUI/shared/models`(extra_model_paths.yaml 的 base_path)。
// 后果不是报错,而是**静默降级**:express 档的 int8 VAE、quality 档的 Acc-8Step LoRA
// 都被判"不存在",用户以为开了极速/高画质,实际跑默认档。
func TestModelDirsCoversSharedLayout(t *testing.T) {
	root := t.TempDir()
	sub := "vae"
	want := []string{
		filepath.Join(root, "ComfyUI", "models", sub),
		filepath.Join(root, "ComfyUI", "shared", "models", sub),
		filepath.Join(root, "shared", "models", sub),
	}
	for _, d := range want {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(ModelDirs(root, sub), "|")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("应包含 %s,实际 %v", w, ModelDirs(root, sub))
		}
	}
	if dirs := ModelDirs("", sub); dirs != nil {
		t.Errorf("空 root 应返回 nil,实际 %v", dirs)
	}
	if dirs := ModelDirs(root, "不存在的子目录"); dirs != nil {
		t.Errorf("不存在的目录不该被返回(否则体检会对着空路径说'有'),实际 %v", dirs)
	}
}

// TestModelFileExistsFindsSharedLayout 关键回归:文件放在 shared 布局下必须被认出来。
func TestModelFileExistsFindsSharedLayout(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ComfyUI", "shared", "models", "vae")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "minimax_h3_video_vae_int8_convrot.safetensors"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := config.ComfySettings{RootDir: root}
	if !modelFileExists(c, name, []string{"vae"}) {
		t.Fatal("shared 布局下的权重必须被认出来 —— 漏判会让 express/quality 档静默降级")
	}
	if modelFileExists(c, "不存在的.safetensors", []string{"vae"}) {
		t.Error("不存在的文件不该返回 true")
	}
	if modelFileExists(c, "", []string{"vae"}) {
		t.Error("空文件名不该返回 true")
	}
	// yaml 里**显式映射**的位置同样要认(ComfyUI 只按段里列出的键映射;
	// 用户换布局时改的就是这个文件)
	elsewhere := filepath.Join(root, "别处")
	if err := os.MkdirAll(filepath.Join(elsewhere, "loras"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ComfyUI"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "shared:\n    base_path: " + filepath.ToSlash(elsewhere) + "\n    loras: loras/\n"
	if err := os.WriteFile(filepath.Join(root, "ComfyUI", "extra_model_paths.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	lora := "acc8.safetensors"
	if err := os.WriteFile(filepath.Join(elsewhere, "loras", lora), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !modelFileExists(c, lora, []string{"loras", "diffusion_models"}) {
		t.Error("extra_model_paths.yaml 显式映射的位置也要认")
	}
	// 段里没列出的键:按 ComfyUI 的默认约定退回 <base>/models/<子目录>
	if err := os.MkdirAll(filepath.Join(elsewhere, "models", "vae"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "models", "vae", "v.safetensors"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !modelFileExists(c, "v.safetensors", []string{"vae"}) {
		t.Error("base_path 下的默认模型目录也要认")
	}
}
