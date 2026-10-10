package sysexec

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 【2026-10-10 真机咬到】`internal/pipeline` 整包 `go test` 卡到 600s / 900s，栈停在
// `os/exec` 的 `io.copyBuffer` —— 这是 Go 的经典坑：**孙进程握着 stdout 管道不放**。
// 工具链是 `asr.cmd`(cmd.exe) → `python.exe`，ctx 到点被杀的是 cmd.exe，python 还活着，
// 于是 `CombinedOutput` / `Wait` 会一直等"管道读完"。
//
// 判据分两层（AGENTS：判据要直接喂闸门本体）：
//
//	① Hide **本体**必须把 WaitDelay 设上（不是"某处调用了它"）；
//	② **行为**：真的造一个"握着管道不放的孙进程"，`CombinedOutput` 必须在几秒内返回，
//	   而不是等孙进程自然结束（这里造它活 30 秒）。
//
// 反向验证：把 Hide 里的 WaitDelay 去掉 ⇒ ② 会等到 30 秒 ⇒ 超时断言变红。
func TestHideSetsWaitDelay(t *testing.T) {
	cmd := Hide(exec.Command("cmd", "/c", "echo hi"))
	if cmd.WaitDelay <= 0 {
		t.Fatalf("Hide 必须给 Wait 一个上限（否则孙进程握着管道就能把整条链路吊死），实际 %v", cmd.WaitDelay)
	}
	if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags != createNoWindow {
		t.Fatalf("隐藏窗口那两个开关也得在：%+v", cmd.SysProcAttr)
	}
}

// TestWaitDoesNotHangWhenGrandchildHoldsPipe 行为判据：孙进程握着 stdout 不放时不许吊死。
//
// 造法（cmd 自带能力，不引依赖）：父 `cmd.exe` 用 `start "" /b` 起一个**后台孙进程**
// （`ping -n 30` ≈ 30 秒），孙进程继承父进程的 stdout（也就是我们的管道），父进程立刻
// `echo GRANDCHILD-OK` 退出。没有 WaitDelay 时 `CombinedOutput` 会一直等到孙进程结束（≈30s）；
// 有了 WaitDelay（3s）就在进程退出后几秒内返回，并把已读到的输出交出来。
//
// 脚本写成临时 **.cmd 文件**再跑：`cmd /c <多行字符串>` 不会按行执行（实测那条路 echo 都没有），
// 而真实工具链就是"一个 .cmd 文件"（`D:\Ai\Tools\ASR\asr.cmd`）—— 夹具照真实形态写。
func TestWaitDoesNotHangWhenGrandchildHoldsPipe(t *testing.T) {
	if testing.Short() {
		t.Skip("依赖真实子进程计时，-short 下跳过")
	}
	script := "@echo off\r\n" +
		"start \"\" /b cmd /c \"ping -n 30 127.0.0.1 >nul\"\r\n" +
		"echo GRANDCHILD-OK\r\n"
	path := filepath.Join(t.TempDir(), "grandchild_holds_pipe.cmd")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := Hide(exec.Command("cmd", "/c", path))
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	// WaitDelay 触发时 Go 返回 ErrWaitDelay（进程已退出、I/O 没读完）—— 那正是预期形态。
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("不该有别种失败：%v（输出 %q）", err, string(out))
	}
	if !strings.Contains(string(out), "GRANDCHILD-OK") {
		t.Errorf("自己那行输出必须读到了：%q", string(out))
	}
	if elapsed > 15*time.Second {
		t.Errorf("孙进程握着管道时等成了 %v —— WaitDelay 没生效（孙进程要活 30 秒，正常应 ≤ 10 秒返回）", elapsed)
	}
	t.Logf("孙进程握着管道，%v 返回（孙进程本身要活 30 秒）", elapsed)
}
