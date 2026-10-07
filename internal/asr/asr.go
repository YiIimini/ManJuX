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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	cmd := exec.CommandContext(ctx, tool, media, "--model", model, "--out", out)
	cmd.Dir = filepath.Dir(media)
	// 控制台中文会乱码（工具的说明里写了），所以结果一律从**文件**读；这里只收 stdout 供报错。
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ASR 调用失败(%v):%s", err, tail(string(raw), 400))
	}
	r, perr := Parse(out)
	if perr != nil {
		return nil, fmt.Errorf("ASR 结果读不回来(%v):%s", perr, tail(string(raw), 400))
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
