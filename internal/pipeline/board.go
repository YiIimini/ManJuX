package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/ff"
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
//
// 【2026-09-28 用户报「不要出现莫名的文字，咋还是有」—— 元凶就是这一串】
// 这里原来写着 "no text, no letters, no numerals, no watermark, no logo, no UI, no like/save
// buttons, no social-media chrome, no screenshot framing"。看着是在禁文字，**实际是在教模型画字**：
// 图片侧跑的是蒸馏模型（CFG 1.0），实测**对否定词免疫、却会把名词当指令画出来** ——
// "no epaulettes" 直接画出肩章（见 uniformCivilianClarify 与 artstyle_test.go 的回归口径）。
// 于是 "no text" 画出 text、"no watermark" 画出 watermark —— 用户看到的乱码段落就是这么来的。
//
// 现在整段改写成**正向句**：pure white seamless / blank and unmarked / complete inside the picture /
// clear unobstructed view / natural lifelike proportions。**本常量里一个否定词都不许有**，
// 由 board_test.go 的门盯着。
const BoardLayoutClause = "one single production reference sheet of this ONE character, standing on a pure " +
	"white seamless studio backdrop under flat, even light; every surface stays blank and unmarked, and the " +
	"sheet holds nothing but this character and its own signature prop; generous margins keep every figure " +
	"complete and fully inside the picture: " +
	"LEFT (main area): three full-body standing views — FRONT, LEFT PROFILE and BACK of the same character " +
	"(identical face, hair, costume, build and height proportions), arms hanging naturally, eye-level, " +
	"a clear unobstructed view of each figure, natural lifelike proportions, hair parting and strand " +
	"direction clearly readable; " +
	"UPPER RIGHT: six head angles of the same character — front face, top-down view of the crown, back of the " +
	"head, left silhouette, near-profile comparison, 3/4 face; " +
	"LOWER RIGHT: six detail tiles — upper-garment fabric, lower-garment front, waist-and-hip tailoring, leg or " +
	"skin detail, eye and facial detail, footwear as a single item; every tile must match the main costume exactly. " +
	"Every identity feature must stay exactly as in the reference image: same face shape, same hair (tie, length " +
	"and forehead state), same costume, same colours, same signature marker."

// BoardFraming 板的取景句（交给 rewriteSheetFraming 替换掉人像取景）。
//
// 为什么必须替换而不是追加：定妆照提示词的尾巴是"半身胸像、整个头在画面内"这类人像取景，
// 与"一张含 3 个全身 + 12 个小图的多视图板"直接打架，模型只会二选一地糊
// （与场景卡被追加"空场"却仍写着人群是同一条机理）。
//
// 同样**只用正向句**（原来结尾的 "no scene and no lettering" 已删，理由见 BoardLayoutClause）。
const BoardFraming = "a complete character production reference sheet: a multi-view turnaround board " +
	"containing full-body views, head studies and detail insets of the same character, " +
	"on a pure white seamless studio backdrop, flat even studio lighting, a clean empty set with " +
	"plain unmarked surfaces"

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
// 三视图细节板的画幅：**固定 16:9 · 2K**。
//
// 为什么不再跟随项目画幅（2026-09-27 用户定调：「三视图做成 16:9 比例，2K」）：
//
//	① 板子不是"一帧画面"而是**一张规格书**（3 个全身 + 6 个头部细节横排），
//	   横版是它的天然形状；项目画幅是竖的（9:16），跟随它只会把板子挤成竖条。
//	② 2K 是为了**细节**：板子从这天起还要顶替转台图当角度参考（见 refs.go 的
//	   character_angles 兜底），喂给 H3 时整张会被缩到入口分辨率 —— 2048 宽下每个
//	   全身仍摊到约 680px，与定妆照自己那张（768 宽）同一量级；照原来按项目长边算
//	   （1344 宽）每个全身只剩约 448px，头部细节更糊，"看板子认发丝走向"这件事就废了。
const (
	// BoardWidth / BoardHeight 2048×1152 = 16:9，两边都是 32 的倍数（采样器友好）。
	BoardWidth  = 2048
	BoardHeight = 1152
)

// BoardSize 板子尺寸（保留 (w,h) 入参只为兼容调用点：板子**不再**跟随项目画幅，理由见上）。
func BoardSize(_, _ int) (int, int) { return BoardWidth, BoardHeight }

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
	case KindHuman, KindAndroid, KindChibi:
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
	if mi.ModTime().After(bi.ModTime()) {
		return true
	}
	// 画幅口径变了也要重出（2026-09-27）：板子从"跟随定妆照"改成固定 **16:9·2K**，
	// 不判这一条的话，盘上那些旧板子会永远是竖的 —— 用户看到的就是「你改了吗？」。
	// 只读像素、不动文件；读不到尺寸（老 jpeg/损坏）就不判，宁可少说一句。
	if w, h := imagePixelSizeOrZero(board); w > 0 && (w != BoardWidth || h != BoardHeight) {
		return true
	}
	return false
}

