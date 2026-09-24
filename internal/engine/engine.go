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
	ProjectID  string
	ShotID     int
	Prompt     string // 完整 H3 提示词(LLM 直出)
	Mode       string // ref2va / t2va
	RefImages  []RefImage
	Width      int
	Height     int
	Length     int // 帧数(17k+5 网格,engine 自行对齐)
	Seed       int64
	Steps      int
	Accel      string // 加速档: quality(Acc-8Step) / turbo(默认) / express(极速预览)
	OutputDir  string // 产物落盘目录(项目 clips/)
	OutputName string // 产物文件名主干(如 shot_001)
	OnProgress func(percent int, note string)

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

// UpscaleOptions 2× 放大后处理的参数。
//
// 放在 engine 包而不是具体引擎里:与 Job/RefImage 同理 —— 一旦类型定义在
// comfyengine,server 层就必须 import 具体引擎才能构造参数,
// 「把具体引擎的能力泄漏到上层」的老问题会以另一种形式回来。
type UpscaleOptions struct {
	// Type 放大方式:
	//   "ltx"    隐空间 2× —— 把已解码的画面重编码到 LTX VAE 空间做学习式空间放大,
	//            细节更自然,是"保音频高清化"的成熟做法;
	//   "esrgan" 帧级 2× —— 逐帧超分,快但对已干净素材更稳妥,容易放大噪点。
	Type string
	// ModelName 模型文件名。**含义随 Type 而变**:ltx 用 latent_upscale_models/ 下的,
	// esrgan 用 upscale_models/ 下的 —— 换 Type 时必须同时换它(用 UpscaleOptionsFor)。
	ModelName string
	// LTXVAE LTX 的视频 VAE 文件名(仅 ltx 方式需要;留空即用 DefaultLTXUpscaleVAE)。
	// 与 ModelName 一样是**文件名**而不是路径:ComfyUI 只会按文件名在自己配置的
	// 模型目录里找,给绝对路径反而匹配不上。
	LTXVAE string
}

// 放大链路的默认权重名。放在 engine 包而不是各调用方:这是**引擎侧的事实** ——
// 它们决定 ComfyUI 的模型目录里必须有什么,前端只负责回显与让用户覆盖。
//
// 此前这些文件名只存在于用户的输入框或调用方的字面量里,等于让用户替引擎记常量:
// 打错一个字,报错要等 ComfyUI 执行到那个节点才浮出来。
const (
	// DefaultESRGANUpscaleModel 帧级超分权重,放在 models/upscale_models/。
	DefaultESRGANUpscaleModel = "4x-UltraSharp.safetensors"
	// DefaultLTXUpscaleModel 隐空间放大权重,放在 models/latent_upscale_models/。
	DefaultLTXUpscaleModel = "ltx-2-spatial-upscaler-x2-1.0.safetensors"
	// DefaultLTXUpscaleVAE LTX 视频 VAE,放在 models/vae/。
	// 注意 Kijai 仓库里还有个 LTX2_video_vae_old_bf16:那是作者注明"抽错了"的旧版,
	// 正确且细节更好的是不带 old 的这个。
	DefaultLTXUpscaleVAE = "LTX2_video_vae_bf16.safetensors"
)

// UpscaleOptionsFor 返回某种放大方式的默认参数。
//
// 必须**按方式**给默认值,不能只给一份公共默认:`ModelName` 的含义随方式而变 ——
// ltx 要去 latent_upscale_models/ 里找,esrgan 要去 upscale_models/ 里找。
// 只把 Type 换成 ltx 而不换权重名,隐空间加载器就会拿着帧级超分的文件名去找,
// 必然失败(实测踩到:界面选「隐空间」时正是这样,两个字段合起来才是一条能跑的路)。
func UpscaleOptionsFor(kind string) UpscaleOptions {
	if kind == "ltx" {
		return UpscaleOptions{
			Type:      "ltx",
			ModelName: DefaultLTXUpscaleModel,
			LTXVAE:    DefaultLTXUpscaleVAE,
		}
	}
	return UpscaleOptions{
		Type:      "esrgan",
		ModelName: DefaultESRGANUpscaleModel,
	}
}

// DefaultUpscaleOptions 返回放大的默认参数。
//
// 默认走 **esrgan** 而不是 ltx:esrgan 只依赖一个 upscale_models 权重,
// 不需要额外备齐 LTX 那套 2.3GB 视频 VAE,速度也更快 —— 对已经定稿的成片更保险
// (逐帧超分对噪点的放大比隐空间放大更可控)。
func DefaultUpscaleOptions() UpscaleOptions { return UpscaleOptionsFor("esrgan") }

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
	// Upscale 对已完成的镜头做 2× 放大后处理(必须保留原音轨)。
	// 与上面几项同属可选能力:云端引擎不支持,给出带指引的错误。
	Upscale(ctx context.Context, srcVideoPath string, opt UpscaleOptions, length int, fps float64, outName, outPath string, timeout time.Duration) (string, error)
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
