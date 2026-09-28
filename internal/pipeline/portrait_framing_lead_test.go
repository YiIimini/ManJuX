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

func leadHead(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
