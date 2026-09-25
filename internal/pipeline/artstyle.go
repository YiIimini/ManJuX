package pipeline

import (
	"regexp"
	"strings"
)

// artstyle.go 角色画风的**硬约束**:动漫化(非真人脸)+ 哑光(去"油腻")+ 不影射真人。
//
// 为什么单独成文件、且分成 image / render 两块(2026-09-24 用户四项报障之①④):
//
//	①「渲染的视频人物油腻」—— 油腻不是"皮肤太亮"这么简单,它是三件事叠出来的:
//	   真人照片先验(毛孔/皮脂高光)+ 模型的美颜先验(光滑无瑕)+ 提示词里的强光描述
//	   (侧逆光/轮廓光把额头鼻梁打成高光斑)。所以要在**两头**同时压:定妆照(Krea-2)
//	   不能画出油光,渲染(H3)不能把油光加上去 —— 两处各写一份必然漂移,于是统一到
//	   本文件,由 image/render 两个出口分别取用。
//	④「角色不要真人脸,动漫化,避免侵权」—— 动漫化与去油腻是同一枚硬币:赛璐璐平涂
//	   + 手绘线稿 + 哑光平涂肤色,天然就没有皮脂高光;而"不要真人脸"还多一层法务含义,
//	   必须显式禁止影射任何真实人物/演员/公众人物。
//
// **两块内容不可互换**(踩过的坑):Krea-2 是蒸馏模型(CFG 1.0),负面词不但不生效,
// 还会把名词当指令画出来(见 uniformCivilianClarify 的实测记录:"no epaulettes" 画出了
// 肩章)。所以:
//   - image 块 = **纯正面描述**,一个否定词、一个"photo/照片/毛孔"类名词都不许出现;
//   - render 块 = H3 侧,官方指南允许散文式排除句,才写 "NEGATIVE (must not happen)"。
//
// 两块都**只描述渲染表面与画风**,绝不写五官形状(形状类词汇一律留在角色卡里)——
// 这是 2026-09-12 那次"风格块把圆脸大眼塞给所有角色"事故的教训。
//
// 2026-09-24 第二轮(用户报「铜雀被渲染成人物」)把两块各自再切成**通用介质 + 类型条款**:
// 通用部分(线稿/平涂/哑光/原创设计)对所有实体都成立;而"五官/肤色"只对人类成立 ——
// 一件器物被要求"简化五官、哑光粉质肤色"就是在逼模型给它长一张脸。类型条款见
// entity_prompts.go 的 entityProfiles(每类各自的渲染方法)。

// MediumMarker 动画介质块的唯一标记串。
//
// 两处用途:① "已经带过就不再叠"的判据;② **反推一张定妆照走的是动画线还是写实线**
// (RealisticForPrompt)—— 没有项目上下文的调用方(资产库「补全多视角」)只能这么判断。
const MediumMarker = "ANIMATED MATTE RENDER"

// RenderMediumMarker **渲染侧**介质块的唯一标记串。
//
// 为什么和 MediumMarker 分开:两块内容不可互换(image 块是纯正面描述、一个否定词都
// 不许有;render 块才允许写 NEGATIVE),标记串也必须分开 —— 否则"这张定妆照是不是
// 动画线"会被渲染侧的标记误判成真。两侧的标记都由测试钉住(mediumMarker 一致性)。
//
// 它的第二个用途是闸门:所有"会出像素"的路径(主渲染/单镜2K/成片2K/局部重绘/音频驱动)
// 的提示词都必须含它 —— 成片 2K 曾用一句裸字面量绕过全部约束(见 final2k.go 的说明)。
const RenderMediumMarker = "MATTE ANIMATED LOOK"

