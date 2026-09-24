package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// anchorPrompt 角色/场景身份锚定:把出场人物与参考图绑在一起交给 H3。
//
// facesOn = 项目的「角色动漫化 + 非真人脸 + 哑光」开关(未表态即开)。开启时在锚定块
// 之后追加 RenderLookBlock —— 这是用户四项报障里「人物油腻」「角色不要真人脸」在
// **视频侧**的落点:定妆照那头由 styleExpand 的 image 块压住,视频这头必须同时压,
// 否则 H3 会照着强光描述把哑光的定妆照重新画成油光脸(两头各写一份必然漂移)。
//
// **身份以参考图为准(2026-09-12 根因修复)**:旧版写的是「外貌描述优先级最高,
// 任何画面细节与之冲突时以此为准」—— 等于明确告诉模型"忘掉参考图那张脸,按文字
// 重画"。实测后果(用户本轮四项报障同一根因):
//   - 同一批角色的脸被文字拉到一起(都是"五十来岁老人"就长成同一张脸);
//   - 文字里有痣/红鼻头就画上,而定妆照(Krea-2 另一套提示词画的)没有 → 成片里
//     出现"无中生有"的面部标记,定妆照与成片对不上;
//   - 定妆照与视频本来是两次独立"按文字自由创作",不可能一致。
//
// 现在改成:长相照抄参考图(Ref2VA 锁脸的标准做法),文字只许用于服装/道具/姿态/
// 情绪/台词,并显式禁止添加参考图里没有的面部标记 —— 定妆照没有的,视频里也不会
// 凭空长出来。
//
// **2026-09-24 再按实体类型分发**(用户报「阿凿是一只铜雀却渲染成人物」):身份锁不能
// 只有"脸"这一种写法。器物锁的是结构/材质/部件,无实体锁的是形体与透明感,妖兽锁的是
// 物种与体量 —— 给一只铜雀写"脸型/五官/肤色/年龄感照抄参考图",模型当然给你画一个人。
func anchorPrompt(plan *project.Plan, shot *project.Shot, facesOn bool) string {
	type named struct {
		name, desc string
		kind       EntityKind
	}
	var chars, scenes []named
	for _, cid := range shot.Characters {
		for _, ch := range plan.Characters {
			if ch.ID == cid && strings.TrimSpace(ch.Description) != "" {
				chars = append(chars, named{ch.Name, strings.TrimSpace(ch.Description), ResolveEntityKind(ch)})
			}
		}
	}
	for _, sc := range plan.Scenes {
		if sc.ID == shot.Scene && strings.TrimSpace(sc.Description) != "" {
			scenes = append(scenes, named{name: sc.Name, desc: strings.TrimSpace(sc.Description)})
		}
	}
	if len(chars) == 0 && len(scenes) == 0 {
		// 无角色也无场景(纯空镜):身份锚定没什么可锚的,但**风格段与介质覆盖仍必须带上**
		// —— 空镜一样要服从全片画风。此前这条早退分支直接把风格与介质段丢了,
		// 结果是"空镜写实、其余动画"的花片(实测真实项目第 1 镜正是无人物空镜)。
		return styleAndMediumBlock(plan, facesOn) + shot.H3Prompt
	}
	var sb strings.Builder
	sb.WriteString("【身份锚定(全片统一,优先级最高)】\n")
	if len(chars) > 0 {
		sb.WriteString("本镜出场角色 —— **按类型分别渲染**(参考图里的东西就是它们本体;同一角色可能有两张参考图:" +
			"**锐利的正面定妆照(唯一权威)** + 横向拼合的多视角图 —— 左起第一格就是同一张正面照," +
			"其余格是同一个主体/同一件东西的其它角度,**它不是第二个角色**):\n")
		for _, c := range chars {
			fmt.Fprintf(&sb, "- 「%s」类型=%s\n", c.name, EntityKindLabel(c.kind))
		}
		// 逐类型条款(同类型只写一次,避免多个器物时把同一段重复三遍)
		seen := map[EntityKind]bool{}
		for _, c := range chars {
			if seen[c.kind] {
				continue
			}
			seen[c.kind] = true
			p := EntityProfileOf(c.kind)
			sb.WriteString("【" + EntityKindLabel(c.kind) + "条款】" + p.AnchorRefNote + "\n")
			sb.WriteString(p.AnchorLock + "\n")
			sb.WriteString(p.AnchorUseFor + ":\n")
		}
		for i, c := range chars {
			fmt.Fprintf(&sb, "%d. %s[%s]:%s\n", i+1, c.name, EntityKindLabel(c.kind), normalizeUniformWording(c.desc))
			if extra := uniformCivilianClarify(c.desc); extra != "" {
				sb.WriteString("   " + strings.TrimSpace(extra) + "\n")
			}
		}
	}
	if len(scenes) > 0 {
		sb.WriteString("场景同样以参考图为准(同一地点不得改造型/改结构):\n")
		for i, sc := range scenes {
			fmt.Fprintf(&sb, "%d. %s:%s\n", i+1, sc.name, sc.desc)
		}
	}
	if len(chars) > 0 {
		// 类型质感 + 反错误形态(不得拟人化/不得实体化/不得缩水)。
		// AntiForm **不受角色动漫化开关影响**:它不是画风选择,而是"这东西本来是什么形态"的
		// 正确性问题 —— 关了动画化也不该把铜雀画成人。
		seen := map[EntityKind]bool{}
		for _, c := range chars {
			if seen[c.kind] {
				continue
			}
			seen[c.kind] = true
			p := EntityProfileOf(c.kind)
			if facesOn && p.RenderLook != "" {
				sb.WriteString(strings.TrimRight(p.RenderLook, "\n") + "\n")
			}
			if p.AntiForm != "" {
				sb.WriteString(p.AntiForm + "\n")
			}
		}
	}
	sb.WriteString(styleAndMediumBlock(plan, facesOn))
	return sb.String() + "\n" + shot.H3Prompt
}

