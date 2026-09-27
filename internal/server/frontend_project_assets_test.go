package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frontend_project_assets_test.go 项目工作台的资产区必须**三类齐全**（角色 / 场景 / 道具）。
//
// 用户 2026-09-27 报障原话：「工作台中的资产也要展示"道具"」。
// 背景：平台侧那天才把"道具图"这条路补完（此前道具只被读进方案、从没出过图），
// 而工作台的资产页当时只有「角色定妆照」「场景参考图」两段 —— 生成出来的道具图没有入口给人看。
//
// 口径也要写在界面上（这条一并钉住）：道具图是**人审资产**（1:1 白底单件、核对跨镜一致性），
// **不进 H3 参考图集合**（官方 "Props never get a picture; describe props in text only"）。
// 不写清这句，用户会以为"这件道具没被用上"而去找 bug。
func TestProjectAssetsShowProps(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	raw, err := os.ReadFile(filepath.Join(root, "views", "ProjectView.vue"))
	if err != nil {
		t.Fatalf("读 ProjectView.vue 失败：%v", err)
	}
	src := string(raw)

	// ① 资产 id 口径：`prop:<id>`（与 `char:` / `scene:` 同一套；后端资产表就是按这个键存的）
	if !strings.Contains(src, "assets.value['prop:' + p.id]") {
		t.Error("道具要以 `prop:<id>` 取资产（与 char:/scene: 同一套命名，别自创键）")
	}
	// ② 上传/删除也要走同一套入口（后端 handlers_asset.go 的 kind 白名单里有 props）
	if !strings.Contains(src, "upload('props', 'prop:'+p.id, p.name)") {
		t.Error("道具图要能上传（kind 传 'props'）——后端那条白名单里本来就有 props")
	}
	if !strings.Contains(src, "removeAsset('prop:'+p.id)") {
		t.Error("道具图要能删除（removeAsset 走同一套库联动）")
	}
	// ③ 三类都在同一个「资产」页里（不是另开一个入口）
	if !strings.Contains(src, `name="assets"`) || !strings.Contains(src, "propsWithAsset") {
		t.Error("道具段应和角色/场景同在「资产」页签里")
	}
	// ④ 口径必须写在界面上：不进参考图 + 白底单件
	for _, want := range []string{"不进 H3 参考图", "白底单件"} {
		if !strings.Contains(src, want) {
			t.Errorf("道具段要写明 %q —— 否则用户会以为这件道具没被用上", want)
		}
	}
	// ⑤ 空态也要有话说（老方案没有 props 卡时别是一片空白）
	if !strings.Contains(src, "没有道具卡") {
		t.Error("方案没有道具卡时要有空态说明（含「可以去资产库入库、同名自动复用」这条出路）")
	}
}

// TestPipelineAssetProgressCountsProps 管线进度条的资产分母必须算上道具。
//
// 这是 2026-09-27 **我自己引入的 bug**：资产阶段开始出道具图之后，`assets` 表里的键变多了
// （多了 `prop:*`），而进度条的分母仍写 `plan.characters.length + plan.scenes.length`
// —— 于是"资产"那一格会显示成 `7/5` 这种不可能的数，而阶段完成判据 `have >= total`
// 会在道具还没出图时就显示"已定妆"。
//
// 三处（完成判据 / 部分进度 / 进度标签）必须问同一个函数，所以断言两件事：
// ① 有那个共用函数；② 三处都在用它、没人再自己数 characters+scenes。
func TestPipelineAssetProgressCountsProps(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	raw, err := os.ReadFile(filepath.Join(root, "components", "PipelineBar.vue"))
	if err != nil {
		t.Fatalf("读 PipelineBar.vue 失败：%v", err)
	}
	src := string(raw)
	if !strings.Contains(src, "function assetCounts(") {
		t.Fatal("资产进度要有唯一的 assetCounts()（三处各写一遍必然漏一处）")
	}
	if n := strings.Count(src, "assetCounts(pj)"); n < 3 {
		t.Errorf("完成判据 / 部分进度 / 进度标签三处都要用 assetCounts()，实际只用了 %d 处", n)
	}
	if strings.Contains(src, "plan.characters.length + pj.plan.scenes.length") ||
		strings.Contains(src, "pj.plan.characters.length + pj.plan.scenes.length") {
		t.Error("仍有地方自己数 characters+scenes 当资产总数 —— 那会漏掉道具，显示成 N/M 里 M 偏小")
	}
	if !strings.Contains(src, "(p.props || []).length") {
		t.Error("assetCounts 必须把道具算进总数（资产阶段现在会出道具图）")
	}
}
