// Package sysexec 统一给子进程加「无窗口」属性 + 「不许被孙进程吊死」的上限。
//
// ManJuX 是 windowsgui 程序:启动 python(ComfyUI)、ffmpeg/ffprobe、explorer 等
// 控制台子进程时,若不加 CREATE_NO_WINDOW,每次调用都会闪一个黑色终端窗
// (作品库 ffprobe 元数据、打开目录、ComfyUI 启动都会闪)。全部 exec 调用
// 一律经 Hide() 包装,根治闪窗。
//
// 【2026-10-10 追加：WaitDelay】真机咬到一次**整条链路吊死**：`internal/pipeline` 的
// `go test` 卡到 600s / 900s，栈停在 `os/exec` 的 `io.copyBuffer` ——
// 根因是 **孙进程握着 stdout 管道不放**：工具链是 `asr.cmd`(cmd.exe) → `python.exe`，
// ctx 到点被杀的是 **cmd.exe**，而 python / 它再拉起的解码器还活着，于是
// `CombinedOutput` / `Wait` 会一直等"管道读完"（Go 的 `Cmd.Wait` 语义就是这样）。
//
// 修法是给每个子进程一个上限（`WaitDelay`，Go 1.20+）：**进程已退出或 ctx 已取消**之后，
// 最多再等这么久就把管道关掉返回。正常路径完全不受影响（进程退出时管道自然关闭）；
// 只有"孙进程赖着管道"这一种情况会被截断 —— 而那正是我们要的。
// 调用方要认得 `exec.ErrWaitDelay`：它表示"进程已退出、I/O 没读完"，
// 结果若从**文件**取（ASR 与装配引擎都是），照样算跑成（见 asr.Transcribe / compose.engine）。
package sysexec

import (
	"os/exec"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

// waitDelay 进程退出/ctx 取消之后，最多再等这么久就关管道（见包注释）。
//
// 取 3 秒：真机实测 ASR 工具"写结果文件 → 退出"之间的间隔是毫秒级，
// 3 秒足够把"正常但慢一点"的收尾读干净，又不至于让整条链路看起来像卡死。
const waitDelay = 3 * time.Second

// Hide 返回带隐藏窗口属性的命令(原命令对象原地修改),并给 Wait 一个**抗孙进程吊死**的上限。
func Hide(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	// 上限只在"进程已退出/已取消、而管道仍被孙进程握着"时生效（见包注释）。
	cmd.WaitDelay = waitDelay
	return cmd
}
