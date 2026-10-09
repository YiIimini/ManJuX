package compose

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

// engine_test.go —— 装配引擎（assemble_mpy.py）对接层的契约测试。
//
// 这些判据全是**纯函数 / 源码级**的，不需要真机素材；端到端那条在 compose_test.go
// （requireEngine 会给它挑一个装了 MoviePy 的解释器，没有就跳过）。
//
// 反向验证该咬的针：
//   - TestEngineScriptHasContractKeys：脚本里少了 --manifest / ASM-DONE / write_timeline … 就红；
//   - TestManifestForShape：fades 长度不是 n-1、路径不是绝对路径就红；
//   - TestParseAsmOutThreeStates：三态解析被合并/顺序写反就红；
//   - TestValidateTimeline：少一镜 / 起点倒退 / 末镜对不上画面时长就红；
//   - TestComposeWiringUsesEngineAndDropsXfade：Compose 不调引擎、或自写 xfade 链没删干净就红。

// ① 内嵌脚本必须含契约关键点（脚本是 go:embed 进来的，改坏一个字这里就该说话）。
func TestEngineScriptHasContractKeys(t *testing.T) {
	raw, err := asmScriptFS.ReadFile("assemble_mpy.py")
	if err != nil {
		t.Fatalf("读不到内嵌脚本 assemble_mpy.py：%v", err)
	}
	src := string(raw)
	for _, key := range []string{"--manifest", "ASM-DONE", "ASM-FAIL", "write_timeline", "padding=-", "color_args"} {
		if !strings.Contains(src, key) {
			t.Errorf("内嵌脚本缺少契约关键点 %q —— 平台按它对接，少了就会静默跑错", key)
		}
	}
}

