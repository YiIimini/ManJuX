package h3api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// 流式请求体与"整体拼再 marshal"必须**逐字节等价**（2026-09-29 性能审计 #7）。
//
// ## 为什么这条测试最重要
//
// 为了把 50MB 成片的峰值内存从 ~250MB 降下来，`createStreaming` 不再把 data URI
// 拼成 string，而是"先把大字段换成占位串 → marshal（产物只有几十 KB）→ 按占位串
// 分块写，中间那段流式 base64"。
//
// 这条链一坏就是**静默坏**：请求体里出现字面的占位串、或者 base64 少了几个字符，
// 服务端只会回一个看不懂的参数错误 —— 而本地一切"成功"。
//
// 所以这里直接比对**两条路产出的字节**：拿同一份 body，一条走流式、一条走
// 老写法（整读 + base64 + 拼接 + marshal），必须一模一样。
func TestStreamingBodyMatchesNaiveMarshal(t *testing.T) {
	dir := t.TempDir()
	// 用"非 3 字节对齐"的长度：base64 的分组补齐（= 填充）最容易在这里写错
	blobPath := filepath.Join(dir, "base.mp4")
	payload := make([]byte, 5000) // 5000 % 3 != 0 ⇒ base64 末尾有 '='
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(blobPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	// 照实盘形态构造 body：content[0] 是文本、content[1] 是源视频（嵌套两层的 url）
	mkBody := func() map[string]any {
		return map[string]any{
			"model":      "MiniMax-H3",
			"task_type":  "regeneration",
			"resolution": "2K",
			"content": []map[string]any{
				{"type": "text", "text": "中文提示词 with \"quotes\" and \\ backslash"},
				{"type": "video_url", "video_url": map[string]any{"url": ""}, "role": "base_video"},
			},
		}
	}
	refs := []blobRef{{Path: "content[1].video_url.url", File: blobPath}}

	// ── 流式那条路 ──
	body := mkBody()
	for i, b := range refs {
		if err := setBlobPlaceholder(body, b, dataURIPlaceholder(i)); err != nil {
			t.Fatalf("占位替换失败:%v", err)
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := writeBodyWithBlobs(&got, raw, refs); err != nil {
		t.Fatalf("分块写失败:%v", err)
	}

	// ── 老写法（整体拼）──
	want := mkBody()
	wantContent := want["content"].([]map[string]any)
	wantContent[1]["video_url"].(map[string]any)["url"] =
		"data:video/mp4;base64," + base64.StdEncoding.EncodeToString(payload)
	wantRaw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	if got.String() != string(wantRaw) {
		// 不打印两个 6KB 的串，只指出第一处差异的位置与上下文
		g, w := got.String(), string(wantRaw)
		i := 0
		for i < len(g) && i < len(w) && g[i] == w[i] {
			i++
		}
		lo := i - 60
		if lo < 0 {
			lo = 0
		}
		t.Fatalf("流式请求体与整体拼不一致（长度 %d vs %d，首处差异在第 %d 字节附近）：\n"+
			"  流式：…%s…\n  期望：…%s…",
			len(g), len(w), i, clip(g, lo, 120), clip(w, lo, 120))
	}

	// **占位串不许留在产物里**（那是这条链最典型的静默坏法）
	if strings.Contains(got.String(), "@@MJBLOB") {
		t.Error("请求体里还留着占位串 —— 服务端只会回一个看不懂的参数错误")
	}
	// base64 必须能解回原文件
	var decoded []byte
	if i := strings.Index(got.String(), "base64,"); i >= 0 {
		rest := got.String()[i+len("base64,"):]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			if decoded, err = base64.StdEncoding.DecodeString(rest[:j]); err != nil {
				t.Fatalf("请求体里的 base64 解不开:%v", err)
			}
		}
	}
	if !bytes.Equal(decoded, payload) {
		t.Errorf("base64 解回来的内容与源文件不一致（长度 %d vs %d）", len(decoded), len(payload))
	}
}

// 路径写错时必须**报错**，而不是静默不替换（那会让请求体带着字面占位串发出去）。
func TestBlobPathErrors(t *testing.T) {
	mk := func() map[string]any {
		return map[string]any{
			"content": []map[string]any{
				{"type": "text"},
				{"video_url": map[string]any{"url": ""}},
			},
		}
	}
	cases := []struct {
		name string
		path string
	}{
		{"字段不存在", "content[1].video_url.nope"},
		{"下标越界", "content[9].video_url.url"},
		{"顶层字段不存在", "nope"},
		{"数组通配被拒绝（每一项是不同字段）", "content[].video_url.url"},
		{"以下标结尾没有叶子字段", "content[1]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := setBlobPlaceholder(mk(), blobRef{Path: c.path, File: "x"}, "P")
			if err == nil {
				t.Errorf("路径 %q 该报错 —— 静默不替换会让请求体带着字面占位串发出去，"+
					"而服务端只会回一个看不懂的参数错误", c.path)
			}
		})
	}
	// 正常路径必须成功（别把对的也拒了）
	if err := setBlobPlaceholder(mk(), blobRef{Path: "content[1].video_url.url", File: "x"}, "P"); err != nil {
		t.Errorf("正常路径不该报错:%v", err)
	}
}

// ContentLength 必须**等于实际写出的字节数**（2026-09-30 深度审计）。
//
// 【现场】`createStreaming` 的 blob 分支里写的是 `raw, err := json.Marshal(body)` ——
// 遮蔽了外层 `raw`，于是 `if len(raw) > 0 { req.ContentLength = … }` 在**带 blobs 的那条路
// 永远不成立**：io.PipeReader + ContentLength==0 ⇒ net/http 自动退化成
// `Transfer-Encoding: chunked`。而带 blobs 的唯一生产调用方是**付费的 2K 定稿**
// （整片源视频，~50MB）—— 云端网关接不接受 chunked，本机验不了。
//
// 判据是结构性的：**算出来的长度 == 真的写出来的字节数**（两条路各算各的，对不上就红）。
// 顺带把"前缀只写一份"也钉住：改了 writer 忘了改算法，这条立刻失败。
func TestStreamingBodyLenMatchesWrittenBytes(t *testing.T) {
	dir := t.TempDir()
	// 尺寸刻意铺开：3 的倍数（base64 无填充）、非 3 的倍数（有 '=' 填充）、以及 0 字节
	sizes := []int{5000, 4096, 0, 1, 2}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("%d字节", size), func(t *testing.T) {
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i % 251)
			}
			blobPath := filepath.Join(dir, fmt.Sprintf("blob_%d.mp4", size))
			if err := os.WriteFile(blobPath, payload, 0o644); err != nil {
				t.Fatal(err)
			}
			// 两张参考图 + 一个源视频：多 blob 的占位串替换顺序不能算错
			refs := []blobRef{
				{Path: "content[1].video_url.url", File: blobPath},
				{Path: "content[2].image_url.url", File: blobPath},
			}
			body := map[string]any{
				"model": "MiniMax-H3",
				"content": []map[string]any{
					{"type": "text", "text": "中文提示词 with \"quotes\""},
					{"type": "video_url", "video_url": map[string]any{"url": ""}, "role": "base_video"},
					{"type": "image_url", "image_url": map[string]any{"url": ""}},
				},
			}
			for i, b := range refs {
				if err := setBlobPlaceholder(body, b, dataURIPlaceholder(i)); err != nil {
					t.Fatalf("占位替换失败:%v", err)
				}
			}
			enc, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			want, err := streamingBodyLen(enc, refs)
			if err != nil {
				t.Fatalf("算长度失败:%v", err)
			}
			var got bytes.Buffer
			if err := writeBodyWithBlobs(&got, enc, refs); err != nil {
				t.Fatalf("分块写失败:%v", err)
			}
			if int64(got.Len()) != want {
				t.Errorf("算出来的长度 %d 与实际写出的字节数 %d 不符 —— "+
					"给 net/http 的 ContentLength 对不上时，请求要么被拦（ContentLength with Body length），"+
					"要么退化成 chunked（云端网关接不接受本机验不了）", want, got.Len())
			}
		})
	}
}

