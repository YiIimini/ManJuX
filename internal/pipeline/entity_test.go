package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 本文件锁住"不同类型的角色走不同渲染方法"这条链路的每一环。
//
// 现实起点(2026-09-24 用户报「阿凿是一只铜雀却渲染成人物」):该项目真实的角色卡是
// 阿凿 = "巴掌大一只铜雀。翅膀由七层薄铜片叠成…" —— 被当成"人"渲染,定妆照出的是
// **一个女孩的半身肖像**(已人工核对原图)。所以这里的用例直接抄真实文案,不另编示例。

// realAzao 真实项目里的铜雀角色卡(逐字取自 plan.json)。
var realAzao = project.Character{
	ID:          "char_azao",
	Name:        "阿凿",
	Description: "巴掌大一只铜雀。翅膀由七层薄铜片叠成,胸口嵌一颗灵枢小珠,蹲下时珠里的光会慢慢转动;尾羽是三片薄铜凿,飞起来哗啦哗啦响,一路洒下极细的金色火星;铜喙尖锐,关节处有小铆钉。",
	ImagePrompt: "A palm-sized mechanical bronze sparrow, wings built from seven thin stacked bronze plates, " +
		"a small glowing spirit-core bead set into its chest, three thin chisel-shaped bronze tail feathers, " +
		"a sharp little bronze beak, tiny riveted joints, warm bronze patina, in CHINESE 2D ANIME STYLE, " +
		"CLEAN LINEWORK, VIBRANT CEL SHADING, DONGHUA QUALITY, plain background, upper-body framing, front-facing",
}

// realLuqingyan 同一项目里的人角色(逐字取自 plan.json)。
var realLuqingyan = project.Character{
	ID:          "char_luqingyan",
	Name:        "鲁青砚",
	Description: "十六岁少年,外壁班末等工匠。窄长脸,颧骨微高,眉心一道淡淡竖纹;常年日晒,肤色暗铜哑光;黑发用一枚旧铜环束成低马尾。",
	ImagePrompt: "16-year-old boy, narrow long face, high cheekbones, sun-baked dull bronze skin, black hair pulled into a " +
		"low ponytail tied with a worn old copper ring, wearing a coarse grey-brown hemp work jacket, " +
		"in CHINESE 2D ANIME STYLE, CLEAN LINEWORK, plain background, upper-body framing, front-facing",
}

// TestInferEntityKindRealCases 类型推断:真实文案必须判对,而且**不能被材质词带偏**
// (鲁青砚的描述里有"暗铜哑光""旧铜环",把他判成器物同样是事故)。
func TestInferEntityKindRealCases(t *testing.T) {
	cases := []struct {
		name, desc, prompt string
		want               EntityKind
	}{
		{realAzao.Name, realAzao.Description, realAzao.ImagePrompt, KindObject},
		{realLuqingyan.Name, realLuqingyan.Description, realLuqingyan.ImagePrompt, KindHuman},
		// 材质词不得把人带偏
		{"郑铁匠", "四十岁铁匠,常年打铁,皮肤被火星烫出斑点,围着皮围裙。", "burly blacksmith", KindHuman},
		{"机关人", "一具黄铜机关人,关节是榫卯与齿轮。", "a clockwork automaton", KindObject},
		{"提线木偶", "半人高的提线木偶,脸上画着戏妆。", "a marionette with painted face", KindObject},
		{"小白", "一只巴掌大的灵狐幼崽,尾巴尖是白的。", "a spirit fox cub", KindSpiritPet},
		{"蛟", "三丈长的黑鳞蛟龙,盘在山谷里。", "a giant scaled serpent", KindBeast},
		{"三娘", "一缕残魂,半透明,边缘化在雾里,能穿墙。", "a translucent lingering spirit", KindIncorporeal},
		{"小鲁Q", "鲁青砚的 Q 版形象,大头小身。", "chibi figure", KindChibi},
	}
	for _, c := range cases {
		if got := InferEntityKind(c.name, c.desc, c.prompt); got != c.want {
			t.Errorf("%s 应判为 %s,实际 %s", c.name, EntityKindLabel(c.want), EntityKindLabel(got))
		}
	}
	if got := InferEntityKind("", "", ""); got != KindHuman {
		t.Errorf("空文本应兜底为人,实际 %s", got)
	}
}

