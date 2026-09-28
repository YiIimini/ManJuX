package pipeline

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// refmanifest.go 渲染期**参考图权威清单**。
//
// 事故(2026-09-25,用户报「角色渲染漂移,看看镜头6主角形象」):
// 第01集镜头6 的参考图实际是 3 张(顾青禾定妆照 + 顾青禾拼合转身图 + 棚内场景),
// 日志可证:「参考图 3 张:角色 2 + 其中拼合转身图 1 + 场景 1」。但 plan 里的
// subject_definitions 写的是「Two pictures are attached in total, one character front
// sheet and the scene」,并把 **<Picture 2> 说成是场景图** —— 于是:
//
//	① 顾青禾自己的拼合转身图被模型当作"场景",retention_analysis 里那一行
//	    `<Picture 2>: weak_reference - scene layout and mood referenced, camera free`
//	    正好落在**她自己的脸上**,模型据此获得"这张脸可以自由重画"的许可;
//	② 真正的场景图(第 3 张)成了无标签的野图;
//	③ 定妆照作为"唯一身份权威"的地位被稀释 —— 成品脸比定妆照更窄更长、眉毛更重、
//	    更年长,而文案里写了、定妆照里没画的细节(右眉尾浅疤、皮筋外的黄铜"禾"扣)
//	    反而被画了出来:模型转去照着**文字**重画,而不是照着那张脸复制。
//
// 根因不是模型不听话,是**提示词在描述它看不见的东西**:张数取决于该角色有没有
// 拼合转身图,而这一步只有渲染器知道(见 RefPlan),LLM 只能猜 —— 猜错就把编号
// 整体错位。refs.go 早已写明「少一张图会让后续 <Picture> 编号整体前移……」,
// 但只在缺图时告警,多一张(多视角开启后每镜必然多)反而没人管。
//
// 修法与既有几处"同一职责收口"一致:**谁装配参考图,谁写编号**。渲染期由
// RefManifest 生成权威清单附在提示词尾部,并明确声明它覆盖上文任何张数/编号的
// 说法;LLM 侧同时被告知不要断言总张数(director 的 sysH3Format)。
// director 的 sysH3Format 与 AssembleShotRefs 的调用点必须成对使用
// EnsureRefManifest(有闸门盯着:TestRefPlanAndManifestArePairedAtEveryCallSite)。

// refManifestKey 幂等判据(改文案时必须保留这个串)。
const refManifestKey = "PICTURE MANIFEST (authoritative"

// refManifestClosingKey 收尾条款的判据串(改文案必须保留这个串,否则闸门与幂等断言都失效)。
const refManifestClosingKey = "PRODUCTION NOTES, NOT PICTURE CONTENT"

// refManifestTailWindow 判据窗口:最终提示词**尾部这么多字符**内不得出现无防护汉字串。
// 与任务书(技能侧《平台侧整改清单》§1.1)的判据同值,闸门见 refmanifest_closing_test.go。
const refManifestTailWindow = 300

// refManifestClosing **整条提示词的最后一段**。
//
// ## 为什么必须有它(2026-09-26 技能侧《平台侧整改清单》§1.1)
//
// 装配顺序是 `ShotRenderPrompt` → `EnsureRefManifest`(见 stages_render.go:245/:261),
// 也就是说清单是**最后一块**;而它每条清单行都写中文名(`<Picture 1> = 翻篇`),它自己
// 还带一段中文对照。官方 ON-SCREEN TEXT 规则的判据是「**没被英文引号括起来的文字,就是
// 模型画成字形的来源**」—— 于是技能侧刚把 `<d>` 外的汉字清到 0(实测 7083 → 0),
// 平台在最后一步又追加一段中文名,后面再无收尾,前半段清洗被最后一段抵消。
//
// 修法必须在**清单之后**(幂等由 refManifestKey 统一守),并且这个收尾本身要能压过
// 尾部窗口:它的长度必须 ≥ refManifestTailWindow,否则清单末尾那一段中文对照仍会落在
// 300 字符窗口内,判据永远红。所以它不是"一句口号",而是把这个窗口填满的一段规则复述
// (顺带也是有用的:画面文字规则写在提示词开头,到这里已经隔了几千字)。
const refManifestClosing = refManifestClosingKey + " (mandatory - this is the last word on the " +
	"subject of visible text): the names and numbers in this manifest are production notes, not visible " +
	"text: never draw, caption or subtitle any of them. Not as a sign, plaque, banner, letter, book cover, " +
	"garment print, prop label or wall writing, and never spoken aloud either. They exist for one reason " +
	"only - so that you can tell which attached picture is which. The frame itself stays free of written " +
	"characters unless the shot explicitly calls for a piece of text and quotes it verbatim in English " +
	"double quotation marks, on the object that carries it. The same goes for every other note, heading, " +
	"list item and bracketed annotation in this prompt: they are addressed to YOU, they are not picture " +
	"content, and anything you letter into the frame from them ruins the shot."

