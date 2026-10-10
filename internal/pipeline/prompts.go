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
				en:    appearanceExcerpt(ch.ImagePrompt),
				voice: strings.TrimSpace(ch.Voice), kind: ResolveEntityKind(ch),
			})
		}
	}
	for _, sc := range plan.Scenes {
		if sc.ID == shot.Scene && strings.TrimSpace(sc.Description) != "" {
			scenes = append(scenes, anchorChar{
				name: sc.Name, desc: strings.TrimSpace(sc.Description),
				en: appearanceExcerpt(sc.ImagePrompt),
			})
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
		return styleAndMediumBlock(plan, facesOn, shot.H3Prompt) + proportion + shot.H3Prompt
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
			fmt.Fprintf(&sb, "%d. %s[%s]:%s\n", i+1, c.name, EntityKindLabel(c.kind), anchorDescOf(c))
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
			fmt.Fprintf(&sb, "%d. %s:%s\n", i+1, sc.name, anchorDescOf(sc))
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
	sb.WriteString(styleAndMediumBlock(plan, facesOn, shot.H3Prompt))
	// 比例锚点放在**最后**(紧贴 shot.H3Prompt)是有意的:它是"别让比例跑掉"的约束而不是
	// 剧情内容,而它要防的正是紧随其后的画面 —— 动作一多,模型优先保动作、把形体当装饰丢掉
	// (与【外貌不变项】记的那次漂移同一机理),所以取离画面最近的注意力位置。
	sb.WriteString(proportion)
	return sb.String() + "\n" + shot.H3Prompt
}

// anchorChar 身份锚定里的一条出场对象(角色带类型与声线,场景只用名字+描述)。
type anchorChar struct {
	name, desc string
	// en 该对象的**英文外观串**（卡里出定妆照用的那一份，见 appearanceExcerpt）。
	// 锚定块**内容**优先用它：进 H3 的是正向提示词，而本仓口径写明
	// 「正向提示词必须全英文，中文只进元数据」（docs/平台侧生成规范-角色场景道具分镜 §2）。
	// 空串 = 这张卡没有英文提示词（老包/手工卡）⇒ 回落中文 desc 并如实留在提示词里。
	en    string
	voice string
	kind  EntityKind
}

// anchorDescOf 锚定块里"这一条长什么样"的正文：**英文优先，中文兜底**。
//
// 【2026-09-29 真机实测】原来一律用中文 `description`，而模型会把汉字**画到画面上**：
// 第 01 集第 1 镜的金属铭牌上清清楚楚写着「钢印铭牌」四个字，而它们正是这段中文里的两个词
// （「机型**钢印** U-80001」＋「腰侧挂一串金属**铭牌**记名链」）被当成"要画的字"照搬。
// 平台自己的口径就是"正向提示词全英文"（同上 §2 的那条 ⛔ 反向执行），这里属于漏网的一处。
//
// 兜底保留中文是有意的：宁可留汉字，也不要让这一镜**没有身份信息** ——
// 英文缺失只会出现在老包/手工卡上，那种卡本来也没有可用的英文外观串。
func anchorDescOf(c anchorChar) string {
	if s := strings.TrimSpace(c.en); s != "" {
		return s
	}
	return normalizeUniformWording(c.desc)
}

// appearanceExcerpt 从卡片的英文提示词里取**外观描述那一段**（装配块之前的部分）。
//
// 为什么不能整串照搬：卡里那份提示词是**给定妆照**用的，末尾带着
// `RENDER STYLE / IDENTITY FIDELITY / FRAMING (mandatory): a front-facing chest-up portrait…`
// 这类**取景与画风**条款 —— 把它原样塞进镜头提示词，等于让这一镜也去拍"正脸半身像"
// （画面与景别直接打架）。所以只取平台标记之前的外观正文；标记之后的条款由本函数所在的
// 锚定块自己按类型下发。
//
// 找不到标记时返回整串（卡是纯外观串的场合，例如技能侧只写了外貌）。
func appearanceExcerpt(prompt string) string {
	s := strings.TrimSpace(StripAnchorStubTag(prompt))
	if s == "" {
		return ""
	}
	cut := len(s)
	for _, m := range []string{
		"RENDER STYLE (mandatory)", "IDENTITY FIDELITY", "CAST DISTINCTNESS",
		"FRAMING (mandatory)", "HAIR EXACTLY AS BRIEFED", "FACE ID (final check",
		"OBJECT FIDELITY", "FORMLESS-FORM FIDELITY", "SCENE FIDELITY",
		"ARTEFACT CLAUSE", "Pose: calm and simple",
	} {
		if i := strings.Index(s, m); i >= 0 && i < cut {
			cut = i
		}
	}
	out := strings.TrimSpace(strings.TrimRight(s[:cut], " ,;.\n"))
	if out == "" {
		return "" // 标记在开头（只有装配块、没有外观正文）：当作没有英文
	}
	return out
}

// hasHumanLikeChar 本镜是否有"有头发/有服装"类型的主体(人类 / Q 版)。
//
// 用于把"发型与发长、服装层数"这类**人形外貌**条款只发给该发的镜头:器物/妖兽/
// 灵宠/无实体镜头里出现这些词,等于把主体往人形上拽。
//
// **人形机械不在这一档**(2026-09-29):它虽然是人形,但"服装层数(夹克+背心+内搭)"这句
// 对它是有害的 —— 一台机体不该被套上古装层次;它的结构由 Android 那套 AnchorLock/AntiForm
// 负责(锁装甲分缝/关节/发光标记/材质)。发型那条走 PortraitHairClause(定妆照侧)。
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
//
// **incoming（本镜提示词原文）只用来判一件事：让位**。技能侧产出的提示词**自带**风格句
// （官方格式要求它写在 `[Shot 1]` 之前），此时平台再插一段风格块 = 一段提示词两套风格，
// 模型只会取中值。所以判据是"提示词里已经有风格句了吗"（`project.HasStyleSentence`，
// 与导入侧提 `plan.Style` 用**同一个**判据），有就不写风格那两行、只留法务底线。
// 这与比例锚点的让位是同一条设计（见 styleExpandFor 与 proportion_clause.go）：
// **不改上游文本，只让自己那段不重复**。
func styleAndMediumBlock(plan *project.Plan, facesOn bool, incoming string) string {
	var sb strings.Builder
	style := ""
	if plan != nil {
		style = strings.TrimSpace(plan.Style)
	}
	if style != "" && !project.HasStyleSentence(incoming) {
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
		if project.HasStyleSentence(incoming) {
			// 让位：风格句已经在提示词里（技能端写的），这里只留法务底线；
			// "照上面那行风格渲染"这句在这里会指向空气（上面没有那行），所以不写。
			sb.WriteString("【原创性底线】STYLE AUTHORITY (the style line is already stated inside the shot text — keep " +
				"it identical in every shot of this series): every design stays ORIGINAL: never make a character " +
				"resemble a real person, actor, singer, celebrity or public figure.\n")
		} else {
			sb.WriteString("【风格执行】以上风格由剧集设定指定,照此渲染,不要另换画风;" +
				"全片风格必须逐镜一致(同一部剧不得出现两种画风)。\n" +
				"STYLE AUTHORITY (from the series spec, do not substitute): render exactly in the style line above and keep " +
				"it identical in every shot of this series. Every design stays ORIGINAL: never make a character resemble a " +
				"real person, actor, singer, celebrity or public figure.\n")
		}
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

// EnsureVoiceoverClause 画外音 / **画面外说话人**镜次的"嘴唇闭合"兜底(幂等)。
//
// 依据:技能端产出的台词里 61% 是画外音(旁白/内心/系统音),而 v1 的提示词把它们
// 与画内对白写得一模一样 —— H3 只看见一句 <d>[Chinese]…</d>,看不见"谁不许开口",
// 于是让画面里的角色张嘴把旁白念了出来(用户报的「系统旁白,主角动嘴」)。
//
// **2026-09-29 补第二种情形**:说话的人**有角色卡但这一镜没让他出场**(用户报的
// 「镜头外的说话声音不应该镜头内人动嘴唇」)。那种镜 `HasVoiceover` 是 false,
// 于是既不写闭嘴句、也没有门 —— 模型照样让画面里的人对口型。判据见 project.OffScreenSpeakers。
//
// 措辞照抄 internal/director 的官方式写法("...while the on-screen characters' lips
// remain completely closed"),两侧同一句:技能端产出时写、平台这里兜底补,幂等可重复调用。
// 判据是 project.VoiceoverClauseMarker,改文案必须保留那个串。
func EnsureVoiceoverClause(plan *project.Plan, sh *project.Shot) bool {
	if sh == nil {
		return false
	}
	voiceover := project.HasVoiceover(*sh)
	offScreen := len(project.OffScreenSpeakers(plan, *sh)) > 0
	if !voiceover && !offScreen {
		return false
	}
	if strings.Contains(sh.H3Prompt, project.VoiceoverClauseMarker) {
		return false
	}
	clause := ""
	if voiceover {
		clause = "VOICEOVER (mandatory): every line flagged as voice-over in this shot is spoken off-screen — " +
			"the on-screen characters' lips remain completely closed while that voice is heard; " +
			"no on-screen character mouths those words, and the voice stays off-frame and disembodied."
	}
	if offScreen {
		// 说话人这一镜不在画面里：画面内的人一律不许动嘴（嘴型只跟着**画面里真的在说的人**走）。
		//
		// 【2026-10-09 真机咬出来的】这条子句原来写「keep their lips completely closed」——
		// 而幂等判据找的是 `VoiceoverClauseMarker`（「lips remain completely closed」），
		// 于是**这条子句本身不幂等**：装配点每调一次就再追加一遍（真机探针第二遍又追加了）。
		// 现在两种子句都逐字含 marker，判据才拦得住重复追加。
		if clause != "" {
			clause += "\n"
		}
		clause += "OFF-SCREEN SPEAKER (mandatory): the character who speaks these lines is NOT in this shot — " +
			"the people on screen keep their lips at rest and stay silent: their lips remain completely closed " +
			"while that voice is heard; nobody on screen mouths those words, and the speaking voice stays off-frame."
	}
	sh.H3Prompt = strings.TrimRight(sh.H3Prompt, "\n") + "\n\n" + clause
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
	EnsureVoiceoverClause(plan, shot) // 画外音 / 画面外说话人补"嘴唇闭合"(幂等,在装配点做一次)
	// **场级光照锚**（2026-09-30 用户：「尼玛，场景卡给谁，让谁去做？」）：
	// 场景卡与镜头提示词都是平台自己产出的，这一条从头到尾归平台侧。
	// 实测那一集采样帧最暗占比 47%/52%/77%（有一镜判黑屏）—— 各镜各写曝光，接在一起就跳。
	// 这里把场景卡的 `light_state`（日/夜 + 主光方向）写进**每一镜**，同一场戏共用一套光态；
	// 夜戏另加一句"画面内必须有可见光源"。
	if shot != nil {
		if clause := sceneLightingClause(plan, shot); clause != "" &&
			!strings.Contains(shot.H3Prompt, "SCENE LIGHTING (scene-level") {
			shot.H3Prompt = strings.TrimRight(shot.H3Prompt, "\n") + clause
		}
	}
	// casting 清单只列**本镜真的会开口的人**（见 ShotSpeakers 的说明）：以前这里传的是
	// `plan.Characters`（全剧角色），于是每个镜头都背着一份 6 人的音色表 —— 含一个 10 岁
	// 顽童的童声。没有台词的镜头(本集 54 镜里 17 镜)照样收到"这些人要用这些嗓子说话"，
	// 模型就把音频填成人声嘈杂，用户听到的是"全是小孩子乱吵"。
	//
	// 【2026-10-07】ShotCasting = ShotSpeakers + **无卡说话人的平台默认音色**（真机 shot 1
	// 里唯一说话的「舱内AI」不是角色卡 ⇒ 那份 casting 块整段缺席，一个正在说话的人没有任何
	// 音色约束）；紧接着 BindLineVoices 把每句台词的引述**逐字绑**成权威声线（卡片 voice /
	// 平台默认）—— 模型自己改写的那一版会被换掉（H3 拿这段文字当音频条件，措辞一变音色就变）。
	promptOut := EnsureVoiceInline(EnsureOnScreenTextRule(EnsureChineseDialogue(anchorPrompt(plan, shot, facesOnOf(proj)))), plan, ShotCasting(plan, shot))
	// 逐句把引述里的声线绑成**权威文本**（卡片 voice / 平台默认）；绑了几处如实计数，
	// 由渲染阶段播报一次（见 stages_render.go 的 VoiceRebound）。
	promptOut, nBound, nUnresolved := BindLineVoices(promptOut, plan, shot)
	if plan != nil {
		plan.VoiceRebound += nBound
		plan.VoiceUnresolved += nUnresolved
	}
	// 语音纪律条款（2026-10-07，用户「配音存在乱配音」）：把「只说 <d> 里那几句、其余时间没有人声」
	// 写成硬约束。判据来自 ASR 实测：写这一条之前，13 镜里 3 镜把同一句念了 2~3 遍、
	// 3 镜凭空多出剧本里没有的话（详见 internal/asr/compare.go 的文件头）。
	//
	// 【判据源收口（2026-10-10 深度排查，用户「配音老是有问题」）】下面两条条款**都按提示词正文判**
	// （正文才是模型读到的东西）；结构字段（`shot.dialogue`）只用来**对账** —— 在这里记一次账，
	// 由渲染收尾处如实播报。对不上就说明上游数据坏了：台词在正文里丢了，或模型自己加了词。
	// 对账口径：正文里的 `<d>` 真句数 vs 方案里登记的真句数（占位/示例标签不算，见 promptSpokenTexts）。
	// 记下**是哪几镜**（光一个计数，用户不知道该去核对哪一镜）。
	if plan != nil && shot != nil {
		if got, want := len(promptSpokenTexts(promptOut)), len(project.PlanSpokenTexts(shot)); got != want {
			plan.SpeechTextMismatch++
			plan.SpeechTextMismatchShots = append(plan.SpeechTextMismatchShots, shot.ID)
		}
	}
	promptOut = EnsureSpokenLinesClause(promptOut, shot)
	// 逐**小镜头**的人声位置（2026-10-08 真机实锤：模型把"每个小镜头都要说点什么"当成规则，
	// 把台词整体前移一格、还在空出来的那格里自编了一句假中文 —— 见 EnsureSpeechPlacementClause 的文件头）。
	promptOut = EnsureSpeechPlacementClause(promptOut, shot)
	return promptOut
}

// ShotSpeakers 本镜**真的会开口的人**:有台词的说话人(台词非空)+ 有旁白/画外音时的旁白者。
//
// 【为什么必须按镜头过滤 casting 清单】(2026-09-27 真机实证,用户报「渲染的分镜里全是
// 小孩子乱吵的配音」)装配点原先把 `plan.Characters` 整个传给 EnsureVoiceInline,于是
// **每一个镜头**的提示词尾部都挂着一份全剧音色表:
//
//	VOICE CASTING (authoritative - use exactly these timbres…):
//	- 周廷 speaks with Deep heavy male voice…
//	- 顽童 speaks with High cheeky child's voice, around 10, jeering
//	…
//
// 而本集 54 个镜头里 17 个既没有台词也没有旁白 —— 那些镜头拿到的是一份"谁该用什么嗓子"
// 的授权清单,却没有一句可念的话,官方句式又写着"use exactly these timbres",模型只能
// 自己造人声;清单里恰好有一个童声,街市镜的音景又写着 crowd murmur/jeers,合起来就是
// 满耳的童声嘈杂。**没有台词的镜头,一份 casting 都不该有。**
//
// 判据只看"本镜台词里出现过的说话人名字":认不出说话人(家丁/路人)时不进清单 ——
// 宁可不给音色,也不要塞错人的嗓子。
func ShotSpeakers(plan *project.Plan, shot *project.Shot) []project.Character {
	if plan == nil || shot == nil {
		return nil
	}
	speak := map[string]bool{}
	for _, l := range shot.Dialogue {
		name := strings.TrimSpace(l.Speaker)
		if name == "" || strings.TrimSpace(l.Text) == "" {
			continue
		}
		speak[name] = true
	}
	if len(speak) == 0 {
		// 没有台词的镜头:只有旁白时才需要音色(旁白音色由 EnsureVoiceInline 自己给),
		// 角色一个都不列 —— 这正是本次修复的核心。
		return nil
	}
	var out []project.Character
	for _, c := range plan.Characters {
		if speak[strings.TrimSpace(c.Name)] {
			out = append(out, c)
		}
	}
	return out
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

// spokenLinesMarker 幂等判据（改了文案必须保留这个串，否则会重复叠加）。
const spokenLinesMarker = "SPOKEN LINES ONLY (MANDATORY)"

// EnsureSpokenLinesClause 「只说这些台词，其余时间没有人声」的硬约束（幂等）。
//
// 【为什么需要它】（2026-10-07，用户「配音存在乱配音」；判据来自 ASR 实测）
// H3 是音画同生模型，一次要输出整整几秒的音轨；而计划里的台词往往只占 2~4 秒。此前
// 提示词只写了「谁用什么声线说哪句话」，**没有一句话管"其余时间怎么办"** —— 于是模型在
// 剩下的秒数里自己填：真机第01集 13 镜里
//
//	镜 4「已读取。无对应档位。」被念了两遍、镜 6 两遍、镜 7「自检窗口，七十二标准时。」三遍；
//	镜 3 凭空多出一整串「跟强我的对数还修戴眼影一下,也续点倾向」而第一句「80000。」根本没念；
//	镜 1 多出「没禁条」、镜 11 多出「图墙白头圣呢﹔裙尾便宜着人呢」。
//
// 这条约束把三件事一次说清：**只念 <d> 里的、每句只念一遍、其余时间不许有人声**。
// 没有台词的镜头另给一句（那些镜头本来最容易被填成人声嘈杂 —— 见 ShotSpeakers 的文件头：
// 「本集 54 个镜头里 17 个既没有台词也没有旁白，却都背着一份音色授权清单」）。
//
// 【它是否有效，只能由重渲 + ASR 复验来说】（诚实边界）本函数保证的只是这句话**在**提示词里、
// 且不多不少只出现一次；它能不能管住模型，要重渲一镜（每镜十几分钟 GPU）再用 ASR 听一遍才知道。
// 【判据源（2026-10-10 深度排查，用户「配音老是有问题」）】**只看提示词正文**里的 `<d>` 标签，
// 不看 `shot.dialogue` —— 同一个文件里 `cutSpeechMap` 早就写下这条口径（「判据完全来自正文…
// 以**真正发给模型的文本**为准」），而本函数原先用结构字段判，于是两处口径相反时真机出现
// 「正文三句台词 + 整镜静音条款」的自相矛盾提示词（归档第01集镜 3 实测）。
// 结构字段与正文的对账改由唯一装配点 `ShotRenderPrompt` 记账（`plan.SpeechTextMismatch`）。
func EnsureSpokenLinesClause(prompt string, shot *project.Shot) string {
	if shot == nil || strings.Contains(prompt, spokenLinesMarker) {
		return prompt
	}
	var body string
	if lines := promptSpokenTexts(prompt); len(lines) > 0 {
		body = "the ONLY human speech in this shot is the line or lines enclosed in <d>…</d> tags. Say each of them " +
			"exactly once, verbatim, in the order written, inside the time window it is placed in. Do NOT repeat, " +
			"re-read, paraphrase, extend, answer or improvise any line, and do NOT add any speech that is not " +
			"inside a <d> tag — no extra dialogue, no commentary, no counting, no readings of signs, numbers or " +
			"panel text. For every remaining moment of this shot there is NO human voice at all: keep the audio to " +
			"the ambient soundscape, room tone and physical foley of the scene. A silent stretch is correct — " +
			"filling it with speech is a defect." + "\n" +
			// 【2026-10-08 真机】"每句只念一遍"这条写在上面了，但真机里出现的是**另一种**重复：
			// 同一句先念糊一遍、再念一遍（镜 5「它是千燃百开﹔一张画有不超过万语」+ 正确那句；
			// 镜 1 把「十一月底」连说两遍）—— 那是"起手没起好就重来"的**假起手**，
			// 与"重复念"是两种毛病。所以单独写死一条：念砸了**接着往下念**，绝不回头重念。
			"No false starts and no re-reading: do not stutter, do not restart a line, and never say a word or " +
			"phrase twice in a row. If the articulation slips, keep going from where you are — starting the line " +
			"over is a defect, and a short hesitation is not." + "\n" +
			"(中文对照：本镜**唯一**的人声就是 <d>…</d> 里的那几句台词 —— 逐字、按顺序、**每句只念一遍**，" +
			"不许重复、不许改写、不许续说、不许自行应答，也不许加入任何不在 <d> 标签里的话（包括念招牌、" +
			"念编号、报数、自言自语）；其余时间**没有任何人声**，只有场景的环境声与动作音。留白是对的，" +
			"拿话把留白填满是缺陷。)**起手也别重来**：不许磕巴、不许把同一句重新起头、不许同一个词连着说" +
			"两遍 —— 念得不利索就**接着往下念**，回头重念算缺陷（短暂停顿不算）。)"
	} else {
		body = "this shot has NO spoken dialogue at all — there is no <d> tag and no narration in it. Do NOT invent " +
			"any human speech, muttering, counting or crowd chatter; the human voice track stays silent for the whole " +
			"shot. Only the ambient soundscape, room tone and physical foley are audible." + "\n" +
			"(中文对照：本镜**没有**任何台词 —— 不许自己造人声、不许嘟囔、不许报数、不许人群嘈杂；" +
			"整镜的人声轨是安静的，只有环境声与动作音。)"
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + spokenLinesMarker + ":\n" + body
}

// speechPlacementMarker 幂等判据（改文案必须保留这个串，否则会重复叠加）。
const speechPlacementMarker = "SPEECH PLACEMENT (MANDATORY)"

// reCutMark 定位提示词里的每一刀（`[Shot N] …` 开头那一行）。
// 时间点用**原文里那一串**（`At 00:02.500`）而不是自己格式化 —— 逐字一致，模型对得上。
var (
	reCutMark    = regexp.MustCompile(`(?im)^[ \t]*\[shot\s+\d+\]([^\n]*)`)
	reCutAtMark  = regexp.MustCompile(`(?i)\bat\s+\d+:\d+(?:\.\d+)?`)
	reSpeechLang = regexp.MustCompile(`^\s*\[\s*[A-Za-z]+\s*\]\s*`)
)

// cutSpeech 一刀里有没有人声、念的是什么（从提示词正文里读，不依赖结构字段）。
type cutSpeech struct {
	At    string   // 「At 00:02.500」；这一刀没写时间点就是空串
	Lines []string // 这一刀里的 <d> 台词（已剥语言标签）
}

// cutSpeechMap 把提示词切成"每一刀 + 那一刀里的台词"。
//
// 判据完全来自正文：`[Shot N]` 分刀，`<d>…</d>` 是台词的唯一通道（H3 官方口径）。
// 结构字段 `shot.cuts` 只用来对账（见 project/cuts.go），这里以**真正发给模型的文本**为准 ——
// 两处不一致时该改的是它们，不是这条条款。
//
// **占位/示例标签不算台词**（2026-10-08 真机实锤，见 project.IsPlaceholderSpoken）：
// 装配点自己追加的条款正文里也写着 `<d>…</d>` 当例子（`enclosed in <d>…</d> tags`、
// `就是 <d>…</d> 里的那几句台词`、`matching the [Chinese] tag inside each <d>…</d>`），
// 它们全都落在**最后一刀**那一段里 ⇒ 逐刀清单写成「这一刀 4 行，每行念一遍」而真实只有 1 行
// ⇒ 模型把空出来的格子填满：镜 1「又是那个人?」念了两遍、镜 5 凭空多出一句「或是血」（ASR 实锤）。
func cutSpeechMap(prompt string) []cutSpeech {
	ms := reCutMark.FindAllStringSubmatchIndex(prompt, -1)
	out := make([]cutSpeech, 0, len(ms))
	for i, m := range ms {
		end := len(prompt)
		if i+1 < len(ms) {
			end = ms[i+1][0]
		}
		seg := prompt[m[0]:end]
		cs := cutSpeech{At: reCutAtMark.FindString(prompt[m[2]:m[3]])}
		for _, d := range reDialogueTag.FindAllStringSubmatch(seg, -1) {
			txt := strings.TrimSpace(reSpeechLang.ReplaceAllString(d[1], ""))
			if txt == "" || project.IsPlaceholderSpoken(txt) {
				continue
			}
			cs.Lines = append(cs.Lines, txt)
		}
		out = append(out, cs)
	}
	return out
}

// promptSpokenTexts 提示词**正文**里的台词（`<d>…</d>` 里的内容，已剥语言标签、已排除占位/示例）。
//
// 【为什么要有它（2026-10-10）】人声条款的判据只能有一个来源 —— **真正发给模型的那段文字**。
// 原先 `EnsureSpokenLinesClause` 用结构字段（`PlanSpokenTexts`）、`cutSpeechMap` 用正文，
// 两个来源一旦相反（真机：方案 `dialogue[].text` 空串、正文有三句 `<d>`），提示词里就同时出现
// 「三句台词」与「整镜没有人声」两句话 ⇒ 音轨不可控。这里把"从正文取台词"收成一个助手，两处共用。
//
// 与 `cutSpeechMap` 的区别：这个**不依赖 `[Shot N]` 分刀**（整镜一刀的提示词也要能数出台词数）。
func promptSpokenTexts(prompt string) []string {
	out := make([]string, 0, 4)
	for _, d := range reDialogueTag.FindAllStringSubmatch(prompt, -1) {
		txt := strings.TrimSpace(reSpeechLang.ReplaceAllString(d[1], ""))
		if txt == "" || project.IsPlaceholderSpoken(txt) {
			continue
		}
		out = append(out, txt)
	}
	return out
}

// EnsureSpeechPlacementClause 逐**小镜头**写明人声出现在哪一段（幂等）。
//
// 【为什么还要一条（2026-10-08 真机第01集镜头 1，ASR 实锤）】`EnsureSpokenLinesClause` 只说"整镜"：
// 唯一的人声是 <d> 里那几句、其余时间没有人声。但真机这一镜是 3 个小镜头，计划是
// 「[Shot 1] 无台词 / [Shot 2] At 00:02.500「硬件钢印 U-80001。」/ [Shot 3] At 00:05.000「该编号不存在，予以静默。」」，
// 实际音轨（ASR medium，从头转写，9.42 s）是**三段**：
//
//	[0.00-2.60] 硬件鋼印U-C缠Hi-001   ← 第 1 句被**提前**讲进了没有台词的开场小镜头，编号还念糊了
//	[2.84-4.72] 黑石逆一幅航旋機     ← 凭空编的假中文（语种自动检测 zh 概率 0.99）
//	[5.36-7.84] 该编号不存在，予以静默。← 第 2 句正常
//
// 也就是说：模型把"每个小镜头里都得说点什么"当成了规则，于是把台词整体前移了一格、并在空出来的
// 那一格里自己编了一句。当时提示词里**已经有** SPOKEN LINES ONLY 与 MANDATORY 语种条款 ——
// 它们管的是"整镜范围"，没有逐段点名。这条把每一段点出来：哪几段有人声、念哪一句、哪几段必须安静，
// 全是计划里的已知量（分刀 + <d> 落在哪一刀）。真机渲染提示词（取自主机 ComfyUI `/history` 实际提交的
// 工作流文本）里也已经绑好了两句的权威声线，问题不在声线，在**位置**。
//
// 【它是否有效，只能由重渲 + ASR 复验来说】（诚实边界）本函数保证的只是这段逐段清单**在**提示词里、
// 且只出现一次；管不管得住模型要重渲一镜再用 ASR 听（每镜约 9~11 分钟 GPU）。
//
// 【安全阀（2026-10-08 追加；2026-10-10 改成"只记账、不再丢条款"）】这条清单等于在向模型**许愿**
// 「这一刀要念这几句」—— 数错了比不加更糟：真机就是这么错的（清单写「这一刀 4 行」，真实只有 1 行
// ⇒ 模型把空出来的格子填满）。所以清单里的真句数会与 `project.PlanSpokenTexts(shot)` 对账。
//
// 但"对不上就不加清单"是**把安全阀装反了**（2026-10-10 实测）：对不上恰恰说明数据坏掉，
// 那几镜正是最需要"台词只在这一刀念"这条约束的（归档第01集 17 镜里 7 镜对不上，全部丢了清单，
// 而同镜的"整镜静音"条款又因为同一个坏数据被写上 ⇒ 提示词自相矛盾）。
// 现在：**清单一律按正文写**（`cutSpeechMap` 读的就是模型要读的那段文字），对账交给唯一装配点
// `ShotRenderPrompt` 记账（`plan.SpeechTextMismatch`），由渲染收尾处如实播报。
func EnsureSpeechPlacementClause(prompt string, shot *project.Shot) string {
	if shot == nil || strings.Contains(prompt, speechPlacementMarker) {
		return prompt
	}
	cuts := cutSpeechMap(prompt)
	if len(cuts) < 2 {
		return prompt // 只有一刀 = 整镜一个时间窗，SPOKEN LINES ONLY 已经覆盖，不重复堆话
	}
	speaking := 0
	var b strings.Builder
	for _, c := range cuts {
		if len(c.Lines) > 0 {
			speaking++
		}
	}
	fmt.Fprintf(&b, "%s:\nthis shot is cut into %d pieces; the human voice is heard in exactly %d of them "+
		"and in no other piece:\n", speechPlacementMarker, len(cuts), speaking)
	for _, c := range cuts {
		where := "the opening of the shot"
		if c.At != "" {
			where = "the cut at " + c.At
		}
		if len(c.Lines) == 0 {
			fmt.Fprintf(&b, "- %s — NO speech of any kind: room tone, hum and foley only. "+
				"No voice, no muttering, no counting, no numbers, no reading of any panel or sign.\n", where)
			continue
		}
		fmt.Fprintf(&b, "- %s — %d line(s), each spoken exactly once inside THIS piece: ", where, len(c.Lines))
		for i, l := range c.Lines {
			if i > 0 {
				b.WriteString(" / ")
			}
			fmt.Fprintf(&b, "「%s」", l)
		}
		b.WriteString("\n")
	}
	b.WriteString("Never move a line into another piece, never begin a line before its own piece starts, " +
		"and keep every piece marked NO speech completely free of human voice.\n" +
		"(中文对照：本镜切成上面这些小镜头，人声**只**出现在其中 " + fmt.Sprint(speaking) + " 个里 —— " +
		"标着 NO speech 的那几段**一点人声都没有**（只有环境声、机器声与动作音，不许嘟囔、不许报数、" +
		"不许念任何面板/招牌上的字）；每段里那句台词只在这一段里念一遍，不许把台词挪到别的小镜头、" +
		"更不许在它自己那一段开始之前就开念。)")
	return strings.TrimRight(prompt, "\n") + "\n\n" + b.String()
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
	Kind EntityKind // 空 = 按 name/description/imagePrompt 推断
	Name string
	// ID 卡片 id（2026-09-28 审计 B9）。
	//
	// 加它是因为下面两处"同剧其它角色"的块（`castContrastBlock` / `castFaceIDs`）
	// 此前**只按名字**排除"自己" —— 于是两个恰好同名但确实是不同人的角色会互相被排除，
	// 对比块与 FACE ID 段里都少了对方的设定（撞脸自检同样漏检，见 cast_distinct.go）。
	// id 才是"同一个人"的稳定真源；名字只在 id 缺失时兜底。
	ID          string
	Description string
	ImagePrompt string
	// FacesOn 是否按动画介质渲染(项目的 script.stylized_faces)。
	// 资产库那条路恒为 true:库是跨项目的全局资产,而全应用默认就是动漫化角色
	// —— 库里生成真人脸,复用到任一项目都会与渲染侧打架(见 LibraryPortraitPrompt)。
	FacesOn bool
	// FrameW/FrameH 出图画幅(项目的 render.width/height)。
	//
	// 为什么要传下来(2026-09-27 用户报障「定妆照应该是上半身正面照片,咋头都顶上看不全」):
	// 画幅决定**构图** —— 同样是"上半身",竖幅(9:16)里装得下的是胸像,横幅里装得下的是
	// 头肩。卡里只写一句写死的 `upper-body framing` 时,竖幅里模型只能二选一:上下留白
	// 或者放大到裁掉头顶 —— 实测它选后者。所以取景按画幅算(见 PortraitFramingClause),
	// 而不是写死。0 值走默认画幅(与 project.DefaultStageParams 的 render 尺寸一致)。
	FrameW, FrameH int
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
	// 性别声明在下面**进 prompt 正文**（描述本身只走 uniformCivilianClarify，塞进去到不了正文）
	prof := EntityProfileOf(kind)
	// 图像侧的两道预处理(见 artstyle.go 的说明):
	//   ① 剥掉微雕文字/极小铭文 —— 在半身幅里它们只会糊成灰渍(实测定妆照胸口与腕部的污渍);
	//   ② 姿态纪律 —— 半身幅里复杂手势会变成连指手套(实测模型自作主张画了抱胸)。
	// （清汉字见下方幂等判据**之后** —— 顺序不能反，理由写在那里。）
	prompt := StripAnchorStubTag(StripMicroTextForImage(normalizeUniformWording(s.ImagePrompt)))
	// 定妆照取景与本片镜头的景别对齐(见 sheet_framing.go):**半身**。
	// 卡里若同时写着取景 `upper-body framing` 与全身比例 `seven and a half heads`,模型会选
	// "按比例画全身"(2026-09-26 实测出图就是全身立绘),而这张图要喂给近景/中景/特写
	// (64 镜里 61 镜)——全身像被要求"裁"出特写,头肩比必然漂。**只剥全身口径**,脸部比例全留。
	if kind == KindHuman {
		prompt = StripFullBodyProportions(prompt)
	}
	if prof.SheetMarker != "" && strings.Contains(prompt, prof.SheetMarker) {
		// 已经是本类型的装配结果:不重复叠加。
		// **但 2026-09-29 新加的开头取景声明要补上**：这一步是后加的，存量成品（库里
		// 已有的定妆照提示词）里没有它 —— 而"不带它的成品"实测出的是全身立绘。
		// 幂等判据是**前缀**：已经是当前画幅那一句就原样返回。
		leadW, leadH := s.FrameW, s.FrameH
		if leadW <= 0 || leadH <= 0 {
			leadW, leadH = DefaultSheetFrameW, DefaultSheetFrameH
		}
		if lead := PortraitLeadFraming(kind, leadW, leadH); lead != "" {
			body := StripPortraitLeadFraming(prompt)
			// 【2026-10-10 用户「定妆照提示词瘦身」】存量成品也要**同一件收口**：
			// 库里已入库的提示词是"卡 + 装配块"的成品，里面可能同时躺着卡自己带来的取景句
			// （真机 `char:char_shenyan` 有两份）与旧措辞的收尾纪律 —— 全是多余口径。
			// 不在这里收，用户点「重新生成」走的就是短路，三套口径**原样保留**。
			body = TightenSheetFraming(body, kind, leadW, leadH)
			if strings.HasPrefix(body, lead) {
				return body
			}
			return project.DropHanRunes(lead) + body
		}
		return prompt
	}
	// 【2026-10-10 用户「定妆照提示词瘦身：去重复取景块」】卡自己带来的**平台取景声明句**
	// 在人像三档（人 / Q 版 / 人形机械）里一律先剥掉 —— 三种形态最终都由平台自己写取景
	// （人/Q 版：尾部 PortraitFramingClause；人形机械：prof.SheetFraming），卡里那份只会稀释。
	//
	// 必须排在 `rewriteSheetFraming` **之前**：人形机械/场景那几档的 `prof.SheetFraming` 里
	// 也可能带"背景/构图"这类词，剥在后面会把平台刚写进去的那句一起吃掉。
	//
	// 真机依据（`TestZZProbeSheetSlim`，读盘上 `asset_map.json` 的成品卡）：
	//   · 人类成品卡 = 平台的开头声明 + 收尾纪律两句（装配前后逐字节相同，已经是一处收口的形态）；
	//   · **Q 版卡**是被人像那套装配出来的（标记是 IDENTITY FIDELITY），而重新生成走的是
	//     CHIBI FIDELITY 那条分支 ⇒ 卡里那份人像收尾纪律会与新的 Q 版取景纪律**并存**
	//     （探针实测：6664 字 → 9554 字，`FRAMING (mandatory)` ×2）—— 就是这里收掉的重复。
	if kind == KindHuman || kind == KindChibi || kind == KindAndroid {
		prompt = StripPlatformFramingSentences(prompt)
	}
	if prof.RewriteSheetFraming {
		prompt = rewriteSheetFraming(prompt, prof.SheetFraming)
	} else if kind == KindHuman || kind == KindChibi {
		// 人像:**也剥**卡里的人像取景短语,但换成"按画幅算"的构图纪律
		// (PortraitFramingClause 在函数尾部追加)。2026-09-27 用户报「定妆照应该是
		// 上半身正面照片,咋现在头都顶上看不全」——根因就是卡里写死的 `upper-body
		// framing` 在 9:16 竖幅里装不下整个头。剥掉它,取景只剩一个来源。
		//
		// 【顺序】**整句**剥离必须排在**短语**剥离之前：`stripSheetFramingWords` 会把一句里的
		// `portrait` 单词摘掉（`The picture is a CHEST-UP : …`），整句判据从此认不出它 ——
		// 而它明明就是同一句取景声明。整句剥离在上面已经做过了（不分人像哪一档）。
		prompt = stripSheetFramingWords(prompt)
	}
	style := ""
	if plan != nil {
		style = plan.Style
	}
	// 画风块由 styleExpandFor 注入,并把**已装配的提示词**带进去:卡里若已带技能侧的人体
	// 比例锚点句,平台自己的比例措辞("cute chibi proportions" / Q 版二到三头身)让位
	// —— 同一段里两套比例,模型只会取中值(技能侧清单 §5.1,见 proportion_clause.go)。
	//
	// 构图纪律按**成片画幅**算(竖幅 → 胸像 + 头顶留白;横幅 → 头肩):卡里那句写死的
	// `upper-body framing` 在竖幅里会把头顶裁掉 —— 见 PortraitFramingClause 的说明。
	frameW, frameH := s.FrameW, s.FrameH
	if frameW <= 0 || frameH <= 0 {
		frameW, frameH = DefaultSheetFrameW, DefaultSheetFrameH
	}
	// 忠实度与画风块由 styleExpand 按类型与开关注入(prof.SheetIdentity / SheetLook,
	// 后者只在 FacesOn 时加)—— 这里**不再单独追加**,否则真人剧项目(开关关)会被
	// 塞进动画介质块,而那是最难查的一类"两边都对、结果不对"。
	//
	// 【2026-09-28 用户报「一张定妆照还出现相同角色多人物」「有莫名的文字」—— 元凶在此】
	// 这些**平台追加的块全都带中英对照**（"(中文对照:……)"）。中文是写给维护者看的，
	// 而图片模型会把它当成"要画的字"**整段照抄到画面上**：实测那张定妆照左右两侧与底部
	// 全是乱码中文段落，连"中文对照:"四个字都清晰可见；成片的文字块又把构图冲乱，
	// 于是同一个角色被画成了两个人。
	//
	// 所以：追加块**统一清汉字后再拼**（`project.StripCJK`），**卡原文 `prompt` 不动** ——
	// 存量包与技能侧导入的数据里，卡原文本身可能带中文的结构描述，清素材会连有用信息一起删
	// （`artstyle_test.go`「原始 image_prompt 必须保留」与 `entity_test.go`「器物结构描述
	// 不得丢失」三条断言就是挡这件事的；第一版我把 StripCJK 加在整个 prompt 末尾，被它们挡回来了）。
	var added strings.Builder
	added.WriteString(styleExpandFor(prompt, style, s.FacesOn, kind))
	// **性别声明**（2026-09-29 用户：「钟离渲染成男的？」）：卡面/锚表里能判出性别时必须写进
	// **提示词正文** —— 不写时模型自己猜（名字偏男性先验，守钟女就被画成了男的）。
	// 判据与预检门同源：`project.GenderOf`（显式字段 `性别：X` 优先，其次强性别词）。
	// 注意：不能塞进 s.Description —— 描述只经 uniformCivilianClarify 走，到不了正文。
	if g, ok := project.GenderOf(s.Description, s.ImagePrompt); ok {
		added.WriteString(project.GenderClause(g))
	}
	// 【2026-10-10 用户报「每个角色的定妆照肩膀上都有三个 Q 型人物」—— 这两行就是元凶】
	//
	// 以前这里往**图像提示词**里塞了两处**别人的外形描述**：
	//   ① `castContrastBlock(...)`：`CAST DISTINCTNESS — other cast members of this series: <6 条别人的长相>`；
	//   ② `PortraitFaceIDClause(..., castFaceIDs(...))` 的 `NOT: <别人的长相> ×N`。
	// 真机那张 6827 字的卡里这两处都在（含 `(A palm-sized miniature of a lean young man …)` 与
	// `(A palm-sized flat matte-black shadow creature …)` —— 两条"巴掌大"的角色）⇒ 出图就多出几个
	// 小尺寸人物蹲在肩上（2026-09-12 已经咬过一次同型：「郑伯的定妆照里出现了小尺寸的卫东与 Q 版江砚」）。
	//
	// 那次加的是**否定句**（`Do NOT depict any of these other characters`）—— 而平台的图像侧规则写得很清楚：
	// **CFG=1.0 的蒸馏档否定词不生效，还会把名词当指令画出来**（见 `artstyle.go` 顶部与
	// `entity_prompts.go` 文件头）。所以"列出来 + 说不许画"这条路在图像侧**注定只能部分有效**，
	// 一旦名单里有"巴掌大的小人"，它就会被当画面元素。
	//
	// 定调（本轮）：**图像提示词里不出现任何"别人的外形"**。防撞脸交给两件确定性的事：
	//   ① 主体**自己**的身份锁逐字重述（下面的 FACE ID，只讲自己）；
	//   ② 出图后**看图判身份**（`cast_distinct.go`：与同剧已生成角色逐张比，判撞脸就带
	//      `CastSeparationClause` 换种子重出一次）—— 它判的是**像素**，不依赖图像模型的否定词能力。
	added.WriteString(uniformCivilianClarify(s.Description))
	added.WriteString(PortraitHairClause(kind))
	added.WriteString(PortraitPoseClause(kind))
	added.WriteString(PortraitFramingClause(kind, frameW, frameH))
	if kind == KindHuman || kind == KindChibi {
		added.WriteString(PortraitFaceIDClause(identityLockExcerpt(s.ImagePrompt)))
	}
	prompt += project.DropHanRunes(added.String())
	// **取景要开头也声明一次**（2026-09-29 用户报「定妆照需要上半身正脸」）：
	// 末尾那句压不住卡原文里大段的**全身**描述（长腿 / 大腿装甲 / 腰侧挂链 / 靴子），
	// 实测同一张卡只加末尾那句仍出全身立绘，把同一句放到**开头**才收住 ——
	// 对照实验与理由见 PortraitLeadFraming。
	if lead := PortraitLeadFraming(kind, frameW, frameH); lead != "" {
		prompt = project.DropHanRunes(lead) + prompt
	}
	return prompt
}

// isSelfCard "这一行是不是**我自己**"的统一判据（审计 B9）。
//
// 口径与 `project.SameCharacter` 一致（**id 优先**、两边 id 都说得出就必须相等），
// 差别只在这里是"逐行判自己"而不是"判两人同族"：档名关系（`沈砚` / `沈砚·少年`）
// 在这里**不算自己** —— 对比块要的正是"同一个人另一档长什么样"，
// 把它排掉等于让变体档的提示词里没有主卡的对照（那正是"同一角色几张脸"的温床）。
//
// 所以：两边 id 都有 → 比 id；有一边为空 → 退回名字精确相等；否则 → 不是自己。
func isSelfCard(selfID, selfName, candID, candName string) bool {
	a, b := normalizeCardID(selfID), normalizeCardID(candID)
	if a != "" && b != "" {
		return a == b
	}
	return strings.TrimSpace(selfName) != "" &&
		strings.TrimSpace(selfName) == strings.TrimSpace(candName)
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
	case KindHuman, KindAndroid, KindChibi:
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

// PortraitPromptForProject 项目内角色卡的便捷包装(取项目开关、方案风格与**出图画幅**)。
func PortraitPromptForProject(proj *project.Project, plan *project.Plan, ch project.Character) string {
	facesOn := true
	frameW, frameH := 0, 0
	if proj != nil {
		facesOn = proj.Config.Params.Script.FacesOn()
		// 画幅决定构图(胸像 vs 头肩),必须在装配时就拿到 —— 定妆照是按成片画幅出的
		// (甲2),而"上半身"在竖幅里装不下整个头(2026-09-27 用户报障)。
		frameW, frameH = proj.Config.Params.Render.Width, proj.Config.Params.Render.Height
	}
	return PortraitPrompt(PortraitSubject{
		Kind: ResolveEntityKind(ch), Name: ch.Name,
		Description: ch.Description, ImagePrompt: ch.ImagePrompt, FacesOn: facesOn,
		FrameW: frameW, FrameH: frameH,
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
// BoardImagePrompt 角色**三视图细节板**的装配点（数据在 board.go，装配在这里）。
//
// 为什么它是**第三个**合法装配点：板子由已通过的定妆照派生，风格块必须原样继承
// （所以不能重新走 PortraitPrompt 那一套 —— 那会按实体类型重算取景与风格），
// 但**取景必须换**：定妆照的尾巴是"半身胸像、整个头在画面内"，与"一张含 3 个全身 +
// 6 个头 + 6 个细节的多视图板"直接打架，模型只会二选一地糊。
// 于是这里只做一件事：把取景重写成板子的取景，其余（身份、风格、材质）逐字保留。
//
// 幂等：已含 BoardMarker 原样返回（装配点可能被调用两次）。
func BoardImagePrompt(masterPrompt string) string {
	if strings.Contains(masterPrompt, BoardMarker) {
		return masterPrompt
	}
	// **板子进模型前清汉字**（2026-09-28 用户报「不要出现莫名的文字，咋还是有」）：
	// masterPrompt 是**已装配的定妆照提示词**，而画风/介质块自带中英对照 —— 中文是写给人看的，
	// 对图片模型却只是"要画的字"。板上那块乱码段落与局部图上的中文小标签就是这么来的：
	// 板子的输入跟着中文一起进了提示词，被照抄成画面上的文字。
	//
	// 只清**输入**（masterPrompt）与拼装结果，`BoardLayoutClause` / `BoardFraming` 本来就是英文；
	// 这一刀不碰 `PortraitPrompt`（那里是卡原文，见它的注释：清素材会连有用描述一起删）。
	//
	// **开头的定妆取景声明要整句剥掉**（2026-09-29）：定妆照提示词现在以
	// "BUST PORTRAIT (…cropped at mid-chest…)" 开头，而板子要的是"3 个全身 + 6 个头"——
	// 留着它就是同一段里两套取景打架（`sheetFramingWords` 只剥词，剥完会剩一堆碎片）。
	return project.DropHanRunes(BoardMarker + " — " + BoardLayoutClause + "\n\n" +
		rewriteSheetFraming(project.DropHanRunes(StripPortraitLeadFraming(masterPrompt)), BoardFraming))
}

func SceneImagePrompt(proj *project.Project, plan *project.Plan, sc project.Scene) string {
	prof := EntityProfileOf(KindScene)
	// 幂等判据按**卡片原文**判：本函数可能被调用两次（资产阶段装配一次、渲染前补齐再一次），
	// 而装配出来的成品会被写回卡片 —— 第二次进来时它已经是成品，必须原样返回，
	// 否则下面的清洗与追加会把它再改一遍（"重复装配改变结果"正是这里要避免的）。
	if prof.SheetMarker != "" && strings.Contains(sc.ImagePrompt, prof.SheetMarker) {
		return sc.ImagePrompt
	}

	// 汉字卫生（2026-09-27 技能侧整改单 §1.4 / 平台侧整改清单 §6.2）：场景板是 T2I 出的，
	// 汉字几乎必然变成乱码字形糊在环境板上 —— 而这块板会被 H3 带进**用到该场景的每一镜**。
	// 契约门只能"提示"，存量包、人工改过的卡与绕过门的路径都还在，所以真正保证渲染输入
	// 干净的只能是装配点（coordinator 不从别处取原值，见 coordinator.go 的注释）。
	// **只清卡片自己的文本**：下面追加的风格/介质/忠实度块本来就有中英对照，不该被动。
	prompt := project.StripCJK(sc.ImagePrompt)

	// 场景板是**空场**:卡片里若写了人群/行人(实测《求你们骂我》写着
	// "a scattered crowd of villagers"),先整项删掉 —— 平台随后追加的取景纪律
	// ("completely deserted and unpeopled")与它本来是打架的,模型只会折中,而场景板
	// 上的人会被 H3 当成"这个地方本来就有的陌生人"带进每一镜。
	prompt = StripSceneCrowd(prompt)
	if prof.RewriteSheetFraming {
		// 剥掉 LLM 从人像模板抄来的取景(plain background / upper-body framing /
		// portrait …),换成"整块地方的空场环境图" —— 与其余非人类型同一套做法。
		prompt = rewriteSheetFraming(prompt, prof.SheetFraming)
	}
	prompt += styleExpand(planStyle(plan), facesOnOf(proj), KindScene)
	// **建立镜头串由装配层保证**（2026-09-28 用户：「场景简陋单一」＋ 实测 4/4 张场景卡都没有它）。
	//
	// 官方 §4.4 要求场景板必须含这一串 —— 平台的规则文字里写了，但实测**模型不照做**（分镜镜头的
	// 同一串它倒是照做：那次只有 0/4 张卡带）。按本仓的既定做法：能确定性做到的就不指望 LLM 自觉，
	// 在**唯一装配点**补上（幂等：已经有了就不重复加；成品会被写回卡片，契约门随后自然通过）。
	if !containsAny(prompt, project.PlanEstablishingExtra) {
		prompt += "\n" + project.PlanEstablishingExtra
	}

	// 事件隔离（整改单 §1.3）：场景板只画**事件发生前的地点固有状态**。
	// 板上若带着血渍/尸体/刚破碎物/遗落道具，那一集每一镜都像"事情已经发生过"
	// （与"板上画了人 → 每镜都出现同一个陌生人"同一条机理）。LLM 侧已在 sysSceneRules
	// 要求写这一串，这里兜底：**装配点是唯一出口**，所以渲染输入里必然有它（幂等：已有就不叠）。
	if !strings.Contains(prompt, project.PlanScenePreEventClause) {
		prompt += " " + project.PlanScenePreEventClause + "."
	}
	return prompt
}

// PropImagePrompt 道具图的**唯一装配点**（2026-09-27 补：道具此前被读进来却从没出过图）。
//
// 缺口是这么发现的：技能侧交付了三类卡（角色/场景/道具），平台契约也把 `props` 收下来了
// （`project.Prop` 的注释写着"字段收下来才算真读到了"），库里有 `props` 类目、上传也支持
// "道具图"、`assetImageSize` 的注释甚至写着"定妆照／场景图／**道具图**" —— 但资产阶段的
// `check()` 只遍历了角色与场景，**道具一次都没生成过**。于是"道具卡"这一整类产出只躺在
// 计划里给人看，渲染侧完全没用上。
//
// 【2026-10-06 审计：这里原先把"道具不给图"说成**官方规则**，是不准确的】
// 官方 `ref-en.txt` 明写 `<Subject N>` 含 props/objects —— 官方**允许**道具作参考主体。
// 平台选择不给道具图，理由是平台自己的：① 参考图槽位上限 9 张，超了 `capRefsToLimit`
// **从尾部裁**，而尾部正是场景图，再塞道具会让"裁掉谁"不可预测；② 道具出图是 1:1 白底单件的
// 商品图纪律，当参考图有"把白底物件糊进画面"的风险（系统镜的全息面板是**明确例外**，
// 见 refs.go 的 SystemAssets）。
// 所以这是**平台自设约束**，不是官方口径 —— 道具的跨镜一致靠正文里的 PROP ANCHORS 文字锚定
// （`EnsurePropAnchors`），而它整条挂在 `prop_ids` 上（空引用由 `FillMissingPropRefs` 补齐）。
//
// 幂等：与场景/角色同口径 —— 已含本类型的 SheetMarker 就原样返回（本函数会被调用两次：
// 资产阶段一次、渲染前补齐一次，装配成品会写回卡片）。
func PropImagePrompt(proj *project.Project, plan *project.Plan, pr project.Prop) string {
	prof := EntityProfileOf(KindObject)
	if prof.SheetMarker != "" && strings.Contains(pr.ImagePrompt, prof.SheetMarker) {
		return pr.ImagePrompt
	}
	// 汉字卫生：与场景板同一条机理（T2I 会把汉字画成乱码字形）。
	prompt := project.StripCJK(pr.ImagePrompt)
	// 兜底：卡片没写或写歪时，用中文描述/英文外观短语拼一句最小可用的 ——
	// 道具卡缺 image_prompt 的存量包里很常见（老包只有 en）。
	if strings.TrimSpace(prompt) == "" {
		fallback := strings.TrimSpace(pr.EN)
		if fallback == "" {
			fallback = project.StripCJK(pr.Description)
		}
		if fallback != "" {
			prompt = fallback + " — a single object, catalogued."
		}
	}
	if prof.RewriteSheetFraming {
		// 剥掉从人像模板抄来的取景（upper-body framing / plain background / portrait …），
		// 换成"单件器物、白底、正对镜头"—— 与其余非人类型同一套做法。
		prompt = rewriteSheetFraming(prompt, prof.SheetFraming)
	}
	prompt += styleExpand(planStyle(plan), facesOnOf(proj), KindObject)
	// 白底单件与"不画字"两条是道具图的可比性前提：底不干净就跨镜对不上，
	// 画了字（名牌/标签）就成了乱码字形 —— 两条都写死在这里，不靠上游记得写。
	if !strings.Contains(prompt, propPlateClause) {
		prompt += " " + propPlateClause
	}
	return prompt
}

// propPlateClause 道具图的画面纪律（1:1 白底单件 + 不画字 + 八项信息的可画化）。见 PropImagePrompt。
//
// 2026-09-27 按技能侧《平台侧生成规范》§5.1 的骨架补齐三样规范明确要求、而这里原先没写的东西：
// ① **≥4 种顶级材质混搭 + 至少一处非功能性装饰**（"极繁＋静态展示"是道具的质感来源）；
// ② **尺寸参照**（规范八项里有"尺寸"，不写模型不知道它多大 —— 大印与书签会长成一样）；
// ③ **面板类道具内容留白**（系统 UI 那块全息面板绝不许画字，与场景板的汉字卫生同一条机理）。
const propPlateClause = "Single object only, centred, shot 1:1 square from a straight-on angle, on a plain pure white " +
	"seamless background: no scenery, no people, no hands, no cast shadow, no text, no letters, no numerals, no watermark, " +
	"no logo, no label of any kind. Keep the object's material, colour and finish exactly as described. " +
	"Build it as an ornate, statically displayed artefact: mix at least four premium materials with visible craft " +
	"(engraving, inlay, filigree or high relief), and include at least one purely decorative, non-functional feature " +
	"(asymmetric guard, tassel, fringe, beast-head finial, pierced openwork). State its SIZE against a human reference " +
	"(a handspan wide / palm-sized / forearm long) so the scale is unambiguous. If this object is a screen-like panel " +
	"(system interface, holographic display), keep its interior completely BLANK: never letter any text, digits, icons " +
	"or UI glyphs into it."

// sheetFramingWords LLM 写在 image_prompt 尾部的人像取景短语。
//
// 为什么要逐个剥掉而不是"整句替换":这些短语散落在语句中(且大小写/连字符写法不一),
// 剥干净再追加类型取景,才能保证"器物不会同时被要求半身正面"。
var sheetFramingWords = []string{
	"plain background", "plain seamless background", "plain pastel background", "dark background",
	// 平台自己写的定妆取景尾巴（2026-09-28 起是 "a pure white seamless backdrop"）也要能被剥：
	// 板子装配时若剥不掉它，就会与 BoardFraming 的背景要求叠成两套。
	"pure white seamless backdrop",
	"upper-body framing", "upper body framing", "upper-body", "upper body",
	"front-facing", "front facing",
	"full body", "full-body", "head and shoulders", "portrait",
}

// stripSheetFramingWords 把取景短语剥干净(保留段落结构:只压行内空白)。
//
// 2026-09-27 起**人类也用它**:卡里那句写死的 `upper-body framing` 在竖幅(9:16)里
// 与实际构图不一致(半身装不下整个头 → 模型裁头顶),取景改由 PortraitFramingClause
// 按画幅算。同一条纪律:一段提示词里取景只有一个来源。
func stripSheetFramingWords(prompt string) string {
	out := prompt
	for _, w := range sheetFramingWords {
		// 大小写不敏感地整词剥离(replaceAllFold 见下)
		out = replaceAllFold(out, w, "")
	}
	// 剥掉碎片后会留下 ", ," / " ," 这类空档(实测
	// "…STYLE, Plain Background, upper-body framing, Front-Facing" → "…STYLE, , ")
	return foldProportionLeftovers(out)
}

// rewriteSheetFraming 剥掉人像取景短语,追加该类型自己的取景(非人类型与人类两条路共用)。
func rewriteSheetFraming(prompt, framing string) string {
	out := strings.Trim(stripSheetFramingWords(prompt), " ,;")
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
// settingBookForProject 找这个项目对应的**设定集**（用户口径：`data/小说/<作品>/设定集/`）。
//
// 怎么定位：项目 id 形如「书名·第01集·标题」（见 `project.SeriesBookName`），书名就是前缀；
// 而项目目录是 `data/projects/<id>`，设定集在 `data/小说/<书名>/设定集/` ——
// 从项目目录反推出 `data` 根即可，不依赖任何配置项（配置项会漂，目录结构是既有约定）。
//
// **找不到就返回 nil，不是错误**：老包、纯原创、技能侧还没产出的中间态都没有设定集，
// 那时照旧走"只喂正文"的老路（`LoadSettingBook` 自己也是这个纪律）。
func (c *Coordinator) settingBookForProject(projID string) *project.SettingBook {
	dir := c.store.Dir(projID) // <data>/projects/<id>
	root := filepath.Dir(filepath.Dir(dir))
	book := project.SeriesBookName(projID)
	if book == "" {
		book = projID // 不是系列分集（单集项目 / 手工建的）：按项目名当书名试一次
	}
	sb, err := project.LoadSettingBook(filepath.Join(root, "小说", book, "设定集"))
	if err != nil {
		return nil
	}
	return sb
}

func (c *Coordinator) bookRoster(projectID string) (string, int) {
	epDir := c.store.Dir(projectID)
	series := filepath.Dir(epDir)
	if filepath.Base(series) == "projects" {
		return "", 0
	}
	type charInfo struct{ key, name, desc string }
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
				ID          string `json:"id"`
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
			// **去重键按 id 优先**（2026-09-28 审计 B5）。
			//
			// 原来直接 `seenC[名字]` —— 同名不同人时**第二个人整个被丢掉**：
			// 它的描述根本不进名单，于是本集里那个同名的角色拿不到自己的设定，
			// 只能被模型按第一个人的形象写（"锚到别人身上"）。
			// 有卡片 id 就用 id 当键：同名不同人是两个条目，本来就该各列一条。
			key := cardSeedKey(ch.ID, n)
			if n == "" || seenC[key] {
				continue
			}
			seenC[key] = true
			if len(chars) < 8 {
				d := strings.TrimSpace(ch.Description)
				if r := []rune(d); len(r) > 110 {
					d = string(r[:110]) + "…"
				}
				chars = append(chars, charInfo{key, n, d})
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
	// 同名计数：给下面的输出用（同名不同人要显式提示模型"这是两个人"）。
	sameNameCount := map[string]int{}
	for _, ch := range chars {
		sameNameCount[ch.name]++
	}
	var sb strings.Builder
	sb.WriteString("【系列角色一致性·必须遵守】以下角色已在前几集登场并定妆,本集若出现这些角色:" +
		"①必须严格沿用原名(不得增减字/换同音字/另起别名);" +
		"②**同一个人可能以不同称呼出现**(名字与代号/编号并存,如 机关人 / 阿枢 / 一三七四一号)——只要判定是同一人,就必须共用同一张角色卡的同一个名字与同一份形象设定,不得因为换了称呼就新建一个角色、另起一份设定;" +
		"③角色表描述与每镜 H3 提示词中的形象必须与下面给出的设定逐项一致(毛色/服饰/年龄/特征细节不得改动):\n")
	for i, ch := range chars {
		// 同名不同人时**必须点名"这是两个人"**：名单里出现两条同名记录，
		// 模型第一反应是"同一个人的两种写法"从而合并 —— 那正好是我们丢掉第二个人设定的原因。
		if sameNameCount[ch.name] > 1 {
			fmt.Fprintf(&sb, "%d. %s【注意：本剧有 %d 个同名但**不同**的角色，本条是其中一张卡，"+
				"不要把这几条当成同一个人的多种写法】:%s\n", i+1, ch.name, sameNameCount[ch.name], ch.desc)
			continue
		}
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
		// **官方 per-shot 前缀里与画风无关的那几句**（`model-selection.md` STEP 7 的 H3 prompt prefix：
		// "strong character design language, clean motion, on-brand color palette"）——
		// 官方那段是给皮克斯风 3D 的（含 "Pixar-inspired / Q-version proportions"，不能照抄到国漫项目），
		// 但**这几句与画风无关**：设计语言要强、动态要干净、配色要同品牌 —— 任何画风都成立，
		// 且正好治"画面平、没有辨识度"这类观感问题。
		return prof.SheetIdentity + "\n" + officialPrefixEssentials
	}
	s := strings.ToLower(style)
	// has3D 只认"**明确要手办 / 虚拟人 / 收藏人偶**"的写法。
	//
	// 【2026-10-08 第三次修正：**"3D CG" 也不能认**（用户：「角色渲染的很丑，你看一下资产库」）】
	// 前两次修的都是"别把 3d 字面读成手办"，但 `has3D` 里仍留着 `3d render` / `3d cg` 两条 ——
	// 于是**本项目**的风格「半写实绘画感的东方星海国风**3D CG**」照样命中，平台往提示词里塞了
	// 「stylized BJD-inspired sculpted forms, **matte powdery skin shading with a dull even finish**,
	// **collectible virtual figure look**」⇒ 出图就是**手办质感**。
	// 真机实证（平台自己的 VLM 读那三张定妆照的原话）：
	//   姜晚舟「皮肤蜡感明显，像次表面散射塑料」「整张脸像塑料壳」；
	//   小灯「3D 半写实娃娃，偏 BJD 塑料感；皮肤蜡感」；曹锦「皮肤过度平滑匀净，像美颜修图」。
	// 三张图"最丑的三点"全部指向这一段 —— **就是平台自己写死的词**。
	// "3D CG / 3D render" 是**媒介**（用什么渲染），不是**材质**（表面长什么样）；
	// 材质该由风格的写实度决定（半写实 ⇒ 有皮肤纹理的半写实表面）。两者拆开：
	//   has3D   = 明确要手办/虚拟人 ⇒ 才给 BJD 那一套；
	//   medium3D = 说了 3D CG ⇒ 只加"3D CG 渲染"这个媒介词，材质另说（见下面 styleIsPhotoreal 支）。
	has3D := strings.Contains(s, "bjd") || strings.Contains(style, "虚拟人") || strings.Contains(style, "虚拟偶像") ||
		strings.Contains(s, "virtual figure") || strings.Contains(s, "collectible figure")
	medium3D := strings.Contains(s, "3d render") || strings.Contains(s, "3d cg")
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
			switch {
			case has3D:
				// 明确要手办/虚拟人 ⇒ 那套"收藏人偶"的材质词是它自己要的
				terms = append(terms, "next-generation 3D CG environment render, stylized sculpted forms, "+
					"matte dull even finish across every surface")
			case medium3D:
				terms = append(terms,
					"next-generation 3D CG environment render, semi-realistic surfaces with real materials "+
						"and natural light")
			default:
				terms = append(terms, "photorealistic cinematic environment and set, realistic materials and natural light")
			}
		} else if kind == KindObject {
			// 非人形的「物」（道具/机关/器物参考图）走**物件那一套介质词**。
			//
			// 【真机证据，2026-10-10】盘上 `prop:prop_key`（一把铜钥匙）的 image_prompt 里写着
			// "next-generation 3D CG **character render, realistic skin shading** with visible skin
			// texture and natural facial highlights" —— 给一把钥匙要求"皮肤着色 / 面部高光"，
			// 只会把静物往"有人味"方向拉，与它自己的「白底单件静物」口径打架。
			//
			// 【这条分工借自 chatfire-AI/huobao-drama】他们把提示词按**资产种类**拆成三套口径
			// （`skills/prompt-generator/{character,scene,prop}-prompt/SKILL.md`），各写各的介质与取景；
			// 我们此前只分了"场景 vs 其它"，道具被算进了"其它"= 人像那一套。
			// 场景支见上面（environment render），人物支见下面（character render）。
			switch {
			case has3D:
				terms = append(terms, "next-generation 3D CG prop render, stylized sculpted forms, "+
					"matte dull even finish across every surface")
			case medium3D:
				terms = append(terms, "next-generation 3D CG prop render, semi-realistic surfaces with "+
					"real materials and natural light")
			default:
				terms = append(terms, "photorealistic product render of a single object, realistic "+
					"materials and natural light")
			}
		} else if has3D {
			// 明确要手办/虚拟人：保留"收藏人偶"材质（那是这类风格的定义）
			terms = append(terms, "next-generation 3D CG character render, stylized BJD-inspired sculpted forms, "+
				"matte powdery skin shading with a dull even finish, collectible virtual figure look")
		} else if medium3D {
			// **只说了 3D CG**（本项目就是这种："半写实绘画感的东方星海国风 3D CG"）：
			// 给**媒介**词（3D CG 渲染）+ **半写实表面**（有皮肤纹理、哑光到缎面），
			// 不给 BJD / collectible / matte powdery / dull even finish ——
			// 那四组词合起来就是"手办塑料感"，是这次用户说"丑"的直接来源。
			// 【2026-10-08 真机 A/B 又咬出一次】原来这里写的是 "semi-realistic **sculpted surfaces**" ——
			// "sculpted"（雕塑）本身就是手办那一族的词根，真机 A/B 出来的新图仍被判
			// 「皮肤蜡感磨皮、看不到毛孔」「头大肩窄似立绘」。改成"真人皮肤该有的响应"：
			// 真实皮肤着色 + 可见皮肤纹理 + 自然面部高光（全是正面词，CFG=1.0 下负面词不参与引导）。
			terms = append(terms,
				"next-generation 3D CG character render, realistic skin shading with visible skin texture and "+
					"natural facial highlights, real materials with visible craft, a matte-to-satin finish")
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
	case medium3D:
		// 说了 3D CG 但**没说手办/虚拟人**（也不属于写实那一支 —— 例如"3D CG 卡通"）：
		// 同一条原则（媒介 ≠ 材质）：只给**媒介**词，材质按"半写实的真实材质"给，
		// 而且**不许留 "sculpted" 这个手办词根**（人类那支给"真人皮肤该有的响应"，
		// 场景给真实材质与自然光）。
		if kind == KindScene {
			terms = append(terms, "next-generation 3D CG environment and set render, "+
				"semi-realistic surfaces with real materials and natural light")
		} else if kind == KindHuman || kind == KindSpiritPet || kind == KindBeast || kind == KindChibi {
			terms = append(terms, "next-generation 3D CG character render, "+
				"realistic skin shading with visible skin texture and natural facial highlights, "+
				"real materials with visible craft")
		} else {
			terms = append(terms, "next-generation 3D CG render, semi-realistic forms with real materials and visible craft")
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

// backfillAssets 渲染前自动补齐缺失的参考图:资产阶段未跑/未生成全时,
// 就地用本地 Krea-2 生成,绝不静默降级纯文生(2026-09-08 逻辑重构:
// 「定妆照没生成就渲染」被用户点名的反直觉行为根治)。
// 资产生成只依赖本地 ComfyUI,与本次渲染引擎选择无关(云端渲染也本地补图)。
// StylePresets 渲染风格预设：key → (显示名, 英文风格提示, **介质族**)。
//
// Medium 只给「**串里明确写了介质**」的预设填，其余留空 = 不参与互斥判定（题材/调子类如
// `cyberpunk`/`urban_xianxia`/`film_noir`/`hongkong` 没写介质，硬猜一个族反而会误判）。
// 判定用途：一个画面不可能同时是实拍、二维动画与水墨 —— 见 StyleMediumConflicts。
var StylePresets = map[string]struct{ Name, Prompt, Medium string }{
	// 国风电影级 —— 与技能侧《立项.json》的「渲染风格串」同源(用户 2026-09-26 规则:
	// 「一切创作产物的电影级画面风格必须是国风系」),也是**新建项目的默认**(见
	// project/params.go)。写在预设表第一位不是装饰:平台 LLM 直出会把这串写进 plan.style,
	// 而下游所有参考图与每一镜都照它渲染 —— 它错了,整部剧的画风就错了。
	// 为什么必须与技能侧同源:两条产品线(小说线 / 电影剧本线)的默认画风不一致时,
	// 同一部剧走不同入口会出两种画风(2026-09-27 用户报「平台端 LLM 直出为什么是 2D 漫画」)。
	"guofeng_cinematic": {"国风电影级", "GUOFENG ANIMATED-FEATURE ART DIRECTION, a photoreal 3D surface with IDEALISED, SEMI-STYLISED faces, a muted celadon-grey and ink-wash palette, real materials with visible craft, soft motivated key light, matte finish, natural film grain, shallow depth of field, a Chinese period film, not a western fantasy, not cyberpunk neon, not japanese anime", "3d"},
	"urban_xianxia":     {"都市仙侠霓虹", "CHINESE URBAN-XIANXIA, NEON-NOIR WET-STREET LIGHTING, CINEMATIC ANIMATION, GOD RAYS THROUGH SKYSCRAPERS", ""},
	"guoman_2d":         {"国漫二维动画", "CHINESE 2D ANIME STYLE, CLEAN LINEWORK, VIBRANT CEL SHADING, DONGHUA QUALITY", "2d"},
	"live_action":       {"真人实拍质感", "PHOTOREALISTIC LIVE-ACTION CINEMATOGRAPHY, 35MM FILM, SHALLOW DEPTH OF FIELD, NATURAL SKIN TEXTURE", "live"},
	"pixar_3d":          {"3D 皮克斯", "PIXAR-STYLE 3D RENDER, SOFT GLOBAL ILLUMINATION, EXPRESSIVE STYLIZED CHARACTERS, SUBSURFACE SCATTERING", "3d"},
	"ink_wash":          {"水墨国风", "CHINESE INK WASH PAINTING STYLE, BRUSH TEXTURE, NEGATIVE SPACE, MISTY MOUNTAINS, TRADITIONAL AESTHETIC", "painted"},
	"cyberpunk":         {"赛博朋克", "CYBERPUNK, RAIN-SOAKED STREETS, HOLOGRAPHIC ADS, CHROME AND NEON, BLADE RUNNER MOOD", ""},
	"ghibli":            {"吉卜力手绘", "GHIBLI-INSPIRED HAND-PAINTED, WARM WATERCOLOR BACKGROUNDS, SOFT WIND AND LIGHT, WHIMSICAL SERENITY", "painted"},
	"film_noir":         {"黑色电影", "FILM NOIR, HIGH CONTRAST BLACK AND WHITE WITH ONE ACCENT COLOR, VENETIAN BLIND SHADOWS, SMOKEY", ""},
	"hongkong":          {"港片胶片", "HONG KONG CINEMA 1990s, SATURATED NEON, TEAL-ORANGE GRADE, ANAMORPHIC FLARES", ""},
	"claymation":        {"黏土定格", "CLAYMATION STOP-MOTION STYLE, FINGERPRINT TEXTURE, TACTILE HANDMADE CHARM", "stopmotion"},
}

// composeStyleHint 风格预设（可组合）拼接 + 用户自由文本。
//
// 【2026-10-09 用户「H3 不是可以多风格组合吗？」→ 查官方原文后改】预设之间用**逗号**并列，
// 而不是 ` + `：官方 `h3-prompt-writing/references/base-en.txt` §4.1 给的就是这种写法 ——
// 「At the beginning of `[Shot 1]`, state the overall style and initial composition. Common styles
// include `Cinematic`, `live-action`, `2D-animated`, `3D CG`…」，示例 `[Shot 1] Live-action,
// cinematic, a medium-wide shot frames...`；而官方唯一用 ` + ` 组合的是**任务类型**
// （`ref-en.txt` §「combine the task types with ` + `」），不是风格。既然这条串最终会进
// 「全局风格:」行并成为 plan.Style（= 提示词开头的风格句），就照官方的风格句写法来。
// 同时**去重**：同一个预设勾两次不该在串里出现两遍。
func composeStyleHint(styles []string, freeHint string) string {
	var parts []string
	seen := map[string]bool{}
	for _, k := range styles {
		if seen[k] {
			continue
		}
		seen[k] = true
		if p, ok := StylePresets[k]; ok {
			parts = append(parts, p.Prompt)
		}
	}
	out := strings.Join(parts, ", ")
	if strings.TrimSpace(freeHint) != "" {
		if out != "" {
			out += "; "
		}
		out += strings.TrimSpace(freeHint)
	}
	return out
}

// ComposeStyleHint 导出给 server 层用（2026-10-09 用户：「小说库导入视频制作时，用户需要选择
// 对应的渲染风格，目前所有视频渲染风格都是一样的」）。
//
// 语义与内部一致：把用户勾选的风格预设（可多选组合）+ 自由文本，拼成一条**确定的风格串** ——
// 这条串会写进每集输入的平台自有行「全局风格:」，再由 director.enforceStyleLock 逐镜覆写 plan.Style。
// 也就是说：**用户的选择是权威，不是"提示模型"**（AGENTS.md：能确定性做到的事不要让模型自觉）。
func ComposeStyleHint(styles []string, freeHint string) string {
	return composeStyleHint(styles, freeHint)
}

// UnknownStyleKeys 返回 styles 里平台不认的 key（供如实点名；不静默吞掉用户的选择）。
func UnknownStyleKeys(styles []string) []string {
	var out []string
	for _, k := range styles {
		if _, ok := StylePresets[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

// StylePresetNames 把 key 译成中文显示名（播报用；未知 key 原样返回）。
func StylePresetNames(styles []string) []string {
	var out []string
	for _, k := range styles {
		if p, ok := StylePresets[k]; ok {
			out = append(out, p.Name)
			continue
		}
		out = append(out, k)
	}
	return out
}

// identityLockExcerpt 取卡里 `IDENTITY LOCK` 那一句（"这个人长什么样"的权威原句）。
func identityLockExcerpt(prompt string) string {
	const marker = "IDENTITY LOCK"
	i := strings.Index(prompt, marker)
	if i < 0 {
		return ""
	}
	rest := prompt[i:]
	// 到下一个段落/换行为止（卡里 IDENTITY LOCK 是一整句/一整段）
	if j := strings.IndexAny(rest, "\r\n"); j > 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// PortraitFaceIDClause 定妆照**最后一句**（最高注意力位）再钉一次"这张脸是谁"。
//
// 为什么放最后（与比例锚点同一条设计，见 proportion_clause.go）：模型对提示词末尾的
// 约束最敏感，而中途那些"风格/介质/理想化"的措辞会把它往**一个模板**上拉 ——
// 平台自己的 `humanFaceRenderClause` 就写着 "idealised… simplified, readable features"，
// 对"19 岁瘦长脸 vs 31 岁方脸"这种差别来说是反向的力。
// 所以这里不删风格块（那是画风，该留），而是**用自己的身份锁压住它**：
// 年龄感、骨相、眼型、肤色必须逐项读出来，理想化不等于同一张脸。
//
// selfLock 传该角色**自己**的 IDENTITY LOCK 原句（逐字重述，不改写）。空则返回空串
// （没有可钉的东西，不硬造）。
//
// 【2026-10-10：签名里的 `others []string` 删掉了 —— 这里以前写的是 `NOT: <别人的外形> ×N`】
// 用户报「每个角色的定妆照肩膀上都有三个 Q 型人物」：那段 NOT 列表就是元凶之一
// （真机那张 6827 字的卡里两条是「巴掌大的小人」）。图像侧是 **CFG=1.0 的蒸馏档**：
// **否定词不生效、名词会被当指令画出来**（`artstyle.go` 顶部 / `entity_prompts.go` 文件头）
// ⇒ "列出来 + 说不许画"在图像侧注定只算半个办法。
// 防撞脸不再靠这一句，靠两件确定性的：主体自己的身份锁（本函数）+ 出图后看图判身份
// （`cast_distinct.go`，判撞脸就带 `CastSeparationClause` 换种子重出）。
func PortraitFaceIDClause(selfLock string) string {
	selfLock = strings.TrimSpace(selfLock)
	if selfLock == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\nFACE ID (final check, mandatory): ")
	sb.WriteString("this frame shows ONE specific person — not a face template. ")
	sb.WriteString("The age read, the bone structure (face shape, jaw, cheekbones, brow ridge), the eye shape, " +
		"the brow shape, the skin tone and the fixed markers below must ALL be visible in this single image: \n")
	sb.WriteString("  SELF: " + selfLock + "\n")
	sb.WriteString("idealised does NOT mean interchangeable: do not fall back to a generic good-looking young face.\n")
	sb.WriteString("(中文对照:这一帧是**某一个具体的人**,不是一张通用脸模板 —— 年龄感、骨相(脸型/下颌/颧骨/眉骨)、" +
		"眼型、眉形、肤色与固定记号都必须在这一张里看得出来;理想化不等于可以互换,不许退回通用帅哥脸。)\n")
	return sb.String()
}

// officialPrefixEssentials 官方 per-shot 前缀里**与画风无关**的那部分（见上方 styleExpandFor 的说明）。
//
// 出处：MiniMax H3 官方 `skills/3d-animation-short-generator/references/model-selection.md`
// STEP 7「H3 prompt prefix」：`strong character design language, clean motion, on-brand color palette`。
// 只取这三句：官方那段还带 "Pixar-inspired 3D cartoon rendering / C4D + Octane / Q-version
// proportions" —— 那是**皮克斯风**的介质描述，照抄到国漫二维/写实项目上会直接改掉用户的画风。
const officialPrefixEssentials = "STRONG CHARACTER DESIGN LANGUAGE (the silhouette and costume read instantly), " +
	"CLEAN MOTION (clean, readable movement; no smearing, no strobing), ON-BRAND COLOUR PALETTE (the palette stays " +
	"consistent with the project's approved look)."

// englishSummary 把一段"给模型看的摘要"过一遍**英文可读**闸：清掉汉字之后，
// 至少还剩 minSummaryLetters 个字母才算可读，否则返回空串（宁可不入列）。
//
// 【为什么必须这道闸（2026-10-08 用户「角色渲染的很丑，你看一下资产库」）】追加块统一过
// `project.DropHanRunes` 清汉字（防模型把中文说明整段画到画面上）。而摘要的来源可能是
// **中文 description** ⇒ 清完只剩一个省略号，却照样被拼进提示词：
// 真机三张卡都是 `other cast members of this series: ( …); ( …); ( …)` 与 `NOT: … ×3`。
// 那几段是**空白约束**（占位符还会被模型当画面上要写的字），既没信息又添乱。
func englishSummary(s string) string {
	t := strings.TrimSpace(project.DropHanRunes(s))
	letters := 0
	for _, r := range t {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
		}
	}
	if letters < minSummaryLetters {
		return ""
	}
	return strings.TrimSpace(strings.Trim(t, " ,;、，；。"))
}

// minSummaryLetters 摘要最少要有几个字母才算"英文可读"（判据取自真机：中文摘要清完只剩
// 省略号 —— 0 个字母；正常英文外形摘要动辄几十个）。
const minSummaryLetters = 8

// englishIdentitySummary 从一张卡里取一段**能进提示词的英文外形摘要**（供同剧对比块与身份锁用）。
//
// 取用顺序（先精准后兜底）：
//  1. 卡里的 IDENTITY LOCK 原句（技能侧产物的权威外形句）；
//  2. description（若本身就是英文）；
//  3. **卡自己的英文定妆提示词** —— 小说线/平台直出的卡 description 是中文，而 image_prompt
//     是翻译会话逐项写出来的英文外形（"A woman in her early twenties … lean-toned with a strong
//     是翻译会话逐项写出来的英文外形（"A woman in her early twenties … lean-toned with a strong
//     grip"）。没有这一条，那类卡在对比块里就只剩一个省略号（2026-10-08 真机三张卡全是 `( …)`）。
//     取的时候走平台**已有的**卡正文边界判据 `appearanceExcerpt`（它就是"装配块之前的外观正文"），
//     并先剥掉平台自己的取景抬头（BUST PORTRAIT …）—— 免得把取景/画风/本块自己（递归）读进来。
//     不自己写"取第一句"那种启发式：真机那份卡正文里根本没有 `. `（一路逗号分号），
//     启发式会把整段 5KB 的装配串都吞进来。
//
// 全都拿不到就返回空串（调用方据此**不拼这一段**，而不是拼一个占位符）。
func englishIdentitySummary(ch project.Character) string {
	if s := englishSummary(identityLockExcerpt(ch.ImagePrompt)); s != "" {
		return s
	}
	if s := englishSummary(ch.Description); s != "" {
		return s
	}
	body := appearanceExcerpt(StripPortraitLeadFraming(strings.TrimSpace(ch.ImagePrompt)))
	if body == "" {
		body = appearanceExcerpt(strings.TrimSpace(ch.ImagePrompt))
	}
	if r := []rune(body); len(r) > 200 {
		body = string(r[:200])
	}
	return englishSummary(body)
}

// MediumLabel 介质族的中文说法（**导出**：播报与界面提示都用它，中文名只此一份）。
func MediumLabel(m string) string { return mediumLabel(m) }

// mediumLabel 介质族的中文说法（播报与界面提示用；空串 = 未声明介质，不参与判定）。
//
// 【2026-10-10】实现**只剩这一份**：词表与判定搬进 `internal/project/style_medium.go`
// （那边 `ValidatePlan` 拿得到，用来判 **LLM 自编的 plan.Style** —— 预设 key 这一路只是其中一半）。
// 这里保留函数名（调用方与测试都按它写），内容转发过去，免得同一句话两处各有一张表。
func mediumLabel(m string) string { return project.MediumFamilyLabel(m) }

// MediumOf 预设 key 的介质族（未知 key 或未声明介质 ⇒ 空串）。
func MediumOf(key string) string {
	if p, ok := StylePresets[key]; ok {
		return p.Medium
	}
	return ""
}

// StyleMediumConflicts 勾选的预设里**介质互相矛盾**的组合（返回可直接播报/显示的说明）。
//
// 【为什么要有它（2026-10-09 用户「H3 不是可以多风格组合吗？」）】H3 确实支持把风格写成
// 逗号并列的一句话（官方 `base-en.txt` §4.1 的示例就是 `Live-action, cinematic`），所以平台
// 允许**多选组合**；但平台自己的规则写着风格串**不许含互斥词组**
// （`docs/技能侧改动-电影级口径.md` §检查表：「`no cartoon or anime styling` 与 `SEMI-STYLISED … faces`
// 同时出现 = 自相矛盾」——"负向禁令与自己的正向词打架"是真正的毒药）。
// 一个画面不可能同时是真人实拍、二维动画与水墨：勾出这种组合时平台**仍然照用户选的做**
// （用户拍板优先），但必须**如实点名**，不能等出图变成塑料 CG 脸才让人发现。
//
// 判定刻意**保守**：只对「串里明确写了介质」的预设判定（Medium 非空），题材/调子类
// （cyberpunk / urban_xianxia / film_noir / hongkong）留空 ⇒ 不参与，免得硬猜介质造成误报。
func StyleMediumConflicts(styles []string) []string {
	seen := map[string]string{} // medium → 第一个用它的中文名
	var out []string
	for _, k := range styles {
		m := MediumOf(k)
		if m == "" {
			continue
		}
		name := k
		if p, ok := StylePresets[k]; ok {
			name = p.Name
		}
		if _, dup := seen[m]; dup {
			continue // 同介质重复（比如同时勾国风电影级 + 3D 皮克斯）：不算矛盾
		}
		seen[m] = name
	}
	if len(seen) < 2 {
		return nil
	}
	// 固定顺序输出（map 遍历无序 ⇒ 播报与判据都不稳）
	order := []string{"live", "2d", "3d", "painted", "stopmotion"}
	var parts []string
	for _, m := range order {
		if name, ok := seen[m]; ok {
			parts = append(parts, name+"("+mediumLabel(m)+")")
		}
	}
	if len(parts) < 2 {
		return nil
	}
	out = append(out, strings.Join(parts, " 与 "))
	return out
}
