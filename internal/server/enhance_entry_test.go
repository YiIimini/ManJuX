package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 两项增强入口(局部重绘 / 音频驱动)的契约测试。
//
// 为什么单测落在这里:这两条链都是"跑十几分钟才出结果"的重活,入口传错一个字段
// (帧数、长度、超时)在真实链路上只会表现为"节点报错"或"产物时长不对",排查成本极高。
// 所以把"帧数/长度/超时到底怎么算"抽成纯函数,在这里穷举。

// TestMaskFixFramesUseSourceFrameCount 局部重绘的帧数必须来自**源片实测**,
// 而不是计划时长×24(审计:差 4~16 帧)。
//
// 4 秒镜的计划帧数是 96,而本地引擎实际产出是 AlignLength(96)=107 ——
// MVEx_SubjectUncrop 拿的是源片的全部 107 帧,给它 96 就会尺寸不匹配或尾部不修。
func TestMaskFixFramesUseSourceFrameCount(t *testing.T) {
	cases := []struct {
		name     string
		duration float64
		fps      float64
		want     int
	}{
		{"4 秒镜(本地引擎产出 107 帧)", 107.0 / 24, 24, 107},
		{"6 秒镜(144 → 158 帧)", 158.0 / 24, 24, 158},
		{"容器不报帧率时按 24 兜底", 107.0 / 24, 0, 107},
		{"云端 5 秒 120 帧:不在网格上,向下取网格", 5.0, 24, 107},
		{"正好在网格上原样返回", 124.0 / 24, 24, 124},
	}
	for _, c := range cases {
		got, err := maskFixFrames(&ff.ProbeInfo{Duration: c.duration, FPS: c.fps})
		if err != nil {
			t.Errorf("%s: 不该报错,实际 %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: maskFixFrames = %d,期望 %d", c.name, got, c.want)
		}
		if engine.AlignLength(got) != got {
			t.Errorf("%s: 提交给 H3 的帧数 %d 不在 17k+5 网格上", c.name, got)
		}
	}
	// 关键回归:计划值 96 绝不能出现在结果里
	if got, _ := maskFixFrames(&ff.ProbeInfo{Duration: 107.0 / 24, FPS: 24}); got == 96 {
		t.Error("帧数仍是计划时长×24(96):贴回节点会拿到比底片更短的结果")
	}
}

// TestMaskFixFramesSnapDownNeverExceedsSource 向下取网格后绝不能超过源片帧数 ——
// 超过就意味着 H3 要输出比底片更多的帧,贴回必然错位。
func TestMaskFixFramesSnapDownNeverExceedsSource(t *testing.T) {
	for n := 5; n <= 400; n++ {
		got, err := maskFixFrames(&ff.ProbeInfo{Duration: float64(n) / 24, FPS: 24})
		if err != nil {
			t.Fatalf("%d 帧的源片不该报错: %v", n, err)
		}
		if got > n {
			t.Errorf("源片 %d 帧 → 提交 %d 帧:超过源片长度", n, got)
		}
		if engine.AlignLength(got) != got {
			t.Errorf("源片 %d 帧 → 提交 %d 帧:不在 H3 的 17k+5 网格上", n, got)
		}
	}
}

// TestMaskFixFramesRejectBrokenSource 源片时长探测异常必须明确报错,
// 而不是蒙一个长度让 ComfyUI 跑十几分钟再在贴回节点上失败。
func TestMaskFixFramesRejectBrokenSource(t *testing.T) {
	for _, info := range []*ff.ProbeInfo{nil, {}, {Duration: 0.01, FPS: 24}} {
		if _, err := maskFixFrames(info); err == nil {
			t.Errorf("源片信息 %+v 应被拒绝", info)
		}
	}
}

// TestEnhanceJobCarriesLengthAndAudio 两个入口共用同一个 Job 组装:
// 帧数(Length)、音频、输出名、超时都必须落进去 —— 音频驱动此前完全没有 Length。
func TestEnhanceJobCarriesLengthAndAudio(t *testing.T) {
	ctx := &shotEnhanceCtx{
		proj:    &project.Project{ID: "pj"},
		shot:    &project.Shot{ID: 7, Duration: 6},
		outName: "shot_007_audiodrive",
		outPath: filepath.Join(t.TempDir(), "clips", "shot_007_audiodrive.mp4"),
		timeout: 60 * time.Minute,
	}
	rp := project.RenderParams{Width: 768, Height: 1344, Steps: 8, Seed: 42, Accel: "turbo"}
	j := enhanceJob(ctx, rp, "P", []engine.RefImage{{Kind: "character", Path: "c.png"}},
		144, []string{"a.wav"})

	if j.Length != 144 {
		t.Errorf("Length 应为本镜计划帧数 144,实际 %d(0 会让引擎按 AlignLength(0)=5 帧建图)", j.Length)
	}
	if len(j.Audios) != 1 || j.Audios[0] != "a.wav" {
		t.Errorf("音频参考必须原样带上,实际 %v", j.Audios)
	}
	if j.OutputName != "shot_007_audiodrive" || filepath.Dir(j.OutputDir) == "" {
		t.Errorf("输出目录/文件名不对:%q %q", j.OutputDir, j.OutputName)
	}
	if j.Timeout != 60*time.Minute {
		t.Errorf("超时应原样透传,实际 %v", j.Timeout)
	}
	if j.Width != 768 || j.Height != 1344 || j.Seed != 42 || j.Steps != 8 || j.Accel != "turbo" {
		t.Errorf("渲染参数透传不全:%+v", j)
	}
	if j.Mode != "ref2va" {
		t.Errorf("两项增强都走 ref2va,实际 %q", j.Mode)
	}
	// 局部重绘那一路没有音频
	if got := enhanceJob(ctx, rp, "P", nil, 107, nil); len(got.Audios) != 0 || got.Length != 107 {
		t.Errorf("局部重绘的 Job 不应带音频,长度应为源片帧数:%+v", got)
	}
}