// RefManifest 按实际装配的参考图写出权威清单。roles 为空(无参考图的 t2va)返回空串,
// 调用方原样放行。
func RefManifest(shot project.Shot, roles []RefRole) string {
	if len(roles) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(refManifestKey + " - written by the renderer from the pictures that are ACTUALLY " +
		"attached; it OVERRIDES any picture count or picture number stated anywhere above, because the " +
		"text above was written before the pictures were attached)\n")
	sb.WriteString(fmt.Sprintf("Exactly %d picture(s) are attached, in this order:\n", len(roles)))
	for _, r := range roles {
		switch r.Kind {
		case "character_front":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = %s, the character's FRONT SHEET. This is the identity authority for "+
					"%s: face shape, chin, eyes, brows, nose, skin tone, hair and every mark are copied "+
					"from THIS picture.\n", r.Picture, r.Name, r.Name))
		case "character_angles":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = %s AGAIN — the SAME character's multi-view strip (several angles inside "+
					"ONE picture). It is NOT a scene, NOT a second character and NOT a new subject: it only "+
					"supplies the other viewing angles. Its cells are small, so NEVER take the face from it — "+
					"<Picture %d> wins on every detail.\n", r.Picture, r.Name, r.Picture-1))
		case "character_front_in_strip":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = %s, one multi-view strip whose LEFTMOST cell is that character's front "+
					"sheet. The leftmost cell is the identity authority (face, hair, skin tone, costume and "+
					"marks); the remaining cells only give the other angles. It is NOT a scene and NOT several "+
					"characters.\n", r.Picture, r.Name))
		case "scene":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = %s (the SCENE reference). This is the scene picture and it is the LAST "+
					"picture: background layout, props and lighting come from it, the camera stays free.\n",
				r.Picture, r.Name))
		case "character_chibi":
			// 内心活动镜（技能侧 2026-09-27 口径）：画面里演出这段心思的是**这个人的 Q 版形象**，
			// 本人不在画面里。这里必须写死这两点 —— 否则模型会把 Q 版当"第二个角色"再画一个本人。
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = the CHIBI (Q-version) figure of %s — the SAME character, drawn in chibi "+
					"proportions (about 2.5–3 heads tall, big head, clear silhouette). This figure performs "+
					"the inner monologue in this shot, and %s's own live-action body is NOT in this frame: "+
					"draw ONE figure, the chibi, and keep its signature brows, eyes and marks.\n",
				r.Picture, r.Name, r.Name))
		case "system_bot":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = the system's companion CHIBI ROBOT (%s). It is a MACHINE — not a person, "+
					"not the speaking character, and not a second human. Keep its capsule body, single-eye "+
					"visor, antenna light and white-porcelain shell with glowing seams.\n", r.Picture, r.Name))
		case "system_panel":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = the floating HOLOGRAPHIC PANEL (%s, a prop). This is the system interface: "+
					"a thin suspended slab with a glowing rim and faint scan lines. Its interior stays BLANK — "+
					"never letter any text, digits, icons or UI glyphs into it (on-screen text is forbidden).\n",
				r.Picture, r.Name))
		}
	}
	// 结束语按**本镜实际有没有拼合图**定制:没有拼合图时还提它,只会让模型去找一张不存在的图。
	hasStrip := false
	for _, r := range roles {
		if r.Kind == "character_angles" || r.Kind == "character_front_in_strip" {
			hasStrip = true
			break
		}
	}
	sb.WriteString("Use these numbers exactly as written. Never renumber them, never merge two pictures into " +
		"one subject, and never read a picture as something this manifest does not call it. If a sentence " +
		"above claims a different total")
	if hasStrip {
		sb.WriteString(", or calls a multi-view strip a second character or a scene")
	} else {
		sb.WriteString(", or treats the scene picture as a character")
	}
	sb.WriteString(" — that sentence is WRONG and this manifest is right.\n")
	sb.WriteString("(中文对照:下面是**渲染器按实际附上的图片**写出的编号清单,它**覆盖**上文任何关于张数/" +
		"编号的说法。")
	if hasStrip {
		sb.WriteString("多视角拼合图是**同一个角色**的另一组角度,不是场景、不是第二个角色,也不得当作" +
			"“取脸来源”(脸一律以定妆照为准)。")
	} else {
		sb.WriteString("本镜只附了定妆照与场景图:脸一律以定妆照为准,场景图只供背景与光线,不含角色。")
	}
	sb.WriteString(")\n")
	if shot.ID > 0 {
		sb.WriteString(fmt.Sprintf("(shot %d)\n", shot.ID))
	}
	// 最后一段:清单的中文名与中文对照都在这之前,收尾必须在它们**之后**(见 refManifestClosing)。
	sb.WriteString("\n" + refManifestClosing + "\n")
	return sb.String()
}

