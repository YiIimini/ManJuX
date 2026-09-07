// Package sysmon 系统资源监控:CPU / RAM(gopsutil)+ GPU(nvidia-smi)。
//
// 顶栏中央状态条每 2s 拉一次快照;CPU 用增量采样(两次调用间的均值),
// GPU 走 nvidia-smi 查询(免管理员/免 cgo,N 卡通用),无 N 卡时置空。
package sysmon

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// Snapshot 一次采样结果。
type Snapshot struct {
	CPU      float64        `json:"cpu"`                // 百分比 0-100
	RAMUsed  uint64         `json:"ramUsed"`            // 字节
	RAMTotal uint64         `json:"ramTotal"`           // 字节
	RAMPct   float64        `json:"ramPct"`             // 百分比
	GPU      *GPUSnapshot   `json:"gpu,omitempty"`      // 无 N 卡时 nil
	CoreN    int            `json:"coreN"`              // 逻辑核心数
	TS       int64          `json:"ts"`                 // 采样时刻 unix 秒
}

// GPUSnapshot GPU 利用率与显存。
type GPUSnapshot struct {
	Name  string  `json:"name"`
	Util  float64 `json:"util"`  // 百分比
	VramU uint64  `json:"vramU"` // MiB
	VramT uint64  `json:"vramT"` // MiB
	Temp  float64 `json:"temp"`  // ℃
}

type Monitor struct {
	mu       sync.Mutex
	lastCall time.Time // cpu.Percent 增量采样基准
	gpuName  string    // 首次探测后缓存;无卡缓存 "-"
	probeAt  time.Time
}

var Default = &Monitor{}

// Sample 采一次快照(阻塞 <150ms)。
func (m *Monitor) Sample() *Snapshot {
	s := &Snapshot{TS: time.Now().Unix()}
	if n, err := cpu.Counts(true); err == nil {
		s.CoreN = n
	}
	m.mu.Lock()
	base := m.lastCall
	m.lastCall = time.Now()
	m.mu.Unlock()
	// interval=0:返回距上次调用的平均占用,恰好适配周期轮询;
	// 首次调用(无基准)给 500ms 阻塞采样,避免显示 0。
	var pct []float64
	var err error
	if base.IsZero() || time.Since(base) > 2*time.Minute {
		pct, err = cpu.Percent(500*time.Millisecond, false)
	} else {
		pct, err = cpu.Percent(0, false)
	}
	if err == nil && len(pct) > 0 {
		s.CPU = round1(pct[0])
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		s.RAMUsed, s.RAMTotal = vm.Used, vm.Total
		s.RAMPct = round1(vm.UsedPercent)
	}
	s.GPU = m.sampleGPU()
	return s
}

// sampleGPU nvidia-smi 查询;GPU 名缓存 10 分钟(卡不热插)。
func (m *Monitor) sampleGPU() *GPUSnapshot {
	m.mu.Lock()
	cachedName, probeAt := m.gpuName, m.probeAt
	m.mu.Unlock()
	if cachedName == "-" {
		return nil
	}
	args := []string{
		"--query-gpu=utilization.gpu,memory.used,memory.total,temperature.gpu,name",
		"--format=csv,noheader,nounits",
	}
	if cachedName != "" && time.Since(probeAt) < 10*time.Minute {
		args[0] = "--query-gpu=utilization.gpu,memory.used,memory.total,temperature.gpu"
	}
	out, err := sysexec.Hide(exec.Command("nvidia-smi", args...)).Output()
	if err != nil {
		// 只在「命令不存在」时判定无卡;瞬时失败仍返回 nil 不缓存,
		// 避免 ComfyUI 满载时偶发超时把 GPU 状态永久打灭。
		if execErr, ok := err.(*exec.Error); ok && execErr.Err == exec.ErrNotFound {
			m.mu.Lock()
			m.gpuName = "-"
			m.mu.Unlock()
		}
		return nil
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ", ")
	if len(fields) < 4 {
		return nil
	}
	g := &GPUSnapshot{
		Util:  parseF(fields[0]),
		VramU: parseU(fields[1]),
		VramT: parseU(fields[2]),
		Temp:  parseF(fields[3]),
	}
	if len(fields) >= 5 {
		g.Name = strings.TrimSpace(fields[4])
		m.mu.Lock()
		m.gpuName, m.probeAt = g.Name, time.Now()
		m.mu.Unlock()
	}
	return g
}

func parseF(s string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return round1(v) }
func parseU(s string) uint64  { v, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64); return v }
func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
