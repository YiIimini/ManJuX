package qc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 定妆照比例自检的单测(技能侧清单 §5.1)。
//
// 这一条链的价值全在判据上:比例正常要**静默**、明显偏离要**点名哪一项**、
// **没验过不许看起来像验过了**。真机测量(真定妆照 + 真视觉模型)属真机验证 ——
// 那需要真图与额度;这里用假客户端把判据钉死(与 textdetect_test.go 同一手法,
// 复用同一个 fakeVision)。

// propJSON 造一份"指定项偏离"的模型输出(未列出的项按不可见处理)。
func propJSON(items map[string][3]string, note string) string {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	first := true
	for _, spec := range proportionSpecs {
		v, ok := items[spec.Key]
		if !ok {
			continue
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		// v = {visible, deviation, observed}
		b.WriteString(`{"key":"` + spec.Key + `","visible":` + v[0] + `,"deviation":"` + v[1] + `","observed":"` + v[2] + `"}`)
	}
	b.WriteString(`],"note":"` + note + `"}`)
	return b.String()
}

// 三庭/眼距明显偏离 → 告警,且文案必须点名**哪一项**(只说"比例不对"没法定位)。
func TestProportionClearDeviationWarns(t *testing.T) {
	// 眼距失衡(1.8 个眼宽)+ 头身比失调(约六个头);脸被拉长体现在眼线位置偏低。
	out := propJSON(map[string][3]string{
		"eye_line":       {"true", DeviationClear, "眼线约在头部自上而下的 58%(脸被拉长)"},
		"eye_spacing":    {"true", DeviationClear, "两眼间距约 1.8 个眼宽"},
		"body_height":    {"true", DeviationClear, "约六个头高"},
		"nose_length":    {"true", DeviationNone, "鼻长约脸长的三分之一"},
		"shoulder_width": {"false", "", ""},
	}, "三庭不匀、眼距偏宽、头身比失调")
	fv := &fakeVision{outs: []string{out}}
	rep, err := judgePortraitProportion(context.Background(), fv, "data:image/png;base64,AAA", true)
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	if !rep.Checked || !rep.AnchorInPrompt {
		t.Errorf("真的量了,Checked 与锚点留痕都应为 true:%+v", rep)
	}
	if len(rep.Clear) != 3 {
		t.Fatalf("应有 3 项明显偏离,实际 %d:%+v", len(rep.Clear), rep.Clear)
	}
	if rep.Measured != 4 {
		t.Errorf("量过的项数 = %d,期望 4(5 项里 1 项不可见)", rep.Measured)
	}
	if len(rep.Invisible) != 1 || rep.Invisible[0] != "肩宽" {
		t.Errorf("看不见的项必须如实记下,实际 %v", rep.Invisible)
	}
	w := ProportionWarnText(rep)
	for _, want := range []string{
		"3 项明显偏离", "眼线位置", "两眼间距", "头身比",
		"期望:", "两眼间距＝一个眼宽", // 期望口径必须来自技能侧比例句
		"实测:两眼间距约 1.8 个眼宽",
		"重新生成", // 严重偏离 → 建议重出该卡
	} {
		if !strings.Contains(w, want) {
			t.Errorf("告警文案应含 %q,实际 %q", want, w)
		}
	}
	// 不可见的项不许出现在告警里(它根本没量过)
	if strings.Contains(w, "肩宽") {
		t.Errorf("看不见的项不该进告警:%q", w)
	}
}

// 比例正常 → **静默**:一项都不许报,免得每张卡都刷一条"没问题"。
func TestProportionNormalIsSilent(t *testing.T) {
	out := propJSON(map[string][3]string{
		"eye_line":    {"true", DeviationNone, "眼线约在头部中线"},
		"eye_spacing": {"true", DeviationNone, "约一个眼宽"},
		// 轻微偏差低于阈值:记进结论、不发告警
		"nose_length": {"true", DeviationSlight, "鼻长约脸长的 40%"},
	}, "整体符合比例约定")
	fv := &fakeVision{outs: []string{out}}
	rep, err := judgePortraitProportion(context.Background(), fv, "data:image/png;base64,AAA", true)
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	if len(rep.Clear) != 0 {
		t.Errorf("没有明显偏离,不该有告警项:%+v", rep.Clear)
	}
	if len(rep.Slight) != 1 {
		t.Errorf("轻微偏差应记进结论(以便复核),实际 %+v", rep.Slight)
	}
	if w := ProportionWarnText(rep); w != "" {
		t.Errorf("比例正常/轻微偏差都不该产生告警文案,实际 %q", w)
	}
}

// 半身幅看不见全身:模型答"不可见"时**不许**折成"合格"。
func TestProportionInvisibleItemsAreNotPasses(t *testing.T) {
	out := propJSON(map[string][3]string{
		"eye_line":       {"true", DeviationNone, "眼线在中线"},
		"shoulder_width": {"false", "", ""},
		"body_height":    {"false", "", ""},
	}, "半身幅,全身比例不可见")
	fv := &fakeVision{outs: []string{out}}
	rep, err := judgePortraitProportion(context.Background(), fv, "data:image/png;base64,AAA", false)
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	if rep.Measured != 1 {
		t.Errorf("只有 1 项真的量过,Measured = %d", rep.Measured)
	}
	if !strings.Contains(strings.Join(rep.Invisible, "、"), "肩宽") ||
		!strings.Contains(strings.Join(rep.Invisible, "、"), "头身比") {
		t.Errorf("看不见的项要如实列出来,实际 %v", rep.Invisible)
	}
	// 看不见的项即使带了读数也不许进告警/结论
	for _, it := range append(rep.Clear, rep.Slight...) {
		if it.Deviation == "" {
			t.Errorf("未量项不该出现在偏离清单里:%+v", it)
		}
	}
}

// 未判定不许伪装:模型没逐项回答 / 键全对不上 / 输出不是 JSON,都必须报错。
func TestProportionRefusesToGuess(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"没有 items", `{"note":"看着还行"}`, "没有逐项回答"},
		{"items 是空数组", `{"items":[],"note":"ok"}`, "没有逐项回答"},
		{"键全对不上(模型自造检查表)", `{"items":[{"key":"golden_ratio","visible":true,"deviation":"clear","observed":"不是黄金比例"}]}`, "与比例检查表对不上"},
		{"输出不是 JSON", `比例都挺好的`, "解析失败"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fv := &fakeVision{outs: []string{tc.out}}
			rep, err := judgePortraitProportion(context.Background(), fv, "data:image/png;base64,AAA", true)
			if err == nil {
				t.Fatalf("必须报错(未判定),却拿到了结论 %+v", rep)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误文案应含 %q,实际 %v", tc.want, err)
			}
		})
	}
	// 视觉调用本身失败 → 原样上抛(调用方如实写进事件流)
	fv := &fakeVision{errs: []error{errors.New("视觉模型 500")}}
	if _, err := judgePortraitProportion(context.Background(), fv, "data:image/png;base64,AAA", true); err == nil ||
		!strings.Contains(err.Error(), "500") {
		t.Errorf("调用失败必须如实上抛,实际 %v", err)
	}
}