// EnsureRefManifest 把权威清单接到提示词尾部,并**把 subject_definitions 里的每个角色
// 绑到它实际那张图上**(幂等:已带过就不再叠)。
//
// 【为什么要绑】(2026-09-26 真机实测:换风格后成片男主的脸仍与定妆照对不上)
// MiniMax 官方的**全参考模式**格式(ref-en.txt §2.1/§2.2)写得非常明确:
//
//	<Subject 1> is the young woman **in <Picture 1>**, with long dark hair, …
//	"If an image is used only to define a character, scene, costume, or style,
//	 do not create a standalone picture entry. Instead, cite the image source
//	 INSIDE the corresponding <Subject N> definition."
//
// 而我们的 subject_definitions 只写 `<Subject 1> is 沈砚（human）— 十八九岁…以附图为准`,
// **没有把任何一张图绑到这个 subject 上**("附图"是泛指)。模型于是按**文字**重画:
// 同一个沈砚在不同镜里成了不同的人(戴抹额的公子 / 十五六岁的少年 / 长刘海青年),
// 而定妆照只用上了它的"风格" —— 这正是用户反复报的"角色漂移"。
// 修复不是再喊一遍"照参考图",而是补上官方那句 `in <Picture K>`。
//
// 位置与画面文字规则、音频标记一致放在尾部:官方要求的六段结构(subject_definitions →
// summary → retention_analysis → detailed_description → overall_soundscape →
// non_diegetic_music)保持不变,清单作为一段结构性声明并列其后。
func EnsureRefManifest(prompt string, shot project.Shot, roles []RefRole) string {
	if len(roles) == 0 {
		return prompt
	}
	if strings.Contains(prompt, refManifestKey) {
		return prompt
	}
	bound := bindSubjectsToPictures(prompt, roles)
	return strings.TrimRight(bound, "\n") + "\n\n" + RefManifest(shot, roles)
}

// reSubjectLine 匹配 subject_definitions 里的一行,捕获编号与 `is` 之后的**前缀**。
//
// 前缀放宽到「is 之后到行尾/换行」的整段(不再在第一个逗号/句点/括号处截断):
// 官方句式要求 `<Subject N> is <角色名>`,但 LLM 会写成
// `<Subject 1> is a slim seventeen-year-old boy named 沈砚（human）— …`
// 这类自然句 —— 旧判据把整段当名字去精确比对,必然查不到 → 原样返回、不绑图、
// 不打日志,于是"补官方句式"这条修复(2026-09-26 真机根因修复)静默失效,回到
// 「模型按文字重画、同一个沈砚每镜不同人」。名字的定位改由 nameInSubjectTail 做。
var reSubjectLine = regexp.MustCompile(`(?m)^<Subject (\d+)>\s+is\s+(.+)$`)

// bindSubjectsToPictures 给 subject_definitions 里每个角色行补上它**实际那张图**的编号:
//
//	`<Subject 1> is 沈砚（human）— …`      →  `<Subject 1> is 沈砚 in <Picture 1>（human）— …`
//	`<Subject 1> is a slim boy named 沈砚…` → `<Subject 1> is a slim boy named 沈砚 in <Picture 1>…`
//
// 编号一律从 roles(渲染器按实际装配顺序给出)里取,**不猜不造**;名字对不上的行原样保留
// (对不上说明它讲的不是这些参考图里的对象,硬塞编号比不绑更坏)。已经有 "in <Picture"
// 的行不动(幂等)。
func bindSubjectsToPictures(prompt string, roles []RefRole) string {
	auth := authorityPictures(roles)
	if len(auth) == 0 {
		return prompt
	}
	return reSubjectLine.ReplaceAllStringFunc(prompt, func(line string) string {
		m := reSubjectLine.FindStringSubmatch(line)
		if m == nil {
			return line
		}
		num, tail := m[1], m[2]
		if strings.Contains(tail, "in <Picture") {
			return line // 已绑过
		}
		name, pic, ok := nameInSubjectTail(tail, auth)
		if !ok {
			return line
		}
		// 在名字之后插入官方句式,名称与其余描述原样保留(模型要在下文按名字继续指代)。
		i := strings.Index(tail, name)
		return "<Subject " + num + "> is " + tail[:i] + name + " in <Picture " + strconv.Itoa(pic) + ">" + tail[i+len(name):]
	})
}

