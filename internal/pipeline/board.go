package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// board.go 角色卡的**三视图细节板**（production reference sheet）。
//
// ## 依据
//
//   - 官方 MiniMax-AI/MiniMax-H3 `skills/3d-animation-short-generator` STEP 3 Character Cards：
//     角色卡应是一张 16:9 的 production reference sheet —— 3/4 视图、正/侧/背三视图、
//     表情、材质与服装细节、道具细节，并"在提示词里重复身份锁 + 一段简短的视觉 ID 说明"。
//   - 技能侧整改单 `docs/技能侧交付-平台侧整改单-2026-09-27.md` §2.2：增补一类资产
//     `角色卡-<名>-三视图.png` ＝ 一张板（3 全身 ＋ 6 头角度 ＋ 6 局部细节，纯白底、平视、
//     中性棚拍光），用途是**人审一致性**与**建模向参考**（发丝走向/服装结构/道具细节）。
//
// ## 三条硬约束（为什么这样写）
//
//  1. **身份源不换**：板子由**已通过的定妆照**经图像编辑派生（`EditImage`，与多视角/造型档
//     同一条链路），绝不重新文生图 —— 重新描述一遍长相必然得到"像但不完全像"的第二张脸，
//     拿它审一致性等于审了个寂寞（与 sheetderive.go 的变体卡同一条教训）。
//  2. **不参与镜头参考**：板子是给人看的、给建模用的，**不是 H3 的参考图**。它上面有 3 个全身
//     ＋ 6 个头 ＋ 6 个细节，喂进 Ref2V 会与"身份照抄定妆照"打架。`refs.go` 只认
//     `<base>.png` 与 `<base>_turnaround.png`，`_board` 不在其中 —— 这条由
//     `refs_test.go` 的用例盯着。
//  3. **板上不画字**：官方那张卡带角色名与角色定位标签，而我们的图像模型画任何文字都几乎
//     必然成乱码字形（与场景卡汉字门同一条机理，见 project.StripCJK 的注释）。所以标签
//     不进画面：身份说明留在提示词与库条目里（`description` / `image_prompt`）。
//
// ## 成本与触发
//
// 一次图像编辑（数秒~数十秒），比多视角的 H3 转身视频便宜一个量级；在资产阶段**定妆照之后**
// 生成，缺板或板比定妆照旧时重出（见 `genCharacterBoard`），失败只告警、不阻断资产阶段。

// BoardSuffix 三视图细节板的文件名后缀（与 `_turnaround` / `_view_*` 并列的关联图）。
//
// 加这个后缀时**必须同步三处**，否则板子会"生成了却看不见/换脸后留着旧板"：
// `library.relatedImages`（入库收集）、`library.ClearRelated`（换图清理）、
// `RelatedSuffixName`（库→项目改名）。有契约测试盯着（见 board_test.go）。
const BoardSuffix = "_board"

// BoardMarker 幂等判据：板子指令已在这段提示词里就不再叠加（与 SheetDeriveMarker 同一套做法）。
const BoardMarker = "PRODUCTION REFERENCE SHEET"

// BoardLayoutClause 版面要求（英文，逐字进提示词）。
//
// 逐条对应官方 STEP 3 的清单（3/4 视图 + 正侧背 + 表情/头角度 + 材质服装道具细节 + 身份锁），
// 以及技能侧 §2.2 的三段式数量（3 全身 + 6 头角度 + 6 局部细节）。
const BoardLayoutClause = "one single production reference sheet of this ONE character on a plain light neutral " +
	"background under flat, even studio lighting — no scene, no other characters, no props other than this " +
	"character's own: a three-quarter view first, then FRONT, SIDE and BACK full-body views of the same person " +
	"at the same scale in the same standing pose, then a row of six head studies (front, three-quarter, profile, " +
	"three-quarter back, slightly low angle, slightly high angle) showing the same face and the same hairstyle, " +
	"then six close-up detail insets (hair strand direction, costume structure and stitching, collar and sleeve " +
	"construction, sash and belt knots, footwear, hands and the character's signature prop). " +
	"Every identity feature must stay exactly as in the reference image: same face shape, same hair (tie, length " +
	"and forehead state), same costume, same colours, same signature marker."

// BoardFraming 板的取景句（交给 rewriteSheetFraming 替换掉人像取景）。
//
// 为什么必须替换而不是追加：定妆照提示词的尾巴是"半身胸像、整个头在画面内"这类人像取景，
// 与"一张含 3 个全身 + 12 个小图的多视图板"直接打架，模型只会二选一地糊
// （与场景卡被追加"空场"却仍写着人群是同一条机理）。
const BoardFraming = "a complete character production reference sheet: a multi-view turnaround board " +
	"containing full-body views, head studies and detail insets of the same character, " +
	"plain neutral background, flat even studio lighting, no scene and no lettering"

// BoardPrompt 把已装配好的定妆照提示词改写成**板子**的提示词。
//
// 实现落在 `prompts.go` 的 `BoardImagePrompt`（装配模块）：本文件只放**数据**（版面句、
// 取景句、后缀、画幅算法）。这条分工不是洁癖 —— 平台有一条源码级守卫
// （`TestPortraitAssemblyHasOneImplementation`）在盯着"取景重写只能发生在装配点里"，
// 随手在别处拼一遍正是"同一个角色几张脸/几种取景"复发的入口。
func BoardPrompt(portraitPrompt string) string { return BoardImagePrompt(portraitPrompt) }

