package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 定妆照必须**上半身正脸**（2026-09-29 用户报障 + 真机对照实验）。
//
// 【为什么要写成开头那一句】原来的取景纪律只在提示词**末尾**（PortraitFramingClause），
// 实测压不住卡原文里大段的全身描述（长腿 / 大腿装甲 / 腰侧挂链 / 靴子）——同一张卡出图
// 仍是全身立绘。真机对照（库侧"按提示词代生成"，同一张卡）：
//
//	① 只有末尾那句              → 全身（头到脚）
//	② 末尾换成更强的"裁到胸口"   → 仍是全身（头到膝）
//	③ **开头加一句 + 末尾保留** → 半身正脸 ✔
//
// 所以这条测试钉的是**位置**：开头必须有取景声明，末尾那句也必须在（两句一起才收得住）。
func TestPortraitLeadFramingIsAtTheFront(t *testing.T) {
	card := "Android girl with apparent age twenty, slender build with eight-heads-tall doll proportions " +
		"and long limbs; porcelain-white skin, golden brass halo ornament, white ceramic armour " +
		"plates on shoulders, forearms, hips and outer thighs, knee guards and ankle boots."
	// 竖幅（成片画幅 768×1344）：要胸像
	got := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "祁七", Description: "非人类机体", ImagePrompt: card,
		FacesOn: true, FrameW: 768, FrameH: 1344,
	}, &project.Plan{Style: "guofeng_cinematic"})
	if !strings.HasPrefix(got, portraitLeadBust) {
		t.Fatalf("定妆照提示词必须以**取景声明**开头（实测否则出全身立绘），实际开头：%q", leadHead(got, 90))
	}
	if !strings.Contains(got, "FRAMING (mandatory)") {
		t.Fatal("末尾那句构图纪律也要保留：开头声明主体、末尾收纪律，缺一不可")
	}
	if !strings.Contains(got, card) {
		t.Fatal("卡原文必须原样保留（取景是**追加**，不是替换卡里的外貌描述）")
	}
	// 幂等：装配点会被调用两次（资产阶段 + 渲染前补齐），叠两句就是两份取景
	if again := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "祁七", Description: "非人类机体", ImagePrompt: got,
		FacesOn: true, FrameW: 768, FrameH: 1344,
	}, &project.Plan{Style: "guofeng_cinematic"}); strings.Count(again, portraitLeadBust) != 1 {
		t.Fatalf("重复装配把取景声明叠了 %d 份", strings.Count(again, portraitLeadBust))
	}
	// 横幅：换成头肩那句（不能一律胸像 —— 横幅里"胸像"会被裁掉肩膀）
	wide := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "祁七", Description: "非人类机体", ImagePrompt: card,
		FacesOn: true, FrameW: 1344, FrameH: 768,
	}, &project.Plan{Style: "guofeng_cinematic"})
	if !strings.HasPrefix(wide, portraitLeadHeadShoulders) {
		t.Fatalf("横幅应取头肩，实际开头：%q", leadHead(wide, 90))
	}
	// 非人类型不套人像取景（器物有自己的取景纪律，见 EntityProfile）
	obj := PortraitPrompt(PortraitSubject{
		Kind: KindObject, Name: "钢印铭牌", Description: "一枚钢印铭牌", ImagePrompt: "A single brushed titanium nameplate",
		FacesOn: true, FrameW: 768, FrameH: 1344,
	}, &project.Plan{Style: "guofeng_cinematic"})
	if strings.Contains(obj, portraitLeadBust) || strings.Contains(obj, portraitLeadHeadShoulders) {
		t.Fatalf("器物不该套人像取景：%q", leadHead(obj, 90))
	}
}

// 板子（三视图）必须**整句剥掉**定妆照的开头取景声明：板子是"3 个全身 + 6 个头 + 6 个细节"，
// 留着"裁到胸口、只要头颈胸"就是同一段里两套取景打架（模型只会二选一地糊）。
func TestBoardDropsPortraitLeadFraming(t *testing.T) {
	portrait := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "祁七", Description: "非人类机体",
		ImagePrompt: "Android girl with porcelain-white skin and white ceramic armour plates.",
		FacesOn:     true, FrameW: 768, FrameH: 1344,
	}, &project.Plan{Style: "guofeng_cinematic"})
	board := BoardImagePrompt(portrait)
	if strings.Contains(board, "BUST PORTRAIT") || strings.Contains(board, "cropped at mid-chest") {
		t.Fatalf("板子输入里还留着定妆照的人像取景：%q", leadHead(board, 200))
	}
	if !strings.Contains(board, BoardMarker) {
		t.Fatal("板子必须带上自己的标记（幂等判据）")
	}
}

