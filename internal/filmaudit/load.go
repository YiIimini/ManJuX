package filmaudit

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/project"
)

// load.go 盘上读取：把一集的**已有文件**组装成体检输入。
//
// 刻意只读盘、不调模型、不重算创作内容：体检的价值就在于"你打开成片之前，先把盘上能算的
// 都算一遍"。

// ProbeFn 量一个媒体文件的时长（秒）。CLI 传 ffprobe 实现；传 nil 就跳过依赖时长的判断。
type ProbeFn func(path string) (float64, error)

// TrackProbeFn 量一个媒体文件**两条轨各自的**时长（秒）。返回 ok=false 表示量不到
// （调用方一律按"没验过"处理：跳过判据，绝不把"没量到"当成"合格"）。
//
// 【为什么还要第二个探针（2026-10-09 真机 P0）】ProbeFn 返回的是 format 时长 = 两条流里
// 最长的那条 —— 而"画面被截断、音轨完好"的成片正是靠这个口径藏起来的（真机第01集：
// 画面 23.7 秒 / 音轨 122.5 秒，format 报 122.5）。
type TrackProbeFn func(path string) (video, audio float64, ok bool)

// LoadEpisode 读一集目录（形如 `data/projects/<书>/<书>·第NN集·xxx/`）。
func LoadEpisode(epDir string, probe ProbeFn, tracks TrackProbeFn) (Input, error) {
	var in Input
	in.ID = filepath.Base(epDir)
	raw, err := os.ReadFile(filepath.Join(epDir, "plan", "plan.json"))
	if err != nil {
		return in, fmt.Errorf("读不到 plan/plan.json：%w", err)
	}
	var plan project.Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return in, fmt.Errorf("plan.json 解析失败：%w", err)
	}
	// **自愈前**先数一次"模型把画内台词标成了画外音"（纠正由下面 HealPlan 做，但这个数要报）。
	in.RawInFrameAsOffscreen = countInFrameMarkedOffscreen(&plan)
	// 体检要看的是**消费方看到的那一份**：与预检/渲染同一条读取边界（HealPlan）。
	project.HealPlan(&plan)
	in.Plan = &plan

	in.Durations = make([]float64, len(plan.Shots))
	in.ASRSpans = make([][]asr.Segment, len(plan.Shots))
	for i, sh := range plan.Shots {
		clip := filepath.Join(epDir, "clips", fmt.Sprintf("shot_%03d.mp4", sh.ID))
		if _, serr := os.Stat(clip); serr != nil {
			in.ClipMissing = append(in.ClipMissing, sh.ID)
			continue
		}
		if probe != nil {
			if d, perr := probe(clip); perr == nil && d > 0 {
				in.Durations[i] = d
			}
		}
		if res, aerr := asr.Parse(ASRSidecarPath(clip)); aerr == nil && res != nil {
			in.ASRSpans[i] = res.Segments
			if in.Durations[i] == 0 && res.Duration > 0 {
				in.Durations[i] = res.Duration
			}
		}
	}

	if b, cerr := os.ReadFile(filepath.Join(epDir, "output", "final.srt")); cerr == nil {
		in.Cues = ParseSRT(string(b))
	}
	if final := filepath.Join(epDir, "output", "final.mp4"); tracks != nil {
		if v, a, ok := tracks(final); ok {
			in.VideoDur, in.AudioDur = v, a
		}
	}
	if final := filepath.Join(epDir, "output", "final.mp4"); probe != nil {
		if _, serr := os.Stat(final); serr == nil {
			if d, perr := probe(final); perr == nil {
				in.FinalDuration = d
			}
		}
	}
	in.Transition, in.FadeSec = readAssembleParams(filepath.Join(epDir, "project.json"))
	return in, nil
}

// readAssembleParams 从 project.json 取转场参数（读不到就返回空，体检会跳过依赖它的结论）。
func readAssembleParams(projectJSON string) (string, float64) {
	b, err := os.ReadFile(projectJSON)
	if err != nil {
		return "", 0
	}
	var doc struct {
		Config struct {
			Params struct {
				Assemble struct {
					Transition string  `json:"transition"`
					FadeSec    float64 `json:"fade_sec"`
				} `json:"assemble"`
			} `json:"params"`
		} `json:"config"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", 0
	}
	return doc.Config.Params.Assemble.Transition, doc.Config.Params.Assemble.FadeSec
}

// srtTimeRe 匹配 `HH:MM:SS,mmm`（也容忍 `.` 作小数分隔符）。
var srtTimeRe = regexp.MustCompile(`(\d+):(\d+):(\d+)[,.](\d+)`)

// ParseSRT 读一份 SRT 的 cue 时间（文本只留原始内容，体检用不到分词）。
func ParseSRT(body string) []Cue {
	var out []Cue
	var pending *Cue
	for _, ln := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		ts := srtTimeRe.FindAllStringSubmatch(ln, -1)
		if len(ts) >= 2 && strings.Contains(ln, "-->") {
			c := Cue{Start: srtSeconds(ts[0]), End: srtSeconds(ts[1])}
			pending = &c
			continue
		}
		if pending != nil {
			if t := strings.TrimSpace(ln); t != "" {
				pending.Text = t
				out = append(out, *pending)
				pending = nil
			}
			continue
		}
	}
	return out
}

// srtSeconds 把 `HH:MM:SS,mmm` 的四个字段换算成秒。
//
// 【2026-10-09 修：这里原来算错过】第一版把毫秒位当"1 位小数"处理
// （`div=1000` 再按位数往下除），于是 `00:00:03,349` 被算成 **37.900**（3 + 349/10）——
// 体检当场把它当成"字幕挂了 15 秒"报出来。真机探针把原始字节与逐行匹配一起打出来才定位到。
// 正确算法只有一个：毫秒字段 / 10^位数。
func srtSeconds(m []string) float64 {
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	s, _ := strconv.Atoi(m[3])
	ms, _ := strconv.Atoi(m[4])
	div := math.Pow(10, float64(len(m[4])))
	return float64(h*3600+mi*60+s) + float64(ms)/div
}

// LevelEmoji 给播报加一眼能认的前缀。
func LevelEmoji(level string) string {
	switch level {
	case "fail":
		return "⛔"
	case "warn":
		return "⚠"
	default:
		return "·"
	}
}
