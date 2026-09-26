package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// 色彩口径(乙3)。全链路此前没有一处读/写色彩元数据:逐镜规范化(scale/pad/fps + 重编码)
// 会把源片 VUI 里的色彩标记丢一半,播放器(浏览器、剪辑软件、微信)只能按"缺省 bt709"猜
// —— 同一部片在不同播放器上偏色不同,而日志里一个字都没有。
//
// 这里做三件事,一件都不多做:
//  1. **量**:逐镜 ffprobe 出 color_space / color_transfer / color_primaries / color_range;
//  2. **照实说**:未标记、标记互不一致都播报出来(不静默兜底);
//  3. **只写不转**:把源片**自己**的标记写进输出。绝不做色彩空间转换,更不会把已标记为
//     bt601/smpte170m 的源片强改 bt709 —— 那会让整片偏色(标记一变,播放器换一套矩阵
//     去解码同样的像素),这正是本任务明令禁止的做法。

// colorSpec 一条视频流的色彩元数据(字段名与 ffprobe 的 JSON 键一致)。
//
// 空串 = 该字段没有标记(ffprobe 报 unknown/reserved/unspecified 的一律归一成空串,
// 见 normColorValue)。注意"部分标记"是真实存在的状态:ffmpeg 用 -colorspace 只写
// 矩阵、不写原色/传输特性,成片里就是 color_space 有值而 color_primaries=unknown。
type colorSpec struct {
	Range     string `json:"color_range"`
	Space     string `json:"color_space"`
	Transfer  string `json:"color_transfer"`
	Primaries string `json:"color_primaries"`
}

// bt709Spec 未标记源片按 bt709 写入的口径(全链路既有的隐含约定,现在写成显式标记)。
var bt709Spec = colorSpec{Range: "tv", Space: "bt709", Transfer: "bt709", Primaries: "bt709"}

// marked 是否带色彩标记:**矩阵或原色有有效值**即算已标记。
//
// 为什么不要求三项齐全:真实素材大量是"只有矩阵"或"只有原色/传输特性"的半标记态,
// 把它们判成"未标记"会触发 bt709 覆盖 —— 恰恰是要避免的强改。
// 为什么只标了 color_range 不算:范围(tv/pc)不是矩阵,单独一个范围值不足以
// 判定这段像素按哪套系数编码。
func (c colorSpec) marked() bool { return c.Space != "" || c.Primaries != "" }

// key 一致性比较键:三项矩阵标记逐字比较(缺项记 "-")。
// 刻意严格 —— 半标记的 bt709 与齐全的 bt709 在播放器里未必同解,报出来让人自己看。
func (c colorSpec) key() string {
	return fmt.Sprintf("%s|%s|%s", orDash(c.Space), orDash(c.Primaries), orDash(c.Transfer))
}

