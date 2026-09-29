package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 资产库必须**诚实标出非人条目**（2026-09-29 用户问：「『归墟环·维护舱主控语音』是角色？」）。
//
// `characters` 这个分类按**存储口径**收"一切有个体的东西"——里面混着器物 / 灵兽 / 妖兽 /
// 无实体（用户那一集的「维护舱主控语音」就是舱顶一块扬声器圆盘）。不标出来，用户点开
// 资产库只会以为那是一张人脸卡。
//
// 这条测试钉三件事：
//
//	① 卡片上要有这个标签（读条目里**平台判定的** `entity_kind`，不是从名字猜的）；
//	② 人（human）**不标** —— 那是绝大多数，标出来只是噪音；
//	③ 落库那一半必须接上：资产阶段生成角色时要把生效类型写进库条目
//	   （否则标签永远拿不到值，也就等于没做）。
func TestLibraryShowsEntityKindForNonHumanCards(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "views", "LibraryView.vue"))
	if err != nil {
		t.Fatalf("读 LibraryView.vue 失败：%v", err)
	}
	src := string(raw)
	for _, want := range []struct{ needle, why string }{
		{"ENTITY_KIND_LABEL", "非人条目要有中文短标签（器物/灵兽/妖兽/无实体/Q 版）"},
		{"entity_kind", "标签判据必须取条目里平台判定的生效类型，不许从名字猜"},
		{"entityKindLabel(row)", "卡片上要真的渲染这个标签"},
		{"ENTITY_KIND_HINT", "标签要有 tooltip 说明（用户问的就是「这算角色吗」）"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("LibraryView.vue 缺少 %q —— %s", want.needle, want.why)
		}
	}
	// ② human 不进标签表（标出来是噪音）
	if strings.Contains(src, "human: '人'") || strings.Contains(src, "human:'人'") {
		t.Error("human 不该出现在标签表里（绝大多数条目都是人，标出来只是噪音）")
	}

	// ③ 落库接线：资产阶段生成角色后要把生效类型写进库条目
	stage, err := os.ReadFile(filepath.Join("..", "pipeline", "stages_assets.go"))
	if err != nil {
		t.Fatalf("读 stages_assets.go 失败：%v", err)
	}
	if !strings.Contains(string(stage), "SetEntityKind(kind, name, string(ek))") {
		t.Error("资产阶段没有把生效实体类型落库（SetEntityKind）—— 界面那个标签就永远是空的")
	}
}