// TestNormalizeEntityKind 中英文别名都要认,认不出来返回空串(交给推断,而不是硬套人)。
func TestNormalizeEntityKind(t *testing.T) {
	cases := map[string]EntityKind{
		"human": KindHuman, "人": KindHuman, "人物": KindHuman,
		"object": KindObject, "器物": KindObject, "木偶": KindObject, "道具": KindObject,
		"spirit_pet": KindSpiritPet, "灵宠": KindSpiritPet,
		"beast": KindBeast, "妖兽": KindBeast,
		"incorporeal": KindIncorporeal, "无实体": KindIncorporeal, "灵体": KindIncorporeal,
		"chibi": KindChibi, "Q版": KindChibi, "q版": KindChibi,
	}
	for in, want := range cases {
		if got := NormalizeEntityKind(in); got != want {
			t.Errorf("NormalizeEntityKind(%q) = %s, want %s", in, got, want)
		}
	}
	if NormalizeEntityKind("随便什么") != "" {
		t.Error("认不出的取值应返回空串(交给推断兜底)")
	}
}

// TestResolveEntityKindExplicitWins 显式 kind 优先于推断(用户手工纠正必须生效)。
func TestResolveEntityKindExplicitWins(t *testing.T) {
	ch := realAzao
	ch.Kind = "spirit_pet" // 用户手工改成灵宠
	if got := ResolveEntityKind(ch); got != KindSpiritPet {
		t.Errorf("显式 kind 应优先,实际 %s", got)
	}
	ch.Kind = "胡说" // 非法值 → 回到推断(铜雀 → 器物)
	if got := ResolveEntityKind(ch); got != KindObject {
		t.Errorf("非法 kind 应回退推断,实际 %s", got)
	}
}

// TestObjectSheetDropsHumanFraming 器物定妆照不得再要求"半身、正面、照抄脸"。
//
// 这是本次报障的直接根因:LLM 写的 image_prompt 尾巴固定是
// `plain background, upper-body framing, front-facing`,照这条渲染铜雀只能出一个人。
func TestObjectSheetDropsHumanFraming(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国漫二维动画", Characters: []project.Character{realAzao, realLuqingyan}}

	got := characterImagePrompt(proj, plan, realAzao)
	low := strings.ToLower(got)
	for _, bad := range []string{"upper-body framing", "front-facing", "upper body"} {
		if strings.Contains(low, bad) {
			t.Errorf("器物定妆照不得保留人像取景「%s」:\n%s", bad, got)
		}
	}
	for _, want := range []string{"OBJECT FIDELITY", "three-quarter product view", "the WHOLE object"} {
		if !strings.Contains(got, want) {
			t.Errorf("器物定妆照缺少「%s」:\n%s", want, got)
		}
	}
	// 人类那份"脸必须与描述逐项一致"的条款绝不能出现在器物上
	if strings.Contains(got, "the face must match the written brief EXACTLY") {
		t.Error("器物定妆照不得带人类的脸部忠实度条款(那是在要求它长脸)")
	}
	// 身份特征仍要在(铜片/铆钉/尾羽这些是它的"长相")
	for _, keep := range []string{"seven thin stacked bronze plates", "riveted joints", "bronze patina"} {
		if !strings.Contains(got, keep) {
			t.Errorf("器物结构描述不得丢失「%s」", keep)
		}
	}
}

