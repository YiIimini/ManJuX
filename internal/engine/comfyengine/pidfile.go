// pidfile.go ComfyUI 代管状态持久化:启动时把 python PID 落盘,
// 应用重启后按 PID 接管(验证进程身份),不再把上实例启动的引擎误判为
// 「外部启动」而失去管理权(停止/退出同步退出全部失效的根因)。
package comfyengine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	gopsproc "github.com/shirou/gopsutil/v4/process"
)

func pidFile(c config.ComfySettings) string {
	return filepath.Join(sidecarRoot(c), "comfyui.pid")
}

func writePidFile(c config.ComfySettings, pid int) {
	_ = os.MkdirAll(filepath.Dir(pidFile(c)), 0o755)
	_ = os.WriteFile(pidFile(c), []byte(strconv.Itoa(pid)), 0o644)
}

func clearPidFile(c config.ComfySettings) { _ = os.Remove(pidFile(c)) }

// adoptFromPidFile 尝试按 pid 文件接管引擎:PID 存活且映像名为 python*
// 才认领(托盘/任务管理器杀掉后残留文件不误接管)。成功返回 true。
func (e *Engine) adoptFromPidFile() bool {
	if e.proc != nil {
		return true
	}
	raw, err := os.ReadFile(pidFile(e.cfg()))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidFile(e.cfg()))
		return false
	}
	p, err := gopsproc.NewProcess(int32(pid))
	if err != nil {
		_ = os.Remove(pidFile(e.cfg())) // 进程不存在:清残留
		return false
	}
	name, err := p.Name()
	if err != nil || !strings.Contains(strings.ToLower(name), "python") {
		// PID 被其它程序复用:不认领,清文件
		_ = os.Remove(pidFile(e.cfg()))
		return false
	}
	// 认领:os.FindProcess 拿句柄供 Kill 用
	if osP, err := os.FindProcess(pid); err == nil {
		e.proc = osP
		return true
	}
	return false
}

// killProc 结束进程并等待退出(Kill 后 Wait 释放资源;超时 5s 放弃)。
func killProc(p *os.Process) error {
	if err := p.Kill(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		_, _ = p.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return nil
}
