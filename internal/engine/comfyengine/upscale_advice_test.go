package comfyengine

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// TestUpscaleExpectedScale 放大前的倍率预测(甲4 的引擎侧一半)。
//
// 引擎侧拿不到 ffprobe,**不能**在这里断言产物尺寸;它能做的是把"这次 2× 的承诺
// 建立在什么之上"说清楚:权重名可辨 → 图会精确收敛到 2×;不可辨 → 只是按 4× 假定。
// 调用方(server 的放大入口)据此在提交前播报,并在下载后做真正的尺寸断言。
func TestUpscaleExpectedScale(t *testing.T) {
	cases := []struct {
		name      string
		opt       UpscaleOptions
		wantRatio float64
		wantKnown bool
	}{
		{
			name:      "ltx 隐空间放大器本身就是 ×2 的空间模型",
			opt:       UpscaleOptions{Type: "ltx", ModelName: engine.DefaultLTXUpscaleModel, LTXVAE: engine.DefaultLTXUpscaleVAE},
			wantRatio: 2, wantKnown: true,
		},
		{
			name:      "esrgan 权重名带 4x:超分 4× 后收敛到 2×",
			opt:       UpscaleOptions{Type: "esrgan", ModelName: "4x-UltraSharp.safetensors"},
			wantRatio: 2, wantKnown: true,
		},
		{
			// 2× 权重不挂收敛节点(见 upscaleDownScale),预期仍然是 2×
			name:      "esrgan 权重名带 2x:本来就对,预期同样 2×",
			opt:       UpscaleOptions{Type: "esrgan", ModelName: "RealESRGAN_x2plus.pth"},
			wantRatio: 2, wantKnown: true,
		},
		{
			name:      "esrgan 权重名认不出倍数:预期是 2×,但只是假定",
			opt:       UpscaleOptions{Type: "esrgan", ModelName: "BSRGAN.pth"},
			wantRatio: 2, wantKnown: false,
		},
		{
			name:      "默认权重也必须认得出来(否则放大链路一上来就告警)",
			opt:       engine.DefaultUpscaleOptions(),
			wantRatio: 2, wantKnown: true,
		},
	}
	for _, c := range cases {
		ratio, known := UpscaleExpectedScale(c.opt)
		if ratio != c.wantRatio || known != c.wantKnown {
			t.Errorf("%s: UpscaleExpectedScale = (%v, %v),期望 (%v, %v)",
				c.name, ratio, known, c.wantRatio, c.wantKnown)
		}
	}
}

// TestUpscaleScaleAdviceOnlyWhenUncertain 告警只在"预测不可信"时出现。
//
// 两头都要钉:认得出时必须**安静**(否则每条放大都刷一行没人看的告警),
// 认不出时必须说清三件事 —— 哪个权重、按几倍假定、以及产物会被尺寸断言拦下
// (最后一条是用户排障时的线索,也是"这条告警不是拒绝服务"的说明)。
func TestUpscaleScaleAdviceOnlyWhenUncertain(t *testing.T) {
	quiet := []UpscaleOptions{
		{Type: "ltx", ModelName: engine.DefaultLTXUpscaleModel},
		{Type: "esrgan", ModelName: "4x-UltraSharp.safetensors"},
		{Type: "esrgan", ModelName: "RealESRGAN_x2plus.pth"},
	}
	for _, opt := range quiet {
		if note, warn := UpscaleScaleAdvice(opt); warn || note != "" {
			t.Errorf("%s/%s:倍数可辨时不该告警,实际 warn=%v note=%q", opt.Type, opt.ModelName, warn, note)
		}
	}

	note, warn := UpscaleScaleAdvice(UpscaleOptions{Type: "esrgan", ModelName: "remacri.pth"})
	if !warn {
		t.Fatal("权重名里没有倍数时必须告警:产物尺寸建立在假定之上")
	}
	for _, want := range []string{"remacri.pth", "假定", "2×", "尺寸断言"} {
		if !strings.Contains(note, want) {
			t.Errorf("告警文案应提到 %q,实际:%s", want, note)
		}
	}
}