// BoardSize 板的画幅：**横版**（官方要求 16:9 production reference sheet）。
//
// 项目画幅通常是竖版（768×1344），板子必须转成横版 —— 否则 3 个全身并排 + 两行小图
// 在竖幅里只能挤成邮票。长边取项目长边（保持同一像素预算），短边按 16:9 折算并对齐到 32 的倍数
// （扩散模型的分块约束，与渲染侧同一套网格）。
func BoardSize(w, h int) (int, int) {
	long, _ := w, h
	if h > w {
		long = h
	}
	short := long * 9 / 16
	if short < 320 {
		short = 320
	}
	short = short / 32 * 32
	if short <= 0 {
		short = 704
	}
	return long / 32 * 32, short
}

// BoardOutPath 由定妆照路径推导板子路径（同目录同主名 + `_board`）。
func BoardOutPath(masterPath string) string {
	ext := filepath.Ext(masterPath)
	return strings.TrimSuffix(masterPath, ext) + BoardSuffix + ext
}

// HumanoidBoardKind 哪些实体类型出板子。
//
// 只给人形两类：人（human）与 Q 版（chibi）—— 官方那张板的用途是"发丝走向/服装结构/
// 道具细节"的建模向参考。器物/灵宠/妖兽/无实体已经有各自的多视角/转台图（turnaround.go），
// 再套一张"3 个全身 + 6 个头"的板子只会画出三只鸟排排站。
func HumanoidBoardKind(ek EntityKind) bool {
	switch NormalizeEntityKind(string(ek)) {
	case KindHuman, KindChibi:
		return true
	}
	return false
}

// boardStale 板子是否需要（重）出：缺文件，或板比定妆照旧（定妆照重出过 → 板子已经过时）。
//
// 为什么按 mtime 而不是"只在首次生成时出"：定妆照会因朝向自检未过而**换种子重出**
// （stages_assets.go 的 regenerateOnBadFacing），那张重出的脸与旧板不是同一个人；
// 只判"文件在不在"就会留下一个骗人的旧板。
func boardStale(master, board string) bool {
	mi, err := os.Stat(master)
	if err != nil {
		return false
	}
	bi, err := os.Stat(board)
	if err != nil {
		return true
	}
	return mi.ModTime().After(bi.ModTime())
}

// BoardOptions 一次三视图板生成。
type BoardOptions struct {
	Master     string // 已通过的定妆照绝对路径（身份源）
	Prompt     string // 已装配好的定妆提示词（会被 BoardPrompt 改写成板的提示词）
	Out        string // 目标路径；空 = BoardOutPath(Master)
	Seed       int64
	OnProgress func(percent int, note string)
}

// GenerateBoard 生成/重出三视图细节板，返回产物路径。
//
// 只走**图像编辑**（从定妆照派生）：拿不到编辑链路就**如实报错**，不悄悄退回文生图 ——
// 退回文生图等于再画一张脸，而板子的全部价值就在于"是同一个人的多视图"。
func GenerateBoard(ctx context.Context, ce engine.Engine, o BoardOptions) (string, error) {
	if strings.TrimSpace(o.Master) == "" {
		return "", fmt.Errorf("三视图板:缺少定妆照（身份源）")
	}
	if _, err := os.Stat(o.Master); err != nil {
		return "", fmt.Errorf("三视图板:读不到定妆照 %s:%w", filepath.Base(o.Master), err)
	}
	ee, isComfy := ce.(*comfyengine.Engine)
	if !isComfy {
		return "", fmt.Errorf("三视图板需要本地 ComfyUI 的图像编辑能力（当前引擎 %s 不支持）", ce.Name())
	}
	if missing := ee.EditReady(ctx); len(missing) > 0 {
		return "", fmt.Errorf("三视图板:图像编辑链路未就绪（%s）", strings.Join(missing, "；"))
	}
	out := o.Out
	if out == "" {
		out = BoardOutPath(o.Master)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	return ee.EditImage(ctx, comfyengine.ImageEditOptions{
		RefImages:  []string{o.Master},
		Prompt:     BoardPrompt(o.Prompt),
		Negative:   imageNegative,
		Seed:       o.Seed,
		UseTurbo:   true, // 只对旧家族(2511)有效；2.1 无对应 LoRA，引擎会忽略
		OnProgress: o.OnProgress,
	}, out)
}

// genCharacterBoard 三视图板的**项目侧调用点**（资产阶段与渲染前补齐共用）。
//
// 增强项语义：失败只告警，绝不阻断资产阶段（与多视角转台图同一条政策 —— 定妆照才是必需的）。
// 触发条件：人形角色，且「板不存在」或「板比定妆照旧」（定妆照因朝向自检换种子重出过，
// 旧板已经是另一个人的脸）。已经是最新的板不重出，这条让"续跑/重进项目"不烧重复 GPU。
func (c *Coordinator) genCharacterBoard(ctx context.Context, proj *project.Project, ce engine.Engine, name, imagePrompt, masterPath string, ek EntityKind) {
	if ctx.Err() != nil || !HumanoidBoardKind(ek) || strings.TrimSpace(masterPath) == "" {
		return
	}
	out := BoardOutPath(masterPath)
	if !boardStale(masterPath, out) {
		return
	}
	w, h := assetImageSize(proj)
	bw, bh := BoardSize(w, h)
	c.emit(proj.ID, PhaseAssets, 0, "info", 0,
		"生成:「%s」三视图细节板（人审一致性 / 建模向参考，%d×%d 横版）…", name, bw, bh)
	p, err := GenerateBoard(ctx, ce, BoardOptions{
		Master: masterPath, Prompt: imagePrompt, Out: out, Seed: nameSeed("board", name),
		OnProgress: func(pct int, note string) {
			c.emit(proj.ID, PhaseAssets, 0, "info", 0, "三视图板「%s」:%s(%d%%)", name, note, pct)
		},
	})
	if err != nil {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "三视图板生成失败（不影响定妆照与渲染）:%v", err)
		return
	}
	c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ 三视图板已生成:%s", filepath.Base(p))
}
