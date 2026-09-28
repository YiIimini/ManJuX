package project

import "testing"

// 技能侧 2026-09-28 通知的 C4 其余分项（平台侧按 WARN）：长度与 Windows 保留名。
//
// 为什么不升 FAIL：非法字符属于"平台根本写不出这个路径"（必然失败 ⇒ 硬拦）；
// 长度与保留名属于"能写但不好"（顶路径长度上限、撞设备名）—— 把后者也做成 fail
// 会把一堆能跑的集数拒掉，那是把口径的洁癖转嫁给用户。技能侧通知里也允许分项做 WARN。
func TestNameRuleProblems(t *testing.T) {
	// 长度：章名 14、书名 20（技能侧规定）
	long15 := "一二三四五六七八九十一二三四五" // 15 字
	if len([]rune(long15)) != 15 {
		t.Fatalf("夹具字数不对: %d", len([]rune(long15)))
	}
	if got := ChapterNameProblems(long15); len(got) == 0 || got[0].Kind != "长度" {
		t.Errorf("超过 14 字的章名应报长度问题，实际 %+v", got)
	}
	ok14 := long15[:len("一二三四五六七八九十一二三四")] // 14 字：刚好上限，不该报
	if got := ChapterNameProblems(ok14); len(got) != 0 {
		t.Errorf("14 字是上限本身，不该报（边界要放行），实际 %+v", got)
	}
	// 书名上限 20：14 字的章名当书名应放行
	if got := BookNameProblems(ok14); len(got) != 0 {
		t.Errorf("书名上限 20，14 字不该报，实际 %+v", got)
	}
	// 保留名（带扩展名也算：章名即文件名）
	for _, n := range []string{"CON", "nul", "Com1", "LPT9", "CON.md"} {
		if got := ChapterNameProblems(n); len(got) == 0 || got[0].Kind != "保留名" {
			t.Errorf("%q 是 Windows 保留名，应报，实际 %+v", n, got)
		}
	}
	// 正常章名：一条都不该报
	for _, n := range []string{"碑上写我死了", "她替他挡账（二）", "封碑三日"} {
		if got := ChapterNameProblems(n); len(got) != 0 {
			t.Errorf("正常章名 %q 不该报，实际 %+v", n, got)
		}
	}
	// 空名不在这里报（调用方用「未命名」兜底）
	if got := ChapterNameProblems("   "); len(got) != 0 {
		t.Errorf("空名由调用方兜底，这里不该报，实际 %+v", got)
	}
}
