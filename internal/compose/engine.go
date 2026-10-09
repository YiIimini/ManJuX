package compose

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// engine.go —— 拼接与接缝转场交给**成熟的装配引擎**（MoviePy；脚本见 assemble_mpy.py）。
//
// 【为什么不再自己拼 ffmpeg 滤镜图（2026-10-09 真机 P0）】
//
//	平台原来用一条 `-filter_complex` 把 N 个镜头串成 xfade 链。真机《被开除一年后…第01集》
//	因此合出**画面 23.708 秒（569 帧 = 只有前 3 镜）**而**音轨 122.499 秒** —— 第 4 镜起
//	画面一直定格、声音照常走，而日志报「成片就绪 122 秒」。根因是 xfade 在「过渡窗口末端
//	越过前一镜真实末帧」时**静默丢弃后面所有输入、退出码仍是 0**。
//	「自己算偏移、自己拼图」正是成熟引擎每天在做的事 ⇒ 交给它，平台只负责喂输入与核对结果。
//
// 【分工】
//
//	① 拼接与转场由 assemble_mpy.py 完成（逐镜拼接 + 接缝交叉淡入淡出；全硬切走 concat 复制）；
//	② 平台把 manifest 写盘、起 python、**逐行读 stdout**（ASM 进度 / ASM-FAIL / ASM-DONE）；
//	③ 引擎把**真实时间轴**（每镜在成片里的起点）写进 timeline JSON —— 字幕落点、成片总长、
//	   母版时长全部读这一份，平台不再自己按 fades 累加推。
//
//go:embed assemble_mpy.py
var asmScriptFS embed.FS

// asmFPS 成片帧率。H3 渲染即 24fps（`ClipManifest.Length = duration×24` 是同一口径）。
const asmFPS = 24.0

// asmAudioCodec 中间轨的音频编码。中间轨一律无损（后面还要做响度归一），所以是 flac。
const asmAudioCodec = "flac"

// asmEndTolerance 时间轴自洽与实测比对允许的偏差（秒）。取 0.5：一帧 @24fps 是 0.0417，
// 半秒足够容下整数帧对齐带来的零头，又能咬住「少了一整镜」这种量级的事故。
const asmEndTolerance = 0.5

// asmStartEpsilon 第 1 镜起点必须（约）为 0 的容差。
const asmStartEpsilon = 1e-6

// asmManifest 引擎 manifest（键名与 assemble_mpy.py 的契约一致，路径一律绝对路径）。
type asmManifest struct {
	Clips      []string  `json:"clips"`
	Fades      []float64 `json:"fades"`
	FPS        float64   `json:"fps"`
	Out        string    `json:"out"`
	Timeline   string    `json:"timeline"`
	AudioCodec string    `json:"audio_codec"`
	// ColorArgs 写进中间轨的色彩标记（全片口径统一时非空，见 colorDecision.masterArgs）。
	ColorArgs []string `json:"color_args,omitempty"`
}

// asmTimeline 引擎回报的真实时间轴。
type asmTimeline struct {
	VideoDuration float64     `json:"video_duration"`
	Expected      float64     `json:"expected"`
	FPS           float64     `json:"fps"`
	Clips         []asmTLClip `json:"clips"`
}

