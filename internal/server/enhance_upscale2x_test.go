package server

import (
	"os"
	"strings"
	"testing"
)

// 2× 放大入口的**产物尺寸断言**(甲4,2026-09-26 渲染管线审查)。
//
// 落点说明:断言必须在 server 侧 —— 引擎侧(comfyengine)拿不到 ffprobe,它只能按权重名
// 推倍数(comfyengine.UpscaleExpectedScale)。而"到底放大了没有"只有产物本身能回答:
// 旧实现把缩放写死 0.5,换 2× 权重时 2× → ×0.5 = 原分辨率,用户拿到一个与源片同尺寸的
// 文件,界面却报「2× 放大完成」。

// TestCheckUpscale2x 纯函数面:源尺寸 + 产物尺寸 → 通过/报错,四种边界穷举。
func TestCheckUpscale2x(t *testing.T) {
	cases := []struct {
		name                   string
		srcW, srcH, outW, outH int
		wantErr                bool
		wantWord               string
	}{
		{name: "768×1344 → 1536×2688:通过", srcW: 768, srcH: 1344, outW: 1536, outH: 2688},
		// 奇数尺寸:编码器会做 ±1 的取整,1px 容差内仍算通过
		{name: "1079×607 → 2158×1213(高度差 1px):通过", srcW: 1079, srcH: 607, outW: 2158, outH: 1213},
		{name: "差 1px 是容差上限:通过", srcW: 768, srcH: 1344, outW: 1537, outH: 2687},
		{name: "差 2px:不通过", srcW: 768, srcH: 1344, outW: 1538, outH: 2688, wantErr: true},

		// 最典型、也是这条断言存在的理由:换 2× 权重、旧实现 ×0.5 → 与源片一样大
		{
			name: "产物与源片同尺寸(根本没放大)", srcW: 768, srcH: 1344, outW: 768, outH: 1344,
			wantErr: true, wantWord: "尺寸相同",
		},
		{name: "只放大了 1.5×", srcW: 768, srcH: 1344, outW: 1152, outH: 2016, wantErr: true, wantWord: "2×"},
		{name: "放大了 4×(权重倍数没被收敛)", srcW: 768, srcH: 1344, outW: 3072, outH: 5376, wantErr: true, wantWord: "2×"},
		// 探测不到有效宽高时不能"当作通过":那等于没核对就报成功
		{name: "产物宽高读不到", srcW: 768, srcH: 1344, outW: 0, outH: 0, wantErr: true, wantWord: "无法核对"},
		{name: "源片宽高读不到", srcW: 0, srcH: 0, outW: 1536, outH: 2688, wantErr: true, wantWord: "无法核对"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkUpscale2x(c.srcW, c.srcH, c.outW, c.outH)
			if !c.wantErr {
				if err != nil {
					t.Fatalf("应通过,实际报错:%v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("应报错(静默交付一个尺寸不对的产物正是这条断言要拦的事)")
			}
			msg := err.Error()
			if c.wantWord != "" && !strings.Contains(msg, c.wantWord) {
				t.Errorf("错误文案应提到 %q,便于用户自查,实际:%s", c.wantWord, msg)
			}
			// 报错必须给出"源片 vs 期望 vs 实际"三个数,否则用户没法判断差在哪
			if !strings.Contains(msg, "×") {
				t.Errorf("错误文案应给出尺寸数字,实际:%s", msg)
			}
		})
	}
}

// TestUpscaleEntryAssertsExact2x 放大入口必须**在落盘之后、报成功之前**核对尺寸,
// 并把产物路径写进报错(产物是排查的物证,不能删了重来)。
//
// 为什么是源码级断言:这一段要真跑起来需要一台装了放大权重的 ComfyUI + 一段真视频,
// 单测里跑不起;而"断言有没有接进这条链路"必须有人守着 —— 它就是这道闸门本身。
func TestUpscaleEntryAssertsExact2x(t *testing.T) {
	src := readServerSource(t, "handlers_enhance.go")
	body := handlerBody(t, src, "func (s *Server) handleShotUpscale(", "// handleShotAudioDrive")

	engineCall := strings.Index(body, "ctx.engineCE.Upscale(")
	assertCall := strings.Index(body, "checkUpscale2x(")
	success := strings.Index(body, "writeJSON(w, 200")
	if engineCall < 0 {
		t.Fatal("找不到放大引擎调用:函数形状变了,这条断言需要跟着更新")
	}
	if assertCall < 0 {
		t.Fatal("放大入口没有调用 checkUpscale2x:产物尺寸没人核对,「没放大却报 2× 完成」会再次静默交付")
	}
	if assertCall < engineCall {
		t.Error("尺寸断言必须在引擎返回**之后**(之前产物还不存在)")
	}
	if success >= 0 && assertCall > success {
		t.Error("尺寸断言必须在 writeJSON(200) **之前**:否则用户已经拿到「放大完成」才被拦下")
	}
	// 产物必须先被探测
	if probeOut := strings.Index(body, "s.ff.Probe(ctx.outPath)"); probeOut < 0 {
		t.Error("必须用 ffprobe 探测**产物**宽高(源片尺寸不足以判断放大了没有)")
	}
	// 报错里必须带上产物路径
	tail := body[assertCall:]
	if !strings.Contains(tail, "ctx.outPath") {
		t.Error("尺寸不符的报错必须带上产物路径(供排查),不能只说一句「尺寸不对」")
	}
	// 提交前还要有"倍率预测"的告警(甲4 的另一半):权重名认不出倍数时提前说清楚
	if !strings.Contains(body, "comfyengine.UpscaleScaleAdvice(") {
		t.Error("放大入口应在提交前做倍率自检(认不出权重倍数时提前告警),而不是等产物出来才发现")
	}
	if advice := strings.Index(body, "comfyengine.UpscaleScaleAdvice("); advice > engineCall {
		t.Error("倍率自检必须排在引擎调用**之前**才有意义(跑完了再告警只是事后说明)")
	}
}

// readServerSource 读本包源码(用于断言"接线位置"这类没有运行期观测面的约定)。
func readServerSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败:%v", name, err)
	}
	return string(raw)
}

// handlerBody 截取某个函数的源码段(从它的签名到 end 标记之前)。
func handlerBody(t *testing.T, src, sig, end string) string {
	t.Helper()
	start := strings.Index(src, sig)
	if start < 0 {
		t.Fatalf("找不到 %q:函数形状变了,这条断言需要跟着更新", sig)
	}
	body := src[start:]
	if i := strings.Index(body, end); i > 0 {
		body = body[:i]
	}
	return body
}
