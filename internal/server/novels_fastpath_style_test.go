package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// 一章一档快路径**必须**把导入时定下的风格串写进每一集输入（`全局风格:` 那一行）。
//
// 【真机事故，2026-10-10】平台在导入时明明扫到了风格（日志原文：
// `🎨 已从这本书里解析到视频风格字段(立项.json 的 art_style):DREAMLIKE CHINESE ARCHITECTURAL
// FANTASY, cinematic vertical poster look: colossa…`），但那一批 **48 集的 source.txt 里
// `全局风格:` 全是空的** —— 因为三档风格裁决原来只写在"分层拆解"分支的 `if ci == 0` 里，
// 而**一章一档快路径在那之前就 return 了**。
//
// 为什么这条要命：剧本会话见空行就自己编风格（`sysOutputFull__OUTPUT.md` 只规定"源文里有就
// 逐字照抄"），真机编成「半写实绘画风，**3D CG 质感**」——与本书风格卡写明的
// 「NOT a plastic 3D render」正相反；平台随后按 medium3D 展开成
// "next-generation 3D CG character render…"，出图就成了塑料娃娃
// （平台自己的 VLM 读图原话：「皮肤蜡感、磨皮重」「像 3D 塑料娃娃，不像真人定妆」）。
//
// 判据是**行为**（读建出来的 source.txt），不是"代码里有没有那句话"：
// 把风格裁决挪回分层拆解那一支，这条测试立刻变红。
func TestFastPathKeepsGlobalStyle(t *testing.T) {
	s := newTestServer(t)
	// 一章一档快路径本身**不调 LLM**（这正是它的意义），但拆集入口要求 LLM 已配置；
	// 给一份能过"已配置"检查的假配置（地址指向本机无用端口，全程不会被调用）。
	{
		g := s.cfg.Get()
		g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model = "http://127.0.0.1:9/v1", "test-key", "test-model"
		if err := s.cfg.Update(g); err != nil {
			t.Fatalf("写入测试用 LLM 配置失败：%v", err)
		}
	}
	const style = "DREAMLIKE CHINESE ARCHITECTURAL FANTASY, a semi-realistic hand-painted finish " +
		"with visible brushwork and fine film grain, NOT a plastic 3D render"

	// 造一本「一章一档」的书（SplitChapters 认得 ≥8 章就走快路径）。
	// 章头与正文按真机形态写：`第001章 标题` + 一段正文。
	var sb strings.Builder
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&sb, "第%03d章 第%d级的活儿\n\n他端着铜锁从证物盘里往前走，两声脆响落在青砖上。\n\n", i, i)
	}

	_, eps, err := s.splitNovelToProjects(context.Background(), "样式快路径夹具",
		sb.String(), 0, importStyle{Style: style, From: "brief"}, "", nil)
	if err != nil {
		t.Fatalf("拆集失败：%v", err)
	}
	if len(eps) == 0 {
		t.Fatal("一章一档快路径一集都没建出来，夹具不成立")
	}
	src, err := s.store.LoadSource(eps[0])
	if err != nil {
		t.Fatalf("读不到建出来的 source（%s）：%v", eps[0], err)
	}
	got := globalStyleLineOf(src)
	if got == "" {
		t.Errorf("一章一档快路径建出来的集「全局风格:」是空的 —— 剧本会话会自己编风格，"+
			"真机就是这么变成「半写实绘画风，3D CG 质感」的（集：%s）", eps[0])
	}
	if got != style {
		t.Errorf("「全局风格:」该逐字等于导入时定下的串。\n 期望：%q\n 实际：%q", style, got)
	}
}

// globalStyleLineOf 取 source 里「全局风格:」那一行的值（与
// `director.extractGlobalStyle` 同一口径：截到换行、去空白）。
func globalStyleLineOf(src string) string {
	for _, ln := range strings.Split(src, "\n") {
		if i := strings.Index(ln, globalStyleLabel); i >= 0 {
			return strings.TrimSpace(ln[i+len(globalStyleLabel):])
		}
	}
	return ""
}