// styleAndMediumBlock 全片风格段 + (开启角色动漫化时的)渲染介质覆盖 + 画风硬约束。
//
// 抽成独立函数是因为它**必须对每一镜都生效**,包括没有角色也没有场景的纯空镜 ——
// 早退分支漏掉它,成片里就会出现"空镜写实、有人物的镜头动画"的花片。
//
// 两段的分工不能混:
//   - 风格段:保留 LLM 写的原文,它是**场景/色调/光线/氛围**的出题依据;
//   - 介质覆盖:无条件声明"人物与整体画法以本段为准",风格串里的 realism/胶片措辞
//     只作用于布景与影调。**不做关键词匹配** —— LLM 的措辞追不完
//     (实测本机真实项目的 "gritty rural Chinese period realism…35mm film grain" 一个词都不命中)。
func styleAndMediumBlock(plan *project.Plan, facesOn bool) string {
	var sb strings.Builder
	style := ""
	if plan != nil {
		style = strings.TrimSpace(plan.Style)
	}
	if style != "" {
		sb.WriteString("【全片视觉风格】" + style + "\n")
	}
	if style != "" {
		// 风格由**上游(小说技能端)指定**时,平台不抢风格(用户 2026-09-24 定调:
		// 「平台端渲染不限制风格,由小说技能端管控指定视频风格」)。
		//
		// 旧行为是无条件 MEDIUM OVERRIDE:不管上面写了什么,一律压成"二维/2.5 维
		// 动画 + 赛璐璐平涂 + 哑光"。后果是技能端指定的写实/胶片/3D 风格全被吃掉 ——
		// 用户看到的是"我明明指定了风格,出来还是那一套动漫脸"。
		//
		// 现在只保留**法务与原创性**那条(不影射真实人物、原创设计):它不规定画风,
		// 是任何风格下都必须成立的底线 —— 那也正是当初加介质锁的一半理由(避免侵权),
		// 而"必须动漫化"那另一半是风格选择,归技能端。
		sb.WriteString("【风格执行】以上风格由剧集设定指定,照此渲染,不要另换画风;" +
			"全片风格必须逐镜一致(同一部剧不得出现两种画风)。\n" +
			"STYLE AUTHORITY (from the series spec, do not substitute): render exactly in the style line above and keep " +
			"it identical in every shot of this series. Every design stays ORIGINAL: never make a character resemble a " +
			"real person, actor, singer, celebrity or public figure.\n")
	} else if facesOn {
		// 上游没给风格(极少数):用平台的动漫化兜底,并说明这是兜底 ——
		// 风格该由谁定写清楚,免得下一轮又变成"平台偷偷替我决定画风"。
		sb.WriteString("【渲染介质·兜底画风】本次没有任何风格设定,按平台默认的二维/2.5 维动画渲染:" +
			"手绘线稿、五官简化、赛璐璐平涂、哑光平涂肤色、柔和低对比光。" +
			"(风格应由上游剧本/技能端在方案里指定;指定后本段不再生效。)\n")
		sb.WriteString(animatedFaceRenderBlock() + "\n")
	}
	// 有上游风格时**不加**兜底画风块:那会把技能端指定的画风again压成动画。
	return sb.String()
}

// 台词语言标记:H3 靠 <d>…</d> 里的语言标记决定用哪种语言说话;标记丢了模型就
// 自己猜 —— 实测成片里出现听不懂的"鸟语"(2026-09-12 用户报障)。提示词模板要求
// 写 <d>[Chinese]台词</d>,但 LLM 经常漏,所以这里做代码级兜底(幂等)。
//
// 实现说明:Go 的 regexp(RE2)不支持负向断言,所以不做"<d> 后面不是 ["的判断,
// 而是先把所有 <d>[任意语言] 归一成裸 <d>,再把裸 <d> 统一换成 <d>[Chinese] ——
// 两步之后所有台词都恰好带一个中文标记。
var reDLangTag = regexp.MustCompile(`<d>\s*\[[A-Za-z][A-Za-z ]*\]\s*`)

// EnsureChineseDialogue 保证每段台词/旁白都带 [Chinese] 标记,并在有台词的提示词
// 末尾点明"只许普通话"。幂等,可反复调用(新老方案都过一遍)。
func EnsureChineseDialogue(prompt string) string {
	if !strings.Contains(prompt, "<d>") {
		return prompt
	}
	p := reDLangTag.ReplaceAllString(prompt, "<d>")
	p = strings.ReplaceAll(p, "<d>", "<d>[Chinese]")
	if !strings.Contains(p, "MANDARIN ONLY") {
		// 幂等判据就是这句话里的标记串:改文案时必须保留 MANDARIN ONLY,
		// 否则重复调用会一条条往上叠(测试里钉住了这一点)。
		p += "\n\nMANDARIN ONLY (mandatory language rule): every spoken line and voiceover MUST be Mandarin Chinese " +
			"(普通话), matching the [Chinese] tag inside each <d>…</d>; no English, no Japanese, no invented or " +
			"gibberish speech, no foreign accent. Ambient non-speech sound only where the soundscape asks for it."
	}
	return p
}

