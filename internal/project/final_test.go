package project

import (
	"os"
	"path/filepath"
	"testing"
)

// 成片判据的唯一真源：四种情形都要对，特别是"0 字节"那一种。
//
// 为什么 0 字节要单独钉：批量产线拿这个判据决定"跳过这一集"（已有成片 ⇒ 断点续跑跳过）。
// 一次写到一半就中断的合成会留下 0 字节的 final.mp4 —— 判成"已有成片"就等于把整整一集
// 永久跳过，而且界面上一路显示"完成"。
func TestDeliveredFinalJudgesBySizeAndKind(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "output")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	// ① 什么都没有
	if got, ok := DeliveredFinal(dir); ok {
		t.Errorf("还没有成片时不该判为已交付，实际 %q", got)
	}

	// ② 0 字节的 final.mp4 —— 不算
	if err := os.WriteFile(filepath.Join(out, "final.mp4"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := DeliveredFinal(dir); ok {
		t.Errorf("0 字节的 final.mp4 不算成片（那是合成写到一半），实际判成 %q", got)
	}

	// ③ 正常大小的 final.mp4
	if err := os.WriteFile(filepath.Join(out, "final.mp4"), []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := DeliveredFinal(dir); !ok || got != "output/final.mp4" {
		t.Errorf("应判为 output/final.mp4，实际 %q ok=%v", got, ok)
	}

	// ④ 2K 版（768P 被删掉时仍算已交付）
	if err := os.Remove(filepath.Join(out, "final.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "final_2k.mp4"), []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := DeliveredFinal(dir); !ok || got != "output/final_2k.mp4" {
		t.Errorf("应判为 output/final_2k.mp4，实际 %q ok=%v", got, ok)
	}

	// ⑤ 目录名叫 final.mp4 也不算（有人把它当目录建出来时别判成成片）
	if err := os.Remove(filepath.Join(out, "final_2k.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "final.mp4"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := DeliveredFinal(dir); ok {
		t.Errorf("同名目录不算成片，实际判成 %q", got)
	}
}
