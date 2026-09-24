package comfyengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// fakeComfy 起一个只实现 UpscaleAssets 会问到的端点的假 ComfyUI。
//
// combos 描述"ComfyUI 侧装了什么",键是 `节点类.输入名`,值是可选文件名列表;
// 某个节点类一个键都没有,就等于那个节点不存在(version 太旧)。
func fakeComfy(t *testing.T, combos map[string][]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/system_stats":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"system":{"comfyui_version":"test"}}`))
		case strings.HasPrefix(r.URL.Path, "/object_info/"):
			class := strings.TrimPrefix(r.URL.Path, "/object_info/")
			required := map[string]json.RawMessage{}
			for key, list := range combos {
				parts := strings.SplitN(key, ".", 2)
				if len(parts) != 2 || parts[0] != class {
					continue
				}
				def, _ := json.Marshal([]any{list, map[string]any{}})
				required[parts[1]] = def
			}
			w.Header().Set("Content-Type", "application/json")
			if len(required) == 0 {
				_, _ = w.Write([]byte(`{}`)) // 节点不存在
				return
			}
			out, _ := json.Marshal(map[string]any{
				class: map[string]any{"input": map[string]any{"required": required}},
			})
			_, _ = w.Write(out)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newFakeEngine(t *testing.T, combos map[string][]string) (*Engine, *httptest.Server) {
	t.Helper()
	ts := fakeComfy(t, combos)
	t.Cleanup(ts.Close)
	base := ts.URL
	return New(func() config.ComfySettings { return config.ComfySettings{BaseURL: base} }), ts
}

// TestUpscaleAssets 放大后处理的权重体检。
//
// 这条测试守的是一个实测踩到的洞:ltx 链路在图与代码层面早就齐备、也有图契约
// 测试,但本机 ComfyUI 只装了 latent upscaler 权重、没装配套的 LTX 视频 VAE,
// 于是这条路**永远跑不通**,而报错要等 ComfyUI 执行到那个节点才以原始报文抛出
// 来。图契约测试证明不了"这台机器上的权重够不够"—— 权重属于环境,只能对着
// 引擎问。这里用假 ComfyUI 把"够/不够"的判定逻辑钉死。
func TestUpscaleAssets(t *testing.T) {
	const (
		ltxModel = "ltx-2-spatial-upscaler-x2-1.0.safetensors"
		ltxVAE   = "LTX2_video_vae_bf16.safetensors"
		esrModel = "4x-UltraSharp.safetensors"
	)
	ltx := engine.UpscaleOptions{Type: "ltx", ModelName: ltxModel, LTXVAE: ltxVAE}
	esr := engine.UpscaleOptions{Type: "esrgan", ModelName: esrModel}

	full := map[string][]string{
		"VAELoader.vae_name":                  {"ae.safetensors", ltxVAE},
		"LatentUpscaleModelLoader.model_name": {ltxModel},
		"UpscaleModelLoader.model_name":       {esrModel},
	}

	cases := []struct {
		name    string
		combos  map[string][]string
		opt     engine.UpscaleOptions
		wantSub string // 期望出现在缺失说明里的字串("" 表示期望无缺失)
	}{
		{name: "ltx 两项权重都在", combos: full, opt: ltx, wantSub: ""},
		{name: "esrgan 权重在", combos: full, opt: esr, wantSub: ""},

		{
			// 就是本机实际踩到的那个:权重在、VAE 不在
			name:    "ltx 缺 VAE 要点出文件名与目录",
			combos:  map[string][]string{"VAELoader.vae_name": {"ae.safetensors"}, "LatentUpscaleModelLoader.model_name": {ltxModel}},
			opt:     ltx,
			wantSub: ltxVAE,
		},
		{
			name:    "ltx 缺隐空间权重要点出目录",
			combos:  map[string][]string{"VAELoader.vae_name": {ltxVAE}, "LatentUpscaleModelLoader.model_name": {}},
			opt:     ltx,
			wantSub: "latent_upscale_models",
		},
		{
			name:    "esrgan 缺超分权重要点出目录",
			combos:  map[string][]string{"UpscaleModelLoader.model_name": {}},
			opt:     esr,
			wantSub: "upscale_models",
		},
		{
			name:    "节点不存在时要说清是节点问题",
			combos:  map[string][]string{},
			opt:     ltx,
			wantSub: "VAELoader",
		},
		{
			name:    "文件名空白要直接点名",
			combos:  full,
			opt:     engine.UpscaleOptions{Type: "ltx", ModelName: ltxModel, LTXVAE: "  "},
			wantSub: "LTX 视频 VAE",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := newFakeEngine(t, c.combos)
			got := e.UpscaleAssets(context.Background(), c.opt)
			if c.wantSub == "" {
				if len(got) != 0 {
					t.Fatalf("权重齐备时不应报缺失,实际 %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("应报出缺失(期望提到 %q),实际为空 —— 这正是「跑进节点才失败」的来源", c.wantSub)
			}
			joined := strings.Join(got, " | ")
			if !strings.Contains(joined, c.wantSub) {
				t.Errorf("缺失说明应提到 %q,便于用户自查,实际:%s", c.wantSub, joined)
			}
		})
	}
}

// TestUpscaleAssetsEngineDown 引擎不可达时必须返回空。
//
// 与 Preflight 同一条原则:连不上 ≠ 资产缺失。若这里改报"缺文件",用户在
// ComfyUI 没起来时会被指着去装一个其实装好了的权重。
func TestUpscaleAssetsEngineDown(t *testing.T) {
	e, ts := newFakeEngine(t, map[string][]string{})
	ts.Close() // 关掉假服务:引擎随即不可达

	got := e.UpscaleAssets(context.Background(), engine.UpscaleOptions{
		Type: "ltx", ModelName: "x.safetensors", LTXVAE: "y.safetensors",
	})
	if len(got) != 0 {
		t.Errorf("引擎不可达时不应报资产缺失(那属于 Available 的职责),实际 %v", got)
	}
}
