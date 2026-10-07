package qc

import (
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/asr"
)

// SpeechSummary / SpeechWarnText 的**文案判据**。
//
// 这一段的要害不在措辞好听，而在三件事必须分得开：
//
//	① "没验过"（跳过/失败）与"验过且干净" —— 报告与日志里长得一样就等于骗人；
//	② 三类结论（重复念 / 凭空加话 / 漏念）各自指向不同的返工动作，混成一句"配音有问题"没法动手；
//	③ 归因不能写死：ASR 的同音字误差与转写幻觉都在，所以 extra 的文案必须把"两种可能"都写出来。
func TestSpeechSummaryDistinguishesNotChecked(t *testing.T) {
	if got := SpeechSummary(nil); got != "" {
		t.Errorf("没开这项功能时不该有摘要，实际 %q", got)
	}
	skip := SpeechSummary(SpeechSkipped("未做语音核对:本机没有 ASR"))
	if !strings.Contains(skip, "跳过") || !strings.Contains(skip, "没有 ASR") {
		t.Errorf("跳过必须说清原因，实际 %q", skip)
	}
	fail := SpeechSummary(SpeechFailed(errStub{}))
	if !strings.Contains(fail, "失败") || !strings.Contains(fail, "不当作通过") {
		t.Errorf("核对失败不能看起来像通过，实际 %q", fail)
	}
	clean := SpeechSummary(&SpeechReport{Checked: true, Segments: 2,
		Lines: []asr.LineCheck{{Line: "收到。"}, {Line: "该钢印不在名册内。"}}})
	if !strings.Contains(clean, "通过") || !strings.Contains(clean, "2 段") {
		t.Errorf("验过且干净要报出量到的东西，实际 %q", clean)
	}
	bad := SpeechSummary(&SpeechReport{Checked: true, Findings: []asr.Finding{{Kind: "repeat"}, {Kind: "extra"}}})
	for _, want := range []string{"2 处", "疑似重复", "计划外的话"} {
		if !strings.Contains(bad, want) {
			t.Errorf("有问题时摘要应含 %q，实际 %q", want, bad)
		}
	}
}

// 三类结论的告警文案必须各自可辨认（否则用户不知道该改提示词还是该补时长）。
//
// 夹具里的 Detail 用的是 asr.Compare **真实产出的那句话**（判据在 asr 包，文案在这里）——
// 写成"细节"两个字就测不出"这句话能不能让人动手"。
func TestSpeechWarnTextPerKind(t *testing.T) {
	cases := []struct {
		kind   string
		detail string
		want   []string
	}{
		{"repeat", "音轨里出现了 2 段与这句相像的话（剧本里只该出现一次）—— 疑似同句被念了多遍",
			[]string{"重复", "念了多遍", "改提示词"}},
		{"missing", "音轨里找不到与这句相像的内容（最像的一窗只有 0.00）—— 疑似整句没念",
			[]string{"漏念", "没念"}},
		{"extra", "音轨里说了计划之外的话：「没禁条」—— 剧本里没有这句（H3 自己编的，也可能是 ASR 听岔，请听一遍确认）",
			[]string{"剧本里没有", "H3 自己编的", "也可能是 ASR 听岔", "听一遍"}},
	}
	for _, tc := range cases {
		got := SpeechWarnText(asr.Finding{Kind: tc.kind, Line: "该钢印不在名册内。", Detail: tc.detail})
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("kind=%s 的告警应含 %q，实际 %q", tc.kind, want, got)
			}
		}
	}
	// 叠字：Detail 自己已经写了「音轨里说了计划之外的话」，外面不该再套一层同义前缀。
	extra := SpeechWarnText(asr.Finding{Kind: "extra", Line: "",
		Detail: "音轨里说了计划之外的话：「没禁条」—— 剧本里没有这句（H3 自己编的，也可能是 ASR 听岔，请听一遍确认）"})
	if n := strings.Count(extra, "音轨里"); n != 1 {
		t.Errorf("extra 的文案出现了 %d 次「音轨里」（叠字），实际 %q", n, extra)
	}
}

// 超时预算：CPU medium 实测约每 10 秒素材 12~15 秒（含模型加载约 10 秒），预算必须留足。
func TestSpeechBudget(t *testing.T) {
	// 0 = 调用方没给时长 ⇒ 按单镜计划下限 8 秒算，与 8 秒同价。
	if got := SpeechBudget(0); got != 160*time.Second {
		t.Errorf("没给时长时按单镜下限 8 秒算，实际 %v", got)
	}
	// 很短的素材（残片）也要给足最低 120 秒：模型加载本身就要 10 秒。
	if got := SpeechBudget(5); got != 120*time.Second {
		t.Errorf("5 秒素材应给最低预算 120s，实际 %v", got)
	}
	if got := SpeechBudget(8); got != 160*time.Second {
		t.Errorf("8 秒素材应给 160s，实际 %v", got)
	}
	if got := SpeechBudget(60); got != 1200*time.Second {
		t.Errorf("长素材应按 20 倍给，实际 %v", got)
	}
}

// errStub 只为构造"失败"的最小 error。
type errStub struct{}

func (errStub) Error() string { return "ASR 调用失败(exit 1): 模型没加载上" }
