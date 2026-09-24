package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// TestAnimatedFaceImageBlockIsPositiveOnly Krea-2 侧的画风块**不许出现否定词与
// "照片/毛孔"类名词**。
//
// 回归口径:蒸馏模型(CFG 1.0)对否定词免疫,却会把名词当指令画出来 —— 实测
// "no epaulettes" 直接画出肩章(见 uniformCivilianClarify 的记录)。所以定妆照这头
// 只能正面描述"它是什么";"不要油光/不要真人脸"这类排除句只能进 H3 那头。
func TestAnimatedFaceImageBlockIsPositiveOnly(t *testing.T) {
	b := strings.ToLower(animatedFaceImageBlock())
	for _, bad := range []string{"no ", "not ", "never ", "without ", "avoid ", "photo", "pore", "oily", "greasy", "plastic", "wax", "sweat"} {
		if strings.Contains(b, bad) {
			t.Errorf("Krea-2 画风块不应出现「%s」(否定词/照片类名词会被照着画):%s", bad, b)
		}
	}
	for _, want := range []string{"line art", "cel-shaded", "matte", "powdery", "original animation design"} {
		if !strings.Contains(b, want) {
			t.Errorf("Krea-2 画风块缺少正向画风词「%s」", want)
		}
	}
	// 绝不许写五官**形状**类词汇(2026-09-12 那次"风格块把所有角色画成圆脸大眼"的教训)
	for _, bad := range []string{"big eyes", "round face", "large eyes", "sharp chin"} {
		if strings.Contains(b, bad) {
			t.Errorf("画风块不得改写五官形状(%s)", bad)
		}
	}
}

// TestAnimatedFaceRenderBlockHitsGreaseAndRealFaces 「人物油腻」「真人脸」两条报障
// 在视频侧必须都有对应的硬约束,否则 H3 会把哑光定妆照重新画成油光真人脸。
func TestAnimatedFaceRenderBlockHitsGreaseAndRealFaces(t *testing.T) {
	b := animatedFaceRenderBlock()
	for _, want := range []string{
		"MATTE ANIMATED LOOK",
		"oily or greasy skin",
		"wet or sweaty highlights",
		"glossy plastic or wax finish",
		"photographic or 3D-scanned human face",
		"live-action actor's face",
		"resemblance to a real person",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("视频侧画风块缺少「%s」", want)
		}
	}
	// 必须点明"全片每一帧"都适用:只对首帧生效等于没生效
	if !strings.Contains(b, "every frame") {
		t.Error("画风块必须声明对全片每一帧生效")
	}
}

// TestStyleExpandOverridesPhotorealWhenFacesOn 角色动漫化开启时,真人/写实风格请求
// 必须被**覆盖**而不是叠加 —— 两组互相打架的风格词同时给模型,只会得到"既写实又扁平"的糊图。
func TestStyleExpandOverridesPhotorealWhenFacesOn(t *testing.T) {
	on := styleExpand("真人实拍质感 + 港片胶片", true)
	if strings.Contains(on, "photorealistic cinematic portrait") {
		t.Errorf("开启角色动漫化后不得再要求写实人像:%s", on)
	}
	if !strings.Contains(on, "ANIMATED MATTE CHARACTER") {
		t.Errorf("开启后必须带动画哑光画风块:%s", on)
	}
	// 用户自己的风格文本要保留(作为整体美术方向),只是不再驱动写实脸
	if !strings.Contains(on, "真人实拍质感") {
		t.Errorf("用户风格原文应保留:%s", on)
	}
	// 身份忠实度与"不许美化"的老约束不能被新块挤掉
	if !strings.Contains(on, "IDENTITY FIDELITY") {
		t.Error("身份忠实度约束丢失")
	}

	off := styleExpand("真人实拍质感", false)
	if !strings.Contains(off, "photorealistic cinematic portrait") {
		t.Errorf("关闭开关后真人风格是合法路线,不该被覆盖:%s", off)
	}
	if strings.Contains(off, "ANIMATED MATTE CHARACTER") {
		t.Errorf("关闭开关后不得注入动画画风块:%s", off)
	}
}