// authorityPictures 名字 → 该对象的"身份权威"图片编号。拼合图不给编号:官方明确它只
// 提供角度,而让它当身份源正是 2026-09-25 镜头6 漂移的根因。
//
// **名字要连别名一起登记**（2026-09-28 用户报「subject_definitions 一行都没能绑到参考图」）：
// 角色卡名常带括号注解（本机实例：`碑生（丙十七）`、`沈砚（human）`），而 LLM 在
// subject_definitions 里往往只写其中一半 —— 实测写的是 `丙十七`，整名对不上 ⇒ 一行都不绑
// ⇒ 模型按文字重画 ⇒ 同一角色逐镜不同人（正是这条修复本该拦住的那个后果）。
// 展开成「整名 / 括号前主名 / 括号内注解」三种写法；命中仍按**最长者胜**
// （nameInSubjectTail），短别名抢不走长名。
func authorityPictures(roles []RefRole) map[string]int {
	auth := map[string]int{}
	for _, r := range roles {
		switch r.Kind {
		case "character_front", "character_front_in_strip", "scene":
			for _, alias := range nameAliases(r.Name) {
				if _, ok := auth[alias]; !ok {
					auth[alias] = r.Picture
				}
			}
		}
	}
	return auth
}

// nameAliases 一个角色名的全部写法：整名、括号前的主名、括号里的注解。
//
// 只做**去括号**这一种展开（不做分词、不做模糊匹配）：它确定、可穷举、也不会把两个真不同的
// 角色混成一个。`碑生（丙十七）` → {碑生（丙十七）, 碑生, 丙十七}；没有括号时只有整名。
//
// 单字别名一律丢掉（`甲`／`乙` 这种太容易在描述正文里撞上）；括号在开头时整名照用、不展开。
//
// 一律按 **rune** 切（第一版按字节切，`name[i+1:]` 把全角括号切成半个字，展开出的是乱码别名——
// 于是"提示词只写注解"那条仍然一个都绑不上，被测试当场抓住）。
func nameAliases(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	out := []string{name}
	r := []rune(name)
	open := -1
	for i, ch := range r {
		if ch == '（' || ch == '(' {
			open = i
			break
		}
	}
	if open <= 0 {
		return out
	}
	if main := strings.TrimSpace(string(r[:open])); len([]rune(main)) >= 2 {
		out = append(out, main)
	}
	inner := r[open+1:]
	for i, ch := range inner {
		if ch == '）' || ch == ')' {
			inner = inner[:i]
			break
		}
	}
	if s := strings.TrimSpace(string(inner)); len([]rune(s)) >= 2 {
		out = append(out, s)
	}
	return out
}

// subjectNameWindow 「is」之后多长的窗口内认名字(字符数)。
//
// 放宽到整行之后必须给一个窗口:名字之外的前半句是自然语言("a slim
// seventeen-year-old boy"),窗口太小会漏、太大则可能撞上后文里提到的别人。
const subjectNameWindow = 40

// nameInSubjectTail 在 `is` 之后的**前缀**里做包含式名字匹配,命中最长者胜。
//
// 为什么要包含式而不是整段精确相等:句子由 LLM 写,官方句式只要求
// `<Subject N> is [full description of the character]`,完全可能写成
// "…is a slim seventeen-year-old boy named 沈砚, …"。旧实现把 is 与第一个逗号/括号
// 之间整段当名字去查表,这类写法必然 miss → 不绑图、不打日志,于是"补官方句式"这条
// 修复静默失效,回到"同一个沈砚每镜不同人"。
func nameInSubjectTail(tail string, auth map[string]int) (string, int, bool) {
	r := []rune(tail)
	if len(r) > subjectNameWindow {
		r = r[:subjectNameWindow]
	}
	window := string(r)
	bestName, bestPic, bestLen := "", 0, -1
	for name, pic := range auth {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if !strings.Contains(window, name) {
			continue
		}
		if n := len([]rune(name)); n > bestLen {
			bestName, bestPic, bestLen = name, pic, n
		}
	}
	if bestLen < 0 {
		return "", 0, false
	}
	return bestName, bestPic, true
}

// subjectBindStats 数这份提示词里"能绑上图的 subject 行 / subject 行总数"。
//
// 用途只有一个:让"一行都没绑上"这件事**可见**。绑定失败时提示词照旧附清单、日志
// 照旧正常,属于最难发现的一类回归(2026-09-26 真机实测过:换风格后男主脸仍与定妆照
// 对不上,根因就是没绑)。判据与 bindSubjectsToPictures 共用 nameInSubjectTail。
//
// 调用方须传**绑定之前**的提示词(绑过之后每行都含 in <Picture,统计会失真)。
func subjectBindStats(prompt string, roles []RefRole) (bound, total int) {
	auth := authorityPictures(roles)
	if len(auth) == 0 {
		return 0, 0
	}
	for _, m := range reSubjectLine.FindAllStringSubmatch(prompt, -1) {
		total++
		if strings.Contains(m[2], "in <Picture") {
			bound++
			continue
		}
		if _, _, ok := nameInSubjectTail(m[2], auth); ok {
			bound++
		}
	}
	return bound, total
}
