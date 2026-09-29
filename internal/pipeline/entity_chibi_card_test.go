package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 「碑生·Q版（肩上巴掌大的自己）」必须按 **Q 版**那套渲染（2026-09-29 用户报障：
// 「咋不是使用Q版函数方法进行渲染？还是用的默认渲染」）。
//
// 真机现场：那张卡的 `kind` 字段被模型写成了 **human**，id 是 `char_bisheng_q`
// （既不是 `chibi_` 前缀也不是 `_chibi` 后缀）⇒ 显式字段一路采信 ⇒ 整张卡按**人**渲染
// （成人比例、八头身、人脸身份锁），Q 版画出来就是"另一个成年人"。
//
// 判据三条任一命中即 Q 版：kind 写 chibi / id 是 chibi 前后缀 / **名字里写着 Q 版**。
func TestChibiCardIsRenderedAsChibiEvenWhenKindSaysHuman(t *testing.T) {
	real := project.Character{
		ID: "char_bisheng_q", Name: "碑生·Q版（肩上巴掌大的自己）", Kind: "human",
		Description: "碑生的 Q 版小人，2.5–3 头身，巴掌大，总蹲在碑生左肩上；脸按碑生五官简化：瘦长脸缩成圆润的 Q 版轮廓",
	}
	if got := ResolveEntityKind(real); got != KindChibi {
		t.Fatalf("真机那张 Q 版卡被判成了 %q（%s）—— 会按成人渲染", got, EntityKindLabel(got))
	}
	if !IsChibiCharacter(real) {
		t.Error("IsChibiCharacter 没认出它")
	}
	// 三种写法都要认
	for _, c := range []project.Character{
		{ID: "chibi_char_bisheng", Name: "碑生·Q版"},
		{ID: "char_bisheng_chibi", Name: "碑生"},
		{ID: "char_x", Name: "碑生", Kind: "chibi"},
	} {
		if got := ResolveEntityKind(c); got != KindChibi {
			t.Errorf("%s/%s 没被判成 Q 版，实得 %q", c.ID, c.Name, got)
		}
	}
	// **反例**：普通角色不许被误判成 Q 版（名字里带"Q"字也算普通人）
	for _, c := range []project.Character{
		{ID: "char_bisheng", Name: "碑生（丙十七）", Kind: "human", Description: "二十出头，瘦高，约七头半身"},
		{ID: "char_aqiang", Name: "阿Q", Kind: "human", Description: "三十来岁的车夫，方脸，络腮胡"},
		{ID: "char_mohui", Name: "墨洄", Kind: "human", Description: "二十八九岁核验官，长菱形脸"},
	} {
		if got := ResolveEntityKind(c); got == KindChibi {
			t.Errorf("普通人「%s」被误判成 Q 版", c.Name)
		}
	}
	// 渲染方法也确实换了一套：Q 版比例条款（二到三头身）在，人那一套不在
	prompt := PortraitPrompt(PortraitSubject{
		Kind: ResolveEntityKind(real), Name: real.Name, Description: real.Description,
		ImagePrompt: "A chibi version of the lead, 2.5 heads tall", FacesOn: true, FrameW: 768, FrameH: 1344,
	}, &project.Plan{Style: "guofeng_cinematic"})
	if !strings.Contains(prompt, EntityProfileOf(KindChibi).SheetMarker) {
		t.Error("Q 版卡没有走 Q 版那套定妆照方法（标记不在提示词里）")
	}
}
