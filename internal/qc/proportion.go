// 定妆照比例自检:出图后问视觉模型"三庭/眼距/头身比有没有偏离技能侧的比例锚点"。
//
// 为什么需要它(技能侧《平台侧整改清单》§5.1,2026-09-26):用户报
// 「《求你们骂我》主角和女角的人物渲染比例不是黄金比例、太丑了」。技能侧定位到的病根是
// 旧口径只有风格词、**没有任何比例**,模型各按各的审美发挥(脸被拉长/三庭不匀/眼距失衡/
// 头身比失调)。技能侧补上 `HUMAN_PROPORTION_CLAUSE`(人体卡自动带)之后,给平台侧的
// 下一句原话是:「定妆照出来后按你们已有的脸部关键点测量核对三庭与眼距,偏离阈值就报出来
// —— "丑"以前是主观的,有锚点后它就是可测的」。
//
// 三件事必须先说清楚,否则这一步会退化成"看起来验过了":
//
//	① **阈值口径全部来自技能侧**,平台只做"测与报" —— 下面 proportionSpecs 的八条逐条
//	   对应技能侧比例句(眼线中线/眼距＝一眼宽/鼻长 ⅓/嘴 ⅕/下巴宽 ⅔ 颧骨/颈细长/
//	   肩宽＝两个头宽/身高七个半头),平台**不另定**一套比例标准(不引入"黄金分割"之类的
//	   第二条尺子 —— 两把尺子量同一张脸,人只会更糊涂)。
//	② **能测才测**:定妆照是半身幅(upper-body framing),全身比例(七个半头、肩宽、腰腿)
//	   往往根本不在画面里。模型对看不见的项必须回答"不可见",平台据实记录 —— 不许让模型
//	   凭常识补一个数(那就是编)。
//	③ **定为 warn,不拦产线**:视觉模型判比例本身有误差,这一步只出可读告警 + 建议重出该卡,
//	   绝不阻断资产生成、也不自动重渲(与画面文字检测同一处置原则)。
//
// 复用既有件,不另起一套:
//   - 视觉调用:同一个 llm.Client.ChatVision 与同一份设置页 VLM 配置(与 VLMJudge /
//     IdentityJudge / DetectOnScreenText 共用一个客户端);
//   - 压图口径:与 identity.go 的多视角身份校验一致(原图直接 base64,不缩放)—— 比例是
//     细粒度判断,缩放会把要量的东西本身削掉;而这里**每张定妆照只调一次**,不是逐帧,
//     省额度不必要。文本级检测那次要一次送多帧,才有"压图"的必要(见 textdetect.go)。
package qc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
)

// proportionSpec 比例锚点里的一项(定义来自技能侧比例句,平台不另立标准)。
type proportionSpec struct {
	Key      string // 模型回答里引用的键(平台侧固定,便于把"哪一项"点名出来)
	Label    string // 中文名(告警文案用)
	Expected string // 期望口径(逐条对应技能侧比例句)
}

