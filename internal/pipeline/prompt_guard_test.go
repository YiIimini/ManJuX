package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 写实风格在**视频侧**也必须有一道守卫(审计:视频侧 RenderLook 没做 photoreal 判断)。
//
// 上游点名写实/3D 时,styleAndMediumBlock 正在写「以上风格由剧集设定指定,照此渲染」+
// STYLE AUTHORITY;而 RenderLook 是"手绘线稿脸 / 赛璐璐平涂 / 哑光"——同段提示词里两个
// 介质打架,模型只会二选一地糊(逐镜摇摆、跨集画风不一致),而定妆照按 styleExpand 的
// photoreal 分支画成写实,又回到"参考图与成片不在同一个视觉世界"的老根因。
// 图像侧早有这道守卫(styleExpand),视频侧漏了 —— 而视频侧才真正出像素。
func TestVideoPromptDropsAnimatedRenderLookWhenStyleIsPhotoreal(t *testing.T) {
	chars := []project.Character{{
		ID: "c1", Name: "沈砚", Kind: "human",
		Description: "十八九岁的少年,清瘦长脸,细长单眼皮",
	}}
	shot := project.Shot{ID: 1, Characters: []string{"c1"}, Scene: "s1", H3Prompt: "shot body"}

	animated := &project.Plan{Style: "国漫二维动画,赛璐璐平涂", Characters: chars}
	gotAnimated := anchorPrompt(animated, &shot, true)
	if !strings.Contains(gotAnimated, "hand-drawn line-art face") {
		t.Errorf("动漫化项目必须保留人物渲染条款:\n%s", gotAnimated)
	}

	photoreal := &project.Plan{
		Style:      "PHOTOREAL 3D CINEMATIC WORLD, SEMI-STYLISED ANIME-ADJACENT FACES, matte finish",
		Characters: chars,
	}
	gotPhotoreal := anchorPrompt(photoreal, &shot, true)
	if strings.Contains(gotPhotoreal, "hand-drawn line-art face") {
		t.Errorf("上游点名写实/3D 时不得再要求「手绘线稿脸」(同段提示词两个介质打架):\n%s", gotPhotoreal)
	}
	// 风格段与 STYLE AUTHORITY 仍在(风格归上游),类型形态条款也不受影响
	if !strings.Contains(gotPhotoreal, "STYLE AUTHORITY") {
		t.Error("写实风格下仍要保留风格权威段")
	}
	// 关掉动漫化开关时本来就不加(旧行为不变)
	if got := anchorPrompt(photoreal, &shot, false); strings.Contains(got, "hand-drawn line-art face") {
		t.Error("facesOn=false 时不该出现动画人脸条款")
	}
	// 否定式写法("NO PHOTOREAL DETAIL")不算要写实 —— 否则会把技能端指定的画风术语丢掉
	negated := &project.Plan{Style: "国漫二维动画, NO PHOTOREAL DETAIL, MATTE POWDERY SKIN", Characters: chars}
	if !strings.Contains(anchorPrompt(negated, &shot, true), "hand-drawn line-art face") {
		t.Error("风格串里的否定式写实字样不该触发守卫(那是纯误报)")
	}
}

