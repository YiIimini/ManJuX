package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 条件缓存记账目录：**清理读的必须就是引擎写的那个**（2026-09-30 深度审计）。
//
// 【现场（实机，不是推断）】本机 settings.json：
//
//	data_dir       = D:\Ai\Datas\ManJuX\data
//	comfy.root_dir = D:\Ai\ComfyUI
//
// 引擎按 `filepath.Dir(root_dir)/data/cond_cache` 写记账 ⇒ `D:\Ai\data\cond_cache`
// （实机 2 个项目子目录：第 01 集 + turnaround），而「一键清理」自己拼
// `<data_dir>/cond_cache` ⇒ `D:\Ai\Datas\ManJuX\data\cond_cache`（**该目录不存在**）。
//
// 于是：`collectLiveCondNames` 读不到任何 `.ok` ⇒ `live` 是空集 ⇒
// `cleanCondCacheFiles` 把 ComfyUI 侧 `models/conditioning/*.pt` **全删**
// （实机 12 个 / 1,877.4 MB，与那 12 个 sidecar 一一对应，全是活的），
// 而 sidecar 还在 ⇒ 下次渲染命中"有 .ok 没 .pt"的坏缓存，报出来的却是 ComfyUI 节点错误。
//
// 判据写成**结构性**的（不是"结果对不对"）：`s.condSidecarDirs()` 必须包含引擎的
// `comfyengine.CondSidecarDir`；并且端到端跑一次清理，活 .pt 必须还在、孤儿必须被清掉。
// 夹具照真机形态写：**ComfyUI 装在应用数据目录之外**（这正是两者错位的唯一条件；
// 之前的用例把夹具放在 `s.dataDir()/cond_cache`，所以它一直看不见这个错位）。
func TestCleanupReadsEngineSidecarRoot(t *testing.T) {
	s := newTestServer(t)

	// 真机形态：应用数据目录与 ComfyUI 的上级**不是同一个目录**。
	base := t.TempDir()
	dataDir := filepath.Join(base, "Datas", "ManJuX", "data")
	comfyRoot := filepath.Join(base, "ComfyUI")
	cfg := s.cfg.Get()
	cfg.DataDir = dataDir
	cfg.Comfy.RootDir = comfyRoot
	if err := s.cfg.Update(cfg); err != nil {
		t.Fatalf("写设置失败：%v", err)
	}

	engineDir := comfyengine.CondSidecarDir(s.cfg.Get().Comfy)
	if want := filepath.Join(base, "data", "cond_cache"); engineDir != want {
		t.Fatalf("引擎记账目录应推导为 %s，实际 %s", want, engineDir)
	}
	if engineDir == filepath.Join(dataDir, "cond_cache") {
		t.Fatalf("夹具不成立：两者重合就看不出错位（engine=%s）", engineDir)
	}

	// ① 结构性判据：清理查的目录里必须有引擎写的那一份。
	dirs := s.condSidecarDirs()
	found := false
	for _, d := range dirs {
		if filepath.Clean(d) == filepath.Clean(engineDir) {
			found = true
		}
	}
	if !found {
		t.Fatalf("清理必须把引擎的记账目录算进去（引擎写 %s，清理查 %v）——"+
			"少了它就会把活的条件缓存 .pt 当成孤儿删掉", engineDir, dirs)
	}

	// ② 端到端：活缓存必须活下来。
	proj, err := s.store.Create("条件缓存记账根测试", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	liveName := "s1_e86f1e56" // 照真机的缓存名形态（s{镜号}_{hash}）
	sidecar := filepath.Join(engineDir, "manjux_"+proj.ID)
	if err := os.MkdirAll(sidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, liveName+".ok"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	ptDir := filepath.Join(comfyRoot, "ComfyUI", "models", "conditioning")
	if err := os.MkdirAll(ptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ptDir, liveName+".pt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphan := "s9_cccc3333"
	if err := os.WriteFile(filepath.Join(ptDir, orphan+".pt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, _ := doReq(t, s, "POST", "/api/cleanup", map[string]any{}); code != 200 {
		t.Fatalf("清理应 200，实际 %d", code)
	}

	if _, err := os.Stat(filepath.Join(ptDir, liveName+".pt")); err != nil {
		t.Errorf("活的 .pt 被删了（%s）—— sidecar 还在，下次渲染会命中坏缓存并报成 ComfyUI 节点错误", liveName)
	}
	if _, err := os.Stat(filepath.Join(ptDir, orphan+".pt")); !os.IsNotExist(err) {
		t.Errorf("孤儿 %s.pt 应被清掉（这才是清理该做的事）", orphan)
	}
}

// 读不到记账目录 ⇒ **一个 .pt 都不许删**（"不知道"不等于"没有"）。
//
// 误删活缓存的根因是"读不到"与"空集合"混成同一件事：`collectLiveCondNames` 读不到目录
// 时返回空集，而空集的语义是"没有任何活缓存"。判据：记账目录存在但读不出来时，
// `unreadableCondSidecar` 必须指认它，清理据此跳过 .pt 清理并**如实播报**。
func TestUnreadableCondSidecarIsNotTreatedAsEmpty(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "nope")
	if got := unreadableCondSidecar([]string{missing}); got != "" {
		t.Errorf("目录本来就不该算\"读不出来\"，实得 %q", got)
	}
	real := filepath.Join(base, "cond_cache")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := unreadableCondSidecar([]string{missing, real}); got != "" {
		t.Errorf("能读的目录不该被报成读不出来，实得 %q", got)
	}
	// 存在的**文件**（不是目录）同样不算"读不出来"—— 这不是记账目录，交回给原来的空集语义。
	if err := os.WriteFile(filepath.Join(base, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := unreadableCondSidecar([]string{filepath.Join(base, "file")}); got != "" {
		t.Errorf("非目录不该被当成记账目录，实得 %q", got)
	}
}
