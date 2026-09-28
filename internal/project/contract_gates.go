package project

import (
	"fmt"
	"strings"
)

// contract_gates.go 契约门的**第二批**（2026-09-28 用户："这些给我留着干嘛？"）。
//
// 本批只收**有明确出处**的判据，出处逐条写在各自函数头：
//
//	① 切镜动词白名单 —— 官方 H3 技能 `ref-en.txt` §4.2 的五个 verbs
//	② 三视图板 15 格 —— 平台自己的 `BoardLayoutClause`（3 全身 + 6 头 + 6 细节）
//	③ 场景四要素 —— 用户《四、场景提示词指令》:63「四大要素：环境类型、具体时间、空间氛围、视觉主要特征」
//	④ 场景视角九选一 —— 用户《四》＋平台 `PlanViews`（场景是每一镜的空间底座，建立镜头必须说从哪看）
//	⑤ 屏幕文字引号 —— 官方硬约束 `sysHard__HARD_CONSTRAINTS.md`「Screen-visible text must be
//	   quoted exactly and kept in original language」
//
// **有一条我拒绝加**（记在这里免得下次又被人当需求塞进来）：审计清单里的"Q 版四表情行"。
// 用户 11 份参考文件里**没有任何一处**要求 Q 版板带表情行 —— 只有《二》里一句
// 「表情：面部平静，通过眉眼传递气质」，那是**定妆照**的要求。没有出处的"需求"不写进契约，
// 否则就是拿我自己的想象去改用户的成品口径（`prop_refs.go` 那种"清单里有个不存在的文件名"
// 已经栽过一次）。

// ── ① 切镜动词白名单 ────────────────────────────────────────────────────────

// PlanCutVerbs 官方 §4.2 的五个切镜写法（**逐字**，不许自创）。
var PlanCutVerbs = []string{
	"the camera cuts to",
	"the shot cuts to",
	"the shot transitions to",
	"the shot changes to",
	"the shot switches to",
}

// cutIntentPhrases 能表达"切镜"意图的**搭配**（不是裸词根）。
//
// 为什么不用裸词根：`cut` 也出现在动作描写里（"a long cut on his hand bleeds"），
// 裸 `cut` 一律命中就等于把正常描写判成违规。真正的切镜是**带方向的搭配**：
// `cut to` / `cuts closer`（后者正是规范点名不许用的写法 —— 只改距离该用运镜）。
var cutIntentPhrases = []string{
	"cut to", "cuts to", "cutting to",
	"cuts tighter", "cuts closer", "cut tighter", "cut closer",
	"transition to", "transitions to", "transitioning to",
	"switch to", "switches to", "switching to",
	"jumps to", "jump to", "jumping to", "cuts away", "cut away",
}

// CutVerbIssues 找出"用自创写法切镜"的镜（返回镜号）。
//
// 官方 §4.2 给了五个写法，并且明说 CUTS 有**自己的词表** —— 模型爱写
// "the scene jumps to" / "we cut to" / "the camera cuts closer"，这些 H3 不认
// （`cuts closer` 尤其典型：规范明说"只改距离或轻微角度时该用**运镜**，不要切"）。
func CutVerbIssues(shots []Shot) []string {
	var out []string
	for _, sh := range shots {
		low := strings.ToLower(sh.H3Prompt)
		// **按 `[Shot N]` 分段判**：官方五个写法出现在**它自己那一段**里就算合规。
		// 不能按整篇判 —— 一篇里完全可以既有合规切镜、又有自创写法。
		for _, seg := range strings.Split(low, "[shot ") {
			if hasAnyFold(seg, PlanCutVerbs) {
				continue // 这一段用了官方写法
			}
			for _, p := range cutIntentPhrases {
				if strings.Contains(seg, p) {
					out = append(out, fmt.Sprintf("#%d「%s」", sh.ID, p))
					break
				}
			}
		}
	}
	return out
}

// ── ② 三视图板 15 格 ─────────────────────────────────────────────────────────

// PlanBoardCells 三视图板的格数构成（平台 `BoardLayoutClause` 的实现口径）。
const (
	PlanBoardFullBodyCells = 3 // LEFT：FRONT / LEFT PROFILE / BACK
	PlanBoardHeadCells     = 6 // UPPER RIGHT：六个头部角度
	PlanBoardDetailCells   = 6 // LOWER RIGHT：六个服饰/材质细节格
)

