// castcontrast_test.go 回归:同剧班底对比块(2026-09-11 用户定调「不同
// 角色脸不能相似」)——对比块必须列出其它角色、不列自己、有标识头。
package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

func TestCastContrastBlock(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "a", Name: "纪野", Description: "19岁清瘦少年,右眉尾旧疤"},
		{ID: "b", Name: "魏启明", Description: "肥胖壮硕圆脸少年"},
		{ID: "c", Name: "裴修远", Description: "23岁窄长脸,白西装"},
	}}
	b := castContrastBlock(plan, "魏启明", KindHuman)
	if !strings.Contains(b, "纪野") || !strings.Contains(b, "裴修远") {
		t.Fatalf("对比块应列出其它角色: %q", b)
	}
	if strings.Contains(b, "魏启明(肥胖") {
		t.Fatalf("对比块不应把自己列进班底列表: %q", b)
	}
	if !strings.Contains(b, "CAST DISTINCTNESS") || !strings.Contains(b, "「魏启明」") {
		t.Fatalf("缺标识头/自称: %q", b)
	}
	if castContrastBlock(nil, "x", KindHuman) != "" || castContrastBlock(&project.Plan{}, "x", KindHuman) != "" {
		t.Fatalf("无 plan/无其它角色时应返回空")
	}
	if castContrastBlock(&project.Plan{Characters: []project.Character{{Name: "独角"}}}, "独角", KindHuman) != "" {
		t.Fatalf("单角色剧无对比对象应返回空")
	}
}
