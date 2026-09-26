package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// 多视角转台图(角色转身参考)生成。
//
// 【两条路,默认走编辑】本文件负责**分流与视频路径**,编辑路径见 viewedit.go:
//   - 编辑路径(默认):Qwen-Image-Edit-2511 拿定妆照本体改角度 —— 身份/配色/画风按像素
//     保住,每张几秒。这是 2026-09-24 之后的默认。
//   - 视频路径(回退):H3 Ref2V 生成一段转身视频再抽帧。它比 Krea-2 img2img 强
//     (Krea-2 是纯文生图模型,没有身份条件注入机制,denoise 0.7 会把角色整个换掉、
//     0.55 则角度几乎不变 —— 模型固有限制,不是调参能解决的),但视频模型会**顺手重新
//     设计**角色:实测赛璐璐少年被画成写实真人、插画铜雀被画成 3D 机械鸟,逐帧身份校验
//     因此正确拒绝、多视角永远拿不到。只在编辑链路未就绪(权重没下/服务离线)时使用。
//
// 产物命名(**全部与定妆照同目录**):
//   - <dir>/<base>_view_threequarter|side|back.png  多视角原图
//   - <dir>/<base>_turnaround.png   横向拼接的转台图(渲染时作该角色的第二张参考)
//   - <videodir>/turnaround.mp4     转身视频本身(体积大且不作参考,默认落在子目录)
//
// 抽帧必须落在定妆照**同目录**而不是 _views/ 子目录:资产库的
// relatedImages/ClearRelated 用 `filepath.Glob(<dir>/<base>_view_*.png)` 收集与
// 清理,glob 不递归 —— 帧放子目录里的结果是「多视角原图既收不进库、也清不掉」。
//
// 本文件供两条链路共用:①资产阶段(pipeline.genCharacterViews)②资产库详情弹窗
// 的「补全多视角」与渲染前自动补齐(library_ops / backfillAssetsViews)。
// 文件名里的 tag 与前端 LibraryView 的 labelOf() 标签(threequarter/side/back)
// 逐字对齐,标签才能显示成「四分之三/侧面/背面」而不是笼统的「多视角」。

// TurnaroundOptions 转台图生成参数。
type TurnaroundOptions struct {
	MasterPath string // 定妆照绝对路径(必填)
	Dir        string // 产物目录;空 = 定妆照所在目录
	Base       string // 产物主名;空 = 定妆照文件主名
	VideoDir   string // 转身视频落地目录;空 = <Dir>/<Base>_views
	TempVideo  bool   // VideoDir 由本函数建临时目录并在结束时清理(库内生成用,不把 30MB 视频留在库里)
	Name       string // 角色名(日志与默认种子)
	Prompt     string // 定妆照提示词(取身份描述用)
	// Kind 实体类型:决定用哪一套"多视角方法" —— 人原地转身、器物转台自转、
	// 妖兽绕行/转身、无实体原地旋动、Q 版 Q 版转身(见 entity_prompts.go)。
	// 零值("")按 human 处理,保证老调用方行为不变。
	Kind EntityKind
	// Source 多视角的生成方式:
	//   "edit" = 图像编辑(Qwen-Image-Edit-2511,首选:改你给的那张图,身份/画风按像素保住)
	//   "h3"   = H3 转身视频抽帧(旧路径;视频模型会顺手重新设计角色,校验多半拒绝)
	//   空 = 自动:配置了编辑模型就用 edit,否则退回 h3。
	Source string
	// Realistic 用户显式要求真人写实(script.stylized_faces 关掉)。
	// **零值是"动漫化"**:与项目策略一致(未表态即开),也让漏传的调用方
	// 拿到的是安全行为(带介质锁)而不是静默丢锁。
	//
	// 为什么必须由调用方传:转身视频是另一次渲染,它拿不到分镜那套
	// styleAndMediumBlock;少了介质锁,H3 会用自己的默认介质重画
	// (实测:赛璐璐角色→写实真人、插画铜雀→写实 3D 机械鸟),逐帧校验必然不通过
	// —— 2026-09-24 用户报「多视角老是生成错误」的根因就是这个。
	Realistic bool
	Steps     int
	Accel     string
	Length    int   // 帧数;0 = 124(约 5.2 秒)
	Seed      int64 // 0 = 按角色名派生稳定种子
	// Verify 视觉模型客户端:非 nil 时逐帧核对"是不是定妆照里那个人",不一致就换种子
	// 重出;全部重试仍不一致则**拒绝使用**(详见 GenerateTurnaround 的说明)。
	Verify         *llm.Client
	VerifyMinScore int // 逐帧最低相似分,0 = 70
	VerifyRetries  int // 不一致时的额外重试次数,默认 1(=最多出 2 次)
	Emit           func(format string, args ...any)
}

