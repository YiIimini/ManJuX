package comfyengine

import (
	"os"
	"strings"
	"testing"
)

// TestAdminNodeChecksCoverRenderPath 管理页的节点清单必须覆盖**渲染必用根节点**。
//
// 2026-09-27（用户「ComfyUI 我升级了」之后的同步核对）抓到的缺口：管理页原先只查
// 条件缓存 / 镜间接缝这两个**可选**节点 —— 它们缺了会降级，所以"缺了也不致命"；
// 而渲染**必用**的根节点（Ref2V / I2V / 双流 sigma 偏移）只有 `Preflight`（按下渲染那一刻）
// 才查。于是"升级后少了个自定义节点"这件事在管理页上看不出来，要跑到一半才由
// ComfyUI 抛原始报文 —— 正是 Preflight 注释声称要消灭的体验。
//
// 这条测试钉三件：① 三个必用根节点都在清单里且标了 required；② 它们与 Preflight
// **共用同一批常量**（改一边另一边不会走散）；③ 两个可选节点仍在（缺了降级，不是致命）。
func TestAdminNodeChecksCoverRenderPath(t *testing.T) {
	byClass := map[string]NodeCheck{}
	for _, nc := range adminNodeChecks {
		if strings.TrimSpace(nc.Label) == "" {
			t.Errorf("节点 %q 没有给人看的标签（界面上只会显示一个类名的话，用户不知道缺了会怎样）", nc.Class)
		}
		if _, dup := byClass[nc.Class]; dup {
			t.Errorf("节点 %s 在清单里重复", nc.Class)
		}
		byClass[nc.Class] = nc
	}
	for _, c := range []string{classRef2V, classI2V, classSigmaShift} {
		nc, ok := byClass[c]
		if !ok {
			t.Fatalf("渲染必用根节点 %s 不在管理页清单里（升级后缺了要等到渲染才暴露）", c)
		}
		if !nc.Required {
			t.Errorf("节点 %s 缺了会让渲染直接失败，必须标 required", c)
		}
		if !strings.Contains(nc.Label, "缺则") {
			t.Errorf("节点 %s 的标签要说明缺了会怎样（实际 %q）", c, nc.Label)
		}
	}
	// 可选降级项仍在，且**不许**被标成 required（误标会挡住生产）
	for _, c := range []string{classCondSave, classMotionCtx} {
		nc, ok := byClass[c]
		if !ok {
			t.Errorf("可选节点 %s 不该从清单里消失（缺了要能看见当前能力少了一块）", c)
			continue
		}
		if nc.Required {
			t.Errorf("节点 %s 缺了会自动降级，标成 required 会误判挡住生产", c)
		}
	}
}

// TestPreflightAndAdminShareRenderClasses 两侧必须问同一批常量（同一个语义一处定义）。
func TestPreflightAndAdminShareRenderClasses(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", name, err)
		}
		return string(raw)
	}
	src := read("preflight.go")
	for _, c := range []string{"classRef2V", "classI2V", "classSigmaShift"} {
		if !strings.Contains(src, c) {
			t.Errorf("Preflight 里没再用 %s —— 两侧各写一份字面量就会走散", c)
		}
	}
	admin := read("admin.go")
	for _, c := range []string{"classRef2V", "classI2V", "classSigmaShift", "classCondSave", "classMotionCtx"} {
		if !strings.Contains(admin, c) {
			t.Errorf("管理页清单里没引用常量 %s（改用字面量后就与 Preflight 脱钩了）", c)
		}
	}
}