// BoardCellIssues 板提示词有没有写全三段格数（空 = 没问题）。
//
// 为什么值得一道门：板子是**一张规格书**，少一段就等于少一类身份信息 ——
// 出图模型不会因为"你没写"而少画，它会**把少了的那段用别的东西填满**
// （实测常见：把 6 个细节格画成 6 张侧脸，于是板子变成"9 张脸的横条"）。
func BoardCellIssues(prompt string) []string {
	low := strings.ToLower(prompt)
	var miss []string
	for _, c := range []struct{ want, label string }{
		{"three full-body", "三个全身（正面/侧面/背面）"},
		{"six head", "六个头部角度"},
		{"six detail", "六个服饰材质细节格"},
	} {
		if !strings.Contains(low, c.want) {
			miss = append(miss, c.label)
		}
	}
	return miss
}

// ── ③ 场景四要素 ─────────────────────────────────────────────────────────────

// PlanSceneTimeWords 场景卡必须点名的**具体时间**词（用户《四》四要素之二）。
var PlanSceneTimeWords = []string{
	"dawn", "sunrise", "morning", "noon", "midday", "afternoon", "dusk", "sunset",
	"twilight", "evening", "night", "midnight", "golden hour", "blue hour", "daylight",
}

// PlanSceneMoodWords 场景卡必须给出的**空间氛围**词（四要素之三）。
var PlanSceneMoodWords = []string{
	"atmosphere", "mood", "air of", "hush", "silence", "solemn", "oppressive", "serene",
	"desolate", "mournful", "tense", "eerie", "sacred", "quiet", "stillness", "dread",
}

// PlanSceneFeatureWords 场景卡必须给出的**视觉主要特征**（四要素之四）。
var PlanSceneFeatureWords = []string{
	"texture", "colour", "color", "material", "weathered", "cracked", "carved", "pattern",
	"detail", "surface", "tone", "contrast", "patina", "moss", "rust",
}

// SceneElementIssues 场景卡缺四要素里的哪几项（空 = 没问题）。
//
// 用户《四、场景提示词指令》:63「**四大要素：环境类型、具体时间、空间氛围、视觉主要特征**」。
// 平台此前只在装配点补齐了"无人 + 事件隔离"那两句，四要素**一条判据都没有**。
func SceneElementIssues(scenes []Scene) []SceneIssue {
	var out []SceneIssue
	for _, sc := range scenes {
		text := strings.ToLower(sc.Description + " " + sc.ImagePrompt)
		var miss []string
		if len(matchWords(text, PlanSceneTimeWords)) == 0 {
			miss = append(miss, "具体时间")
		}
		if len(matchWords(text, PlanSceneMoodWords)) == 0 {
			miss = append(miss, "空间氛围")
		}
		if len(matchWords(text, PlanSceneFeatureWords)) == 0 {
			miss = append(miss, "视觉主要特征")
		}
		if len(miss) > 0 {
			out = append(out, SceneIssue{Kind: SceneMissingElement, Name: sc.Name, Words: miss})
		}
	}
	return out
}

// ── ④ 场景视角九选一 ─────────────────────────────────────────────────────────

// SceneViewIssues 场景卡没写九视角词表里任何一条的（空 = 没问题）。
//
// 用户《四》＋官方视角表：场景是**每一镜的空间底座**，建立镜头必须说清"从哪看"。
// `PlanViewIn` 早就在了（分镜侧一直在用），但**场景卡从来没有走过这道判据** ——
// 于是板子上可以一个视角词都没有，渲染时全靠模型自己猜一个（每集还不一样）。
func SceneViewIssues(scenes []Scene) []string {
	var out []string
	for _, sc := range scenes {
		// **必须小写归一**：`PlanViewIn` 是逐字包含匹配，而真实场景卡的视角串是**全大写**
		// （技能侧写的就是 `ULTRA-WIDE 45-DEGREE AERIAL VIEW of …`）—— 不归一会把每一张
		// 写了视角的卡都判成"没写"（第一版就是这样，夹具上当场误报）。
		if _, ok := PlanViewIn(strings.ToLower(sc.ImagePrompt + " " + sc.Description)); !ok {
			out = append(out, sc.Name)
		}
	}
	return out
}

// ── ⑤ 屏幕文字引号 ───────────────────────────────────────────────────────────

// PlanScreenTextWords 会让人"读上面的字"的画面物件（官方 ON-SCREEN TEXT 规则管的就是它们）。
//
// ⚠️ 判据**不能用裸 Contains**：`sign` 是 `signature`（定妆照的 "signature marks"）、
// `design`、`assignment` 的子串 —— 第一版就是这么栽的，夹具里一句 signature marks
// 被判成"画面里有要读的字"。见 screenTextNear 的词边界处理。
var PlanScreenTextWords = []string{
	"sign", "signage", "banner", "plaque", "label", "notice", "poster", "inscription",
	"stele", "tablet", "chalkboard", "signboard",
}

