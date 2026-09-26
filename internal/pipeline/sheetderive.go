package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// sheetderive.go 造型变体的定妆照**派生**(技能侧《平台侧整改清单》§4.1)。
//
// ## 问题
//
// 技能侧把"造型随剧情变"落成**一角色多张卡**:主卡 + `翻篇·归零` / `糖球·雨夜` /
// `老墙·认错`……(《翻篇》实测 12 → 21 张卡,按场自动切换;声线与 sid 继承主卡)。
// 而平台的资产链原本是**每张卡各自文生图** —— 同一个人的几张脸会漂,正是
// refmanifest.go 注释里记着的"同一角色逐镜不同人"。
//
// 契约(技能侧 `retention_analysis`)本来就要求"照抄参考图锁造型",所以变体的一致性
// 只能由平台侧兜住:变体卡**以同族主卡为参考图派生**,而不是重新描述一遍长相。
//
// ## 做法(两条现成路线,不新造链路)
//
// 派生的出图指令写死技能侧给的那一句(SheetDeriveClause),拼接顺序是
// **换装指令在前、风格与取景在后**(见 SheetDerivePrompt);出图路线按引擎实际能力选
// (chooseSheetRoute):
//
//	编辑(首选)   comfyengine.EditImage —— 改你给的这张图,身份/配色/画风按像素保住。
//	              这不是"另一条新链路":多视角自 2026-09-24 起默认就走它,而"换服装"
//	              与"换视角"是同一类操作(只改一处、其余照抄)。本机图像家族是
//	              qwen_image_2_1,而 imagegen.go 写明 GenerateImageFrom 只支持老式
//	              CLIPTextEncode 家族(Krea-2/Z-Image)—— Qwen 的参考图**只能走编辑图**
//	              (见 imageedit.go 的 buildEditGraphQwen21:参考图直接进文本编码器)。
//	图生图        engine.GenerateImageFrom —— 编辑链路没就绪(权重没下齐/引擎不支持
//	              编辑)时的兜底,也是任务书点名的那个入口。denoise 见 SheetDeriveDenoise。
//	文生图        两条都不行 → **如实降级并播报**(不许静默:静默的后果就是"几张脸"复活)。
//
// ## 闭环判据
//
// 技能侧原话:同一角色的两张变体卡之间,脸型/毛色/印记应能过平台**已有的逐帧身份校验**;
// 过不了就重出变体。所以这儿复用 qc.IdentityJudge(多视角那条链的同一份实现)对
// "主卡 vs 新出的变体卡"判一次,不过就换种子重出(沿用 verifyAttempts 那套"重试一轮"
// 的既有语义),再不过则**如实告警留给人看**(不像多视角那样拒绝交付:造型档本身是
// 必需的 —— 没有它这一场戏就没有定妆照,退回文生图只会更不一致)。

// SheetDeriveClause 变体卡的**派生指令**(写死技能侧 `封面工艺卡 §2.5` 的后备路径原句)。
const SheetDeriveClause = "Change ONLY the costume / the stated state. Keep the face, muzzle shape, " +
	"fur colour and markings EXACTLY as the reference."

// SheetDeriveMarker 幂等判据:派生指令已经在提示词里就不再叠加。
const SheetDeriveMarker = "Change ONLY the costume / the stated state."

// SheetDeriveDenoise 变体卡走**图生图**路线时的去噪强度。
//
// 依据(不是拍的):imagegen.go 记着实测口径 —— 0.6–0.75 是"保身份"的一档
// ("0.7 已实测:视角成功转为三分之四侧视,而雀斑/发型/服装等身份特征完整保留"),
// 而换装要改的只有服装与状态**一处**,所需自由度比换角度更低;再往下(同一条注释:
// "太低角度几乎不变")画面几乎不动,服装就换不过来、等于白出一张。所以取这一档的
// **下沿** 0.6:在保身份区间内,取对脸扰动最小的那一侧。
// 若真机发现服装没换成功,改这一个常量(全平台只有这一处)。
const SheetDeriveDenoise = 0.6

// sheetRetrySeedStep 校验不过时换种子的步进(质数,与多视角同一套:避开同种子的复现)。
const sheetRetrySeedStep = 7919

// SheetDerivePrompt 变体卡的出图提示词 = 派生指令(在前) + 原样定妆提示词。
//
// 顺序不能反:定妆提示词里的取景/风格块(styleExpand 的介质块、取景重写结果)也带着
// "照抄参考图"这类措辞,排在换装指令之前会把"只改服装"这一条淹没
// (与技能侧 `封面工艺卡 §2.5` 的后备路径同一口径:身份/换装指令在前,风格与取景在后)。
func SheetDerivePrompt(prompt string) string {
	if strings.Contains(prompt, SheetDeriveMarker) {
		return prompt
	}
	return SheetDeriveClause + "\n" + prompt
}