// animatedMediumImageBlock Krea-2 侧的**通用介质块**(不含任何身体部位词)。
//
// 只能用"它是什么"来描述:手绘线稿 + 赛璐璐平涂 + 宽而软的平涂阴影 + 哑光暗哑质感 +
// 柔和低对比散射光 + 原创设计。一个否定词都不写。
func animatedMediumImageBlock() string {
	return "\n\n" + MediumMarker + " (mandatory): 2D/2.5D animation — hand-drawn clean line art, " +
		"flat cel-shaded colour with wide soft shadow shapes, a matte dull even finish across every surface, " +
		"soft diffuse low-contrast light that falls gently across the form. " +
		"The subject is an original animation design." +
		"(中文对照:动画化渲染——手绘线稿 + 赛璐璐平涂与宽软的平涂阴影 + 整体哑光暗哑质感(均匀、不反光)+ " +
		"柔和低对比散射光;主体为原创设计。)"
}

// animatedFaceImageBlock Krea-2(人物定妆照/重抽)用的画风块 = 通用介质 + **人类专属条款**。
//
// 只能用"它是什么"来描述:手绘线稿 + 简化的图形化五官 + 赛璐璐平涂 + 宽而软的平涂
// 阴影 + 哑光粉质肤色 + 柔和低对比散射光 + 原创动画设计。一个否定词都不写。
func animatedFaceImageBlock() string {
	return animatedMediumImageBlock() + "\n- Human faces are hand-drawn with simplified graphic facial features, and skin reads as a " +
		"matte powdery complexion with a dull even finish." +
		"(中文对照:人物:五官为手绘简化图形化处理,肤色哑光粉质、整体暗哑均匀。)"
}

// animatedMediumRenderBlock H3 侧的**通用介质块**:画风 + 质感 + 通用排除句。
//
// 通用排除句里保留"真人照片脸/演员/影射真实人物"三条 —— 它们对任何实体都成立
// (一件器物也不该长着一张明星脸)。皮肤专属的排除项(油光/汗湿高光)放在人类条款里。
func animatedMediumRenderBlock() string {
	return "\n\n" + RenderMediumMarker + " (mandatory, applies to every frame of this video):\n" +
		"- The whole video is stylized 2D/2.5D animation (donghua): hand-drawn line art, flat cel-shaded colour with wide soft shadow shapes.\n" +
		"- Every surface reads MATTE: flat even shading, with each highlight staying dull, wide and low in contrast.\n" +
		"- Everything stays dry and powdery throughout: the finish remains dull and even.\n" +
		"- Lighting is soft, even and low-contrast with a gentle falloff, so nothing ever carries a hard hot spot.\n" +
		"- Every design in this video is ORIGINAL.\n" +
		"NEGATIVE (must not happen): a photographic or 3D-scanned human face, a live-action actor's face, any resemblance to a real person, " +
		"celebrity or public figure, and a glossy plastic or wax finish."
}

// humanFaceRenderClause 人类专属的渲染条款(脸/皮肤),接在通用介质块之后。
func humanFaceRenderClause() string {
	return "\n- Every human face is a hand-drawn line-art face with simplified graphic features; skin reads as a flat matte complexion " +
		"with a dull even finish.\n" +
		"- Skin specifically: no oily or greasy skin, no wet or sweaty highlights, and no hard specular on the forehead, nose bridge or cheekbones.\n" +
		"(中文对照:人物:手绘线稿脸、五官简化;肤色哑光均匀,额头/鼻梁/颧骨的高光宽而暗哑;" +
		"全片不许出现油光、汗湿反光、塑料蜡质感、真人照片脸或任何影射真实人物/演员/公众人物的长相。)"
}

// animatedFaceRenderBlock H3(渲染出片/2K 重生成/局部重绘/音频驱动)用的画风块:
// 通用介质块 + 人类条款(默认/人类镜头的组合形态)。
//
// 这里才允许点名"不要什么":油光、湿汗高光、塑料蜡质、真人照片脸、演员明星相似度。
func animatedFaceRenderBlock() string {
	return animatedMediumRenderBlock() + humanFaceRenderClause()
}

// 开关本身在 project.ScriptParams(StylizedFaces / FacesOn),未表态即开 ——
// 理由见那里的注释:存量项目正是报障的那批,默认关掉等于新约束对它们不可见。