// LibraryPortraitPrompt 资产库侧(不持有项目上下文的路径:抽卡 / 自定义新建 / 重抽)
// 的定妆照提示词:**按实体类型**补上取景、忠实度与画风块。
//
// 为什么这里能用推断:库里存着角色的 name/description/image_prompt 三样,正好是
// `InferEntityKind` 的输入 —— 于是"库里那只铜雀"与"项目里那只铜雀"走的是同一套渲染
// 方法,不会出现"项目里是器物、库里当人"的分叉。
//
// 为什么用"总是加"而不是跟着项目开关:资产库是跨项目的全局资产,而全应用现在默认
// 就是动漫化角色。若库里生成的是真人脸,复用到任一项目后都会与渲染侧的介质块打架
// (定妆照写实、视频动画 = 又回到"两张脸")。要真人脸的角色走**上传**那条路
// (上传路径不做任何改写);要全片真人剧则关项目开关并把定妆照换成上传图。
func LibraryPortraitPrompt(name, description, prompt string) string {
	// FacesOn 恒为 true(库是跨项目全局资产,全应用默认动漫化);没有项目上下文,
	// 因此不注入同剧对比块 —— 那是"同一部剧内不撞脸"的约束,库侧无从判断。
	return PortraitPrompt(PortraitSubject{
		Name: name, Description: description, ImagePrompt: prompt, FacesOn: true,
	}, nil)
}

// ShotRenderPrompt **渲染期**提示词的唯一装配点:身份锚定(以定妆照为准)+ 角色画风
// 硬约束(动漫化/哑光)+ 台词语言标记。
//
// 为什么必须收口(2026-09-24 补):这一串约束原先只长在 stages_render.go 的主渲染里,
// 而"真正出像素"的路径有四条 —— 主渲染、云端 2K 定稿、局部重绘、音频驱动。后三条各自
// 直接拿 shot.H3Prompt 提交,于是画风/身份约束在它们身上全部缺席:2K 会把 768p 的哑光
// 画面重新生成出油光,局部重绘会在脸上补一块写实皮肤。约束只写一处必然如此,
// 所以收成一个函数,四路共用(与 EnsureChineseDialogue 的"五处统一"同一思路)。
func ShotRenderPrompt(proj *project.Project, plan *project.Plan, shot *project.Shot) string {
	return EnsureNoOnScreenText(EnsureChineseDialogue(anchorPrompt(plan, shot, facesOnOf(proj))))
}

// noOnScreenTextMarker 幂等判据(改了文案必须保留这个串,否则会重复叠加)。
const noOnScreenTextMarker = "NO ON-SCREEN TEXT"

// EnsureNoOnScreenText 给镜头提示词补上"画面里不许出现文字"的硬约束(幂等)。
//
// 用户 2026-09-24 报「视频渲染中的文字有时候出现错误的字」——根因是**画面文字从来没被
// 真正禁止**:分镜提示词里那句 exclusions 只是 `e.g.`("例如:不要字幕、不要水印"),
// LLM 写不写、怎么写全凭自觉;而招牌/牌匾/书页/胸牌这类东西一旦进了画面描述,
// 视频模型就会"自己发明"几个字 —— 汉字它认不出,于是画出形近的错字或糊成一团的假字。
//
// 所以这条在**代码层**兜底(与 EnsureChineseDialogue 同一思路:提示词管不住的事,
// 收口到装配点):无论 LLM 写没写,每条出像素的提示词都带上这段。
//
// 注意它不是"一律禁止文字"这么简单,而是分三种情况说清楚,否则模型会走另一个极端
// (该有字的地方也留白,或者反过来把字写错):
//  1. 默认:画面里不要任何文字;
//  2. 剧情确实需要(牌匾/书名/告示):**照抄提示词里给出的原字**,不许自己发明;
//  3. 不需要辨认的远景文字:用无法辨认的抽象笔画带过,绝不拼凑假字。
func EnsureNoOnScreenText(prompt string) string {
	if strings.Contains(prompt, noOnScreenTextMarker) {
		return prompt
	}
	return prompt + "\n\n" + noOnScreenTextMarker + " (mandatory): do not render any readable text in the frame — " +
		"no signage, no banners or plaques, no book or newspaper pages with writing, no letters, no UI, " +
		"no subtitles, no watermarks, no logos, no name tags. " +
		"If a sign or prop inherently carries writing, show it as out-of-focus, cropped away, turned away from " +
		"camera, or as character-free abstract marks: never invent, guess or approximate characters, because " +
		"invented Chinese characters always render as wrong or garbled glyphs. " +
		"If the brief explicitly quotes specific characters, render EXACTLY those characters and nothing else; " +
		"do not add extra words, and if the quoted text is long or uncertain, prefer framing it out of shot.\n" +
		"(中文对照:画面内不得出现任何可辨认的文字 —— 招牌/牌匾/书报/胸牌/字幕/水印/logo 一律不画;" +
		"道具上本来带字的,用失焦、裁掉、转向镜头外或纯抽象笔画表达,**绝不自己发明汉字**" +
		"(模型自造的汉字必然出错);提示词里明确给出的原字必须逐字照抄,不加不减。"
}