// asmTLClip 单个镜头在成片里的真实起点与时长。
type asmTLClip struct {
	Path     string  `json:"path"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
}

// stdout 行的三态（ASM 进度 / ASM-FAIL / ASM-DONE）与其它。
const (
	asmKindProgress = "progress"
	asmKindFail     = "fail"
	asmKindDone     = "done"
	asmKindOther    = "other"
)

// manifestFor 生成引擎 manifest 的 JSON。纯函数，可脱离进程单测。
//
// 校验（任何一条不满足就报错，不许把坏输入喂给引擎）：
//   - clips 非空；
//   - fades 长度**必须 = 镜头数-1**（逐接缝，末镜后面没有接缝）；
//   - clips / out / timeline 一律绝对路径；
//   - 叠化时长非负、非 NaN。
//   - colorArgs 逐字透传给引擎（引擎重编码像素 ⇒ 色彩标记必须由它写进中间轨，
//     否则成片会丢掉「这段像素是什么口径」，播放器只能猜）。空 = 全片口径不统一，谁都不许写。
func manifestFor(parts []string, fades []float64, fps float64, out, timeline string, colorArgs []string) ([]byte, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("装配引擎的 clips 为空")
	}
	if len(fades) != len(parts)-1 {
		return nil, fmt.Errorf("装配引擎的 fades 长度必须 = 镜头数-1（%d），实际 %d", len(parts)-1, len(fades))
	}
	for i, p := range parts {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("装配引擎要求绝对路径，第 %d 镜是相对路径：%s", i+1, p)
		}
	}
	if out == "" || !filepath.IsAbs(out) {
		return nil, fmt.Errorf("装配引擎要求 out 是绝对路径，实际「%s」", out)
	}
	if timeline == "" || !filepath.IsAbs(timeline) {
		return nil, fmt.Errorf("装配引擎要求 timeline 是绝对路径，实际「%s」", timeline)
	}
	for i, d := range fades {
		if math.IsNaN(d) || d < 0 {
			return nil, fmt.Errorf("第 %d 处接缝的叠化时长非法：%v", i+1, d)
		}
	}
	if fps <= 0 {
		fps = asmFPS
	}
	m := asmManifest{Clips: parts, Fades: fades, FPS: fps, Out: out, Timeline: timeline,
		AudioCodec: asmAudioCodec, ColorArgs: colorArgs}
	return json.MarshalIndent(m, "", " ")
}

// parseAsmOut 解析引擎的一行 stdout。纯函数，可脱离进程单测。
//
//	ASM <说明>            ⇒ (asmKindProgress, 说明)
//	ASM-FAIL <原因>       ⇒ (asmKindFail, 原因)
//	ASM-DONE video=...    ⇒ (asmKindDone, "video=...")
//	其它                  ⇒ (asmKindOther, 原行)
//
// 先判 ASM-FAIL / ASM-DONE 再判 "ASM "：两者都以 "ASM" 开头，但只有进度行带那一个空格。
func parseAsmOut(line string) (kind, payload string) {
	s := strings.TrimRight(line, "\r\n")
	switch {
	case strings.HasPrefix(s, "ASM-FAIL"):
		return asmKindFail, strings.TrimSpace(strings.TrimPrefix(s, "ASM-FAIL"))
	case strings.HasPrefix(s, "ASM-DONE"):
		return asmKindDone, strings.TrimSpace(strings.TrimPrefix(s, "ASM-DONE"))
	case strings.HasPrefix(s, "ASM "):
		return asmKindProgress, strings.TrimPrefix(s, "ASM ")
	default:
		return asmKindOther, s
	}
}

// validateTimeline 校验引擎回报的时间轴并返回每镜**真实起点**（秒）。
//
// 任何一条不满足都报错（不许静默继续）：
//   - 镜头数必须与输入一致（少一镜 = 引擎漏拼了整段，正是真机那次的形态）；
//   - 第 1 镜起点必须为 0；
//   - 起点单调不减；
//   - 末镜结束（起点+时长）与引擎回报的画面时长之差 ≤ asmEndTolerance。
func validateTimeline(tl *asmTimeline, parts []string) ([]float64, error) {
	if tl == nil {
		return nil, fmt.Errorf("装配引擎没有回报时间轴")
	}
	if len(tl.Clips) != len(parts) {
		return nil, fmt.Errorf("装配引擎回报的时间轴有 %d 镜，而输入是 %d 镜 —— 引擎少拼/多拼了镜头", len(tl.Clips), len(parts))
	}
	if tl.VideoDuration <= 0 {
		return nil, fmt.Errorf("装配引擎回报的画面时长为 %.3f 秒（无效）", tl.VideoDuration)
	}
	starts := make([]float64, len(tl.Clips))
	for i, c := range tl.Clips {
		if c.Duration <= 0 {
			return nil, fmt.Errorf("时间轴第 %d 镜的时长为 %.3f 秒（无效）", i+1, c.Duration)
		}
		starts[i] = c.Start
	}
	if math.Abs(starts[0]) > asmStartEpsilon {
		return nil, fmt.Errorf("时间轴第 1 镜的起点应为 0，实际 %.3f", starts[0])
	}
	for i := 1; i < len(starts); i++ {
		if starts[i] < starts[i-1]-asmStartEpsilon {
			return nil, fmt.Errorf("时间轴起点倒退：第 %d 镜 %.3f < 第 %d 镜 %.3f", i+1, starts[i], i, starts[i-1])
		}
	}
	last := tl.Clips[len(tl.Clips)-1]
	if end := last.Start + last.Duration; math.Abs(end-tl.VideoDuration) > asmEndTolerance {
		return nil, fmt.Errorf("时间轴自相矛盾：末镜结束 %.3f 秒 与引擎回报的画面时长 %.3f 秒 差 %.3f 秒（>%.1f）",
			end, tl.VideoDuration, math.Abs(end-tl.VideoDuration), asmEndTolerance)
	}
	return starts, nil
}

// engineFades 把 seamFades 的产物（长度 = 镜数、末项恒 0）裁成引擎要的逐接缝数组
// （长度 = 镜数-1）。单镜时为空数组（引擎的契约如此）。
func engineFades(fades []float64) []float64 {
	if len(fades) <= 1 {
		return []float64{}
	}
	return fades[:len(fades)-1]
}

// stitchWithEngine 调装配引擎拼接 parts，返回中间轨路径与每镜**真实起点**。
//
// 过程：脚本与 manifest 写进 work → 起 python → 逐行读 stdout（ASM 进度原样播报给用户）
// → 进程退出码非 0 或没看到 ASM-DONE 就报错（错误里带引擎最后几行输出与可复现命令）
// → 读回 timeline JSON 并校验 → 与 ffprobe 实测的画面时长交叉核对。
func stitchWithEngine(ctx context.Context, f *ff.FF, parts []string, fades []float64, work, toolDir string, colorArgs []string, onProgress func(int, string)) (string, []float64, error) {
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("没有可拼接的镜头")
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", nil, err
	}
	script, err := writeEngineScript(work)
	if err != nil {
		return "", nil, err
	}
	master := filepath.Join(work, "master.mkv")
	timeline := filepath.Join(work, "asm_timeline.json")
	_ = os.Remove(timeline) // 旧结果不许冒充这一次的
	raw, err := manifestFor(parts, fades, asmFPS, master, timeline, colorArgs)
	if err != nil {
		return "", nil, err
	}
	manifest := filepath.Join(work, "asm_manifest.json")
	if err := os.WriteFile(manifest, raw, 0o644); err != nil {
		return "", nil, err
	}
	python, err := enginePython(ctx, toolDir, onProgress)
	if err != nil {
		return "", nil, err
	}
	if onProgress != nil {
		onProgress(60, fmt.Sprintf("拼接（装配引擎 MoviePy）:%d 镜 / %d 处接缝", len(parts), len(fades)))
	}
	// **必须经 sysexec.Hide**（平台是 windowsgui 程序，不加 CREATE_NO_WINDOW 的控制台子进程
	// 每次调用都会在桌面上闪一个黑窗）。python 侧已设 PYTHONIOENCODING=utf-8 与 PYTHONUTF8，
	// 中文进度行不会被 GBK 管道带坏。
	cmd := sysexec.Hide(exec.CommandContext(ctx, python, script, "--manifest", manifest))
	// 引擎的临时文件（MoviePy 的 *_wvf_snd.flac 等）落在 cwd ⇒ 切到 work，随成功一起清掉。
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	var errTail tailBuffer
	errTail.max = 4096
	cmd.Stderr = &errTail
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("起不动装配引擎（%s）：%w", python, err)
	}
	var tail []string
	sawDone := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		tail = appendTail(tail, line, 12)
		kind, payload := parseAsmOut(line)
		switch kind {
		case asmKindProgress:
			if onProgress != nil && strings.TrimSpace(payload) != "" {
				onProgress(60, payload)
			}
		case asmKindDone:
			sawDone = true
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()
	cmdline := fmt.Sprintf("\"%s\" \"%s\" --manifest \"%s\"", python, script, manifest)
	if waitErr != nil || scanErr != nil || !sawDone {
		why := "引擎没有报告 ASM-DONE"
		if waitErr != nil {
			why = waitErr.Error()
		} else if scanErr != nil {
			why = "读引擎输出失败: " + scanErr.Error()
		}
		return "", nil, fmt.Errorf("拼接失败（%s）\n引擎最后几行输出:\n%s\nstderr:\n%s\n可复现命令: %s",
			why, strings.Join(tail, "\n"), tailText(errTail.String(), 1200), cmdline)
	}
	tlRaw, rerr := os.ReadFile(timeline)
	if rerr != nil {
		return "", nil, fmt.Errorf("引擎没写出时间轴（%s）：%w\n可复现命令: %s", timeline, rerr, cmdline)
	}
	var tl asmTimeline
	if jerr := json.Unmarshal(tlRaw, &tl); jerr != nil {
		return "", nil, fmt.Errorf("引擎时间轴解析失败（%s）：%w", timeline, jerr)
	}
	starts, verr := validateTimeline(&tl, parts)
	if verr != nil {
		return "", nil, verr
	}
	// 交叉核对：引擎**自报**的画面时长 vs 我们 ffprobe 实测的中间轨（两个独立来源，
	// 一个来源说谎不该被当成真相）。量不到就不拦（这里是兜底，不是唯一判据）。
	if f != nil {
		if info, perr := f.Probe(master); perr == nil && info != nil && info.VideoDuration > 0 &&
			math.Abs(info.VideoDuration-tl.VideoDuration) > asmEndTolerance {
			return "", nil, fmt.Errorf("引擎回报的画面 %.3f 秒 与实测中间轨 %.3f 秒 差 %.3f 秒（>%.1f）—— 拒绝交付",
				tl.VideoDuration, info.VideoDuration, math.Abs(info.VideoDuration-tl.VideoDuration), asmEndTolerance)
		}
	}
	if _, serr := os.Stat(master); serr != nil {
		return "", nil, fmt.Errorf("引擎报成功但没有中间轨（%s）：%w", master, serr)
	}
	return master, starts, nil
}

// writeEngineScript 把内嵌的装配脚本落到 work（每次覆盖：脚本随版本走，不留旧副本）。
func writeEngineScript(dir string) (string, error) {
	data, err := asmScriptFS.ReadFile("assemble_mpy.py")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "assemble_mpy.py")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// enginePython 找到可用的 python 解释器。顺序：
//
//	① 环境变量 MANJUX_MPY_PYTHON
//	② <toolDir>/mpy-venv/Scripts/python.exe
//	③ 都没有 ⇒ 首次引导创建（uv 优先，退回 python -m venv），失败如实报错并给出可手工执行的命令。
//
// 引导**失败不许静默退回旧链**（旧链已整条删掉）：宁可报错并说清怎么手工装。
func enginePython(ctx context.Context, toolDir string, onProgress func(int, string)) (string, error) {
	if py := strings.TrimSpace(os.Getenv("MANJUX_MPY_PYTHON")); py != "" {
		if _, err := os.Stat(py); err != nil {
			return "", fmt.Errorf("环境变量 MANJUX_MPY_PYTHON 指向的解释器不存在：%s", py)
		}
		return py, nil
	}
	if strings.TrimSpace(toolDir) == "" {
		toolDir = filepath.Join(os.TempDir(), "manjux-mpy")
	}
	venv := filepath.Join(toolDir, "mpy-venv")
	py := filepath.Join(venv, "Scripts", "python.exe")
	if _, err := os.Stat(py); err == nil {
		return py, nil
	}
	if onProgress != nil {
		onProgress(60, fmt.Sprintf("首次使用装配引擎：正在准备 python 环境（%s，装 MoviePy，只做这一次）", venv))
	}
	if err := bootstrapEngineEnv(ctx, venv); err != nil {
		return "", err
	}
	if _, err := os.Stat(py); err != nil {
		return "", fmt.Errorf("装配引擎环境准备失败：没有生成「%s」\n请手工执行：\n"+
			"  uv venv \"%s\"\n  uv pip install --python \"%s\" moviepy\n"+
			"或（没有 uv 时）：\n  python -m venv \"%s\"\n  \"%s\" -m pip install moviepy",
			py, venv, py, venv, py)
	}
	return py, nil
}

// bootstrapEngineEnv 首次引导：优先 uv，退回 python -m venv。失败信息里带用户可手工执行的命令。
func bootstrapEngineEnv(ctx context.Context, venv string) error {
	if err := os.MkdirAll(filepath.Dir(venv), 0o755); err != nil {
		return err
	}
	venvPy := filepath.Join(venv, "Scripts", "python.exe")
	if uv, err := exec.LookPath("uv"); err == nil {
		if out, verr := runHidden(ctx, uv, "venv", venv); verr != nil {
			return fmt.Errorf("uv venv 失败（%v）：%s\n请手工执行：uv venv \"%s\"", verr, tailText(out, 800), venv)
		}
		if out, verr := runHidden(ctx, uv, "pip", "install", "--python", venvPy, "moviepy"); verr != nil {
			return fmt.Errorf("uv pip install moviepy 失败（%v）：%s\n请手工执行：uv pip install --python \"%s\" moviepy",
				verr, tailText(out, 800), venvPy)
		}
		return nil
	}
	base, err := exec.LookPath("python")
	if err != nil {
		if p2, e2 := exec.LookPath("py"); e2 == nil {
			base = p2
		} else {
			return fmt.Errorf("本机既没有 uv 也没有 python —— 无法准备装配引擎环境。\n请任选其一：\n"+
				"  ① 装 uv（推荐）：uv venv \"%s\" && uv pip install --python \"%s\" moviepy\n"+
				"  ② 装 python 后：python -m venv \"%s\" && \"%s\" -m pip install moviepy",
				venv, venvPy, venv, venvPy)
		}
	}
	if out, verr := runHidden(ctx, base, "-m", "venv", venv); verr != nil {
		return fmt.Errorf("python -m venv 失败（%v）：%s\n请手工执行：python -m venv \"%s\"", verr, tailText(out, 800), venv)
	}
	if out, verr := runHidden(ctx, venvPy, "-m", "pip", "install", "moviepy"); verr != nil {
		return fmt.Errorf("pip install moviepy 失败（%v）：%s\n请手工执行：\"%s\" -m pip install moviepy",
			verr, tailText(out, 800), venvPy)
	}
	return nil
}

// runHidden 跑一条准备环境的命令（同样必须隐藏窗口）。
func runHidden(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := sysexec.Hide(exec.CommandContext(ctx, bin, args...))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// tailBuffer 只保留最后 max 字节的 writer（引擎的 stderr 可能很长，错误里只贴尾部）。
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if t.max > 0 && len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// appendTail 把行追加进只留 keep 条的环形切片。
func appendTail(lines []string, line string, keep int) []string {
	lines = append(lines, line)
	if keep > 0 && len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	return lines
}

// tailText 取文本尾部的 n 个字符（报错信息里贴太多没意义）。
func tailText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}
