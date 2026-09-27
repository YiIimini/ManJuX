package compose

import (
	"context"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// TestGradeFilterLevels 调色档的取值口径:off 为空串、空/未知回落 film、每档的关键滤镜齐全。
func TestGradeFilterLevels(t *testing.T) {
	if got := GradeFilter(GradeOff); got != "" {
		t.Errorf("off 档必须是空串(终混视频零滤镜、才允许 -c:v copy),实际 %q", got)
	}
	// 未表态(null 洞 / 老项目缺键)与写错的值一律按 film —— 调色默认开
	for _, name := range []string{"", "  ", "FILM", "cinematic", "不知道", "true"} {
		if got := GradeFilter(name); got != gradeFilm {
			t.Errorf("档位 %q 应回落 film,实际 %q", name, got)
		}
		if got := NormalizeGrade(name); got != GradeFilm {
			t.Errorf("NormalizeGrade(%q) 应为 film,实际 %q", name, got)
		}
	}
	if NormalizeGrade(" OFF ") != GradeOff || NormalizeGrade("Heavy") != GradeHeavy {
		t.Error("档位名应大小写/空白不敏感")
	}

	film := GradeFilter(GradeFilm)
	for _, key := range []string{"eq=contrast=1.12", "colorbalance=", "unsharp=", "vignette=PI/4.5"} {
		if !strings.Contains(film, key) {
			t.Errorf("film 档缺关键滤镜 %q:%s", key, film)
		}
	}
	// 2026-09-27:颗粒从 film 挪走 —— 实测它把码率推到 2.5×,而平台二次编码会先把它抹掉。
	// 这条断言是双面的:film 一旦重新长出 noise,就等于把那份代价悄悄加回了默认交付。
	if strings.Contains(film, "noise=") {
		t.Errorf("film 档不该带颗粒(颗粒只留在 heavy 档;实测颗粒 = 2.5× 码率且平台会抹掉):%s", film)
	}
	soft := GradeFilter(GradeSoft)
	if !strings.Contains(soft, "eq=contrast=1.06") || !strings.Contains(soft, "unsharp=5:5:0.5") {
		t.Errorf("soft 档应为更轻的一套参数:%s", soft)
	}
	if strings.Contains(soft, "vignette") || strings.Contains(soft, "noise=") {
		t.Errorf("soft 档不该有暗角与颗粒(那是 film 的强度):%s", soft)
	}
	heavy := GradeFilter(GradeHeavy)
	// heavy = film 同一串 + 色差 + 更重的颗粒
	for _, key := range []string{"eq=contrast=1.12", "colorbalance=", "vignette=PI/4.5", "chromashift=cbh=2:crh=-2", "noise=alls=10"} {
		if !strings.Contains(heavy, key) {
			t.Errorf("heavy 档缺 %q:%s", key, heavy)
		}
	}
	// 颗粒只许有一道:叠两个 noise 滤镜是"噪上加噪",不是"更重"
	if n := strings.Count(heavy, "noise="); n != 1 {
		t.Errorf("heavy 的颗粒应当是同一道滤镜加大到 10,而不是叠一层(实际 %d 处 noise=):%s", n, heavy)
	}

	// unsharp 的写法闸门:`5:5=0.0:5:5:0.8` 会被 ffmpeg 当成"名为 5=0.0 的参数"整链拒绝
	for _, name := range []string{GradeSoft, GradeFilm, GradeHeavy} {
		if g := GradeFilter(name); strings.Contains(g, "5:5=") {
			t.Errorf("%s 档的 unsharp 写法非法(ffmpeg 会报 No option name near '5:5=…'):%s", name, g)
		}
	}
}

// TestGradeFilterChainsRunOnRealFfmpeg 每一档都真的丢给本机 ffmpeg 跑一遍。
//
// 为什么必须真跑:滤镜名/参数写错时,合成会走到"终混失败"才炸,而那时已经烧掉
// 整片规范化与拼接的时间。这条测试是那份代价的替代品。
func TestGradeFilterChainsRunOnRealFfmpeg(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过调色链校验: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{GradeSoft, GradeFilm, GradeHeavy} {
		chain := GradeFilter(name)
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc=size=128x128:rate=5:duration=0.4",
			"-vf", chain, "-f", "null", "-"); err != nil {
			t.Errorf("%s 档的滤镜串被 ffmpeg 拒绝: %v", name, err)
		}
	}
}