// facesOnOf 项目开关(未表态 = 开);proj 为 nil 时按开处理。
func facesOnOf(proj *project.Project) bool {
	if proj == nil {
		return true
	}
	return proj.Config.Params.Script.FacesOn()
}

// PortraitSubject 一张定妆照的装配输入(唯一装配点 PortraitPrompt 的参数)。
type PortraitSubject struct {
	Kind        EntityKind // 空 = 按 name/description/imagePrompt 推断
	Name        string
	Description string
	ImagePrompt string
	// FacesOn 是否按动画介质渲染(项目的 script.stylized_faces)。
	// 资产库那条路恒为 true:库是跨项目的全局资产,而全应用默认就是动漫化角色
	// —— 库里生成真人脸,复用到任一项目都会与渲染侧打架(见 LibraryPortraitPrompt)。
	FacesOn bool
}

// PortraitPrompt **定妆照提示词的唯一装配点**(审计 P1-2)。
//
// 为什么必须收口:这一段(normalizeUniformWording / 取景重写 / styleExpand /
// castContrastBlock / uniformCivilianClarify)此前在**四处**各写一遍,而且**幂等判据
// 各不相同**("RENDER STYLE" / "CAST DISTINCTNESS" / 类型标记串)——
// 于是"重新生成的那张定妆照"与产线那张不是同一套约束,而两者是同一个角色的脸,
// 不一致就是换脸;Q 版那条更糟:它把 Q 版取景**追加**在 LLM 的人像取景之后,
// 同一段提示词里同时要求"大头身"和"upper-body framing, front-facing"。
//
// 幂等只用**一个**判据:该实体类型的 SheetMarker。带过就直接返回 ——
// 重复调用、跨路径复用都不会叠加。
func PortraitPrompt(s PortraitSubject, plan *project.Plan) string {
	kind := NormalizeEntityKind(string(s.Kind))
	if kind == "" {
		kind = InferEntityKind(s.Name, s.Description, s.ImagePrompt)
	}
	prof := EntityProfileOf(kind)
	prompt := normalizeUniformWording(s.ImagePrompt)
	if prof.SheetMarker != "" && strings.Contains(prompt, prof.SheetMarker) {
		return prompt // 已经是本类型的装配结果:不重复叠加
	}
	if prof.RewriteSheetFraming {
		prompt = rewriteSheetFraming(prompt, prof.SheetFraming)
	}
	style := ""
	if plan != nil {
		style = plan.Style
	}
	prompt += styleExpand(style, s.FacesOn, kind)
	prompt += castContrastBlock(plan, s.Name, kind)
	prompt += uniformCivilianClarify(s.Description)
	// 忠实度与画风块由 styleExpand 按类型与开关注入(prof.SheetIdentity / SheetLook,
	// 后者只在 FacesOn 时加)—— 这里**不再单独追加**,否则真人剧项目(开关关)会被
	// 塞进动画介质块,而那是最难查的一类"两边都对、结果不对"。
	return prompt
}

// PortraitPromptForProject 项目内角色卡的便捷包装(取项目开关与方案风格)。
func PortraitPromptForProject(proj *project.Project, plan *project.Plan, ch project.Character) string {
	facesOn := true
	if proj != nil {
		facesOn = proj.Config.Params.Script.FacesOn()
	}
	return PortraitPrompt(PortraitSubject{
		Kind: ResolveEntityKind(ch), Name: ch.Name,
		Description: ch.Description, ImagePrompt: ch.ImagePrompt, FacesOn: facesOn,
	}, plan)
}

// characterImagePrompt 旧名保留为**一行委托**:调用方与测试很多,但实现只能有一个。
func characterImagePrompt(proj *project.Project, plan *project.Plan, ch project.Character) string {
	return PortraitPromptForProject(proj, plan, ch)
}

// sheetFramingWords LLM 写在 image_prompt 尾部的人像取景短语。
//
// 为什么要逐个剥掉而不是"整句替换":这些短语散落在语句中(且大小写/连字符写法不一),
// 剥干净再追加类型取景,才能保证"器物不会同时被要求半身正面"。
var sheetFramingWords = []string{
	"plain background", "plain seamless background", "plain pastel background", "dark background",
	"upper-body framing", "upper body framing", "upper-body", "upper body",
	"front-facing", "front facing",
	"full body", "full-body", "head and shoulders", "portrait",
}

// rewriteSheetFraming 剥掉人像取景短语,追加该类型自己的取景(非人类型专用)。
func rewriteSheetFraming(prompt, framing string) string {
	out := prompt
	for _, w := range sheetFramingWords {
		// 大小写不敏感地整词剥离(replaceAllFold 见下)
		out = replaceAllFold(out, w, "")
	}
	out = strings.Join(strings.Fields(out), " ")
	out = strings.TrimRight(strings.TrimSpace(out), ",;")
	// 剥掉碎片后可能留下 ", ," 这类空档:反复折叠直到稳定(实测
	// "…STYLE, Plain Background, upper-body framing, Front-Facing" → "…STYLE, , ")
	for i := 0; i < 4; i++ {
		before := out
		out = strings.ReplaceAll(out, ", ,", ",")
		out = strings.ReplaceAll(out, " ,", ",")
		out = strings.ReplaceAll(out, ",,", ",")
		out = strings.ReplaceAll(out, "; ;", ";")
		out = strings.ReplaceAll(out, ",;", ",")
		out = strings.ReplaceAll(out, "  ", " ")
		if out == before {
			break
		}
	}
	out = strings.Trim(out, " ,;")
	if strings.TrimSpace(framing) != "" {
		out += ", " + strings.TrimSpace(framing)
	}
	return out
}