// ② manifest 的字段名/长度/绝对路径。fades 长度**必须 = 镜头数-1**，否则引擎会把接缝算错。
func TestManifestForShape(t *testing.T) {
	parts := []string{`C:\w\a.mp4`, `C:\w\b.mp4`, `C:\w\c.mp4`}
	fades := []float64{0.5, 0.0}
	out := `C:\w\master.mkv`
	tl := `C:\w\asm_timeline.json`
	raw, err := manifestFor(parts, fades, 24, out, tl, nil)
	if err != nil {
		t.Fatalf("manifestFor 正常输入应通过，实际：%v", err)
	}
	// 键名是跨进程契约，逐字钉住。
	for _, key := range []string{`"clips"`, `"fades"`, `"fps"`, `"out"`, `"timeline"`, `"audio_codec"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("manifest 缺键 %s（与 assemble_mpy.py 的契约对不上）", key)
		}
	}
	var m asmManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest 不是合法 JSON：%v", err)
	}
	if len(m.Clips) != 3 {
		t.Errorf("clips 该是 3 条，实际 %d", len(m.Clips))
	}
	if len(m.Fades) != 2 {
		t.Errorf("fades 长度必须 = 镜头数-1 = 2，实际 %d", len(m.Fades))
	}
	if m.FPS != 24 || m.Out != out || m.Timeline != tl || m.AudioCodec != "flac" {
		t.Errorf("manifest 字段不对：%+v", m)
	}
	// 色彩标记：平台量完全片口径才会给，给了必须**逐字**进 manifest（引擎按它写中间轨的标记）；
	// 不给（全片口径不统一）就必须连键都不出现 —— 不许替平台猜一个"统一"。
	if strings.Contains(string(raw), "color_args") {
		t.Error("没给色彩标记时 manifest 里不许出现 color_args（否则引擎会写一个平台没说过的口径）")
	}
	withColor, err := manifestFor(parts, fades, 24, out, tl, []string{"-colorspace", "bt709"})
	if err != nil {
		t.Fatalf("带色彩标记的 manifestFor 应通过，实际：%v", err)
	}
	var mc asmManifest
	if err := json.Unmarshal(withColor, &mc); err != nil {
		t.Fatalf("带色彩标记的 manifest 不是合法 JSON：%v", err)
	}
	if len(mc.ColorArgs) != 2 || mc.ColorArgs[0] != "-colorspace" || mc.ColorArgs[1] != "bt709" {
		t.Errorf("色彩标记必须逐字透传给引擎，实际 %v", mc.ColorArgs)
	}
	// fades 多一条（长度 = n）必须报错，不许静默截断。
	if _, err := manifestFor(parts, []float64{0.5, 0, 0}, 24, out, tl, nil); err == nil {
		t.Error("fades 长度不等于 n-1 必须报错（否则接缝会被算错）")
	}
	// 相对路径必须报错（契约：路径一律绝对路径）。
	if _, err := manifestFor([]string{"a.mp4"}, nil, 24, out, tl, nil); err == nil {
		t.Error("clips 里的相对路径必须报错")
	}
	if _, err := manifestFor(parts, fades, 24, "master.mkv", tl, nil); err == nil {
		t.Error("out 的相对路径必须报错")
	}
	// 空输入必须报错。
	if _, err := manifestFor(nil, nil, 24, out, tl, nil); err == nil {
		t.Error("clips 为空必须报错")
	}
}

// engineFades 必须把「长度 = 镜数、末项恒 0」的 seamFades 产物裁成 n-1 条。
func TestEngineFadesLength(t *testing.T) {
	if got := engineFades([]float64{0, 0, 0}); len(got) != 2 {
		t.Errorf("3 镜该给 2 条接缝，实际 %v", got)
	}
	if got := engineFades([]float64{0}); len(got) != 0 {
		t.Errorf("单镜没有接缝，该是空，实际 %v", got)
	}
	if got := engineFades(nil); len(got) != 0 {
		t.Errorf("空输入该是空，实际 %v", got)
	}
}

// ③ stdout 三态解析（顺序关键：ASM-FAIL / ASM-DONE 也都以 "ASM" 开头，只有进度行带空格）。
// 夹具里放**真机那两行**（叠化 2 镜 video=18.749；硬切 3 镜 video=29.874 frames=717 expect=29.875）。
func TestParseAsmOutThreeStates(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		kind    string
		payload string
	}{
		{"进度行", "ASM 输入 2 镜 fps=24 fades=[0.5]", asmKindProgress, "输入 2 镜 fps=24 fades=[0.5]"},
		{"真机叠化 2 镜完成行",
			"ASM-DONE video=18.749 audio=18.750 frames=450 expect=18.750 out=C:/w/master.mkv elapsed=3.2s",
			asmKindDone, "video=18.749 audio=18.750 frames=450 expect=18.750 out=C:/w/master.mkv elapsed=3.2s"},
		{"真机硬切 3 镜完成行",
			"ASM-DONE video=29.874 audio=29.875 frames=717 expect=29.875 out=C:/w/master.mkv elapsed=4.1s",
			asmKindDone, "video=29.874 audio=29.875 frames=717 expect=29.875 out=C:/w/master.mkv elapsed=4.1s"},
		{"真机硬切退回引擎那一行",
			"ASM 硬切复制出来的画面 30.046 秒 比各镜合计 29.875 秒 多 0.171 秒 ⇒ 改用引擎重编",
			asmKindProgress, "硬切复制出来的画面 30.046 秒 比各镜合计 29.875 秒 多 0.171 秒 ⇒ 改用引擎重编"},
		{"失败行", "ASM-FAIL 拼接失败: 缺片段: shot_004.mp4", asmKindFail, "拼接失败: 缺片段: shot_004.mp4"},
		{"无关行", "ffmpeg version 7.0", asmKindOther, "ffmpeg version 7.0"},
	}
	for _, c := range cases {
		kind, payload := parseAsmOut(c.line)
		if kind != c.kind {
			t.Errorf("%s：kind 该是 %q，实际 %q", c.name, c.kind, kind)
		}
		if payload != c.payload {
			t.Errorf("%s：payload 该是 %q，实际 %q", c.name, c.payload, payload)
		}
	}
	// 反向针：ASM-FAIL 不能被"以 ASM 开头"当成进度行（判据顺序写反就红）。
	if k, _ := parseAsmOut("ASM-FAIL x"); k == asmKindProgress {
		t.Error("ASM-FAIL 被判成进度行 —— 失败会被当成进度吞掉")
	}
	// 反向针：只有带空格的 "ASM " 才是进度，光秃秃的 "ASM" 不是。
	if k, _ := parseAsmOut("ASM"); k == asmKindProgress {
		t.Error("裸 \"ASM\" 不该被当成进度行")
	}
}

// ④ 时间轴校验：合法的通过；少一镜 / 起点倒退 / 第 1 镜起点非 0 / 末镜对不上画面时长，都必须报错。
func TestValidateTimeline(t *testing.T) {
	parts := []string{`C:\w\a.mp4`, `C:\w\b.mp4`, `C:\w\c.mp4`}
	ok := &asmTimeline{VideoDuration: 29.874, Expected: 29.875, FPS: 24, Clips: []asmTLClip{
		{Path: `C:\w\a.mp4`, Start: 0, Duration: 9.833},
		{Path: `C:\w\b.mp4`, Start: 9.833, Duration: 9.417},
		{Path: `C:\w\c.mp4`, Start: 19.25, Duration: 10.624},
	}}
	starts, err := validateTimeline(ok, parts)
	if err != nil {
		t.Fatalf("合法时间轴该通过，实际：%v", err)
	}
	if len(starts) != 3 || starts[0] != 0 {
		t.Fatalf("starts 应逐镜等于 clips[i].start，实际 %v", starts)
	}
	if math.Abs(starts[2]-19.25) > 1e-9 {
		t.Errorf("第 3 镜起点该是 19.25，实际 %.3f", starts[2])
	}

	// 少一镜（真机那种"引擎只拼了前几镜"的形态）
	short := &asmTimeline{VideoDuration: 19.25, Clips: []asmTLClip{
		{Start: 0, Duration: 9.833}, {Start: 9.833, Duration: 9.417},
	}}
	if _, err := validateTimeline(short, parts); err == nil {
		t.Error("时间轴少一镜必须报错（否则少拼的那一镜会被静默放行）")
	}

	// 起点倒退
	back := &asmTimeline{VideoDuration: 29.874, Clips: []asmTLClip{
		{Start: 0, Duration: 9.833}, {Start: 9.833, Duration: 9.417}, {Start: 5, Duration: 10.624},
	}}
	if _, err := validateTimeline(back, parts); err == nil {
		t.Error("起点倒退必须报错")
	}

	// 第 1 镜起点不是 0
	notZero := &asmTimeline{VideoDuration: 29.874, Clips: []asmTLClip{
		{Start: 1.0, Duration: 9.833}, {Start: 9.833, Duration: 9.417}, {Start: 19.25, Duration: 10.624},
	}}
	if _, err := validateTimeline(notZero, parts); err == nil {
		t.Error("第 1 镜起点不是 0 必须报错")
	}

	// 末镜结束与引擎回报的画面时长差 > 0.5 秒
	bad := &asmTimeline{VideoDuration: 10.0, Clips: []asmTLClip{
		{Start: 0, Duration: 9.833}, {Start: 9.833, Duration: 9.417}, {Start: 19.25, Duration: 10.624},
	}}
	if _, err := validateTimeline(bad, parts); err == nil {
		t.Error("末镜结束与画面时长差超过 0.5 秒必须报错")
	}

	// nil 也不许放行
	if _, err := validateTimeline(nil, parts); err == nil {
		t.Error("没有时间轴必须报错")
	}
}

// ⑤ 接线门：Compose 必须调装配引擎，且自写的 xfade 链必须**整条删除**。
//
// 【为什么盯"生效的那一行"】判据只在实际代码行上做（整行注释先剥掉）—— 否则把
// `stitchWithEngine(` 写进一句注释里也能让这道门变绿，等于摆设守卫。
func TestComposeWiringUsesEngineAndDropsXfade(t *testing.T) {
	raw, err := os.ReadFile("compose.go")
	if err != nil {
		t.Fatalf("读不到 compose.go：%v", err)
	}
	src := string(raw)
	if !hasCodeLine(src, "stitchWithEngine(") {
		t.Error("Compose 必须调用 stitchWithEngine 拼接 —— 换回自己拼图就重新踩真机 P0")
	}
	for _, banned := range []string{"xfade=transition=", "xfadeOffsets", "stitchWithTransitions", "stitchCut", "verifyStitchedLength"} {
		if hasCodeLine(src, banned) {
			t.Errorf("自写的 xfade 链应已整条删除，但代码里仍出现 %q", banned)
		}
	}
}

// hasCodeLine 判断 needle 是否出现在**实际代码行**上（整行注释与空行先剥掉）。
func hasCodeLine(src, needle string) bool {
	for _, ln := range strings.Split(src, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		if strings.Contains(ln, needle) {
			return true
		}
	}
	return false
}