// defaultVerifyRetries 「校验不过就换种子重出」的默认次数(= 多出 1 轮,共 2 轮)。
//
// 文档一直写着"默认 1",而三个调用方(stages_assets 定妆照后 / backfillAssetViews /
// 库「补全多视角」)都只传 Verify、没传 VerifyRetries —— 于是 attempts 恒为 1,
// 「换种子重出」那条分支**永远不执行**(死代码)。后果不是崩溃而是静默降级:VLM 只要
// 判一次不合格,该角色本次就丢弃全部角度帧、退回单张正面照,而日志只说"多视角生成
// 失败",用户不知道这本来是一次重试就能拿到的结果。默认值写在注释里等于没有。
const defaultVerifyRetries = 1

// verifyAttempts 本次多视角最多出几轮:未表态(<=0)按默认 1 轮重试兜底;
// 没配视觉模型时恒为 1 轮(判不了"像不像本人",重出没有意义)。
func verifyAttempts(o TurnaroundOptions) int {
	if o.Verify == nil {
		return 1
	}
	retries := o.VerifyRetries
	if retries <= 0 {
		retries = defaultVerifyRetries
	}
	return 1 + retries
}

// GenerateTurnaround 定妆照 → 多视角图 → 校验 → 横向拼转台图(返回转台图绝对路径)。
// 生成方式按 o.Source 分流:默认走图像编辑(见 viewedit.go),回退到 H3 转身视频。
// 任何一步失败都返回错误(由调用方决定告警还是阻断)。
//
// **为什么必须校验后才交付(2026-09-24 用户报「多视角/转台图和参考的定妆照不一致」)**:
// 生成模型(视频路径尤其)有创作自由,可以把人转着转着画成另一个人。旧实现对此毫无
// 察觉,而且这张"另一个人"的拼图会作为该角色的**第二张参考图**喂回渲染 —— 主动把成片
// 的人脸往错误方向拉,比"没有多视角"更糟。所以两条路都装同一道闸门:
//
//  1. 出图之后、拼图之前逐张判"像不像本人"(视觉模型,见 qc.IdentityJudge);
//  2. 不一致就换种子重出(VerifyRetries 次);
//  3. 仍不一致 —— **拒绝交付**:删掉本次产出的角度帧、不生成转台图、返回错误。
//     调用方照旧只告警,于是该角色退回"单张正面照"渲染(锁脸锚点仍在,只是少了
//     其它角度),既不污染一致性也不阻断出片。
//
// 校验需要视觉模型;没配置(VLM 未填)时跳过校验并明确写进日志 —— 不假装校验过。
func GenerateTurnaround(ctx context.Context, ce engine.Engine, f *ff.FF, o TurnaroundOptions) (string, error) {
	if strings.TrimSpace(o.MasterPath) == "" {
		return "", fmt.Errorf("缺少定妆照路径")
	}
	if _, err := os.Stat(o.MasterPath); err != nil {
		return "", fmt.Errorf("定妆照不可读:%w", err)
	}
	// 生成方式分流。编辑路径原地改这张图,不重新设计角色,所以是默认首选;
	// 只在编辑模型缺失(用户没下权重)或引擎不是本地 ComfyUI 时退回视频路径。
	if source, reason := resolveViewSource(ctx, ce, o); source == "edit" {
		return GenerateViewsEdit(ctx, ce, f, o)
	} else if reason != "" {
		if o.Emit != nil {
			o.Emit("%s", reason)
		}
	}

	dir := o.Dir
	if dir == "" {
		dir = filepath.Dir(o.MasterPath)
	}
	base := o.Base
	if base == "" {
		base = strings.TrimSuffix(filepath.Base(o.MasterPath), filepath.Ext(o.MasterPath))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	viewDir := o.VideoDir
	if viewDir == "" {
		viewDir = filepath.Join(dir, base+"_views")
	}
	if o.TempVideo {
		tmp, err := os.MkdirTemp("", "mx-turnaround-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		viewDir = tmp
	}
	if err := os.MkdirAll(viewDir, 0o755); err != nil {
		return "", err
	}
	clip := filepath.Join(viewDir, "turnaround.mp4")

	emit := o.Emit
	if emit == nil {
		emit = func(string, ...any) {}
	}
	length := o.Length
	if length <= 0 {
		length = 124 // 17k+5 网格,约 5.2 秒
	}
	// 本次转身视频的画幅(甲2):与定妆照同尺寸,抽帧后才与主图同比例。
	frameW, frameH := turnaroundFrameSize(o.MasterPath)
	prof := EntityProfileOf(o.Kind)
	kindLabel := EntityKindLabel(NormalizeEntityKind(string(o.Kind)))
	if NormalizeEntityKind(string(o.Kind)) == "" {
		kindLabel = "人"
	}
	turnVerb := o.Kind.TurnVerb()
	baseSeed := o.Seed
	if baseSeed == 0 {
		baseSeed = nameSeed("turnaround", o.Name)
	}
	attempts := verifyAttempts(o)
	if o.Verify == nil {
		emit("多视角:未配置视觉模型,本次不做「与定妆照是否同一%s」的校验", kindLabel)
	}

	// 时间点:1.3s 四分之三 → 2.6s 侧面 → 3.9s 背面。
	// 0s 的正面帧**刻意不抽**:它与定妆照本就是同一张脸同一角度,拼转台图时直接
	// 用定妆照作第一格,不必再占一份磁盘与一个 glob 名额。
	stamps := []struct{ at, tag string }{
		{"1.3", "threequarter"},
		{"2.6", "side"},
		{"3.9", "back"},
	}
	lastReason := ""
	for attempt := 1; attempt <= attempts; attempt++ {
		seed := baseSeed + int64(attempt-1)*7919 // 重试换种子(质数步进,避开同种子的复现)
		if attempt == 1 {
			emit("多视角:「%s」(%s)%s视频生成中(H3 Ref2V,约数分钟)…", o.Name, kindLabel, turnVerb)
		} else {
			emit("多视角:「%s」第 %d 次重出(上一次与定妆照不一致:%s)", o.Name, attempt, lastReason)
		}
		job := engine.Job{
			ProjectID: "turnaround", ShotID: 0,
			Prompt:    prof.TurnaroundPrompt(o.Name, o.Prompt, TurnaroundMediumBlock(o.Kind, o.Realistic)),
			Mode:      "ref2va",
			RefImages: []engine.RefImage{{Kind: "character", Path: o.MasterPath}},
			// 画幅 = 定妆照自身的像素(甲2):转身视频与定妆照同比例,抽帧才能直接拼
			// (旧实现写死 832×1248,与 9:16 的定妆照不同比例,拼图只能靠裁切对齐)。
			Width: frameW, Height: frameH,
			Length:    length,
			Seed:      seed,
			Steps:     o.Steps,
			Accel:     o.Accel,
			OutputDir: viewDir, OutputName: "turnaround",
			OnProgress: func(p int, note string) {
				emit("多视角「%s」:%s(%d%%)", o.Name, note, p)
			},
		}
		if _, err := ce.Render(ctx, job); err != nil {
			return "", fmt.Errorf("转身视频渲染失败:%w", err)
		}
		views := make([]string, 0, len(stamps))
		for _, s := range stamps {
			p := filepath.Join(dir, base+"_view_"+s.tag+".png")
			if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
				"-ss", s.at, "-i", clip, "-map", "0:v:0", "-frames:v", "1", "-update", "1", p); err != nil {
				emit("多视角抽帧失败(%ss):%v", s.at, err)
				continue
			}
			if st, err := os.Stat(p); err == nil && st.Size() > 0 {
				views = append(views, p)
			}
		}
		if len(views) < 2 {
			removeFiles(views)
			return "", fmt.Errorf("多视角可用帧不足(仅 %d 张)", len(views))
		}
		if o.Verify != nil {
			rep, err := qc.IdentityJudge(ctx, o.Verify, o.MasterPath, views, o.VerifyMinScore, prof.JudgePoints, kindLabel)
			if err != nil {
				emit("多视角校验不可用(%v):本次按未校验交付,建议在设置页配好视觉模型", err)
			} else if !rep.Pass {
				lastReason = rep.Reason
				removeFiles(views)
				continue // 换种子重出
			} else {
				emit("✔「%s」多视角校验通过:%s", o.Name, rep.Reason)
			}
		}
		// 转台图 = 正面定妆照 + 抽到的各角度帧(顺序即 正面→四分之三→侧面→背面)
		frames := make([]string, 0, len(views)+1)
		if _, err := os.Stat(o.MasterPath); err == nil {
			frames = append(frames, o.MasterPath)
		}
		frames = append(frames, views...)

		turn := filepath.Join(dir, base+"_turnaround.png")
		if err := hstackImages(ctx, f, frames, turn); err != nil {
			return "", fmt.Errorf("转台图拼接失败(各角度帧已保留在 %s):%w", dir, err)
		}
		emit("✔「%s」转台图已生成(%d 个视角)", o.Name, len(frames))
		return turn, nil
	}
	// 全部尝试都不一致:拒绝交付,并如实说清后果(该角色退回单张正面照)
	emit("✕「%s」多视角校验未通过(%d 次尝试):%s —— 已丢弃本次角度帧,"+
		"该角色本次按单张正面照渲染(宁缺勿错:错误的第二张参考会把%s往错的方向拉)",
		o.Name, attempts, lastReason, kindLabel)
	return "", fmt.Errorf("多视角与定妆照不一致,已拒绝使用:%s", lastReason)
}

// resolveViewSource 决定本次多视角走哪条路,返回 (source, 需要告知用户的原因)。
//
// 为什么要有分流而不是"统一改成编辑路径":编辑路径需要用户真的把 Qwen-Image-Edit 的
// 权重下下来(约 30GB)。没下完就硬走编辑,结果是多视角每次都以"模型未配置"失败,
// 比"用旧路径多花几分钟"更糟。所以规则是:**能走编辑就走编辑**(它才是不会改写角色的那条),
// 走不了才退回视频路径,并且把"为什么退回、怎么升级"明确说出来。
func resolveViewSource(ctx context.Context, ce engine.Engine, o TurnaroundOptions) (string, string) {
	requested := strings.ToLower(strings.TrimSpace(o.Source))
	// 显式指定就照做:用户要 A/B 或明确不要某条路时,不做"自作聪明"的回退。
	if requested == "edit" || requested == "h3" || requested == "video" {
		s, _ := viewSourceDecision(requested, true, nil)
		return s, ""
	}
	ee, ok := ce.(*comfyengine.Engine)
	if !ok {
		return viewSourceDecision(requested, false, nil)
	}
	return viewSourceDecision(requested, true, ee.EditReady(ctx))
}

// viewSourceDecision 纯函数:把"用户要什么"与"编辑链路是否就绪"折算成实际走哪条路。
//
// 抽成纯函数是为了让分流规则可以被穷举测试(见 turnaround_source_test.go)——分流
// 判错的两种后果都很难在事后发现:要么白白多等几分钟视频,要么拿到一张"另一个角色"
// 的角度图并被当作第二张参考喂进渲染。
//
//	requested:   "edit" / "h3" / "video" / ""(auto)
//	editCapable: 引擎是否支持图像编辑(非本地 ComfyUI 一律 false)
//	editMissing: 编辑链路缺什么(空 = 就绪)
func viewSourceDecision(requested string, editCapable bool, editMissing []string) (source, reason string) {
	switch requested {
	case "edit":
		return "edit", ""
	case "h3", "video":
		return "h3", ""
	}
	if editCapable && len(editMissing) == 0 {
		return "edit", ""
	}
	if !editCapable {
		return "h3", "多视角:当前引擎不支持图像编辑(需本地 ComfyUI),本次走 H3 转身视频路径"
	}
	return "h3", fmt.Sprintf(
		"多视角:图像编辑链路还没就绪(%s)—— 本次走 H3 转身视频路径。"+
			"把 Qwen-Image-Edit-2511 的底模/文本编码器/VAE 放进 ComfyUI 的 models 目录后,"+
			"多视角会自动改用编辑路径:一样是这张定妆照,只转角度、不改画风(不再出现"+
			"「转台图是另一个人」)", strings.Join(editMissing, ";"))
}

// assembleTurnaround 把定妆照 + 各角度帧横向拼成转台图(两条多视角路径共用)。
// 抽成函数是为了保证"产物命名与拼法"只有一个实现点 —— 视频路径与图像编辑路径
// 必须产出**一模一样**的文件,否则渲染时的第二张参考会因路径不同而落空。
func assembleTurnaround(ctx context.Context, f *ff.FF, o TurnaroundOptions, views []string) (string, error) {
	dir := o.Dir
	if dir == "" {
		dir = filepath.Dir(o.MasterPath)
	}
	base := o.Base
	if base == "" {
		base = strings.TrimSuffix(filepath.Base(o.MasterPath), filepath.Ext(o.MasterPath))
	}
	frames := make([]string, 0, len(views)+1)
	if _, err := os.Stat(o.MasterPath); err == nil {
		frames = append(frames, o.MasterPath) // 左起第一格 = 正面定妆照
	}
	frames = append(frames, views...)
	turn := filepath.Join(dir, base+"_turnaround.png")
	if err := hstackImages(ctx, f, frames, turn); err != nil {
		return "", fmt.Errorf("转台图拼接失败(各角度帧已保留在 %s):%w", dir, err)
	}
	return turn, nil
}

// removeFiles 尽力删除文件(校验不通过时清掉角度帧,不让它们被资产库扫进去)。
func removeFiles(paths []string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// ClearDerivedViews 作废某资产的全部派生角度图(`<base>_view_*.png` 与 `<base>_turnaround.*`),
// 返回删除数量。被三处共用:
//   - 单资产重生成(换了脸,旧角度图对应上一张脸);
//   - 资产库重抽(library.ClearRelated 管库侧,这边管项目侧);
//   - **改实体类型**(putCharacter):类型决定了取景与多视角方式,旧角度图是按旧类型出的,
//     留着会被当成"该角色的第二张参考"喂回渲染 —— 那正是"错的参考比没有更糟"的场景。
//
// 只删派生图,绝不动定妆照本体(用户可能就是想留着它做对比)。
func ClearDerivedViews(assetPath string) int {
	base := strings.TrimSuffix(assetPath, filepath.Ext(assetPath))
	n := 0
	for _, pat := range []string{base + "_view_*.png", base + "_turnaround.png", base + "_turnaround.*"} {
		ms, err := filepath.Glob(pat)
		if err != nil {
			continue
		}
		for _, m := range ms {
			if os.Remove(m) == nil {
				n++
			}
		}
	}
	return n
}

// TurnaroundVerifier 由视觉模型配置构造多视角校验客户端;未配置返回 nil。
//
// 三处调用方(资产阶段、渲染前补齐、资产库「补全多视角」)共用同一判据:
// 设置页填了 VLM 就校验,没填就跳过并如实告知 —— 不假装校验过。
func TurnaroundVerifier(v config.VLMSettings) *llm.Client {
	if strings.TrimSpace(v.BaseURL) == "" || strings.TrimSpace(v.APIKey) == "" || strings.TrimSpace(v.Model) == "" {
		return nil
	}
	return llm.New(v.BaseURL, v.APIKey, v.Model, 120)
}

// compactIdentity 从定妆照提示词里提取**极简**身份锚点(年龄感+性别+服装),
// 剥掉构图要求与一切可能被模型当成"脸部改写指令"的形容词。
//
// 必须剥构图:定妆照提示词含 front-facing / plain background / upper-body framing,
// 而多视角要的正是"不朝正面" —— 两组指令直接打架,实测是身份漂移的主因之一。
// 2026-09-12 起进一步剥掉面部/皮肤类词汇:长相只认参考图,文字多写一句就多一次
// "模型按文字重画"的机会(红鼻头/腮红即由此而来)。
func compactIdentity(p string) string {
	for _, w := range []string{
		"front-facing", "plain background", "upper-body framing", "upper body framing",
		"upper-body", "upper body", "full body", "portrait",
	} {
		p = strings.ReplaceAll(p, w, "")
	}
	low := strings.ToLower(p)
	// **按整词删**( 边界),不能再用子串替换:实测 strings.ReplaceAll(low,"chin","")
	// 会把 "Chinese man" 削成 "ese man" —— 族群信息一丢,转身视频立刻把角色画成外国人
	// (2026-09-12 用户报「转台图和多位图咋生成外国人了」,根因即此)。整词边界同时
	// 避免 chin→machine、lip→clip、face→surface 这类误伤。
	// 另外族群/身份词(Chinese/Asian/东方)一律**保留**:它们是身份锚,不是"面部细节"。
	low = reStripWord.ReplaceAllString(low, " ")
	return tidyCompact(low)
}

// compactStripWords 转身视频提示词里**要剥掉**的面部/皮肤类词(整词匹配)。
// 为什么剥:长相一律以参考图为准,文字多写一句就多一次"模型按文字重画"的机会。
// 注意这些都是**整词**,且不含 Chinese/Asian 等族群词。
var compactStripWords = []string{
	"rosy", "blush", "freckles", "freckle", "mole", "scars", "scar",
	"birthmark", "pockmarks", "pockmark", "eyelids", "eyelid",
	"eyebrows", "eyebrow", "cheekbones", "cheekbone", "cheeks", "cheek",
	"chin", "jaw", "lips", "lip", "nose", "eyes", "eye", "face",
	"skin", "complexion", "makeup", "handsome", "pretty", "beautiful", "beautify", "cute",
}

// reStripWord 整词删除用的正则(按词边界,避免削掉 Chinese/machine 这类词的一部分)。
var reStripWord = regexp.MustCompile("\x5cb(?:" + strings.Join(compactStripWords, "|") + ")\x5cb")

// tidyCompact 清理删除后留下的空格与孤立标点(残句会误导模型,如 "long narrow with a sharp ,")。
//
// 2026-09-24 追加**尾部孤儿词**处理:整词删除会把一个修饰语留在句尾而不带走它修饰的名词
// (实测 "…with rosy cheeks and a long chin" → "…with and a long")。这种残句比"少一句描述"
// 更糟 —— "long" 悬在那里,模型很可能把它接到别处(比如把脸画长),正好制造我们要消除的漂移。
// 判据只作用于**句尾**:中间内容一个词都不动,不干扰已有的身份锚点(服装/发型/族群)。
func tidyCompact(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for _, bad := range []string{" ,", " .", " ;", "with a ,", ", ,"} {
		s = strings.ReplaceAll(s, bad, "")
	}
	s = strings.ReplaceAll(s, "with and", "with")
	s = strings.ReplaceAll(s, " ,", ",")
	s = strings.ReplaceAll(s, ",,", ",")
	s = strings.ReplaceAll(s, "  ", " ")
	// 句尾孤儿词:连接词/冠词/量词/无名词可修饰的形容词。它们单独留着没有任何信息量。
	for {
		f := strings.Fields(s)
		if len(f) == 0 {
			break
		}
		if !tidyTrailingJunk[strings.Trim(f[len(f)-1], ",;.")] {
			break
		}
		s = strings.Join(f[:len(f)-1], " ")
	}
	if r := []rune(s); len(r) > 400 {
		s = string(r[:400])
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), ",;"))
}