// imageFromEngine 以参考图为起点生成图像的能力(img2img)。
//
// 为什么是**消费者侧窄接口**而不是往 engine.Engine 上加方法:云端引擎没有这个能力,
// 加了就得给它编一个假实现(或者让所有调用方回去断言具体类型 —— 那正是
// engine.Engine 当初把这些能力提为接口方法要解决的问题)。这里只问"你能不能"。
type imageFromEngine interface {
	GenerateImageFrom(ctx context.Context, refImagePath, prompt, neg string, seed int64, w, h int, denoise float64, outPath string) (string, error)
}

// sheetRef 变体卡派生用的参考图。
type sheetRef struct {
	Path string // 空 = 拿不到主卡图,只能退回文生图
	Name string // 主卡名(播报用:让用户知道是拿哪一张派的)
	Via  string // 来源:本项目 / 资产库(播报用)
}

// sheetJob 一次定妆照出图。
type sheetJob struct {
	proj   *project.Project
	assets map[string]project.Asset // 本项目资产表(找同族主卡用;可为 nil)
	kind   string                   // characters / scenes / props
	id     string                   // 资产 id 的实体部分(char_01v4;不是 "char:char_01v4")
	name   string                   // 卡名
	prompt string                   // 已装配好的定妆提示词
	ek     EntityKind               // 生效实体类型(决定身份校验的判据点)
	w, h   int
	seed   int64
}

// sheetVariantTrigger 这张卡要不要走"从主卡派生"。
//
// id 带档位号(`char_01v4`)是机器写的,最可靠;**id 认不出来时才退回名字里的档名**
// (`翻篇·归零`)。Q 版一律不走:平台给 Q 版资产起的名字叫「青禾 · Q版」,名字里也有
// 「·」,但它不是同一个人换了衣服 —— 拿真人定妆照去派生 Q 版形象是错的。
func sheetVariantTrigger(id, name string, ek EntityKind) bool {
	if NormalizeEntityKind(string(ek)) == KindChibi {
		return false
	}
	if project.CharVariantNumberOf(id) > 0 {
		return true
	}
	return project.CharVariantLabel(name) != ""
}

// sheetDeriveRoute 变体卡的出图路线。
type sheetDeriveRoute int

const (
	routeText2Img sheetDeriveRoute = iota // 没有可用参考图 / 引擎两种派生都做不了
	routeImg2Img                          // 图生图(以参考图为潜空间起点)
	routeEdit                             // 图像编辑(改这张图:身份按像素保住)
)

// chooseSheetRoute **纯函数**:按引擎能力选出图路线,便于穷举(判错的两种后果都很难
// 事后发现:该编辑时走了图生图会拿到一张"像但不完全像"的脸;该图生图时走了文生图
// 则直接漂成另一个人)。
//
//	editReady  编辑链路(编辑底模+CLIP+VAE 都在,且引擎可达)是否就绪
//	i2iCapable 引擎是否支持以参考图为起点生成
func chooseSheetRoute(editReady, i2iCapable bool) sheetDeriveRoute {
	if editReady {
		return routeEdit
	}
	if i2iCapable {
		return routeImg2Img
	}
	return routeText2Img
}

func (r sheetDeriveRoute) label() string {
	switch r {
	case routeEdit:
		return "图像编辑派生(只换服装/状态,身份按像素保住)"
	case routeImg2Img:
		return "图生图派生"
	}
	return "文生图"
}

// familyMasterInAssets 在本项目资产表里找同族里**档位最靠前**的那张卡(排除自己)。
//
// 两个家族口径**任一命中**即算一家人(id 口径与名字口径,见 project.CharFamilyKeys):
// 技能侧的 id 是自动编号、名字是手写档名,两边理论上一致,但一旦有一边不规范,
// 只认单边就会漏判 —— 而漏判的后果(变体卡退回文生图)正是本函数要防的事。
//
// 排序权重:档位号小的优先(`char_01v1` 先于 `char_01v4`),同档按名字序稳定挑选。
func familyMasterInAssets(assets map[string]project.Asset, selfAssetID, idKey, nameKey string) (project.Asset, bool) {
	var best project.Asset
	bestRank, bestName, found := 0, "", false
	for aid, a := range assets {
		if aid == selfAssetID || !strings.HasPrefix(aid, "char:") || strings.TrimSpace(a.File) == "" {
			continue
		}
		cid := strings.TrimPrefix(aid, "char:")
		cIDKey, cNameKey := project.CharFamilyKeys(cid, a.Name)
		if !(idKey != "" && cIDKey == idKey) && !(nameKey != "" && cNameKey == nameKey) {
			continue
		}
		rank := project.CharVariantNumberOf(cid)
		if !found || rank < bestRank || (rank == bestRank && a.Name < bestName) {
			best, bestRank, bestName, found = a, rank, a.Name, true
		}
	}
	return best, found
}