// TestHumanSheetFramingIsComputedFromAspect 人的取景**由平台按画幅算**,不再照抄卡里那句。
//
// 2026-09-27 用户报「角色定妆照应该是角色上半身的正面照片啊,咋现在角色头都顶上看不全」:
// 卡里写死的 `upper-body framing`(上半身)在 768×1344 的竖幅里装不下整个头,模型把头顶
// 裁掉了。取景因此改成按画幅算(竖幅 → 胸像 + 头顶留白);人类忠实度条款照旧保留。
func TestHumanSheetFramingIsComputedFromAspect(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国漫二维动画", Characters: []project.Character{realLuqingyan}}
	got := characterImagePrompt(proj, plan, realLuqingyan)
	if !strings.Contains(got, "chest-up portrait") || !strings.Contains(got, "clear empty space above the hair") {
		t.Errorf("竖幅下应给「胸像 + 头顶留白」的构图:\n%s", got)
	}
	if strings.Contains(got, "upper-body framing") {
		t.Errorf("卡里那句写死的半身取景应被剥掉(取景只有一个来源):\n%s", got)
	}
	if !strings.Contains(got, "IDENTITY FIDELITY") {
		t.Error("人应带脸部忠实度条款")
	}
	if strings.Contains(got, "OBJECT FIDELITY") {
		t.Error("人不得带器物条款")
	}
}

// TestAnchorPromptDispatchesByIdentityKind 镜头锚定按类型分发:
// 同一镜里"人 + 器物"必须各拿各的身份锁,而且器物段不许出现人形要求。
func TestAnchorPromptDispatchesByIdentityKind(t *testing.T) {
	plan := &project.Plan{
		Style:      "国漫二维动画",
		Characters: []project.Character{realLuqingyan, realAzao},
		Scenes:     []project.Scene{{ID: "s1", Name: "外壁脚手架", Description: "风中的木架"}},
	}
	shot := &project.Shot{ID: 3, Characters: []string{"char_luqingyan", "char_azao"}, Scene: "s1",
		H3Prompt: "detailed_description:\n[Shot 1] ..."}
	got := anchorPrompt(plan, shot, true)

	if !strings.Contains(got, "类型=人") || !strings.Contains(got, "类型=物") {
		t.Errorf("出场角色应逐条标出类型:\n%s", got)
	}
	if !strings.Contains(got, "【人条款】") || !strings.Contains(got, "【物条款】") {
		t.Error("两类条款都要在(人抄脸 / 器物抄结构)")
	}
	if !strings.Contains(got, "器物锁") {
		t.Error("缺少器物锁")
	}
	if !strings.Contains(got, "ARTEFACT CLAUSE") {
		t.Error("缺器物反拟人化条款(不得给它加人脸/四肢)")
	}
	if !strings.Contains(got, "身份锁:画面中每个人物的脸型/五官") {
		t.Error("人的身份锁必须保留(只有人适用)")
	}
}

// TestAnchorPromptObjectOnlyHasNoHumanLock 纯器物镜头不得出现"每个人物的脸"这类人形锚定。
func TestAnchorPromptObjectOnlyHasNoHumanLock(t *testing.T) {
	plan := &project.Plan{
		Style:      "国漫二维动画",
		Characters: []project.Character{realAzao},
		Scenes:     []project.Scene{{ID: "s1", Name: "外壁", Description: "风中的木架"}},
	}
	shot := &project.Shot{ID: 4, Characters: []string{"char_azao"}, Scene: "s1",
		H3Prompt: "detailed_description:\n[Shot 1] ..."}
	got := anchorPrompt(plan, shot, true)
	if strings.Contains(got, "每个人物的脸型") {
		t.Errorf("纯器物镜头不得带人类身份锁:\n%s", got)
	}
	for _, want := range []string{"器物锁", "ARTEFACT CLAUSE", "STYLE AUTHORITY"} {
		if !strings.Contains(got, want) {
			t.Errorf("纯器物镜头缺少「%s」", want)
		}
	}
	// 关掉角色动漫化后,反拟人化条款**仍须在**(它是正确性,不是画风选择)
	off := anchorPrompt(plan, shot, false)
	if !strings.Contains(off, "ARTEFACT CLAUSE") {
		t.Error("关闭画风开关不应丢掉反拟人化条款")
	}
	if strings.Contains(off, RenderMediumMarker) {
		t.Error("关闭画风开关后不应再有画风块")
	}
}

