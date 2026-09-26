package comfyengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// TestCleanupProjectCacheSharedLayout 清理必须认得出 shared 布局的模型目录。
//
// 回归(2026-09-26 渲染管线审查,medium):旧实现只拼 `<root>/ComfyUI/models/conditioning`,
// 而本机模型实际在 `<root>/ComfyUI/shared/models/`(extra_model_paths 的 base_path)——
// 于是删项目时一个 .pt 都没删,日志照样打「已清理项目条件缓存」,目录只增不减
// (实测堆到 4.9GB / 17.4GB,而 .pt 文件名不含项目标识,事后无法按项目反查)。
func TestCleanupProjectCacheSharedLayout(t *testing.T) {
	root := t.TempDir()
	comfyRoot := filepath.Join(root, "comfyui")
	ptDir := filepath.Join(comfyRoot, "ComfyUI", "shared", "models", "conditioning")
	sidecar := filepath.Join(root, "data", "cond_cache", "manjux_proj1")
	for _, d := range []string{ptDir, sidecar} {
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
	other := "s1_cccc3333"
	if err := os.WriteFile(filepath.Join(ptDir, other+".pt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.ComfySettings{RootDir: comfyRoot}
	e := New(func() config.ComfySettings { return cfg })
	e.CleanupProjectCache("proj1")

	for _, n := range mine {
		if _, err := os.Stat(filepath.Join(ptDir, n+".pt")); !os.IsNotExist(err) {
			t.Errorf("shared 布局下的本项目缓存 %s.pt 必须被删掉", n)
		}
	}
	if _, err := os.Stat(filepath.Join(ptDir, other+".pt")); err != nil {
		t.Errorf("他人项目的缓存 %s.pt 不得被误删", other)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Error("sidecar 记账目录应被删除")
	}
}

// TestCondPtDirsUsesModelDirs 数据目录解析必须与清理/管理页共用同一实现。
//
// 两处各写一份是这条缺陷的根因(comfydirs.go 的注释里记过一次同类事故)。
func TestCondPtDirsUsesModelDirs(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "ComfyUI", "shared", "models", "conditioning")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	got := condPtDirs(config.ComfySettings{RootDir: root})
	if len(got) != 1 || got[0] != want {
		t.Errorf("condPtDirs = %v,期望 [%s]", got, want)
	}
	// 同一目录不得重复(extra_model_paths 可能再映射一次)
	if n := strings.Count(strings.Join(got, "|"), want); n != 1 {
		t.Errorf("目录应去重,实际 %v", got)
	}
	if got := condPtDirs(config.ComfySettings{}); len(got) != 0 {
		t.Errorf("未配置根目录时应返回空,实际 %v", got)
	}
}

// TestCondDataDirsEmptyStillWarnsNotSilent 找不到数据目录时不得静默报成功。
//
// 旧实现在 shared 布局下 removed 恒为 0,日志却照样写「已清理」。现在改成
// "只清掉 N/M 个 .pt"的告警。这里用源码判据钉住这个分支存在(日志文案没有别的观测面)。
func TestCondDataDirsEmptyStillWarnsNotSilent(t *testing.T) {
	src, err := readSourceFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "项目条件缓存只清掉") {
		t.Error("removed < caches 时必须告警,而不是打成功日志(旧实现就是这么骗过用户的)")
	}
	if !strings.Contains(src, "ModelDirs(c.RootDir, \"conditioning\")") {
		t.Error("数据目录必须走 ModelDirs(唯一解析点),不得再硬拼 ComfyUI/models/conditioning")
	}
}
