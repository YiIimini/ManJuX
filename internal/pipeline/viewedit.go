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
	"github.com/YiIimini/ManJuX/internal/qc"
)

// viewedit.go 多视角的**图像编辑路径**(Qwen-Image-Edit-2511)。
//
// 与 H3 转身视频路径(video 路径,见 turnaround.go)的关系:
//   - 两条路产出**同一套产物**(<base>_view_threequarter|side|back.png + <base>_turnaround.png),
//     走**同一个 VLM 身份闸门**(qc.IdentityJudge),失败策略也一致(换种子重试一次,仍不行
//     就整批丢弃、退回单张正面照);
//   - 差别只在"怎么拿到另一个角度":视频路径请 H3 渲一段转身视频再抽帧(它会顺手重新
//     设计角色 → 校验必然拒绝),编辑路径请 Qwen-Image-Edit **改这张图**(身份/配色/画风
//     按像素保住)。首次实现时两条路都保留,便于同机 A/B。
//
// 最终选择见 docs/多视角方案.md 的实测结论。

// viewAngle 一个目标视角:tag 与 H3 路径逐字对齐(文件名与前端标签都靠它)。
type viewAngle struct {
	Tag   string
	Brief string // 中文简述(日志用)
	En    string // 英文编辑指令主体
}

// viewAngles 三个角度。与 H3 路径的抽帧时间点(1.3/2.6/3.9s = 四分之三/侧面/背面)对应。
var viewAngles = []viewAngle{
	{"threequarter", "四分之三侧", "a three-quarter view, turned about 45 degrees to the subject's left so the face is still clearly visible"},
	{"side", "正侧面", "a full side profile, turned about 90 degrees so the subject faces the left edge of the frame"},
	{"back", "背面", "a rear view, turned about 180 degrees so we see the back of the subject"},
}

// ViewEditMediumBlock 编辑路径的**介质锁**。
//
// 刻意用**图像侧**的介质块(animatedFaceImageBlock / animatedMediumImageBlock),
// 而不是 H3 那套 animatedMediumRenderBlock —— 后者是给视频写的("applies to every frame
// of this video"),而这里是单张图。更关键的是:图像侧这两块**与定妆照提示词用的是同一段词**,
// 于是"定妆照的画风"与"角度图的画风"在文字层面就是同一个定义,不存在两套口径。
// realistic=true(用户显式要真人写实)时返回空:那种情况下强塞动画介质才是错的。
func ViewEditMediumBlock(kind EntityKind, realistic bool) string {
	if realistic {
		return ""
	}
	switch NormalizeEntityKind(string(kind)) {
	case KindHuman, KindChibi:
		return animatedFaceImageBlock()
	default:
		return animatedMediumImageBlock()
	}
}