// BoardOptions 一次三视图板生成。
type BoardOptions struct {
	// CardBoard 技能侧卡片的 oard_prompt（三视图板串）。有它就用它 —— 板子的 15 格内容是
	// 技能侧口径（《平台侧生成规范》§3.4/§3.5），平台自己那份只作兜底。见 BoardPromptWithCard。
	CardBoard string
	Master    string // 已通过的定妆照绝对路径（身份源）
	// Canvas 可选：已备好的 **16:9 白底画布**（定妆照等比居中放进去）。
	//
	// 为什么需要它（2026-09-27）：Qwen-Image-2.1 的编辑链路**按第一张参考图的画幅决定
	// 输出 latent**，所以"板子是 16:9"这件事只能靠"参考图就是 16:9"来实现。
	// 空 = 直接用 Master（老行为：出成定妆照的画幅）。
	Canvas     string
	Prompt     string // 已装配好的定妆提示词（会被 BoardPrompt 改写成板的提示词）
	Out        string // 目标路径；空 = BoardOutPath(Master)
	Seed       int64
	OnProgress func(percent int, note string)
}

// BoardCanvasPath 画布临时文件的落点（与板子同目录同主名，便于一起清理）。
func BoardCanvasPath(boardOut string) string {
	ext := filepath.Ext(boardOut)
	return strings.TrimSuffix(boardOut, ext) + "_canvas" + ext
}

// boardCanvas 把定妆照放进一张 **w×h 白底画布**（等比缩放、居中、不裁切）。
//
// 口径为什么是"白底 + 居中 + 不裁切"：板子最终是一张白底规格书，边缘留白正好给
// "三个全身横排 + 头部细节行"腾地方；而裁切会切掉角色的手臂/衣摆（板子要审的正是这些），
// 拉伸更不行（比例一歪，板子上的人就不是定妆照那个人了）。
func boardCanvas(ctx context.Context, f *ff.FF, master, out string, w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("画布尺寸非法:%d×%d", w, h)
	}
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,"+
		"pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=white", w, h, w, h)
	_, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-i", master, "-vf", vf, "-frames:v", "1", out)
	return err
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
	// 参考图 = 画布（有就优先）：2.1 编辑链按**第一张参考图的画幅**决定输出 latent，
	// 喂 16:9 画布才出得了 16:9 板子；画布本身就是"定妆照居中放进白底"的同一张身份源。
	ref := o.Master
	if strings.TrimSpace(o.Canvas) != "" {
		if _, err := os.Stat(o.Canvas); err == nil {
			ref = o.Canvas
		}
	}
	return ee.EditImage(ctx, comfyengine.ImageEditOptions{
		RefImages:  []string{ref},
		Prompt:     BoardPromptWithCard(o.Prompt, o.CardBoard),
		Negative:   imageNegative,
		Seed:       o.Seed,
		UseTurbo:   true, // 只对旧家族(2511)有效；2.1 无对应 LoRA，引擎会忽略
		OnProgress: o.OnProgress,
	}, out)
}

