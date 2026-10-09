package sysexec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Hide 本身要真的把「无窗口」两个开关都设上（删一个就等于没隐藏）。
//
// 两个都要：`HideWindow` 管窗口是否显示，`CreationFlags = CREATE_NO_WINDOW` 让控制台程序**根本不创建**
// 窗口 —— 后者才是"闪黑窗"的根治（前者在部分场景下仍会先建再隐藏，看得到那一闪）。
func TestHideSetsBothNoWindowFlags(t *testing.T) {
	cmd := Hide(exec.Command("cmd", "/c", "echo"))
	if cmd.SysProcAttr == nil {
		t.Fatal("Hide 没设 SysProcAttr —— 子进程会照常弹控制台窗口")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow 没设上")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Errorf("CreationFlags 里没有 CREATE_NO_WINDOW（%#x）—— 控制台程序仍会先建窗再隐藏，看得到闪一下",
			cmd.SysProcAttr.CreationFlags)
	}
}

// 每一处 `exec.Command` / `exec.CommandContext` 都必须包在 `sysexec.Hide(...)` 里。
//
// 【为什么要有这道门（2026-10-09 用户：「质检时一直不停重复 显示/关闭 终端窗口」）】
// 平台是 windowsgui 程序：不加 CREATE_NO_WINDOW 的控制台子进程，**每次调用**都会在桌面上闪一个黑窗。
// 这条规矩本来就写在 `sysexec.go` 的文件头（"全部 exec 调用一律经 Hide() 包装,根治闪窗"），
// 但**散文规矩管不住代码** —— 真机报障时，29 处调用里有 2 处漏了包装，而且恰好都在质检链上：
//
//	qc 逐角色的人脸测量（`qc/face.go`）、逐镜的语音对账（`asr/asr.go`）
//
// ⇒ 质检一开就是十几次闪烁。所以补这道门：**漏一处就红**，而不是等用户看见窗口在闪。
//
// 只在**生产代码**上判（`_test.go` 不判）：测试本来就跑在控制台里，闪不闪无所谓。
// 本包自己是唯一入口，不判。
func TestEveryExecIsHidden(t *testing.T) {
	root := repoRootOf(t)
	selfDir := filepath.Join(root, "internal", "sysexec")
	var bad []string
	sites := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "data", "build", "out", "frontend", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		if filepath.Dir(p) == selfDir {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "exec.Command(") && !strings.Contains(line, "exec.CommandContext(") {
				continue
			}
			sites++
			if !strings.Contains(line, "sysexec.Hide(") {
				rel, _ := filepath.Rel(root, p)
				bad = append(bad, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描仓库失败：%v", err)
	}
	// 扫描本身要有效：写错路径/过滤条件时上面会"一处都没扫到"，那种绿是假的。
	if sites < 15 {
		t.Fatalf("只扫到 %d 处 exec 调用 —— 扫描失效了（别把这道门变成摆设）", sites)
	}
	if len(bad) > 0 {
		t.Errorf("这些子进程调用没经 sysexec.Hide —— 每次调用都会在桌面上闪一个终端窗口：\n%s",
			strings.Join(bad, "\n"))
	}
}

// repoRootOf 从测试的工作目录往上找 go.mod（找不到就**大声失败**，不许悄悄跳过）。
func repoRootOf(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取不到工作目录：%v", err)
	}
	for i := 0; i < 8; i++ {
		if _, serr := os.Stat(filepath.Join(dir, "go.mod")); serr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("往上找不到 go.mod —— 扫描起点不对")
	return ""
}