// TestStyleExpand3DNoLongerAsksForPlasticSkin 3D 分支原先写着 "smooth poreless
// synthetic skin"(光滑无孔合成皮肤)—— 无孔即塑料即皮脂高光,正是"人物油腻"的一半来源。
func TestStyleExpand3DNoLongerAsksForPlasticSkin(t *testing.T) {
	got := styleExpand("次世代 3D·BJD 虚拟人", false)
	if strings.Contains(got, "poreless") || strings.Contains(got, "synthetic skin") {
		t.Errorf("3D 分支不得再要求无孔合成皮肤(那是油腻的来源):%s", got)
	}
	if !strings.Contains(got, "matte powdery skin") {
		t.Errorf("3D 分支应改为哑光粉质肤色:%s", got)
	}
}

// TestPhotorealRequested 真人/写实关键词识别(用于把"风格与开关冲突"如实告知用户)。
func TestPhotorealRequested(t *testing.T) {
	for _, s := range []string{"真人实拍质感", "写实电影", "photorealistic", "live-action cinematography"} {
		if !photorealRequested(s) {
			t.Errorf("%q 应被判为真人/写实风格", s)
		}
	}
	for _, s := range []string{"国漫二维动画", "水墨国风", "CHINESE 2D ANIME STYLE"} {
		if photorealRequested(s) {
			t.Errorf("%q 不该被判为真人风格", s)
		}
	}
}

// TestCharacterImagePromptIsSingleAssemblyPoint 定妆照提示词只能有一个装配点:
// 四处来源(资产阶段/渲染前补齐/单资产重生成/Q 版)各写一份必然分叉,而它们画的是
// 同一个角色的脸 —— 分叉就是换脸。
func TestCharacterImagePromptIsSingleAssemblyPoint(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{
		Style: "国漫二维动画",
		Characters: []project.Character{
			{ID: "c1", Name: "张屠", Description: "圆胖脸,褐色短褂", ImagePrompt: "chubby middle-aged man, brown jacket"},
			{ID: "c2", Name: "卫东", Description: "清瘦少年", ImagePrompt: "slim young man, grey shirt"},
		},
	}
	got := characterImagePrompt(proj, plan, plan.Characters[0])
	if !strings.Contains(got, "chubby middle-aged man") {
		t.Error("原始 image_prompt 必须保留(身份特征前置)")
	}
	if !strings.Contains(got, "RENDER STYLE") || !strings.Contains(got, "CAST DISTINCTNESS") {
		t.Error("装配点必须同时带上风格块与同剧对比块")
	}
	if !strings.Contains(got, "ANIMATED MATTE CHARACTER") {
		t.Error("默认(未表态)应开启动画哑光画风")
	}
	// 关闭开关后不得带画风块
	proj.Config.Params.Script.StylizedFaces = project.Bool(false)
	if strings.Contains(characterImagePrompt(proj, plan, plan.Characters[0]), "ANIMATED MATTE CHARACTER") {
		t.Error("显式关闭后不得注入动画画风块")
	}
}

// TestPhotorealOverrideNotice 风格与开关冲突时必须能生成一句给用户看的话。
func TestPhotorealOverrideNotice(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "photorealistic cinematic"}
	if n := photorealOverrideNotice(proj, plan); !strings.Contains(n, "已按动漫化渲染") {
		t.Errorf("真人风格 + 开关开启应给出覆盖说明,实际:%q", n)
	}
	proj.Config.Params.Script.StylizedFaces = project.Bool(false)
	if n := photorealOverrideNotice(proj, plan); n != "" {
		t.Errorf("关闭开关后不应再告警(真人剧是合法路线),实际:%q", n)
	}
	proj.Config.Params.Script.StylizedFaces = project.Bool(true)
	if n := photorealOverrideNotice(proj, &project.Plan{Style: "国漫二维动画"}); n != "" {
		t.Errorf("动画风格不应触发覆盖告警,实际:%q", n)
	}
}