// 自造的项丢弃(它没有技能侧口径可比),但**不许**因此把整份回答判失败。
func TestProportionDropsUnknownKeysButKeepsTheRest(t *testing.T) {
	out := `{"items":[{"key":"golden_ratio","visible":true,"deviation":"clear","observed":"不是黄金比例"},` +
		`{"key":"eye_spacing","visible":true,"deviation":"clear","observed":"约 1.7 个眼宽"}],"note":"ok"}`
	fv := &fakeVision{outs: []string{out}}
	rep, err := judgePortraitProportion(context.Background(), fv, "data:image/png;base64,AAA", true)
	if err != nil {
		t.Fatalf("不该整份失败: %v", err)
	}
	if len(rep.Clear) != 1 || rep.Clear[0].Item != "两眼间距" {
		t.Errorf("只该保留检查表里的项,实际 %+v", rep.Clear)
	}
	if !strings.Contains(rep.Clear[0].Expected, "一个眼宽") {
		t.Errorf("期望口径必须来自技能侧比例句,实际 %q", rep.Clear[0].Expected)
	}
	if w := ProportionWarnText(rep); strings.Contains(w, "黄金") {
		t.Errorf("自造的项不该出现在告警里(那不是平台的约定):%q", w)
	}
}

// 偏离档位的归一:认不出的写法按 slight(既不假告警,也不被当成"没问题")。
func TestNormalizeProportionDeviation(t *testing.T) {
	for in, want := range map[string]string{
		"clear": DeviationClear, "SEVERE": DeviationClear, "明显": DeviationSlight, // 中文档位认不出 → slight(见下)
		"slight": DeviationSlight, "minor": DeviationSlight,
		"none": DeviationNone, "OK": DeviationNone, "": DeviationSlight,
	} {
		if got := normalizeProportionDeviation(in); got != want {
			t.Errorf("normalizeProportionDeviation(%q) = %q,期望 %q", in, got, want)
		}
	}
}