// label 播报用的可读名:以矩阵为主,原色/传输与矩阵不同才附在后面
// (如 "bt709(传输 smpte170m)")。范围不进播报 —— 它不是矩阵,写进人话里只会干扰。
func (c colorSpec) label() string {
	main := c.Space
	if main == "" {
		main = c.Primaries
	}
	if main == "" {
		return "未标记"
	}
	var extra []string
	if c.Primaries != "" && c.Primaries != main {
		extra = append(extra, "原色 "+c.Primaries)
	}
	if c.Transfer != "" && c.Transfer != main {
		extra = append(extra, "传输 "+c.Transfer)
	}
	if len(extra) == 0 {
		return main
	}
	return main + "(" + strings.Join(extra, ",") + ")"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// colorDecision 全片色彩处理决策(纯函数产物,可单测)。
type colorDecision struct {
	unmarked   []int     // 未标记的镜号(1-based)
	variants   []string  // 已标记镜之间不一致时,各口径的首现镜号 + 标记
	uniform    colorSpec // 全片可统一写入的标记(见 masterArgs)
	hasUniform bool      //
	notes      []string  // 如实播报
}

// planColor 依各镜的探测结果给出处理决策。infos 与镜头同序。
//
// 三种分支(与设计口径一一对应):
//
//	① 全部已标记且一致 → 原样保留并统一透传(逐镜写回同一份标记,终混也写这一份);
//	② 存在未标记的镜 → 那几镜按 bt709 写入,并**如实播报**("源片未标记色彩,已按 bt709 处理");
//	③ 已标记的镜之间不一致 → 播报并列出各镜差异;不做统一改写(各镜留自己的标记)。
func planColor(infos []colorSpec) colorDecision {
	var d colorDecision
	firstByKey := map[string]int{}
	var keyOrder []string
	for i, c := range infos {
		if !c.marked() {
			d.unmarked = append(d.unmarked, i+1)
			continue
		}
		k := c.key()
		if _, ok := firstByKey[k]; ok {
			continue
		}
		firstByKey[k] = i
		keyOrder = append(keyOrder, k)
		d.variants = append(d.variants, fmt.Sprintf("第 %d 镜 %s", i+1, c.label()))
	}

	if len(d.unmarked) > 0 {
		d.notes = append(d.notes, fmt.Sprintf(
			"源片未标记色彩,已按 bt709 处理(第 %s 镜)", joinInts(d.unmarked)))
	}
	switch {
	case len(keyOrder) > 1:
		d.notes = append(d.notes, fmt.Sprintf(
			"⚠ 各镜色彩标记不一致(%s):按各镜原标记原样保留、不做统一改写(强改 bt709 会让本标注为其它矩阵的镜头整体偏色)",
			strings.Join(d.variants, "、")))
	case len(keyOrder) == 1 && len(d.unmarked) == 0:
		// ① 全片一个口径:透传这一份(带上它自己的 range,不臆造)
		d.uniform, d.hasUniform = infos[firstByKey[keyOrder[0]]], true
	case len(keyOrder) == 0:
		// 全片都没标记:统一按 bt709 写
		d.uniform, d.hasUniform = bt709Spec, true
	}
	return d
}

// masterArgs 中间轨重编码与终混重编码要写入的色彩标记。
//
// 只有全片口径统一(全标记且一致,或全未标记 → bt709)时才写:成品的色标是**整条流只
// 有一份**,混合口径时写任何一份都是替某一镜撒谎;那种情况留给逐镜标记 + 已发出的播报。
func (d colorDecision) masterArgs() []string {
	if !d.hasUniform {
		return nil
	}
	return specColorArgs(d.uniform)
}

// shotColorArgs 该镜规范化输出侧要写入的色彩标记。
// 已标记 → 写它自己的;未标记 → 按 bt709 写。
func shotColorArgs(c colorSpec) []string {
	if !c.marked() {
		return specColorArgs(bt709Spec)
	}
	return specColorArgs(c)
}

// specColorArgs 把一份色彩标记翻成 ffmpeg 参数。
//
// 为什么用 -x264-params 而不是 -color_primaries/-color_trc/-colorspace:后者在
// ffmpeg 9.0.1 + libx264 上**不会**把传输特性与原色落进成品的 VUI/colr
// (2026-09-26 实测:-colorspace 只落了 color_space,color_transfer/color_primaries
// 仍是 unknown;同一条命令换成 -x264-params colorprim/transfer/colormatrix 三个字段
// 全部落盘,ffprobe 复读一致)。只写 color_space 的半标记等于让播放器继续猜。
//
// 每个字段单独判断:源片只标了矩阵就只写矩阵 —— 补齐没标的字段是"替它表态",
// 可能反而与像素真实口径不符。认不出的取值一律不写(宁可少标,不可错标)。
func specColorArgs(c colorSpec) []string {
	var params []string
	if v, ok := x264Matrix[strings.ToLower(c.Space)]; ok {
		params = append(params, "colormatrix="+v)
	}
	if v, ok := x264Transfer[strings.ToLower(c.Transfer)]; ok {
		params = append(params, "transfer="+v)
	}
	if v, ok := x264Primaries[strings.ToLower(c.Primaries)]; ok {
		params = append(params, "colorprim="+v)
	}
	var args []string
	if len(params) > 0 {
		args = append(args, "-x264-params", strings.Join(params, ":"))
	}
	if v, ok := colorRangeNames[strings.ToLower(c.Range)]; ok {
		args = append(args, "-color_range", v)
	}
	return args
}

// 三张取值表:key 是 ffprobe 报的名字,value 是 x264 认的名字。
// 两边绝大多数同名,保留映射表是为了"只放行确定能落盘的取值"(见 specColorArgs)。
var x264Matrix = map[string]string{
	"bt709": "bt709", "fcc": "fcc", "bt470bg": "bt470bg", "smpte170m": "smpte170m",
	"smpte240m": "smpte240m", "bt2020nc": "bt2020nc", "bt2020c": "bt2020c",
}

var x264Primaries = map[string]string{
	"bt709": "bt709", "bt470m": "bt470m", "bt470bg": "bt470bg", "smpte170m": "smpte170m",
	"smpte240m": "smpte240m", "film": "film", "bt2020": "bt2020",
	"smpte428": "smpte428", "smpte431": "smpte431", "smpte432": "smpte432",
}

var x264Transfer = map[string]string{
	"bt709": "bt709", "gamma22": "gamma22", "gamma28": "gamma28", "smpte170m": "smpte170m",
	"smpte240m": "smpte240m", "linear": "linear", "log100": "log100", "log316": "log316",
	"iec61966-2-4": "iec61966-2-4", "iec61966-2-1": "iec61966-2-1",
	"bt2020-10": "bt2020-10", "bt2020-12": "bt2020-12", "smpte2084": "smpte2084",
	"smpte428": "smpte428", "arib-std-b67": "arib-std-b67",
}

var colorRangeNames = map[string]string{
	"tv": "tv", "limited": "tv", "mpeg": "tv",
	"pc": "pc", "full": "pc", "jpeg": "pc",
}

// probeColors 逐镜探测源片色彩元数据(与镜头同序)。探测失败/无 ffprobe 的镜返回零值
// (= 未标记),由 planColor 走 bt709 分支 —— 量不到就按缺省口径处理,并会被播报出来。
func probeColors(ctx context.Context, f *ff.FF, clips []string) []colorSpec {
	out := make([]colorSpec, len(clips))
	for i, clip := range clips {
		if ctx.Err() != nil {
			return out
		}
		c, err := probeClipColor(ctx, f, clip)
		if err != nil {
			continue
		}
		out[i] = c
	}
	return out
}

// probeClipColor 读一条视频流的色彩元数据。
//
// 单独走一次 ffprobe 而不是扩 ff.Probe:ff.Probe 是**全应用共用**的摘要探测
// (画幅/帧率/音轨),要不要把色彩字段塞进它的结构是另一处的决定;这里只多读四个字段,
// mp4 上是毫秒级。
func probeClipColor(ctx context.Context, f *ff.FF, path string) (colorSpec, error) {
	if f == nil || f.Ffprobe == "" {
		return colorSpec{}, fmt.Errorf("未配置 ffprobe")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := sysexec.Hide(exec.CommandContext(ctx, f.Ffprobe,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=color_range,color_space,color_transfer,color_primaries",
		"-of", "json", path)).Output()
	if err != nil {
		return colorSpec{}, fmt.Errorf("ffprobe 色彩探测失败: %w", err)
	}
	var po struct {
		Streams []colorSpec `json:"streams"`
	}
	if err := json.Unmarshal(raw, &po); err != nil {
		return colorSpec{}, err
	}
	if len(po.Streams) == 0 {
		return colorSpec{}, fmt.Errorf("ffprobe 没有返回视频流")
	}
	return normColorSpec(po.Streams[0]), nil
}

// normColorSpec 把 ffprobe 的"未标记"取值统一成空串。
// ffprobe 在 JSON 输出里通常直接省略 unknown 字段,但不同版本/不同封装会写出
// "unknown"/"unspecified"/"reserved"/"N/A",这些都等于"没标"。
func normColorSpec(c colorSpec) colorSpec {
	return colorSpec{
		Range:     normColorValue(c.Range),
		Space:     normColorValue(c.Space),
		Transfer:  normColorValue(c.Transfer),
		Primaries: normColorValue(c.Primaries),
	}
}

func normColorValue(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "unknown", "unspecified", "reserved", "n/a", "none":
		return ""
	}
	return s
}
