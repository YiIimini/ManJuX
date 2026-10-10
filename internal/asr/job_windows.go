//go:build windows

package asr

import (
	"os/exec"
	"syscall"
	"unsafe"
)

// job_windows.go ASR 子进程要**连子孙一起清算**（2026-10-10）。
//
// 【为什么必须有它】ASR 工具链是 `asr.cmd`(cmd.exe) → `python.exe`（python 还可能再拉起解码器）。
// `exec.CommandContext` 在 ctx 到点/取消时只杀**直接子进程**（cmd.exe），python 会活下来继续占着
// 工作目录与 CPU：真机表现是
//
//   - 质检逐镜跑 ASR，某镜超时后**残留一个 python 在后台跑**（用户看到的是"停止后风扇还在转"）；
//   - 集成测试的临时目录清理当场失败：
//     `TempDir RemoveAll cleanup: ... projects\渲染接线测试\clips: The process cannot access the file
//     because it is being used by another process`（那一刻 python 的 cwd 正是 clips）。
//
// 修法用 Windows 的 **Job Object**（`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`）：把起出来的进程放进一个
// 作业对象，**关掉作业句柄时整棵树被内核杀掉**。这不需要遍历进程树、也不依赖父进程还活着。
//
// 边界（如实说）：这一步只挂在 ASR 这条链上（`Transcribe`）。全仓其它子进程仍是
// `CommandContext` 的"只杀直接子进程"语义 —— 要推广得逐个评估（例如 ComfyUI 是我们**故意**
// 留着长期跑的，不能一起收）。

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject          = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
)

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	_                       uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	_                       uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimitInformation struct {
	BasicLimitInformation jobBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// procJob 一个"连子孙一起杀"的作业对象（Close 即清算整棵树）。
type procJob struct{ h uintptr }

// Close 关掉作业句柄 ⇒ 内核杀掉作业里的所有进程（含孙进程）。幂等。
func (j *procJob) Close() {
	if j != nil && j.h != 0 {
		procCloseHandle.Call(j.h)
		j.h = 0
	}
}

// startInJob 起进程并把它放进 kill-on-close 的作业对象。
//
// 拿不到作业对象时**不失败**（照常返回已启动的进程）—— 这条链路的价值是"能收就收"，
// 不该因为 Job 建不出来就把一次语音核对判成失败。调用方只需 defer job.Close()。
func startInJob(cmd *exec.Cmd) (*procJob, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	j := &procJob{}
	h, _, _ := procCreateJobObject.Call(0, 0)
	if h == 0 {
		return j, nil
	}
	j.h = h
	info := jobExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	if r, _, _ := procSetInformationJobObject.Call(h, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); r == 0 {
		j.Close()
		return j, nil
	}
	ph, _, _ := procOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(cmd.Process.Pid))
	if ph == 0 {
		return j, nil
	}
	procAssignProcessToJobObject.Call(h, ph)
	procCloseHandle.Call(ph)
	return j, nil
}