// 未配置视觉模型 → **如实降级**,不假装通过。
func TestDetectPortraitProportionWithoutClientIsHonest(t *testing.T) {
	if _, err := DetectPortraitProportion(context.Background(), nil, "x.png", true); err == nil ||
		!strings.Contains(err.Error(), "未配置视觉模型") {
		t.Errorf("没有视觉模型时必须报错(未判定),实际 %v", err)
	}
	// 降级文案按技能侧的原话
	if !strings.Contains(ProportionSkipNoCapability, "未做比例自检:无可用能力") {
		t.Errorf("降级文案必须说清「未做」,实际 %q", ProportionSkipNoCapability)
	}
	// 开关关闭 = 连条目都不写
	if ProportionSkipDisabled != "" {
		t.Errorf("开关关闭必须是空文案(不留痕),实际 %q", ProportionSkipDisabled)
	}
	// 跳过 / 失败 / 合格三种形态必须可区分(报告是给人读的)
	sk := PortraitProportionSkipped(ProportionSkipNoCapability)
	if sk.Checked || sk.Skipped == "" || len(sk.Clear) != 0 {
		t.Errorf("跳过必须 Checked=false 且带原因:%+v", sk)
	}
	fa := PortraitProportionFailed(errors.New("读取定妆照失败:文件不存在"))
	if fa.Checked || fa.Error == "" || fa.Skipped != "" {
		t.Errorf("失败必须 Checked=false 且带原始错误:%+v", fa)
	}
	if ProportionWarnText(sk) != "" || ProportionWarnText(fa) != "" {
		t.Error("跳过/失败都不该产生告警文案")
	}
	// 缺图路径 / 空图也要如实报错
	if _, err := DetectPortraitProportion(context.Background(), &fakeVision{}, "", true); err == nil {
		t.Error("缺定妆照路径应报错")
	}
}

// 真读一张图:路径 → base64 data URL → 单图送视觉模型(与 identity.go 同一口径)。
func TestDetectPortraitProportionReadsImageFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sheet.png")
	// 1×1 PNG(content 不重要:这条只钉"读了、编码对了、只送一张")
	raw := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	fv := &fakeVision{outs: []string{propJSON(map[string][3]string{
		"eye_line": {"true", DeviationNone, "中线"},
	}, "ok")}}
	rep, err := DetectPortraitProportion(context.Background(), fv, p, true)
	if err != nil {
		t.Fatalf("读图失败: %v", err)
	}
	if !rep.Checked {
		t.Errorf("读了图就该 Checked:%+v", rep)
	}
	if len(fv.images) != 1 || fv.images[0] != 1 {
		t.Errorf("每张定妆照只送一张图,实际 %v", fv.images)
	}
	if !strings.Contains(fv.prompt, "eye_spacing") || !strings.Contains(fv.prompt, "one eye-width apart") {
		t.Errorf("用户消息里必须把检查表与技能侧口径逐条给全:%q", fv.prompt)
	}
	// 空图必须报错,不许当成"比例没问题"
	empty := filepath.Join(dir, "empty.png")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectPortraitProportion(context.Background(), fv, empty, true); err == nil ||
		!strings.Contains(err.Error(), "为空") {
		t.Errorf("空图必须报错,实际 %v", err)
	}
}

// 检查表的键必须唯一(重复键会让"哪一项"点错名)。
func TestProportionSpecsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range proportionSpecs {
		if s.Key == "" || s.Label == "" || s.Expected == "" {
			t.Errorf("检查表项不完整:%+v", s)
		}
		if seen[s.Key] {
			t.Errorf("检查表键重复:%s", s.Key)
		}
		seen[s.Key] = true
	}
	// 八条口径逐条来自技能侧比例句(缺一条就是"平台只测了一半")
	for _, key := range []string{"eye_line", "eye_spacing", "nose_length", "mouth_length", "chin_width", "neck", "shoulder_width", "body_height"} {
		if _, ok := proportionSpecOf(key); !ok {
			t.Errorf("检查表缺项 %s(技能侧比例句里有这一条)", key)
		}
	}
	if _, ok := proportionSpecOf("golden_ratio"); ok {
		t.Error("平台不另定比例标准:黄金分割不是技能侧口径里的项")
	}
}

