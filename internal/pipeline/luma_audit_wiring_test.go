package pipeline

import (
	"strings"
	"testing"
)

// luma_audit_wiring_test.go 亮度/黑场体检的**接线**门（2026-10-11）。
//
// 【为什么单独一道】判据（filmaudit 里那些纯函数）已经被 fixture_test 逐条咬过，但"判据用对了、
// **没接起来**"照样能让用户看不到结论 —— 第 33 轮审计的原话就是"接线这一层一条没测"。
// 这里只咬三件事：① app 内体检真的量了（且量尺从本机 ffmpeg 来）；② 没量到时**如实说一句**
// （三态：没量到 ≠ 合格）；③ 命令行与 app 用同一套。

func TestLumaAuditIsMeasuredAndHonestlyReported(t *testing.T) {
	app := readSrc(t, "film_audit.go")
	const call = "filmaudit.MeasureLuma(lumaFn, clips)"
	at := strings.Index(app, call)
	if at < 0 {
		t.Fatal("合片体检没有量画面亮度/黑场 —— 「画面不如意」只能等用户自己看出来")
	}
	if line := lineOf(app, at); strings.HasPrefix(strings.TrimSpace(line), "//") {
		t.Fatalf("那句调用只出现在注释里：%q", line)
	}
	// 量尺必须从**本机 ffmpeg** 来（不是随便一个非 nil 的东西），并且不可用时留 nil（三态的第一态）。
	if !strings.Contains(app, "filmaudit.LumaFromFFmpeg(") {
		t.Error("量尺不是用 filmaudit.LumaFromFFmpeg 造的 —— 亮度/黑场就量不出来")
	}
	// 三态必须都有出口：量尺没有 / 一镜都没量到 / 有几镜没量到。
	for _, want := range []string{"lumaFn == nil", "in.Luma == nil", "len(lumaMissing) > 0"} {
		if !strings.Contains(app, want) {
			t.Errorf("少了三态里的这一支：%s —— 没量到会被当成「合格」", want)
		}
	}
	// 量不到的镜必须**点名**（不然用户只知道"有几镜没量"，不知道自己该渲哪一镜）。
	if !strings.Contains(app, "shotIDsText(lumaMissing)") {
		t.Error("没量到的镜没有点名（shotIDsText(lumaMissing)）")
	}
}

// 命令行与 app 必须共用同一套判据 —— 包括新加的亮度/黑场这一项。
func TestCLIAndAppShareTheLumaJudgement(t *testing.T) {
	cli := readSrc(t, "../../cmd/auditfilm/main.go")
	if !strings.Contains(cli, "filmaudit.LumaFromFFmpeg(") {
		t.Error("cmd/auditfilm 没接亮度/黑场量尺 —— 命令行与 app 的结论会分叉")
	}
	if !strings.Contains(cli, "filmaudit.LoadEpisode(dir, probe, tracks, luma)") {
		t.Error("cmd/auditfilm 没把量尺交给 LoadEpisode —— 命令行那边永远量不到")
	}
	// 命令行也要**如实播报没量到**（不许静默跳过、更不许当成合格）。
	if !strings.Contains(cli, "len(in.Luma) == 0") {
		t.Error("cmd/auditfilm 在没量到亮度时一句话都不说 —— 用户会以为「这集画面亮度没问题」")
	}
}