// scaleImageExact 把图片**归一化到精确像素**（就地覆盖）。用于板子：编辑链只保证画幅比例，
// 不保证像素数（实测要 2048×1152、给 1376×768）。
//
// 为什么交给 ffmpeg：本仓唯一的媒体工具就是它，且这条路已有同一套调用方式（见 boardCanvas）。
func scaleImageExact(ctx context.Context, f *ff.FF, path string, w, h int) error {
	if f == nil || w <= 0 || h <= 0 {
		return fmt.Errorf("缺少 ffmpeg 或目标尺寸非法")
	}
	tmp := path + ".scale" + filepath.Ext(path)
	args := []string{"-y", "-i", path, "-vf", fmt.Sprintf("scale=%d:%d:flags=lanczos", w, h), tmp}
	if _, err := f.Run(ctx, args...); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// assetGenerator 生成一张资产图的回调（资产阶段把它传给朝向自检、撞脸自检等"换种子重出"的路径）。
//
// 为什么立成命名类型：这套参数已经长到 7 个，两处各写一遍完整签名，加一个参数就要改两处；
// 更重要的是**cardBoard 必须一路带到出板那一步**（技能侧卡片的三视图板串，见 BoardPromptWithCard）。
type assetGenerator func(kind, id, name, imagePrompt, cardBoard string, ek EntityKind, attempt int) (string, error)

// BoardPromptWithCard 板提示词：**优先用技能侧卡片的三视图板串**（《平台侧生成规范》§3.4/§3.5），
// 卡里没有（老方案 / 平台自己生成的卡）才退回平台那份兜底布局串。
//
// 【为什么以卡片为准】板子的 15 格内容（3 全身 ＋ 6 头角度 ＋ 6 局部细节）是技能侧定死的口径，
// 而平台兜底那份的"头角度"与"局部细节"两项与它并不一致 —— 两份同时在场迟早走散。
//
// 【卡片板串里的一句被掐掉】技能侧板串写着 `LABELS (for the design sheet): the character name, …`，
// 而**同一条规范 §3.4 明说**板上"不出现文字说明、不出现 LOGO/水印/UI 元素"，串末自己也写着
// `no text, no letters, no watermark` —— 自相矛盾。按 §3.4 与平台"画面文字禁止"的口径，
// 装配时掐掉那一行（见 project.StripBoardLabels），并回报技能侧让他们自己删。
func BoardPromptWithCard(masterPrompt, cardBoard string) string {
	if strings.Contains(masterPrompt, BoardMarker) {
		return masterPrompt // 幂等：装配点可能被调用两次
	}
	if cb := strings.TrimSpace(cardBoard); cb != "" {
		return BoardMarker + " — " + cb
	}
	return BoardImagePrompt(masterPrompt)
}

// genCharacterBoard 三视图板的**项目侧调用点**（资产阶段与渲染前补齐共用）。
//
// 增强项语义：失败只告警，绝不阻断资产阶段（与多视角转台图同一条政策 —— 定妆照才是必需的）。
// 触发条件：人形角色，且「板不存在」或「板比定妆照旧」（定妆照因朝向自检换种子重出过，
// 旧板已经是另一个人的脸）。已经是最新的板不重出，这条让"续跑/重进项目"不烧重复 GPU。
func (c *Coordinator) genCharacterBoard(ctx context.Context, proj *project.Project, ce engine.Engine, id, name, imagePrompt, masterPath, cardBoard string, ek EntityKind) {
	if ctx.Err() != nil || !HumanoidBoardKind(ek) || strings.TrimSpace(masterPath) == "" {
		return
	}
	out := BoardOutPath(masterPath)
	if !boardStale(masterPath, out) {
		return
	}
	// 三视图板是固定 16:9·2K 的规格书（见 BoardSize 的说明），与定妆照画幅无关。
	bw, bh := BoardSize(0, 0)
	c.emit(proj.ID, PhaseAssets, 0, "info", 0,
		"生成:「%s」三视图细节板（角度参考 / 人审一致性 / 建模向，%d×%d 16:9·2K）…", name, bw, bh)
	// **画布**：Qwen-Image-2.1 的编辑链路按第一张参考图的画幅决定输出 latent
	// （见 comfyengine/imageedit.go 的 buildEditGraphQwen21 说明）—— 所以"要一张 16:9 的板子"
	// 只能靠喂一张 16:9 的参考图。只把尺寸写进选项是没用的：Options 此前根本没有尺寸可传，
	// 板子一直跟着定妆照出成 9:16（2026-09-27 用户报「三视图为什么还是 9:16」就是这个）。
	canvas := ""
	if c.ff != nil {
		canvas = BoardCanvasPath(out)
		if err := boardCanvas(ctx, c.ff, masterPath, canvas, bw, bh); err != nil {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "三视图板:画布准备失败，按定妆照画幅出图:%v", err)
			canvas = ""
		} else {
			// 画布是**中间产物**，用完就删：留在资产目录里虽然不会被 lib 的关联图 glob 收走
			// （它只认 `*_board.png`，见 library.go 的 relatedGlobs），但会让人对着两个板子
			// 文件猜哪个是真的。删了下次要重出时重新拼一张（ffmpeg 一次几十毫秒）。
			defer os.Remove(canvas)
		}
	}
	p, err := GenerateBoard(ctx, ce, BoardOptions{
		Master: masterPath, Canvas: canvas, Prompt: imagePrompt, CardBoard: cardBoard, Out: out,
		// 种子锚定键有 id 用 id（2026-09-28 审计 B6）：名字写法一漂种子就变，
		// 板子就会跟着换一张脸，而日志与契约门都看不出问题。
		Seed: nameSeed("board", cardSeedKey(id, name)),
		OnProgress: func(pct int, note string) {
			c.emit(proj.ID, PhaseAssets, 0, "info", 0, "三视图板「%s」:%s(%d%%)", name, note, pct)
		},
	})
	if err != nil {
		c.emit(proj.ID, PhaseAssets, 0, "warn", 0, "三视图板生成失败（不影响定妆照与渲染）:%v", err)
		return
	}
	// 出图后**归一化到规格尺寸**（2026-09-28 实测）：编辑链只保证"画幅比例跟着第一张参考图"，
	// **不保证像素数** —— 本机要求 2048×1152，实测产物是 1376×768（链内部缩了 1.5 倍）。
	// 规格书说是 16:9·2K，就得真的是 2048×1152：否则人审/建模参考/库条目拿到的尺寸
	// 与日志里那句承诺对不上。失败只告警，不丢产物。
	if c.ff != nil {
		bw, bh := BoardSize(0, 0)
		if w, h := imagePixelSizeOrZero(p); w > 0 && (w != bw || h != bh) {
			if err := scaleImageExact(ctx, c.ff, p, bw, bh); err != nil {
				c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
					"三视图板尺寸归一化失败(%d×%d → %d×%d):%v（保留原图，不影响渲染）", w, h, bw, bh, err)
			} else {
				c.emit(proj.ID, PhaseAssets, 0, "info", 0,
					"三视图板尺寸已归一化:%d×%d → %d×%d（编辑链只保证比例，不保证像素）", w, h, bw, bh)
			}
		}
	}
	c.emit(proj.ID, PhaseAssets, 0, "success", 0, "✔ 三视图板已生成:%s", filepath.Base(p))
}
