package project

import "testing"

// 交付分辨率与字幕安全区的参数闸门(红果/抖音对标 P1-1 / P0-3)。
//
// 为什么这两条要有闸门:它们都改了**默认值**(交付档从"跟随渲染"改成"按画幅自动";
// 字幕下边距 8% → 15%),而默认值改错了不会报错 —— 只会让每一部新项目的成片
// 悄悄变成"没放大"或"字幕被平台 UI 压住"。默认值是产品口径,必须有断言钉住。

// TestAssembleDeliverDefaultsFollowAspect 交付档未表态时保持"按画幅自动"的**未表态**语义,
// 不能在 Sanitize 里就把它写死成一个尺寸。
//
// 为什么:横屏项目与竖屏项目的交付标准不同(1920×1080 vs 1080×1920),而"按画幅算"
// 只有一个实现点(compose.deliverSize)。在这里写死就等于把这条规则抄成第二份 ——
// 这份仓库吃过"同一语义两处实现"的亏(Sanitize 与 compose.NormalizeGrade 的同口径双写)。
func TestAssembleDeliverDefaultsFollowAspect(t *testing.T) {
	d := DefaultStageParams()
	if d.Assemble.DeliverWidth != 0 || d.Assemble.DeliverHeight != 0 {
		t.Errorf("交付分辨率出厂值应为 0/0(= 按画幅自动),实际 %d×%d —— 写死尺寸会让横屏项目也照抄竖屏交付档",
			d.Assemble.DeliverWidth, d.Assemble.DeliverHeight)
	}
	if d.Assemble.SubtitleMarginV != 15 {
		t.Errorf("字幕下边距出厂值应为 15%%(平台底部 UI 安全区下限),实际 %d", d.Assemble.SubtitleMarginV)
	}
	// 存过一次参数(整份快照回写)之后语义必须不变
	var p StageParams
	p.Sanitize()
	if p.Assemble.DeliverWidth != 0 || p.Assemble.DeliverHeight != 0 {
		t.Errorf("Sanitize 不该把「未表态」就地定成具体尺寸,实际 %d×%d",
			p.Assemble.DeliverWidth, p.Assemble.DeliverHeight)
	}
	if p.Assemble.SubtitleMarginV != 15 {
		t.Errorf("Sanitize 后字幕下边距应为 15,实际 %d", p.Assemble.SubtitleMarginV)
	}
}

// TestAssembleDeliverSanitize 交付分辨率的钳制:成对才生效、奇数取偶、越界收敛。
func TestAssembleDeliverSanitize(t *testing.T) {
	cases := []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"只填宽 = 坏配置,两边都归未表态(不猜另一半)", 1080, 0, 0, 0},
		{"只填高 = 同上", 0, 1920, 0, 0},
		{"负数 = 未表态", -100, -100, 0, 0},
		{"成对填写原样保留", 1080, 1920, 1080, 1920},
		{"奇数边长向下取偶(yuv420p 不接受奇数)", 1081, 1921, 1080, 1920},
		{"过小收敛到下限", 10, 20, DeliverMinEdge, DeliverMinEdge},
		{"过大收敛到上限", 99999, 99999, DeliverMaxEdge, DeliverMaxEdge},
	}
	for _, c := range cases {
		p := StageParams{}
		p.Assemble.DeliverWidth, p.Assemble.DeliverHeight = c.w, c.h
		p.Sanitize()
		if p.Assemble.DeliverWidth != c.wantW || p.Assemble.DeliverHeight != c.wantH {
			t.Errorf("%s:输入 %d×%d 应为 %d×%d,实际 %d×%d",
				c.name, c.w, c.h, c.wantW, c.wantH, p.Assemble.DeliverWidth, p.Assemble.DeliverHeight)
		}
	}
}

// TestAssembleSubtitleMarginSanitize 字幕下边距的钳制:未表态落回 15%,上限 30%。
//
// 上限不是洁癖:下边距超过屏高 30% 之后字幕已经越过画面中线,那不是"避开平台 UI"
// 而是"字幕跑到人脸上了" —— 手改 JSON 能写出这种值,所以必须钳。
func TestAssembleSubtitleMarginSanitize(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 15}, {-3, 15}, {5, 5}, {15, 15}, {30, 30}, {60, 30},
	}
	for _, c := range cases {
		p := StageParams{}
		p.Assemble.SubtitleMarginV = c.in
		p.Sanitize()
		if p.Assemble.SubtitleMarginV != c.want {
			t.Errorf("字幕下边距 %d 应钳成 %d,实际 %d", c.in, c.want, p.Assemble.SubtitleMarginV)
		}
	}
}
