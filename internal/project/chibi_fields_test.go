package project

import "testing"

// Q 版档位字段（技能侧 2026-09-29 要补进模板的那套）—— 夹具照锚表真实写法。
//
// 为什么这几个字段重要：平台侧要把 Q 版**由主卡定妆照图生图**产出，就必须确定性地知道
// "这个 Q 版是谁的"；头身比要喂区间门与出图提示词；位置要进分镜的空间锚
// （实测这条丢了，Q 版在成片里变成"单独蹲在石台上"）。
func TestParseChibiFields(t *testing.T) {
	src := `### 碑生（丙十七）
- 年龄：二十出头
- 身形：瘦高，肩薄，约七头半身
- 比例锚_机器：7.5 头身
- Q版形象：有
- Q版本体：碑生（丙十七）
- Q版比例锚_机器：2.5 头身
- Q版位置：总蹲在碑生左肩上
`
	f, ok := ParseChibiFields(src)
	if !ok || !f.Has {
		t.Fatalf("没认出 Q 版档位：ok=%v has=%v", ok, f.Has)
	}
	if f.Parent != "碑生（丙十七）" {
		t.Errorf("本体名取错：%q（ref2img 会拿错人的定妆照）", f.Parent)
	}
	if f.Ratio != 2.5 {
		t.Errorf("Q 版头身比取错：%v（喂区间门与出图提示词）", f.Ratio)
	}
	if f.Position != "总蹲在碑生左肩上" {
		t.Errorf("位置取错：%q（这条丢了 Q 版会在成片里乱站）", f.Position)
	}
	// 「无」要认成没有；半角冒号也要认
	if f2, ok2 := ParseChibiFields("- Q版形象: 无"); !ok2 || f2.Has {
		t.Errorf("「Q版形象：无」应判为没有 Q 版，实得 ok=%v has=%v", ok2, f2.Has)
	}
	// 完全没写这套字段的锚表不该误判
	if _, ok3 := ParseChibiFields("- 年龄：三十\n- 身形：高壮"); ok3 {
		t.Error("没有 Q 版字段的锚表被判成了有 Q 版档位")
	}
}