// replaceAllFold 大小写不敏感的子串替换(strings.ReplaceAll 是大小写敏感的)。
func replaceAllFold(s, old, new string) string {
	if old == "" {
		return s
	}
	low, lowOld := strings.ToLower(s), strings.ToLower(old)
	var sb strings.Builder
	for {
		i := strings.Index(low, lowOld)
		if i < 0 {
			sb.WriteString(s)
			return sb.String()
		}
		sb.WriteString(s[:i])
		sb.WriteString(new)
		s = s[i+len(old):]
		low = low[i+len(old):]
	}
}

// photorealOverrideNotice 风格要真人、但角色动漫化开关开着时的告警文案(空 = 无冲突)。
//
// 必须让用户看见:否则他会以为"我选了真人实拍风格却没生效",而实际是角色非真人脸
// 这条硬约束把它覆盖了 —— 想拍真人剧就把 script.stylized_faces 关掉。
func photorealOverrideNotice(proj *project.Project, plan *project.Plan) string {
	if proj == nil || plan == nil || !proj.Config.Params.Script.FacesOn() {
		return ""
	}
	if !photorealRequested(plan.Style) && !photorealRequested(proj.Config.Params.Script.StyleHint) {
		return ""
	}
	return "风格里含真人/写实请求,但「角色动漫化(非真人脸)」开关是开的 —— 已按动漫化渲染;" +
		"确实要真人剧请在剧本阶段关掉该开关(参数抽屉)"
}