// TestTurnaroundPromptPerKind 多视角提示词按类型分发:器物转台、人转身、灵体不得实体化。
func TestTurnaroundPromptPerKind(t *testing.T) {
	obj := EntityProfileOf(KindObject).TurnaroundPrompt("阿凿", realAzao.ImagePrompt, "")
	for _, want := range []string{"EXACT SAME object", "object turntable", "NOT turn into a creature", "whole object"} {
		if !strings.Contains(obj, want) {
			t.Errorf("器物多视角提示词缺少「%s」:\n%s", want, obj)
		}
	}
	if strings.Contains(obj, "SAME PERSON") || strings.Contains(obj, "Character turnaround") {
		t.Error("器物多视角不得套用人物转身文案")
	}

	hum := EntityProfileOf(KindHuman).TurnaroundPrompt("鲁青砚", realLuqingyan.ImagePrompt, "")
	for _, want := range []string{"Character turnaround", "SAME PERSON", "FACE LOCK", "upper-body medium shot"} {
		if !strings.Contains(hum, want) {
			t.Errorf("人物多视角提示词缺少「%s」(2026-09-12 定稿不得回退)", want)
		}
	}

	inc := EntityProfileOf(KindIncorporeal).TurnaroundPrompt("三娘", "a translucent woman in a red robe", "")
	for _, want := range []string{"EXACT SAME apparition", "never becomes solid", "no cast shadow"} {
		if !strings.Contains(inc, want) {
			t.Errorf("无实体多视角提示词缺少「%s」", want)
		}
	}
	// 六类都要能出提示词(不允许空壳)
	for _, k := range EntityKinds {
		p := EntityProfileOf(k).TurnaroundPrompt("测试", "a thing", "")
		if len(p) < 600 || !strings.Contains(p, "subject_definitions:") || !strings.Contains(p, "overall_soundscape:") {
			t.Errorf("%s 的多视角提示词不完整(长度 %d)", EntityKindLabel(k), len(p))
		}
		if !strings.Contains(p, "[Shot 4]") {
			t.Errorf("%s 的多视角提示词缺第 4 帧", EntityKindLabel(k))
		}
	}
}

// TestEntityProfilesComplete 六类渲染方法必须各自成体系(取景/身份/画风/校验要点齐全),
// 且**非人类型不得夹带人类专属条款**(脸/肤色/半身)。
func TestEntityProfilesComplete(t *testing.T) {
	for _, k := range EntityKinds {
		p := EntityProfileOf(k)
		if p.Label == "" || p.Hint == "" {
			t.Errorf("%s 缺标签或说明", k)
		}
		if p.SheetIdentity == "" || p.SheetLook == "" {
			t.Errorf("%s 缺定妆照条款", EntityKindLabel(k))
		}
		if p.TurnSubject == "" || p.TurnSummary == "" || len(p.TurnRetention) == 0 ||
			p.TurnDetail == "" || p.TurnNegative == "" || p.TurnSound == "" {
			t.Errorf("%s 的多视角方法不完整", EntityKindLabel(k))
		}
		if p.AnchorLock == "" || p.AnchorUseFor == "" || p.JudgePoints == "" {
			t.Errorf("%s 缺镜头锚定或校验要点", EntityKindLabel(k))
		}
		if k == KindHuman || k == KindChibi {
			continue
		}
		if p.RewriteSheetFraming != true {
			t.Errorf("%s 必须重写取景尾巴(否则沿用 LLM 写的人像取景)", EntityKindLabel(k))
		}
		if p.AntiForm == "" {
			t.Errorf("%s 缺反错误形态条款(不得拟人化/不得实体化/不得缩水)", EntityKindLabel(k))
		}
		for _, bad := range []string{"upper-body framing", "the face must match"} {
			if strings.Contains(p.SheetIdentity, bad) || strings.Contains(p.SheetFraming, bad) {
				t.Errorf("%s 不得夹带人类专属条款「%s」", EntityKindLabel(k), bad)
			}
		}
	}
	// 妖兽/灵宠必须显式禁止拟人化;器物必须显式禁止长脸;无实体必须显式禁止实体化
	for k, want := range map[EntityKind]string{
		KindObject:      "no human face",
		KindSpiritPet:   "no human face",
		KindBeast:       "no human face",
		KindIncorporeal: "must NOT become solid",
	} {
		if !strings.Contains(EntityProfileOf(k).TurnNegative, want) {
			t.Errorf("%s 的多视角禁止项缺少「%s」", EntityKindLabel(k), want)
		}
	}
}

