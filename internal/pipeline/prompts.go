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
	var chars, scenes []anchorChar
	// shotKinds 本镜出场角色的**类型**(与 chars 分开收):比例锚点的分发闸只看"镜头里是
	// 谁",与平台手上有没有这个角色的描述无关 —— 而 chars 只收有描述的(没描述没得可锚)。
	var shotKinds []EntityKind
	for _, cid := range shot.Characters {
		for _, ch := range plan.Characters {
			if ch.ID != cid {
				continue
			}
			shotKinds = append(shotKinds, ResolveEntityKind(ch))
			if strings.TrimSpace(ch.Description) == "" {
				continue
			}
			chars = append(chars, anchorChar{
				name: ch.Name, desc: strings.TrimSpace(ch.Description),
				voice: strings.TrimSpace(ch.Voice), kind: ResolveEntityKind(ch),
			})
		}
	}
	for _, sc := range plan.Scenes {
		if sc.ID == shot.Scene && strings.TrimSpace(sc.Description) != "" {
			scenes = append(scenes, anchorChar{name: sc.Name, desc: strings.TrimSpace(sc.Description)})
		}
	}
	// 【镜头比例锚点】(2026-09-26)按景别注入技能侧的人体比例口径 —— 定妆照那条链上一直
	// 有它,镜头提示词里一个都没有(实测 64 个镜头 0 个),于是每一镜各按各的审美发挥比例,
	// 跨镜还对不上。分发规则、口径来源与两道闸见 shot_proportion.go。
	//
	// **已有技能侧比例句时不注入**(判据复用 proportion_clause.go 的
	// HumanProportionClausePresent,不在这里另写一套):同一段提示词里两套比例,模型只会
	// 取中值 —— 技能侧那一段是它的定义权,平台只让位、不重写。这条判据同时就是幂等性:
	// 注入的正文本身带着技能侧的特征短语,任何已带锚点的提示词再进这条路都会被认出来。
	//
	// 它先算出来而不是只在主分支里追加:这条闸只看"景别 + 镜头里是谁",与有没有场景、
	// 角色有没有描述无关,所以下面的纯空镜早退分支也要带上(在册但没描述的角色同样是有人)。
	proportion := ""
	if !HumanProportionClausePresent(shot.H3Prompt) {
		proportion = shotProportionBlock(shot.ShotSize, shotKinds)
	}
	if len(chars) == 0 && len(scenes) == 0 {
		// 无角色也无场景(纯空镜):身份锚定没什么可锚的,但**风格段与介质覆盖仍必须带上**
		// —— 空镜一样要服从全片画风。此前这条早退分支直接把风格与介质段丢了,
		// 结果是"空镜写实、其余动画"的花片(实测真实项目第 1 镜正是无人物空镜)。
		return styleAndMediumBlock(plan, facesOn) + proportion + shot.H3Prompt
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
	// 外貌不变项(2026-09-25 用户报「19 镜后面主角的头发飘逸了」)。
	//
	// 实测:第01集 26 镜里,**每一镜的提示词都写着 "short low ponytail … brass ring …
	// reflective strip"**(逐镜核对过),而渲染出来的 19/20/22 镜头:头发变长变散、
	// 额前垂下一大绺、马尾上的黄铜"禾"扣不见、夹克下面的背心被简化成一件内搭。
	// 也就是说文字是对的、**画面漂了** —— 而且是"随剧情放松"式漂移:一旦画面里事情变多
	// (折纸、搬箱、甩上肩),模型就优先保住动作,把外貌细节当装饰丢掉。
	// 更关键的是:2 张参考图与 3 张参考图的镜头都漂(与拼合图无关),所以只能从**锁定措辞**
	// 上收紧 —— 用"这三条不许动"把外貌从"形容词"提升为"事实"。
	//
	// **必须按实体类型分发**(审计):这段写的是发型/发长/服装层数 —— 人形词汇。
	// 器物/灵体镜头里同时出现「ARTEFACT CLAUSE: no human face, no living eyes, no
	// flesh」与「头发不得随时间变长」,等于把"头发/服装"重新塞回条件里,有把铜雀、
	// 妖兽拉回人形的风险(2026-09-24 报障「阿凿是一只铜雀却渲染成人物」的同一类)。
	// 与 PortraitHairClause(已按 Kind 分发)保持同一写法:只对有人物头发的类型注入。
	if hasHumanLikeChar(chars) {
		sb.WriteString("【外貌不变项(逐帧核对,不得随剧情放松)】以下每一条都是**身份事实**而不是形容词," +
			"任何镜头、任何动作、任何情绪下都不得改变:\n" +
			"- 发型与**发长**:参考图上是短发就是短发。头发不得随时间变长、变散、变成飘动的长发;" +
			"只保留参考图上看得见的那些碎发,不许自行加长度、加发量、加飘动。\n" +
			"- 配件:角色卡上写到的每一件(发饰、腕环、挎带、工牌等)都要在参考图显示它的位置上、" +
			"以同样的颜色与材质出现;**不许因为画面太满就拿掉某一件**。\n" +
			"- 服装层数:参考图上是几层就是几层(如夹克 + 背心 + 内搭),不许简化成一件," +
			"也不许把背心画成内搭、把内搭画成背心。\n")
	}
	// 声线锚定(2026-09-25 用户报「人物配音错误异常」):角色卡上的 voice 此前**从未注入**
	// 镜头提示词,逐镜声线全靠 LLM 现写 —— 实测同一角色在不同镜头被写成
	// "young female, low and even…" / "young female voice, low and even…" /
	// "…speaks from the chest" / "…speaking from the chest" 这类细微措辞漂移,
	// 而 H3 是把这段文字当音频条件用的:措辞一变,音色就跟着变。
	hasVoice := false
	for _, c := range chars {
		if c.voice != "" {
			hasVoice = true
			break
		}
	}
	if hasVoice {
		sb.WriteString("【声线(逐字照抄,不得改写)】本镜每句台词的 [voice description] 必须与下面这一条**逐字一致**;" +
			"同一角色在任何镜头都用同一句,两个角色的声线不得相同:\n")
		for _, c := range chars {
			if c.voice == "" {
				continue
			}
			fmt.Fprintf(&sb, "- %s: %s\n", c.name, c.voice)
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
		//
		// **RenderLook 要与图像侧同一道守卫**:上游点名写实/3D 时不能加这一块 ——
		// 它是介质条款("手绘线稿脸 / 赛璐璐平涂 / 哑光"),而同一段提示词里
		// styleAndMediumBlock 正在写「以上风格由剧集设定指定,照此渲染」+ STYLE AUTHORITY。
		// 两句对撞,模型只会二选一地糊:逐镜摇摆、跨集画风不一致,而定妆照按 styleExpand
		// 的 photoreal 分支画成写实 —— 又回到"参考图与成片不在同一个视觉世界"那个老根因。
		// 图像侧早就加了这个守卫(styleExpand: facesOn && !styleIsPhotoreal),
		// 视频侧漏了,而视频侧才是真正出像素的那一端。
		styleIsPhotoreal := photorealRequested(planStyle(plan))
		seen := map[EntityKind]bool{}
		for _, c := range chars {
			if seen[c.kind] {
				continue
			}
			seen[c.kind] = true
			p := EntityProfileOf(c.kind)
			if facesOn && !styleIsPhotoreal && p.RenderLook != "" {
				sb.WriteString(strings.TrimRight(p.RenderLook, "\n") + "\n")
			}
			if p.AntiForm != "" {
				sb.WriteString(p.AntiForm + "\n")
			}
		}
	}
	sb.WriteString(styleAndMediumBlock(plan, facesOn))
	// 比例锚点放在**最后**(紧贴 shot.H3Prompt)是有意的:它是"别让比例跑掉"的约束而不是
	// 剧情内容,而它要防的正是紧随其后的画面 —— 动作一多,模型优先保动作、把形体当装饰丢掉
	// (与【外貌不变项】记的那次漂移同一机理),所以取离画面最近的注意力位置。
	sb.WriteString(proportion)
	return sb.String() + "\n" + shot.H3Prompt
}

// anchorChar 身份锚定里的一条出场对象(角色带类型与声线,场景只用名字+描述)。
type anchorChar struct {
	name, desc string
	voice      string
	kind       EntityKind
}

// hasHumanLikeChar 本镜是否有"有头发/有服装"类型的主体(人类 / Q 版)。
//
// 用于把"发型与发长、服装层数"这类**人形外貌**条款只发给该发的镜头:器物/妖兽/
// 灵宠/无实体镜头里出现这些词,等于把主体往人形上拽。
func hasHumanLikeChar(chars []anchorChar) bool {
	for _, c := range chars {
		switch NormalizeEntityKind(string(c.kind)) {
		case KindHuman, KindChibi:
			return true
		}
	}
	return false
}

// planStyle 安全取全片风格串(plan 为 nil 时返回空串)。
func planStyle(plan *project.Plan) string {
	if plan == nil {
		return ""
	}
	return plan.Style
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

// EnsureVoiceoverClause 画外音镜次的"嘴唇闭合"兜底(幂等)。
//
// 依据:技能端产出的台词里 61% 是画外音(旁白/内心/系统音),而 v1 的提示词把它们
// 与画内对白写得一模一样 —— H3 只看见一句 <d>[Chinese]…</d>,看不见"谁不许开口",
// 于是让画面里的角色张嘴把旁白念了出来(用户报的「系统旁白,主角动嘴」)。
//
// 措辞照抄 internal/director 的官方式写法("...while the on-screen characters' lips
// remain completely closed"),两侧同一句:技能端产出时写、平台这里兜底补,幂等可重复调用。
// 判据是 project.VoiceoverClauseMarker,改文案必须保留那个串。
func EnsureVoiceoverClause(sh *project.Shot) bool {
	if sh == nil || !project.HasVoiceover(*sh) {
		return false
	}
	if strings.Contains(sh.H3Prompt, project.VoiceoverClauseMarker) {
		return false
	}
	sh.H3Prompt = strings.TrimRight(sh.H3Prompt, "\n") + "\n\n" +
		"VOICEOVER (mandatory): every line flagged as voice-over in this shot is spoken off-screen — " +
		"the on-screen characters' lips remain completely closed while that voice is heard; " +
		"no on-screen character mouths those words, and the voice stays off-frame and disembodied."
	return true
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
	EnsureVoiceoverClause(shot) // 画外音镜次补"嘴唇闭合"(幂等,在装配点做一次)
	return EnsureVoiceInline(EnsureOnScreenTextRule(EnsureChineseDialogue(anchorPrompt(plan, shot, facesOnOf(proj)))), plan, plan.Characters)
}

// onScreenTextMarker 幂等判据(改了文案必须保留这个串,否则会重复叠加)。
const onScreenTextMarker = "ON-SCREEN TEXT (QUOTED, VERBATIM)"

// EnsureOnScreenTextRule 画面文字规则(幂等)。
//
// **方向依据官方指南修正**(2026-09-25 用户要求"参考全网找解决方案,不要自己想当然"):
// 我先前写的是"画面内一律不得出现可辨认文字",而官方给的是**正向解法** ——
// MiniMax 官方基础指南 §4.5:画面里实际可见的招牌/横幅/标签/字幕/霓虹文字都要放进
// **英文双引号**、逐字保留原字与标点、不翻译;ComfyUI 官方提示词指南的社区实测进一步
// 说明:**把每段文字用引号逐条枚举出来能保持清晰可读,未定义的文字才容易变成乱码符号**。
//
//	https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/base-en.txt
//	https://docs.comfy.org/zh/tutorials/video/minimax/minimax-h3-prompt-guide
//
// 所以规则改成:该出现的文字**必须引号逐字给出**;引号之外不得出现任何文字(那才是
// 乱码的来源)。这样既不丢剧情需要的文字(牌匾/招牌/书名),也不会让模型自己造字。
func EnsureOnScreenTextRule(prompt string) string {
	if strings.Contains(prompt, onScreenTextMarker) {
		return prompt
	}
	return prompt + "\n\n" + onScreenTextMarker + " (mandatory):\n" +
		// ① 该出现的文字:仍按官方指南"引号逐字"写,但**必须贴在承载它的物件上**
		"1. The only text allowed to be visible in the frame is text the shot explicitly calls for as part of the " +
		"scene (a shop sign, a plaque, a banner, a letter, a book cover). Each such piece MUST be written out inside " +
		"English double quotation marks, verbatim, in its original characters and never translated, and it must " +
		"appear ON the object that carries it — never as a centered caption/subtitle line.\n" +
		// ② 本条是本次真机实测的修法(见下):我们自己的说明文字被画进了画面
		"2. NEVER turn this prompt's own text into picture content. Every instruction, note, list item and label in " +
		"this prompt — voice descriptions, style/medium notes, subject definitions, retention analysis, manifests, " +
		"offscreen-narration notes and any bracketed annotation such as 【…】 or […] — is addressed to YOU as a " +
		"production note. Do not draw it, do not caption it, do not subtitle it and do not speak it aloud.\n" +
		// ③ 字幕/系统面板一律由后期加,模型自己烧上去就是废片
		"3. No burned-in subtitles, no captions, no lower-third text, no watermarks, no logos and no floating UI " +
		"panels or status cards. This film's captions — and any system/status panel — are composited in " +
		"post-production, not by you.\n" +
		// ④ 剧本括号批注:是"内容",不是"画面文字"
		"4. Bracketed plot annotations in the shot text (e.g. 【检测到宿主处于…状态】) are STORY CONTENT to act on — " +
		"render the moment they describe, never the characters inside the brackets.\n" +
		// ⑤ 官方口径的兜底(原样保留):引号之外不得有字,没有就一个都不写
		"5. No other text may appear anywhere in the frame: do not invent, guess or approximate characters for any " +
		"surface you did not quote, and omit text entirely when the shot does not call for it.\n" +
		"(中文对照:① 画面里真要出现的文字(招牌/匾额/标签)**必须用英文双引号逐条写出、逐字保留**,并贴在" +
		"承载它的物件上 —— 不许做成居中的字幕条;② 提示词自己的任何说明文字(**声线描述、画风与介质说明、" +
		"subject_definitions、retention_analysis、编号清单、旁白说明,以及【…】这类括号批注**)都是写给模型的" +
		"制作说明,**不许画进画面、不许当字幕、不许念出来** —— 真机实测里被念出来/被烧成字幕的正是这些行;" +
		"③ **不许烧任何字幕/说明条/水印/系统面板**:本片的字幕与系统面板一律由后期合成;④ 剧本里的括号批注" +
		"(如【检测到宿主处于…状态】)是**剧情内容**,要把它描述的戏演出来,而不是把括号里的字显示出来。)"
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
	// 图像侧的两道预处理(见 artstyle.go 的说明):
	//   ① 剥掉微雕文字/极小铭文 —— 在半身幅里它们只会糊成灰渍(实测定妆照胸口与腕部的污渍);
	//   ② 姿态纪律 —— 半身幅里复杂手势会变成连指手套(实测模型自作主张画了抱胸)。
	prompt := StripMicroTextForImage(normalizeUniformWording(s.ImagePrompt))
	// 定妆照取景与本片镜头的景别对齐(见 sheet_framing.go):**半身**。
	// 卡里若同时写着取景 `upper-body framing` 与全身比例 `seven and a half heads`,模型会选
	// "按比例画全身"(2026-09-26 实测出图就是全身立绘),而这张图要喂给近景/中景/特写
	// (64 镜里 61 镜)——全身像被要求"裁"出特写,头肩比必然漂。**只剥全身口径**,脸部比例全留。
	if kind == KindHuman {
		prompt = StripFullBodyProportions(prompt)
	}
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
	// 画风块由 styleExpandFor 注入,并把**已装配的提示词**带进去:卡里若已带技能侧的人体
	// 比例锚点句,平台自己的比例措辞("cute chibi proportions" / Q 版二到三头身)让位
	// —— 同一段里两套比例,模型只会取中值(技能侧清单 §5.1,见 proportion_clause.go)。
	prompt += styleExpandFor(prompt, style, s.FacesOn, kind)
	prompt += castContrastBlock(plan, s.Name, kind)
	prompt += uniformCivilianClarify(s.Description)
	prompt += PortraitHairClause(kind)
	prompt += PortraitPoseClause(kind)
	// 忠实度与画风块由 styleExpand 按类型与开关注入(prof.SheetIdentity / SheetLook,
	// 后者只在 FacesOn 时加)—— 这里**不再单独追加**,否则真人剧项目(开关关)会被
	// 塞进动画介质块,而那是最难查的一类"两边都对、结果不对"。
	return prompt
}

// PortraitHairClause 定妆照的**发型纪律**(人类 / Q 版)。
//
// 事故(2026-09-25,用户报「19 镜后面主角的头发飘逸了」):角色卡写的是"短低马尾",
// 而定妆照**自己给额前加了几缕碎发**。按我们自己的规矩"参考图是唯一权威",H3 于是忠实
// 复现那几缕碎发,一进入动作段就被放大成"头发变长变散、额前垂一大绺"。
// 也就是说:**渲染侧的漂移,根子在定妆照**;而在定妆照上继续追,根子在"简报没说额前状态"。
//
// 所以这一条同时约束三件事 —— 束法、长度、**额前状态** —— 并明确"简报没说就别自造"。
// 只对有人物头发的类型注入(器物/灵体没有发型可谈)。
func PortraitHairClause(kind EntityKind) string {
	switch NormalizeEntityKind(string(kind)) {
	case KindHuman, KindChibi:
	default:
		return ""
	}
	return "\nHAIR EXACTLY AS BRIEFED (mandatory): draw the hairstyle exactly as the brief states — the same TIE " +
		"(low ponytail / high ponytail / bun / loose), the same LENGTH, and the same FOREHEAD STATE. If the brief " +
		"says the hair is tied back, then the forehead and the cheeks show AT MOST the wisps the brief actually " +
		"mentions: do not invent extra strands, do not let hair fall across the eyes or the face, and never " +
		"lengthen the hair or turn a tied style into loose flowing hair.\n" +
		"(中文对照:发型照简报画 —— 束法、发长、**额前状态**三者与简报一致。简报写的是束起的," +
		"额前与脸颊就最多只有简报里写明的那几缕:不许自造碎发、不许让发丝遮住眉眼、" +
		"不许把头发加长,更不许把束发画成垂在额前的散发。这条是给“参考图”定的规矩," +
		"因为参考图就是全片身份的权威 —— 图上多一缕,成片里就会多一大绺。)\n"
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

// SceneImagePrompt **场景参考图的唯一装配点**(与角色侧的 PortraitPrompt 对称)。
//
// 【为什么必须收口(2026-09-26 乙1,本批唯一未修的高危)】场景图此前**四条路径各自
// 把 LLM 的 image_prompt 原样丢给图像模型**:资产阶段的 check、渲染前的 backfillAssets、
// 服务端上传入库、服务端视频截帧入库。后果是**一部剧里两套画风** —— 角色走
// styleExpand(风格串 + 介质块 + 忠实度),场景走裸提示词:上游风格是国漫二维,
// 人物的定妆照是赛璐璐平涂,而(空)景图可能被画成写实摄影或另一套介质。
// 而场景图同样是 H3 的参考图之一,**参考图与成片不在同一个视觉世界**时,模型只能
// 二选一地糊(2026-09-24「定妆照与成片不在同一个视觉世界」那条根因的图像版)。
//
// 与角色侧的对称关系(这就是它能复用同一套守卫的原因):
//   - 同样注入**全片风格串**(plan.Style,经 styleExpand 具象化);
//   - 同样复用 styleExpand 里的**写实守卫**:上游点名写实/3D 时不叠"二维/赛璐璐"
//     介质块(同一段提示词里两个介质打架,模型只会二选一地糊);
//   - 同样只有一个幂等判据(KindScene 的 SheetMarker,即 SCENE FIDELITY)。
//
// 与角色侧**不同**的只有两处:取景走环境图/空场(见 entity_prompts.go 的 KindScene),
// 忠实度落在空间结构与材质而不是脸。
//
// 幂等:同一段提示词重复装配不会叠加(带过 SCENE FIDELITY 就直接返回),
// 所以"资产阶段装配一次、渲染前补齐再装配一次"是安全的。
func SceneImagePrompt(proj *project.Project, plan *project.Plan, sc project.Scene) string {
	prompt := sc.ImagePrompt
	prof := EntityProfileOf(KindScene)
	if prof.SheetMarker != "" && strings.Contains(prompt, prof.SheetMarker) {
		return prompt // 已经是本装配点的产物:不重复叠加
	}
	if prof.RewriteSheetFraming {
		// 剥掉 LLM 从人像模板抄来的取景(plain background / upper-body framing /
		// portrait …),换成"整块地方的空场环境图" —— 与其余非人类型同一套做法。
		prompt = rewriteSheetFraming(prompt, prof.SheetFraming)
	}
	prompt += styleExpand(planStyle(plan), facesOnOf(proj), KindScene)
	return prompt
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

// styleFallbackNotice 兜底画风的告知(空 = 不需要说)。
//
// 语义在 2026-09-24 用户定调后**变了**:平台不再抢风格(上游/技能端指定什么就渲染什么),
// 所以"风格要真人但被平台压成动漫"这件事**已经不会发生** —— 旧函数
// photorealOverrideNotice 仍然在喊"已按动漫化渲染",是**说假话**(用户 2026-09-25 正是
// 看到这条过时告警来问的)。
//
// 现在只剩一种情况值得告知:**方案里没有任何风格设定** —— 此时平台按兜底画风渲染,
// 用户应当知道"这次的画风是平台兜的,不是上游指定的"(否则他下次又会问"风格怎么是这样")。
func styleFallbackNotice(proj *project.Project, plan *project.Plan) string {
	if proj == nil || !proj.Config.Params.Script.FacesOn() {
		return ""
	}
	style := ""
	if plan != nil {
		style = strings.TrimSpace(plan.Style)
	}
	if style != "" || strings.TrimSpace(proj.Config.Params.Script.StyleHint) != "" {
		return "" // 上游给了风格 → 平台照它渲染,没什么要提醒的
	}
	return "本次方案里没有任何风格设定,已按平台兜底画风(二维/2.5 维动画 + 哑光)渲染;" +
		"风格应由上游剧本/技能端在方案里指定(指定后本段不再生效)"
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

// styleExpand 画风块的**无上下文入口**:等价于"这段提示词里还没有技能侧比例句"
// (见 styleExpandFor 的让位判据)。装配点请用 styleExpandFor —— 它能把已装配的提示词
// 带进来,好让平台自己的比例措辞在技能侧比例句同现时让位。
func styleExpand(rawStyle string, facesOn bool, kind EntityKind) string {
	return styleExpandFor("", rawStyle, facesOn, kind)
}

// styleExpandFor 画风块(介质 + 类型忠实度),并按**已有的提示词**决定平台侧比例措辞要不要让位。
//
// existingPrompt 的用途只有一个(技能侧清单 §5.1 "别覆盖它"):卡里已经带技能侧的人体
// 比例锚点句时,平台侧**自己**的比例措辞("cute chibi proportions" / Q 版的二到三头身)
// 不再追加 —— 同一段提示词里两套比例,模型只会取中值,这正是"丑"的来源之一。
// 判据与依据见 proportion_clause.go;平台不删、不改技能侧那一段。
//
// 只让位**比例**,不让位介质与取景:介质(线稿/平涂/哑光)是平台的职责,取景另有
// SheetFraming 那条路 —— 删掉它们会引入新问题(见 artstyle.go 的文件头说明)。
func styleExpandFor(existingPrompt, rawStyle string, facesOn bool, kind EntityKind) string {
	// 图像侧(Krea-2,蒸馏/CFG 1.0)不认否定词,还会把否定词里的名词当指令画出来 ——
	// 统一剥掉再进提示词(见 artstyle.go 的 StripNegations)。
	style := StripNegations(rawStyle)
	prof := EntityProfileOf(kind)
	// 技能侧比例锚点是否已经在场(在场 = 平台自己那套比例措辞让位)。
	skillAnchor := HumanProportionClausePresent(existingPrompt)
	if strings.TrimSpace(style) == "" && !facesOn {
		// 只丢"介质画风块"(SheetLook),**不丢身份忠实度**(SheetIdentity):
		// 后者(按文字画、不得美化/放大眼睛/柔化下颌/变年轻)与画风无关,是防生成模型
		// "美人先验"的硬约束,而且是它全项目**唯一**的注入点。旧实现在这里直接 return
		// 空串,「关掉角色动漫化拍真人剧 + 方案里没有风格串」这一组合下,定妆照提示词
		// 里一条"脸必须与角色卡逐项一致"的约束都没有 —— 定妆照是全片身份的权威,
		// 它一偏,成片与角色卡就永远对不上,而提示词里没有任何痕迹可查。
		// PortraitPrompt 的注释本来就写着分工是"只有 SheetLook 看 FacesOn"。
		return prof.SheetIdentity
	}
	s := strings.ToLower(style)
	has3D := strings.Contains(s, "3d") || strings.Contains(s, "bjd") || strings.Contains(style, "虚拟人") || strings.Contains(style, "虚拟偶像")
	hasAnime := strings.Contains(style, "国漫") || strings.Contains(style, "动漫") || strings.Contains(style, "动画") || strings.Contains(s, "anime") || strings.Contains(s, "donghua")
	hasComic := strings.Contains(style, "美漫") || strings.Contains(s, "comic")
	hasInk := strings.Contains(style, "水墨") || strings.Contains(style, "国画")
	hasReal := strings.Contains(style, "真人") || strings.Contains(style, "写实电影") || strings.Contains(s, "photoreal")
	hasQ := strings.Contains(style, "q版") || strings.Contains(style, "呆萌")
	var terms []string
	stylized := true
	// 上游指定了写实/3D/真人画风时:**风格归上游**,镜头动漫化开关不再覆盖画风
	// (用户 2026-09-26 定调:「资产和视频渲染需要根据技能侧输出的脚本里的风格来定,
	// 如未明确要求风格才走默认风格」)。保留的只有法务底线 —— 任何风格下都不得像真人。
	//
	// 【为什么必须改】真机实测(求你们骂我·第01集):技能端 plan.style =
	// "PHOTOREAL 3D CINEMATIC WORLD, SEMI-STYLISED ANIME-ADJACENT FACES, matte finish",
	// 视频侧按 STYLE AUTHORITY 照它渲染(成片是 3D 写实),而定妆照被这一支覆盖成
	// 二维赛璐璐。参考图与成片**不在同一个视觉世界**里 —— H3 拿一张二维平涂脸去渲染
	// 3D 写实人物,身份只能靠猜,用户长期报的"脸/头发漂移"正是这条链的末端。
	// 视频侧的同一条决定见 styleAndMediumBlock(那里早就只保留法务条款)。
	styleIsPhotoreal := photorealRequested(rawStyle)
	switch {
	case styleIsPhotoreal:
		if kind == KindScene {
			// 场景不能要"人像 + 真实皮肤质感":摄影机对着的是一块地方。
			// 沿用同一条守卫语义(写实/3D 由上游说了算),只把落点从人物表面换成环境材质。
			if has3D {
				terms = append(terms, "next-generation 3D CG environment render, stylized sculpted forms, "+
					"matte dull even finish across every surface")
			} else {
				terms = append(terms, "photorealistic cinematic environment and set, realistic materials and natural light")
			}
		} else if has3D {
			terms = append(terms, "next-generation 3D CG character render, stylized BJD-inspired sculpted forms, "+
				"matte powdery skin shading with a dull even finish, collectible virtual figure look")
		} else {
			terms = append(terms, "photorealistic cinematic portrait, realistic skin texture")
		}
		if hasAnime {
			// 风格自己写了"半写实/动漫相邻的脸",那就是它要的混合介质,照写
			terms = append(terms, "Chinese donghua animation styling, semi-realistic cel-and-CG hybrid shading")
		}
		terms = append(terms, "every design stays ORIGINAL: never make a character resemble a real person, "+
			"actor, singer, celebrity or public figure")
		stylized = false
	case has3D:
		// 注意:**风格块绝不能改写面部特征**。此前这里写着 "large glossy stylized eyes",
		// 与角色卡的面部描述(如"细长单眼皮")直接打架 —— 定妆照被风格"重新设计"成圆软脸
		// 大眼,而视频按文字走,两边永远对不上(2026-09-12 用户报一致性丢失,根因即此)。
		// 风格只描述材质与渲染方式(表面/光泽/媒介),形状类词汇一律不写。
		// 2026-09-24:原措辞 "smooth poreless synthetic skin"(光滑无孔合成皮肤)正是
		// 用户报的"人物油腻"的一半来源 —— 无孔 = 塑料 = 皮脂高光。改成哑光粉质;
		// 同时**去掉皮肤/五官类词汇对非人类型的影响**:器物只有材质,没有皮肤。
		if kind == KindScene {
			terms = append(terms, "next-generation 3D CG environment and set render, stylized sculpted forms, "+
				"matte dull even finish across every surface")
		} else if kind == KindHuman || kind == KindSpiritPet || kind == KindBeast || kind == KindChibi {
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
		if kind == KindScene {
			terms = append(terms, "Chinese donghua (animation) painted background art, anime-influenced stylized environment, clean cel shading")
		} else {
			terms = append(terms, "Chinese donghua (animation) character design, anime-influenced stylized features, clean cel shading")
		}
	case hasComic:
		terms = append(terms, "American comic-book illustration, bold ink outlines, saturated flat colors, halftone dots")
	case hasInk:
		terms = append(terms, "Chinese ink-wash painting style, expressive brush outlines, negative space")
	case hasReal:
		if kind == KindScene {
			terms = append(terms, "photorealistic cinematic environment and set, realistic materials and natural light")
		} else {
			terms = append(terms, "photorealistic cinematic portrait, realistic skin texture") // 真人剧是合法风格(需关掉角色动漫化)
		}
		stylized = false
	default:
		// 无已知风格关键词:不强行改写用户的风格描述,**但类型忠实度与画风块必须留下** ——
		// 那段不是"风格",是"这件东西长什么样"的地基(器物抄结构 / 灵体抄形体)。
		// 旧实现这里直接 return "",连忠实度块一起丢了。
		stylized = false
	}
	if hasQ && !skillAnchor {
		// 平台侧的比例措辞之一:卡里已有技能侧人体比例句时不再追加(两套比例 = 取中值)。
		terms = append(terms, "cute chibi proportions")
	}
	// 忠实度与画风块**按类型取**(人类抄脸 / 器物抄结构 / 灵体抄形体):同一句 human 条款
	// 套在铜雀身上,就是在要求它长一张脸。
	byKind := prof.SheetIdentity
	if facesOn && !styleIsPhotoreal {
		// 动漫化 + 哑光硬约束:与渲染侧的介质块是同一件事的两头,改动要成对。
		// 上游点名写实/3D 时**不加**这一块 —— 那句"赛璐璐平涂"会把刚定下的写实风格
		// 又拽回二维(同一段提示词里两个介质打架,模型只会二选一地糊)。
		byKind += prof.SheetLook
	}
	if !skillAnchor {
		// 平台侧的**比例**措辞(目前只有 Q 版):技能侧比例句在场则让位,介质块不受影响。
		byKind += prof.SheetProportions
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
	// 国风电影级 —— 与技能侧《立项.json》的「渲染风格串」同源(用户 2026-09-26 规则:
	// 「一切创作产物的电影级画面风格必须是国风系」),也是**新建项目的默认**(见
	// project/params.go)。写在预设表第一位不是装饰:平台 LLM 直出会把这串写进 plan.style,
	// 而下游所有参考图与每一镜都照它渲染 —— 它错了,整部剧的画风就错了。
	// 为什么必须与技能侧同源:两条产品线(小说线 / 电影剧本线)的默认画风不一致时,
	// 同一部剧走不同入口会出两种画风(2026-09-27 用户报「平台端 LLM 直出为什么是 2D 漫画」)。
	"guofeng_cinematic": {"国风电影级", "GUOFENG ANIMATED-FEATURE ART DIRECTION, a photoreal 3D surface with IDEALISED, SEMI-STYLISED faces, a muted celadon-grey and ink-wash palette, real materials with visible craft, soft motivated key light, matte finish, natural film grain, shallow depth of field, a Chinese period film, not a western fantasy, not cyberpunk neon, not japanese anime"},
	"urban_xianxia":     {"都市仙侠霓虹", "CHINESE URBAN-XIANXIA, NEON-NOIR WET-STREET LIGHTING, CINEMATIC ANIMATION, GOD RAYS THROUGH SKYSCRAPERS"},
	"guoman_2d":         {"国漫二维动画", "CHINESE 2D ANIME STYLE, CLEAN LINEWORK, VIBRANT CEL SHADING, DONGHUA QUALITY"},
	"live_action":       {"真人实拍质感", "PHOTOREALISTIC LIVE-ACTION CINEMATOGRAPHY, 35MM FILM, SHALLOW DEPTH OF FIELD, NATURAL SKIN TEXTURE"},
	"pixar_3d":          {"3D 皮克斯", "PIXAR-STYLE 3D RENDER, SOFT GLOBAL ILLUMINATION, EXPRESSIVE STYLIZED CHARACTERS, SUBSURFACE SCATTERING"},
	"ink_wash":          {"水墨国风", "CHINESE INK WASH PAINTING STYLE, BRUSH TEXTURE, NEGATIVE SPACE, MISTY MOUNTAINS, TRADITIONAL AESTHETIC"},
	"cyberpunk":         {"赛博朋克", "CYBERPUNK, RAIN-SOAKED STREETS, HOLOGRAPHIC ADS, CHROME AND NEON, BLADE RUNNER MOOD"},
	"ghibli":            {"吉卜力手绘", "GHIBLI-INSPIRED HAND-PAINTED, WARM WATERCOLOR BACKGROUNDS, SOFT WIND AND LIGHT, WHIMSICAL SERENITY"},
	"film_noir":         {"黑色电影", "FILM NOIR, HIGH CONTRAST BLACK AND WHITE WITH ONE ACCENT COLOR, VENETIAN BLIND SHADOWS, SMOKEY"},
	"hongkong":          {"港片胶片", "HONG KONG CINEMA 1990s, SATURATED NEON, TEAL-ORANGE GRADE, ANAMORPHIC FLARES"},
	"claymation":        {"黏土定格", "CLAYMATION STOP-MOTION STYLE, FINGERPRINT TEXTURE, TACTILE HANDMADE CHARM"},
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
