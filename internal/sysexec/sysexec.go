// Package sysexec 统一给子进程加「无窗口」属性。
//
// ManJuX 是 windowsgui 程序:启动 python(ComfyUI)、ffmpeg/ffprobe、explorer 等
// 控制台子进程时,若不加 CREATE_NO_WINDOW,每次调用都会闪一个黑色终端窗
// (作品库 ffprobe 元数据、打开目录、ComfyUI 启动都会闪)。全部 exec 调用
// 一律经 Hide() 包装,根治闪窗。
package sysexec

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// Hide 返回带隐藏窗口属性的命令(原命令对象原地修改)。
func Hide(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	return cmd
}
