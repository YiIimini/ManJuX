package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 定妆照取景对齐:只剥**全身口径**,脸部比例全留。
//
// 2026-09-26 用户报「渲染出来的角色比例那么奇怪」的根因之一:技能侧的定妆卡里
// 取景(`upper-body framing`)与比例(`seven and a half heads`)自相矛盾,模型选了
// "按比例画全身" → 定妆照是全身立绘,而 64 个镜头里 61 个是近景/中景/特写。
// 这条闸门钉住:整身那几条被剥掉、脸的那几条一个字都不许动。
func TestStripFullBodyProportions(t *testing.T) {
	// 用技能侧真实卡片的原句(节选自《求你们骂我》沈砚·一品的 image_prompt)。
	real := "Proportions must be balanced and idealised: the eye line sits at the vertical middle of the head, " +
		"the two eyes are one eye-width apart, the nose is about one third of the face length, " +
		"the mouth about one fifth, the chin about two thirds of the cheekbone width, " +
		"the neck slim and long, the shoulders about two head-widths, height is seven and a half heads, " +
		"slim waist and long legs. Never distort these ratios, never lengthen the face and never enlarge the eyes. " +
		"plain pure white seamless background, upper-body framing, front-facing, no other people"

	got := StripFullBodyProportions(real)

	// ① 全身口径必须消失
	for _, gone := range []string{"seven and a half heads", "seven-and-a-half", "full body", "head-to-toe"} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(gone)) {
			t.Errorf("全身口径「%s」应被剥掉,实际仍在:\n%s", gone, got)
		}
	}
	// ② 脸部比例必须逐条保留(它们是近景/特写真正用得上的锚点)
	for _, keep := range []string{
		"eye line sits at the vertical middle", "one eye-width apart", "one third of the face length",
		"one fifth", "two thirds of the cheekbone width", "the neck slim and long",
		"the shoulders about two head-widths", "Never distort these ratios", "never enlarge the eyes",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("脸部比例「%s」不该被剥掉,实际:\n%s", keep, got)
		}
	}
	// ③ 取景与其余内容保持
	for _, keep := range []string{"upper-body framing", "front-facing", "pure white seamless background"} {
		if !strings.Contains(got, keep) {
			t.Errorf("取景/背景「%s」不该被动,实际:\n%s", keep, got)
		}
	}
	// ④ 不留标点残迹
	for _, bad := range []string{", ,", ",,", " ,", ", and ,", "  "} {
		if strings.Contains(got, bad) {
			t.Errorf("剥短语后留下标点残迹 %q:\n%s", bad, got)
		}
	}
	// ⑤ 幂等
	if again := StripFullBodyProportions(got); again != got {
		t.Errorf("应幂等:\n首次:%s\n再次:%s", got, again)
	}
}

// TestStripFullBodyProportionsVariants 各种写法(大小写/连字符)都要认。
//
// 期望值是**整项消失**(不是"剩半句"):`height is` 这种残留是平台往技能侧口径里塞词,
// 见文件头"为什么是整项删除"。
func TestStripFullBodyProportionsVariants(t *testing.T) {
	cases := map[string]string{
		"height is seven and a half heads":  "",
		"height is Seven And A Half Heads":  "",
		"a full-body shot":                  "",
		"shown FULL BODY here":              "",
		"framed head to toe":                "",
		"framed head-to-toe":                "",
		"the whole body is visible":         "",
		"keep the eyes one eye-width apart": "keep the eyes one eye-width apart", // 不碰
		"proportions must be balanced":      "proportions must be balanced",      // 通用词不碰
		"a full-length robe":                "a full-length robe",                // 服装长度不是取景
	}
	for in, want := range cases {
		if got := StripFullBodyProportions(in); got != want {
			t.Errorf("StripFullBodyProportions(%q) = %q,期望 %q", in, got, want)
		}
	}
}

// TestStripFullBodyKeepsWhateverSharesTheItem 防误伤:整身比例与脸部锚点写在**同一个
// 逗号项**里时,只剥片语、不删整项 —— 删掉脸部锚点比留下残迹严重得多。
func TestStripFullBodyKeepsWhateverSharesTheItem(t *testing.T) {
	in := "the shoulders are two head-widths wide and the height is seven and a half heads"
	got := StripFullBodyProportions(in)
	if strings.Contains(strings.ToLower(got), "seven and a half heads") {
		t.Errorf("整身片语必须剥掉:\n%s", got)
	}
	if !strings.Contains(got, "the shoulders are two head-widths wide") {
		t.Errorf("同一项里的脸部/肩部锚点不许被牵连:\n%s", got)
	}
}

