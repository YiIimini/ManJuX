package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 运行日志必须**按管线节点分组**（2026-09-29 用户要求：
// 「运行日志目前显示的好杂，精简优化，一眼看明白（分类以不同颜色区分展示，按管线节点分类，
// 分类可收起/展开方便查阅，成功的分类自动收起）」）。
//
// 这条测试钉的是四件事，缺一条用户就会说"还是那么杂"：
//
//	① 按 **phase**（后端事件自带的阶段字段）分组，而不是平铺成一列；
//	② 每个阶段有**自己的颜色**（PHASE_TONE），组头按组内最严重的级别着色；
//	③ 组头可点（收起/展开），且用户点过的以他为准（manual 表）；
//	④ **成功的分类自动收起**：默认规则 = 最新那一组 + 出过 warn/error 的组展开，
//	   其余收起 —— 阶段一跑完就被后来者顶下去，自然收起，不需要任何 watcher。
func TestLogConsoleGroupsByPhase(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "components", "LogConsole.vue"))
	if err != nil {
		t.Fatalf("读 LogConsole.vue 失败：%v", err)
	}
	src := string(raw)

	// ① 分组 + ② 配色
	for _, want := range []struct{ needle, why string }{
		{"const groups = computed(", "日志必须按阶段分组渲染（平铺成一列正是用户说的\"好杂\"）"},
		{"PHASE_TONE", "每个管线节点要有自己的颜色（分类以不同颜色区分）"},
		{"worstLevel", "组头要按组内最严重的级别着色（有 warn/error 的那一组一眼能看见）"},
		// ③ 可收起/展开 + 手动优先
		{"@click=\"toggleGroup(g)\"", "组头必须可点（收起/展开）"},
		{"manual.value[g.id] !== undefined", "用户手动展开/收起过的那一组，要以他点过的为准"},
		// ④ 成功的自动收起
		{"i === 0 || hasProblem", "默认规则：最新那一组与有问题的组展开，其余（已完成的阶段）收起"},
		{"l.phase", "分组键必须用后端事件的 phase（按管线节点分类的唯一真源）"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("LogConsole.vue 缺少 %q —— %s", want.needle, want.why)
		}
	}
	// 反面：不许再按数组下标当 key（倒序列表 + 下标 key = 每来一条事件全列重新 patch）
	if strings.Contains(src, "v-for=\"l in reversed\"") {
		t.Error("日志又退回平铺渲染了：分组是这一轮的要求，别把 reversed 直接铺出来")
	}
}
