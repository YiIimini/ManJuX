package server

import (
	"github.com/YiIimini/ManJuX/internal/project"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// TestCleanupRemovesStageGarbage 一键清理必须覆盖各环节产生的垃圾/过时文件。
//
// 回归 2026-09-12(用户「一键清理需要排查进行完善升级处理清理掉各环节产生的垃圾过时文件」):
// 实测遗漏项 —— ComfyUI input 里每次渲染上传的参考图副本(252.8MB/202 个)、资产阶段的
// 转身视频中间目录(<base>_views/,5-30MB/角色)、临时对照目录、以及无轮转的日志。
// 本测试用临时目录构造这些现场,断言"该删的删掉、该留的一个不能少"。
func TestCleanupRemovesStageGarbage(t *testing.T) {
	s := newTestServer(t)
	g := s.cfg.Get()

	// ① ComfyUI input:24 小时前的参考图副本(应删)+ 刚上传的(应留)+ 用户素材子目录(应留)+ 非图片文件(应留)
	inputDir := filepath.Join(g.Comfy.RootDir, "ComfyUI", "input")
	if err := os.MkdirAll(filepath.Join(inputDir, "用户素材"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(inputDir, "characters_char_c1.png")
	fresh := filepath.Join(inputDir, "characters_char_c2.png")
	script := filepath.Join(inputDir, "user_script.py")
	for _, p := range []string{old, fresh, script} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(old, past, past)

	// ② 资产阶段:转身视频中间目录(应删),同目录的参考图(必须留)
	proj, err := s.store.Create("清理测试项目", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	charDir := filepath.Join(s.store.Dir(proj.ID), "assets", "characters")
	viewsDir := filepath.Join(charDir, "characters_char_c1_views")
	if err := os.MkdirAll(viewsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vid := filepath.Join(viewsDir, "turnaround.mp4")
	if err := os.WriteFile(vid, []byte("video-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	keepRefs := []string{
		filepath.Join(charDir, "characters_char_c1_view_side.png"),
		filepath.Join(charDir, "characters_char_c1_turnaround.png"),
	}
	for _, p := range keepRefs {
		if err := os.WriteFile(p, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// ③ 临时对照目录 + 膨胀日志
	scratch := filepath.Join(s.dataDir(), "_scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "a.png"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(s.dataDir(), "logs", "manjux.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, make([]byte, 9<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	// 执行清理
	if code, _ := doReq(t, s, "POST", "/api/cleanup", map[string]any{}); code != 200 {
		t.Fatalf("清理应 200,实际 %d", code)
	}

	// 断言:该删的删了
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("24 小时前的 input 参考图副本应被清理")
	}
	if _, err := os.Stat(viewsDir); !os.IsNotExist(err) {
		t.Error("转身视频中间目录应被清理")
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Error("临时对照目录应被清理")
	}
	if st, err := os.Stat(logPath); err != nil || st.Size() > 8<<20 {
		t.Errorf("膨胀日志应轮转(归档 .1 并清空),实际 size=%v err=%v", st, err)
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Error("日志应归档为 .1,便于回溯")
	}
	// 断言:该留的一个不能少
	for _, p := range append([]string{fresh, script, filepath.Join(inputDir, "用户素材")}, keepRefs...) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("不该被清理的路径被删了:%s(%v)", p, err)
		}
	}
	if _, err := os.Stat(s.store.Dir(proj.ID)); err != nil {
		t.Error("项目目录本身不得被清理")
	}
}
