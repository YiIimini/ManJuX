package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// TestAnchorPinsFragileAppearanceItems 外貌不变项(2026-09-25 用户报「19 镜后面主角的
// 头发飘逸了」)。
//
// 实测事实:第01集 26 镜**每一镜的提示词都写着短低马尾/黄铜扣/反光条**,而渲染出来的
// 19/20/22 镜:头发变长变散、额前垂一大绺、马尾上的黄铜扣不见、夹克下的背心被简化成内搭。
// 文字是对的、画面漂了 —— 而且是"画面一忙就丢装饰"的漂法,且 2 张参考图与 3 张参考图的
// 镜头都漂(与拼合图无关)。所以从措辞上把外貌从"形容词"提升为"身份事实"。
//
// 这三条分别对应真实出现过的三种漂移:头发变长(发长)、配件消失(黄铜扣/腕环)、
// 层数被简化(背心→内搭)。
func TestAnchorPinsFragileAppearanceItems(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	proj.Config.Params.Script.StylizedFaces = nil // 未表态 = 动漫化(默认路径)
	plan := &project.Plan{
		Style: "国漫二维动画",
		Characters: []project.Character{{
			ID: "c1", Name: "顾青禾", Kind: "human",
			Description: "26岁女性送单员。窄长脸,麦色皮肤。短低马尾用旧橡皮筋扎着,皮筋外套一枚拇指盖大黄铜扣。" +
				"穿褪色灰蓝送单夹克,配反光条。",
			Voice: "young woman, low and steady",
		}},
	}
	shot := &project.Shot{ID: 20, Mode: "ref2va", Duration: 6, ShotSize: "中景",
		Characters: []string{"c1"},
		H3Prompt:   "detailed_description:\n[Shot 1] she lifts the crate.\n"}

	got := ShotRenderPrompt(proj, plan, shot)
	for _, want := range []string{
		"【外貌不变项",
		"发长",
		"不得随时间变长、变散、变成飘动的长发",
		"不许因为画面太满就拿掉某一件",
		"服装层数",
		"不许简化成一件",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("身份锚定缺少外貌不变项(%q):\n%s", want, got)
		}
	}
	// 原文不能被挤掉:角色描述与声线都还要在
	if !strings.Contains(got, "黄铜扣") {
		t.Error("角色卡描述被挤掉了")
	}
	if !strings.Contains(got, "逐字照抄") {
		t.Error("声线锚定被挤掉了")
	}
	// 幂等:同一条提示词过两遍不该叠成两份
	if n := strings.Count(ShotRenderPrompt(proj, plan, shot), "【外貌不变项"); n != 1 {
		t.Errorf("外貌不变项必须只出现一次,实际 %d 次", n)
	}
	// 无角色(纯空镜)时不该硬塞:没有角色就没有"外貌"可锁
	empty := &project.Shot{ID: 1, Mode: "t2va", H3Prompt: "integrated_multimodal_description:\n[Shot 1] an empty street."}
	if strings.Contains(ShotRenderPrompt(proj, plan, empty), "【外貌不变项") {
		t.Error("没有出场角色的镜头不该注入外貌不变项")
	}
}
