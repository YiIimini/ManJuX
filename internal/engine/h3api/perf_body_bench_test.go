package h3api

// 云端请求体的**实测**工具（性能审计 #7：大字段流式）。
//
// 这里不做"等价性"证明（那是 streaming_body_test.go 的事），只回答一个数字问题：
// 发一份**真实形态**的 2K 再生成请求（50MB 源视频 + 3 张真实参考图）时，
// 老写法与新写法各自要分配多少内存、峰值堆占用是多少。
//
// 两条链在同一个二进制里都能跑 —— 老写法用的 `fileToVideoDataURI`
// 就是审计当时那条链（本仓为 Regen2K 之外的老路径保留着），所以这是一次
// **同进程、同载荷、同时刻**的对照，不是两个版本的推算。
//
// 载荷来自环境变量（真实文件，不是造的字符串）：
//
//	MJX_PERF_VIDEO  源视频（真实 mp4，≤50MB，云端 regeneration 的上限）
//	MJX_PERF_IMAGE  参考图（真实 png/jpg）
//
// 没有这些文件时 benchmark 自动跳过 —— 它不该拖慢日常 `go test`。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
)

func perfFile(tb testing.TB, env string) string {
	p := os.Getenv(env)
	if p == "" {
		tb.Skipf("未提供 %s（真实载荷路径），跳过这项实测", env)
	}
	if _, err := os.Stat(p); err != nil {
		tb.Skipf("%s 指向的文件不存在：%v", env, err)
	}
	return p
}