// TestStripFullBodyKeepsLineStructure 只压行内空白,不压换行 ——
// 提示词是多段装配的产物,段间空行是结构;压平会让第二次装配与第一次不同。
func TestStripFullBodyKeepsLineStructure(t *testing.T) {
	in := "a boy in a grey robe\n\nRENDER STYLE (mandatory): donghua\n\nheight is seven and a half heads\n\nPOSE: calm"
	got := StripFullBodyProportions(in)
	if !strings.Contains(got, "\n\n") {
		t.Errorf("段间空行是装配结构,不许被压平:\n%q", got)
	}
	if strings.Contains(strings.ToLower(got), "seven and a half heads") {
		t.Errorf("整身口径仍应剥掉:\n%q", got)
	}
	if again := StripFullBodyProportions(got); again != got {
		t.Errorf("应幂等:\n首次:%q\n再次:%q", got, again)
	}
}

// TestPortraitPromptStripsFullBodyForHumansOnly 接线:只有人类卡走这条剥离;
// 非人类型(器物/妖兽)的"整件完整入画"是它们自己的取景纪律,不许被误伤。
func TestPortraitPromptStripsFullBodyForHumansOnly(t *testing.T) {
	plan := &project.Plan{Title: "测试", Style: "LIVE-ACTION CINEMATIC PHOTOGRAPHY"}

	human := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "沈砚", FacesOn: true,
		ImagePrompt: "a lean teenage boy. Proportions: height is seven and a half heads, " +
			"the two eyes are one eye-width apart. upper-body framing.",
	}, plan)
	if strings.Contains(human, "seven and a half heads") {
		t.Error("人类定妆照不该保留全身口径")
	}
	if !strings.Contains(human, "one eye-width apart") {
		t.Error("人类定妆照必须保留脸部比例")
	}

	obj := PortraitPrompt(PortraitSubject{
		Kind: KindObject, Name: "铜雀", FacesOn: true,
		ImagePrompt: "a bronze sparrow lamp, the whole object complete and centred.",
	}, plan)
	if !strings.Contains(obj, "whole object") {
		t.Error("器物的『整件完整入画』是它自己的取景纪律,不该被剥掉")
	}
}

// TestPortraitFramingClauseByAspect 构图随**画幅**变 —— 它不是常量,是算出来的。
//
// 用户 2026-09-27 的批评:「变量应该是实时改变的,不要固定成常量啊」。定妆照的取景
// 正是这类东西:同一个"上半身",竖幅里装得下的是胸像、横幅里装得下的是头肩。
func TestPortraitFramingClauseByAspect(t *testing.T) {
	v := PortraitFramingClause(KindHuman, 768, 1344)
	for _, want := range []string{"chest-up portrait", "the WHOLE head is inside the picture", "clear empty space above the hair"} {
		if !strings.Contains(v, want) {
			t.Errorf("竖幅定妆取景缺「%s」:%s", want, v)
		}
	}
	h := PortraitFramingClause(KindHuman, 1344, 768)
	if !strings.Contains(h, "head-and-shoulders") {
		t.Errorf("横幅应走头肩取景:%s", h)
	}
	if strings.Contains(h, "chest-up") {
		t.Errorf("横幅不该要胸像(那是竖幅的构图):%s", h)
	}
	for _, k := range []EntityKind{KindObject, KindBeast, KindScene} {
		if got := PortraitFramingClause(k, 768, 1344); got != "" {
			t.Errorf("%v 不该套人像构图纪律:%s", k, got)
		}
	}
	low := strings.ToLower(v + " " + h)
	for _, bad := range []string{" no ", " not ", "never ", "without ", "avoid "} {
		if strings.Contains(low, bad) {
			t.Errorf("构图纪律不得含否定词「%s」(Krea-2 会把名词当指令画出来):%s", bad, low)
		}
	}
}

// TestPortraitPromptFollowsProjectAspect 端到端:装配点真的用上了**项目画幅**。
func TestPortraitPromptFollowsProjectAspect(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国风电影级"}
	ch := project.Character{ID: "c1", Name: "沈砚", ImagePrompt: "a lean young man"}
	if got := PortraitPromptForProject(proj, plan, ch); !strings.Contains(got, "chest-up portrait") {
		t.Errorf("默认竖幅应给胸像构图:\n%s", got)
	}
	proj.Config.Params.Render.Width, proj.Config.Params.Render.Height = 1344, 768
	if got := PortraitPromptForProject(proj, plan, ch); !strings.Contains(got, "head-and-shoulders") {
		t.Errorf("改成横幅后构图应跟着变(取景是算出来的,不是常量):\n%s", got)
	}
}
