package compose

import (
	"fmt"
	"math"
)

// 交付分辨率解耦(见 compose.Params.DeliverWidth/DeliverHeight)。
//
// 背景与证据(2026-09-27《红果对标-画质配置方案》§2、§3 的 G1):
//   - H3 本地渲染的短边上限是 768(768×1344),而抖音/红果的竖屏交付口径是 1080×1920;
//   - `render.width/height` 一直**同时**驱动 H3 的生成尺寸与成片的交付尺寸,
//     所以"把交付升到 1080"这件事在旧实现里做不到 —— 改了会把 H3 推到它不支持的尺寸上;
//   - 结果是把 768 放大到 1080 的工作留给了平台(平台按自己的算法二次编码)。
//
// 这里把两件事拆开:**渲染尺寸归渲染,交付尺寸归合成**。成片在终混那一步被缩放到交付
// 分辨率,而且**缩放在调色与字幕之前** —— 于是锐化与颗粒作用在真正的交付像素上(而不是
// 768 原生像素上,那是"锐化把 H3 偏软的边缘放大成光晕、再被平台转码削掉"的成因),
// 字幕也按交付画幅排版(PlayRes = 交付尺寸,字号与安全区都按交付高度算)。
//
// 代价(实测,7.29 秒 / 175 帧):768×1344 crf18 → 2.01 Mbps;1080×1920 crf18 → 5.04 Mbps,
// 编码耗时 0.7s → 1.6s。文件体积约 ×2.5,编码耗时增量可忽略。

// aspectTolerance 判定"交付画幅与渲染画幅算不算同一个画幅"的相对误差上限。
//
// 为什么要有这条:等比放大到另一个画幅必然留黑边。768×1344 与 1080×1920 并不是同一个
// 画幅(0.5714 vs 0.5625,差 1.6%)—— 等比缩放到 1080 宽只有 1890 高,上下各留 15px 黑边。
// 1.6% 的形变肉眼不可见,而每帧两条黑边是实实在在的观感损失,所以容忍范围内的做法是
// **直接缩放到目标画幅**(不补边);超出容忍范围(用户把交付画幅设成了另一个比例,比如
// 竖屏项目填了 1920×1080)才退回"等比缩放 + 补黑边",并如实播报 —— 那种情况下拉伸
// 会明显变形,补边是唯一诚实的做法。
const aspectTolerance = 0.05

// deliverTarget 交付分辨率的出厂口径(按画幅):竖屏 → 1080×1920,横屏 → 1920×1080。
//
// 依据(对标 §1.1):抖音/红果的竖屏短剧交付口径是 1080×1920,横屏是 1920×1080。
// 这两组数是"平台交付标准",不是风格选择 —— 所以未表态时的默认必须就是它,
// 而不是"跟随渲染尺寸"。理由与本仓库其它默认(如调色档 GradeAuto)同源:
// **指望人记得去项目参数里改,就是漏掉的那次**。想按渲染尺寸原样交付的项目,
// 把交付宽高显式填成渲染尺寸即可(那时不缩放,行为与解耦之前一致)。
const (
	deliverVerticalW, deliverVerticalH     = 1080, 1920
	deliverHorizontalW, deliverHorizontalH = 1920, 1080
)

