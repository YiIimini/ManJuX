package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 定妆照的发型纪律闸门(2026-09-25 用户报「19 镜后面主角的头发飘逸了」)。
//
// 事故链:角色卡写"短低马尾" → **定妆照自己给额前加了几缕碎发** → 按"参考图是唯一权威"
// 的规矩,H3 忠实复现那几缕 → 一进动作段被放大成"头发变长变散、额前垂一大绺"。
// 所以根子在定妆照,而定妆照的根子在"简报没说额前状态"。这一条同时钉住束法/长度/额前
// 三件事,并明确"简报没说就别自造"。
func TestPortraitHairClausePinsTieLengthForehead(t *testing.T) {
	got := PortraitHairClause(KindHuman)
	for _, want := range []string{
		"HAIR EXACTLY AS BRIEFED",
		"the same TIE",
		"the same LENGTH",
		"the same FOREHEAD STATE",
		"do not invent extra strands",
		"do not let hair fall across the eyes",
		"never lengthen the hair",
		"额前状态",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("发型纪律缺少 %q:\n%s", want, got)
		}
	}
	if c := PortraitHairClause(KindChibi); !strings.Contains(c, "HAIR EXACTLY AS BRIEFED") {
		t.Error("Q 版也有人物头发,同样要注入")
	}
	for _, k := range []EntityKind{KindObject, KindIncorporeal} {
		if c := PortraitHairClause(k); c != "" {
			t.Errorf("%s 没有发型可谈,不该注入:\n%s", EntityKindLabel(k), c)
		}
	}
}

// TestPortraitPromptCarriesHairClause 装配点必须真的带上它(人类角色,项目内与资产库两条路)。
func TestPortraitPromptCarriesHairClause(t *testing.T) {
	plan := &project.Plan{Style: "国漫二维动画"}
	ch := project.Character{
		ID: "c1", Name: "顾青禾", Kind: "human",
		Description: "26岁女性送单员。窄长脸,麦色皮肤。短低马尾用旧橡皮筋扎着。",
		ImagePrompt: "26-year-old Chinese woman, narrow long face, short low ponytail, " +
			"faded grey-blue courier jacket, plain background, upper-body framing, front-facing",
	}
	p := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: ch.Name, Description: ch.Description,
		ImagePrompt: ch.ImagePrompt, FacesOn: true,
	}, plan)
	if !strings.Contains(p, "HAIR EXACTLY AS BRIEFED") {
		t.Errorf("项目内角色卡的定妆照装配里缺少发型纪律:\n%s", p)
	}
	// 资产库那条路(无项目上下文)同样要带
	if libP := LibraryPortraitPrompt(ch.Name, ch.Description, ch.ImagePrompt); !strings.Contains(libP, "HAIR EXACTLY AS BRIEFED") {
		t.Errorf("资产库定妆照装配里缺少发型纪律:\n%s", libP)
	}
	// 器物不该被塞进来
	obj := LibraryPortraitPrompt("铜雀", "一只插画铜雀", "an ornate bronze sparrow, plain background")
	if strings.Contains(obj, "HAIR EXACTLY AS BRIEFED") {
		t.Error("器物定妆照不该出现发型纪律")
	}
}

// TestHairClauseIsIdempotent 幂等:定妆照提示词可能被多条路径(重建描述 / 重抽 / 库复用)
// 反复经过装配,不许叠成两份。
func TestHairClauseIsIdempotent(t *testing.T) {
	plan := &project.Plan{Style: "国漫二维动画"}
	s := PortraitSubject{Kind: KindHuman, Name: "顾青禾", FacesOn: true,
		ImagePrompt: "a young woman with a short low ponytail, plain background, upper-body framing, front-facing"}
	once := PortraitPrompt(s, plan)
	twice := PortraitPrompt(PortraitSubject{Kind: KindHuman, Name: s.Name, FacesOn: true, ImagePrompt: once}, plan)
	if n := strings.Count(twice, "HAIR EXACTLY AS BRIEFED"); n != 1 {
		t.Errorf("发型纪律必须只出现一次,实际 %d 次", n)
	}
}
