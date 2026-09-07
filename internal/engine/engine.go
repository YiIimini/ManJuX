// Package engine 定义渲染引擎抽象。新增引擎(如未来 VDN-H3、其它云端)
// 只需实现 Engine 接口并注册,管线与 UI 无需改动。
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// RefImage 一张参考图(本地绝对路径)。
type RefImage struct {
	Kind string // character / scene / prop / first_frame
	Path string
}

// Job 单镜渲染任务。
type Job struct {
	ProjectID   string
	ShotID      int
	Prompt      string // 完整 H3 提示词(LLM 直出)
	Mode        string // ref2va / t2va
	RefImages   []RefImage
	Width       int
	Height      int
	Length      int // 帧数(17k+5 网格,engine 自行对齐)
	Seed        int64
	Steps       int
	OutputDir   string // 产物落盘目录(项目 clips/)
	OutputName  string // 产物文件名主干(如 shot_001)
	OnProgress  func(percent int, note string)
}

// Result 渲染结果。
type Result struct {
	VideoPath string // 本地绝对路径
	TaskID    string // 云端任务 id(如有)
	Engine    string
	Cached    bool // 命中缓存/断点续跑
}

// Engine 渲染引擎。
type Engine interface {
	Name() string
	// Available 探测可用性:不可用时返回带修复指引的错误。
	Available(ctx context.Context) error
	Render(ctx context.Context, job Job) (*Result, error)
}

// Registry 引擎注册表。
type Registry struct {
	mu      sync.RWMutex
	engines map[string]Engine
	order   []string
}

// NewRegistry 创建注册表。
func NewRegistry() *Registry {
	return &Registry{engines: map[string]Engine{}}
}

// Register 注册引擎(name 唯一)。
func (r *Registry) Register(e Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.engines[e.Name()]; dup {
		return
	}
	r.engines[e.Name()] = e
	r.order = append(r.order, e.Name())
}

// Get 按名取引擎。
func (r *Registry) Get(name string) (Engine, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.engines[name]
	if !ok {
		return nil, fmt.Errorf("未知引擎: %s(可用: %s)", name, strings.Join(r.order, ", "))
	}
	return e, nil
}

// Names 返回注册顺序的引擎名列表。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	sort.Strings(out)
	return out
}

// AlignLength 将期望帧数对齐到 H3 的 17k+5 网格(Go 负数取模须归一)。
func AlignLength(base int) int {
	if base < 5 {
		base = 5
	}
	delta := (5 - base%17 + 17) % 17
	return base + delta
}

// SnapCanvas 将宽/高对齐到 32 的倍数(不低于 32)。
func SnapCanvas(w, h int) (int, int) {
	snap := func(v int) int {
		if v < 32 {
			return 32
		}
		return v / 32 * 32
	}
	return snap(w), snap(h)
}