// TestShotRenderPromptIsSingleSourceForAllRenderPaths 四条真正出像素的路径
// (主渲染 / 云端 2K 定稿 / 局部重绘 / 音频驱动)必须拿到同一条提示词。
//
// 回归口径:这几条原先各自直接提交 shot.H3Prompt,画风与身份约束在它们身上全丢 ——
// 表现为"768p 是哑光动画脸,升到 2K 变成油光写实脸"、局部重绘在脸上补一块写实皮肤。
func TestShotRenderPromptIsSingleSourceForAllRenderPaths(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{
		Style:      "photorealistic cinematic, 35mm film",
		Characters: []project.Character{{ID: "c1", Name: "张屠", Description: "圆胖脸", ImagePrompt: "chubby man"}},
		Scenes:     []project.Scene{{ID: "s1", Name: "废件库", Description: "青砖大院"}},
	}
	shot := &project.Shot{ID: 1, Characters: []string{"c1"}, Scene: "s1",
		H3Prompt: "detailed_description:\n[Shot 1] ...\n<d>[Chinese]走。</d>"}
	got := ShotRenderPrompt(proj, plan, shot)
	if !strings.Contains(got, "MATTE ANIMATED LOOK") {
		t.Error("渲染期提示词必须带画风硬约束")
	}
	if !strings.Contains(got, "身份锁") {
		t.Error("渲染期提示词必须带身份锚定")
	}
	if strings.Contains(got, "【全片视觉风格】photorealistic cinematic") {
		t.Error("风格段与画风开关冲突时必须把写实风格段本身换掉,不能两条打架的指令一起发")
	}
	if !strings.Contains(got, "overridden") {
		t.Error("被覆盖的风格应留下可追溯的说明")
	}
	// 关掉开关:风格段原样保留(真人剧合法)
	proj.Config.Params.Script.StylizedFaces = project.Bool(false)
	off := ShotRenderPrompt(proj, plan, shot)
	if !strings.Contains(off, "【全片视觉风格】photorealistic cinematic") {
		t.Errorf("关闭开关后风格段应原样保留:%s", off)
	}
	if strings.Contains(off, "MATTE ANIMATED LOOK") {
		t.Error("关闭开关后不得注入画风硬约束")
	}
}

// TestLibraryPortraitPromptIsIdempotent 资产库定妆照的画风块不能重复叠加。
func TestLibraryPortraitPromptIsIdempotent(t *testing.T) {
	base := "slim young man, grey work shirt"
	once := LibraryPortraitPrompt(base)
	if !strings.Contains(once, "ANIMATED MATTE CHARACTER") || !strings.Contains(once, base) {
		t.Fatalf("应保留原提示词并补上画风块:%s", once)
	}
	if twice := LibraryPortraitPrompt(once); strings.Count(twice, "ANIMATED MATTE CHARACTER") != 1 {
		t.Errorf("重复调用不得叠加画风块:%s", twice)
	}
}

// TestCharactersFromLibraryShareHouseStyle 库里的角色与项目里生成的角色必须是同一种画风,
// 否则一个写实脸的库资产被复用到动画项目里,又变成"两张脸"。
func TestCharactersFromLibraryShareHouseStyle(t *testing.T) {
	lib := LibraryPortraitPrompt("mature man in a coat")
	if !strings.Contains(lib, animatedFaceImageBlock()) {
		t.Error("库侧定妆照必须带项目侧同一份画风块(单一来源,不得各写一份)")
	}
}

// TestDefaultProjectIsAnimeFaced 新建项目的默认值必须就是用户要的那套:
// 国漫二维 + 角色动漫化开(否则"新建一个项目试一下"仍然会得到真人油腻脸)。
func TestDefaultProjectIsAnimeFaced(t *testing.T) {
	d := project.DefaultStageParams()
	if !d.Script.FacesOn() {
		t.Error("默认应开启角色动漫化(非真人脸)")
	}
	if len(d.Script.Styles) == 0 || d.Script.Styles[0] != "guoman_2d" {
		t.Errorf("默认风格应为国漫二维,实际 %v", d.Script.Styles)
	}
	if !d.Assemble.DenoiseOn() {
		t.Error("默认应开启音频净化(用户报「有杂音」)")
	}
	var zero project.ScriptParams
	if !zero.FacesOn() {
		t.Error("老项目(参数快照无该键)也必须按开启处理 —— 它们正是报障的那批")
	}
	var za project.AssembleParams
	if !za.DenoiseOn() {
		t.Error("老项目也应默认开启音频净化")
	}
}
