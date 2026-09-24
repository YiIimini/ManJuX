// killcomfy.go ComfyUI 进程清理:手动停止/直接退出必须把引擎进程清干净
// (曾只杀代管主 PID,子进程与外部同安装实例残留,导致停止后端口仍活/重启失败)。
// 策略:① 代管主进程整树强杀(taskkill /T /F);② 系统扫描 rootDir 下所有
// python 进程(独立环境 exe / 命令行含 ComfyUI main.py)一并结束,一个不留。
package comfyengine

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/sysexec"
	gopsproc "github.com/shirou/gopsutil/v4/process"
)

// killTree 强杀进程树(含全部子进程;taskkill 失败退回直接 Kill)。
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	// taskkill 是控制台程序,windowsgui 下不加 CREATE_NO_WINDOW 会闪黑窗
	if err := sysexec.Hide(exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")).Run(); err == nil {
		return
	}
	if osP, e := os.FindProcess(pid); e == nil {
		_ = osP.Kill()
	}
	log.Printf("[comfy] killTree %d 回退直接 Kill", pid)
}

// comfyCandidates 收集需要结束的进程:
//   - 代管主进程 pid(存活中,连同其子孙树)
//   - 全系统 python*:exe 位于 ComfyUI 根目录内(standalone-env 等),
//     或命令行命中 根目录/ComfyUI/main.py(外部手动 `python main.py` 启动)
//
// RootDir 为空/非法时只按代管 PID(绝不误杀无关 python)。
func comfyCandidates(c config.ComfySettings, managedPID int) []int {
	seen := map[int]bool{}
	add := func(pids ...int) {
		for _, p := range pids {
			if p > 0 {
				seen[p] = true
			}
		}
	}
	add(managedPID)
	root := strings.ToLower(filepath.Clean(c.RootDir))
	if root == "" || root == "." {
		return keys(seen)
	}
	procs, err := gopsproc.Processes()
	if err != nil {
		return keys(seen)
	}
	for _, p := range procs {
		name, err1 := p.Name()
		if err1 != nil || !strings.Contains(strings.ToLower(name), "python") {
			continue
		}
		hit := false
		if exe, err2 := p.Exe(); err2 == nil {
			hit = strings.HasPrefix(strings.ToLower(filepath.Clean(exe)), root)
		}
		if !hit {
			if cl, err2 := p.Cmdline(); err2 == nil {
				cll := strings.ToLower(cl)
				hit = strings.Contains(cll, strings.ToLower(filepath.Join(c.RootDir, "ComfyUI", "main.py")))
			}
		}
		if hit {
			add(int(p.Pid))
		}
	}
	return keys(seen)
}

func keys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// procAlive PID 是否仍存在。
func procAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := gopsproc.NewProcess(int32(pid))
	return err == nil
}

// killAllComfy 结束全部关联进程(树杀 + 系统扫描),清代管状态与 pid 文件。
// 返回被结束的进程数(含未存活的代管记录,仅为提示)。
func (e *Engine) killAllComfy(why string) int {
	c := e.cfg()
	managed := 0
	// 取一次快照并立刻固化成 int:避免两次读之间代管状态被其它 goroutine 改写
	if p := e.ownedProc(); p != nil {
		managed = p.Pid
	}
	pids := comfyCandidates(c, managed)
	for _, pid := range pids {
		killTree(pid)
	}
	// 等代管主进程从系统消失(句柄/资源释放);超时 6s 不强等
	if managed > 0 {
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) && procAlive(managed) {
			time.Sleep(120 * time.Millisecond)
		}
	}
	e.setOwnedProc(nil)
	clearPidFile(c)
	log.Printf("[comfy] %s:已结束 %d 个 ComfyUI 相关进程", why, len(pids))
	return len(pids)
}

// stopManagedOnly 仅停代管进程树(空闲自动退出用;外部同安装实例不碰)。
func (e *Engine) stopManagedOnly() error {
	p := e.ownedProc()
	if p == nil && !e.adoptFromPidFile() {
		return fmt.Errorf("ComfyUI 非本应用启动,不做空闲退出接管")
	}
	// 认领成功后进程句柄已重新写入,必须再取一次 —— 沿用上面的 nil 快照会空指针
	if p = e.ownedProc(); p == nil {
		return fmt.Errorf("ComfyUI 代管进程句柄丢失,无法停止")
	}
	pid := p.Pid
	killTree(pid)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && procAlive(pid) {
		time.Sleep(120 * time.Millisecond)
	}
	e.setOwnedProc(nil)
	clearPidFile(e.cfg())
	notifyLifecycle(false, "空闲自动退出")
	return nil
}
