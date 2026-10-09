package pipeline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// 装配引擎（MoviePy 的 python 环境）必须落在**数据根**下的 tools，而不是项目仓库根下。
//
// 【为什么这条要有门】2026-10-09 换装配引擎时，第一版写的是
// `filepath.Join(c.store.Root(), "tools")`，而 store 的根是 `<数据根>/projects`
// （见 main.go 的 project.NewStore(filepath.Join(g.DataDir, "projects"))）——
// 于是"引擎环境在哪"这条事实在**两个地方各说一套**：文档/我以为是 `<数据根>/tools`
// （那台机器上环境也真的建在那里），代码却指向 `<数据根>/projects/tools`
// ⇒ 真机第一次合片会去一个**空目录**重新引导一遍（联网装 moviepy），
// 而排查的人会看到"环境明明装好了却还要重装"。
//
// 判据只喂**函数本体**（不喂上游过滤过的数据）：把 engineToolDir 改回 store.Root() 就红。
func TestEngineToolDirIsDataRootTools(t *testing.T) {
	root := t.TempDir()
	cm, err := config.NewManager(filepath.Join(root, "settings.json"), root)
	if err != nil {
		t.Fatalf("建配置管理器失败：%v", err)
	}
	c := &Coordinator{cfg: cm}
	got := c.engineToolDir()
	dataDir := cm.Get().DataDir
	if dataDir == "" {
		t.Fatalf("夹具前提不成立：DataDir 为空（config.NewManager 应依 exeDir 兜底填 <exeDir>/data）")
	}
	if want := filepath.Join(dataDir, "tools"); got != want {
		t.Errorf("装配引擎环境该落在 %s，实际 %s", want, got)
	}
	// 反向：绝不许落在项目仓库（<数据根>/projects）里 —— 那里每次盘点/清理/导出都在扫。
	if strings.HasPrefix(got, filepath.Join(dataDir, "projects")+string(filepath.Separator)) {
		t.Errorf("装配引擎环境不许落在项目仓库里（实际 %s）—— 项目仓库的根是 store.Root()，"+
			"改回 filepath.Join(c.store.Root(), \"tools\") 就是这个后果", got)
	}
	// 取不到配置时返回空串（compose 会如实降级并播报），不许 panic、也不许猜一个路径。
	if d := (&Coordinator{}).engineToolDir(); d != "" {
		t.Errorf("没有配置时该返回空串，实际 %q", d)
	}
}

// 接线门：Compose 那一次调用必须把引擎落点传下去（传不到 ⇒ Params.ToolDir 为空 ⇒
// 引擎环境退到用户缓存目录，与上面那条事实又分叉）。
func TestComposeCallPassesEngineToolDir(t *testing.T) {
	src := readSrc(t, filepath.Join("stages_qc_assemble.go"))
	found := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue // 注释里出现不算（摆设守卫就是这么来的）
		}
		if strings.Contains(trimmed, "ToolDir: c.engineToolDir(),") {
			found = true
		}
	}
	if !found {
		t.Error("compose.Compose 的调用处必须写 ToolDir: c.engineToolDir()（引擎落点要传下去）")
	}
}
