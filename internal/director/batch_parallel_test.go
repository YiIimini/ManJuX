package director

import (
	"strings"
	"testing"
)

// 并行出镜的衔接上下文：用**规划轮给的前一段 brief**。
//
// 【2026-09-28 用户问「拆完分镜脚本，脚本中分不同的镜头，这不需要花费那么的推理时间吧」】
// 分批出镜原来是逐批串行的（36 镜 6 批 = 6 次完整推理），而各批之间本来没有依赖：
// 段落边界与各段梗概在规划轮就定好了。改成并发之后，喂给每一批的"上一段是什么"
// 不能再等上一批的产出，改用规划轮的 brief —— 它本来就是同一份信息的上游。
func TestPrevSegBriefUsesPlanningBrief(t *testing.T) {
	segs := []segment{
		{From: 1, To: 5, Brief: "开场：祁七在回收站醒来，环上的编号闪着"},
		{From: 6, To: 10, Brief: ""},
	}
	if got := prevSegBrief(segs, 0); got != "" {
		t.Errorf("第一批没有上一段，不该给衔接上下文，实际 %q", got)
	}
	got := prevSegBrief(segs, 1)
	if !strings.Contains(got, "镜 1-5") || !strings.Contains(got, "祁七在回收站醒来") {
		t.Errorf("衔接上下文要带上一段的镜号区间与梗概，实际 %q", got)
	}
	// 上一段没有梗概时也要给一句能接的话：空串等于把上下文断了
	if got := prevSegBrief(segs, 2); strings.TrimSpace(got) == "" {
		t.Error("上一段没有 brief 时也要给一句衔接语，不能是空串")
	}
	// 越界不该 panic、也不该编造
	if got := prevSegBrief(segs, 9); got != "" {
		t.Errorf("批号越界应返回空串，实际 %q", got)
	}
	if got := prevSegBrief(nil, 1); got != "" {
		t.Errorf("没有段落时应返回空串，实际 %q", got)
	}
}