// TestShotEntityKinds 本镜类型集合按固定顺序去重(供画风块逐类展开)。
func TestShotEntityKinds(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{realLuqingyan, realAzao, {
		ID: "char_x", Name: "小白", Description: "一只灵狐幼崽",
	}}}
	shot := &project.Shot{Characters: []string{"char_azao", "char_luqingyan", "char_x", "char_luqingyan"}}
	got := ShotEntityKinds(plan, shot)
	want := []EntityKind{KindHuman, KindObject, KindSpiritPet}
	if len(got) != len(want) {
		t.Fatalf("类型集合应为 %v,实际 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("类型集合应为 %v,实际 %v", want, got)
		}
	}
}

// TestCastContrastIsKindAware 同剧对比块也要按类型换措辞。
//
// 真实数据实测(2026-09-24):这段原先一律写"脸型/年龄/肤色要和他们不同、画面里只能有
// 一个人"—— 套在铜雀身上等于又在要求它长脸。器物比的是形制/材质/部件数。
func TestCastContrastIsKindAware(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{realAzao, realLuqingyan}}
	obj := castContrastBlock(plan, "", realAzao.Name, KindObject)
	if !strings.Contains(obj, "different silhouette") || !strings.Contains(obj, "exactly ONE object") {
		t.Errorf("器物对比块应比形制/材质,且「画面里只有一件」:\n%s", obj)
	}
	if strings.Contains(obj, "different face shape") || strings.Contains(obj, "ONE person") {
		t.Errorf("器物对比块不得比脸/称「一个人」:\n%s", obj)
	}
	hum := castContrastBlock(plan, "", realLuqingyan.Name, KindHuman)
	if !strings.Contains(hum, "different face shape") || !strings.Contains(hum, "exactly ONE person") {
		t.Errorf("人物对比块应保持原措辞(2026-09-11 定稿):\n%s", hum)
	}
}

// TestClearDerivedViews 改类型/重生成时作废旧角度图:本体必须留着,派生图必须清掉。
func TestClearDerivedViews(t *testing.T) {
	dir := t.TempDir()
	master := filepath.Join(dir, "characters_char_azao.png")
	files := []string{
		master,
		filepath.Join(dir, "characters_char_azao_turnaround.png"),
		filepath.Join(dir, "characters_char_azao_view_side.png"),
		filepath.Join(dir, "characters_char_azao_view_back.png"),
		filepath.Join(dir, "characters_char_other.png"), // 别人的图不许动
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := ClearDerivedViews(master); n != 3 {
		t.Fatalf("应清掉 3 张派生图,实际 %d", n)
	}
	for _, keep := range []string{master, filepath.Join(dir, "characters_char_other.png")} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("不该删的文件被删了:%s", keep)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "characters_char_azao_turnaround.png")); err == nil {
		t.Error("转台图应已删除(它是按旧类型出的第二张参考)")
	}
}

// TestRewriteSheetFraming 取景重写要剥干净且不留残句标点。
func TestRewriteSheetFraming(t *testing.T) {
	in := "slim figure, warm bronze patina, in CHINESE 2D ANIME STYLE, Plain Background, upper-body framing, Front-Facing"
	got := rewriteSheetFraming(in, "the WHOLE object centred")
	low := strings.ToLower(got)
	for _, bad := range []string{"upper-body", "front-facing", "plain background"} {
		if strings.Contains(low, bad) {
			t.Errorf("应剥掉「%s」:%s", bad, got)
		}
	}
	if !strings.Contains(got, "warm bronze patina") || !strings.Contains(got, "the WHOLE object centred") {
		t.Errorf("原有材质描述与新取景都要在:%s", got)
	}
	if strings.Contains(got, " ,") || strings.Contains(got, ",,") {
		t.Errorf("剥除后不得留下残句标点:%s", got)
	}
}
