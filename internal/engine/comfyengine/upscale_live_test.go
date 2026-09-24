package comfyengine

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
)

// TestLiveUpscaleRoute 对着**真** ComfyUI 跑一次两条放大链路(默认跳过)。
//
// 为什么需要一条"要人工开引擎"的测试:
//
//	图契约测试只能证明**图**是对的,证明不了**这台机器上的权重**够不够。
//	实测就踩到过 —— ltx 链路在代码与图契约层面早就齐备,但本机缺配套的 LTX
//	视频 VAE,于是这条路永远跑不通;而且界面选「隐空间」时 ModelName 仍是帧级
//	超分的文件名,连权重目录都找错了。这两件事都只属于环境,unit test 看不见。
//
// 用法(本机 ComfyUI 需先起来,并且模型齐备):
//
//	$env:MANJUX_LIVE_COMFY = 'http://127.0.0.1:8190'
//	$env:MANJUX_LIVE_SRC   = 'C:\path\to\src.mp4'   # 宽高能被 32 整除,最好带音轨
//	go test ./internal/engine/comfyengine -run TestLiveUpscaleRoute -v -timeout 40m
//
// 源片建议 320x192 / 24fps / 2.5s(60 帧):60 不落在 LTX 的 8n+1 网格上,
// 顺带把"补尾帧再裁回"那段也验了。
func TestLiveUpscaleRoute(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("MANJUX_LIVE_COMFY"))
	if base == "" {
		t.Skip("未设置 MANJUX_LIVE_COMFY(如 http://127.0.0.1:8190),跳过真实放大验证")
	}
	src := strings.TrimSpace(os.Getenv("MANJUX_LIVE_SRC"))
	if src == "" {
		t.Skip("未设置 MANJUX_LIVE_SRC(源视频绝对路径),跳过真实放大验证")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("MANJUX_LIVE_SRC 不可读: %v", err)
	}

	e := New(func() config.ComfySettings { return config.ComfySettings{BaseURL: base} })
	ctx := context.Background()
	if !e.client().Alive(ctx) {
		// 显式指定了地址却连不上时不能静默跳过:那会让这条测试永远"绿",
		// 而它存在的全部意义就是真的跑一遍。
		t.Fatalf("已指定 MANJUX_LIVE_COMFY=%s 但连不上,请先启动 ComfyUI", base)
	}

	prober, err := ff.New(os.Getenv("MANJUX_LIVE_FFMPEG"), os.Getenv("MANJUX_LIVE_FFPROBE"))
	if err != nil {
		t.Skipf("找不到 ffmpeg/ffprobe,无法核对产物(可设 MANJUX_LIVE_FFMPEG / MANJUX_LIVE_FFPROBE):%v", err)
	}
	info, err := prober.Probe(src)
	if err != nil {
		t.Fatalf("探测源片失败: %v", err)
	}
	fps := info.FPS
	if fps <= 0 {
		fps = 24 // 与 server 层同一兜底口径
	}
	length := int(math.Round(info.Duration * fps))
	if length <= 1 {
		t.Fatalf("源片时长异常(%.2fs),无法确定帧数", info.Duration)
	}
	if info.Width%32 != 0 || info.Height%32 != 0 {
		t.Fatalf("源片尺寸 %dx%d 不能被 32 整除,LTX VAE 的空间下采样会算不整", info.Width, info.Height)
	}
	t.Logf("源片 %dx%d %d 帧 @ %.2ffps 音轨=%v", info.Width, info.Height, length, fps, info.HasAudio)

	outDir := t.TempDir()

	for _, tc := range []engine.UpscaleOptions{
		engine.DefaultUpscaleOptions(),
		engine.UpscaleOptionsFor("ltx"),
	} {
		t.Run(tc.Type, func(t *testing.T) {
			// 先体检权重:缺资产时给出的是"该放哪个文件到哪个目录",而不是
			// 一句 ComfyUI 原始报文。
			if miss := e.UpscaleAssets(ctx, tc); len(miss) > 0 {
				t.Fatalf("权重体检未通过(这台机器还缺资产):%s", strings.Join(miss, "; "))
			}

			out := filepath.Join(outDir, "live-"+tc.Type+"-x2.mp4")
			got, err := e.Upscale(ctx, src, tc, length, fps, "live-"+tc.Type, out, 20*time.Minute)
			if err != nil {
				t.Fatalf("放大失败(%s / %s):%v", tc.Type, tc.ModelName, err)
			}
			fi, err := os.Stat(got)
			if err != nil || fi.Size() == 0 {
				t.Fatalf("产物不存在或为空: %v (size=%v)", err, fi)
			}
			t.Logf("产物 %s(%d 字节)", got, fi.Size())

			oi, err := prober.Probe(got)
			if err != nil {
				t.Fatalf("探测产物失败: %v", err)
			}

			// ① 必须正好是 2×:隐空间那条靠模型、帧级那条靠 ImageScaleBy 收敛,
			//    两者都可能"差不多是 2×",这里要求精确。
			if oi.Width != info.Width*2 || oi.Height != info.Height*2 {
				t.Errorf("分辨率应为 %dx%d(源片 2×),实际 %dx%d",
					info.Width*2, info.Height*2, oi.Width, oi.Height)
			}

			// ② 时长不得漂移:ltx 会先补尾帧到 8n+1 再裁回,裁错就变长。
			if drift := math.Abs(oi.Duration - info.Duration); drift > 1.0/fps {
				t.Errorf("时长漂移超过 1 帧(源 %.3fs → 产物 %.3fs,差 %.3fs)",
					info.Duration, oi.Duration, drift)
			}

			// ③ 音轨必须还在:两条链路都是"重混源音轨",接错就静默丢声音。
			if info.HasAudio && !oi.HasAudio {
				t.Error("源片有音轨,产物却没有 —— 放大链路的音轨重混没生效")
			}
		})
	}
}
