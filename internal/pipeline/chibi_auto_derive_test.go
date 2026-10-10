package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// chibi_auto_derive_test.go 自动 Q 版那条支线**必须以定妆照为参考图派生**。
//
// 【2026-10-10 用户「Q 版卡有对应角色，直接图生图为 Q 版形象能简单一点吗?」—— 能,而且平台早该这样】
//
// 派生链路 2026-09-28 就按同一条口径建好了（`sheetderive.go`：`ChibiDeriveClause` 只改画法与头身比、
// 脸/发型/服装/记号照抄参考图；`ChibiDeriveDenoise = 0.75`；出图后 `qc.IdentityJudge` 拿定妆照判身份、
// 不过就换种子重出一轮）—— 方案里那张 Q 版卡（`char_xxx_q`）走的就是它。
// **唯独自动支线漏接了**：`stages_assets.go` 里生成 `char:<本体id>_chibi` 时直接
// `comfyGen.GenerateImage(...)`（纯文生图、无参考图、写死 1024×1024）。
//
// 这条门钉两件事（一条源码级接线 + 一条行为级语义），因为它管的正是"口径没接线"那类错：
//
//	① 自动支线必须调 `c.genSheetImage(`（唯一出口），且该文件里**不许**再出现直调 `comfyGen.GenerateImage(`；
//	② 画幅必须是**成片画幅**：Q 版图会被挂进内心镜的参考图位（`refs.go` 的 `character_chibi`），
//	   而参考图与成片同比例是既有设计（`asset_size_test.go`）—— 原先写死的 1024×1024 方图是**反例**。
func TestAutoChibiDerivesFromMasterSheet(t *testing.T) {
	// 判据只看**代码行**：注释里会（也应当）写着这条路的历史名字 —— 上一轮栽过
	// "断言命中注释里的同名字"（AGENTS 的摆设守卫那条：判据要看**生效的那一行**）。
	code := codeLines(readSrc(t, "stages_assets.go"))
	if !strings.Contains(code, "c.genSheetImage(ctx, comfyGen, sheetJob{") {
		t.Errorf("自动 Q 版必须走唯一出口 `genSheetImage`（它内部才会以定妆照为参考图派生）：\n" +
			"找不到 `c.genSheetImage(ctx, comfyGen, sheetJob{`")
	}
	// 直调文生图这条路必须**整条消失**（否则"两条路各写一份路线判断"的老毛病就回来了）。
	if strings.Contains(code, "comfyGen.GenerateImage(") {
		t.Errorf("自动 Q 版不许再直接文生图（把长相重新描述一遍 ⇒ 「像但不完全像」的第二张脸）")
	}
	// 画幅：不许再写死 1024×1024；必须是成片画幅。
	if strings.Contains(code, "1024, 1024") {
		t.Errorf("Q 版不许再写死 1024×1024 —— 它会被挂进参考图位，必须与成片同画幅")
	}
	if !strings.Contains(code, `cw, chh := assetImageSize(proj, "characters")`) {
		t.Errorf("自动 Q 版的画幅要取成片画幅（assetImageSize(proj, \"characters\")）")
	}
}

// codeLines 去掉**整行注释**后的源码（判据只看生效的代码行）。
func codeLines(src string) string {
	var b strings.Builder
	for _, ln := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		b.WriteString(ln)
		b.WriteString("\n")
	}
	return b.String()
}

// TestAutoChibiJobShapeDerivesAcrossI2I 真机形态的那一份 job（id/名/画幅都照自动支线）
// 走一遍唯一出口：**必须走图生图**、必须带上 Q 版派生指令、参考图必须是本体定妆照、种子必须锚定。
//
// 夹具用自动支线真正会传进来的那一组值（`char_01_chibi` / `青禾 · Q版` / 成片画幅），
// 并把断言打在**引擎收到的参数**上（`fakeSheetEngine` 三个记录字段）——
// 只看"出了图"是摆设：那条路走到文生图也照样出图。
func TestAutoChibiJobShapeDerivesAcrossI2I(t *testing.T) {
	rig := newSheetRig(t)
	eng := &fakeSheetEngine{}
	master, assets := rig.masterSheet(t, "char_01", "青禾")
	// `hasBaseCard` 先看方案（只有资产表里有图、方案里却没这个人，按平台判据算"有中点 ≠ 有主卡"）。
	if err := rig.store.SavePlan(rig.proj.ID, &project.Plan{
		Characters: []project.Character{{ID: "char_01", Name: "青禾"}},
	}); err != nil {
		t.Fatal(err)
	}
	w, h := assetImageSize(rig.proj, "characters")
	j := sheetJob{
		proj: rig.proj, assets: assets, kind: "characters",
		id: "char_01_chibi", name: "青禾 · Q版", desc: "青禾本人的Q版形象",
		prompt: "a small chibi version of the character", ek: KindChibi, w: w, h: h,
		seed: nameSeed("chibi", "char_01"),
	}
	path, err := rig.c.genSheetImage(context.Background(), eng, j)
	if err != nil {
		t.Fatal(err)
	}
	// ① 必须走派生（图生图），一次文生图都不许有
	if eng.t2i != 0 {
		t.Errorf("自动 Q 版退回文生图了（t2i=%d）—— 那就是「像但不完全像」的第二张脸", eng.t2i)
	}
	if eng.i2i == 0 {
		t.Fatalf("自动 Q 版没走图生图（i2i=0）：说明参考图没解析到或路线判错")
	}
	// ② 派生指令必须在提示词里（幂等判据是 ChibiDeriveMarker，不叠第二遍）
	if !strings.Contains(eng.lastPrompt, ChibiDeriveMarker) {
		t.Errorf("提示词里没有 Q 版派生指令（%q）：\n%s", ChibiDeriveMarker, eng.lastPrompt)
	}
	if n := strings.Count(eng.lastPrompt, ChibiDeriveMarker); n != 1 {
		t.Errorf("派生指令叠了 %d 遍（幂等破了）：\n%s", n, eng.lastPrompt)
	}
	// ③ 参考图必须是**本体定妆照**（不是别人的、也不是空的）
	if filepath.Clean(eng.lastRef) != filepath.Clean(master) {
		t.Errorf("派生参考图不是本体定妆照：得到 %q，期望 %q", eng.lastRef, master)
	}
	// ④ 种子锚定（同角色重跑得到同一种子；换出图走「重新生成」那条随机路）
	if eng.lastSeed != nameSeed("chibi", "char_01") {
		t.Errorf("种子没锚定：得到 %d，期望 %d", eng.lastSeed, nameSeed("chibi", "char_01"))
	}
	// ⑤ 落盘位置与资产键的契约没变（老行为：assets/characters/characters_char_01_chibi.png）
	want := filepath.Join(rig.store.Dir(rig.proj.ID), "assets", "characters", "characters_char_01_chibi.png")
	if filepath.Clean(path) != filepath.Clean(want) {
		t.Errorf("落盘路径变了：得到 %q，期望 %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("出图文件不在：%v", err)
	}
	// ⑥ 主卡在图、方案里也有本体 ⇒ 不许出现"主卡缺图…退回文生图"的降级播报
	if m := rig.findMessage("主卡缺图"); m != "" {
		t.Errorf("主卡在图、方案里也有本体，不该降级：%s", m)
	}
}
