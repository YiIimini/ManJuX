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
	raw, err := os.ReadFile(filepath.Join(root, "components", "LibAssetDialog.vue"))
	if err != nil {
		t.Fatalf("读 LibAssetDialog.vue 失败：%v", err)
	}
	src := string(raw)

	// 居中：flex 换行 + justify-content:center。
	// ⚠ 上一版用的是 `repeat(auto-fill,112px)` + justify-content:center —— **实测不居中**：
	// auto-fill 先把整行轨道铺满（空轨道照样占位），居中因此等于没居中（用户第二次反馈「也未居中」）。
	if !strings.Contains(src, "flex flex-wrap") || !strings.Contains(src, "justify-content:center") {
		t.Error("详情弹窗的图片要 flex 换行 + 居中排（grid auto-fill 的空轨道会占位，居中是假的）")
	}
	if strings.Contains(src, "grid-template-columns:repeat(auto-fill,112px)") {
		t.Error("别再退回 grid auto-fill 定宽列 —— 那个做法不居中（见注释）")
	}
	// 高度：格子定高（撑满这一块给的高度），宽由产物比例算出来。
	//
	// 【2026-10-10】判据从"必须逐字写成 `height:' + IMG_BOX_H + '`"改成"**有定高 + 有比例**"：
	// 用户要求「三视图更换为角色板显示更详细」之后，横向大图（角色板/转台图/场景板）改用更高的
	// `BOARD_BOX_H` 并占整行 ⇒ 原来那条逐字判据会把**正确的**实现判红（判据挂在字面量上是老毛病）。
	if !strings.Contains(src, "'height:'") || !strings.Contains(src, "const IMG_BOX_H") {
		t.Error("图片格子要**定高**显示（用户：「图片显示高度应该是那个区域的高度」），宽度按产物比例")
	}
	if !strings.Contains(src, "aspect-ratio:' + tileRatio(f)") {
		t.Error("格子形状要按产物形状算（aspect-ratio + tileRatio）")
	}
	if !strings.Contains(src, "object-fit:contain") || !strings.Contains(src, "object-position:center") {
		t.Error("详情弹窗的图要 contain + 居中（cover 会把转台图/角色板裁得只剩中间一条）")
	}
	if !strings.Contains(src, "tileRatio(f)") {
		t.Error("格子形状要按产物形状给（tileRatio）：定妆照 3/4、转台图与角色板 16:9")
	}
	// **角色板要看得清**（用户 2026-10-10「三视图更换为角色板显示更详细」）：
	// 横向大图占整行 + 更高的框，标签里写清它是「角色板（正/侧/背 + 头部细节）」。
	if !strings.Contains(src, "const BOARD_BOX_H") || !strings.Contains(src, "flex-basis:100%") {
		t.Error("角色板/转台图/场景板要占整行（flex-basis:100%）并给更高的框（BOARD_BOX_H）")
	}
	if !strings.Contains(src, "角色板（正/侧/背 + 头部细节）") {
		t.Error("标签要写清是「角色板（正/侧/背 + 头部细节）」—— 用户要求把「三视图」改叫角色板")
	}
	// 「有没有角度参考」的判据必须认三视图板（用户：「三视图不就是多视角的吗，相关"单视角"状态也要同步调整显示」）。
	// 判据在**共用组件**里（详情状态用它），卡片标记那份在 LibraryView（见那个文件），
	// 两处必须同一套 —— 这里是共用组件这一侧。
	if !strings.Contains(src, "function hasAngleRef(") || !strings.Contains(src, "_board|_view_|_turnaround") {
		t.Error("角度参考的判据要把 _board 算进去，否则已经有板子的角色会被标成「缺角度参考」")
	}
	if n := strings.Count(src, "hasAngleRef("); n < 2 {
		t.Errorf("hasAngleRef 在本组件里应当是「定义 + 详情状态调用」，实际 %d 处", n)
	}
	cardRaw, err2 := os.ReadFile(filepath.Join(root, "views", "LibraryView.vue"))
	if err2 != nil {
		t.Fatalf("读 LibraryView.vue 失败：%v", err2)
	}
	card := string(cardRaw)
	if n := strings.Count(card, "hasAngleRef("); n < 2 {
		t.Errorf("卡片标记那一侧也要用同一个判据（定义 + 调用），实际 %d 处", n)
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
