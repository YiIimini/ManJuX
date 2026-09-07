// Package watchdog 单实例看门狗:Windows 命名互斥量保证全系统仅一个 ManJuX 实例。
// 第二实例启动时检测到互斥量已存在 → 通过约定 URL 唤起既有实例的工作台后退出。
package watchdog

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	procCreateMu = kernel32.NewProc("CreateMutexW")
)

const mutexName = "Global\\ManJuX_SingleInstance_Mutex"

// Acquire 尝试获取单实例互斥量。成功返回释放函数;已在运行返回 err。
func Acquire() (release func(), err error) {
	namePtr, _ := syscall.UTF16PtrFromString(mutexName)
	handle, _, lastErr := procCreateMu.Call(0, uintptr(unsafe.Pointer(namePtr)), 0)
	if handle == 0 {
		return nil, fmt.Errorf("创建互斥量失败: %v", lastErr)
	}
	// CreateMutexW 已存在时 err=ERROR_ALREADY_EXISTS(183) 且 handle 有效
	if errno, ok := lastErr.(syscall.Errno); ok && errno == 183 {
		syscall.CloseHandle(syscall.Handle(handle))
		return nil, fmt.Errorf("ManJuX 已在运行")
	}
	return func() {
		syscall.CloseHandle(syscall.Handle(handle))
	}, nil
}

// IsSecondInstance 便捷判断。
func IsSecondInstance() bool {
	_, err := Acquire()
	if err != nil {
		return true
	}
	return false
}

// Halt 快速退出第二实例(保留给 main 使用)。
func Halt(msg string) {
	if msg != "" {
		// windowsgui 模式无控制台,静默退出即可(不弹窗打扰)
		_ = msg
	}
	os.Exit(0)
}
