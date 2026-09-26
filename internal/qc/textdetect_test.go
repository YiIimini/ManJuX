package qc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/llm"
)

// 画面文字检测的单测(清单 §3.1a)。
//
// 这一条链的价值全在判据上:有字要报、无字要静默、**没验过不许看起来像验过了**。
// 真机抽帧 + 真视觉调用属真机验证(那需要真片与额度),这里用假客户端把判据钉死 ——
// 假客户端同时能造出"模型没逐帧回答""模型自造帧号""输出不是 JSON"这些真机上偶发、
// 而单测里可穷举的失败形态。

// fakeVision 假视觉客户端:按脚本逐次返回(第 n 次调用用第 n 条;picked 记下收到的图数)。
type fakeVision struct {
	outs   []string
	errs   []error
	calls  int
	images []int
	prompt string
}

func (f *fakeVision) ChatVision(_ context.Context, _ string, turns []llm.VisionContent, _ bool) (string, error) {
	f.calls++
	if len(turns) > 0 {
		f.images = append(f.images, len(turns[0].Images))
		f.prompt = turns[0].Text
	}
	i := f.calls - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return "", f.errs[i]
	}
	if i < len(f.outs) {
		return f.outs[i], nil
	}
	return "", errors.New("假客户端没有更多脚本")
}

// frames5 五个采样帧(与 samplePositions 同形,但不需要真 ffmpeg)。
func frames5() []sampledFrame {
	out := make([]sampledFrame, 0, 5)
	for i := 1; i <= 5; i++ {
		out = append(out, sampledFrame{Index: i, AtSec: float64(i), Image: fmt.Sprintf("data:image/jpeg;base64,AAA%d", i)})
	}
	return out
}

// json5 造一份"5 帧、指定帧命中"的模型输出。
func json5(hits map[int]bool) string {
	var b strings.Builder
	b.WriteString(`{"frames":[`)
	for i := 1; i <= 5; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		note := ""
		conf := 0
		if hits[i] {
			note = fmt.Sprintf("右下角有字幕「第%d句」", i)
			conf = 80 + i
		}
		fmt.Fprintf(&b, `{"index":%d,"has_text":%v,"confidence":%d,"note":%q}`, i, hits[i], conf, note)
	}
	b.WriteString(`],"note":"台词字幕出现在后半段"}`)
	return b.String()
}

// 有文字 → 命中:帧号、置信度、逐帧留痕都要齐,并能折出告警文案。
func TestTextDetectReportsHit(t *testing.T) {
	fv := &fakeVision{outs: []string{json5(map[int]bool{2: true, 4: true})}}
	rep, err := judgeTextFrames(context.Background(), fv, frames5())
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	if !rep.Checked {
		t.Error("真的看了画面,Checked 必须为 true")
	}
	if !rep.HasText {
		t.Fatal("模型报了两帧有字,结论必须是命中")
	}
	if got := fmt.Sprint(rep.HitFrames); got != "[2 4]" {
		t.Errorf("命中帧号 = %s,期望 [2 4]", got)
	}
	if rep.SampledFrames != 5 || len(rep.Frames) != 5 {
		t.Errorf("逐帧留痕不完整:sampled=%d frames=%d", rep.SampledFrames, len(rep.Frames))
	}
	if rep.MaxConfidence != 84 { // 帧 4 给的是 84
		t.Errorf("最高置信度 = %d,期望 84", rep.MaxConfidence)
	}
	w := TextWarnText(rep)
	for _, want := range []string{"帧 2、4", "2/5 帧命中", "84%", rep.Note} {
		if !strings.Contains(w, want) {
			t.Errorf("告警文案应含 %q,实际 %q", want, w)
		}
	}
}

// 无文字 → 静默:HasText=false 且告警文案为空(不许每镜都刷一条"没字")。
func TestTextDetectNoTextIsSilent(t *testing.T) {
	fv := &fakeVision{outs: []string{json5(nil)}}
	rep, err := judgeTextFrames(context.Background(), fv, frames5())
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	if rep.HasText || len(rep.HitFrames) != 0 {
		t.Errorf("模型说没有字,不该报命中:%+v", rep)
	}
	if !rep.Checked {
		t.Error("Checked 必须为 true —— 「看过且干净」与「没看过」是两件事")
	}
	if w := TextWarnText(rep); w != "" {
		t.Errorf("没有命中就不该产生告警文案,实际 %q", w)
	}
}