// 方案无风格 + 关掉动漫化时,定妆照**不能丢身份忠实度块**。
//
// SheetIdentity(按文字画、不得美化/放大眼睛/柔化下颌/变年轻)与画风无关,而且是它
// 全项目唯一的注入点。旧实现在 styleExpand 开头直接 `return ""`,于是"真人剧 + 方案
// 无风格"这一组合下,定妆照提示词里一条身份约束都没有 —— 定妆照是全片身份的权威,
// 它一偏,成片与角色卡就永远对不上,而且提示词里没有任何痕迹可查。
func TestStyleExpandKeepsIdentityFidelityWithoutStyle(t *testing.T) {
	prof := EntityProfileOf(KindHuman)
	got := styleExpand("", false, KindHuman)
	if !strings.Contains(got, prof.SheetIdentity) {
		t.Fatalf("无风格 + 关闭动漫化时必须保留身份忠实度块,实际:\n%q", got)
	}
	if !strings.Contains(got, "IDENTITY FIDELITY") {
		t.Errorf("身份忠实度块的内容也要在,实际:\n%s", got)
	}
	// 但介质/画风块(SheetLook)确实不该加 —— 那是"动漫化"这一半的职责
	if strings.Contains(got, prof.SheetLook) && strings.TrimSpace(prof.SheetLook) != "" {
		t.Errorf("关闭动漫化时不该注入介质画风块:\n%s", got)
	}
	// 有风格时不受影响(两端都在)
	withStyle := styleExpand("国漫二维动画", false, KindHuman)
	if !strings.Contains(withStyle, "RENDER STYLE") {
		t.Errorf("有风格时仍要写渲染风格段:\n%s", withStyle)
	}
	// 器物也要拿到自己的忠实度块(没有风格时同样不许丢)
	if o := styleExpand("", false, KindObject); !strings.Contains(o, "OBJECT FIDELITY") {
		t.Errorf("器物无风格时同样要保留结构忠实度块:\n%s", o)
	}
}

// 【外貌不变项】必须按实体类型分发(审计:给铜雀/灵体也写发型与服装层数)。
//
// 那段写的是"发型与发长、服装层数"——人形词汇。器物镜头里同时出现
// 「ARTEFACT CLAUSE: no human face …」与「头发不得随时间变长」,等于把主体往人形上拽
// (正是 2026-09-24「阿凿是一只铜雀却渲染成人物」的同一类)。
func TestAppearanceLockClauseIsKindDispatched(t *testing.T) {
	shot := project.Shot{ID: 1, Characters: []string{"c1", "c2"}, H3Prompt: "body"}
	mixed := &project.Plan{
		Style: "国漫二维动画",
		Characters: []project.Character{
			{ID: "c1", Name: "沈砚", Kind: "human", Description: "十八九岁的少年,短低马尾"},
			{ID: "c2", Name: "阿凿", Kind: "object", Description: "一只插画铜雀,铆钉与玻璃眼"},
		},
	}
	got := anchorPrompt(mixed, &shot, true)
	if !strings.Contains(got, "外貌不变项") {
		t.Error("有真人角色的镜头仍要注入外貌不变项")
	}
	if !strings.Contains(got, "ARTEFACT CLAUSE") {
		t.Error("器物条款必须仍在")
	}

	// 纯器物镜头:不该出现任何"头发/服装层数"的人形措辞
	objShot := project.Shot{ID: 2, Characters: []string{"c2"}, H3Prompt: "body"}
	objOnly := anchorPrompt(mixed, &objShot, true)
	if strings.Contains(objOnly, "外貌不变项") {
		t.Errorf("纯器物镜头不得注入人形外貌不变项:\n%s", objOnly)
	}
	for _, bad := range []string{"发型与**发长**", "服装层数", "头发不得随时间变长"} {
		if strings.Contains(objOnly, bad) {
			t.Errorf("纯器物镜头的提示词里出现了人形措辞 %q —— 会把器物往人形上拽:\n%s", bad, objOnly)
		}
	}

	// 唯一无实体镜头同理
	inc := &project.Plan{
		Style: "国漫二维动画",
		Characters: []project.Character{
			{ID: "c3", Name: "系统音", Kind: "incorporeal", Description: "一团半透明的微光形体"},
		},
	}
	gotInc := anchorPrompt(inc, &project.Shot{ID: 3, Characters: []string{"c3"}, H3Prompt: "body"}, true)
	if strings.Contains(gotInc, "外貌不变项") {
		t.Errorf("无实体镜头不该有发型/服装条款:\n%s", gotInc)
	}
}
