package pipeline

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 三视图板必须写全**三段格数**：3 全身 + 6 头部 + 6 细节 = 15 格。
//
// 【为什么值得一道门】板子是一张**规格书**，少一段不等于"少画几格"—— 出图模型不会因为
// 你没写就留白，它会**把那段位置用别的东西填满**（实测常见：6 个细节格变成 6 张侧脸，
// 板子成了"9 张脸的横条"），而外观上仍是"一张板"，日志与契约门都看不出问题。
//
// 判据本体在 `project.BoardCellIssues`（`contract_gates.go` 的契约门第二批），
// 这里只钉"装配出来的板提示词真的带全了三段"。
func TestBoardPromptHasAllThreeCellGroups(t *testing.T) {
	portrait := "ultra-wide angle, grand scale, epic wide shot; a lean young man in dark robes, " +
		"eight and a half heads tall, straight-backed; MATTE CINEMATIC RENDER; " +
		"half-body chest-up framing, the whole head inside the frame"
	got := BoardImagePrompt(portrait)
	if miss := project.BoardCellIssues(got); len(miss) > 0 {
		t.Errorf("板提示词缺格数段：%v\n实际：\n%s", miss, got)
	}
	// 三段各自的常量也必须与文案对得上（改了文案忘了改判据 = 门变成摆设）
	if project.PlanBoardFullBodyCells+project.PlanBoardHeadCells+project.PlanBoardDetailCells != 15 {
		t.Errorf("三视图板总格数应为 15（3+6+6），常量给的是 %d+%d+%d",
			project.PlanBoardFullBodyCells, project.PlanBoardHeadCells, project.PlanBoardDetailCells)
	}
}
