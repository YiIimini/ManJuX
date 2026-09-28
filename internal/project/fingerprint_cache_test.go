package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 指纹缓存的正确性（2026-09-29 性能审计 #3）。
//
// ## 缓存最危险的失败是"文件变了还返回旧指纹"
//
// 指纹的用途是**判断"这一镜用的是不是同一张图"**：资产换了脸、上游重渲、
// 2K 定稿覆盖产物 —— 这些都要靠指纹变化被发现。缓存一旦漏掉"内容已变"，
// 表现是**渲染继续用旧参考图 / 断点续跑以为产物还是新的**，而两处都报成功。
//
// 所以这个测试的正事不是"命中缓存"，而是**改了文件之后必须重算**。
func TestFingerprintFileCacheInvalidates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	if err := os.WriteFile(p, []byte("first-version"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp1 := FingerprintFile(p)
	if fp1 == "" {
		t.Fatal("指纹不该为空")
	}
	// ① 同内容再算一次：结果一样（这就是缓存生效的路径）
	if fp2 := FingerprintFile(p); fp2 != fp1 {
		t.Errorf("同一份内容两次指纹必须相同，%s != %s", fp1, fp2)
	}

	// ② **内容变了必须变** —— 这是缓存最危险的失败模式
	// 注意 mtime 精度：同一纳秒内改写是不现实的，但测试里要显式推一下时间戳，
	// 免得在"文件系统 mtime 精度较粗"的环境上假绿。
	if err := os.WriteFile(p, []byte("second-version-longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	fp3 := FingerprintFile(p)
	if fp3 == fp1 {
		t.Error("文件**内容已变**却返回了旧指纹 —— 后果是「继续用旧参考图」或「以为产物还是新的」，" +
			"而两处都报成功（这正是缓存必须按 mtime+size 失效的理由）")
	}

	// ③ 大小相同、mtime 不同（模拟"同尺寸的另一张图"）：也必须重算
	if err := os.WriteFile(p, []byte("SECOND-VERSION-LONGER"), 0o644); err != nil {
		t.Fatal(err)
	}
	future2 := future.Add(2 * time.Second)
	if err := os.Chtimes(p, future2, future2); err != nil {
		t.Fatal(err)
	}
	fp4 := FingerprintFile(p)
	if fp4 == fp3 {
		t.Error("同尺寸但内容不同、mtime 也不同时，必须重算")
	}
	if fp4 == "" {
		t.Error("指纹不该为空")
	}

	// ④ 不存在的文件返回空串（调用方靠它判"没有这张图"）
	if got := FingerprintFile(filepath.Join(dir, "nope.png")); got != "" {
		t.Errorf("文件不存在时该返回空串，实际 %q", got)
	}

	// ⑤ 两次调用之间**不动文件**：结果稳定（缓存不该引入随机性）
	if a, b := FingerprintFile(p), FingerprintFile(p); a != b || a == "" {
		t.Errorf("同一文件连续两次指纹必须稳定，%q vs %q", a, b)
	}
}