// resolveSheetRef 变体卡的派生参考图:**本项目优先,其次资产库**。
//
// 顺序的理由:同一集里往往同时用到同一角色的好几档(技能侧按场切换),本条目的主卡
// 图就在资产表里、且是"本集这张脸"的权威;跨集时(第一集定的妆、本集只用到第 4 档)
// 本集根本没有主卡,只能去资产库取。
func (c *Coordinator) resolveSheetRef(j sheetJob) sheetRef {
	idKey, nameKey := project.CharFamilyKeys(j.id, j.name)
	if idKey == "" && nameKey == "" {
		return sheetRef{}
	}
	if a, ok := familyMasterInAssets(j.assets, "char:"+j.id, idKey, nameKey); ok {
		if p := filepath.Join(c.store.Dir(j.proj.ID), a.File); fileReadable(p) {
			return sheetRef{Path: p, Name: a.Name, Via: "本项目"}
		}
	}
	if c.lib != nil {
		if e, img, ok := c.lib.FindInFamily("characters", j.name); ok && fileReadable(img) {
			return sheetRef{Path: img, Name: e.Name, Via: "资产库"}
		}
	}
	return sheetRef{}
}

// fileReadable 文件在且非空(参考图为空文件时图生图必然失败,提前判掉)。
func fileReadable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

// genSheetImage 定妆照出图的**唯一出口**:产线资产阶段、渲染前补齐、单资产重生成
// 三条路共用。变体卡走派生(见文件头),其余照旧一次文生图。
//
// 降级必须**播报**:主卡缺图时静默退回文生图,用户看到的会是"这个角色换了一集就
// 换了一张脸",而日志里一个字都没有。
func (c *Coordinator) genSheetImage(ctx context.Context, ce engine.Engine, j sheetJob) (string, error) {
	// 造型档只存在于**角色**里:场景/道具没有"同一个人换档"这回事
	// (一个叫「临水城·夜」的场景不该被当成某人的造型档去派生)。
	if j.kind != "characters" || !sheetVariantTrigger(j.id, j.name, j.ek) {
		return c.genPlainSheet(ctx, ce, j)
	}
	ref := c.resolveSheetRef(j)
	if ref.Path == "" {
		c.emit(j.proj.ID, PhaseAssets, 0, "warn", 0,
			"⚠ 主卡缺图:造型档「%s」找不到同族参考图(主卡未生成/库中无同族条目),"+
				"本卡退回文生图,与主卡的相似度不保证", j.name)
		return c.genPlainSheet(ctx, ce, j)
	}
	return c.deriveVariantSheet(ctx, ce, j, ref)
}

// genPlainSheet 一次普通文生图(原 genAssetImage 的行为,一行不变)。
func (c *Coordinator) genPlainSheet(ctx context.Context, ce engine.Engine, j sheetJob) (string, error) {
	out := filepath.Join(c.store.Dir(j.proj.ID), "assets", j.kind,
		director.SanitizeForCache(j.kind+"_"+j.id)+".png")
	return ce.GenerateImage(ctx, j.prompt, imageNegative, j.seed, j.w, j.h, out)
}