// proportionSpecs 技能侧比例锚点的八个可测项。
//
// 顺序与技能侧比例句一致(眼线 → 眼距 → 鼻 → 嘴 → 下巴 → 颈 → 肩 → 头身比),
// 提示词里也按这个顺序给模型,免得它自己重新发明一套检查表。
var proportionSpecs = []proportionSpec{
	{"eye_line", "眼线位置", "the eye line sits at the vertical middle of the head(眼线在头部垂直中线)"},
	{"eye_spacing", "两眼间距", "the eyes are one eye-width apart(两眼间距＝一个眼宽)"},
	{"nose_length", "鼻长", "the nose is about one third of the face length(鼻长约脸长三分之一)"},
	{"mouth_length", "嘴长", "the mouth is about one fifth of the face length(嘴约脸长五分之一)"},
	{"chin_width", "下巴宽", "the chin is about two thirds as wide as the cheekbones(下巴宽约颧骨的三分之二)"},
	{"neck", "颈", "the neck is slim and long(颈细长)"},
	{"shoulder_width", "肩宽", "the shoulders are about two head-widths wide(肩宽约两个头宽)"},
	{"body_height", "头身比", "the figure is seven and a half heads tall with a slim waist and long legs(身高七个半头、腰细腿长)"},
	// 朝向是**构图项**,不是比例项 —— 但它和比例项放在同一次自检里,因为两者的判据
	// 是同一张图、同一次视觉调用(多分一次调用只为问一句"是不是正脸"不划算)。
	//
	// 为什么它必须可测(2026-09-27 真机实测):定妆照是**全片身份的权威参考图**,
	// 而提示词里那句 `shot STRAIGHT-ON from the front…` 压不住模型 —— 同一段提示词、
	// 只换采样种子,4 个样本里 3 个正面、1 个四分之三侧(见 docs/定妆照朝向-实测与处置.md)。
	// 更麻烦的是定妆照的种子是**按名字锚定**的(nameSeed):同一个角色名每次重跑都出
	// 同一张图,所以一旦那次采样偏成侧脸,这个角色就会**一直是侧脸**,直到种子或提示词变。
	// 只靠措辞是治不了的(前置强调做过 A/B:结果一模一样),得靠"测出来 + 换种子重出"。
	{"face_direction", "朝向", "the portrait is shot STRAIGHT-ON from the front: the face looks directly into the " +
		"camera, both eyes visible at the same size and level, nose and chin pointing at the camera(正面胸像)"},
}

// KeyFaceDirection 朝向项的键(调用方据此从报告里取这一项)。
const KeyFaceDirection = "face_direction"

// proportionSpecOf 按键取项(取不到返回 false)。
func proportionSpecOf(key string) (proportionSpec, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, s := range proportionSpecs {
		if s.Key == k {
			return s, true
		}
	}
	return proportionSpec{}, false
}

// 偏离档位。**阈值就是 clear**:slight 记进结论但不发告警 —— 视觉模型判比例有误差,
// 轻微偏差也发告警会把信噪比拉垮(与画面文字检测"单镜命中不升级为集级告警"同一取舍)。
const (
	DeviationNone   = "none"   // 与技能侧口径一致
	DeviationSlight = "slight" // 有偏差但轻微(低于阈值)
	DeviationClear  = "clear"  // 明显偏离(阈值以上 → 告警 + 建议重出该卡)
)

// ProportionItem 单项比例判定。
type ProportionItem struct {
	Key       string `json:"key"`
	Item      string `json:"item"`      // 中文名(眼线位置/两眼间距/…)
	Expected  string `json:"expected"`  // 期望口径(技能侧比例句)
	Observed  string `json:"observed"`  // 模型量到的(文字)
	Deviation string `json:"deviation"` // none / slight / clear
}

// ProportionReport 一张定妆照的比例自检结论。
//
// Checked / Skipped / Error 三个字段刻意分开(与 TextDetectReport 同一理由):
// 它们回答"到底看没看",而 Clear 只回答"看到了什么"。只看 Clear 会把**压根没做**读成
// "比例没问题",那正是本条要防的"没验过却看起来像验过了"。
type ProportionReport struct {
	Checked bool   `json:"checked"` // 是否真的量了
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
	// AnchorInPrompt 这张卡的提示词里是否带技能侧比例锚点句(仅作留痕:没有锚点时,
	// 说明卡本身缺了比例约定 —— 技能侧 validate_plan 会把"缺锚点"报给作者)。
	AnchorInPrompt bool             `json:"anchor_in_prompt,omitempty"`
	Measured       int              `json:"measured"` // 画面里**真的看得见**因而被量过的项数
	Clear          []ProportionItem `json:"clear,omitempty"`
	Slight         []ProportionItem `json:"slight,omitempty"`
	Invisible      []string         `json:"invisible,omitempty"` // 看不见、因而没量的项(中文名)
	Note           string           `json:"note,omitempty"`      // 模型给的中文总体描述
}

// 未配置能力时的如实降级文案(技能侧 §5.1 原话:"如实降级(未做比例自检:无可用能力)")。
const (
	// ProportionSkipNoCapability 没有可用的视觉能力(设置页未填 VLM)。
	ProportionSkipNoCapability = "未做比例自检:无可用能力(未配置视觉模型;在设置页填好 VLM 后会自动开检)"
	// ProportionSkipDisabled 开关关闭:调用方**连条目都不写**(关掉的功能不该留痕,
	// 否则"关掉"看起来就像"每张卡都量过且合格")。
	ProportionSkipDisabled = ""
)

