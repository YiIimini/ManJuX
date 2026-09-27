package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// prop_asset_test.go 道具图**从"读进来"到"真的出图"**的契约（2026-09-27 补的缺口）。
//
// 缺口的形状值得记下来：技能侧交付了角色/场景/道具三类卡，平台契约把 `props` 收下来了
// （`project.Prop` 注释："字段收下来才算真读到了"），资产库里早有 `props` 类目、上传端点
// 接受"道具图"、连 `assetImageSize` 的注释都写着"定妆照／场景图／**道具图**" ——
// **唯独资产阶段的 check() 只遍历了角色与场景**。于是一整类产出只躺在计划里给人看。
//
// 这条测试钉两半：① 装配点本身（白底单件 + 不画字 + 风格块 + 幂等 + 兜底）；
// ② 接线（两条资产路径都必须遍历 plan.Props —— 只看装配点会漏掉"没人调它"）。

func TestPropImagePromptAssembly(t *testing.T) {
	proj := &project.Project{Config: project.ProjectConfig{Params: project.DefaultStageParams()}}
	plan := &project.Plan{Style: "GUOFENG ANIMATED-FEATURE ART DIRECTION (period Chinese): a photoreal 3D surface."}
	pr := project.Prop{
		ID: "prop_01", Name: "沈砚的伞", EN: "an oiled paper umbrella",
		Description: "旧伞，竹骨，油纸面",
		ImagePrompt: "An oiled paper umbrella, bamboo ribs, worn paper canopy, upper-body framing, plain background.",
	}
	got := PropImagePrompt(proj, plan, pr)

	// ① 取景必须被换成"1:1 白底单件"：抄来的人像取景（upper-body framing / plain background）
	//    留着就会画成"人拿着伞"或"半身构图"，道具图的可比性直接没了。
	low := strings.ToLower(got)
	for _, bad := range []string{"upper-body framing"} {
		if strings.Contains(low, bad) {
			t.Errorf("道具图里残留人像取景 %q：\n%s", bad, got)
		}
	}
	for _, want := range []string{"1:1", "pure white", "no text", "no letters", "Single object only"} {
		if !strings.Contains(got, want) {
			t.Errorf("道具图缺 %q（白底单件/不画字是可比性与「不成乱码字形」的前提）：\n%s", want, got)
		}
	}
	// ② 风格块必须注入（与角色/场景同一套全片风格，否则道具自成一套画风）
	if !strings.Contains(got, "GUOFENG") {
		t.Errorf("道具图没吃到全片风格块：\n%s", got)
	}
	// ③ 幂等：装配成品会被写回卡片，第二次进来必须原样返回
	if again := PropImagePrompt(proj, plan, project.Prop{ImagePrompt: got}); again != got {
		t.Errorf("二次装配改变了结果：\n一次：%s\n二次：%s", got, again)
	}
	// ④ 兜底：老包的道具卡只有 en（没有 image_prompt）也要能出图
	fallback := PropImagePrompt(proj, plan, project.Prop{ID: "p2", Name: "扁担", EN: "a well-worn carrying pole"})
	if !strings.Contains(fallback, "carrying pole") || !strings.Contains(fallback, "pure white") {
		t.Errorf("只有 en 的存量道具卡也应能装配出可用提示词：\n%s", fallback)
	}
	// ⑤ 汉字必须清掉（T2I 会把汉字画成乱码字形）
	if han := PropImagePrompt(proj, plan, project.Prop{ImagePrompt: "一把旧伞 umbrella"}); strings.Contains(han, "旧伞") {
		t.Errorf("道具提示词里的汉字应被清掉：\n%s", han)
	}
}

// TestAssetStageIteratesProps 接线层：两条资产路径都必须遍历 plan.Props。
//
// 为什么用源码扫描而不是跑一遍资产阶段：资产阶段要真引擎（生成图），而这里要钉的是
// **"有没有这条循环"** —— 缺口恰恰就是"装配点写好了但没人调用"，跑真引擎反而更慢更脆
// （与 accel_refine_test.go / portrait_assembly_test.go 的接线扫描同一手法）。
func TestAssetStageIteratesProps(t *testing.T) {
	raw, err := os.ReadFile("stages_assets.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if n := strings.Count(src, "for _, pr := range plan.Props"); n < 2 {
		t.Errorf("runAssets 与 backfillAssets 都要遍历 plan.Props，实际 %d 处（只补一处会出现「初出有道具图、补缺没有」）", n)
	}
	if !strings.Contains(src, `check("props", "prop:"+pr.ID`) {
		t.Error("道具必须走与角色/场景同一个 check 入口（否则它不进资产清单、也不进库）")
	}
	if !strings.Contains(src, "PropImagePrompt(proj, plan, pr)") {
		t.Error("道具的提示词必须走唯一装配点 PropImagePrompt（装配守卫也盯着这条）")
	}
	// 范围那半：道具目前不做取镜范围限制（镜头里还没有道具引用），理由写在 asset_scope.go
	scope, err := os.ReadFile("asset_scope.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scope), "func (n *assetNeed) wantsProp") {
		t.Error("assetNeed 要有 wantsProp（`prop_ids` 到位后范围限制就落在它身上）")
	}
}