// 朝向项(face_direction):定妆照是**全片身份的权威参考图**,出了四分之三侧脸会让
// H3 拿侧脸当身份锚 —— 而种子按资产名锚定("同名必同图"),不换种子它就一直偏着。
//
// 三条判据缺一不可(2026-09-27 真机实测见 docs/定妆照朝向-实测与处置.md):
//
//	① 这一项必须真的在检查表里、且在给模型的提示词里逐条列出 —— 不然模型永远不会回答它;
//	② "没量到"与"量到且是正面"必须分得开(否则"没验过"看起来像"合格");
//	③ **只有 clear 才触发重出**:重出一张要真花 20-30 秒 GPU + 一次视觉调用。
func TestProportionFacingItemIsMeasured(t *testing.T) {
	spec, ok := proportionSpecOf(KeyFaceDirection)
	if !ok {
		t.Fatal("检查表缺朝向项:朝向是定妆照的唯一构图硬约束,测不到就只能靠肉眼")
	}
	if spec.Label != "朝向" {
		t.Errorf("朝向项的中文名应为「朝向」(告警文案直接用它),实际 %q", spec.Label)
	}
	// 提示词必须逐条列出(模型按这个清单回答;漏列 = 这项永远回到 Invisible)
	prompt := proportionUserPrompt()
	if !strings.Contains(prompt, KeyFaceDirection) || !strings.Contains(prompt, spec.Expected) {
		t.Error("给视觉模型的检查清单里没有朝向项(或没有给出期望口径)—— 这项会被答成「看不见」")
	}
	// 系统提示词要把"什么算偏离"说清:四分之三侧/侧脸 = clear
	if !strings.Contains(proportionSystem, "three-quarter") {
		t.Error("系统提示词必须写明「四分之三侧/侧脸算明显偏离」,否则模型会拿自己的审美判朝向")
	}

	// ② 三态分流:没量到 ≠ 合格
	none := &ProportionReport{Checked: true, Measured: 1,
		Slight: []ProportionItem{{Key: "eye_line", Item: "眼线位置"}}}
	if got := FacingOf(none); got != DeviationNone {
		t.Errorf("朝向项既不在 clear/slight、也不在「看不见」里 = 量到且合格,应为 none,实际 %q", got)
	}
	inv := &ProportionReport{Checked: true, Invisible: []string{"朝向"}}
	if got := FacingOf(inv); got != "" {
		t.Errorf("朝向项看不见时是「没量到」(空档位),不许当成合格,实际 %q", got)
	}
	if got := FacingOf(&ProportionReport{}); got != "" {
		t.Errorf("没做自检(Checked=false)时是「没量到」,实际 %q", got)
	}
	if got := FacingOf(nil); got != "" {
		t.Errorf("nil 报告是「没量到」,实际 %q", got)
	}

	// ③ 只有 clear 才重出
	for _, tc := range []struct {
		name string
		rep  *ProportionReport
		want bool
	}{
		{"四分之三侧 → 重出", &ProportionReport{Checked: true, Clear: []ProportionItem{{Key: KeyFaceDirection, Item: "朝向"}}}, true},
		{"轻微偏差 → 不重出(模型犹豫一下就重渲是浪费)", &ProportionReport{Checked: true, Slight: []ProportionItem{{Key: KeyFaceDirection, Item: "朝向"}}}, false},
		{"正面 → 不重出", &ProportionReport{Checked: true, Slight: []ProportionItem{{Key: "eye_line", Item: "眼线位置"}}}, false},
		{"没量到 → 不重出(不能拿「没验过」当「不合格」去烧 GPU)", nil, false},
	} {
		if got := FacingNeedsRegen(tc.rep); got != tc.want {
			t.Errorf("%s: FacingNeedsRegen = %v,期望 %v", tc.name, got, tc.want)
		}
	}
}

// 朝向偏离走的是**同一次视觉调用**(不额外多调一次模型),且解析路径与比例项一致。
func TestProportionParsesFacingItem(t *testing.T) {
	out := propJSON(map[string][3]string{
		"face_direction": {"true", DeviationClear, "鼻尖偏向画面右侧,只看得全左眼(四分之三侧)"},
		"eye_line":       {"true", DeviationNone, "眼线约在头部中线"},
	}, "整体比例正常,但朝向偏了")
	fv := &fakeVision{outs: []string{out}}
	rep, err := judgePortraitProportion(context.Background(), fv, "data:image/png;base64,AAA", false)
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	if got := FacingOf(rep); got != DeviationClear {
		t.Fatalf("朝向应解析成 clear,实际 %q(%+v)", got, rep.Clear)
	}
	if !FacingNeedsRegen(rep) {
		t.Error("四分之三侧必须触发「换种子重出」")
	}
	// 告警文案同样要能点名它(用户看到的是"朝向(期望:正面胸像;实测:…)")
	if w := ProportionWarnText(rep); !strings.Contains(w, "朝向") {
		t.Errorf("告警必须点名朝向,实际 %q", w)
	}
}