// perfStubServer 收下整个请求体并回一个成功的 task_id。
// **必须真读 body**：客户端是 io.Pipe 流式写的，服务端不读就测不出真实的写出过程。
func perfStubServer(tb testing.TB) (*httptest.Server, *int64) {
	var recv int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		atomic.AddInt64(&recv, n)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"task_id":"perf-task","base_resp":{"status_code":0,"status_msg":"ok"}}`)
	}))
	tb.Cleanup(srv.Close)
	return srv, &recv
}

// perfRegenBody 照 Regen2K 的真实形状搭 body：第 0 项文本、随后各张参考图、最后是源视频。
func perfRegenBody(tb testing.TB, refs []string) (map[string]any, []blobRef) {
	content := []map[string]any{{"type": "text", "text": "重新生成这一镜，保持人物与构图"}}
	blobs := []blobRef{}
	for _, r := range refs {
		uri, err := fileToDataURI(r)
		if err != nil {
			tb.Fatal(err)
		}
		content = append(content, map[string]any{
			"type": "image_url", "image_url": map[string]any{"url": uri}, "role": "reference_image",
		})
	}
	content = append(content, map[string]any{
		"type": "video_url", "video_url": map[string]any{"url": ""}, "role": "base_video",
	})
	body := map[string]any{
		"model": "MiniMax-H3", "task_type": "regeneration", "content": content, "resolution": "2K",
	}
	// 与 Regen2K 的真实登记一致：**只有源视频**走流式（参考图是 fileToDataURI 内联的，
	// 因为它们本来就只有 1~3MB，云端那条链的峰值来源是 50MB 成片）。
	blobs = append(blobs, blobRef{Path: fmt.Sprintf("content[%d].video_url.url", len(content)-1), File: os.Getenv("MJX_PERF_VIDEO")})
	return body, blobs
}

// perfRegenBodyLegacy 老写法：源视频整读 → base64 → 拼成 data URI → 塞进 body。
func perfRegenBodyLegacy(tb testing.TB, refs []string) map[string]any {
	body, _ := perfRegenBody(tb, refs)
	uri, err := fileToVideoDataURI(os.Getenv("MJX_PERF_VIDEO"))
	if err != nil {
		tb.Fatal(err)
	}
	content := body["content"].([]map[string]any)
	content[len(content)-1]["video_url"] = map[string]any{"url": uri}
	body["content"] = content
	return body
}

func perfEngine(tb testing.TB, srv *httptest.Server) (*Engine, config.MiniMaxSettings) {
	cfg := config.MiniMaxSettings{BaseURL: srv.URL, APIKey: "perf-key", Model: "MiniMax-H3", Resolution: "2K"}
	return New(func() config.MiniMaxSettings { return cfg }), cfg
}

func perfRefs(tb testing.TB) []string {
	img := perfFile(tb, "MJX_PERF_IMAGE")
	return []string{img, img, img}
}

// BenchmarkStreamingBody 新写法：占位串 + 边读边编码。
func BenchmarkStreamingBody(b *testing.B) {
	srv, _ := perfStubServer(b)
	e, cfg := perfEngine(b, srv)
	refs := perfRefs(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body, blobs := perfRegenBody(b, refs)
		if _, err := e.createStreaming(ctx, cfg, body, blobs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLegacyBody 老写法：整读 + base64 + marshal（审计当时的链）。
func BenchmarkLegacyBody(b *testing.B) {
	srv, _ := perfStubServer(b)
	e, cfg := perfEngine(b, srv)
	refs := perfRefs(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body := perfRegenBodyLegacy(b, refs)
		if _, err := e.create(ctx, cfg, body); err != nil {
			b.Fatal(err)
		}
	}
}

// TestPeakBodyMemory 峰值堆占用：两条链各发一次真实请求，期间每 1ms 采一次 HeapAlloc。
// B/op 讲的是"总共分配了多少"，这里讲的是"同一时刻活着多少" —— 现场 OOM 看的是后者。
func TestPeakBodyMemory(t *testing.T) {
	perfFile(t, "MJX_PERF_VIDEO")
	srv, recv := perfStubServer(t)
	e, cfg := perfEngine(t, srv)
	refs := perfRefs(t)
	ctx := context.Background()

	peak := func(streaming bool) (base, hi uint64, sent int64) {
		// FreeOSMemory 把"上一轮留下的堆目标"一并清掉：Go 的 GC 触发点是按活堆的两倍
		// 推算的，先跑过老写法那一轮（471MB 分配）之后再来测新写法，采样能高出一倍
		// —— 那是测量方法的问题，不是新写法真的活着那么多。
		runtime.GC()
		debug.FreeOSMemory()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		atomic.StoreInt64(recv, 0)
		done := make(chan uint64, 1)
		stop := make(chan struct{})
		go func() {
			var top uint64
			for {
				select {
				case <-stop:
					done <- top
					return
				default:
				}
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > top {
					top = m.HeapAlloc
				}
				time.Sleep(200 * time.Microsecond)
			}
		}()
		if streaming {
			body, blobs := perfRegenBody(t, refs)
			if _, err := e.createStreaming(ctx, cfg, body, blobs); err != nil {
				t.Fatal(err)
			}
		} else {
			body := perfRegenBodyLegacy(t, refs)
			if _, err := e.create(ctx, cfg, body); err != nil {
				t.Fatal(err)
			}
		}
		close(stop)
		top := <-done
		return before.HeapAlloc, top, atomic.LoadInt64(recv)
	}

	// **先测新写法**：顺序反过来会让新写法吃上一轮的堆目标（见 peak 里的说明）
	streamBase, streamPeak, streamBytes := peak(true)
	legacyBase, legacyPeak, legacyBytes := peak(false)
	vid, _ := os.Stat(os.Getenv("MJX_PERF_VIDEO"))
	if abs64(legacyBytes-streamBytes) > 1024 {
		t.Fatalf("两条链发出去的字节数不一致（老 %d / 新 %d）——先证明等价再谈省内存", legacyBytes, streamBytes)
	}
	t.Logf("源视频 %.1fMB + 3 张参考图；服务端各收下 %.1fMB（两条链一致）",
		float64(vid.Size())/(1<<20), float64(streamBytes)/(1<<20))
	t.Logf("峰值 HeapAlloc  老写法 %.1f MB（起 %.1f）→ 新写法 %.1f MB（起 %.1f）",
		float64(legacyPeak)/(1<<20), float64(legacyBase)/(1<<20),
		float64(streamPeak)/(1<<20), float64(streamBase)/(1<<20))
	t.Logf("峰值增量 老 +%.1f MB vs 新 +%.1f MB",
		float64(legacyPeak-legacyBase)/(1<<20), float64(streamPeak-streamBase)/(1<<20))
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