// genAssetImage Krea-2 生成单张资产图(角色竖幅半身/场景按画幅方向)。
// bookRoster 收集系列兄弟集(同父目录)已确立的角色名+形象设定与场景名,
// 返回注入分镜提示的锚定文案与角色数。跨集角色一致性的根因修复:同集内
// 镜头间角色稳定(定妆照锚定有效),但每集分镜由 LLM 独立撰写,同一角色的
// 形象描述措辞漂移(穿着/毛色/年龄细节不一致),文字把画面拉离定妆照
// (2026-09-09 用户实证「貅兜兜跨集飘逸」)。单集项目返回空。
func (c *Coordinator) bookRoster(projectID string) (string, int) {
	epDir := c.store.Dir(projectID)
	series := filepath.Dir(epDir)
	if filepath.Base(series) == "projects" {
		return "", 0
	}
	type charInfo struct{ name, desc string }
	chars := []charInfo{}
	scenes := []string{}
	seenC, seenS := map[string]bool{}, map[string]bool{}
	eps, err := os.ReadDir(series)
	if err != nil {
		return "", 0
	}
	for _, e := range eps {
		if !e.IsDir() || filepath.Join(series, e.Name()) == epDir {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(series, e.Name(), "plan", "plan.json"))
		if err != nil {
			continue
		}
		var plan struct {
			Characters []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"characters"`
			Scenes []struct {
				Name string `json:"name"`
			} `json:"scenes"`
		}
		if json.Unmarshal(raw, &plan) != nil {
			continue
		}
		for _, ch := range plan.Characters {
			n := strings.TrimSpace(ch.Name)
			if n == "" || seenC[n] {
				continue
			}
			seenC[n] = true
			if len(chars) < 8 {
				d := strings.TrimSpace(ch.Description)
				if r := []rune(d); len(r) > 110 {
					d = string(r[:110]) + "…"
				}
				chars = append(chars, charInfo{n, d})
			}
		}
		for _, sc := range plan.Scenes {
			n := strings.TrimSpace(sc.Name)
			if n != "" && !seenS[n] {
				seenS[n] = true
				if len(scenes) < 10 {
					scenes = append(scenes, n)
				}
			}
		}
	}
	if len(chars) == 0 && len(scenes) == 0 {
		return "", 0
	}
	var sb strings.Builder
	sb.WriteString("【系列角色一致性·必须遵守】以下角色已在前几集登场并定妆,本集若出现这些角色:" +
		"①必须严格沿用原名(不得增减字/换同音字/另起别名);" +
		"②**同一个人可能以不同称呼出现**(名字与代号/编号并存,如 机关人 / 阿枢 / 一三七四一号)——只要判定是同一人,就必须共用同一张角色卡的同一个名字与同一份形象设定,不得因为换了称呼就新建一个角色、另起一份设定;" +
		"③角色表描述与每镜 H3 提示词中的形象必须与下面给出的设定逐项一致(毛色/服饰/年龄/特征细节不得改动):\n")
	for i, ch := range chars {
		fmt.Fprintf(&sb, "%d. %s:%s\n", i+1, ch.name, ch.desc)
	}
	if len(scenes) > 0 {
		sb.WriteString("已确立的场景名(再次出现时沿用原名):")
		for _, n := range scenes {
			sb.WriteString("「" + n + "」")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("本集新登场的人物/场景属正常剧情推进,可另起新名与新设定,但不得与上述既有角色混淆或顶替。")
	return sb.String(), len(chars)
}

func styleExpand(style string, facesOn bool, kind EntityKind) string {
	if strings.TrimSpace(style) == "" && !facesOn {
		return ""
	}
	prof := EntityProfileOf(kind)
	s := strings.ToLower(style)
	has3D := strings.Contains(s, "3d") || strings.Contains(s, "bjd") || strings.Contains(style, "虚拟人") || strings.Contains(style, "虚拟偶像")
	hasAnime := strings.Contains(style, "国漫") || strings.Contains(style, "动漫") || strings.Contains(style, "动画") || strings.Contains(s, "anime") || strings.Contains(s, "donghua")
	hasComic := strings.Contains(style, "美漫") || strings.Contains(s, "comic")
	hasInk := strings.Contains(style, "水墨") || strings.Contains(style, "国画")
	hasReal := strings.Contains(style, "真人") || strings.Contains(style, "写实电影") || strings.Contains(s, "photoreal")
	hasQ := strings.Contains(style, "q版") || strings.Contains(style, "呆萌")
	var terms []string
	stylized := true
	switch {
	case facesOn && photorealRequested(style):
		// 动漫化开关打开时,真人/写实风格请求被明确覆盖(2026-09-24 用户定调
		// 「角色不要真人脸,动漫化,避免侵权」)。写实风格词与哑光平涂的动画约束
		// 直接打架,同时交给模型只会得到"既写实又扁平"的糊图 —— 覆盖掉,并由
		// 调用方告警告知用户"要真人剧请关掉角色动漫化开关"。
		terms = append(terms, "Chinese donghua (animation) character design, anime-influenced stylized features, clean cel shading")
		if strings.TrimSpace(style) != "" {
			terms = append(terms, "overall art direction: "+strings.TrimSpace(style))
		}
	case has3D:
		// 注意:**风格块绝不能改写面部特征**。此前这里写着 "large glossy stylized eyes",
		// 与角色卡的面部描述(如"细长单眼皮")直接打架 —— 定妆照被风格"重新设计"成圆软脸
		// 大眼,而视频按文字走,两边永远对不上(2026-09-12 用户报一致性丢失,根因即此)。
		// 风格只描述材质与渲染方式(表面/光泽/媒介),形状类词汇一律不写。
		// 2026-09-24:原措辞 "smooth poreless synthetic skin"(光滑无孔合成皮肤)正是
		// 用户报的"人物油腻"的一半来源 —— 无孔 = 塑料 = 皮脂高光。改成哑光粉质;
		// 同时**去掉皮肤/五官类词汇对非人类型的影响**:器物只有材质,没有皮肤。
		if kind == KindHuman || kind == KindSpiritPet || kind == KindBeast || kind == KindChibi {
			terms = append(terms, "next-generation 3D CG character render, stylized BJD-inspired sculpted forms, "+
				"matte powdery skin shading with a dull even finish, collectible virtual figure look")
		} else {
			terms = append(terms, "next-generation 3D CG render, stylized sculpted forms, "+
				"matte dull even finish across every surface, collectible virtual figure look")
		}
		if hasAnime {
			terms = append(terms, "Chinese donghua animation styling, semi-realistic cel-and-CG hybrid shading")
		}
	case hasAnime:
		terms = append(terms, "Chinese donghua (animation) character design, anime-influenced stylized features, clean cel shading")
	case hasComic:
		terms = append(terms, "American comic-book illustration, bold ink outlines, saturated flat colors, halftone dots")
	case hasInk:
		terms = append(terms, "Chinese ink-wash painting style, expressive brush outlines, negative space")
	case hasReal:
		terms = append(terms, "photorealistic cinematic portrait, realistic skin texture") // 真人剧是合法风格(需关掉角色动漫化)
		stylized = false
	default:
		// 无已知风格关键词:不强行改写用户的风格描述,**但类型忠实度与画风块必须留下** ——
		// 那段不是"风格",是"这件东西长什么样"的地基(器物抄结构 / 灵体抄形体)。
		// 旧实现这里直接 return "",连忠实度块一起丢了。
		stylized = false
	}
	if hasQ {
		terms = append(terms, "cute chibi proportions")
	}
	// 忠实度与画风块**按类型取**(人类抄脸 / 器物抄结构 / 灵体抄形体):同一句 human 条款
	// 套在铜雀身上,就是在要求它长一张脸。
	byKind := prof.SheetIdentity
	if facesOn {
		// 动漫化 + 哑光硬约束:与渲染侧的介质块是同一件事的两头,改动要成对。
		byKind += prof.SheetLook
	}
	if len(terms) == 0 {
		return byKind
	}
	tail := ". Render strictly in this exact art style."
	if stylized {
		tail = ". The output must read as a stylized render in this exact art style."
	}
	if strings.TrimSpace(style) == "" {
		return "\n\nRENDER STYLE (mandatory): " + strings.Join(terms, "; ") + tail + byKind
	}
	return "\n\nRENDER STYLE (mandatory): " + strings.TrimSpace(style) + " — " + strings.Join(terms, "; ") + tail + byKind
}

// identityFidelityBlock 忠实度硬约束:定妆照必须与角色卡描述逐项一致,风格不得改写长相。
//
// 为什么必须有它(2026-09-12 一致性事故的根因修复):Krea-2 有强烈的"美人先验"——
// 描述写"清瘦长脸、窄下巴、细长单眼皮、眉骨略高",它照样画成圆软脸大眼。而定妆照是
// 全片锁脸的锚:它一旦偏离描述,视频(按文字渲染)与多视角(按定妆照转身)就两边都对
// 不上,用户看到的就是"同一个人三张脸"。所以把"按描述画、不许美化"写成硬约束。
//
// 这条只作用于**角色**(styleExpand 只在角色路径被追加),不影响场景图。
func identityFidelityBlock() string {
	return "\n\nIDENTITY FIDELITY (mandatory): the face must match the written brief EXACTLY — " +
		"same face shape and jaw, same chin, same eye shape and eyelids, same brow shape, same nose, same hair length and style. " +
		"Art style changes the RENDERING SURFACE only (material, sheen, shading); it must NEVER change the character's features. " +
		"Do not beautify, do not enlarge or round the eyes, do not soften a sharp jaw, do not make the subject younger, prettier or more handsome than described. " +
		// 实测(2026-09-12):模型会自作主张在制服胸口加一块带字的胸牌(画出了「老东」——把两个角色名拼在一起),
		// 服装上除角色卡明确要求的标识外不得有任何文字。
		"Do NOT add any lettering, name tag text or emblem text to the clothing unless the brief explicitly asks for it."
}

// uniformCivilianClarify 民用制服"去警用化"补充说明。
//
// 为什么需要(2026-09-12 用户实证「保安制服不要渲染的和警察服一样啊」):中式保安服与
// 警服本就相似,模型对"黑制服 + 对讲机"的先验直接画成警服 —— 实测出了肩章星徽与带字
// 胸牌。Krea-2 是蒸馏模型(CFG 1.0),**负面词基本不生效**,所以这里用正面、具体的
// 描述把"普通民用工装"讲清楚(中英双写:定妆照走英文字面,视频提示词也带同一段)。
//
// 仅在角色描述命中"保安/门卫/看守/协管/值勤"等民用制服角色时追加,不干扰其它角色。
func uniformCivilianClarify(text string) string {
	hit := false
	for _, k := range []string{"保安", "门卫", "看守", "协管", "值勤", "更夫", "巡夜"} {
		if strings.Contains(text, k) {
			hit = true
			break
		}
	}
	if !hit {
		low := strings.ToLower(text)
		for _, k := range []string{"security guard", "security uniform", "watchman", "attendant uniform"} {
			if strings.Contains(low, k) {
				hit = true
				break
			}
		}
	}
	if !hit {
		return ""
	}
	// 纯正面描述:**绝不**在提示词里列举"不要肩章/警徽/臂章"—— 蒸馏模型(CFG 1.0)对否定
	// 词免疫,却会照着名词画出来。实测第一版写了 no epaulettes / no metal badge,画面反而
	// 多了肩章星徽、胸徽与臂章,比修之前更像警服。只描述"它是什么"。
	return "\n\nCIVILIAN WORKWEAR (mandatory): the character is a civilian staff member on shift at a market gate. " +
		"He wears an ordinary shop-floor work shirt — a plain, slightly faded single-colour short-sleeved shirt of cheap cotton, collar open, " +
		"with a small woven cloth name strip stitched above the chest pocket, a staff ID card hanging on a simple lanyard, " +
		"and a two-way radio hooked on the shirt pocket; plain dark trousers and a soft canvas belt. " +
		"Everything reads as the everyday working clothes of an ordinary shop or factory employee — a bit worn and unremarkable. " +
		"(中文对照:普通民用工装——略旧的廉价纯色短袖工作衬衫,领口敞开,胸袋上方一小条布质名牌,工牌挂简易挂绳,对讲机挂在胸袋上,深色长裤 + 布腰带;整体就是普通商场/厂区员工的日常工装,平淡、旧。)"
}

// normalizeUniformWording 把"保安制服 / security uniform"这类名词短语就地换成具体的民用
// 工装描述(定妆照与视频提示词共用同一套改法)。
//
// 为什么必须改源文本而不是只加补充段:模型对名词短语的权重最高,"black security uniform"
// 本身足以触发警服先验 —— 实测补充段写明"这是民用工装、不是警服"后,画面依旧画出肩章与
// 胸徽。把名词短语本身换掉,再配上面的正面描述,才拉得回来。
func normalizeUniformWording(text string) string {
	for _, r := range []struct{ from, to string }{
		{"黑保安制服", "普通民用保安工装(廉价纯色短袖工作衬衫,胸袋上方布质名牌,胸前挂对讲机)"},
		{"保安制服", "普通民用保安工装(廉价纯色工作衬衫 + 布质名牌 + 对讲机)"},
		{"保安服", "普通民用保安工装"},
		{"black security uniform", "plain civilian work shirt of a shop-floor employee (cheap single-colour short-sleeved shirt with a small cloth name strip)"},
		{"security uniform", "plain civilian work shirt of a shop-floor employee"},
		{"guard uniform", "plain civilian work shirt"},
	} {
		text = strings.ReplaceAll(text, r.from, r.to)
	}
	return text
}

// castContrastBlock 同剧班底对比块:列出本剧其它角色的关键形象,追加到
// Krea-2 提示词尾部,强制与同剧角色拉开差异(2026-09-11 用户定调
// 「不同角色脸不能相似」)。实证:LLM 已写了 fat/scar/narrow face 等
// 差异化特征,但逐字相同的巨型风格前缀+模型帅哥先验把特征吞掉(肥胖
// →清瘦、疤→无疤)。生成时给模型看全剧班底,它才知道"要和谁不同"。
// 注入的提示词随资产入库,库内重生成/跨集复用自带对比上下文。
//
// kind 决定"差异"落在哪:人/Q 版比脸,器物比形制与材质,妖兽比物种与体量。
// 同一段"脸型、年龄、肤色调开"的措辞套在铜雀上,就是在要求它长一张脸
// (2026-09-24 用户报「阿凿是一只铜雀却渲染成人物」的连带根因)。
func castContrastBlock(plan *project.Plan, selfName string, kind EntityKind) string {
	if plan == nil {
		return ""
	}
	self := strings.TrimSpace(selfName)
	var parts []string
	for _, ch := range plan.Characters {
		n := strings.TrimSpace(ch.Name)
		if n == "" || n == self {
			continue
		}
		d := strings.TrimSpace(ch.Description)
		if r := []rune(d); len(r) > 60 {
			d = string(r[:60]) + "…"
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", n, d))
		if len(parts) >= 6 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	traits := "different face shape, age, build, skin tone and unique markers; these identity traits MUST dominate the image; " +
		"never average them into a generic handsome/pretty face"
	one := "exactly ONE person appears in the frame"
	switch NormalizeEntityKind(string(kind)) {
	case KindObject:
		traits = "a different silhouette, form, part count, material, colour and finish — it must never be mistaken for a person " +
			"or for a living creature; these traits MUST dominate the image"
		one = "exactly ONE object appears in the frame"
	case KindSpiritPet, KindBeast:
		traits = "a different species, size class, build, coat or scale pattern and unique markers; these traits MUST dominate the image; " +
			"never turn it into a generic cute mascot"
		one = "exactly ONE creature appears in the frame"
	case KindIncorporeal:
		traits = "a different form, colour, glow and degree of transparency; these traits MUST dominate the image"
		one = "exactly ONE presence appears in the frame"
	case KindChibi:
		one = "exactly ONE chibi figure appears in the frame"
	}
	return "\n\nCAST DISTINCTNESS (reference note, NOT to be drawn) — other cast members of this series: " + strings.Join(parts, "; ") +
		". They are listed ONLY so that the subject depicted here (「" + self + "」) is clearly distinguishable from every one of them: " +
		traits + ". " +
		// 实测(2026-09-12):不点明"不要画出来"时,模型会把名单里的其他角色当画面元素塞进角落
		// (郑伯的定妆照里出现了小尺寸的卫东与 Q 版江砚)。名单只作差异化参考:画面里只能有本体一个。
		"Do NOT depict any of these other characters: " + one + " — 「" + self + "」."
}

// backfillAssets 渲染前自动补齐缺失的参考图:资产阶段未跑/未生成全时,
// 就地用本地 Krea-2 生成,绝不静默降级纯文生(2026-09-08 逻辑重构:
// 「定妆照没生成就渲染」被用户点名的反直觉行为根治)。
// 资产生成只依赖本地 ComfyUI,与本次渲染引擎选择无关(云端渲染也本地补图)。
var StylePresets = map[string]struct{ Name, Prompt string }{
	"urban_xianxia": {"都市仙侠霓虹", "CHINESE URBAN-XIANXIA, NEON-NOIR WET-STREET LIGHTING, CINEMATIC ANIMATION, GOD RAYS THROUGH SKYSCRAPERS"},
	"guoman_2d":     {"国漫二维动画", "CHINESE 2D ANIME STYLE, CLEAN LINEWORK, VIBRANT CEL SHADING, DONGHUA QUALITY"},
	"live_action":   {"真人实拍质感", "PHOTOREALISTIC LIVE-ACTION CINEMATOGRAPHY, 35MM FILM, SHALLOW DEPTH OF FIELD, NATURAL SKIN TEXTURE"},
	"pixar_3d":      {"3D 皮克斯", "PIXAR-STYLE 3D RENDER, SOFT GLOBAL ILLUMINATION, EXPRESSIVE STYLIZED CHARACTERS, SUBSURFACE SCATTERING"},
	"ink_wash":      {"水墨国风", "CHINESE INK WASH PAINTING STYLE, BRUSH TEXTURE, NEGATIVE SPACE, MISTY MOUNTAINS, TRADITIONAL AESTHETIC"},
	"cyberpunk":     {"赛博朋克", "CYBERPUNK, RAIN-SOAKED STREETS, HOLOGRAPHIC ADS, CHROME AND NEON, BLADE RUNNER MOOD"},
	"ghibli":        {"吉卜力手绘", "GHIBLI-INSPIRED HAND-PAINTED, WARM WATERCOLOR BACKGROUNDS, SOFT WIND AND LIGHT, WHIMSICAL SERENITY"},
	"film_noir":     {"黑色电影", "FILM NOIR, HIGH CONTRAST BLACK AND WHITE WITH ONE ACCENT COLOR, VENETIAN BLIND SHADOWS, SMOKEY"},
	"hongkong":      {"港片胶片", "HONG KONG CINEMA 1990s, SATURATED NEON, TEAL-ORANGE GRADE, ANAMORPHIC FLARES"},
	"claymation":    {"黏土定格", "CLAYMATION STOP-MOTION STYLE, FINGERPRINT TEXTURE, TACTILE HANDMADE CHARM"},
}

// composeStyleHint 风格预设(可组合)拼接 + 用户自由文本。
func composeStyleHint(styles []string, freeHint string) string {
	var parts []string
	for _, k := range styles {
		if p, ok := StylePresets[k]; ok {
			parts = append(parts, p.Prompt)
		}
	}
	out := strings.Join(parts, " + ")
	if strings.TrimSpace(freeHint) != "" {
		if out != "" {
			out += "; "
		}
		out += strings.TrimSpace(freeHint)
	}
	return out
}