// TestEnhanceTimeoutAddsChainGrace 增强链比一次整镜渲染更长(多出源片上传、
// SAM3 逐帧跟踪、贴回、下载),超时预算必须大于单镜渲染超时,否则合法的慢重绘会被
// 杀在"快贴回"那一步上,GPU 时间白烧。
func TestEnhanceTimeoutAddsChainGrace(t *testing.T) {
	base := 45 * time.Minute
	got := enhanceTimeout(base)
	if got <= base {
		t.Errorf("增强链路预算 %v 必须大于单镜渲染超时 %v", got, base)
	}
	if got > base+30*time.Minute {
		t.Errorf("宽限过大(%v):卡死的重绘会白等太久", got-base)
	}
	// 未配置(0)= 交回引擎兜底,不能返回"0 + 宽限"那种比兜底更短的值
	if got := enhanceTimeout(0); got != 0 {
		t.Errorf("未配置时应返回 0(交给引擎兜底),实际 %v", got)
	}
}

// TestMaskFixEnsuresEngineBeforePreflight M25(本分区的一半):MaskAssets 在引擎
// **不可达**时按设计返回空("不把连不上误判成缺文件"),所以必须先 Available 再体检 ——
// 否则冷启动时"缺节点/缺 SAM3 权重"的体检被静默跳过(返回空 = 通过),用户白等
// 引擎启动、再拿到原始 node_errors。放大那条链路早就这么做了。
//
// 这是**源码级**契约断言:该分支要真跑起来需要一台可达且缺权重的 ComfyUI;
// 顺序一旦被改回来,这条断言会立刻红。
func TestMaskFixEnsuresEngineBeforePreflight(t *testing.T) {
	raw, err := os.ReadFile("handlers_enhance.go")
	if err != nil {
		t.Fatalf("读取句柄文件失败: %v", err)
	}
	src := string(raw)
	// 只看 handleShotMaskFix 这一段(到下一个 handler 注释为止)
	start := strings.Index(src, "func (s *Server) handleShotMaskFix(")
	if start < 0 {
		t.Fatal("找不到 handleShotMaskFix:函数的形状变了,这条断言需要跟着更新")
	}
	body := src[start:]
	if end := strings.Index(body, "// handleShotUpscale"); end > 0 {
		body = body[:end]
	}
	avail := strings.Index(body, "ctx.engineCE.Available(")
	assets := strings.Index(body, "ctx.engineCE.MaskAssets(")
	if avail < 0 {
		t.Fatal("局部重绘入口必须先调 Available(否则冷启动时体检被静默跳过)")
	}
	if assets < 0 {
		t.Fatal("找不到 MaskAssets 体检调用")
	}
	if avail > assets {
		t.Error("Available 必须在 MaskAssets **之前**:体检在引擎不可达时一律返回空 = 静默通过")
	}
}

// TestEnhanceEntrypointsAssembleJobOnce 两个入口必须经由同一个 Job 组装点。
//
// 这条是**类级**回归屏障:同一份"漏传字段"的缺陷在这两个入口各发生过一次
// (音频驱动漏 Length、局部重绘传计划帧数),只要有人再往入口里手写 Job 字面量,
// 就会绕开 enhanceJob 与上面几条断言。
func TestEnhanceEntrypointsAssembleJobOnce(t *testing.T) {
	raw, err := os.ReadFile("handlers_enhance.go")
	if err != nil {
		t.Fatalf("读取句柄文件失败: %v", err)
	}
	src := string(raw)
	if n := strings.Count(src, "enhanceJob("); n < 3 { // 1 次定义 + 2 个调用点
		t.Errorf("两个增强入口都应调用 enhanceJob(定义+2 处调用),实际出现 %d 次", n)
	}
	if strings.Contains(src, "ctx.shot.Duration*24, rp.Width") {
		t.Error("局部重绘又用回了计划时长×24(应取源片实际帧数,见 maskFixFrames)")
	}
	for _, bad := range []string{"Length: 0", "Length:0"} {
		if strings.Contains(src, bad) {
			t.Errorf("入口里出现 %q:帧数必须由 enhanceJob 统一给出", bad)
		}
	}
}
