package compose

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// 合成参数"真的接到底"的针对性测试(审计 P1-1)。
//
// 为什么单独写:`assemble.peak` 这个参数一直存在、界面能调、Sanitize 也在校验,
// 而真正生效的地方(loudnorm 滤镜串)写死 `TP=-1.5` —— 字段被"读过"(校验),
// 却从未影响行为,"字段必须有读取点"那种扫描抓不到这一类。
// 判据必须落在**滤镜串**上:参数变,串就得变。

func TestPeakParameterReachesLoudnorm(t *testing.T) {
	cases := []struct {
		linear float64
		wantDB float64
	}{
		{0.92, -0.7}, // 默认(项目参数默认值)
		{0.89, -1.0}, // 略保守
		{1.0, 0.0},   // 不设上限
		{0.5, -6.0},  // 极端保守
	}
	for _, c := range cases {
		got := peakDBTP(c.linear)
		if math.Abs(got-c.wantDB) > 0.06 {
			t.Errorf("peakDBTP(%.2f) = %.2f dBTP,期望约 %.1f", c.linear, got, c.wantDB)
		}
		// 滤镜串必须带上这个值(而不是写死的 -1.5)
		filter := peakBaseFilter(-16, got)
		if !strings.Contains(filter, fmt.Sprintf("TP=%.2f", got)) {
			t.Errorf("滤镜串未带项目的峰值上限:peak=%.2f → %q", c.linear, filter)
		}
		if strings.Contains(filter, "TP=-1.50") && math.Abs(got+1.5) > 0.06 {
			t.Errorf("滤镜串里仍是写死的 -1.5,参数没生效:%q", filter)
		}
	}
}

// TestPeakDBTPFallsBackOnInvalid 非法值(0 / 负数 / >1)必须回到默认,
// 而不是生成一个会让 loudnorm 报错的串。
func TestPeakDBTPFallsBackOnInvalid(t *testing.T) {
	def := peakDBTP(defaultPeakLinear)
	for _, bad := range []float64{0, -1, 1.5} {
		if got := peakDBTP(bad); math.Abs(got-def) > 0.001 {
			t.Errorf("非法 peak=%.2f 应回退默认(%.2f dBTP),实际 %.2f", bad, def, got)
		}
	}
}

// TestLoudnormAppliedCarriesPeak 两遍法的第二遍同样要带峰值上限 ——
// 只改第一遍是最容易漏的半边(测量用了新值、应用还是旧值)。
func TestLoudnormAppliedCarriesPeak(t *testing.T) {
	tp := peakDBTP(0.89)
	withMeasure := loudnormApplied(&loudnormMeasured{InputI: "-18.0", InputTP: "-2.0",
		InputLRA: "5.0", InputThresh: "-28.0", TargetOffset: "0.5"}, -16, tp)
	withoutMeasure := loudnormApplied(nil, -16, tp)
	for _, s := range []string{withMeasure, withoutMeasure} {
		if !strings.Contains(s, fmt.Sprintf("TP=%.2f", tp)) {
			t.Errorf("第二遍 loudnorm 未带峰值上限:%q", s)
		}
		if !strings.Contains(s, "I=-16") {
			t.Errorf("第二遍 loudnorm 未带目标响度:%q", s)
		}
	}
}
