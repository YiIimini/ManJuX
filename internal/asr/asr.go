// Package asr 调用本机的 faster-whisper 转写，**核对成片里到底说了什么**。
//
// 【为什么平台必须有这把尺子（用户 2026-10-07：「配音存在乱配音」）】
// H3 是音画同生的模型：台词说不说、说什么、有没有重复，都发生在**音轨里**，
// 而字幕是平台后期自己烧上去的 —— 两者是两回事。没有 ASR 时只能量到"音频结构"
// （见 internal/audio 的人声包络），量不到"念的是不是那句台词"。
//
// 真机第01集 13 镜实测（medium 模型，CPU int8，每镜 10~15 秒）抓到的三类问题：
//
//	镜 4 台词「已读取。无对应档位。」→ 音轨把它念了**两遍**
//	镜 7 台词「自检窗口，七十二标准时。」→ 念了**三遍**
//	镜 3 台词只有两句（80000。/读我的钢印。硬件不认名册。）→ 音轨多出一整串
//	     「跟强我的对数还修戴眼影一下,也续点倾向」，而第一句 80000 根本没念
//	镜 11 内心戏 → 音轨多出一串「图墙白头圣呢﹔裙尾便宜着人呢」
//
// 这些正是用户说的"乱配音"。判据在 compare.go（纯函数、可测），转写与调用在这里。
//
// 工具位置：本机装在 D:\Ai\Tools\ASR（独立 venv，与 ComfyUI 环境隔离），
// 入口 asr.cmd / transcribe.py 都可以直接用；**没装时如实报"跳过"，不假装验过**。
package asr

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// Segment 一段转写（秒 + 文本）。
type Segment struct {
	Start, End float64
	Text       string
}

// Result 一次转写的结果。
type Result struct {
	Tool     string // 用的哪个入口（诊断用）
	Model    string
	Lang     string
	Prob     float64
	Duration float64
	Segments []Segment
	// Words 词级时间戳（逐词起止）。**老结果文件里没有这一段**（工具升级前不写），
	// 那时这里是 nil ⇒ 上层自动退回段级落点（见 internal/compose/subtitle.go 的四档）。
	//
	// 【为什么要有（2026-10-09 用户：「看看全网上有没有最优的解决方案？」）】成片语音是 H3
	// 音画同生生的，**没有任何现成时间戳**；字幕要贴在人声上只能事后对齐 —— 业界标准就是
	// forced alignment（WhisperX 的 wav2vec2 对齐 / stable-ts 的 align，arXiv 2303.00747）。
	// 真机取证：镜 10 的 ASR 只有 2 段、台词也是 2 句，可"段"与"句"的边界并不重合
	// （段 4.18-6.30 里其实是「工牌呢」，段 6.30-7.84 是「被收了」），段级判据**判不了**这件事，
	// 词级才能（见 align.go 的 AlignLines）。
	Words []Word
}

// Word 一个词的起止 + 文本。
type Word struct {
	Start, End float64
	Text       string
}

