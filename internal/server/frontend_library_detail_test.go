package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frontend_library_detail_test.go 资产库「详情弹窗」里图片的展示口径。
//
// 用户 2026-09-27 原话：「资产库里的弹窗详情里的图片请居中对齐显示」。
// 两个来源同一个症状，都要钉住：
//
//	① **居中**：1~2 张图时 `repeat(auto-fill, minmax(112px,1fr))` 会把空列留在右边，
//	   图看着是靠左而不是居中 → 改成定宽列 + `justify-content:center`；
//	② **完整**：格子一律 3/4 + `object-fit:cover` 会把横幅裁得只剩中间一条
//	   —— 转台图是 4 格横向拼图(8:3)、三视图板是 16:9，"只给你看中间那个全身"。
//	   所以格子形状按**产物形状**给（tileRatio），图用 `contain` 完整显示、居中摆放。
//
// 这条也顺手守住"三视图板在详情里能被完整看到"——板子现在还兼角度参考，
// 看板子的人要能一次看全 3 个全身 + 6 个头部细节。
func TestLibraryDetailImagesCentered(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	raw, err := os.ReadFile(filepath.Join(root, "views", "LibraryView.vue"))
	if err != nil {
		t.Fatalf("读 LibraryView.vue 失败：%v", err)
	}
	src := string(raw)

	if !strings.Contains(src, "repeat(auto-fill,112px)") || !strings.Contains(src, "justify-content:center") {
		t.Error("详情弹窗的图片网格要定宽列 + 居中排（auto-fill+1fr 会把 1~2 张图压在左边）")
	}
	if !strings.Contains(src, "object-fit:contain") || !strings.Contains(src, "object-position:center") {
		t.Error("详情弹窗的图要 contain + 居中（cover 会把转台图/三视图板裁得只剩中间一条）")
	}
	if !strings.Contains(src, "tileRatio(f)") {
		t.Error("格子形状要按产物形状给（tileRatio）：定妆照 3/4、转台图与三视图板 16:9")
	}
	// tileRatio 的判据与 labelOf 同一套（文件名后缀），别去猜像素
	for _, want := range []string{"function tileRatio(", "_board", "_turnaround", "return '16/9'", "return '3/4'"} {
		if !strings.Contains(src, want) {
			t.Errorf("tileRatio 里缺 %q", want)
		}
	}
	// 详情弹窗里图片那一块必须真的用上它（别定义了没用）
	if strings.Count(src, "tileRatio(") < 2 {
		t.Error("tileRatio 要在模板里被调用（定义 + 调用）")
	}
}
