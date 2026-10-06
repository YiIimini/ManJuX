package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// fakeEngine_test.go 渲染编排层的**测试替身**（2026-09-29 代码健康审计 P0-1）。
//
// ## 为什么必须有它
//
// `runRender` 512 行、`stages_render.go` 里 486 条可测语句只覆盖了 85 条（17.5%），
// 而那 85 条**全部落在主函数之后抽出去的纯函数里**（种子派生、接缝判定、产物校验…）。
// `runRender` 函数体内 **0 个已覆盖语句块** —— 也就是"哪个判据在什么顺序、传了什么参数"
// 这层接线完全没有测试。
//
// 审计的原话是："这是夹具债不是架构债"：`Coordinator` 的字段本来就都可注入
// （`store`/`cfg`/`reg`/`ff`/`lib`），`Registry.Register` 也是公开的 —— 唯一缺的就是
// 一个 Engine 实现。这就是那个实现。
//
// ## 它能做的事
//
// 按"第 N 次调用"决定返回什么：成功、超时、业务失败、返回空产物……
// 于是可以钉住"三级降级阶梯到底怎么走""步数钳制在什么条件下生效""断点续跑跳过哪些镜"。
type fakeEngine struct {
	name string

	mu    sync.Mutex
	calls []fakeCall // 每次 Render 的记录（顺序即真实调用顺序）
	// 计划：第 N 次调用（1 基）返回什么；超出计划长度一律成功
	plan []fakeResult
	// 每次 Render 的行为（默认写一个占位文件，让产物校验通过）
	writeFile bool
}

type fakeCall struct {
	ShotID    int
	Prompt    string
	RefCount  int
	Steps     int
	Accel     string
	Seed      int64
	Mode      string
	OutputDir string
	// Length 引擎拿到的**帧数**（2026-10-06 加）：降级阶梯第二级"缩短到 6 秒再出"
	// 只改时长，帧数是它唯一的可观测证据（提示词本身在单小镜头时不会变）。
	Length int
}

type fakeResult struct {
	err      error
	emptyOut bool // 返回成功但不写产物（模拟"引擎说好了、文件没出来"）
	delay    time.Duration
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{name: "comfy", writeFile: true}
}

// withPlan 设定"第 N 次调用"的返回（1 基，顺序即调用顺序）。
func (f *fakeEngine) withPlan(rs ...fakeResult) *fakeEngine {
	f.plan = rs
	return f
}

func (f *fakeEngine) Name() string { return f.name }

func (f *fakeEngine) Available(ctx context.Context) error { return nil }

func (f *fakeEngine) Render(ctx context.Context, job engine.Job) (*engine.Result, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, fakeCall{
		ShotID: job.ShotID, Prompt: job.Prompt, RefCount: len(job.RefImages),
		Steps: job.Steps, Accel: job.Accel, Seed: job.Seed, Mode: job.Mode,
		OutputDir: job.OutputDir, Length: job.Length,
	})
	var r fakeResult
	if n < len(f.plan) {
		r = f.plan[n]
	}
	write := f.writeFile
	f.mu.Unlock()

	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	out := filepath.Join(job.OutputDir, job.OutputName+".mp4")
	if write && !r.emptyOut {
		if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
			return nil, err
		}
		// 内容无所谓（真机是视频），但**大小必须非 0** —— 产物校验按字节数判有效性
		if err := os.WriteFile(out, []byte("fake-mp4-payload"), 0o644); err != nil {
			return nil, err
		}
	}
	return &engine.Result{VideoPath: out, Engine: f.name, ActualFrames: job.Length}, nil
}

// callsOf 取第 i 次调用（1 基），没有则返回零值。
func (f *fakeEngine) callOf(i int) fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i-1 < len(f.calls) {
		return f.calls[i-1]
	}
	return fakeCall{}
}

func (f *fakeEngine) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// —— 其余接口方法：测试里用不到，给安全空实现 ——

func (f *fakeEngine) FreeVRAM(ctx context.Context) error { return nil }
func (f *fakeEngine) ClearQueue(ctx context.Context)     {}
func (f *fakeEngine) Interrupt(ctx context.Context)      {}

func (f *fakeEngine) GenerateImage(ctx context.Context, prompt, neg string, seed int64, w, h int, outPath string) (string, error) {
	if err := os.WriteFile(outPath, []byte("fake-png"), 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

func (f *fakeEngine) GenerateMusic(ctx context.Context, job engine.MusicJob) (string, error) {
	return "", fmt.Errorf("fake: 不支持配乐")
}

func (f *fakeEngine) Upscale(ctx context.Context, srcVideoPath string, opt engine.UpscaleOptions, length int, fps float64, outName, outPath string, timeout time.Duration) (string, error) {
	if err := os.WriteFile(outPath, []byte("fake-2x"), 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}
