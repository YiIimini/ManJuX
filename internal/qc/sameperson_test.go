package qc

import (
	"context"
	"strings"
	"testing"
)

// TestParseSamePerson 解析容忍度：模型爱怎么拐弯都行，但**认不出必须报错**。
//
// 这条尤其重要：判定失败若被当成"不同"，撞脸就会静默通过 —— 与比例自检同一条口径
// （"没验过" ≠ "验过了没问题"）。
func TestParseSamePerson(t *testing.T) {
	cases := []struct {
		name string
		in   string
		same bool
		bad  bool
	}{
		{"标准 SAME", "SAME\n两人都是瘦长脸、年纪相仿,发髻也几乎一样", true, false},
		{"标准 DIFFERENT", "DIFFERENT\n脸型一圆一长,年龄感差十岁", false, false},
		{"带标点", "**SAME**。\n像同一个演员换了衣服", true, false},
		{"中文结论", "同一个人\n脸型与眼型几乎一致", true, false},
		{"中文否定", "不同\n脸型差异明显", false, false},
		{"先解释后结论", "我先看脸型与眼型。\nDIFFERENT\n下颌线差别很大", false, false},
		{"小写", "same\n像兄弟", true, false},
		{"认不出", "我看不出来这两张图", false, true},
		{"空", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := parseSamePerson(tc.in)
			if tc.bad {
				if err == nil {
					t.Fatalf("认不出时必须报错（不能默认成「不同」）: %+v", v)
				}
				return
			}
			if err != nil {
				t.Fatalf("应能解析:%v", err)
			}
			if v.Same != tc.same {
				t.Errorf("Same = %v,期望 %v（原文 %q）", v.Same, tc.same, tc.in)
			}
		})
	}
}

// TestJudgeSamePersonSendsBothImages 两张图都要真的发出去（只发一张等于没比），
// 且提示词必须明确要求 SAME/DIFFERENT 两个词（否则模型没法照口径回）。
func TestJudgeSamePersonSendsBothImages(t *testing.T) {
	f := &fakeVision{outs: []string{"DIFFERENT\n不一样"}}
	v, err := judgeSamePerson(context.Background(), f, "data:image/png;base64,AAA", "data:image/png;base64,BBB")
	if err != nil {
		t.Fatal(err)
	}
	if v.Same {
		t.Error("应判为不同")
	}
	if len(f.images) != 1 || f.images[0] != 2 {
		t.Errorf("两张图都要发给视觉模型,实际 %v", f.images)
	}
	if !strings.Contains(f.prompt, "SAME") || !strings.Contains(f.prompt, "DIFFERENT") {
		t.Errorf("提示词要明确要求 SAME/DIFFERENT:%s", f.prompt)
	}
}

// TestSamePersonWarnText 撞脸告警必须点名"和谁像"并说清后果（否则用户不知道要改哪张）。
func TestSamePersonWarnText(t *testing.T) {
	txt := SamePersonWarnText("周廷", "沈砚", &SamePersonVerdict{Same: true, Note: "都是瘦长脸"})
	for _, want := range []string{"周廷", "沈砚", "都是瘦长脸", "认成一个人"} {
		if !strings.Contains(txt, want) {
			t.Errorf("告警里缺 %q:%s", want, txt)
		}
	}
	if SamePersonWarnText("周廷", "沈砚", &SamePersonVerdict{Same: false}) != "" {
		t.Error("不撞脸时不该出声")
	}
}
