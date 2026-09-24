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
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// 多视角转台图(角色转身参考)生成。
//
// 为什么必须是 H3 Ref2V 而不是 Krea-2 img2img:实测 Krea-2 是**纯文生图模型,
// 没有身份条件注入机制**,只能靠 denoise 在"改动幅度"与"保住身份"之间猜 ——
// denoise 0.7 会把人整个换掉(木偶角色被改写成人类),0.55 则身份勉强保住但
// 角度几乎不变。这条路是模型的固有限制,不是参数调优能解决的。
//
// 正解是让**有身份参考机制**的 H3 Ref2V 来完成:以定妆照为参考生成一段转身视频,
// 身份由 ref 图锁住、角度由提示词驱动,两者不再互斥。
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
	Kind   EntityKind
	Steps  int
	Accel  string
	Length int   // 帧数;0 = 124(约 5.2 秒)
	Seed   int64 // 0 = 按角色名派生稳定种子
	// Verify 视觉模型客户端:非 nil 时逐帧核对"是不是定妆照里那个人",不一致就换种子
	// 重出;全部重试仍不一致则**拒绝使用**(详见 GenerateTurnaround 的说明)。
	Verify         *llm.Client
	VerifyMinScore int // 逐帧最低相似分,0 = 70
	VerifyRetries  int // 不一致时的额外重试次数,默认 1(=最多出 2 次)
	Emit           func(format string, args ...any)
}

// GenerateTurnaround 定妆照 → 原地转身视频 → 抽帧 → 校验 → 横向拼转台图。
// 返回转台图绝对路径。任何一步失败都返回错误(由调用方决定告警还是阻断)。
//
// **为什么必须校验后才交付(2026-09-24 用户报「多视角/转台图和参考的定妆照不一致」)**:
// 转身视频是 H3 生成的,它有创作自由,可以把人转着转着画成另一个人。旧实现对此毫无
// 察觉,而且这张"另一个人"的拼图会作为该角色的**第二张参考图**喂回渲染 —— 主动把成片
// 的人脸往错误方向拉,比"没有多视角"更糟。所以:
//
//  1. 抽帧之后、拼图之前逐帧判"像不像本人"(视觉模型,见 qc.IdentityJudge);
//  2. 不一致就换种子重出(VerifyRetries 次);
//  3. 仍不一致 —— **拒绝交付**:删掉本次抽出的角度帧、不生成转台图、返回错误。
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
	attempts := 1
	if o.Verify != nil && o.VerifyRetries > 0 {
		attempts += o.VerifyRetries
	}
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
			Prompt:    prof.TurnaroundPrompt(o.Name, o.Prompt),
			Mode:      "ref2va",
			RefImages: []engine.RefImage{{Kind: "character", Path: o.MasterPath}},
			Width:     832, Height: 1248,
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
func tidyCompact(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for _, bad := range []string{" ,", " .", " ;", "with a ,", ", ,"} {
		s = strings.ReplaceAll(s, bad, "")
	}
	s = strings.ReplaceAll(s, " ,", ",")
	s = strings.ReplaceAll(s, ",,", ",")
	s = strings.ReplaceAll(s, "  ", " ")
	if r := []rune(s); len(r) > 400 {
		s = string(r[:400])
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), ",;"))
}

// hstackImages 横向拼接多张图(ffmpeg hstack):统一缩放到同一高度后并排,
// 得到一张转台图。
func hstackImages(ctx context.Context, f *ff.FF, inputs []string, out string) error {
	if len(inputs) < 2 {
		return fmt.Errorf("至少需要两张图才能拼接")
	}
	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	for _, in := range inputs {
		args = append(args, "-i", in)
	}
	filters := make([]string, 0, len(inputs)+1)
	refs := ""
	for i := range inputs {
		filters = append(filters, fmt.Sprintf("[%d:v]scale=-1:1248[v%d]", i, i))
		refs += fmt.Sprintf("[v%d]", i)
	}
	filters = append(filters, fmt.Sprintf("%shstack=inputs=%d[out]", refs, len(inputs)))
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[out]", "-frames:v", "1", out)
	_, err := f.Run(ctx, args...)
	return err
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
