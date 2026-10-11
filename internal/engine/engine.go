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

	// CondCache 两阶段渲染(条件缓存 .pt):项目参数优先,未表态(nil)时回退全局设置
	// 里的 comfy.cond_cache。
	//
	// 为什么要让项目说了算:项目参数抽屉里一直有这个开关(render.cond_cache,默认开),
	// 而真正决定行为的是全局设置 —— 用户关掉它,只要全局开着就照样写 .pt(该目录曾堆到
	// 4.9GB),反过来项目开着而全局关着也没人告知。属于"能点、能存、无日志、无效果"的
	// 假开关(见 pipeline/caps_params.go 的同名提示)。
	CondCache *bool
}

// Result 渲染结果。
type Result struct {
	VideoPath string // 本地绝对路径
	TaskID    string // 云端任务 id(如有)
	Engine    string
	Cached    bool // 命中缓存/断点续跑

	// ActualFrames 本次**实际产出**的帧数(24fps 口径),0 = 该引擎没给出。
	//
	// 与 Job.Length(计划帧数)不是一回事,两条路各有各的改写:
	//   本地 —— 提交长度必须对齐 17k+5 网格(计划 96 → 107 帧),接缝生效时还要扣掉
	//           被 Trim 裁掉的 22 帧 pinned 头(comfyengine.producedFrames);
	//   云端 —— 只吃整数秒且区间更窄,4 秒镜会被抬成 5 秒(h3api.submitDuration)。
	// 上层据此把"计划"与"实际"分开记账(乙6:云端 4 秒镜被抬成 5 秒),否则清单里
	// 只有计划值,两条路的产物时长差异永远对不上账。
	ActualFrames int
	// DurationAdjusted 实际时长是否**被引擎改写**过(网格量化、接缝裁剪、云端秒数钳制)。
	// true 不代表失败,只代表"实际 ≠ 计划",需要在界面上如实说明而不是装作一致。
	DurationAdjusted bool
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
	// GenerateMusic 生成一段配乐（本地引擎实现；云端给出带指引的错误）。
	//
	// 与 GenerateImage/Upscale 同属"可选能力"：调用方用 Caps.Music 先问能不能做，
	// 不能做时走"如实报告没有配乐"，而不是硬调用再报一个看不懂的错。
	GenerateMusic(ctx context.Context, job MusicJob) (string, error)
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

// H3TrainMinFrames / H3TrainMaxFrames H3 的**训练帧数区间**（官方节点自己写的那个区间）。
//
// 【为什么单列这两个数（2026-10-11，用户：「渲染出来的视频很不如意」）】ComfyUI 本体里的
// `nodes_minimax_h3.py` 把训练区间标注成 **124–362 帧（5.17–15.08 秒 @24fps）**。提交长度
// **低于 124 帧**的镜（例如计划 4 秒 ⇒ AlignLength(96) = **107 帧 = 4.46 秒**）不在训练区间内，
// 实测这类镜的画面在中后段更容易散（首帧正常、越走越糊/结构崩）。
//
// 平台**不能**把渲染窗口的下限从 4 秒抬上去：技能端旧包里有整批 4 秒镜（真产物夹具
// `internal/project/testdata/skill-produced-plan-ep01.json` 有 35 个），抬上去等于整本拒收 ——
// 那是契约版本级改动，要两侧同改同提交（详见 `internal/director/dialogue.go` 的说明；
// 生成侧另有 `GenMinShotSeconds = 5` 兜着新包）。所以这条只能**如实播报**：
// 判据与播报点见 `internal/pipeline` 的 `trainRangeNote`（用 AlignLength 算**真正提交**的帧数）。
const (
	H3TrainMinFrames = 124
	H3TrainMaxFrames = 362
)

// AlignLength 将期望帧数对齐到 H3 的 17k+5 网格(Go 负数取模须归一)。
func AlignLength(base int) int {
	if base < 5 {
		base = 5
	}
	delta := (5 - base%17 + 17) % 17
	return base + delta
}

// AlignLengthDown 向下取到 H3 的 17k+5 网格(已在网格上则原样返回)。
//
// 与 AlignLength(向上)的分工:向上用于"我要渲这么多帧"(宁多勿少,成片不会缩水);
// 向下用于"我要贴回一段**已存在**的素材"—— 局部重绘要拿 H3 的输出逐帧贴回源片,
// 帧数必须 ≤ 源片帧数,多一帧都会让贴回节点拿到比底片更长的结果
// (见 server 侧 maskFixFrames)。
func AlignLengthDown(base int) int {
	if base <= 5 {
		return 5
	}
	return 5 + (base-5)/17*17
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

// MusicJob 一次配乐生成的输入。
//
// 为什么类型放在 engine 包（而不是 comfyengine）：它是**接口的一部分** ——
// 云端引擎给安全空实现、本地引擎真正实现，调用方（管线）不该知道具体引擎类型。
type MusicJob struct {
	// Tags 风格/情绪/乐器/节奏的描述（ACE 的 tags 就是它的提示词）。
	Tags string
	// Seconds 目标时长（秒）。成片铺底会自动循环，所以不必等于成片长度。
	Seconds float64
	// BPM 速度；0 = 引擎默认（120）。
	BPM int
	// Seed 0 = 随机；非 0 可复现同一段曲子。
	Seed int64
	// OutputDir / OutputName 落点。
	OutputDir  string
	OutputName string
	// Timeout 0 = 引擎默认（首次要加载文本编码器，比出图慢得多）。
	Timeout time.Duration
}
