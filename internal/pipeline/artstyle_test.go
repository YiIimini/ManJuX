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
	// photoreal / photorealistic 是**正向**的渲染质感描述(电影级写实表面),与"照片"这个实体
	// 名词不是一回事:前者说"怎么渲染",后者会让模型去画一张照片。先剔掉它们再查"照片类名词"。
	probe := strings.NewReplacer("photorealistic", "", "photoreal", "").Replace(b)
	for _, bad := range []string{"no ", "not ", "never ", "without ", "avoid ", "photo", "pore", "oily", "greasy", "plastic", "wax", "sweat"} {
		if strings.Contains(probe, bad) {
			t.Errorf("Krea-2 画风块不应出现「%s」(否定词/照片类名词会被照着画):%s", bad, b)
		}
	}
	// 介质迁移(2026-09-27):正向画风词从"线稿/平涂"改成电影级写实表面 —— 但**仍然只描述
	// 渲染方式与质感**,一个 2D 卡通词都不许留(留了就是往回拉)。
	for _, want := range []string{"photoreal 3d", "real materials", "matte", "film grain", "idealised", "original design"} {
		if !strings.Contains(b, want) {
			t.Errorf("Krea-2 画风块缺少正向画风词「%s」", want)
		}
	}
	for _, bad := range []string{"line art", "cel-shaded", "cel shading", "anime", "manga", "cartoon", "donghua"} {
		if strings.Contains(b, bad) {
			t.Errorf("Krea-2 画风块不得再出现 2D 卡通用词「%s」:%s", bad, b)
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
		RenderMediumMarker,
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
	// 介质迁移(2026-09-27)新增的一半:必须**显式排除 2D 卡通/手绘动画** ——
	// 用户问「为什么是 2D 漫画风格」,而只说清"要什么"不够,模型会往训练集里
	// 最省事的平涂卡通回落,所以两面都要写死。
	for _, want := range []string{"cel-shaded cartoon", "hand-drawn anime"} {
		if !strings.Contains(b, want) {
			t.Errorf("视频侧画风块必须显式排除 2D 卡通,缺少「%s」", want)
		}
	}
	// 必须点明"全片每一帧"都适用:只对首帧生效等于没生效
	if !strings.Contains(b, "every frame") {
		t.Error("画风块必须声明对全片每一帧生效")
	}
}

// TestStyleExpandKeepsUpstreamPhotorealWhenFacesOn 上游点名写实/3D 时,**画风归上游**,
// 动漫化开关不再覆盖它(2026-09-26 用户定调:「资产和视频渲染需要根据技能侧输出的脚本
// 里的风格来定,如未明确要求风格才走默认风格」)。
//
// 【为什么推翻旧行为】旧用例钉的是 2026-09-24 的"开关覆盖写实"。真机实测后发现它
// 制造了一条更坏的链:视频侧早已改成"风格归上游"(STYLE AUTHORITY),而定妆照这一侧
// 还在覆盖 —— 于是同一部剧里**定妆照是二维赛璐璐、成片是 3D 写实**,参考图与渲染
// 不在同一个视觉世界,H3 只能靠猜脸。用户长期报的"脸/头发漂移"正是这条链的末端。
//
// 保留的底线:法务条款(不得像真人)在任何风格下都必须有;开关在**上游没给风格**时
// 仍然决定兜底画风(见本文件另一条用例)。
func TestStyleExpandKeepsUpstreamPhotorealWhenFacesOn(t *testing.T) {
	// 真机里那条风格串(求你们骂我·第01集 plan.style),原样拿来钉
	real := styleExpand("PHOTOREAL 3D CINEMATIC WORLD, SEMI-STYLISED ANIME-ADJACENT FACES, matte finish", true, KindHuman)
	if strings.Contains(real, "clean cel shading") {
		t.Errorf("上游点名 3D 写实时不得再注入二维赛璐璐(参考图会与成片脱节):%s", real)
	}
	// 2026-09-27 修正:这条串要的是**电影级写实人像**,不是 3D CG 手办。
	// 旧判据 `Contains(s,"3d")` 会把它读成后者,于是往提示词里写
	// "BJD-inspired sculpted forms … collectible virtual figure look"(手办质感)。
	if !strings.Contains(real, "photorealistic cinematic portrait") {
		t.Errorf("上游的写实电影风格词应被翻译成电影级人像约束:%s", real)
	}
	if strings.Contains(real, "BJD") || strings.Contains(real, "collectible virtual figure") {
		t.Errorf("电影级写实不该被译成 3D CG 手办口径(s 里含 \"3d\" 不等于要手办):%s", real)
	}
	if strings.Contains(real, MediumMarker) {
		t.Errorf("上游点名写实/3D 时不许再塞平台动画介质块(两个介质打架,模型只会糊):%s", real)
	}
	for _, want := range []string{"PHOTOREAL 3D CINEMATIC WORLD", "IDENTITY FIDELITY", "never make a character resemble a real person"} {
		if !strings.Contains(real, want) {
			t.Errorf("风格原文 / 身份忠实度 / 法务底线三者都必须留(%q):%s", want, real)
		}
	}

	// 真人写实(非 3D)同样归上游
	on := styleExpand("真人实拍质感 + 港片胶片", true, KindHuman)
	if !strings.Contains(on, "photorealistic cinematic portrait") {
		t.Errorf("上游要写实就该给写实约束(开关不该吞掉它):%s", on)
	}
	if !strings.Contains(on, "真人实拍质感") {
		t.Errorf("风格原文应保留:%s", on)
	}
	// 开关关掉时行为不变(真人剧是合法路线)
	off := styleExpand("真人实拍质感", false, KindHuman)
	if !strings.Contains(off, "photorealistic cinematic portrait") || strings.Contains(off, MediumMarker) {
		t.Errorf("关闭开关的写实风格不该被动画化:%s", off)
	}
}

// TestStyleExpandAnimeFallbackStillApplies 上游**没给风格**时,角色动漫化开关仍然
// 决定兜底画风 —— 改上一条时不许把用户 2026-09-24 的"不要真人脸"一起改掉。
func TestStyleExpandAnimeFallbackStillApplies(t *testing.T) {
	got := styleExpand("", true, KindHuman)
	if !strings.Contains(got, MediumMarker) {
		t.Errorf("没有上游风格 + 动漫化开 → 必须给平台的动画兜底画风块:%s", got)
	}
	if strings.Contains(got, "photorealistic cinematic portrait") {
		t.Errorf("没有上游风格时不该冒出写实人像要求:%s", got)
	}
	// 国漫二维风格(非写实关键词)照旧走动画分支
	fam := styleExpand("国漫二维,赛璐璐", true, KindHuman)
	if !strings.Contains(fam, "clean cel shading") {
		t.Errorf("国漫二维风格应走动画分支:%s", fam)
	}
}

// TestStyleExpand3DNoLongerAsksForPlasticSkin 3D 分支原先写着 "smooth poreless
// synthetic skin"(光滑无孔合成皮肤)—— 无孔即塑料即皮脂高光,正是"人物油腻"的一半来源。
func TestStyleExpand3DNoLongerAsksForPlasticSkin(t *testing.T) {
	got := styleExpand("次世代 3D·BJD 虚拟人", false, KindHuman)
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
	if !strings.Contains(got, MediumMarker) {
		t.Error("默认(未表态)应开启动画哑光画风")
	}
	// 关闭开关后不得带画风块
	proj.Config.Params.Script.StylizedFaces = project.Bool(false)
	if strings.Contains(characterImagePrompt(proj, plan, plan.Characters[0]), MediumMarker) {
		t.Error("显式关闭后不得注入动画画风块")
	}
}

// TestStyleFallbackNotice 兜底画风的告知(语义 2026-09-24 变更)。
//
// 旧函数在"风格要真人 + 开关开着"时喊「已按动漫化渲染」——那条路径**已经不存在**了
// (平台不再抢风格),继续喊就是**说假话**(用户 2026-09-25 正是看到这条过时告警来问的)。
// 现在只有"方案里没有任何风格设定"才需要告知。
func TestStyleFallbackNotice(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	// 上游指定了风格(哪怕是写实)→ 平台照它渲染,不该有任何"我覆盖了你"的告警
	if n := styleFallbackNotice(proj, &project.Plan{Style: "photorealistic cinematic"}); n != "" {
		t.Errorf("上游已指定风格时不该告警(平台不再抢画风),实际:%q", n)
	}
	if n := styleFallbackNotice(proj, &project.Plan{Style: "国漫二维动画"}); n != "" {
		t.Errorf("动画风格同样不该告警,实际:%q", n)
	}
	// 没有任何风格设定 → 说明这是兜底,并指出风格该由谁定
	proj.Config.Params.Script.StyleHint = ""
	if n := styleFallbackNotice(proj, &project.Plan{}); !strings.Contains(n, "兜底画风") {
		t.Errorf("没有风格设定时应说明这是兜底,实际:%q", n)
	}
	// 显式关掉平台兜底 → 不再有任何画风可兜,也就没什么可说
	proj.Config.Params.Script.StylizedFaces = project.Bool(false)
	if n := styleFallbackNotice(proj, &project.Plan{}); n != "" {
		t.Errorf("关掉兜底画风后不该再提示,实际:%q", n)
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
	// 2026-09-24 用户定调「平台端渲染不限制风格,由小说技能端管控指定视频风格」:
	// 上游给了风格时,平台只负责"逐镜贯彻 + 原创底线",不再压成动画介质。
	if !strings.Contains(got, "STYLE AUTHORITY") {
		t.Error("渲染期提示词必须带风格执行段(上游风格逐镜贯彻)")
	}
	if !strings.Contains(got, "身份锁") {
		t.Error("渲染期提示词必须带身份锚定")
	}
	// 介质覆盖**无条件**追加,不靠关键词:LLM 的措辞追不完(实测真实项目的
	// "gritty rural Chinese period realism" 一个写实关键词都不命中)。
	if strings.Contains(got, "MEDIUM OVERRIDE") {
		t.Error("上游已指定风格时不得再做介质覆盖(平台不抢画风)")
	}
	if !strings.Contains(got, "STYLE AUTHORITY") {
		t.Error("风格执行段必须说明「照此渲染、不得另换画风」")
	}
	if !strings.Contains(got, "【全片视觉风格】photorealistic cinematic") {
		t.Error("风格段本身应原样保留(它仍是场景/色调/氛围的出题依据)")
	}
	// 关掉开关:不得注入介质覆盖,风格段也原样保留(真人剧合法)
	proj.Config.Params.Script.StylizedFaces = project.Bool(false)
	off := ShotRenderPrompt(proj, plan, shot)
	if !strings.Contains(off, "【全片视觉风格】photorealistic cinematic") {
		t.Errorf("关闭开关后风格段应原样保留:%s", off)
	}
	if strings.Contains(off, RenderMediumMarker) || strings.Contains(off, "MEDIUM OVERRIDE") {
		t.Error("关闭开关后不得注入画风硬约束与介质覆盖")
	}
}

// TestRealWorldStyleStringStillGetsMediumOverride 真实数据的回归:
// LLM 产出的风格串里常有**不含任何写实关键词**却明显是照片路线的措辞
// (实测本机在跑的项目的风格串后半段就是 "natural dawn light, 35mm film grain,
// shallow depth of field" —— 一个关键词都不命中)。
// 关键词匹配必然漏,所以介质覆盖必须**无条件**生效,不能靠识别风格串。
func TestRealWorldStyleStringStillGetsMediumOverride(t *testing.T) {
	const photoish = "natural dawn light, 35mm film grain, shallow depth of field, desaturated earthy palette"
	if photorealRequested(photoish) {
		t.Fatalf("这条风格串本不该命中关键词(测试前提失效),请换一条:%q", photoish)
	}
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: photoish}
	shot := &project.Shot{ID: 1, H3Prompt: "detailed_description:\n[Shot 1] ..."}
	got := ShotRenderPrompt(proj, plan, shot)
	// 上游**给了**风格(哪怕是这种不含关键词的写法)时:平台照它渲染,不再压成动画;
	// 兜底画风块只在**没有**风格时才出现(见 styleAndMediumBlock)。
	if strings.Contains(got, "MEDIUM OVERRIDE") {
		t.Errorf("上游已指定风格时不得做介质覆盖:\n%s", got)
	}
	if !strings.Contains(got, "STYLE AUTHORITY") {
		t.Errorf("上游指定的风格必须带风格执行段:\n%s", got)
	}

	// 已知的写实措辞也要能识别出来(用于给用户那句"要真人剧请关掉开关"的告警)
	if !photorealRequested("gritty rural Chinese period realism") {
		t.Error("period realism 属于写实措辞,应能提示用户")
	}
}

// TestLibraryPortraitPromptIsIdempotent 资产库定妆照的类型块不能重复叠加。
func TestLibraryPortraitPromptIsIdempotent(t *testing.T) {
	base := "slim young man, grey work shirt"
	once := LibraryPortraitPrompt("小凡", "十七岁少年", base)
	if !strings.Contains(once, MediumMarker) || !strings.Contains(once, base) {
		t.Fatalf("应保留原提示词并补上画风块:%s", once)
	}
	if twice := LibraryPortraitPrompt("小凡", "十七岁少年", once); strings.Count(twice, "IDENTITY FIDELITY") != 1 {
		t.Errorf("重复调用不得叠加类型块:%s", twice)
	}
	// 库侧的器物也必须走器物那套(否则库里把铜雀当人出图,复用到项目里全是错的)
	obj := LibraryPortraitPrompt("阿凿", "巴掌大的一只机关铜雀", "a palm-sized mechanical bronze sparrow")
	if !strings.Contains(obj, "OBJECT FIDELITY") || strings.Contains(obj, "IDENTITY FIDELITY") {
		t.Errorf("库侧器物应按器物渲染方法组装:\n%s", obj)
	}
	if strings.Contains(strings.ToLower(obj), "upper-body") {
		t.Errorf("库侧器物不得保留半身取景:\n%s", obj)
	}
}

// TestCharactersFromLibraryShareHouseStyle 库里的角色与项目里生成的角色必须是同一种画风,
// 否则一个写实脸的库资产被复用到动画项目里,又变成"两张脸"。
func TestCharactersFromLibraryShareHouseStyle(t *testing.T) {
	lib := LibraryPortraitPrompt("小凡", "十七岁少年", "mature young man in a coat")
	// 判据用**标记串**而不是整块文本：装配点现在会把中英对照整块清掉（`project.DropHanRunes`），
	// 拿 `animatedMediumImageBlock()` 的完整文本去比，比的是"连中文对照都要在" ——
	// 那与"中文不许进画面提示词"直接打架。标记串是英文、且是"这一段在不在"的唯一稳定凭据。
	if !strings.Contains(lib, MediumMarker) {
		t.Errorf("库侧定妆照必须带项目侧同一份介质块(单一来源,不得各写一份)，缺标记 %q:\n%s", MediumMarker, lib)
	}
	if project.CountCJK(lib) != 0 {
		t.Errorf("库侧定妆照进模型前不得残留汉字（会被当成要画的字）:%s", lib)
	}
}

// TestDefaultProjectIsAnimeFaced 新建项目的默认值必须就是用户要的那套:
// 国风**电影级** + 角色半风格化(否则"新建一个项目试一下"仍然会得到真人油腻脸、
// 或者 2026-09-27 那次报障里的 2D 漫画风)。
func TestDefaultProjectIsAnimeFaced(t *testing.T) {
	d := project.DefaultStageParams()
	if !d.Script.FacesOn() {
		t.Error("默认应开启角色动漫化(非真人照片脸)")
	}
	// 2026-09-27 改:默认从 `guoman_2d`(国漫二维)换成 `guofeng_cinematic`(国风电影级)——
	// 用户问「平台端 LLM 解析渲染的内容贴合原文,但为什么是 2D 漫画风格」,根因之一就是
	// 这个默认值 + 当时 director 提示词里"必须是动画风格"那句。
	if len(d.Script.Styles) == 0 || d.Script.Styles[0] != "guofeng_cinematic" {
		t.Errorf("默认风格应为国风电影级,实际 %v", d.Script.Styles)
	}
	// 预设表里那条必须是**能渲染的实串**,不是占位符
	if p, ok := StylePresets["guofeng_cinematic"]; !ok || !strings.Contains(p.Prompt, "photoreal 3D") {
		t.Errorf("默认预设必须在 StylePresets 里且写明电影级口径,实际 %q (exists=%v)", p.Prompt, ok)
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