// 真的发出去的请求：**有 ContentLength、不是 chunked**（2026-09-30 深度审计）。
//
// 上面那条钉的是"算得对"，这条钉的是"真的用在请求上了"：把 createStreaming 打到一个
// 本地服务端，读服务端侧看到的 ContentLength / TransferEncoding / 实际收到的字节数。
// 少了这条，把 `req.ContentLength = contentLen` 删掉照样能全绿。
func TestCreateStreamingSendsExplicitContentLength(t *testing.T) {
	dir := t.TempDir()
	payload := make([]byte, 5000) // 非 3 字节对齐 ⇒ base64 末尾有填充（最容易算错的那种）
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	blobPath := filepath.Join(dir, "base.mp4")
	if err := os.WriteFile(blobPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	type seen struct {
		contentLen  int64
		transferred []string
		bodyLen     int
		bodyHasBlob bool
	}
	got := make(chan seen, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got <- seen{
			contentLen:  r.ContentLength,
			transferred: r.TransferEncoding,
			bodyLen:     len(raw),
			bodyHasBlob: strings.Contains(string(raw), "data:video/mp4;base64,"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"t"}`))
	}))
	defer ts.Close()

	e := New(func() config.MiniMaxSettings {
		return config.MiniMaxSettings{BaseURL: ts.URL, APIKey: "k", Model: "MiniMax-H3"}
	})
	body := map[string]any{
		"model": "MiniMax-H3",
		"content": []map[string]any{
			{"type": "text", "text": "t"},
			{"type": "video_url", "video_url": map[string]any{"url": ""}, "role": "base_video"},
		},
	}
	if _, err := e.createStreaming(context.Background(), config.MiniMaxSettings{
		BaseURL: ts.URL, APIKey: "k", Model: "MiniMax-H3",
	}, body, []blobRef{{Path: "content[1].video_url.url", File: blobPath}}); err != nil {
		t.Fatalf("提交失败:%v", err)
	}

	s := <-got
	for _, te := range s.transferred {
		if strings.EqualFold(te, "chunked") {
			t.Error("请求体被发成 chunked 了 —— 带 blobs 的这条路是付费的 2K 定稿，" +
				"云端网关接不接受 chunked 本机验不了；ContentLength 必须显式给对")
		}
	}
	if s.contentLen != int64(s.bodyLen) {
		t.Errorf("ContentLength=%d 与实际收到的 %d 字节不符", s.contentLen, s.bodyLen)
	}
	if s.contentLen <= 0 {
		t.Error("ContentLength 必须是正的（为 0 就会被 net/http 当成不发送已知长度）")
	}
	if !s.bodyHasBlob {
		t.Error("服务端没收到 data URI —— 说明流式那一段没真的写出去")
	}
}

func clip(s string, from, n int) string {
	if from >= len(s) {
		return ""
	}
	end := from + n
	if end > len(s) {
		end = len(s)
	}
	return s[from:end]
}