// 未判定不许伪装:跳过/失败的报告必须与"看过且干净"可区分。
func TestTextDetectSkippedAndFailedAreDistinguishable(t *testing.T) {
	sk := TextDetectSkipped("未配置视觉模型")
	if sk.Checked || sk.HasText || sk.Skipped == "" {
		t.Errorf("跳过必须 Checked=false 且带原因:%+v", sk)
	}
	if TextWarnText(sk) != "" {
		t.Error("跳过不该产生告警文案")
	}
	fa := TextDetectFailed(errors.New("抽帧失败:ffmpeg not found"))
	if fa.Checked || fa.HasText || !strings.Contains(fa.Error, "抽帧失败") {
		t.Errorf("失败必须 Checked=false 且带原始错误:%+v", fa)
	}
	// 两者的 JSON 形态也必须能分辨(报告是给人读的)
	if sk.Skipped == "" || fa.Skipped != "" || fa.Error == "" {
		t.Error("skipped 与 error 字段必须各自留痕,不能混用")
	}
}

// 模型没逐帧回答 / 帧号对不上 / 输出不是 JSON:三种都必须报错,**不许**折成"没有字"。
func TestTextDetectRefusesToGuess(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"一帧都没回答", `{"frames":[],"note":"看起来没问题"}`},
		{"帧号全对不上", `{"frames":[{"index":7,"has_text":true,"confidence":90}],"note":""}`},
		{"不是 JSON", `我不确定,画面里好像有字。`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fv := &fakeVision{outs: []string{tc.out}}
			rep, err := judgeTextFrames(context.Background(), fv, frames5())
			if err == nil {
				t.Fatalf("必须报错(未判定),却拿到了结论:%+v", rep)
			}
		})
	}
	// 反向:模型自造的帧号被丢弃,但**采过的帧**照样判定(不能因为一行噪声整镜作废)
	fv := &fakeVision{outs: []string{`{"frames":[{"index":9,"has_text":true,"confidence":99},{"index":3,"has_text":false,"confidence":10}],"note":"x"}`}}
	rep, err := judgeTextFrames(context.Background(), fv, frames5())
	if err != nil {
		t.Fatalf("有效帧存在时应照常判定: %v", err)
	}
	if rep.HasText {
		t.Errorf("只有自造帧号 9 说有字,不该判命中:%+v", rep.HitFrames)
	}
	if len(rep.Frames) != 1 || !strings.Contains(rep.Note, "模型仅逐帧回答了 1/5 帧") {
		t.Errorf("覆盖率不足必须留痕:%+v", rep)
	}
}

// 围栏/散文包裹的 JSON 也要能解析(模型常这么答)。
func TestTextDetectParsesFencedJSON(t *testing.T) {
	out := "好的,我逐帧看了:\n```json\n" + json5(map[int]bool{5: true}) + "\n```\n以上。"
	fv := &fakeVision{outs: []string{out}}
	rep, err := judgeTextFrames(context.Background(), fv, frames5())
	if err != nil {
		t.Fatalf("围栏 JSON 解析失败: %v", err)
	}
	if fmt.Sprint(rep.HitFrames) != "[5]" {
		t.Errorf("命中帧 = %v,期望 [5]", rep.HitFrames)
	}
}

// 抽取与调用链的形状:一次调用带全部采样帧、提示词点名了帧数与时间点。
func TestTextDetectSendsAllSampledFrames(t *testing.T) {
	fv := &fakeVision{outs: []string{json5(nil)}}
	if _, err := judgeTextFrames(context.Background(), fv, frames5()); err != nil {
		t.Fatal(err)
	}
	if fv.calls != 1 {
		t.Errorf("应该一次调用判完所有帧,实际调了 %d 次", fv.calls)
	}
	if len(fv.images) != 1 || fv.images[0] != 5 {
		t.Errorf("应把 5 帧一起送出去,实际 %v", fv.images)
	}
	if !strings.Contains(fv.prompt, "5 images") || !strings.Contains(fv.prompt, "t≈1.00s") {
		t.Errorf("提示词要点明帧数与时间点,实际 %q", fv.prompt)
	}
}

