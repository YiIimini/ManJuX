package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/project"
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

// TestCleanupCoversSharedModelLayout 回归 2026-09-24(用户:「一键清理也要清掉
// \ComfyUI\shared\models\conditioning\」):
//
// 清理代码原先只拼 `<root>/ComfyUI/models/conditioning`,而本机模型在
// `<root>/ComfyUI/shared/models/`(extra_model_paths.yaml 的 base_path)——
// 真正堆着 838 文件 / 17.4GB 的那份目录从未被清理,按钮却报"清理完成"。
//
// 判据:所有**实际存在**的布局都要清到,标准布局与 shared 布局各有一批残留时
// 一个都不能漏;仍被 sidecar 引用的 .pt 在任何布局下都必须保留。
func TestCleanupCoversSharedModelLayout(t *testing.T) {
	s := newTestServer(t)
	g := s.cfg.Get()

	// 两种布局各放一份(现实里就是这样:换布局后旧目录的残留还在)
	ptDirs := []string{
		filepath.Join(g.Comfy.RootDir, "ComfyUI", "models", "conditioning"),
		filepath.Join(g.Comfy.RootDir, "ComfyUI", "shared", "models", "conditioning"),
	}
	// 活缓存要有**对应的项目**才叫"活":③ 会先把没有项目的 sidecar 目录当孤儿清掉
	// (连同它引用的 .pt)—— 那是正确行为,所以这里必须建一个真项目。
	proj, err := s.store.Create("条件缓存清理测试", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	liveName := "s1_live0001"
	condDir := filepath.Join(s.dataDir(), "cond_cache", "manjux_"+proj.ID)
	if err := os.MkdirAll(condDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(condDir, liveName+".ok"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i, dir := range ptDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		orphan := filepath.Join(dir, "s9_orphan000"+strconv.Itoa(i)+".pt")
		if err := os.WriteFile(orphan, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, liveName+".pt"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if code, _ := doReq(t, s, "POST", "/api/cleanup", map[string]any{}); code != 200 {
		t.Fatalf("清理应 200,实际 %d", code)
	}

	for i, dir := range ptDirs {
		orphan := filepath.Join(dir, "s9_orphan000"+strconv.Itoa(i)+".pt")
		if _, err := os.Stat(orphan); !os.IsNotExist(err) {
			t.Errorf("布局 %s 里的孤儿 .pt 应被清理(这正是 17.4GB 滞留的位置)", dir)
		}
		if _, err := os.Stat(filepath.Join(dir, liveName+".pt")); err != nil {
			t.Errorf("布局 %s 里被 sidecar 引用的 .pt 不得删除", dir)
		}
	}
}

// TestComfyModelDirsFindsEveryLayout 目录解析本身要覆盖各布局,包括
// extra_model_paths.yaml 里显式映射的位置(用户换布局时改的就是它)。
func TestComfyModelDirsFindsEveryLayout(t *testing.T) {
	root := t.TempDir()
	want := []string{
		filepath.Join(root, "ComfyUI", "models", "conditioning"),
		filepath.Join(root, "ComfyUI", "shared", "models", "conditioning"),
		filepath.Join(root, "shared", "models", "conditioning"),
	}
	for _, d := range want {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// yaml 里再映射一个自定义位置
	custom := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ComfyUI"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "shared_current:\n    base_path: " + filepath.ToSlash(custom) + "\n    conditioning: pt-cache/\n"
	if err := os.WriteFile(filepath.Join(root, "ComfyUI", "extra_model_paths.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(custom, "pt-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(custom, "models", "conditioning"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := comfyModelDirs(root, "conditioning")
	joined := strings.Join(got, "|")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("应包含布局 %s,实际 %v", w, got)
		}
	}
	if !strings.Contains(joined, filepath.Join(custom, "pt-cache")) {
		t.Errorf("应解析 extra_model_paths.yaml 的自定义映射,实际 %v", got)
	}
	if !strings.Contains(joined, filepath.Join(custom, "models", "conditioning")) {
		t.Errorf("应包含 yaml 段 base_path 下的默认模型目录,实际 %v", got)
	}
	// 不存在的目录不得出现在结果里(否则清理会对着空路径报"已清理")
	if strings.Contains(joined, filepath.Join(root, "models", "conditioning")) {
		t.Errorf("不存在的目录不该被返回:%v", got)
	}
	// 空 root 不得 panic
	if dirs := comfyModelDirs("", "conditioning"); dirs != nil {
		t.Errorf("空 root 应返回 nil,实际 %v", dirs)
	}
}

// TestComfyRunDirsBothShapes output/input 可能在本体目录,也可能直接在 root 下。
func TestComfyRunDirsBothShapes(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "ComfyUI", "input")
	b := filepath.Join(root, "input")
	for _, d := range []string{a, b, filepath.Join(root, "ComfyUI", "output")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ins := comfyRunDirs(root, "input")
	if len(ins) != 2 {
		t.Fatalf("两种布局的 input 都应被找到,实际 %v", ins)
	}
	outs := comfyRunDirs(root, "output")
	if len(outs) != 1 || outs[0] != filepath.Join(root, "ComfyUI", "output") {
		t.Errorf("output 应只找到存在的那一份,实际 %v", outs)
	}
	if got := comfyRunDirs("", "input"); got != nil {
		t.Errorf("空 root 应返回 nil,实际 %v", got)
	}
}

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
