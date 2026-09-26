package compose

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// TestPlanColorThreeBranches 色彩口径的三个分支(乙3):
// ① 全标记且一致 → 原样透传、不播报;② 有未标记 → 按 bt709 写并如实播报;
// ③ 标记互不一致 → 播报并列出差异,不做统一改写。
func TestPlanColorThreeBranches(t *testing.T) {
	// ① 全部已标记且一致
	uniform := planColor([]colorSpec{bt709Spec, bt709Spec})
	if len(uniform.notes) != 0 {
		t.Errorf("口径一致时不该制造噪声播报,实际 %v", uniform.notes)
	}
	if !uniform.hasUniform || uniform.uniform.key() != bt709Spec.key() {
		t.Fatalf("口径一致时应透传那一份标记,实际 %+v", uniform)
	}
	if args := uniform.masterArgs(); !hasArg(args, "colormatrix=bt709") {
		t.Errorf("统一口径应写进终混: %v", args)
	}

	// ② 未标记 → bt709,并如实播报
	un := planColor([]colorSpec{{}, {Range: "tv"}})
	if joinInts(un.unmarked) != "1、2" {
		t.Errorf("只标了 color_range 不算已标记,应报出第 1、2 镜: %v", un.unmarked)
	}
	joined := strings.Join(un.notes, "|")
	if !strings.Contains(joined, "源片未标记色彩,已按 bt709 处理") {
		t.Errorf("未标记必须如实播报,实际 %q", joined)
	}
	if !strings.Contains(joined, "第 1、2 镜") {
		t.Errorf("播报应点出是哪些镜,实际 %q", joined)
	}
	if args := shotColorArgs(colorSpec{}); !hasArg(args, "colormatrix=bt709") || !hasArg(args, "colorprim=bt709") {
		t.Errorf("未标记的镜应按 bt709 写全三项:%v", args)
	}

	// ③ 已标记但不一致 → 播报差异、不统一改写
	b601 := colorSpec{Range: "tv", Space: "smpte170m", Transfer: "smpte170m", Primaries: "smpte170m"}
	mixed := planColor([]colorSpec{bt709Spec, b601})
	joined = strings.Join(mixed.notes, "|")
	if !strings.Contains(joined, "各镜色彩标记不一致") {
		t.Errorf("标记不一致必须播报,实际 %q", joined)
	}
	if !strings.Contains(joined, "第 1 镜 bt709") || !strings.Contains(joined, "第 2 镜 smpte170m") {
		t.Errorf("播报应列出各镜的差异,实际 %q", joined)
	}
	if mixed.hasUniform {
		t.Error("口径不一致时不该给出一份'全片统一标记'(成品的色标只有一份,写谁都是替另一镜撒谎)")
	}
	if args := mixed.masterArgs(); len(args) != 0 {
		t.Errorf("口径不一致时终混不该写统一标记,实际 %v", args)
	}
	// 但已标记的镜仍按**它自己**的标记写 —— 这是"不强改"的关键
	if args := shotColorArgs(b601); !hasArg(args, "colormatrix=smpte170m") {
		t.Errorf("已标记为 smpte170m 的镜必须原样写回:%v", args)
	}

	// 混合:有未标记 + 已标记一致 → 播报未标记,但不给全片统一标记
	partial := planColor([]colorSpec{bt709Spec, {}})
	if !strings.Contains(strings.Join(partial.notes, "|"), "源片未标记色彩") {
		t.Errorf("混合场景也要播报未标记的镜,实际 %v", partial.notes)
	}
	if partial.hasUniform {
		t.Error("只要还有未标记的镜,就不能声称全片口径统一")
	}
}

// TestShotColorArgsNeverForcesBT709 铁律:已标记为其它矩阵的源片**绝不能被改成 bt709**
// (强改会让整片偏色:像素没动,播放器却换了一套矩阵去解码)。
func TestShotColorArgsNeverForcesBT709(t *testing.T) {
	cases := []struct {
		name string
		spec colorSpec
		want []string
	}{
		{"bt601 全标", colorSpec{Range: "tv", Space: "smpte170m", Transfer: "smpte170m", Primaries: "smpte170m"},
			[]string{"colormatrix=smpte170m", "transfer=smpte170m", "colorprim=smpte170m"}},
		{"只标矩阵(ffmpeg -colorspace 的真实产物)", colorSpec{Range: "tv", Space: "smpte170m"},
			[]string{"colormatrix=smpte170m"}},
		{"只标原色", colorSpec{Primaries: "bt470bg"},
			[]string{"colorprim=bt470bg"}},
	}
	for _, tc := range cases {
		args := shotColorArgs(tc.spec)
		for _, want := range tc.want {
			if !hasArg(args, want) {
				t.Errorf("%s:缺少 %q(实际 %v)", tc.name, want, args)
			}
		}
		if hasArg(args, "colormatrix=bt709") || hasArg(args, "colorprim=bt709") || hasArg(args, "transfer=bt709") {
			t.Errorf("%s:不得把已标记的源片强改成 bt709(实际 %v)", tc.name, args)
		}
	}
}