// PortraitProportionSkipped 构造"没做"的结论(开关关闭 / 无可用能力)。
func PortraitProportionSkipped(reason string) *ProportionReport {
	return &ProportionReport{Checked: false, Skipped: reason}
}

// PortraitProportionFailed 构造"量了但失败"的结论(读图/调用/解析失败)。
func PortraitProportionFailed(err error) *ProportionReport {
	msg := "未知错误"
	if err != nil {
		msg = err.Error()
	}
	return &ProportionReport{Checked: false, Error: msg}
}

// DetectPortraitProportion 对一张定妆照做比例自检。
//
// imagePath 是定妆照本体(upper-body framing);anchorInPrompt 来自装配点
// (pipeline.HumanProportionClausePresent),只作留痕,不参与判据 —— 判据始终是技能侧
// 的那八条口径,与"卡里带没带锚点"无关(缺锚点是技能侧 validate_plan 的事)。
//
// 返回的 error 一律由调用方**如实写进事件流**(见 pipeline 的接线):读不到图、模型没回答、
// 输出不可解析,都等于"没验过",不许当成"比例没问题"。
func DetectPortraitProportion(ctx context.Context, client VisionClient, imagePath string, anchorInPrompt bool) (*ProportionReport, error) {
	if client == nil {
		return nil, fmt.Errorf("未配置视觉模型")
	}
	if strings.TrimSpace(imagePath) == "" {
		return nil, fmt.Errorf("缺少定妆照路径")
	}
	raw, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, fmt.Errorf("读取定妆照失败:%w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("定妆照为空:%s", imagePath)
	}
	// 与 identity.go 的多视角校验同一口径:原图直接 base64(比例是细粒度判断,缩放会把
	// 要量的东西本身削掉)。mime 按扩展名给,认不出按 png(定妆照本身就是 Krea-2 出的 png)。
	mime := "image/png"
	switch strings.ToLower(filepath.Ext(imagePath)) {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".webp":
		mime = "image/webp"
	}
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
	return judgePortraitProportion(ctx, client, dataURL, anchorInPrompt)
}

// judgePortraitProportion 把已编码的图交给视觉模型逐项判定(便于单测用假客户端穷举)。
func judgePortraitProportion(ctx context.Context, client VisionClient, image string, anchorInPrompt bool) (*ProportionReport, error) {
	if client == nil {
		return nil, fmt.Errorf("未配置视觉模型")
	}
	out, err := client.ChatVision(ctx, proportionSystem, []llm.VisionContent{{
		Role:   "user",
		Text:   proportionUserPrompt(),
		Images: []string{image},
	}}, true)
	if err != nil {
		return nil, err
	}
	items, note, err := parsePortraitProportion(out)
	if err != nil {
		return nil, err
	}
	return buildProportionReport(items, note, anchorInPrompt), nil
}

// proportionUserPrompt 该项检查表(键 + 期望口径逐条给全)。
//
// 为什么要逐条列出:模型的默认"美"是它自己的先验(大眼小脸网红脸),不把技能侧的口径
// 摆在它面前,它就会拿自己的审美当标准判 —— 那就成了"平台另定一套比例标准"。
func proportionUserPrompt() string {
	var b strings.Builder
	b.WriteString("This ONE image is a character reference sheet (a portrait of one character). " +
		"For EVERY item below, decide whether the picture's proportions match the stated production ratio, " +
		"and report your answer for that item. Items:\n")
	for _, s := range proportionSpecs {
		b.WriteString("- " + s.Key + " (" + s.Label + "): " + s.Expected + "\n")
	}
	b.WriteString("Rules: judge ONLY what is actually visible in this image. If an item cannot be seen or measured " +
		"at this framing (for example the whole figure is not in frame), answer \"visible\":false and do not guess. " +
		"The stated ratios above are the production contract — do not substitute your own taste or a generic beauty standard. " +
		"Return the JSON.")
	return b.String()
}

