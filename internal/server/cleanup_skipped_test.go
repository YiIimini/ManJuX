package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cleanup_skipped_test.go 「一键清理」不许**虚报释放量**（2026-10-08 深度排查）。
//
// 原来各处都是 `_ = os.RemoveAll(full)` + 无条件计数：Windows 上文件被占用（资源管理器开着、
// 引擎还握着、有进程在读）时删不掉，结果照样报"已清理 N 个 / X MB" —— 用户以为空间回来了。
// 判据：删不掉的项必须进 `skipped`（如实说哪一项没清掉），而且**不许**出现在"已清理"清单里。
func TestCleanupReportsUnremovableItemInsteadOfFakingFreed(t *testing.T) {
	s := newTestServer(t)
	dir := s.dataDir()
	if strings.TrimSpace(dir) == "" {
		t.Skip("测试服务器没有 DataDir")
	}
	scratch := filepath.Join(dir, "_scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 64<<10)
	for i := range payload {
		payload[i] = 'x'
	}
	locked := filepath.Join(scratch, "locked.bin")
	if err := os.WriteFile(locked, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	// 独占句柄：Go 的 os.Open 不带 FILE_SHARE_DELETE ⇒ Windows 上 RemoveAll 删不掉它。
	f, err := os.Open(locked)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	code, out := doReq(t, s, "POST", "/api/cleanup", map[string]any{})
	if code != 200 {
		t.Fatalf("清理应 200，实际 %d（%v）", code, out)
	}

	skipped := strList(out["skipped"])
	items := strList(out["items"])
	hitSkip := false
	for _, x := range skipped {
		if strings.Contains(x, "_scratch") {
			hitSkip = true
		}
	}
	if !hitSkip {
		t.Errorf("删不掉的目录必须进 skipped（如实说哪一项没清）\nskipped=%v\nitems=%v", skipped, items)
	}
	for _, x := range items {
		if strings.Contains(x, "_scratch") {
			t.Errorf("删不掉却报成\"已清理\"：%q（就是在虚报释放量）", x)
		}
	}
	if mb, _ := out["freedMB"].(float64); mb > 1 {
		t.Errorf("那一项没清掉，释放量不该把它算进去：freedMB=%.3f", mb)
	}
	// 前置条件确认：文件确实还在（否则这条测试什么都没测到）。
	if _, err := os.Stat(locked); err != nil {
		t.Fatalf("前置条件不成立：文件已经被删掉了（%v）—— 这条测试没测到\"删不掉\"的分支", err)
	}
}

// strList 把 JSON 数组转成 []string（测试里只关心字符串项）。
func strList(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