// ScreenTextQuoteIssues 找出"画面里要读的字没加引号"的镜（返回镜号）。
//
// 官方硬约束：「Screen-visible text must be quoted exactly and kept in original language」——
// 引号是**给渲染器的信号**：引号里的内容是要**逐字出现**的文字，不是画面形容。
// 不引的结果实测是"模型自己编一段伪汉字"（`sysH3Format` 那条中文卫生门管的是另一件事：
// 提示词里不该有裸中文；这条管的是**该有的那几句中文有没有按规矩写**）。
func ScreenTextQuoteIssues(shots []Shot) []string {
	var out []string
	for _, sh := range shots {
		low := strings.ToLower(sh.H3Prompt)
		if !screenTextNear(low) {
			continue
		}
		// 画面上确实有要读的字了 —— 那正文里**必须**出现成对引号包着的原文
		// （中英文引号都认：英文技能侧写 "..."，技能侧中文卡写「…」）
		if strings.ContainsAny(sh.H3Prompt, "\u201C\u201D") || strings.Contains(sh.H3Prompt, "「") ||
			strings.Count(sh.H3Prompt, "\"") >= 2 {
			continue
		}
		out = append(out, fmt.Sprintf("#%d", sh.ID))
	}
	return out
}

// screenTextNear 文本里有没有"要读的字"的物件，**按词边界判**。
//
// 裸 Contains 会把 `signature marks`（定妆照口径）、`design`、`assignment` 全判成 `sign` ——
// 第一版就是这样在夹具上误报的。这里要求命中词的**前后都不是字母**。
func screenTextNear(low string) bool {
	for _, w := range PlanScreenTextWords {
		idx := 0
		for {
			i := strings.Index(low[idx:], w)
			if i < 0 {
				break
			}
			at := idx + i
			before := byte(' ')
			if at > 0 {
				before = low[at-1]
			}
			after := byte(' ')
			if at+len(w) < len(low) {
				after = low[at+len(w)]
			}
			if !isASCIILetter(before) && !isASCIILetter(after) {
				return true
			}
			idx = at + len(w)
		}
	}
	return false
}

// isASCIILetter 只把 a–z 当"字母"：中文与标点都算边界（判据要能在中英混排里工作）。
func isASCIILetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// EmotionShotIssues 承载**核心情绪**的镜头时长不够的（返回人话，空 = 没问题）。
//
// 出处（用户文件，三份都在说同一件事）：
//
//	《【额外】测试版30S分镜指令》：「承载核心情绪反应的镜头（特写面部微表情、沉默对峙、
//	  泪目、咬牙、握拳颤抖等），其镜头时长不得少于 **5 秒**。这类镜头是"戏眼"，
//	  快切会直接消解情感浓度。宁愿压缩前面的动作铺垫，也要给足情绪发酵时间。」
//	《五、最新豆包十秒指令》：「使用慢动作运镜，保持静止镜头聚焦说话者面部微表情，
//	  延长环境氛围渲染」（同一诉求的另一种说法）
//
// 识别方式：钩子类型（`tender` / `climax`）+ 景别（特写/近景）。
// **不靠正文里有没有"泪目"这类词**去猜 —— 那是模型写不写的问题，不是镜头语言的问题。
func EmotionShotIssues(p *Plan) []string {
	if p == nil {
		return nil
	}
	var ids []string
	for _, sh := range p.Shots {
		if !isEmotionShot(sh) {
			continue
		}
		if float64(sh.Duration) < PlanEmotionMinSeconds {
			ids = append(ids, fmt.Sprintf("#%d(%ds)", sh.ID, sh.Duration))
		}
	}
	return ids
}

// isEmotionShot 这一镜是不是"戏眼"（核心情绪镜）。
func isEmotionShot(sh Shot) bool {
	h := strings.ToLower(strings.TrimSpace(sh.HookType))
	emotionHook := false
	for _, t := range PlanEmotionHookTypes {
		if h == t {
			emotionHook = true
			break
		}
	}
	// 情绪钩子 + 小景别（脸在画面里）才算戏眼：一个 `climax` 的远景是"事件"，
	// 不是"情绪" —— 远景本来就该短，要求它 ≥5 秒反而会拖节奏。
	near := false
	for _, s := range PlanFaceCloseSizes {
		if strings.Contains(sh.ShotSize, s) {
			near = true
			break
		}
	}
	return emotionHook && near
}

// hasAnyFold 文本里是否含列表里任一项（忽略大小写；文本须已小写）。
func hasAnyFold(low string, words []string) bool {
	for _, w := range words {
		if strings.Contains(low, strings.ToLower(w)) {
			return true
		}
	}
	return false
}