// proportionOut 模型原始输出(只取需要的字段)。
type proportionOut struct {
	Items []struct {
		Key       string `json:"key"`
		Visible   bool   `json:"visible"`
		Deviation string `json:"deviation"`
		Observed  string `json:"observed"`
	} `json:"items"`
	Note string `json:"note"`
}

// parsePortraitProportion 解析模型输出(纯函数,可穷举)。
//
// 三条纪律,与 identity.go「以逐帧分数为准重算 pass,不信模型自己的汇总字段」同源:
//  1. 只认**检查表里的键**:模型自造的项(它没被要求量、也就没有期望口径)直接丢弃 ——
//     把它写进告警会让人去改一个平台没有约定过的东西;
//  2. 逐项结论才算数:模型 note 里的"看着还行"不参与判定;
//  3. 一项都没回答 / 键全对不上 = **失败**(未判定),不许当成"比例没问题"。
func parsePortraitProportion(raw string) ([]ProportionItem, string, error) {
	s := stripJSONFence(raw)
	var o proportionOut
	if err := json.Unmarshal([]byte(s), &o); err != nil {
		return nil, "", fmt.Errorf("比例自检输出解析失败: %w", err)
	}
	if len(o.Items) == 0 {
		return nil, "", fmt.Errorf("模型没有逐项回答(未判定,不当作通过)")
	}
	out := make([]ProportionItem, 0, len(o.Items))
	for _, it := range o.Items {
		spec, ok := proportionSpecOf(it.Key)
		if !ok {
			continue // 自造的项:没有技能侧口径可比,丢弃
		}
		item := ProportionItem{
			Key: spec.Key, Item: spec.Label, Expected: spec.Expected,
			Observed: strings.TrimSpace(it.Observed),
		}
		if !it.Visible {
			// 不可见 = 没量过:档位留空(空档位 = 未量,见 buildProportionReport 的分流),
			// 也不留模型可能顺手编出来的读数。它既不是"合格"也不是"不合格"。
			item.Deviation = ""
			item.Observed = ""
			out = append(out, item)
			continue
		}
		item.Deviation = normalizeProportionDeviation(it.Deviation)
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, "", fmt.Errorf("模型回答的项与比例检查表对不上(未判定,不当作通过)")
	}
	return out, strings.TrimSpace(o.Note), nil
}

// normalizeProportionDeviation 把模型的偏离档位归一到三档。
//
// 认不出的写法(拼错、空串、自造词)一律按 **slight** 处理:它既不产生假告警(clear 才告警),
// 也不会被当成"没问题"(none)—— 这两头都是"看着像验过了"的典型形态。
func normalizeProportionDeviation(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "clear", "severe", "obvious", "marked", "major":
		return DeviationClear
	case "slight", "minor", "mild", "small":
		return DeviationSlight
	case "none", "no", "ok", "normal", "match", "matches":
		return DeviationNone
	default:
		return DeviationSlight
	}
}

// buildProportionReport 由逐项结论折出总报告(纯函数,可穷举)。
//
// 分流:visible=false 的项进 Invisible(没量过)、slight 进 Slight、clear 进 Clear;
// Measured = 真的量过的项数(= 总项数 - 不可见项数)。Measured==0 时报告是"看过但没量到",
// 由调用方静默处理 —— 它既不是"合格"也不是"没验过"。
func buildProportionReport(items []ProportionItem, note string, anchor bool) *ProportionReport {
	r := &ProportionReport{Checked: true, AnchorInPrompt: anchor, Note: note}
	for _, it := range items {
		switch it.Deviation {
		case "":
			r.Invisible = append(r.Invisible, it.Item)
		case DeviationClear:
			r.Measured++
			r.Clear = append(r.Clear, it)
		case DeviationSlight:
			r.Measured++
			r.Slight = append(r.Slight, it)
		default:
			r.Measured++
		}
	}
	return r
}

