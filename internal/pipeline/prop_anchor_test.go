package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// prop_anchor_test.go 关键道具的**跨镜文字锚定**（治漂移的那一半）。
//
// 官方规则是 Props never get a picture（道具不进参考图集合），一致性只能靠文字 ⇒
// 渲染前必须把该镜引用到的器物卡原文**逐字**锚进提示词；不锚，模型每镜自己发明一件。
func TestEnsurePropAnchors(t *testing.T) {
	plan := &project.Plan{Props: []project.Prop{
		{ID: "prop_blade", Name: "碑刀", EN: "a thin worn stone-cutting blade with a hemp-wrapped handle",
			Description: "磨薄的旧碑石刀，柄缠麻布，刃口沁朱砂", ImagePrompt: "x"},
		{ID: "prop_ledger", Name: "名册板", EN: "a palm-sized wooden ledger board", Description: "x", ImagePrompt: "y"},
	}}
	shot := project.Shot{ID: 5, PropIDs: []string{"prop_blade"}}
	got := EnsurePropAnchors("CUT: detailed_description...", plan, shot)
	if !strings.Contains(got, propAnchorMarker) {
		t.Fatal("应追加锚定块")
	}
	if !strings.Contains(got, "碑刀") || !strings.Contains(got, "hemp-wrapped handle") {
		t.Errorf("锚定文本要用卡片原文（中文名 + 英文外观短语）：\n%s", got)
	}
	if strings.Contains(got, "名册板") {
		t.Error("没点名的道具不许塞进来（否则每镜堆满全片道具）")
	}
	// 幂等
	if again := EnsurePropAnchors(got, plan, shot); again != got {
		t.Errorf("重复调用不该重复追加：\n%s", again)
	}
	// 没带 prop_ids ⇒ 原样放行
	if raw := "x"; EnsurePropAnchors(raw, plan, project.Shot{ID: 6}) != raw {
		t.Error("没有 prop_ids 的镜不该被改动")
	}
	// 卡片缺失 ⇒ 原样放行（留给契约门报，不在这里猜）
	if raw := "x"; EnsurePropAnchors(raw, plan, project.Shot{ID: 7, PropIDs: []string{"prop_nope"}}) != raw {
		t.Error("卡片不存在时不该伪造锚定")
	}
}

// TestRenderAssemblesPropAnchors 接线：渲染装配点真的调用它（门写了不接线＝没写）。
//
// 【2026-10-06 调整】装配链现在收在**唯一入口** `assembleShotPrompt` 里（降级阶梯的
// "缩短到 6 秒再出"那一级此前是从裸正文另拼提示词、把这一层整块丢掉的）。判据跟着落到
// 那一处，并额外钉住"渲染主循环真的调用这个入口" —— 光有入口不算接上。
func TestRenderAssemblesPropAnchors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("stages_render.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []struct{ needle, why string }{
		{"EnsurePropAnchors(prompt, plan, *shot)", "渲染阶段要把器物卡原文锚进每镜提示词 —— 这是道具跨镜一致的唯一机制"},
		{"func assembleShotPrompt(", "四步装配必须收在一个入口里（缩短重出那一级要复用它）"},
		{"prompt := assembleShotPrompt(proj, plan, &shot, roles, prevShot)", "渲染主循环必须真的调用装配入口（有入口没接上＝没写）"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("装配链缺 %q —— %s", want.needle, want.why)
		}
	}
}
