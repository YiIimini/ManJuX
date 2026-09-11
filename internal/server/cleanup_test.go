package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCleanCondCacheFiles 清理条件缓存数据文件的判据:
// 仍被某个 sidecar(.ok)引用的保留,其余(孤儿)删除 —— 这正是 4.9GB 滞留的修复点。
// .pt 文件名不含项目标识(项目号只参与 hash),所以只能靠 sidecar 反查。
func TestCleanCondCacheFiles(t *testing.T) {
	root := t.TempDir()
	condDir := filepath.Join(root, "data", "cond_cache")
	ptDir := filepath.Join(root, "comfyui", "ComfyUI", "models", "conditioning")

	liveNames := []string{"s1_aaaa1111", "s2_bbbb2222"}
	if err := os.MkdirAll(filepath.Join(condDir, "manjux_proj1"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range liveNames {
		if err := os.WriteFile(filepath.Join(condDir, "manjux_proj1", n+".ok"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(ptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range liveNames {
		if err := os.WriteFile(filepath.Join(ptDir, n+".pt"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 孤儿:一个当前命名格式、两个早期 manjux_ 命名格式(实测 206 个属后者)
	orphans := []string{"s9_cccc3333", "manjux_old_s1_dddd4444", "manjux_old_s2_eeee5555"}
	for _, n := range orphans {
		if err := os.WriteFile(filepath.Join(ptDir, n+".pt"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 非 .pt 文件不该被碰
	if err := os.WriteFile(filepath.Join(ptDir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	live := collectLiveCondNames(condDir)
	if len(live) != 2 {
		t.Fatalf("应收集到 2 个活缓存名,实际 %d: %v", len(live), live)
	}
	count, size, err := cleanCondCacheFiles(ptDir, live)
	if err != nil {
		t.Fatal(err)
	}
	if count != len(orphans) {
		t.Errorf("应删除 %d 个孤儿,实际 %d", len(orphans), count)
	}
	if size <= 0 {
		t.Error("应统计释放字节数(用于 UI 显示释放量)")
	}
	for _, n := range liveNames {
		if _, err := os.Stat(filepath.Join(ptDir, n+".pt")); err != nil {
			t.Errorf("活文件 %s.pt 不得被删除", n)
		}
	}
	for _, n := range orphans {
		if _, err := os.Stat(filepath.Join(ptDir, n+".pt")); !os.IsNotExist(err) {
			t.Errorf("孤儿 %s.pt 应被删除", n)
		}
	}
	if _, err := os.Stat(filepath.Join(ptDir, "keep.txt")); err != nil {
		t.Error("非 .pt 文件不得被删除")
	}
}

// TestCleanCondCacheFilesNoDir 目录不存在时返回错误而非 panic,
// 调用方据此静默跳过(ComfyUI 未部署时不该报错)。
func TestCleanCondCacheFilesNoDir(t *testing.T) {
	if _, _, err := cleanCondCacheFiles(filepath.Join(t.TempDir(), "nope"), nil); err == nil {
		t.Error("目录不存在应返回错误")
	}
}

// TestCollectLiveCondNamesEmpty 无 sidecar 目录时返回空集合(此时全部 .pt 视为孤儿)。
func TestCollectLiveCondNamesEmpty(t *testing.T) {
	if live := collectLiveCondNames(filepath.Join(t.TempDir(), "nope")); len(live) != 0 {
		t.Errorf("无 sidecar 目录应返回空集合,实际 %d 项", len(live))
	}
}