// TestPortraitFramingIsACropNotAProportion 胸像取景不许再自相矛盾
// （2026-10-08 用户「角色渲染的很丑，你看一下资产库」）。
//
// 真机那三张定妆照最丑的一点：**"头占画面 1/4" + "按卡里的头身比画（七头半/八头身 ⇒ 高瘦长腿）"
// 两条要求并存** ⇒ 模型只好把整身画进来。平台自己的 VLM 原话：「全身完整入镜至靴子」
// 「含发髻的头约占画面高 1/4，脸本体约 1/8」「头偏大、身偏短，比例近童装立绘」。
//
// 修法：**只动平台自己写的那两句取景文案** —— 删掉"按卡里的头身比画"那句，换成可执行的
// **裁切**口径（画面下缘落在胸口、构图保持这个紧度）。卡原文（含"eight-heads-tall"这类
// 外貌描述）一个字不动 —— 取景是**追加**，不是替换（见上一条测试）。
func TestPortraitFramingIsACropNotAProportion(t *testing.T) {
	for _, lead := range []string{portraitLeadBust, portraitLeadHeadShoulders} {
		if strings.Contains(lead, "head-to-body ratio") || strings.Contains(lead, "long-limbed") {
			t.Fatalf("取景声明里不许再要求按卡里的头身比画（胸像里它与\"头占 1/4\"打架，模型只能画全身）：%q", lead)
		}
	}
	if !strings.Contains(portraitLeadBust, portraitCropCue) {
		t.Fatal("胸像声明必须带上裁切口径（只写 cropped at mid-chest 不够 —— 真机那张就是这么出的全身像）")
	}
	clause := PortraitFramingClause(KindHuman, 768, 1344)
	if strings.Contains(clause, "head-to-body ratio") || strings.Contains(clause, "long-limbed") {
		t.Fatalf("末尾的取景纪律同样不许提头身比：%q", clause)
	}
	if !strings.Contains(clause, portraitCropCue) {
		t.Fatalf("末尾的取景纪律必须与开头同一套裁切口径：%q", clause)
	}
	// 尺度线索（头占画面 1/4）是 2026-09-29 那条实测的产物（没有它，模型把头顶满竖幅：
	// 霍延那张头占画面一半以上）—— 改裁切口径时**不许连带删掉**。
	if !strings.Contains(portraitLeadBust, "ONE QUARTER") || !strings.Contains(clause, "ONE QUARTER") {
		t.Fatal("头占画面 1/4 的尺度线索必须保留（删了模型会把头放大到占满竖幅）")
	}
}

func leadHead(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// TestStripPortraitLeadFramingIsShapeBased 存量卡也要剥得掉（2026-10-08 真机探针咬出）。
//
// 盘上那些卡的 image_prompt 存的是**当时那份**取景措辞（这一轮把胸像里"按卡里的头身比画"
// 那句删掉了），只按当前字面量判前缀 ⇒ 旧取景声明剥不掉，会作为"卡正文"残留：
// 重装配时又拼一遍，或被当成**别人的外形摘要**（真机探针输出里摘要开头就是
// `BUST PORTRAIT (head, neck …`）。
func TestStripPortraitLeadFramingIsShapeBased(t *testing.T) {
	// 逐字取自盘上 asset.json（姜晚舟那张，旧措辞）
	const stored = "BUST PORTRAIT (head, neck and upper chest only - the picture is cropped at mid-chest, " +
		"pure white seamless backdrop). SCALE (mandatory): the head is about ONE QUARTER of the picture " +
		"height — the head stays proportionate instead of filling the frame; the shoulders span the full " +
		"width of the picture and stay WIDER than the head, and the chest and upper arms fill the lower " +
		"half. Keep the head-to-body ratio stated in the brief (a seven-and-a-half or eight-heads-tall " +
		"adult reads as tall and long-limbed, with a proportionate head). A woman in her early twenties, " +
		"medium height, lean-toned with a strong grip, oval face with a crisp defined jawline."
	got := StripPortraitLeadFraming(stored)
	if !strings.HasPrefix(got, "A woman in her early twenties") {
		t.Fatalf("旧措辞的取景声明必须剥掉，实际开头：%q", leadHead(got, 80))
	}
	for _, bad := range []string{"BUST PORTRAIT", "ONE QUARTER", "head-to-body ratio"} {
		if strings.Contains(got, bad) {
			t.Fatalf("剥完还残留平台取景语汇 %q：%q", bad, leadHead(got, 120))
		}
	}
	// 幂等：剥过一次再剥不变（装配点会被调用两次）
	if again := StripPortraitLeadFraming(got); again != got {
		t.Fatalf("剥取景不是幂等的：%q", leadHead(again, 80))
	}
	// 技能侧只写了外貌、没有取景抬头 ⇒ 原样返回
	const bare = "16-year-old boy, narrow long face, sun-baked dull bronze skin."
	if StripPortraitLeadFraming(bare) != bare {
		t.Fatal("没有取景抬头的卡不该被动")
	}
	// 抬头在开头、正文第一句不是取景句 ⇒ 只剥取景那几句
	const mixed = "BUST PORTRAIT (head, neck and upper chest only). A tall woman with a long ponytail."
	if s := StripPortraitLeadFraming(mixed); s != "A tall woman with a long ponytail." {
		t.Fatalf("只该剥取景句，实际：%q", s)
	}
}