// Text 全部转写文本（按段拼）。
func (r *Result) Text() string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	for _, s := range r.Segments {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

// Locate 找本机的 ASR 入口。顺序：环境变量 MANJUX_ASR_CMD → D:\Ai\Tools\ASR\asr.cmd →
// PATH 上的 asr.cmd / asr。找不到返回空串（调用方要如实播报"跳过"）。
func Locate() string {
	if p := strings.TrimSpace(os.Getenv("MANJUX_ASR_CMD")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, p := range []string{
		`D:\Ai\Tools\ASR\asr.cmd`,
		`D:\Ai\Tools\ASR\asr.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, name := range []string{"asr.cmd", "asr"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// segRe 转写文件里的一行：`[  0.34-  7.84] 文本`。
var segRe = regexp.MustCompile(`^\[\s*([0-9.]+)\s*-\s*([0-9.]+)\s*\]\s*(.*)$`)

// metaRe 头部注释：`# model=medium lang=zh prob=1.00 dur=7.3s segs=1`。
var metaRe = regexp.MustCompile(`model=(\S+)\s+lang=(\S+)\s+prob=([0-9.]+)\s+dur=([0-9.]+)s`)

// wordRe 词级行：`w   4.18-  6.30 公`（工具 2026-10-09 起写的第二段）。
//
// 刻意用 `w ` 前缀而不是 `[...]`：老的解析器只认 `[start-end] 文本`，多出来的这类行
// 会被它忽略 ⇒ **工具升级不会打断老版本平台**（反过来，老工具不写这一段，
// 上层自动退回段级落点）。
var wordRe = regexp.MustCompile(`^w\s+([0-9.]+)\s*-\s*([0-9.]+)\s+(\S.*)$`)

// Parse 读一份转写结果文件（asr.cmd 的 `--out` 产物）。
func Parse(path string) (*Result, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	r := &Result{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if strings.HasPrefix(line, "#") {
			if m := metaRe.FindStringSubmatch(line); m != nil {
				r.Model, r.Lang = m[1], m[2]
				r.Prob, _ = strconv.ParseFloat(m[3], 64)
				r.Duration, _ = strconv.ParseFloat(m[4], 64)
			}
			continue
		}
		if m := wordRe.FindStringSubmatch(line); m != nil {
			ws, _ := strconv.ParseFloat(m[1], 64)
			we, _ := strconv.ParseFloat(m[2], 64)
			r.Words = append(r.Words, Word{Start: ws, End: we, Text: strings.TrimSpace(m[3])})
			continue
		}
		if m := segRe.FindStringSubmatch(line); m != nil {
			st, _ := strconv.ParseFloat(m[1], 64)
			en, _ := strconv.ParseFloat(m[2], 64)
			r.Segments = append(r.Segments, Segment{Start: st, End: en, Text: strings.TrimSpace(m[3])})
		}
	}
	return r, sc.Err()
}

// Transcribe 跑一次转写。media 是**本地文件绝对路径**；model 空则用 medium。
// 结果文件由工具自己写在 media 同级（`<media>.asr.txt`），这里读回来。
//
// 超时由调用方通过 ctx 控制：CPU medium 实测≈每 10 秒素材 12~15 秒（含 10 秒模型加载）。
func Transcribe(ctx context.Context, tool, media, model string) (*Result, error) {
	if tool == "" {
		return nil, fmt.Errorf("没有找到 ASR 入口（见 asr.Locate）")
	}
	// **先转成绝对路径再交给工具**：下面会把子进程的工作目录切成素材所在目录（工具的
	// --out 与写回的文件都按 cwd 解析），此时再传相对路径就成了"相对 cwd 的相对路径"——
	// 实测就是这么炸的：`clips/shot_003.mp4` + `cmd.Dir=clips` ⇒ 工具报 media not found。
	abs, err := filepath.Abs(media)
	if err != nil {
		return nil, fmt.Errorf("素材路径解析失败: %w", err)
	}
	media = abs
	if _, err := os.Stat(media); err != nil {
		return nil, fmt.Errorf("素材不存在: %w", err)
	}
	if strings.TrimSpace(model) == "" {
		model = "medium"
	}
	out := strings.TrimSuffix(media, filepath.Ext(media)) + ".asr.txt"
	_ = os.Remove(out) // 旧结果不许冒充这一次的
	// **必须经 sysexec.Hide**（2026-10-09 用户：「质检时一直不停重复 显示/关闭 终端窗口」）：
	// 语音对账是**逐镜**跑的，直接 exec 时每一镜都要在桌面上闪一个黑窗（十几镜就是十几次）。
	// 结果本来就一律从**文件**读（下面 Parse(out)），stdout 只用于报错，隐藏窗口不影响取数。
	cmd := sysexec.Hide(exec.CommandContext(ctx, tool, media, "--model", model, "--out", out))
	cmd.Dir = filepath.Dir(media)
	// 控制台中文会乱码（工具的说明里写了），所以结果一律从**文件**读；这里只收 stdout 供报错。
	//
	// 【2026-10-10】`ErrWaitDelay` 必须当成"跑成了"来看待（前提：结果文件读得回来）：
	// 工具链是 `asr.cmd`(cmd.exe) → `python.exe`(可能再拉起解码器)，`sysexec.Hide` 给 Wait 加了上限
	// ⇒ 进程已退出、而孙进程还握着 stdout 管道时，Go 返回的正是这个哨兵错误。
	// 工具的契约本来就是**先写 `<media>.asr.txt` 再退出**，所以"文件写出来了"才是判据；
	// 把它当失败会让整条语音核对（逐镜跑）报一堆假失败 —— 真机咬到过更极端的形态：
	// 孙进程握着管道不放，`CombinedOutput` 一直等 ⇒ `go test` 卡到 600s / 900s（栈在 io.copyBuffer）。
	//
	// 【2026-10-10 二修：**连子孙一起收**】原先这里直接 `CombinedOutput`（它内部 Start+Wait），
	// 而 `exec.CommandContext` 只杀**直接子进程**（cmd.exe）—— python 会活下来继续占着工作目录与 CPU。
	// 真机表现：质检某镜超时后残留一个 python；集成测试的临时目录清理当场失败
	// （`clips: The process cannot access the file because it is being used by another process`）。
	// 现在自己 Start，并把进程放进 **kill-on-close 的作业对象**：`defer job.Close()` 一关，
	// 整棵树（cmd.exe + python + 它拉起的解码器）被内核清算。
	var buf strings.Builder
	cmd.Stdout, cmd.Stderr = &buf, &buf
	job, jerr := startInJob(cmd)
	if jerr != nil {
		return nil, fmt.Errorf("ASR 工具起不动(%v)", jerr)
	}
	defer job.Close() // 关作业 ⇒ 整棵树一起收（含孙进程）
	err = cmd.Wait()
	raw := buf.String()
	r, perr := Parse(out)
	if perr != nil {
		if err != nil {
			return nil, fmt.Errorf("ASR 调用失败(%v):%s", err, tail(string(raw), 400))
		}
		return nil, fmt.Errorf("ASR 结果读不回来(%v):%s", perr, tail(string(raw), 400))
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		// 结果文件在、但进程退出码非零 ⇒ 结果可信度不足，如实报错（不静默采信半截输出）。
		return nil, fmt.Errorf("ASR 调用失败(%v):%s", err, tail(string(raw), 400))
	}
	r.Tool = tool
	return r, nil
}

// tail 取文本尾部（报错信息里贴太多没意义）。
func tail(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[len(r)-n:])
}
