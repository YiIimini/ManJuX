package comfyengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// TestConflictsOnlyFlagsEnabledPacks 冲突检测的边界:
// 只报「已安装且未禁用」的已知冲突包 —— 已按文档禁用(目录名以 .disabled 结尾)的
// 不再打扰,无关插件包也绝不能误报(误报会让用户对告警脱敏)。
func TestConflictsOnlyFlagsEnabledPacks(t *testing.T) {
	root := t.TempDir()
	nodes := filepath.Join(root, "ComfyUI", "custom_nodes")
	for _, name := range []string{
		"ComfyUI-MiniMaxH3-Cache",           // 未禁用 → 必须告警
		"ComfyUI-MiniMaxH3-Cache.disabled",  // 已禁用 → 不告警
		"ComfyUI-KJNodes",                    // 无关包 → 不告警
		"ComfyUI-H3-ConditioningCache",       // 名字相近但无关 → 不告警
	} {
		if err := os.MkdirAll(filepath.Join(nodes, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.ComfySettings{RootDir: root}
	e := New(func() config.ComfySettings { return cfg })

	got := e.Conflicts()
	if len(got) != 1 {
		t.Fatalf("应只对未禁用的冲突包告警一条,实际 %d 条: %v", len(got), got)
	}
	if !strings.Contains(got[0], "MiniMaxH3-Cache") {
		t.Errorf("告警应指明插件目录名,实际 %q", got[0])
	}
	if !strings.Contains(got[0], "cond_audio") {
		t.Errorf("告警应说明冲突机制(便于用户理解为何要禁用),实际 %q", got[0])
	}
}

// TestConflictsWithoutRootDir root_dir 未配置时不得 panic。
func TestConflictsWithoutRootDir(t *testing.T) {
	e := New(func() config.ComfySettings { return config.ComfySettings{} })
	if got := e.Conflicts(); len(got) != 0 {
		t.Errorf("未配置根目录时应返回空,实际 %v", got)
	}
}