// TestFinalVideoFiltersGradeBeforeSubtitles 终混滤镜串的顺序是硬约束:
// 交付缩放 → 调色 → 字幕(颗粒/暗角/锐化作用在字幕上会把字压糊;
// 缩放排在调色之前,锐化与颗粒才作用在交付像素上而不是渲染像素上)。
//
// 这里断言的是**真正交给 ffmpeg 的那条串**,不是注释里的承诺。
func TestFinalVideoFiltersGradeBeforeSubtitles(t *testing.T) {
	vf, need := finalVideoFilters(GradeFilm, true, "")
	if !need {
		t.Fatal("烧字幕 + 调色必须重编码")
	}
	gi := strings.Index(vf, GradeFilter(GradeFilm))
	si := strings.Index(vf, "subtitles=filename=final.ass")
	if gi < 0 || si < 0 {
		t.Fatalf("滤镜串应同时含调色与字幕:%s", vf)
	}
	if gi > si {
		t.Errorf("调色必须排在字幕之前(否则颗粒与暗角压在字幕上):%s", vf)
	}

	// 无字幕分支也要应用调色(甲1 的显式要求)
	vf2, need2 := finalVideoFilters(GradeFilm, false, "")
	if !need2 || vf2 != GradeFilter(GradeFilm) {
		t.Errorf("不烧字幕时也必须应用调色,实际 need=%v vf=%q", need2, vf2)
	}
	// 空档位(未表态)= 默认开
	if _, need3 := finalVideoFilters("", false, ""); !need3 {
		t.Error("档位未表态时应默认开调色(与 GradeFilter 的 film 默认一致)")
	}
	// off 且不烧字幕 → 零滤镜:只有这一路才允许 -c:v copy(审计 M35)
	if vf4, need4 := finalVideoFilters(GradeOff, false, ""); need4 || vf4 != "" {
		t.Errorf("off + 不烧字幕应零滤镜(保留 -c:v copy 快路),实际 need=%v vf=%q", need4, vf4)
	}
	// off + 烧字幕 → 只有字幕那一段
	if vf5, need5 := finalVideoFilters(GradeOff, true, ""); !need5 || strings.Contains(vf5, "eq=") {
		t.Errorf("off 档不该出现任何调色滤镜,实际 need=%v vf=%q", need5, vf5)
	}
}

