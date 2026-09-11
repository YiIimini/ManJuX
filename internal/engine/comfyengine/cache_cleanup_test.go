package comfyengine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// TestCleanupProjectCacheRemovesPtData 回归:清理项目缓存必须把 .pt **数据**一起删。
//
// 背景:数据(.pt)在 ComfyUI 侧 models/conditioning,记账(.ok)在本应用侧
// data/cond_cache —— 是两处不同的位置,且 .pt 文件名只有 s{shot}_{hash}、
// 不含项目标识,只能靠 .ok 文件名反查。旧实现只清记账,实测该目录因此堆积
// 到 4.9GB 且只增不减。
func TestCleanupProjectCacheRemovesPtData(t *testing.T) {
	root := t.TempDir()
	comfyRoot := filepath.Join(root, "comfyui")
	// sidecarRoot(c) = Dir(RootDir)/data
	sidecar := filepath.Join(root, "data", "cond_cache", "manjux_proj1")
	ptDir := filepath.Join(comfyRoot, "ComfyUI", "models", "conditioning")
	for _, d := range []string{sidecar, ptDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mine := []string{"s1_aaaa1111", "s2_bbbb2222"}
	for _, n := range mine {
		if err := os.WriteFile(filepath.Join(sidecar, n+".ok"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ptDir, n+".pt"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 别的项目的缓存:绝不能误删
	other := "s1_cccc3333"
	if err := os.WriteFile(filepath.Join(ptDir, other+".pt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.ComfySettings{RootDir: comfyRoot}
	e := New(func() config.ComfySettings { return cfg })
	e.CleanupProjectCache("proj1")

	for _, n := range mine {
		if _, err := os.Stat(filepath.Join(ptDir, n+".pt")); !os.IsNotExist(err) {
			t.Errorf("本项目缓存 %s.pt 应被删除", n)
		}
	}
	if _, err := os.Stat(filepath.Join(ptDir, other+".pt")); err != nil {
		t.Errorf("他人项目的缓存 %s.pt 不得被误删", other)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Error("sidecar 记账目录应被删除")
	}
}

// TestCleanupProjectCacheWithoutSidecar 无记账目录时不应 panic(缓存从未建立过)。
func TestCleanupProjectCacheWithoutSidecar(t *testing.T) {
	root := t.TempDir()
	cfg := config.ComfySettings{RootDir: filepath.Join(root, "comfyui")}
	e := New(func() config.ComfySettings { return cfg })
	e.CleanupProjectCache("never-existed") // 只要求不 panic
}