// TestSpecColorArgsOnlyWritesKnownValues 认不出的取值一律不写、range 缺省不臆造:
// 宁可少标一个字段,也不能标错(错标 = 让播放器按错的矩阵解码)。
func TestSpecColorArgsOnlyWritesKnownValues(t *testing.T) {
	args := specColorArgs(colorSpec{Space: "未来新矩阵", Transfer: "bt709", Primaries: "reserved"})
	if hasArg(args, "colormatrix=") {
		t.Errorf("认不出的矩阵不该写: %v", args)
	}
	if hasArg(args, "colorprim=") {
		t.Errorf("认不出的原色不该写: %v", args)
	}
	if !hasArg(args, "transfer=bt709") {
		t.Errorf("认得出的字段照写: %v", args)
	}
	if v, ok := argValue(args, "-color_range"); ok {
		t.Errorf("源片没标范围就不该替它表态(实际写了 %q):%v", v, args)
	}
	if v, _ := argValue(specColorArgs(colorSpec{Range: "pc", Space: "bt709"}), "-color_range"); v != "pc" {
		t.Errorf("标了范围要照写,实际 %q", v)
	}
	// ffprobe 的"没标"写法要归一成空串,不能当成一个取值写下去
	if normColorValue("unknown") != "" || normColorValue("N/A") != "" || normColorValue(" BT709 ") != "bt709" {
		t.Error("ffprobe 的未标记写法必须归一成空串")
	}
	if c := normColorSpec(colorSpec{Space: "unknown", Primaries: "reserved", Range: "tv"}); c.marked() || c.Range != "tv" {
		t.Errorf("归一后应判为未标记、范围保留: %+v", c)
	}
}

// TestProbeClipColorRoundTrip 真跑一遍 ffprobe:探测要能读出标记,且读出来的标记
// 会被**原样**写回(不是被换成 bt709)。
func TestProbeClipColorRoundTrip(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机无 ffmpeg,跳过色彩探测实测: %v", err)
	}
	ctx := context.Background()
	dir := t.TempDir()

	tagged := filepath.Join(dir, "b601.mp4")
	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=5:duration=0.4",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-x264-params", "colorprim=smpte170m:transfer=smpte170m:colormatrix=smpte170m",
		"-color_range", "tv", tagged); err != nil {
		t.Fatalf("造带标记的测试片段失败: %v", err)
	}
	got, err := probeClipColor(ctx, f, tagged)
	if err != nil {
		t.Fatalf("色彩探测失败: %v", err)
	}
	if got.Space != "smpte170m" || got.Primaries != "smpte170m" || got.Transfer != "smpte170m" {
		t.Fatalf("探测结果与写入不符(探测链路本身有问题): %+v", got)
	}
	if !got.marked() {
		t.Fatal("三项齐全的源片必须判为已标记")
	}
	args := shotColorArgs(got)
	if !hasArg(args, "colormatrix=smpte170m") || hasArg(args, "colormatrix=bt709") {
		t.Errorf("已标记的源片必须原样写回自己的矩阵: %v", args)
	}
	// 全片只有这一镜 → 属于"已标记且一致"分支,终混也应写同一份
	if d := planColor([]colorSpec{got}); !d.hasUniform || d.uniform.Space != "smpte170m" {
		t.Errorf("一致口径应被透传: %+v", d)
	}

	plain := filepath.Join(dir, "plain.mp4")
	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=5:duration=0.4",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", plain); err != nil {
		t.Fatalf("造无标记测试片段失败: %v", err)
	}
	got, err = probeClipColor(ctx, f, plain)
	if err != nil {
		t.Fatalf("色彩探测失败: %v", err)
	}
	if got.marked() {
		t.Errorf("未标记的源片不该被判成已标记(否则会漏掉 bt709 播报): %+v", got)
	}
	if d := planColor([]colorSpec{got}); !strings.Contains(strings.Join(d.notes, "|"), "源片未标记色彩,已按 bt709 处理") {
		t.Errorf("未标记应播报,实际 %v", d.notes)
	}
}

// hasArg 参数列表里是否含该 ffmpeg 参数或取值。
func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want || strings.Contains(a, want) {
			return true
		}
	}
	return false
}

// argValue 取形如 "-color_range pc" 的取值。
func argValue(args []string, flag string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}
