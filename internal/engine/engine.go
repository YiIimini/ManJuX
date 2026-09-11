// Package engine 定义渲染引擎抽象。新增引擎(如未来 VDN-H3、其它云端)
// 只需实现 Engine 接口并注册,管线与 UI 无需改动。
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrRenderTimeout 渲染超时哨兵。管线据此决定「释放显存后原地重试一次」,
// 不再依赖错误文案里是否含「超时」二字 —— 调整文案不该悄悄改掉重试行为。
// 各引擎的超时错误都必须用 %w 包装本哨兵。
var ErrRenderTimeout = errors.New("渲染超时")

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
	Accel       string // 加速档: quality(Acc-8Step) / turbo(默认) / express(极速预览)
	OutputDir   string // 产物落盘目录(项目 clips/)
	OutputName  string // 产物文件名主干(如 shot_001)
	OnProgress  func(percent int, note string)

	// 镜间连续(MotionContext 接缝):Seam=true 且 PrevShot>0 时,
	// 加载上一镜保存的 AV latent 钉入条件,产物自动裁掉 pinned 头。
	Seam     bool
	PrevShot int

	// Timeout 单镜渲染超时;<=0 时各引擎回退自身默认值(本地 45 分钟 / 云端 20 分钟)。
	// 由管线从设置页「单镜渲染超时」透传 —— 此前该配置项被写死忽略,用户在设置页
	// 怎么改都不生效。
	Timeout time.Duration

	// Audios 音频参考(本地绝对路径)。H3 Ref2V 原生支持音频条件:喂入定稿配音后,
	// 人物的口型与动作节奏会跟随音频 —— 这是「音频驱动」(Audio Drive)的入口,
	// 对应精确台词路线:先把配音做成确定资产,再用它驱动画面。
	Audios []string
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

	// —— 通用可选能力:本地引擎实现,云端引擎给出安全空实现 ——
	// 它们原本要调用方断言 *comfyengine.Engine 才能调用,把具体引擎的能力
	// 泄漏到了管线层与 HTTP 层。提升为接口方法后调用方不再依赖具体类型。
	FreeVRAM(ctx context.Context) error
	ClearQueue(ctx context.Context)
	Interrupt(ctx context.Context)
	GenerateImage(ctx context.Context, prompt, neg string, seed int64, w, h int, outPath string) (string, error)
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