// deliverSize 解析最终交付分辨率,并给出需要如实播报的内容(不需要时为 "")。
//
// 三个分支:
//   - 宽高都 >0:照填的(要"按渲染尺寸原样交付"就把渲染尺寸填进来);
//   - 都未填(0):按画幅取交付标准 —— 竖屏 1080×1920 / 横屏 1920×1080;
//   - 只填一边:坏配置,视为未填(与其猜另一半,不如回退到默认口径)。
//
// x264 的 yuv420p 要求偶数边长(奇数会让 ffmpeg 报错或偷偷裁 1 行),这里统一向下取偶
// 并播报取整动作 —— 静默把 1081 变成 1080 与静默忽略参数是同一种毛病。
func (p Params) deliverSize() (w, h int, note string) {
	raw := func(x int) int {
		if x%2 != 0 {
			return x - 1
		}
		return x
	}
	switch {
	case p.DeliverWidth > 0 && p.DeliverHeight > 0:
		w, h = raw(p.DeliverWidth), raw(p.DeliverHeight)
		if w != p.DeliverWidth || h != p.DeliverHeight {
			note = fmt.Sprintf("⚠ 交付分辨率 %d×%d 含奇数边长:已向下取偶为 %d×%d(yuv420p 不接受奇数)",
				p.DeliverWidth, p.DeliverHeight, w, h)
		}
	case p.Height > p.Width && p.Height > 0:
		w, h = deliverVerticalW, deliverVerticalH
	case p.Width > p.Height && p.Width > 0:
		w, h = deliverHorizontalW, deliverHorizontalH
	default:
		// 方形/尺寸未知:没有可依据的交付标准,跟随渲染尺寸(不放大)。
		return p.Width, p.Height, ""
	}
	if p.deliverAuto() {
		note = fmt.Sprintf("📐 交付档按画幅自动:%d×%d 交付(项目参数 deliver_width/height 未填;要按渲染尺寸原样交付就把它们填成 %d×%d)",
			w, h, p.Width, p.Height)
	}
	return w, h, note
}

// deliverAuto 交付分辨率是不是"未表态时按画幅取的标准口径"。
func (p Params) deliverAuto() bool {
	return p.DeliverWidth <= 0 || p.DeliverHeight <= 0
}

// deliverScale 生成"把渲染尺寸放大到交付尺寸"的缩放滤镜。
//
// 返回的 filter 为空串表示不需要缩放(交付尺寸与渲染尺寸一致)—— 此时整条链的
// 状态与解耦之前完全一样,包括"零滤镜时可以 -c:v copy"这条纪律。
//
// note 为需要如实播报的内容(缩放口径 + 形变/黑边代价),不需要播报时为空串。
func deliverScale(renderW, renderH, delW, delH int) (filter, note string) {
	if delW <= 0 || delH <= 0 || renderW <= 0 || renderH <= 0 {
		return "", ""
	}
	if delW == renderW && delH == renderH {
		return "", ""
	}
	base := fmt.Sprintf("scale=%d:%d:flags=lanczos", delW, delH)
	renderAspect := float64(renderW) / float64(renderH)
	deliverAspect := float64(delW) / float64(delH)
	drift := math.Abs(deliverAspect-renderAspect) / renderAspect
	if drift <= aspectTolerance {
		return base, fmt.Sprintf("📐 交付放大 %d×%d → %d×%d(lanczos;画幅差 %.1f%%,直接缩放不补边,锐化与颗粒因此作用在交付像素上)",
			renderW, renderH, delW, delH, drift*100)
	}
	return base + fmt.Sprintf(":force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", delW, delH),
		fmt.Sprintf("⚠ 交付画幅 %d×%d 与渲染画幅 %d×%d 不成比例(差 %.0f%%):已等比放大并补黑边(画面不变形,但成片会带边)",
			delW, delH, renderW, renderH, drift*100)
}

// stripMetadataArgs 清掉输出容器里的全局元数据。
//
// 依据(对标实测):ComfyUI 落到每个镜头 mp4 里的 `prompt` 标签是**整份工作流 JSON**
// (compose 的输入就是这些文件)。体积影响极小,但它会被 -map_metadata 默认继承一路带进
// 成片,属于不必要地把内部工作流泄露出去。只清**容器级**元数据,不动流级标记
// —— 色彩 VUI(bt709 那组)是写在码流里的,与这条无关。
func stripMetadataArgs() []string { return []string{"-map_metadata", "-1"} }

// subtitleMarginVPct 解析字幕下边距(屏高百分比),0/负数 = 默认 15%。
func (p Params) subtitleMarginVPct() int {
	if p.SubtitleMarginV > 0 {
		return p.SubtitleMarginV
	}
	return subtitleMarginVPctDefault
}