// tidyTrailingJunk 句尾允许被剥掉的词(见 tidyCompact 的说明)。
var tidyTrailingJunk = map[string]bool{
	"with": true, "and": true, "of": true, "in": true, "on": true, "over": true, "under": true,
	"a": true, "an": true, "the": true, "his": true, "her": true, "its": true, "their": true,
	"one": true, "two": true, "very": true,
	"long": true, "short": true, "big": true, "small": true, "wide": true, "narrow": true,
	"thick": true, "thin": true, "sharp": true, "round": true, "flat": true, "deep": true,
}

// hstackImages 横向拼接多张图(ffmpeg hstack):每格统一到**首图(定妆照)的像素**后并排,
// 得到一张转台图。
//
// 【为什么必须"锁框"而不是只锁高度(甲2)】旧实现是 `scale=-1:1248`:只统一高度、
// 宽度各按自己的比例算。于是**单格比例可以互不相同** —— 定妆照是 9:16(768×1344),
// 而从 2:3 的转身视频抽出来的角度帧是 832×1248,两者并排进同一张拼图后:定妆照那格
// 被缩成 713×1248、角度帧那格是 832×1248。模型看到的"同一张参考图里每格都不一样宽",
// 拼合图的格间一致性(以及"左起第一格就是正面照"这条约定)就被稀释了。
// 现在统一成"缩放到覆盖 + 居中裁切"(scale=…:force_original_aspect_ratio=increase,crop),
// 既不改单格比例,也不拉伸 —— 每格恰好是定妆照的尺寸。
func hstackImages(ctx context.Context, f *ff.FF, inputs []string, out string) error {
	if len(inputs) < 2 {
		return fmt.Errorf("至少需要两张图才能拼接")
	}
	// 首图 = 定妆照(两个调用点都把它放在第一格):它已是项目画幅,就是本拼图的框。
	fw, fh := stripFrameBox(imagePixelSizeOrZero(inputs[0]))
	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	for _, in := range inputs {
		args = append(args, "-i", in)
	}
	filters := make([]string, 0, len(inputs)+1)
	refs := ""
	for i := range inputs {
		filters = append(filters, fmt.Sprintf(
			"[%d:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d[v%d]",
			i, fw, fh, fw, fh, i))
		refs += fmt.Sprintf("[v%d]", i)
	}
	filters = append(filters, fmt.Sprintf("%shstack=inputs=%d[out]", refs, len(inputs)))
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[out]", "-frames:v", "1", out)
	_, err := f.Run(ctx, args...)
	return err
}