// TurnaroundMediumBlock 转身/转台视频的**介质锁**。
//
// 为什么必须单独给这一层(2026-09-24 用户报「角色多视角老是生成错误」的根因):
// 转身视频是**另一次渲染调用**,它不吃分镜那套 styleAndMediumBlock,而
// TurnaroundPrompt 里只有一句轻描淡写的 "same art style"。实测(H3 Ref2V)结果:
//   - 赛璐璐动漫角色 → 被重画成写实真人(换成素色和服、迷彩纹样丢失);
//   - 插画风青铜雀   → 被重画成写实 3D 机械铜鸟(铆钉/玻璃眼/金属板);
//
// 于是逐帧身份校验**正确地**判定"不是同一主体"并丢弃 —— 三个角色连续失败,
// 用户看到的就是"多视角老是生成错误"。判定没错,缺的是介质锁。
//
// realistic=true(用户显式关了「角色动漫化」要拍真人剧)时返回空串:那种情况下
// 强塞动画介质才是错的。
func TurnaroundMediumBlock(kind EntityKind, realistic bool) string {
	if realistic {
		return ""
	}
	switch NormalizeEntityKind(string(kind)) {
	case KindHuman, KindChibi:
		return animatedFaceRenderBlock() // 通用介质 + 人脸条款
	default:
		// 器物/灵宠/妖兽/无实体:只给通用介质(给器物写"肤色哑光"等于逼它长一张脸)
		return animatedMediumRenderBlock()
	}
}

// RealisticForPrompt 反推"这张定妆照该用写实介质还是动画介质" —— 给**没有项目
// 上下文**的调用方用(资产库「补全多视角」拿不到项目的 stylized_faces)。
//
// 判据是定妆照提示词里有没有我们自己的介质标记串:有 = 它是 Krea-2 动画线画的;
// 没有 = 多半是用户上传的真人照片/写实图(那种情况下强塞动画介质同样是错的)。
func RealisticForPrompt(imagePrompt string) bool {
	return !strings.Contains(imagePrompt, MediumMarker)
}

// photorealRequested 风格文本里是否明确要真人/写实画风。
//
// 用途只有一个:开启了「动漫化」时,把这种自相矛盾的风格请求**告知用户**
// (要真人剧请关掉开关)。**不再用它改写提示词** —— 关键词表永远追不上 LLM 的措辞
// (实测本机真实项目的风格是 "gritty rural Chinese period realism",一个关键词都不命中,
// 而它显然是要写实),所以提示词侧改用**无条件介质覆盖**(见 anchorPrompt 的
// MEDIUM OVERRIDE):不管风格怎么写,"人物画法以介质段为准"。
// StripNegations 剥掉风格串里的**否定式**条款(", NO GLOSS" / "no wet highlights" /
// "without X" / "avoid X"),保留其余正面描述。
//
// 为什么必须剥(2026-09-25 用户报「动漫化角色还很丑」):上游(技能端)写来的风格串
// 常常自带一串否定 —— 本机那部《全城榜一》方案里就写着
// "…MATTE POWDERY SKIN WITH A DULL EVEN FINISH, **NO GLOSS, NO WET HIGHLIGHTS,
// NO PHOTOREAL DETAIL**"。而 Krea-2 是蒸馏模型(CFG 1.0):**负面词不但不生效,还会把
// 名词当指令画出来**(本文件顶部记录过实测:"no epaulettes" 画出了肩章)。
// 于是"不要油光"被读成"油光"、"不要写实细节"被读成"写实细节" —— 定妆照上那一片片
// 发灰的污渍感、脏兮兮的平涂阴影,正是这么来的。
//
// 只用于**图像侧**(Krea-2);视频侧(H3)允许散文式排除句,不要在这里替它决定。
func StripNegations(style string) string {
	var keep []string
	for _, seg := range strings.Split(style, ",") {
		s := strings.TrimSpace(seg)
		if s == "" {
			continue
		}
		low := strings.ToLower(s)
		neg := false
		for _, pre := range []string{"no ", "not ", "without ", "avoid ", "never ", "do not ", "don't ", "free of ", "zero "} {
			if strings.HasPrefix(low, pre) {
				neg = true
				break
			}
		}
		// 段内的否定短语(如 "skin with a dull finish NO GLOSS")也去掉该短语
		if !neg {
			if i := reNegPhrase.FindStringIndex(s); i != nil {
				s = strings.TrimSpace(s[:i[0]])
				if s == "" {
					continue
				}
			}
		}
		if neg {
			continue
		}
		keep = append(keep, s)
	}
	return strings.Join(keep, ", ")
}