// viewEditPrompt 组装一个角度的编辑指令。
//
// 编辑式模型吃的是"对 image 1 做什么",所以指令必须同时说清四件事:
// ① 转多少度;② 主体不变(逐项点名脸/发型/服装/配色/结构);③ **介质不变**;
// ④ 构图/背景/光线不变(否则拼出来的转台图会一格一个尺度)。
func viewEditPrompt(kind EntityKind, a viewAngle, name, imagePrompt string, realistic bool) string {
	subject := "the character"
	same := "same face, facial features, hairstyle, hair colour, skin tone, costume, accessories, colours and body proportions"
	switch NormalizeEntityKind(string(kind)) {
	case KindObject:
		subject = "the object"
		same = "same silhouette, structure, every part, material, colour, patina and ornament — no part may be added, removed or redesigned"
	case KindSpiritPet, KindBeast:
		subject = "the creature"
		same = "same species, body plan, fur/feather/scale colour and pattern, eye colour, ear/horn/tail shape and overall mass"
	case KindIncorporeal:
		subject = "the same bodiless entity"
		same = "same silhouette, glow colour and intensity, degree of transparency and edge treatment"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Show %s in image 1 from %s.\n", subject, a.En))
	sb.WriteString(fmt.Sprintf("Keep it the EXACT SAME subject: %s. ", same))
	sb.WriteString("This is the SAME subject seen from another angle, NOT a redesign, not a new character, " +
		"not a beautified / stylised / aged / de-aged / chibi-fied version.\n")
	// 介质锁**无条件**写(realistic 时只是不追加动画段):锁的是"与 image 1 同介质",
	// 这件事在任何模式下都成立 —— 曾经把整句塞进"动画段非空"的分支里,于是 realistic
	// 模式连"别换画风"都没说,模型可以自由把写实照片重画成插画。
	sb.WriteString("Keep the EXACT SAME rendering medium and art style as image 1 — do not re-render it in another medium.")
	if m := ViewEditMediumBlock(kind, realistic); m != "" {
		sb.WriteString(m)
	}
	sb.WriteString("\n")
	sb.WriteString("Keep the SAME plain background, the SAME lighting and the SAME framing/scale as image 1, " +
		"so the result can be placed side by side with it. The subject stays centred and fully inside the frame.")
	if d := compactIdentity(imagePrompt); strings.TrimSpace(d) != "" && len([]rune(d)) < 300 {
		// 身份描述只作"是哪一类东西"的补充;参考图仍是唯一权威(与 H3 路径同一条原则)
		sb.WriteString("\n(The subject is: " + d + ")")
	}
	return sb.String()
}

// 多视角生成方式的取值(TurnaroundOptions.Source)。
//
// 用常量而不是散落的字面量:第一,调用方不必知道魔法字符串;第二,project 侧那条
// "每个资产来源都要有中文名"的闸门会扫 `Source:` 字面量,而"edit/h3"是**生成方式**、
// 不是资产来源 —— 把它写成常量,两个词汇表就不会互相误伤(2026-09-25 实测踩到)。
const (
	ViewSourceEdit = "edit" // 图像编辑(默认首选:改你给的那张图,身份按像素保住)
	ViewSourceH3   = "h3"   // H3 转身视频抽帧(旧路径:视频模型会顺手重设计角色)
)

// GenerateViewsEdit 用图像编辑逐角度生成多视角,返回转台图路径。
//
// 与 H3 路径共用:产物命名、VLM 身份闸门、失败策略(换种子重试 → 仍不行则丢弃全部角度帧)。
// 编辑每张只需数秒(4 步 Lightning),所以这里逐角度串行即可,不必并发抢显存。
func GenerateViewsEdit(ctx context.Context, ce engine.Engine, f *ff.FF, o TurnaroundOptions) (string, error) {
	ee, ok := ce.(*comfyengine.Engine)
	if !ok {
		return "", fmt.Errorf("多视角编辑需要本地 ComfyUI 引擎(当前引擎不支持图像编辑)")
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
	kindLabel := EntityKindLabel(NormalizeEntityKind(string(o.Kind)))
	if NormalizeEntityKind(string(o.Kind)) == "" {
		kindLabel = "人"
	}
	prof := EntityProfileOf(o.Kind)
	emit := o.Emit
	if emit == nil {
		emit = func(string, ...any) {}
	}
	baseSeed := o.Seed
	if baseSeed == 0 {
		baseSeed = nameSeed("viewedit", o.Name)
	}
	attempts := 1
	if o.Verify != nil && o.VerifyRetries > 0 {
		attempts += o.VerifyRetries
	}
	emit("多视角:「%s」(%s)改用图像编辑逐角度生成(Qwen-Image-Edit,每张数秒)…", o.Name, kindLabel)

	lastReason := ""
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			emit("多视角:「%s」第 %d 次重出(上一次与定妆照不一致:%s)", o.Name, attempt, lastReason)
		}
		views := make([]string, 0, len(viewAngles))
		for i, a := range viewAngles {
			out := filepath.Join(dir, base+"_view_"+a.Tag+".png")
			_, err := ee.EditImage(ctx, comfyengine.ImageEditOptions{
				RefImages: []string{o.MasterPath},
				Prompt:    viewEditPrompt(o.Kind, a, o.Name, o.Prompt, o.Realistic),
				Seed:      baseSeed + int64(attempt-1)*7919 + int64(i)*104729,
				Steps:     o.Steps,
				UseTurbo:  true,
				// UseAngles 默认**关**:Multiple-Angles LoRA 在实测里没有转动机位,
				// 反而把训练集里的背景(星空+极光)带了进来(2026-09-24,另一处同类用途
				// 的实测:改用基座模型 + 一句自然语言机位指令才真的转,4 步/20 步都成功)。
				// 我们的编辑指令本身就是自然语言机位描述(viewEditPrompt),所以先按
				// "不用 LoRA"跑;权重下齐后若发现转角度不足,把它改回 true 做 A/B ——
				// 这个开关留着就是为这一次对比。
				UseAngles: false,
				Timeout:   0,
			}, out)
			if err != nil {
				emit("多视角:「%s」%s 角度生成失败:%v", o.Name, a.Brief, err)
				removeFiles(views)
				return "", fmt.Errorf("多视角编辑失败(%s):%w", a.Brief, err)
			}
			views = append(views, out)
			errMsg := ""
			if o.Verify != nil {
				rep, verr := qc.IdentityJudge(ctx, o.Verify, o.MasterPath, []string{out}, o.VerifyMinScore, prof.JudgePoints, kindLabel)
				switch {
				case verr != nil:
					emit("多视角校验不可用(%v):本次按未校验交付,建议在设置页配好视觉模型", verr)
				case !rep.Pass:
					errMsg = rep.Reason
				default:
					emit("✔「%s」%s 校验通过:%s", o.Name, a.Brief, rep.Reason)
				}
			}
			if errMsg != "" {
				lastReason = a.Brief + ":" + errMsg
				emit("✕「%s」%s 与定妆照不一致:%s", o.Name, a.Brief, errMsg)
				removeFiles(views)
				views = nil
				break
			}
		}
		if len(views) == len(viewAngles) {
			turn, err := assembleTurnaround(ctx, f, o, views)
			if err != nil {
				return "", err
			}
			emit("✔「%s」转台图已生成(%d 个视角,图像编辑)", o.Name, len(views)+1)
			return turn, nil
		}
	}
	emit("✕「%s」多视角校验未通过(%d 次尝试):%s —— 已丢弃本次角度帧,"+
		"该角色本次按单张正面照渲染(宁缺勿错)", o.Name, attempts, lastReason)
	return "", fmt.Errorf("多视角(图像编辑)与定妆照不一致,已拒绝使用:%s", lastReason)
}
