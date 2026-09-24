package comfyengine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// TestWaitForDoneUsesConfiguredTimeout 回归 2026-09-11 + 2026-09-12:
// ①配置的单镜超时必须真正生效,且超时错误要能被 errors.Is(err, engine.ErrRenderTimeout)
// 识别 —— 管线据此决定是否释放显存重试;
// ②但超时**只对"已开始执行"的时间**生效:排队等待不吃执行预算。
// 旧实现从提交那刻起算,定妆照排在长视频任务后面排 10 分钟队就必被判"渲染超时",
// 实测整批角色与场景定妆照被误杀、镜头缺参考图降级 t2va(2026-09-12 用户报障)。
func TestWaitForDoneUsesConfiguredTimeout(t *testing.T) {
	newSrv := func(queueBody string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasPrefix(r.URL.Path, "/history/"):
				_, _ = w.Write([]byte(`{}`))
			case r.URL.Path == "/queue":
				_, _ = w.Write([]byte(queueBody))
			default:
				_, _ = w.Write([]byte(`{}`))
			}
		}))
	}

	// ① 任务还在排队:1 纳秒的执行上限也不该判超时(排队不计时),
	//    只受调用方 ctx 约束 —— 这里用 8 秒 ctx 收尾,验证它一直等到 ctx 到期。
	pendSrv := newSrv(`{"queue_running":[],"queue_pending":[[1,"pid-1"]]}`)
	defer pendSrv.Close()
	pc := NewClient(pendSrv.URL)
	pctx, pcancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer pcancel()
	if _, err := pc.WaitForDone(pctx, "pid-1", time.Nanosecond, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("排队中的任务不应被判执行超时(排队不吃执行预算),实际 %v", err)
	}

	// ② 任务已开始执行:1 纳秒上限应在下一次心跳被判超时,且包装 ErrRenderTimeout
	runSrv := newSrv(`{"queue_running":[[1,"pid-1"]],"queue_pending":[]}`)
	defer runSrv.Close()
	c := NewClient(runSrv.URL)
	started := time.Now()
	// 轮询心跳固定 3s,超短超时会在第一个心跳后被判定,而不是等默认 45 分钟
	_, err := c.WaitForDone(context.Background(), "pid-1", time.Nanosecond, nil)
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if !errors.Is(err, engine.ErrRenderTimeout) {
		t.Fatalf("超时错误必须包装 engine.ErrRenderTimeout,实际 %v", err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("短超时不应等待默认上限,实际等待 %v", elapsed)
	}
}

// TestWaitForDoneZeroTimeoutFallsBack 未指定超时(<=0)时回退默认值,
// 不得退化成「立即超时」;定妆图超时应短于整镜超时。
func TestWaitForDoneZeroTimeoutFallsBack(t *testing.T) {
	if defaultShotTimeout <= 0 {
		t.Fatal("默认单镜超时必须为正")
	}
	if defaultImageTimeout <= 0 || defaultImageTimeout >= defaultShotTimeout {
		t.Fatalf("定妆图超时应为正且短于整镜超时(shot=%v image=%v)", defaultShotTimeout, defaultImageTimeout)
	}
}

// TestComboListBothSchemaShapes ComfyUI 的 combo 有两种形态,必须都认。
//
// 回归 2026-09-24:`ComboList` 原来只认旧式(def[0] 就是字符串数组),而
// `LatentUpscaleModelLoader` / `UpscaleModelLoader` 用的是新式
// (`["COMBO", {"options":[…]}]`)—— 于是放大链路的权重体检把"权重装好了"
// 误报成"节点缺失或输入已改名",用户会被指着去查一个根本没坏的节点。
func TestComboListBothSchemaShapes(t *testing.T) {
	// 报文取自本机 ComfyUI 0.35.1 对 /object_info 的真实响应。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/object_info/VAELoader":
			_, _ = w.Write([]byte(`{"VAELoader":{"input":{"required":{"vae_name":[["LTX2_video_vae_bf16.safetensors","ae.safetensors"]]}}}}`))
		case "/object_info/LatentUpscaleModelLoader":
			_, _ = w.Write([]byte(`{"LatentUpscaleModelLoader":{"input":{"required":{"model_name":["COMBO",{"multiselect":false,"options":["ltx-2-spatial-upscaler-x2-1.0.safetensors"]}]}}}}`))
		case "/object_info/EmptyNode":
			_, _ = w.Write([]byte(`{"EmptyNode":{"input":{"required":{}}}}`))
		default:
			_, _ = w.Write([]byte(`{}`)) // 节点不存在
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	ctx := context.Background()

	// 旧式
	list, ok := c.ComboList(ctx, "VAELoader", "vae_name")
	if !ok || len(list) != 2 || list[0] != "LTX2_video_vae_bf16.safetensors" {
		t.Errorf("旧式 combo 应解析出 2 项,实际 ok=%v list=%v", ok, list)
	}

	// 新式(这条就是回归点)
	list, ok = c.ComboList(ctx, "LatentUpscaleModelLoader", "model_name")
	if !ok {
		t.Fatal("新式 combo([\"COMBO\",{options:[…]}])必须能解析 —— 解析不了会让权重体检误报节点缺失")
	}
	if len(list) != 1 || list[0] != "ltx-2-spatial-upscaler-x2-1.0.safetensors" {
		t.Errorf("新式 combo 应解析出 1 项,实际 %v", list)
	}

	// 输入名不存在 / 节点不存在:必须明确返回 false,而不是空列表 OK
	if _, ok := c.ComboList(ctx, "EmptyNode", "nope"); ok {
		t.Error("输入名不存在时应返回 ok=false")
	}
	if _, ok := c.ComboList(ctx, "NoSuchNode", "field"); ok {
		t.Error("节点不存在时应返回 ok=false")
	}
}