// TestFinalVideoFiltersDeliveryScaleGoesFirst 交付缩放在整条链最前面。
//
// 顺序错了不会报错,只会让锐化与颗粒作用在 768 原生像素上(那份"光晕被平台削掉"的账
// 白付),而且 off 档 + 不烧字幕时还多压一代 x264 —— 两条都是静默的。
func TestFinalVideoFiltersDeliveryScaleGoesFirst(t *testing.T) {
	scale, note := deliverScale(768, 1344, 1080, 1920)
	if scale == "" {
		t.Fatal("768×1344 → 1080×1920 应该生成缩放滤镜")
	}
	if !strings.Contains(scale, "scale=1080:1920") || !strings.Contains(scale, "flags=lanczos") {
		t.Errorf("缩放滤镜应是 lanczos 到交付尺寸:%s", scale)
	}
	if strings.Contains(scale, "force_original_aspect_ratio") {
		t.Errorf("768×1344 与 1080×1920 只差 1.6%%,在容忍带内应直接缩放(不补黑边):%s", scale)
	}
	if !strings.Contains(note, "交付放大") {
		t.Errorf("交付放大必须如实播报(用户要能判断这一步到底做没做):%q", note)
	}

	vf, need := finalVideoFilters(GradeFilm, true, scale)
	if !need {
		t.Fatal("有缩放就必须重编码")
	}
	if i, j := strings.Index(vf, scale), strings.Index(vf, GradeFilter(GradeFilm)); i != 0 || j <= i {
		t.Errorf("缩放必须在最前、调色在其后:%s", vf)
	}
	if i, j := strings.Index(vf, GradeFilter(GradeFilm)), strings.Index(vf, "subtitles="); j <= i {
		t.Errorf("调色仍必须在字幕之前:%s", vf)
	}

	// 只有缩放(off 档 + 不烧字幕)也必须重编码 —— 否则 -c:v copy 会产出一个
	// 根本没被放大的"成片",而日志显示合成成功。
	if vf6, need6 := finalVideoFilters(GradeOff, false, scale); !need6 || vf6 != scale {
		t.Errorf("只有缩放这一路必须重编码且只含缩放,实际 need=%v vf=%q", need6, vf6)
	}

	// 画幅不成比例时才补黑边,并且要播报
	mismatch, note2 := deliverScale(768, 1344, 1920, 1080)
	if !strings.Contains(mismatch, "force_original_aspect_ratio=decrease") || !strings.Contains(mismatch, "pad=1920:1080") {
		t.Errorf("画幅不成比例应等比缩放 + 补黑边(不拉伸):%s", mismatch)
	}
	if !strings.Contains(note2, "不成比例") {
		t.Errorf("补黑边必须如实播报:%q", note2)
	}

	// 交付尺寸 = 渲染尺寸时零滤镜(老行为一字不变)
	if s, n := deliverScale(768, 1344, 768, 1344); s != "" || n != "" {
		t.Errorf("交付与渲染一致时不该有任何缩放,实际 filter=%q note=%q", s, n)
	}
	// 未表态(0/0)= 按画幅取交付标准:竖屏 1080×1920、横屏 1920×1080。
	// 默认必须是对的档,否则又回到"指望人记得去项目参数里改"。
	if w, h, _ := (Params{Width: 768, Height: 1344}).deliverSize(); w != 1080 || h != 1920 {
		t.Errorf("竖屏项目未填交付档时应按 1080×1920 交付,实际 %dx%d", w, h)
	}
	if w, h, _ := (Params{Width: 1344, Height: 768}).deliverSize(); w != 1920 || h != 1080 {
		t.Errorf("横屏项目未填交付档时应按 1920×1080 交付,实际 %dx%d", w, h)
	}
	// 只填一边 = 坏配置 → 视为未表态(回落到按画幅的标准档),不是跟随渲染
	if w, h, note := (Params{Width: 768, Height: 1344, DeliverWidth: 1080}).deliverSize(); w != 1080 || h != 1920 {
		t.Errorf("只填一个交付边长应视为未填并回落到按画幅的交付标准,实际 %dx%d(%s)", w, h, note)
	}
	// 成对填写才生效(想"按渲染尺寸原样交付"就显式填渲染尺寸)
	if w, h, note := (Params{Width: 768, Height: 1344, DeliverWidth: 768, DeliverHeight: 1344}).deliverSize(); w != 768 || h != 1344 || note != "" {
		t.Errorf("显式填写交付尺寸应照用,实际 %dx%d note=%q", w, h, note)
	}
	// 显式填的标准档要播报"自动"吗?不要 —— 自动提示只在真未填时出现
	if _, _, note := (Params{Width: 768, Height: 1344, DeliverWidth: 1080, DeliverHeight: 1920}).deliverSize(); strings.Contains(note, "自动") {
		t.Errorf("显式填了交付尺寸就不该再报「按画幅自动」:%q", note)
	}
	if _, _, note := (Params{Width: 768, Height: 1344}).deliverSize(); !strings.Contains(note, "自动") {
		t.Errorf("按画幅自动取交付档必须如实播报一次:%q", note)
	}
	// 奇数边长取偶 + 播报(yuv420p 不接受奇数)
	if w, h, note := (Params{Width: 768, Height: 1344, DeliverWidth: 1081, DeliverHeight: 1921}).deliverSize(); w != 1080 || h != 1920 {
		t.Errorf("yuv420p 要偶数边长,奇数应向下取偶,实际 %dx%d", w, h)
	} else if !strings.Contains(note, "奇数") {
		t.Errorf("取偶动作必须播报(静默改参数与参数不生效是同一种毛病):%q", note)
	}
}