// deriveVariantSheet 变体卡的派生 + 身份校验闭环(见文件头)。
func (c *Coordinator) deriveVariantSheet(ctx context.Context, ce engine.Engine, j sheetJob, ref sheetRef) (string, error) {
	out := filepath.Join(c.store.Dir(j.proj.ID), "assets", j.kind,
		director.SanitizeForCache(j.kind+"_"+j.id)+".png")
	// 路线选择:编辑链路是否就绪要问具体引擎(云端/非 ComfyUI 一律按"没有编辑"处理)。
	ee, isComfy := ce.(*comfyengine.Engine)
	editReady := false
	editMissing := []string(nil)
	if isComfy {
		editMissing = ee.EditReady(ctx)
		editReady = len(editMissing) == 0
	}
	i2i, i2iCapable := ce.(imageFromEngine)
	route := chooseSheetRoute(editReady, i2iCapable)
	if route == routeText2Img {
		cause := "当前引擎不支持以参考图出图(需要本地 ComfyUI)"
		if isComfy && len(editMissing) > 0 {
			cause = "编辑链路未就绪(" + strings.Join(editMissing, ";") + ")"
		}
		c.emit(j.proj.ID, PhaseAssets, 0, "warn", 0,
			"⚠ 主卡有图但%s:造型档「%s」退回文生图,与主卡的相似度不保证", cause, j.name)
		return c.genPlainSheet(ctx, ce, j)
	}
	prompt := SheetDerivePrompt(j.prompt)
	// 身份闸门:与多视角共用同一份实现(qc.IdentityJudge)与同一套"重试一轮"的语义。
	verifier := TurnaroundVerifier(c.cfg.Get().VLM)
	attempts := verifyAttempts(TurnaroundOptions{Verify: verifier})
	prof := EntityProfileOf(j.ek)
	label := EntityKindLabel(NormalizeEntityKind(string(j.ek)))
	if NormalizeEntityKind(string(j.ek)) == "" {
		label = "人"
	}
	if verifier != nil {
		c.emit(j.proj.ID, PhaseAssets, 0, "info", 0,
			"造型档「%s」按主卡「%s」(%s)派生:%s…", j.name, ref.Name, ref.Via, route.label())
	} else {
		c.emit(j.proj.ID, PhaseAssets, 0, "info", 0,
			"造型档「%s」按主卡「%s」(%s)派生:%s —— 未配置视觉模型,本次不做「与主卡是否同一%s」的校验",
			j.name, ref.Name, ref.Via, route.label(), label)
	}

	last := ""
	lastReason := ""
	for attempt := 1; attempt <= attempts; attempt++ {
		seed := j.seed + int64(attempt-1)*sheetRetrySeedStep
		if attempt > 1 {
			c.emit(j.proj.ID, PhaseAssets, 0, "info", 0,
				"造型档「%s」第 %d 次重出(上一次与主卡不一致:%s)", j.name, attempt, lastReason)
		}
		var (
			path string
			err  error
		)
		if route == routeEdit {
			path, err = ee.EditImage(ctx, comfyengine.ImageEditOptions{
				RefImages: []string{ref.Path},
				Prompt:    prompt,
				Seed:      seed,
				// UseTurbo 只对旧家族(2511)有 Lightning 4 步 LoRA;2.1 无对应 LoRA,
				// 这个标志会被引擎忽略(见 ImageEditOptions 的字段说明)。
				UseTurbo: true,
				// 0 = 交给引擎的**图像级**兜底(10 分钟),不是单镜视频的 45 分钟。
				Timeout: 0,
				OnProgress: func(p int, note string) {
					c.emit(j.proj.ID, PhaseAssets, 0, "info", 0, "造型档「%s」:%s(%d%%)", j.name, note, p)
				},
			}, out)
		} else {
			path, err = i2i.GenerateImageFrom(ctx, ref.Path, prompt, imageNegative, seed, j.w, j.h, SheetDeriveDenoise, out)
		}
		if err != nil {
			return "", err
		}
		last = path
		if verifier == nil {
			return path, nil
		}
		rep, jerr := qc.IdentityJudge(ctx, verifier, ref.Path, []string{path}, 0, prof.JudgePoints, label)
		if jerr != nil {
			c.emit(j.proj.ID, PhaseAssets, 0, "warn", 0,
				"造型档校验不可用(%v):本次按未校验交付,建议在设置页配好视觉模型", jerr)
			return path, nil
		}
		if rep.Pass {
			c.emit(j.proj.ID, PhaseAssets, 0, "success", 0,
				"✔ 造型档「%s」与主卡「%s」身份校验通过:%s", j.name, ref.Name, rep.Reason)
			return path, nil
		}
		lastReason = rep.Reason
	}
	// 全部尝试仍不一致:**保留最后一张并如实告警**(不像多视角那样拒绝交付 —— 造型档
	// 本身是必需的,退回文生图只会让"几张脸"更严重),由人决定要不要重出。
	c.emit(j.proj.ID, PhaseAssets, 0, "warn", 0,
		"⚠ 造型档「%s」与主卡「%s」身份校验未通过(%d 次尝试):%s —— 已保留最后一张;建议对这张卡点「重新生成」,"+
			"或核对主卡「%s」的定妆照是否本身就跑偏了", j.name, ref.Name, attempts, lastReason, ref.Name)
	return last, nil
}