// historyOf 用真实报文片段拼一个 HistoryEntry。
func historyOf(t *testing.T, nodes map[string]string) *HistoryEntry {
	t.Helper()
	h := &HistoryEntry{Outputs: map[string]map[string]json.RawMessage{}}
	for id, body := range nodes {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("解析节点 %s 的报文失败: %v", id, err)
		}
		h.Outputs[id] = m
	}
	return h
}

// TestExtractFilesSkipsInputs 产物必须是"做出来的",不能是"传进去的"。
//
// 回归 2026-09-24(真机实测):放大链路的历史里,`LoadVideo`(节点 10)把自己的
// **输入素材** src-test.mp4 也报成了 animated 产物,与 `SaveVideo`(节点 19)的
// 真产物并列。`Upscale` 取"第一个 animated 文件",而 outputs 是 map、遍历顺序随机
// —— 一半概率把源片原样下载回来并报"放大完成"(实测:分辨率 320x192、字节数与原片
// 完全相同)。这是间歇性静默失败,比直接报错更糟。
//
// 报文取自本机 ComfyUI 0.35.1 的一次真实放大执行。
func TestExtractFilesSkipsInputs(t *testing.T) {
	h := historyOf(t, map[string]string{
		"10": `{"images":[{"filename":"src-test.mp4","subfolder":"","type":"input"}],"animated":[true]}`,
		"19": `{"images":[{"filename":"live-esrgan_00001_.mp4","subfolder":"manjux","type":"output"}],"animated":[true]}`,
	})

	// map 遍历顺序随机 → 多跑几轮,确保不是碰巧通过
	for i := 0; i < 30; i++ {
		files := h.ExtractFiles()
		if len(files) != 1 {
			t.Fatalf("第 %d 轮:应只返回 1 个产物(输入素材不算),实际 %d 个:%v", i, len(files), files)
		}
		f := files[0]
		if f.Filename != "live-esrgan_00001_.mp4" || f.Type != "output" {
			t.Fatalf("第 %d 轮:应返回 SaveVideo 的产物,实际 %+v", i, f)
		}
		if !f.Animated {
			t.Errorf("第 %d 轮:产物应带 animated 标志,实际 %+v", i, f)
		}
	}
}

// TestExtractFilesNoSaver 只有输入节点时不得返回任何"产物"。
//
// 与其把源片当成结果返回,不如返回空 —— 调用方会明确报"未找到视频产物",
// 而不是给用户一个"放大完成但文件没变"的假成功。
func TestExtractFilesNoSaver(t *testing.T) {
	h := historyOf(t, map[string]string{
		"10": `{"images":[{"filename":"src-test.mp4","subfolder":"","type":"input"}],"animated":[true]}`,
	})
	if files := h.ExtractFiles(); len(files) != 0 {
		t.Errorf("没有保存节点时不应返回任何产物,实际 %v", files)
	}
}

// TestExtractFilesStillFindsImage 过滤输入素材不能把正常的出图链路一起滤掉。
func TestExtractFilesStillFindsImage(t *testing.T) {
	h := historyOf(t, map[string]string{
		"9":  `{"images":[{"filename":"avatar_00001_.png","subfolder":"manjux","type":"output"}]}`,
		"12": `{"images":[{"filename":"ref_uploaded.png","subfolder":"","type":"input"}]}`,
	})
	files := h.ExtractFiles()
	if len(files) != 1 || files[0].Filename != "avatar_00001_.png" {
		t.Fatalf("出图链路应仍然只返回 SaveImage 的产物,实际 %v", files)
	}
	if files[0].Animated {
		t.Error("静态图不应被标成 animated")
	}
}
