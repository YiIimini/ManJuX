package project

import (
	"strings"
	"testing"
)

// source_sections_test.go 本集 source.txt 的段落读取判据。
//
// 【夹具照真机原文写】下面这段的**剧本段**逐字取自真机 source.txt（2026-10-07/10-08 两轮取证里
// 反复引用的那几行：爆点句、场次头、五节），**原文段**按拆集写进去的形态拼（第001章 + 正文）。
// 判据要咬的就是"剧本段与原文段能不能被准确切开" —— 夹具不真，这道门就是摆设。

const realSourceFixture = `《名册之外，八万零一号》系列短视频 第01 集《我是代号》
全局风格:国风写实

【本集剧情】【开场爆点】“重新检测到生命反应。”——灰纪元第八万年，编号 U-80001 的硬件钢印刚亮起，下一句就是「该编号不存在，予以静默」。
【场 1|归墟环·维护舱|灰纪元第八万年·第三千四百六十二标准日|内】
  目的:主角刚睁眼就知道自己「不算人」。
  冲突:祁七 vs 舱内 AI——她要一个编号，AI 只给静默。
  转折:她把铭牌按在扫描口上，屏幕回了四个字。
  爽点:大爽点——当场把「不存在」的编号念出来，AI 被迫应答。
  钩子:应答之后，舱门自己开了。
  正文:祁七把铭牌按在扫描口上。“U-80001。”屏幕亮起四个字：编号有效。
【场 2|归墟环·主控廊|灰纪元第八万年·第三千四百六十二标准日|内】
  目的:把「她为什么存在」推到台面上。
  冲突:祁七 vs 沈砚——他要把她关机。
  转折:他看见她手背的编号。
  爽点:小爽点——祁七先一步拔了他的权限卡。
  钩子:权限卡背面刻着同一个编号。
  正文:沈砚抬手要关机。祁七先一步拔了他的权限卡。
【关键道具与信息】
  - 铭牌|挂在祁七腰侧|她的身份证明
  - 权限卡|沈砚胸袋|第 2 场第一次揭示
【本集钩子】权限卡背面刻着同一个编号。
【本集原文】(素材：小说原文；分镜只按【本集剧情】排)
第001章 编号之外
她睁开眼的时候，舱里的灯只亮着右边一排。扬声器说，检测到生命反应。然后说，该编号不存在。
`

func TestEpisodeBriefAndOriginalAreSplit(t *testing.T) {
	brief, orig, hasBrief, hasOrig := EpisodeSourceParts(realSourceFixture)
	if !hasBrief || !hasOrig {
		t.Fatalf("两段都该被认出来：hasBrief=%v hasOrig=%v", hasBrief, hasOrig)
	}
	// 剧本段：爆点 + 两场六节 + 道具清单 + 钩子，全在里面。
	for _, want := range []string{"【开场爆点】", "【场 1|", "【场 2|", "爽点:大爽点", "【关键道具与信息】", "【本集钩子】"} {
		if !strings.Contains(brief, want) {
			t.Errorf("剧本段少了 %q", want)
		}
	}
	// 剧本段**不许**把原文段吃进来（那是"读边界"这道门存在的唯一理由）。
	for _, leak := range []string{"【本集原文】", "第001章", "她睁开眼的时候"} {
		if strings.Contains(brief, leak) {
			t.Errorf("剧本段混进了原文：%q", leak)
		}
	}
	// 原文段：从【本集原文】之后到文末（拆集在标记后面写了一句括注"(素材：…)"，那是**原文段的一部分**）。
	if !strings.HasPrefix(orig, "(素材：") || !strings.Contains(orig, "第001章") {
		t.Errorf("原文段取错了：%q", orig)
	}
	// 原文段里不许混进剧本的正文/场次（两个方向都要判，否则边界只是"看起来对"）。
	for _, leak := range []string{"【场 1|", "爽点:大爽点", "祁七把铭牌按在扫描口上"} {
		if strings.Contains(orig, leak) {
			t.Errorf("原文段混进了剧本内容：%q", leak)
		}
	}
	// 抬头（书名/集名/全局风格）不属于任何一段 —— 这是拆集写进去的第三种材料。
	for _, leak := range []string{"全局风格", "《名册之外"} {
		if strings.Contains(brief, leak) || strings.Contains(orig, leak) {
			t.Errorf("抬头被算进材料段了：%q", leak)
		}
	}
}

// TestEpisodeBriefFallsBackHonestly 没有【本集剧情】时必须**说没有**（ok=false），
// 而不是返回整份文本让调用方以为"有剧本"。
func TestEpisodeBriefFallsBackHonestly(t *testing.T) {
	onlyOrig := "《某剧》第01集\n全局风格:国风\n\n【本集原文】\n第001章 开场\n她睁开眼。\n"
	if brief, ok := EpisodeBrief(onlyOrig); ok || strings.TrimSpace(brief) != "" {
		t.Errorf("没有剧本标记时必须 ok=false，实际 ok=%v brief=%q", ok, brief)
	}
	if _, _, hasBrief, hasOrig := EpisodeSourceParts(onlyOrig); hasBrief || !hasOrig {
		t.Errorf("只有原文时：hasBrief=%v hasOrig=%v（想要 false/true）", hasBrief, hasOrig)
	}
	// 原文缺标记 ⇒ 退回全文（技能侧包/裁过的稿子：整份文本就是全部材料）。
	plain := "任意一份没有节标记的文本"
	if got := EpisodeOriginal(plain); got != plain {
		t.Errorf("没有【本集原文】标记时应退回全文，实际 %q", got)
	}
	if _, _, _, hasOrig := EpisodeSourceParts(plain); hasOrig {
		t.Error("没有标记时 hasOrig 必须是 false（否则调用方以为材料里有原文段）")
	}
}

// TestEpisodeBriefBoundaryIsNotBlankLine 【真机踩过的坑】模型写的剧本本来就有空行，
// 边界只能取**平台自己的节标记**：用空行切时只取回 51 字（整段 2090 字）。
func TestEpisodeBriefBoundaryIsNotBlankLine(t *testing.T) {
	src := "【本集剧情】【开场爆点】“重新检测到生命反应。”\n\n【场 1|归墟环·维护舱|内】\n  目的:…\n【本集原文】\n第001章\n"
	brief, ok := EpisodeBrief(src)
	if !ok {
		t.Fatal("应当取到剧本段")
	}
	if strings.Contains(brief, "【本集原文】") || strings.Contains(brief, "第001章") {
		t.Errorf("边界没落在节标记上：%q", brief)
	}
	if !strings.Contains(brief, "【场 1|") {
		t.Errorf("空行之后的场次头被切掉了（就是真机那次只取回 51 字的原因）：%q", brief)
	}
}
