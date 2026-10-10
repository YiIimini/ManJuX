package server

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// 小说详情的 `chapters` **必须是数组，不能是 null**（2026-09-30 深度审计）。
//
// 【现场】`handleNovelDetail` 里是 `var chapters []novelChapter`：当一本书的目录里
// 没有一个**可读正文**文件（只有图片 / 空目录 / 平台不读的格式）时它保持 nil，
// Go 把 nil 切片 marshal 成 `null`，而详情弹窗直接读 `dlg.chapters.length`（NovelsView 三处）
// ⇒ 点开就整页炸 `Cannot read properties of null (reading 'length')`，弹窗内容一个字都不显示
// （`main.js` 的错误兜底只把异常文案贴出来）。
//
// 判据：不带正文章的目录，`chapters` 要回 **[]**（空数组），而不是 null。
// 与 `project.HealPlan` 那条"空切片也要是 []"同一口径 —— 收在产出侧一处。
//
// 【2026-10-10 更新夹具】这份夹具原来用的是 `全本.epub` —— 那时 `.epub` 属于"平台不读"，
// 现在 `.epub`/`.docx` 已经能读（见 novel_formats.go），再拿它当"没有可读正文"就不成立了
// （实测这条门当场变红：目录里列出了 1 项「📖 全本 · 全本.epub」）。所以夹具换成
// `.pdf` —— 它是**永久边界**里的格式（要版式解析器），语义与原来一致：文件在、但读不出正文。
func TestNovelDetailChaptersIsArrayNotNull(t *testing.T) {
	s := newTestServer(t)
	if s.novelRoot() == "" {
		t.Fatal("测试服务器的小说库根目录为空，夹具不成立")
	}
	// 真形态：只有电子书、没有可读正文的目录（用户从别处拷来的书常常就是这样）
	book := filepath.Join(s.novelRoot(), "只有电子书没有正文")
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatal(err)
	}
	// 真形态：只有一本"平台不读"的电子书、没有可读正文的目录（用户从别处拷来的书常常就是这样）
	if err := os.WriteFile(filepath.Join(book, "全本.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := doReq(t, s, "GET", "/api/novels/detail?path="+url.QueryEscape(book), nil)
	if code != 200 {
		t.Fatalf("详情应 200，实际 %d（%v）", code, out)
	}
	raw, ok := out["chapters"]
	if !ok {
		t.Fatal("响应里必须有 chapters 字段")
	}
	if raw == nil {
		t.Error("chapters 是 null —— 前端读 `dlg.chapters.length` 会整页炸（Cannot read properties of null）")
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("chapters 应是数组，实际 %T", raw)
	}
	if len(arr) != 0 {
		t.Errorf("这目录没有可读正文章节，应为空数组，实得 %d 项", len(arr))
	}

	// 反面：真有正文的目录必须照旧列出来（别为了"不为 null"把内容也吃掉）
	book2 := filepath.Join(s.novelRoot(), "有正文的书")
	if err := os.MkdirAll(filepath.Join(book2, "正文"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(book2, "正文", "第01章.md"), []byte("正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out2 := doReq(t, s, "GET", "/api/novels/detail?path="+url.QueryEscape(book2), nil)
	arr2, _ := out2["chapters"].([]any)
	if len(arr2) == 0 {
		t.Errorf("有正文的目录应列出章节，实得 %v", out2["chapters"])
	}
}