// 未配置视觉模型:检测入口本身要拒绝(调用方据此如实降级,而不是拿到"没有字")。
func TestDetectOnScreenTextWithoutClientIsHonest(t *testing.T) {
	if _, err := DetectOnScreenText(context.Background(), nil, nil, "x.mp4", 5, TextDetectOptions{}); err == nil ||
		!strings.Contains(err.Error(), "未配置视觉模型") {
		t.Fatalf("未配置视觉模型必须明确报错,实际 %v", err)
	}
}

// 集级判据:超阈值是显式告警,不是 fail;未超阈值一律 nil(静默)。
func TestTextHitAlertThresholds(t *testing.T) {
	cases := []struct {
		name     string
		examined int
		hits     []int
		wantNil  bool
	}{
		{"3/20=15% 超阈值 → 告警", 20, []int{4, 9, 15}, false},
		{"2/10=20% 超阈值 → 告警", 10, []int{1, 2}, false},
		{"2/20=10% 恰好等于阈值(判据是 >10%)→ 不升级", 20, []int{3, 4}, true},
		{"1/20=5% 未超 → 静默", 20, []int{7}, true},
		{"1/5=20% 但命中镜数 < 2(小分母噪声)→ 不升级", 5, []int{3}, true},
		{"一镜没测过 → 没有分母,不升级", 0, []int{1, 2}, true},
		{"无命中 → 静默", 20, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TextHitAlert(tc.examined, tc.hits)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("不该升级却拿到告警:%+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("超阈值必须给出显式告警")
			}
			if got.Examined != tc.examined || len(got.HitShots) != len(tc.hits) {
				t.Errorf("告警账目不对:%+v", got)
			}
			wantRatio := float64(len(tc.hits)) / float64(tc.examined)
			if got.Ratio != wantRatio {
				t.Errorf("命中率 = %v,期望 %v", got.Ratio, wantRatio)
			}
			// 文案必须点名命中镜号并带上阈值 —— 否则人拿到告警也不知道去看哪几镜
			for _, want := range []string{fmt.Sprintf("%d/%d", len(tc.hits), tc.examined), "命中镜", "阈值"} {
				if !strings.Contains(got.Msg, want) {
					t.Errorf("告警文案应含 %q,实际 %q", want, got.Msg)
				}
			}
			for _, id := range tc.hits {
				if !strings.Contains(got.Msg, fmt.Sprintf("%d", id)) {
					t.Errorf("告警文案应点名镜号 %d,实际 %q", id, got.Msg)
				}
			}
			// 告警文案里"命中镜"后面不能漏掉**镜**这个量纲(帧号与镜号是两回事)
			if strings.Contains(got.Msg, "命中帧") {
				t.Errorf("集级文案说的是镜,不该出现帧:%q", got.Msg)
			}
		})
	}
}

// 置信度钳制:模型偶尔给出 120 或负数。
func TestClampConfidence(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{-5, 0}, {0, 0}, {73, 73}, {100, 100}, {120, 100}} {
		if got := clampConfidence(tc.in); got != tc.want {
			t.Errorf("clampConfidence(%d) = %d,期望 %d", tc.in, got, tc.want)
		}
	}
}

// 低置信度也算命中:这是给人看的标记,不是自动重渲的判据(宁多标一次)。
func TestLowConfidenceStillCounts(t *testing.T) {
	rep := buildTextReport([]TextFrameHit{
		{Index: 1, HasText: false},
		{Index: 2, HasText: true, Confidence: 5, Note: "左上角似乎有字"},
	}, "", 5)
	if !rep.HasText || rep.MaxConfidence != 5 {
		t.Errorf("低置信度命中必须照样标出来:%+v", rep)
	}
	if w := TextWarnText(rep); !strings.Contains(w, "标记不是判负") {
		t.Errorf("告警文案要讲清这是标记不是判负,实际 %q", w)
	}
}
