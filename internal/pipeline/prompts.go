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
func anchorPrompt(plan *project.Plan, shot *project.Shot) string {
	type named struct{ name, desc string }
	var chars, scenes []named
	for _, cid := range shot.Characters {
		for _, ch := range plan.Characters {
			if ch.ID == cid && strings.TrimSpace(ch.Description) != "" {
				chars = append(chars, named{ch.Name, strings.TrimSpace(ch.Description)})
			}
		}
	}
	for _, sc := range plan.Scenes {
		if sc.ID == shot.Scene && strings.TrimSpace(sc.Description) != "" {
			scenes = append(scenes, named{sc.Name, strings.TrimSpace(sc.Description)})
		}
	}
	if len(chars) == 0 && len(scenes) == 0 {
		return shot.H3Prompt
	}
	var sb strings.Builder
	sb.WriteString("【身份锚定(全片统一,优先级最高)】\n")
	if len(chars) > 0 {
		sb.WriteString("本镜出场人物 —— 参考图里出现的人就是他们本人(同一角色可能有两张参考图:" +
			"**锐利的正面定妆照(脸的唯一权威)** + 横向拼合的转身图;转身图的左起第一格就是同一张正面照、" +
			"其余格是同一个人的其它角度,**它不是第二个角色**):\n")
		for _, c := range chars {
			fmt.Fprintf(&sb, "- 「%s」\n", c.name)
		}
		sb.WriteString("身份锁:画面中每个人物的脸型/五官/发型发色/肤色/年龄感/体型必须**照抄参考图里对应的那个人**," +
			"逐项一致 —— 参考图拍的就是这个人物本人,不是风格参考;脸部细节(脸型/眼睛/眉毛/发型长度与形状)" +
			"**以正面定妆照为准**;不得按文字重画、不得美化/年轻化/换成更俊美的版本、" +
			"不得把多人的脸平均化成同一张脸。\n" +
			"禁止添加参考图里没有的面部标记:痣、疤、胎记、腮红红晕、红鼻头、夸张大眼睛、额外的胡须与首饰,一律不得无中生有;" +
			"反过来,参考图里有的特征一个都不能丢(发型/发饰/服饰细节同样照抄)。\n" +
			"下面的文字描述只用于「服装/道具/姿态/情绪/台词」,不得用来改写长相:\n")
		for i, c := range chars {
			fmt.Fprintf(&sb, "%d. %s:%s\n", i+1, c.name, normalizeUniformWording(c.desc))
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
	if strings.TrimSpace(plan.Style) != "" {
		sb.WriteString("【全片视觉风格】" + strings.TrimSpace(plan.Style) + "\n")
	}
	return sb.String() + "\n" + shot.H3Prompt
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

func styleExpand(style string) string {
	if strings.TrimSpace(style) == "" {
		return ""
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
	switch {
	case has3D:
		// 注意:**风格块绝不能改写面部特征**。此前这里写着 "large glossy stylized eyes",
		// 与角色卡的面部描述(如"细长单眼皮")直接打架 —— 定妆照被风格"重新设计"成圆软脸
		// 大眼,而视频按文字走,两边永远对不上(2026-09-12 用户报一致性丢失,根因即此)。
		// 风格只描述材质与渲染方式(表面/光泽/媒介),形状类词汇一律不写。
		terms = append(terms, "next-generation 3D CG character render, BJD ball-jointed doll aesthetic, smooth poreless synthetic skin, collectible virtual figure look")
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
		terms = append(terms, "photorealistic cinematic portrait, realistic skin texture") // 真人剧是合法风格
		stylized = false
	default:
		return "" // 无已知关键词:不强行改写用户自定义风格
	}
	if hasQ {
		terms = append(terms, "cute chibi proportions")
	}
	tail := ". Render strictly in this exact art style."
	if stylized {
		tail = ". The output must read as a stylized character render in this exact art style."
	}
	return "\n\nRENDER STYLE (mandatory): " + strings.TrimSpace(style) + " — " + strings.Join(terms, "; ") + tail +
		identityFidelityBlock()
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
// Krea-2 提示词尾部,强制与同剧角色拉开面部差异(2026-09-11 用户定调
// 「不同角色脸不能相似」)。实证:LLM 已写了 fat/scar/narrow face 等
// 差异化特征,但逐字相同的巨型风格前缀+模型帅哥先验把特征吞掉(肥胖
// →清瘦、疤→无疤)。生成时给模型看全剧班底,它才知道"要和谁不同"。
// 注入的提示词随资产入库,库内重生成/跨集复用自带对比上下文。
func castContrastBlock(plan *project.Plan, selfName string) string {
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
	return "\n\nCAST DISTINCTNESS (reference note, NOT to be drawn) — other cast members of this series: " + strings.Join(parts, "; ") +
		". They are listed ONLY so that the character depicted here (「" + self + "」) is clearly distinguishable from every one of them: " +
		"different face shape, age, build, skin tone and unique markers; these identity traits MUST dominate the image; " +
		"never average them into a generic handsome/pretty face. " +
		// 实测(2026-09-12):不点明"不要画出来"时,模型会把名单里的其他角色当画面元素塞进角落
		// (郑伯的定妆照里出现了小尺寸的卫东与 Q 版江砚)。名单只作差异化参考:画面里只能有本人一个。
		"Do NOT depict any of these other characters: exactly ONE person appears in the frame — 「" + self + "」."
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
