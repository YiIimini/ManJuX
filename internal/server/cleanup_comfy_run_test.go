package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cleanup_comfy_run_test.go ComfyUI 运行时目录整扫（2026-09-27 用户指示）。
//
// 用户原话：「一键清理 还需要清理 D:\Ai\ComfyUI\ComfyUI\input 和 output 这两个目录」——
// 之前只清 `output/manjux/` 与 input 里 24 小时前的图片，别的全留着，所以"点了清理还占着几个 G"。
func TestSweepRunDirRemovesOldFiles(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "output")
	if err := os.MkdirAll(filepath.Join(out, "manjux"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	mk := func(rel string, mod time.Time) string {
		p := filepath.Join(out, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldPng := mk("ComfyUI_00001_.png", old) // 自己的出图：**要清**（此前漏的正是这类）
	oldJSON := mk("prompt.json", old)       // 非图片扩展名：**要清**（此前只清图片）
	oldNested := mk(filepath.Join("temp", "x.webp"), old)
	fresh := mk("just_now.png", time.Now())                         // 刚出的：不能动（正在跑的那一镜）
	manjux := mk(filepath.Join("manjux", "shot_1.mp4"), time.Now()) // 平台产物：整删（不论新旧）

	n, size := sweepRunDir(out, "output", time.Now().Add(-time.Hour))
	if n != 4 {
		t.Fatalf("应清 4 个：旧图 / 旧 json / 嵌套旧图 / manjux 整删，实际 %d", n)
	}
	if size <= 0 {
		t.Error("应统计出释放的字节数")
	}
	for _, gone := range []string{oldPng, oldJSON, oldNested, manjux} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("该被清掉却还在：%s", gone)
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("1 小时内的文件不能删（可能是正在跑的那一镜）")
	}
	// 空目录顺手删掉（不占空间，但会让人以为还有东西）
	if _, err := os.Stat(filepath.Join(out, "manjux")); err == nil {
		t.Error("清空后的子目录该删掉")
	}
}

// TestSweepRunDirKeepsInputSubdirs input 的子目录一律不动（可能是用户自己放的素材）。
func TestSweepRunDirKeepsInputSubdirs(t *testing.T) {
	root := t.TempDir()
	in := filepath.Join(root, "input")
	old := time.Now().Add(-5 * time.Hour)
	mk := func(rel string) string {
		p := filepath.Join(in, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, old, old)
		return p
	}
	flat := mk("ref_01.png")                   // 平铺的上传副本：清
	mine := mk(filepath.Join("我的素材", "a.png")) // 子目录：不动

	n, _ := sweepRunDir(in, "input", time.Now().Add(-time.Hour))
	if n != 1 {
		t.Fatalf("只该清平铺的那一个，实际 %d", n)
	}
	if _, err := os.Stat(flat); err == nil {
		t.Error("平铺的参考图副本该清掉")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Error("input 的子目录里可能是用户素材，一律不动")
	}
}

// TestSweepRunDirMissingDir 目录不存在时安静返回（ComfyUI 没装 / 路径不同都不该报错）。
func TestSweepRunDirMissingDir(t *testing.T) {
	n, size := sweepRunDir(filepath.Join(t.TempDir(), "nope"), "output", time.Now())
	if n != 0 || size != 0 {
		t.Errorf("不存在的目录应安静返回，实际 %d/%d", n, size)
	}
	if n, _ := sweepRunDir("", "output", time.Now()); n != 0 {
		t.Error("空路径应安静返回")
	}
}

// TestCleanupCoversComfyRunDirs 接线：一键清理必须真的调用整扫（别只留个函数没人用）。
func TestCleanupCoversComfyRunDirs(t *testing.T) {
	raw, err := os.ReadFile("cleanup.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, `for _, name := range []string{"output", "input"}`) {
		t.Error("一键清理要同时覆盖 output 与 input 两个目录")
	}
	if !strings.Contains(src, "sweepRunDir(dir, name, sweepCut)") {
		t.Error("要走到 sweepRunDir（整扫实现只有一份）")
	}
	if strings.Contains(src, "24 * time.Hour") {
		t.Error("旧的 24 小时窗口已被 1 小时整扫取代（留着就是两处各清一遍）")
	}
}