// turnaroundFrameSize 转身视频的**渲出色度** = 定妆照自身的像素(定妆照现已等于成片画幅)。
//
// 旧实现写死 832×1248:定妆照一旦是 768×1344(9:16),转身视频仍按 2:3 出图,
// 抽出来的角度帧与定妆照**不同比例**,拼图时只能靠裁切对齐(见 hstackImages)——
// 而裁切 2:3 → 9:16 会切掉四分之一画面宽度,角色的手臂/道具可能被切出画面。
// 读不到定妆照尺寸时回落到平台默认画幅(不猜)。
func turnaroundFrameSize(masterPath string) (int, int) {
	return stripFrameBox(imagePixelSizeOrZero(masterPath))
}

// stripFrameBox 视图条里**单格**的统一尺寸:取首图(定妆照)的像素;
// 读不到(文件缺失/格式不认识)时用平台默认画幅 —— 宁可退化到出厂口径,
// 也不要拿一个猜出来的框去裁用户的参考图。
func stripFrameBox(w, h int) (int, int) {
	if w > 0 && h > 0 {
		return w, h
	}
	return DefaultAssetWidth, DefaultAssetHeight
}

// imagePixelSizeOrZero 读图片宽高,失败返回 (0,0)(调用方据此走默认口径)。
func imagePixelSizeOrZero(path string) (int, int) {
	w, h, err := imagePixelSize(path)
	if err != nil {
		return 0, 0
	}
	return w, h
}

// RelatedSuffixName 由关联图文件名推导「在项目侧应该叫什么」:把主名前缀换成
// 项目资产的命名,只保留角度标记(_view_<tag> / _turnaround)。
//
// 库里关联图的文件名来自入库时那个项目的命名(如 characters_char_张三_view_side.png),
// 而新项目落盘的主图叫 characters_char_张三.png —— 直接把库里文件名抄进项目会得到
// 一个 turnaroundPath() 永远拼不出来的名字(它按主图主名 + _turnaround 推导),
// 于是"库里有转台图、渲染却只喂正面照"。
func RelatedSuffixName(base, related string) string {
	name := filepath.Base(related)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if strings.HasSuffix(stem, "_turnaround") {
		return base + "_turnaround" + ext
	}
	if i := strings.LastIndex(stem, "_view_"); i >= 0 {
		return base + "_view_" + stem[i+len("_view_"):] + ext
	}
	return name // 认不出的关联图原样保留,至少不丢文件
}