// reNegPhrase 段内否定短语(整词边界,避免削到 "notebook" 这类词)。
var reNegPhrase = regexp.MustCompile(`(?i)\b(no|not|without|avoid|never)\b`)

// photorealRequested 风格文本里是否明确要真人/写实画风。
//
// 判据前先剥否定式:**"NO PHOTOREAL DETAIL" 里的 photoreal 不算要写实**
// (2026-09-25 用户拿着一条"风格里含真人/写实请求"的告警来问 —— 他的风格串里唯一的
// 写实字样就是那个 NO 后面的,属于纯误报;更糟的是它还让 styleExpand 走了"覆盖"分支,
// 把技能端指定的画风术语丢掉换成平台罐头词)。
func photorealRequested(style string) bool {
	s := strings.ToLower(StripNegations(style))
	for _, k := range []string{"真人", "写实", "实拍", "realis", "photoreal", "live-action", "live action", "liveaction", "photo-real"} {
		if strings.Contains(s, k) || strings.Contains(StripNegations(style), k) {
			return true
		}
	}
	return false
}

// StripMicroTextForImage 把提示词里的**微雕文字/极小铭文**改成"模糊磨旧的痕迹"。
//
// 依据(2026-09-25 实测本机定妆照):上游提示词写着
//
//	a thumb-nail-sized brass clip … engraved with a tiny worn '禾' character whose
//	second stroke is almost rubbed away
//
// —— 拇指盖大的铜扣上刻一个几乎磨没的汉字,在 832×1248 的半身幅里根本没有足够像素。
// 模型不会"省略",而是**糊成一团灰渍**(实测那张定妆照胸口与腕部正是一片片发灰的污渍)。
// 这与视频侧"绝不自己发明汉字"是同一条道理的图像版:认不出的字只会变成噪点。
func StripMicroTextForImage(prompt string) string {
	out := reInscribedGlyph.ReplaceAllString(prompt, "$1 with a faint worn mark")
	out = reQuotedGlyph.ReplaceAllString(out, "a faint worn mark")
	return out
}

var (
	// engraved/carved/… with …… 引号里 1-6 个字符(含汉字)
	reInscribedGlyph = regexp.MustCompile(`(?i)\b(engraved|carved|printed|written|stamped|painted|etched|marked|inscribed)[^,.;]{0,60}?["'“”‘’][^"'“”‘’]{1,6}["'“”‘’]`)
	// '禾' character / "字" letter 这类写法
	reQuotedGlyph = regexp.MustCompile(`["'“”‘’][^"'“”‘’]{1,6}["'“”‘’]\s*(?i:character|characters|word|letter|text|glyph)s?`)
)

// PortraitPoseClause 人像幅面的**姿态纪律**。
//
// 依据:同一张定妆照里,模型在"upper-body framing, front-facing"且提示词没写姿态时
// 自作主张画了**双臂抱胸** —— 半身幅里手只占很少像素,于是画成一只连指手套(实测)。
func PortraitPoseClause(kind EntityKind) string {
	switch NormalizeEntityKind(string(kind)) {
	case KindHuman, KindChibi:
		return " Pose: calm and simple, arms relaxed; keep the hands out of frame or uncomplicated" +
			" (this is a reference sheet, not a pose study — complex hand poses render as blobs at this framing)."
	default:
		return ""
	}
}