// FacingOf 取报告里**朝向项**的偏离档位。
//
// 返回 "" 表示这一项没被量到(可见=false 或整份报告没做/失败)—— 与 "none" 严格区分:
// 前者是"不知道",后者是"量了,是正面"。把两者混成一个空值,就会让"没验过"看起来像"合格"。
func FacingOf(r *ProportionReport) string {
	if r == nil || !r.Checked {
		return ""
	}
	for _, it := range r.Clear {
		if it.Key == KeyFaceDirection {
			return DeviationClear
		}
	}
	for _, it := range r.Slight {
		if it.Key == KeyFaceDirection {
			return DeviationSlight
		}
	}
	for _, name := range r.Invisible {
		if name == "朝向" {
			return ""
		}
	}
	// 既不在 clear/slight 里、也没被记成"看不见" → 它是 none(量了且合格)
	return DeviationNone
}

// FacingNeedsRegen 朝向是否明显偏离到"值得换种子重出"。
//
// 只有 **clear** 才算:这条判据会花掉一次真实出图(约 20-30 秒 GPU),而视觉模型判朝向
// 也有误差 —— 把 slight 也算进来,等于"模型犹豫一下就重渲一遍"。slight 仍然走原有的
// 告警通道(用户自己决定要不要重出那张卡)。
func FacingNeedsRegen(r *ProportionReport) bool {
	return FacingOf(r) == DeviationClear
}

// ProportionWarnText 告警文案(空串 = 不告警,调用方据此决定是否发出事件)。
//
// **只有"明显偏离"(clear)才告警**,并且必须点出是**哪一项**、期望多少、量到多少 ——
// 只说"比例不对"没法定位,而这条告警的用途正是让人决定要不要重出这张卡。
func ProportionWarnText(r *ProportionReport) string {
	if r == nil || !r.Checked || len(r.Clear) == 0 {
		return ""
	}
	parts := make([]string, 0, len(r.Clear))
	for _, it := range r.Clear {
		obs := it.Observed
		if obs == "" {
			obs = "模型未给出量到的值"
		}
		parts = append(parts, fmt.Sprintf("%s(期望:%s;实测:%s)", it.Item, it.Expected, obs))
	}
	return fmt.Sprintf("比例自检发现 %d 项明显偏离技能侧比例锚点 —— %s;建议在「资产定妆」里对该卡点 ✨ 重新生成(这是告警不是判负:视觉模型量比例有误差,重出与否由你定)",
		len(r.Clear), strings.Join(parts, ";"))
}

// proportionSystem 系统提示词(英文,与本包既有的 vlmSystem / identitySystem / textDetectSystem 同一口径)。
//
// 五条纪律写死在提示词里:①只看画面里真的看得见的东西;②看不见就说看不见,不许猜;
// ③口径用给定的生产比例,不用自己的审美;④"偏离"要分档(none/slight/clear);
// ⑤逐项回答,不许只给一句总体评价。
const proportionSystem = `You are a strict proportion checker for a 2D/3D animated character reference sheet.
You are given ONE reference sheet image of a single character (usually an upper-body portrait).
You are also given the production's stated anatomical ratios (its proportion contract). For EVERY listed item,
compare what the image actually shows with the stated ratio, and report that item.
Rules:
- Judge ONLY what is visibly rendered in this image. Never infer from the character's description, and never invent measurements you cannot see.
- If an item is not visible or not measurable at this framing (a half-body portrait cannot show total body height, leg length or full shoulder width), answer "visible": false for that item and leave your reading empty. A guess is worse than "not visible".
- Use the STATED ratios as the standard. Do not substitute your own taste, a generic beauty ideal, or any other proportion system.
- One item is about COMPOSITION rather than anatomy (face_direction): for it, judge whether the head is shot straight-on and frontal as stated. Mark it "clear" when the head is visibly turned (three-quarter or profile: the nose points off to one side and only one eye is fully visible), and "none" when both eyes are visible at the same size and the nose points at the camera. "visible" is true whenever the face can be seen at all.
- "deviation" is one of: "none" (matches the stated ratio), "slight" (a small deviation), "clear" (an obvious deviation a viewer would notice).
- Answer EVERY item in the list, individually.
Return STRICT JSON ONLY:
{"items":[{"key":"eye_line","visible":true/false,"deviation":"none|slight|clear","observed":"中文:你在这张图上实际量到的(如 眼线约在头部自上而下的 42%;眼睛间距约 1.8 个眼宽);看不见则留空"}],"note":"中文一句话总体结论"}
"observed" is for a human: write the actual reading you took from THIS image, in Chinese, short.`
