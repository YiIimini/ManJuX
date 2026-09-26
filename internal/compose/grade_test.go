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
	for _, key := range []string{"eq=contrast=1.12", "colorbalance=", "unsharp=", "vignette=PI/4.5", "noise=alls=6"} {
		if !strings.Contains(film, key) {
			t.Errorf("film 档缺关键滤镜 %q:%s", key, film)
		}
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
// 调色必须在字幕之前(颗粒/暗角/锐化作用在字幕上会把字压糊)。
//
// 这里断言的是**真正交给 ffmpeg 的那条串**,不是注释里的承诺。
func TestFinalVideoFiltersGradeBeforeSubtitles(t *testing.T) {
	vf, need := finalVideoFilters(GradeFilm, true)
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
	vf2, need2 := finalVideoFilters(GradeFilm, false)
	if !need2 || vf2 != GradeFilter(GradeFilm) {
		t.Errorf("不烧字幕时也必须应用调色,实际 need=%v vf=%q", need2, vf2)
	}
	// 空档位(未表态)= 默认开
	if _, need3 := finalVideoFilters("", false); !need3 {
		t.Error("档位未表态时应默认开调色(与 GradeFilter 的 film 默认一致)")
	}
	// off 且不烧字幕 → 零滤镜:只有这一路才允许 -c:v copy(审计 M35)
	if vf4, need4 := finalVideoFilters(GradeOff, false); need4 || vf4 != "" {
		t.Errorf("off + 不烧字幕应零滤镜(保留 -c:v copy 快路),实际 need=%v vf=%q", need4, vf4)
	}
	// off + 烧字幕 → 只有字幕那一段
	if vf5, need5 := finalVideoFilters(GradeOff, true); !need5 || strings.Contains(vf5, "eq=") {
		t.Errorf("off 档不该出现任何调色滤镜,实际 need=%v vf=%q", need5, vf5)
	}
}
